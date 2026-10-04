package events

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func recoveryPlan() RecoveryPlan {
	c := testCheckpoint()
	p := RecoveryPlan{ID: "70000000-0000-4000-8000-000000000001", GapID: "80000000-0000-4000-8000-000000000001", DeploymentID: c.DeploymentID, FeedGeneration: 3, ConfigurationRevision: 1, Reason: string(CursorExpired), Mode: RecoveryRetained, PriorCheckpoint: &c, PriorNamespaceIDs: c.NamespaceIDs, PriorCursor: "previous.head", Checkpoint: c, CreatedAt: c.AsOf, RemovedNamespaceIDs: []string{}, AddedNamespaceIDs: []string{}}
	p.EffectiveReason = p.Reason
	p.Digest = p.Fingerprint()
	return p
}

func TestRecoveryPlanPinsSourceScopeEpochAndPolicy(t *testing.T) {
	p := recoveryPlan()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*RecoveryPlan){
		"replacement":              func(p *RecoveryPlan) { p.Checkpoint.ControlInstanceID = p.ID },
		"rollback":                 func(p *RecoveryPlan) { p.Checkpoint.RecoveryEpoch = "1" },
		"wrong-deployment":         func(p *RecoveryPlan) { p.Checkpoint.DeploymentID = p.ID },
		"missing-scope-tombstones": func(p *RecoveryPlan) { p.Checkpoint.NamespaceIDs = []string{p.ID} },
		"capacity-cannot-replay":   func(p *RecoveryPlan) { p.Reason = "capacity" },
		"cannot-ignore-conflict":   func(p *RecoveryPlan) { p.Reason = "event_conflict" },
		"restore-cutoff-required":  func(p *RecoveryPlan) { p.Uncertainty.DashboardRestored = true },
		"missing-configuration":    func(p *RecoveryPlan) { p.ConfigurationRevision = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := recoveryPlan()
			mutate(&bad)
			bad.Digest = bad.Fingerprint()
			if bad.Validate() == nil {
				t.Fatal("unsafe recovery plan accepted")
			}
		})
	}
	p.Reason, p.EffectiveReason, p.Mode = "capacity", "capacity", RecoveryContinue
	p.Digest = p.Fingerprint()
	if err := p.Validate(); err != nil {
		t.Fatal("capacity continuation rejected", err)
	}
	p.PriorCursor = "edited"
	if p.Validate() == nil {
		t.Fatal("changed cursor retained approved fingerprint")
	}
}

func TestRecoveryReceiptIsExplicitBoundedAndHonest(t *testing.T) {
	p := recoveryPlan()
	r := ReconciliationReceipt{PlanDigest: p.Digest, AcknowledgedGap: true, CompletedAt: p.CreatedAt, Namespaces: []NamespaceReconciliation{{NamespaceID: p.Checkpoint.NamespaceIDs[0], Status: "unavailable"}}}
	if err := r.Validate(p); err != nil {
		t.Fatal("honest unavailable receipt rejected", err)
	}
	for _, status := range []string{"complete", "partial"} {
		r.Namespaces[0].Status = status
		if r.Validate(p) == nil {
			t.Fatal("observation without source time accepted")
		}
		r.Namespaces[0].AsOf = &p.Checkpoint.AsOf
		if r.Validate(p) != nil {
			t.Fatal("bounded source observation rejected")
		}
		r.Namespaces[0].AsOf = nil
	}
	r.Namespaces[0].Status = "inaccessible"
	r.Namespaces[0].JobsRead = 1
	if r.Validate(p) == nil {
		t.Fatal("inaccessible namespace disclosed a count")
	}
	r.Namespaces[0].JobsRead = 0
	r.AcknowledgedGap = false
	if r.Validate(p) == nil {
		t.Fatal("gap acknowledged implicitly")
	}
}

