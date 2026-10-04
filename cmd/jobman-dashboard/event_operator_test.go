package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/store"
)

const operatorID = "20000000-0000-4000-8000-000000000001"

func operatorState() events.RecoveryState {
	cp := events.Checkpoint{DeploymentID: operatorID, ControlInstanceID: "30000000-0000-4000-8000-000000000001", NamespaceIDs: []string{"40000000-0000-4000-8000-000000000001"}, RecoveryEpoch: "1", AsOf: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC), HeadCursor: "private.plan.head", OldestCursor: "private.oldest", RetentionSeconds: "2592000", BacklogCount: "0"}
	plan := events.RecoveryPlan{ID: "60000000-0000-4000-8000-000000000001", GapID: "70000000-0000-4000-8000-000000000001", DeploymentID: operatorID, FeedGeneration: 3, ConfigurationRevision: 2, Reason: string(events.CursorExpired), EffectiveReason: string(events.CursorExpired), Mode: events.RecoveryRetained, PriorCheckpoint: &cp, PriorNamespaceIDs: cp.NamespaceIDs, PriorCursor: "private.previous", Checkpoint: cp, RemovedNamespaceIDs: []string{}, AddedNamespaceIDs: []string{}, CreatedAt: cp.AsOf}
	plan.Digest = plan.Fingerprint()
	consumed := cp
	consumed.HeadCursor = "private.consumed.head"
	return events.RecoveryState{Plan: plan, Status: events.RecoveryReady, Revision: 4, Cursor: consumed.HeadCursor, Checkpoint: consumed, Pages: 1, Scanned: 2, Added: 1, LeaseToken: "private.lease"}
}
func TestEventOperatorRejectsInapplicableOrUnsafeFlags(t *testing.T) {
	common := []string{"--config", "/private/operator.json"}
	state := operatorState()
	valid := map[string][]string{
		"gap": {"--deployment", operatorID}, "plan": {"--deployment", operatorID, "--gap", state.Plan.GapID, "--generation", "3"}, "status": {"--recovery", state.Plan.ID},
		"step": {"--recovery", state.Plan.ID, "--digest", state.Plan.Digest, "--pages", "50"}, "reconcile": {"--recovery", state.Plan.ID, "--digest", state.Plan.Digest},
		"apply": {"--recovery", state.Plan.ID, "--digest", state.Plan.Digest, "--revision", "4", "--acknowledge-gap"}, "prune": {"--deployment", operatorID, "--generation", "3"},
		"hold-status": {}, "hold": {"--generation", "1", "--restore-through", "2026-10-04T02:00:00Z"}, "resume": {"--generation", "2", "--acknowledge-gap"},
	}
	for command, flags := range valid {
		args := append([]string{command}, common...)
		args = append(args, flags...)
		if _, err := parseEventOperator(args); err != nil {
			t.Fatal(command, err)
		}
		if _, err := parseEventOperator(append(args, "--unknown", "private.canary")); err == nil || strings.Contains(err.Error(), "private.canary") {
			t.Fatal("unknown flag accepted or echoed")
		}
	}
	for _, args := range [][]string{
		{"gap", "--config", "x", "--deployment", "../wrong"}, {"plan", "--config", "x", "--deployment", operatorID, "--gap", state.Plan.GapID, "--generation", "1", "--restored"},
		{"step", "--config", "x", "--recovery", state.Plan.ID, "--digest", state.Plan.Digest, "--pages", "51"}, {"hold-status", "--config", "x", "--suppress-all-recovered"},
		{"resume", "--config", "x", "--generation", "1"}, {"apply", "--config", "x", "--recovery", state.Plan.ID, "--digest", state.Plan.Digest, "--revision", "4"},
	} {
		if _, err := parseEventOperator(args); err == nil {
			t.Fatal("unsafe operator flags accepted", args[0])
		}
	}
}
func TestEventOperatorSummaryNeverExposesPrivateCursors(t *testing.T) {
	state := operatorState()
	encoded, err := json.Marshal(summarizeRecovery(state))
	if err != nil || strings.Contains(string(encoded), "private.") || strings.Contains(string(encoded), "lease") || strings.Contains(string(encoded), "principal") {
		t.Fatal("private provenance exposed", err)
	}
	if !strings.Contains(string(encoded), state.Plan.Digest) || !strings.Contains(string(encoded), `"revision":"4"`) {
		t.Fatal("reviewable CAS provenance missing")
	}
}

type operatorSource struct {
	cp    events.Checkpoint
	calls int
	after func(*events.Checkpoint)
}

func (s *operatorSource) SourceID() string       { return s.cp.DeploymentID }
func (s *operatorSource) NamespaceIDs() []string { return s.cp.NamespaceIDs }
func (s *operatorSource) Checkpoint(context.Context) (events.Checkpoint, error) {
	s.calls++
	cp := s.cp
	if s.calls > 1 && s.after != nil {
		s.after(&cp)
	}
	return cp, nil
}
func (*operatorSource) Read(context.Context, string, int) (events.Page, error) {
	panic("not a replay test")
}

type operatorDB struct {
	operatorRecoveryStore
	state   events.RecoveryState
	applied bool
	receipt events.ReconciliationReceipt
	guard   bool
}

