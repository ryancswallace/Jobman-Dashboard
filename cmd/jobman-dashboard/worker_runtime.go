package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/control"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
	"github.com/ryancswallace/jobman-dashboard/internal/observability"
	"github.com/ryancswallace/jobman-dashboard/internal/reports"
	"github.com/ryancswallace/jobman-dashboard/internal/runtimeconfig"
	"github.com/ryancswallace/jobman-dashboard/internal/store"
)

// The API role never loads provider signing material or launches background
// work. Combined serve remains explicit compatibility behavior.
func validateAPIMode(c config.Config) error {
	if len(c.Notifications.APNs) != 0 {
		return errors.New("api mode cannot contain APNs signing credentials; configure a delivery worker")
	}
	return nil
}

type workerSecrets struct {
	observations     *runtimeconfig.ProcessObservations
	databaseURL      string
	sources          []control.Config
	brokers          map[string]logs.ClientConfig
	logCursorKey     []byte
	redaction        *reports.RedactionPolicy
	companionVersion string
	notifications    *notificationRuntime
	policy           *notifications.DevicePolicy
}

func loadWorkerRuntime(c config.WorkerConfig) (result workerSecrets, err error) {
	defer func() {
		if err != nil {
			result.observations.Close()
			result.notifications.Close()
		}
	}()
	ids := make([]string, len(c.Controls))
	for i, source := range c.Controls {
		ids[i] = source.ID
	}
	result.observations, err = runtimeconfig.NewObservations(c.Observability, "worker", c.ConfigurationRevision, ids, "")
	if err != nil {
		return result, err
	}
	result.databaseURL, err = databaseSecret(c.DatabaseURLFile)
	if err != nil {
		return result, fmt.Errorf("worker database: %w", err)
	}
	// A retention-only process uses deployment IDs, never source credentials.
	network := c.Has(config.WorkerIngestion) || c.Has(config.WorkerNotifications) || c.Has(config.WorkerDelivery) || c.Has(config.WorkerReports)
	if network {
		for _, source := range c.Controls {
			loaded, e := runtimeconfig.Source(source)
			if e != nil {
				return result, fmt.Errorf("worker Control trust/key material: %w", e)
			}
			loaded.ActorMode = auth.DelegationWorker
			result.sources = append(result.sources, loaded)
		}
	}
	if c.Has(config.WorkerReports) {
		build, ok := debug.ReadBuildInfo()
		if !ok {
			return result, errors.New("diagnosis requires embedded dependency version information")
		}
		result.companionVersion, err = companionBuildVersion(build)
		if err != nil {
			return result, err
		}
		result.redaction, err = loadReportPolicy(c.Reports, nil)
		if err != nil {
			return result, err
		}
		result.brokers = map[string]logs.ClientConfig{}
		for _, broker := range c.LogBrokers {
			loaded, e := runtimeconfig.Broker(broker)
			if e != nil {
				return result, fmt.Errorf("worker broker trust/key material: %w", e)
			}
			loaded.ActorMode = auth.DelegationWorker
			result.brokers[broker.ID] = loaded
		}
		if len(c.LogBrokers) > 0 {
			result.logCursorKey, err = loadLogCursorKey(c.LogCursorKeyFile, nil)
			if err != nil {
				return result, err
			}
		}
	}
	if c.Has(config.WorkerDelivery) {
		result.notifications, err = loadNotificationRuntime(config.Config{Notifications: c.Notifications, Events: config.Events{Enabled: true}}, nil)
		if err != nil {
			return result, err
		}
		result.policy = result.notifications.policy
	} else if len(c.Notifications.DeviceTopics) > 0 {
		topics := make([]notifications.DeviceTopic, len(c.Notifications.DeviceTopics))
		for i, v := range c.Notifications.DeviceTopics {
			topics[i] = notifications.DeviceTopic{Topic: v.Topic, Environment: v.Environment}
		}
		result.policy, err = notifications.NewDevicePolicy(topics)
		if err != nil {
			return result, errors.New("worker device topic policy is invalid")
		}
	}
	return result, nil
}

