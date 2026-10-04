package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/control"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/httpapi"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
	"github.com/ryancswallace/jobman-dashboard/internal/reports"
	"github.com/ryancswallace/jobman-dashboard/internal/runtimeconfig"
	"github.com/ryancswallace/jobman-dashboard/internal/store"
)

type runtimeSecrets struct {
	observations     *runtimeconfig.ProcessObservations
	tls              tls.Certificate
	databaseURL      string
	identityClient   *http.Client
	identity         auth.OIDCOptions
	sources          []control.Config
	brokers          map[string]logs.ClientConfig
	redaction        *reports.RedactionPolicy
	companionVersion string
	logCursorKey     []byte
	static           *os.Root
	notifications    *notificationRuntime
}

func textSecret(path string) (string, error) {
	data, err := config.ReadSecret(path, 16384)
	if err != nil {
		return "", err
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("text secret must contain one nonempty line")
	}
	return value, nil
}
func rootsFile(path string) (*x509.CertPool, error) { return runtimeconfig.Roots(path) }
func certificateFiles(certPath, keyPath string) (tls.Certificate, error) {
	return runtimeconfig.Certificate(certPath, keyPath)
}
func databaseSecret(path string) (string, error) {
	value, err := textSecret(path)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Fragment != "" || u.Query().Get("sslmode") != "verify-full" || u.Path == "" || u.Path == "/" {
		return "", errors.New("production database URL must name a database and use sslmode=verify-full")
	}
	return value, nil
}
func loadRuntime(c config.Config) (runtimeSecrets, error) { return loadRuntimeMode(c, "serve") }
func loadRuntimeMode(c config.Config, role string) (result runtimeSecrets, err error) {
	defer func() {
		if err != nil {
			result.notifications.Close()
			result.observations.Close()
			if result.static != nil {
				_ = result.static.Close()
			}
			if result.identityClient != nil {
				result.identityClient.CloseIdleConnections()
			}
		}
	}()
	result.tls, err = certificateFiles(c.ServerTLS.CertificateFile, c.ServerTLS.KeyFile)
	if err != nil {
		return result, fmt.Errorf("Dashboard TLS: %w", err)
	}
	publicURL, _ := url.Parse(c.PublicOrigin)
	if result.tls.Leaf.VerifyHostname(publicURL.Hostname()) != nil {
		return result, errors.New("Dashboard TLS certificate does not identify publicOrigin")
	}
	result.databaseURL, err = databaseSecret(c.DatabaseURLFile)
	if err != nil {
		return result, fmt.Errorf("runtime database: %w", err)
	}
	identityRoots, err := rootsFile(c.OIDC.TrustRootsFile)
	if err != nil {
		return result, fmt.Errorf("identity trust: %w", err)
	}
	result.identityClient, err = auth.PinnedIdentityHTTPClient(c.OIDC.Issuer, identityRoots)
	if err != nil {
		return result, err
	}
	secret, err := textSecret(c.OIDC.WebClientSecretFile)
	if err != nil {
		return result, fmt.Errorf("web client secret: %w", err)
	}
	key, err := config.ReadSecret(c.Encryption.KeyFile, 32)
	if err != nil || len(key) != 32 {
		return result, errors.New("encryption key file must contain exactly 32 private raw bytes")
	}
	result.identity = auth.OIDCOptions{Issuer: c.OIDC.Issuer, Audience: c.OIDC.APIAudience, WebClientID: c.OIDC.WebClientID, WebClientSecret: secret, NativeClientID: c.OIDC.NativeClientID, NativeRedirectURI: c.OIDC.NativeRedirectURI, DirectoryIDClaim: c.OIDC.DirectoryIDClaim, ClientIDClaim: c.OIDC.ClientIDClaim, PublicOrigin: c.PublicOrigin, Scopes: c.OIDC.Scopes, EncryptionKey: key, EncryptionKeyID: c.Encryption.KeyID, HTTPClient: result.identityClient}
	result.notifications, err = loadNotificationRuntime(c, key)
	if err != nil {
		return result, err
	}
	if c.Reports.ObjectRoot != "" {
		build, ok := debug.ReadBuildInfo()
		if !ok {
			return result, errors.New("diagnosis requires embedded dependency version information")
		}
		result.companionVersion, err = companionBuildVersion(build)
		if err != nil {
			return result, err
		}
		result.redaction, err = loadReportPolicy(c.Reports, key)
		if err != nil {
			return result, err
		}
	}
	for _, source := range c.Controls {
		loaded, err := runtimeconfig.Source(source)
		if err != nil {
			return result, fmt.Errorf("Control trust/key material: %w", err)
		}
		result.sources = append(result.sources, loaded)
	}
	result.brokers = make(map[string]logs.ClientConfig)
	for _, broker := range c.LogBrokers {
		loaded, err := runtimeconfig.Broker(broker)
		if err != nil {
			return result, fmt.Errorf("broker trust/key material: %w", err)
		}
		result.brokers[broker.ID] = loaded
	}
	if len(c.LogBrokers) > 0 {
		result.logCursorKey, err = loadLogCursorKey(c.LogCursorKeyFile, key)
		if err != nil {
			return result, err
		}
	}
	ids := make([]string, len(c.Controls))
	for i, source := range c.Controls {
		ids[i] = source.ID
	}
	result.observations, err = runtimeconfig.NewObservations(c.Observability, role, c.ConfigurationRevision, ids, c.WebRoot)
	if err != nil {
		return result, err
	}
	result.static, err = openStatic(c)
	if err != nil {
		return result, err
	}
	return result, nil
}

