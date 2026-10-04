package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/control"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
	"github.com/ryancswallace/jobman-dashboard/internal/runtimeconfig"
	"github.com/ryancswallace/jobman-dashboard/internal/store"
)

type eventOperatorOptions struct {
	command, config, deployment, gap, recovery, digest string
	generation, revision                               int64
	pages                                              int
	restored, suppressAll, acknowledge, incomplete     bool
	cutoff                                             *time.Time
}

var errIncompleteReconciliation = errors.New("reconciliation is incomplete; inspect events reconcile and use --allow-incomplete only after reviewing that coverage")
var errConfiguredDeliveryHold = errors.New("set events.deliveryHold=false before explicitly releasing the persisted hold")
var errRestoreHoldRequired = errors.New("restore planning requires a persisted global hold and an external restore cutoff at least as late as the plan cutoff")

func parseEventOperator(args []string) (eventOperatorOptions, error) {
	var o eventOperatorOptions
	if len(args) == 0 {
		return o, errors.New("events requires gap, plan, status, step, reconcile, apply, prune, hold-status, hold, or resume")
	}
	o.command = args[0]
	allowed := map[string][]string{
		"gap": {"config", "deployment"}, "plan": {"config", "deployment", "gap", "generation", "restored", "suppress-all-recovered", "suppress-through"},
		"status": {"config", "recovery"}, "step": {"config", "recovery", "digest", "pages"}, "reconcile": {"config", "recovery", "digest"},
		"apply": {"config", "recovery", "digest", "revision", "acknowledge-gap", "allow-incomplete"}, "prune": {"config", "deployment", "generation"},
		"hold-status": {"config"}, "hold": {"config", "generation", "restore-through"}, "resume": {"config", "generation", "acknowledge-gap"},
	}
	fields, ok := allowed[o.command]
	if !ok {
		return o, errors.New("unknown events operator command")
	}
	fs := flag.NewFlagSet("events "+o.command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.config, "config", "", "private runtime config path")
	fs.StringVar(&o.deployment, "deployment", "", "configured deployment UUID")
	fs.StringVar(&o.gap, "gap", "", "observed gap UUID")
	fs.StringVar(&o.recovery, "recovery", "", "recovery UUID")
	fs.StringVar(&o.digest, "digest", "", "reviewed plan digest")
	fs.Int64Var(&o.generation, "generation", 0, "expected generation")
	fs.Int64Var(&o.revision, "revision", 0, "expected recovery revision")
	fs.IntVar(&o.pages, "pages", 1, "bounded page count, 1..50")
	fs.BoolVar(&o.restored, "restored", false, "Dashboard database restored")
	fs.BoolVar(&o.suppressAll, "suppress-all-recovered", false, "suppress all replayed events")
	fs.BoolVar(&o.acknowledge, "acknowledge-gap", false, "explicitly acknowledge incomplete historic delivery")
	fs.BoolVar(&o.incomplete, "allow-incomplete", false, "allow explicitly reported partial/unavailable current-state reconciliation")
	var cutoff, restore string
	fs.StringVar(&cutoff, "suppress-through", "", "inclusive original recorded-time cutoff, RFC3339")
	fs.StringVar(&restore, "restore-through", "", "external restore uncertainty cutoff, RFC3339")
	if fs.Parse(args[1:]) != nil || fs.NArg() != 0 {
		return o, errors.New("invalid events flags; values are not echoed")
	}
	invalid := false
	fs.Visit(func(f *flag.Flag) {
		if !slices.Contains(fields, f.Name) {
			invalid = true
		}
	})
	if invalid || o.config == "" || o.pages < 1 || o.pages > 50 {
		return o, errors.New("flags do not apply to this events command or exceed its bounds")
	}
	validID := func(v string) bool {
		cp := events.Checkpoint{DeploymentID: v, ControlInstanceID: v, RecoveryEpoch: "1", NamespaceIDs: []string{v}, AsOf: time.Unix(1, 0), HeadCursor: "head", OldestCursor: "oldest", RetentionSeconds: "86400", BacklogCount: "0"}
		return cp.Validate() == nil
	}
	if slices.Contains(fields, "deployment") && !validID(o.deployment) || slices.Contains(fields, "gap") && !validID(o.gap) || slices.Contains(fields, "recovery") && !validID(o.recovery) || slices.Contains(fields, "generation") && o.generation < 1 || slices.Contains(fields, "revision") && o.revision < 1 {
		return o, errors.New("canonical identifiers and positive expected revisions/generations are required")
	}
	if slices.Contains(fields, "digest") && (len(o.digest) != 64 || strings.Trim(o.digest, "0123456789abcdef") != "") {
		return o, errors.New("reviewed canonical plan digest is required")
	}
	if restore != "" {
		cutoff = restore
	}
	if cutoff != "" {
		v, err := time.Parse(time.RFC3339Nano, cutoff)
		if err != nil || v.Year() < 1970 || v.Year() > 9999 {
			return o, events.ErrInvalid
		}
		v = v.UTC()
		o.cutoff = &v
	}
	if o.restored && o.cutoff == nil {
		return o, errors.New("restore planning requires an external uncertainty cutoff")
	}
	if (o.command == "apply" || o.command == "resume") && !o.acknowledge {
		return o, errors.New("explicit --acknowledge-gap is required")
	}
	return o, nil
}