func combinedWorkerConfig(c config.Config) config.WorkerConfig {
	components := []config.WorkerComponent{config.WorkerRetention}
	if c.Events.Enabled {
		components = append(components, config.WorkerIngestion, config.WorkerNotifications)
	}
	if len(c.Notifications.APNs) > 0 {
		components = append(components, config.WorkerDelivery)
	}
	if c.Reports.ObjectRoot != "" {
		components = append(components, config.WorkerReports)
	}
	return config.WorkerConfig{ConfigurationRevision: c.ConfigurationRevision, DatabaseURLFile: c.DatabaseURLFile, Components: components, IdentityIssuer: c.OIDC.Issuer, Controls: c.Controls, DeliveryHold: c.Events.DeliveryHold, Reports: c.Reports, LogBrokers: c.LogBrokers, LogMappings: c.LogMappings, LogCursorKeyFile: c.LogCursorKeyFile, Notifications: c.Notifications}
}

// Resources are fully constructed before any runner starts. Close first cancels
// and joins every runner, then closes transports/object handles before the DB.
type backgroundRuntime struct {
	runners []func(context.Context)
	cleanup []func()
	cancel  context.CancelFunc
	group   sync.WaitGroup
}

func (b *backgroundRuntime) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	b.cancel = cancel
	for _, run := range b.runners {
		b.group.Go(func() { run(ctx) })
	}
}
func (b *backgroundRuntime) Close() {
	if b.cancel != nil {
		b.cancel()
		b.group.Wait()
	}
	for i := len(b.cleanup) - 1; i >= 0; i-- {
		b.cleanup[i]()
	}
	b.cleanup = nil
}