func runConfigured(path, mode, migrationURLFile string) error {
	return runConfiguredMode(path, mode, migrationURLFile, "serve")
}

func runConfiguredMode(path, mode, migrationURLFile, checkMode string) error {
	if mode != "serve" && mode != "api" && mode != "worker" && mode != "check-config" && mode != "migrate" {
		return errors.New("mode must be serve, api, worker, check-config, or migrate")
	}
	if checkMode != "serve" && (mode != "check-config" || checkMode != "api" && checkMode != "worker") {
		return errors.New("check-mode must be serve, api, or worker and is used only by check-config")
	}
	if mode == "worker" || mode == "check-config" && checkMode == "worker" {
		if migrationURLFile != "" {
			return errors.New("migration identity may be supplied only in migrate mode")
		}
		return runWorkerConfigured(path, mode == "check-config")
	}
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	if mode == "api" || mode == "check-config" && checkMode == "api" {
		if err := validateAPIMode(c); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if mode == "migrate" {
		if migrationURLFile == "" {
			return errors.New("migrate requires --migration-database-url-file with a separate DDL identity")
		}
		dsn, err := databaseSecret(migrationURLFile)
		if err != nil {
			return err
		}
		db, err := store.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := db.Migrate(ctx); err != nil {
			return errors.New("Dashboard migration failed; inspect the database migration ledger")
		}
		slog.Info("Dashboard migrations applied")
		return nil
	}
	if migrationURLFile != "" {
		return errors.New("migration identity may be supplied only in migrate mode")
	}
	role := mode
	if mode == "check-config" {
		role = checkMode
	}
	loaded, err := loadRuntimeMode(c, role)
	if err != nil {
		return err
	}
	defer loaded.static.Close()
	defer loaded.observations.Close()
	defer loaded.notifications.Close()
	defer loaded.identityClient.CloseIdleConnections()
	if mode == "check-config" {
		slog.Info("configuration and local key material validated; network and source authorization not tested")
		return nil
	}
	db, err := store.Open(ctx, loaded.databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	loaded.observations.Database(db)
	if err := db.CheckSchema(ctx); err != nil {
		return err
	}
	if c.Events.DeliveryHold {
		if mode == "api" {
			if err := requirePersistedDeliveryHold(ctx, db); err != nil {
				return err
			}
		} else {
			state, err := db.NotificationDeliveryControl(ctx)
			if err != nil {
				return errors.New("cannot inspect notification delivery hold")
			}
			if !state.Held {
				if _, err = db.HoldNotifications(ctx, state.Generation, nil); err != nil {
					return errors.New("cannot establish notification delivery hold")
				}
			}
		}
	}
	identity, err := auth.NewOIDC(ctx, loaded.identity, db)
	if err != nil {
		return err
	}
	sources := make([]monitoring.Source, 0, len(loaded.sources))
	logSources := make(map[string]logs.ManifestSource)
	reportSources := make([]reports.Source, 0, len(loaded.sources))
	eventSources := make([]events.Source, 0, len(loaded.sources))
	ruleSources := make([]notifications.RuleSource, 0, len(loaded.sources))
	for _, entry := range loaded.sources {
		entry.Observer = loaded.observations.Registry
		entry.VerifyIdentity = func(ctx context.Context, instance, epoch string) error {
			return db.VerifySourceIdentity(ctx, entry.DeploymentID, instance, epoch, c.ConfigurationRevision)
		}
		client, err := control.New(entry)
		if err != nil {
			return err
		}
		defer client.Close()
		sources = append(sources, client)
		logSources[entry.DeploymentID] = client
		reportSources = append(reportSources, client)
		ruleSources = append(ruleSources, client)
		if c.Events.Enabled {
			entry.VerifyIdentity = func(ctx context.Context, instance, epoch string) error {
				err := db.VerifySourceIdentity(ctx, entry.DeploymentID, instance, epoch, c.ConfigurationRevision)
				if errors.Is(err, store.ErrSourceIdentityConflict) {
					return &events.RecoveryError{Reason: events.SourceChanged}
				}
				return err
			}
			eventSource, err := control.NewEventSource(entry)
			if err != nil {
				return err
			}
			defer eventSource.Close()
			eventSources = append(eventSources, eventSource)
		}
	}
	engine, err := monitoring.New(sources, db)
	if err != nil {
		return err
	}
	ruleService, err := notifications.NewRuleService(db, ruleSources, eventSources, db)
	if err != nil {
		return err
	}
	inboxService, err := notifications.NewInboxService(db, ruleSources, db)
	if err != nil {
		return err
	}
	var logService *logs.Broker
	if len(loaded.brokers) > 0 {
		brokers := make(map[string]*logs.Client)
		for id, cfg := range loaded.brokers {
			cfg.Observer = loaded.observations.Registry
			client, err := logs.NewClient(cfg)
			if err != nil {
				return err
			}
			defer client.Close()
			brokers[id] = client
		}
		mappings := make([]logs.RemoteMapping, 0, len(c.LogMappings))
		for _, m := range c.LogMappings {
			mappings = append(mappings, logs.RemoteMapping{DeploymentID: m.DeploymentID, TargetGenerationID: m.TargetGenerationID, StoreName: m.StoreName, StoreVersion: m.StoreVersion, Client: brokers[m.BrokerID]})
		}
		chunks, err := logs.NewRemoteChunks(mappings)
		if err != nil {
			return err
		}
		logService, err = logs.NewWithChunks(logSources, chunks, loaded.logCursorKey)
		if err != nil {
			return err
		}
	}
	if logService != nil {
		logService.SetObserver(loaded.observations.Registry, "interactive")
	}
	var reportService *reports.Service
	if c.Reports.ObjectRoot != "" {
		var objects reports.ObjectReader
		if mode == "api" {
			reader, err := reports.OpenObjectReader(c.Reports.ObjectRoot, reportObjectAccess(c.Reports))
			if err != nil {
				return errors.New("read-only diagnosis object storage is unavailable")
			}
			defer reader.Close()
			reader.SetObserver(loaded.observations.Registry)
			objects = reader
		} else {
			writer, err := reports.OpenObjectsWithAccess(c.Reports.ObjectRoot, reportObjectAccess(c.Reports))
			if err != nil {
				return errors.New("private diagnosis object storage is unavailable")
			}
			defer writer.Close()
			writer.SetObserver(loaded.observations.Registry)
			objects = writer
		}
		var reportLogs reports.LogService
		if logService != nil {
			reportLogs = logService
		}
		reportService, err = reports.NewService(reports.ServiceConfig{Observer: loaded.observations.Registry, Sources: reportSources, Queue: db, Objects: objects, Logs: reportLogs, Redaction: loaded.redaction, CompanionVersion: loaded.companionVersion})
		if err != nil {
			return err
		}
	}
	var notificationDevices *store.NotificationDeviceStore
	if loaded.notifications != nil {
		notificationDevices, err = store.NewNotificationDeviceStore(db, loaded.notifications.policy, loaded.notifications.cipher)
		if err != nil {
			return err
		}
	}
	if mode == "serve" {
		workerConfig := combinedWorkerConfig(c)
		workerMaterial := workerSecrets{observations: loaded.observations, sources: loaded.sources, brokers: loaded.brokers, redaction: loaded.redaction, companionVersion: loaded.companionVersion, notifications: loaded.notifications}
		workerMaterial.logCursorKey = loaded.logCursorKey
		background, err := prepareBackground(workerConfig, workerMaterial, db)
		if err != nil {
			return err
		}
		background.Start(ctx)
		defer background.Close()
	}
	app := &httpapi.Server{Engine: engine, Auth: identity, AuthRoutes: identity, Preferences: db, Static: loaded.static.FS()}
	app.Rules = ruleService
	app.Inbox = inboxService
	if notificationDevices != nil {
		app.Devices = notificationDevices
	}
	if logService != nil {
		app.Logs = logService
	}
	if reportService != nil {
		app.Reports = reportService
	}
	server := &http.Server{Addr: c.Listen, Handler: loaded.observations.Registry.HTTP(app.Handler()), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{loaded.tls}}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 128 << 10}
	if err = loaded.observations.Listen(ctx, loaded.observations.Readiness(db)); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	loaded.observations.Started()
	done := make(chan error, 1)
	go func() {
		slog.Info("Dashboard HTTPS service starting", "listen", c.Listen)
		done <- server.ServeTLS(listener, "", "")
	}()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		loaded.observations.Draining()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