type operatorRecoveryStore interface {
	events.RecoveryJournal
	EventRecoveryGap(context.Context, string) (string, events.Feed, error)
	EventRecovery(context.Context, string) (events.RecoveryState, error)
	PlanEventRecovery(context.Context, string, int64, int64, events.Checkpoint, events.RecoveryUncertainty) (events.RecoveryState, error)
	ApplyEventRecovery(context.Context, string, int64, events.ReconciliationReceipt, store.RecoveryScopeGuard) error
	RevokeRemovedNotificationScopes(context.Context, pgx.Tx, events.RecoveryPlan) error
	PruneCapacityEvents(context.Context, string, int64, int64, events.Checkpoint) (int64, error)
	NotificationDeliveryControl(context.Context) (store.DeliveryControl, error)
	HoldNotifications(context.Context, int64, *time.Time) (store.DeliveryControl, error)
	ResumeNotifications(context.Context, int64, int64, []events.Checkpoint) (store.DeliveryControl, error)
}

type eventOperator struct {
	db                    operatorRecoveryStore
	sources               map[string]events.Source
	reconcile             events.TerminalReconciler
	configurationRevision int64
	startupHold           bool
}

// recoverySummary deliberately excludes private cursors, tokens, represented
// principals and all job content. It is safe to retain as operator evidence.
type recoverySummary struct {
	ID                    string                     `json:"id"`
	DeploymentID          string                     `json:"deploymentId"`
	GapID                 string                     `json:"gapId"`
	Digest                string                     `json:"digest"`
	Revision              int64                      `json:"revision,string"`
	FeedGeneration        int64                      `json:"feedGeneration,string"`
	ConfigurationRevision int64                      `json:"configurationRevision,string"`
	Status                string                     `json:"status"`
	Reason                string                     `json:"reason"`
	EffectiveReason       string                     `json:"effectiveReason"`
	Mode                  string                     `json:"mode"`
	NamespaceIDs          []string                   `json:"namespaceIds"`
	Removed               []string                   `json:"removedNamespaceIds"`
	AddedNamespaces       []string                   `json:"addedNamespaceIds"`
	Pages                 int64                      `json:"pages,string"`
	Scanned               int64                      `json:"scanned,string"`
	Added                 int64                      `json:"added,string"`
	Uncertainty           events.RecoveryUncertainty `json:"uncertainty"`
}