func prepareBackground(c config.WorkerConfig, material workerSecrets, db *store.Store) (result *backgroundRuntime, err error) {
	result = &backgroundRuntime{}
	defer func() {
		if err != nil {
			result.Close()
		}
	}()
	var observer *observability.Registry
	if material.observations != nil {
		observer = material.observations.Registry
	}
	ruleSources := make([]notifications.RuleSource, 0, len(material.sources))
	eventSources := make([]events.Source, 0, len(material.sources))
	reportSources := make([]reports.Source, 0, len(material.sources))
	logSources := map[string]logs.ManifestSource{}
	actorReads := c.Has(config.WorkerNotifications) || c.Has(config.WorkerDelivery) || c.Has(config.WorkerReports)
	eventReads := c.Has(config.WorkerIngestion) || c.Has(config.WorkerNotifications) || c.Has(config.WorkerDelivery)
	for _, entry := range material.sources {
		entry.ActorMode = auth.DelegationWorker
		entry.Observer = observer
		if actorReads {
			entry.VerifyIdentity = func(ctx context.Context, instance, epoch string) error {
				return db.VerifySourceIdentity(ctx, entry.DeploymentID, instance, epoch, c.ConfigurationRevision)
			}
			client, e := control.New(entry)
			if e != nil {
				return result, e
			}
			result.cleanup = append(result.cleanup, client.Close)
			ruleSources = append(ruleSources, client)
			reportSources = append(reportSources, client)
			logSources[entry.DeploymentID] = client
		}
		if eventReads {
			entry.VerifyIdentity = func(ctx context.Context, instance, epoch string) error {
				e := db.VerifySourceIdentity(ctx, entry.DeploymentID, instance, epoch, c.ConfigurationRevision)
				if errors.Is(e, store.ErrSourceIdentityConflict) {
					return &events.RecoveryError{Reason: events.SourceChanged}
				}
				return e
			}
			source, e := control.NewEventSource(entry)
			if e != nil {
				return result, e
			}
			result.cleanup = append(result.cleanup, source.Close)
			eventSources = append(eventSources, source)
		}
	}
	if c.Has(config.WorkerIngestion) {
		ingestor, e := events.NewIngestor(eventSources, db)
		if e != nil {
			return result, e
		}
		result.runners = append(result.runners, ingestor.Run)
	}
	var evaluator *notifications.Evaluator
	var evaluationStore *store.NotificationEvaluationStore
	if c.Has(config.WorkerNotifications) || c.Has(config.WorkerDelivery) {
		policy := material.policy
		if material.notifications != nil {
			policy = material.notifications.policy
		}
		evaluationStore, err = store.NewNotificationEvaluationStore(db, c.IdentityIssuer, policy)
		if err != nil {
			return result, err
		}
		evaluator, err = notifications.NewEvaluator(evaluationStore, db, ruleSources, eventSources, c.ConfigurationRevision)
		if err != nil {
			return result, err
		}
	}
	if c.Has(config.WorkerNotifications) {
		service, e := notifications.NewRuleService(db, ruleSources, eventSources, db)
		if e != nil {
			return result, e
		}
		activationStore, e := store.NewNotificationActivationStore(db, c.IdentityIssuer)
		if e != nil {
			return result, e
		}
		activation, e := notifications.NewActivationWorker(activationStore, service)
		if e != nil {
			return result, e
		}
		result.runners = append(result.runners, activation.Run, evaluator.Run)
	}
	if c.Has(config.WorkerDelivery) {
		if material.notifications == nil || len(material.notifications.providers) == 0 {
			return result, errors.New("delivery worker has no configured providers")
		}
		devices, e := store.NewNotificationDeviceStore(db, material.notifications.policy, material.notifications.cipher)
		if e != nil {
			return result, e
		}
		pairs := make([]notifications.DeviceTopic, 0, len(material.notifications.providers))
		providers := make(map[notifications.DeviceTopic]notifications.PushProvider, len(material.notifications.providers))
		for pair, provider := range material.notifications.providers {
			pairs = append(pairs, pair)
			providers[pair] = provider
		}
		delivery, e := store.NewNotificationDeliveryStore(evaluationStore, devices, pairs)
		if e != nil {
			return result, e
		}
		ids := make([]string, len(eventSources))
		for i, source := range eventSources {
			ids[i] = source.SourceID()
		}
		sender, e := notifications.NewSender(delivery, evaluator, ids, providers)
		if e != nil {
			return result, e
		}
		sender.SetObserver(observer)
		result.runners = append(result.runners, sender.Run)
	}
	var objects *reports.ObjectStore
	if c.Reports.ObjectRoot != "" {
		objects, err = reports.OpenObjectsWithAccess(c.Reports.ObjectRoot, reportObjectAccess(c.Reports))
		if err != nil {
			return result, errors.New("worker diagnosis object storage is unavailable")
		}
		objects.SetObserver(observer)
		result.cleanup = append(result.cleanup, func() { _ = objects.Close() })
	}
	if c.Has(config.WorkerReports) {
		var reportLogs reports.LogService
		if len(material.brokers) > 0 {
			clients := make(map[string]*logs.Client, len(material.brokers))
			for id, cfg := range material.brokers {
				cfg.ActorMode = auth.DelegationWorker
				cfg.Observer = observer
				client, e := logs.NewClient(cfg)
				if e != nil {
					return result, e
				}
				clients[id] = client
				result.cleanup = append(result.cleanup, client.Close)
			}
			mappings := make([]logs.RemoteMapping, 0, len(c.LogMappings))
			for _, m := range c.LogMappings {
				mappings = append(mappings, logs.RemoteMapping{DeploymentID: m.DeploymentID, TargetGenerationID: m.TargetGenerationID, StoreName: m.StoreName, StoreVersion: m.StoreVersion, Client: clients[m.BrokerID]})
			}
			chunks, e := logs.NewRemoteChunks(mappings)
			if e != nil {
				return result, e
			}
			broker, e := logs.NewWithChunks(logSources, chunks, material.logCursorKey)
			if e != nil {
				return result, e
			}
			broker.SetObserver(observer, "worker")
			reportLogs = broker
		}
		service, e := reports.NewService(reports.ServiceConfig{Observer: observer, Sources: reportSources, Queue: db, Objects: objects, Logs: reportLogs, Redaction: material.redaction, CompanionVersion: material.companionVersion})
		if e != nil {
			return result, e
		}
		result.runners = append(result.runners, service.Run)
	}
	if c.Has(config.WorkerRetention) {
		ids := make([]string, len(c.Controls))
		for i, source := range c.Controls {
			ids[i] = source.ID
		}
		result.runners = append(result.runners, func(ctx context.Context) { runRetentionObserved(ctx, db, objects, ids, observer) })
	}
	return result, nil
}