func TestRecoveryReceiptSupportsAllNamespacesWithoutUnboundedScans(t *testing.T) {
	p := recoveryPlan()
	p.Checkpoint.NamespaceIDs = nil
	for i := 1; i <= 320; i++ {
		p.Checkpoint.NamespaceIDs = append(p.Checkpoint.NamespaceIDs, fmt.Sprintf("30000000-0000-4000-8000-%012d", i))
	}
	p.RemovedNamespaceIDs, p.AddedNamespaceIDs = NamespaceChanges(p.PriorNamespaceIDs, p.Checkpoint.NamespaceIDs)
	p.Digest = p.Fingerprint()
	r := ReconciliationReceipt{PlanDigest: p.Digest, AcknowledgedGap: true, CompletedAt: p.CreatedAt}
	for _, id := range p.Checkpoint.NamespaceIDs {
		r.Namespaces = append(r.Namespaces, NamespaceReconciliation{NamespaceID: id, Status: "partial", JobsRead: 3125, AsOf: &p.Checkpoint.AsOf})
	}
	if err := r.Validate(p); err != nil {
		t.Fatal("exact million-job/320-namespace receipt rejected", err)
	}
	r.Namespaces[0].JobsRead++
	if r.Validate(p) == nil {
		t.Fatal("global reconciliation scan bound ignored")
	}
}

func TestRecoveryUncertaintyUsesRecordedTimeAndInclusiveCutoff(t *testing.T) {
	e := testEvent()
	cutoff := e.RecordedAt
	u := RecoveryUncertainty{DashboardRestored: true, SuppressRecordedThrough: &cutoff}
	if u.Validate() != nil || !u.Suppresses(e) {
		t.Fatal("restore equality escaped suppression")
	}
	e.RecordedAt = cutoff.Add(time.Nanosecond)
	e.ObservedCompletedAt = &cutoff
	if u.Suppresses(e) {
		t.Fatal("observed completion incorrectly controlled uncertainty")
	}
	u.SuppressAllRecovered = true
	if !u.Suppresses(e) {
		t.Fatal("explicit recovery suppression ignored")
	}
}

type recoverySource struct {
	checkpoint Checkpoint
	read       func(context.Context, string, int) (Page, error)
}

func (s recoverySource) SourceID() string                               { return s.checkpoint.DeploymentID }
func (s recoverySource) NamespaceIDs() []string                         { return s.checkpoint.NamespaceIDs }
func (s recoverySource) Checkpoint(context.Context) (Checkpoint, error) { return s.checkpoint, nil }
func (s recoverySource) Read(ctx context.Context, c string, limit int) (Page, error) {
	return s.read(ctx, c, limit)
}

type recoveryJournal struct {
	state              RecoveryState
	appendErr          error
	appended, released bool
}

func (j *recoveryJournal) ClaimEventRecovery(context.Context, string) (RecoveryState, error) {
	return j.state, nil
}
func (j *recoveryJournal) AppendEventRecovery(_ context.Context, _ RecoveryState, _ Page) error {
	j.appended = true
	return j.appendErr
}
func (j *recoveryJournal) ReleaseEventRecovery(ctx context.Context, _ RecoveryState) error {
	j.released = ctx.Err() == nil
	return nil
}

func TestRecovererDoesOneBoundedPageAndReleasesAfterCancellation(t *testing.T) {
	p := recoveryPlan()
	j := &recoveryJournal{state: RecoveryState{Plan: p, Status: RecoveryReplaying, Revision: 1, Cursor: p.Checkpoint.OldestCursor}}
	reads := 0
	source := recoverySource{checkpoint: p.Checkpoint, read: func(ctx context.Context, cursor string, limit int) (Page, error) {
		reads++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > IngestionTimeout || cursor != p.Checkpoint.OldestCursor || limit != 200 {
			t.Fatal("unbounded recovery request")
		}
		return Page{Checkpoint: p.Checkpoint, Items: []Event{}, NextCursor: p.Checkpoint.HeadCursor}, nil
	}}
	r, err := NewRecoverer(source, j)
	if err != nil || r.Step(t.Context(), p.ID) != nil || reads != 1 || !j.appended || !j.released {
		t.Fatal("single-page recovery failed", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	j.appended, j.released = false, false
	r.source = recoverySource{checkpoint: p.Checkpoint, read: func(context.Context, string, int) (Page, error) { cancel(); return Page{}, context.Canceled }}
	if err = r.Step(ctx, p.ID); !errors.Is(err, context.Canceled) || j.appended || !j.released {
		t.Fatal("cancellation appended data or stranded lease", err)
	}
	j.state.Plan.Checkpoint.NamespaceIDs = []string{p.ID}
	j.state.Plan.Digest = j.state.Plan.Fingerprint()
	if r.Step(t.Context(), p.ID) == nil {
		t.Fatal("changed configured scope accepted")
	}
}