func (d *operatorDB) EventRecovery(context.Context, string) (events.RecoveryState, error) {
	return d.state, nil
}
func (d *operatorDB) ApplyEventRecovery(_ context.Context, id string, rev int64, r events.ReconciliationReceipt, guard store.RecoveryScopeGuard) error {
	if id != d.state.Plan.ID || rev != d.state.Revision {
		return events.ErrRecoveryStale
	}
	d.applied = true
	d.receipt = r
	d.guard = guard != nil
	d.state.Status = events.RecoveryApplied
	d.state.Revision++
	return nil
}
func (*operatorDB) RevokeRemovedNotificationScopes(context.Context, pgx.Tx, events.RecoveryPlan) error {
	return nil
}

type operatorReconciler struct {
	status string
	calls  int
}

func (r *operatorReconciler) Reconcile(_ context.Context, p events.RecoveryPlan) (events.ReconciliationReceipt, error) {
	r.calls++
	receipt := events.ReconciliationReceipt{PlanDigest: p.Digest, AcknowledgedGap: true, CompletedAt: p.CreatedAt.Add(time.Minute)}
	for _, id := range p.Checkpoint.NamespaceIDs {
		ns := events.NamespaceReconciliation{NamespaceID: id, Status: r.status}
		if r.status == "complete" || r.status == "partial" {
			ns.AsOf = &p.Checkpoint.AsOf
			ns.JobsRead = 2
		}
		receipt.Namespaces = append(receipt.Namespaces, ns)
	}
	return receipt, nil
}
func TestEventOperatorApplyReconcilesAndNeverReplacesConsumedHead(t *testing.T) {
	for _, mode := range []string{"complete", "incomplete", "acknowledged-partial", "changed-source", "stale-revision", "wrong-digest", "preview"} {
		t.Run(mode, func(t *testing.T) {
			db := &operatorDB{state: operatorState()}
			source := &operatorSource{cp: db.state.Plan.Checkpoint}
			source.cp.HeadCursor = "new.unconsumed.source.head"
			reconciler := &operatorReconciler{status: "complete"}
			r := eventOperator{db: db, sources: map[string]events.Source{operatorID: source}, reconcile: reconciler, configurationRevision: 2}
			o := eventOperatorOptions{command: "apply", recovery: db.state.Plan.ID, digest: db.state.Plan.Digest, revision: db.state.Revision, acknowledge: true}
			switch mode {
			case "incomplete":
				reconciler.status = "unavailable"
			case "acknowledged-partial":
				reconciler.status = "partial"
				o.incomplete = true
			case "changed-source":
				source.after = func(cp *events.Checkpoint) { cp.RecoveryEpoch = "2" }
			case "stale-revision":
				o.revision--
			case "wrong-digest":
				o.digest = strings.Repeat("0", 64)
			case "preview":
				o.command = "reconcile"
			}
			_, err := r.execute(t.Context(), o)
			success := mode == "complete" || mode == "acknowledged-partial"
			if db.applied != success || ((err == nil) != (success || mode == "preview")) {
				t.Fatal("unsafe apply decision", db.applied, err)
			}
			if db.state.Cursor != "private.consumed.head" || db.state.Checkpoint.HeadCursor != "private.consumed.head" {
				t.Fatal("fresh source head skipped unconsumed events")
			}
			if success && (!db.guard || reconciler.calls != 1 || source.calls != 2 || db.receipt.Validate(db.state.Plan) != nil) {
				t.Fatal("missing reconciliation or final source fence")
			}
			if mode == "changed-source" && !errors.Is(err, events.ErrRecoveryStale) {
				t.Fatal("source identity not fenced", err)
			}
		})
	}
}

func (d *operatorDB) NotificationDeliveryControl(context.Context) (store.DeliveryControl, error) {
	return store.DeliveryControl{Generation: 2, Held: true}, nil
}
func (d *operatorDB) HoldNotifications(_ context.Context, generation int64, cutoff *time.Time) (store.DeliveryControl, error) {
	return store.DeliveryControl{Generation: generation + 1, Held: true, RestoreRecordedThrough: cutoff}, nil
}
func TestEventOperatorCanHoldWhenSourceKeyLoadingFails(t *testing.T) {
	for _, command := range []string{"hold", "hold-status", "resume"} {
		r := eventOperator{db: &operatorDB{}}
		called := false
		result, err := r.executeWithSourceSetup(t.Context(), eventOperatorOptions{command: command, generation: 1}, func() error { called = true; return errors.New("source certificate unavailable") })
		if command == "resume" {
			if !called || err == nil {
				t.Fatal("resume skipped current source initialization")
			}
			continue
		}
		if called || err != nil || !result.(store.DeliveryControl).Held {
			t.Fatal("local safety hold depended on remote material", called, err)
		}
	}
}

func TestEventOperatorRestorePlanCannotSubstituteForGlobalHold(t *testing.T) {
	db := &operatorDB{state: operatorState()}
	source := &operatorSource{cp: db.state.Plan.Checkpoint}
	r := eventOperator{db: db, sources: map[string]events.Source{operatorID: source}}
	cutoff := source.cp.AsOf
	_, err := r.execute(t.Context(), eventOperatorOptions{command: "plan", deployment: operatorID, restored: true, cutoff: &cutoff})
	if !errors.Is(err, errRestoreHoldRequired) || source.calls != 0 {
		t.Fatal("restore planning bypassed missing global uncertainty policy", err)
	}
}