func summarizeRecovery(s events.RecoveryState) recoverySummary {
	return recoverySummary{s.Plan.ID, s.Plan.DeploymentID, s.Plan.GapID, s.Plan.Digest, s.Revision, s.Plan.FeedGeneration, s.Plan.ConfigurationRevision, s.Status, s.Plan.Reason, s.Plan.EffectiveReason, s.Plan.Mode, s.Plan.Checkpoint.NamespaceIDs, s.Plan.RemovedNamespaceIDs, s.Plan.AddedNamespaceIDs, s.Pages, s.Scanned, s.Added, s.Plan.Uncertainty}
}
func (r eventOperator) checkpoint(ctx context.Context, id string) (events.Checkpoint, error) {
	source := r.sources[id]
	if source == nil {
		return events.Checkpoint{}, events.ErrInvalid
	}
	cp, err := source.Checkpoint(ctx)
	if err != nil {
		return cp, err
	}
	if cp.Validate() != nil || cp.DeploymentID != id || !slices.Equal(cp.NamespaceIDs, source.NamespaceIDs()) {
		return events.Checkpoint{}, events.ErrInvalid
	}
	return cp, nil
}
func (r eventOperator) execute(ctx context.Context, o eventOperatorOptions) (any, error) {
	switch o.command {
	case "hold-status":
		return r.db.NotificationDeliveryControl(ctx)
	case "hold":
		return r.db.HoldNotifications(ctx, o.generation, o.cutoff)
	case "resume":
		if r.startupHold {
			return nil, errConfiguredDeliveryHold
		}
		ids := make([]string, 0, len(r.sources))
		for id := range r.sources {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		cps := make([]events.Checkpoint, 0, len(ids))
		for _, id := range ids {
			cp, err := r.checkpoint(ctx, id)
			if err != nil {
				return nil, err
			}
			cps = append(cps, cp)
		}
		return r.db.ResumeNotifications(ctx, o.generation, r.configurationRevision, cps)
	case "gap":
		if r.sources[o.deployment] == nil {
			return nil, events.ErrInvalid
		}
		id, feed, err := r.db.EventRecoveryGap(ctx, o.deployment)
		if err != nil {
			return nil, err
		}
		return struct {
			GapID        string `json:"gapId"`
			DeploymentID string `json:"deploymentId"`
			Generation   int64  `json:"generation,string"`
			Status       string `json:"status"`
		}{id, feed.DeploymentID, feed.Generation, feed.Status}, nil
	case "plan", "prune":
		if o.command == "plan" && o.restored {
			hold, err := r.db.NotificationDeliveryControl(ctx)
			if err != nil {
				return nil, err
			}
			if !hold.Held || hold.RestoreRecordedThrough == nil || o.cutoff == nil || hold.RestoreRecordedThrough.Before(*o.cutoff) {
				return nil, errRestoreHoldRequired
			}
		}
		cp, err := r.checkpoint(ctx, o.deployment)
		if err != nil {
			return nil, err
		}
		if o.command == "prune" {
			n, err := r.db.PruneCapacityEvents(ctx, o.deployment, o.generation, r.configurationRevision, cp)
			return struct {
				Pruned int64 `json:"pruned,string"`
			}{n}, err
		}
		state, err := r.db.PlanEventRecovery(ctx, o.gap, o.generation, r.configurationRevision, cp, events.RecoveryUncertainty{DashboardRestored: o.restored, SuppressAllRecovered: o.suppressAll, SuppressRecordedThrough: o.cutoff})
		if err != nil {
			return nil, err
		}
		return summarizeRecovery(state), nil
	}
	state, err := r.db.EventRecovery(ctx, o.recovery)
	if err != nil {
		return nil, err
	}
	if state.Plan.Validate() != nil || r.sources[state.Plan.DeploymentID] == nil {
		return nil, events.ErrRecoveryStale
	}
	if o.command == "status" {
		return summarizeRecovery(state), nil
	}
	if state.Plan.Digest != o.digest || state.Plan.ConfigurationRevision != r.configurationRevision {
		return nil, events.ErrRecoveryStale
	}
	if o.command == "step" {
		worker, err := events.NewRecoverer(r.sources[state.Plan.DeploymentID], r.db)
		if err != nil {
			return nil, err
		}
		for i := 0; i < o.pages; i++ {
			if state.Status == events.RecoveryReady {
				break
			}
			if state.Status != events.RecoveryReplaying {
				return nil, events.ErrRecoveryStale
			}
			if err = worker.Step(ctx, state.Plan.ID); err != nil {
				return nil, err
			}
			state, err = r.db.EventRecovery(ctx, state.Plan.ID)
			if err != nil {
				return nil, err
			}
			if state.Plan.Digest != o.digest {
				return nil, events.ErrRecoveryStale
			}
		}
		return summarizeRecovery(state), nil
	}
	if o.command != "reconcile" && o.command != "apply" {
		return nil, events.ErrInvalid
	}
	if o.command == "apply" && (state.Status != events.RecoveryReady || state.Revision != o.revision) {
		return nil, events.ErrRecoveryStale
	}
	current, err := r.checkpoint(ctx, state.Plan.DeploymentID)
	if err != nil {
		return nil, err
	}
	same := func(cp events.Checkpoint) bool {
		return cp.ControlInstanceID == state.Plan.Checkpoint.ControlInstanceID && cp.RecoveryEpoch == state.Plan.Checkpoint.RecoveryEpoch && slices.Equal(cp.NamespaceIDs, state.Plan.Checkpoint.NamespaceIDs)
	}
	if !same(current) {
		return nil, events.ErrRecoveryStale
	}
	receipt, err := r.reconcile.Reconcile(ctx, state.Plan)
	if err != nil {
		return nil, err
	}
	if receipt.Validate(state.Plan) != nil {
		return nil, events.ErrReconciliation
	}
	if o.command == "reconcile" {
		return receipt, nil
	}
	if !o.incomplete {
		for _, ns := range receipt.Namespaces {
			if ns.Status != "complete" {
				return nil, errIncompleteReconciliation
			}
		}
	}
	current, err = r.checkpoint(ctx, state.Plan.DeploymentID)
	if err != nil {
		return nil, err
	}
	if !same(current) {
		return nil, events.ErrRecoveryStale
	}
	if err = r.db.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, receipt, r.db.RevokeRemovedNotificationScopes); err != nil {
		return nil, err
	}
	state, err = r.db.EventRecovery(ctx, state.Plan.ID)
	if err != nil {
		return nil, err
	}
	return struct {
		Recovery       recoverySummary              `json:"recovery"`
		Reconciliation events.ReconciliationReceipt `json:"reconciliation"`
	}{summarizeRecovery(state), receipt}, nil
}