// Each maintenance operation gets an independent bounded budget: a slow source
// cannot consume another component's entire pass. The loop itself is serial.
func runRetention(ctx context.Context, db *store.Store, objects *reports.ObjectStore, sourceIDs []string) {
	runRetentionObserved(ctx, db, objects, sourceIDs, nil)
}
func runRetentionObserved(ctx context.Context, db *store.Store, objects *reports.ObjectStore, sourceIDs []string, observer *observability.Registry) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	step := func(name string, fn func(context.Context) error) {
		if ctx.Err() != nil {
			return
		}
		bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		start := time.Now()
		err := fn(bounded)
		outcome := "ok"
		if err != nil {
			outcome = "failed"
		}
		observer.Observe("retention", name, "", "worker", outcome, time.Since(start))
		if err != nil && ctx.Err() == nil {
			slog.Warn("retention pass failed", "component", name)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		step("authentication", db.PruneAuthentication)
		step("browse", func(c context.Context) error { _, e := db.PruneCursors(c); return e })
		step("notification_delivery", func(c context.Context) error { _, e := db.ExpireNotificationDeliveries(c); return e })
		step("notifications", func(c context.Context) error { _, e := db.PruneNotifications(c); return e })
		if objects != nil {
			step("diagnosis", func(c context.Context) error { return reports.PruneObjects(c, db, objects) })
		}
		for _, id := range sourceIDs {
			step("source_events", func(c context.Context) error { _, e := db.PruneSourceEvents(c, id); return e })
		}
	}
}

func reportObjectAccess(c config.Reports) reports.ObjectAccess {
	if c.ObjectAccess == nil {
		return reports.ObjectAccess{}
	}
	return reports.ObjectAccess{Mode: c.ObjectAccess.Mode, WorkerUID: c.ObjectAccess.WorkerUID, ReaderGID: c.ObjectAccess.ReaderGID}
}

func runWorkerConfigured(path string, check bool) error {
	c, err := config.LoadWorker(path)
	if err != nil {
		return err
	}
	loaded, err := loadWorkerRuntime(c)
	if err != nil {
		return err
	}
	defer loaded.notifications.Close()
	defer loaded.observations.Close()
	if check {
		slog.Info("worker configuration and selected local key material validated; network, schema and authorization not tested")
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, loaded.databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	loaded.observations.Database(db)
	if err = db.CheckSchema(ctx); err != nil {
		return err
	}
	if c.DeliveryHold {
		if err = requirePersistedDeliveryHold(ctx, db); err != nil {
			return err
		}
	}
	background, err := prepareBackground(c, loaded, db)
	if err != nil {
		return err
	}
	defer background.Close()
	if err = loaded.observations.Listen(ctx, loaded.observations.Readiness(db)); err != nil {
		return err
	}
	background.Start(ctx)
	loaded.observations.Started()
	slog.Info("Dashboard worker starting", "components", c.Components)
	<-ctx.Done()
	loaded.observations.Draining()
	return nil
}

// Split runtime roles can inspect an operator hold, never establish or release
// one. The narrow interface deliberately has no mutation method.
type deliveryHoldReader interface {
	NotificationDeliveryControl(context.Context) (store.DeliveryControl, error)
}

func requirePersistedDeliveryHold(ctx context.Context, reader deliveryHoldReader) error {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := reader.NotificationDeliveryControl(bounded)
	if err != nil {
		return errors.New("cannot verify the required notification delivery hold")
	}
	if !state.Held {
		return errors.New("establish the notification delivery hold with the operator command before starting this process")
	}
	return nil
}