// Local hold commands remain usable when remote trust/key material is broken.
func (r *eventOperator) executeWithSourceSetup(ctx context.Context, o eventOperatorOptions, setup func() error) (any, error) {
	if o.command != "hold" && o.command != "hold-status" {
		if err := setup(); err != nil {
			return nil, err
		}
	}
	return r.execute(ctx, o)
}

func runEventOperator(args []string, output io.Writer) error {
	o, err := parseEventOperator(args)
	if err != nil {
		return err
	}
	c, err := config.Load(o.config)
	if err != nil {
		return err
	}
	if !c.Events.Enabled {
		return errors.New("events operator commands require events.enabled=true")
	}
	dsn, err := databaseSecret(c.DatabaseURLFile)
	if err != nil {
		return errors.New("operator runtime database material is unavailable")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return errors.New("operator runtime database connection failed")
	}
	defer db.Close()
	if err = db.CheckSchema(ctx); err != nil {
		return err
	}
	r := eventOperator{db: db, sources: map[string]events.Source{}, configurationRevision: c.ConfigurationRevision, startupHold: c.Events.DeliveryHold}
	closers := []func(){}
	defer func() {
		for _, close := range closers {
			close()
		}
	}()
	result, err := r.executeWithSourceSetup(ctx, o, func() error {
		represented := []monitoring.Source{}
		for _, entry := range c.Controls {
			sourceConfig, err := runtimeconfig.Source(entry)
			if err != nil {
				return errors.New("operator source trust material is unavailable")
			}
			sourceConfig.VerifyIdentity = func(ctx context.Context, instance, epoch string) error {
				return db.VerifySourceIdentity(ctx, entry.ID, instance, epoch, c.ConfigurationRevision)
			}
			source, err := control.NewEventSource(sourceConfig)
			if err != nil {
				return errors.New("operator event source configuration failed")
			}
			closers = append(closers, source.Close)
			r.sources[entry.ID] = source
			client, err := control.New(sourceConfig)
			if err != nil {
				return errors.New("operator represented source configuration failed")
			}
			closers = append(closers, client.Close)
			represented = append(represented, client)
		}
		r.reconcile, err = notifications.NewReconciler(db, represented)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		// Never print DB driver, remote response, cursor, or credential-bearing errors.
		for _, safe := range []error{events.ErrInvalid, events.ErrRecoveryStale, events.ErrRecoveryQuarantined, events.ErrReconciliation, events.ErrCapacity, events.ErrLease, events.ErrPaused, events.ErrConflict, errIncompleteReconciliation, errConfiguredDeliveryHold, errRestoreHoldRequired} {
			if errors.Is(err, safe) {
				return safe
			}
		}
		return errors.New("event operator action failed; prior cursor and hold remain authoritative; inspect source health and retry status")
	}
	enc := json.NewEncoder(output)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
