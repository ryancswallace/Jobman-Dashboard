package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

func planPausedRecovery(t *testing.T, s *Store, feed events.Feed, target events.Checkpoint, reason string, uncertainty events.RecoveryUncertainty) events.RecoveryState {
	t.Helper()
	if err := s.PauseFeed(t.Context(), feed, reason); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifySourceIdentity(t.Context(), target.DeploymentID, target.ControlInstanceID, target.RecoveryEpoch, 1); err != nil {
		t.Fatal(err)
	}
	gap, paused, err := s.EventRecoveryGap(t.Context(), target.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanEventRecovery(t.Context(), gap, paused.Generation, 1, target, uncertainty)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func recoveryReceipt(state events.RecoveryState) events.ReconciliationReceipt {
	r := events.ReconciliationReceipt{PlanDigest: state.Plan.Digest, AcknowledgedGap: true, CompletedAt: state.Plan.CreatedAt.Add(time.Second)}
	for _, id := range state.Plan.Checkpoint.NamespaceIDs {
		r.Namespaces = append(r.Namespaces, events.NamespaceReconciliation{NamespaceID: id, Status: "unavailable"})
	}
	return r
}

func readyRecovery(t *testing.T, s *Store, state events.RecoveryState) events.RecoveryState {
	t.Helper()
	claim, err := s.ClaimEventRecovery(t.Context(), state.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AppendEventRecovery(t.Context(), claim, eventPage(state.Plan.Checkpoint, "exact-consumed-head")); err != nil {
		t.Fatal(err)
	}
	state, err = s.EventRecovery(t.Context(), state.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestEventRecoveryReplayIsAtomicDeduplicatedAndRequiresExplicitApply(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	feed := initializedFeed(t, s, c)
	original := sourceEvent(c, 1)
	if err := s.AppendFeed(ctx, feed, eventPage(c, "old-consumed", original)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET processed_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	feed, err := s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	target := c
	target.RecoveryEpoch, target.HeadCursor, target.OldestCursor = "2", "new-head", "new-oldest"
	state := planPausedRecovery(t, s, feed, target, string(events.SourceChanged), events.RecoveryUncertainty{})
	if state.Cursor != target.OldestCursor || state.LastPosition != 0 {
		t.Fatal("replay did not use new retained lower bound")
	}
	claim, err := s.ClaimEventRecovery(ctx, state.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	page := events.Page{Checkpoint: target, Items: []events.Event{original, sourceEvent(target, 2)}, NextCursor: "middle", HasMore: true}
	if _, err = s.Pool.Exec(ctx, `CREATE FUNCTION reject_recovery_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic recovery interruption'; END $$; CREATE TRIGGER reject_recovery_commit BEFORE UPDATE OF cursor ON dashboard_event_recoveries FOR EACH ROW EXECUTE FUNCTION reject_recovery_commit()`); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendEventRecovery(ctx, claim, page); err == nil {
		t.Fatal("injected interruption absent")
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_source_events`).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial recovery events committed", err)
	}
	if _, err = s.Pool.Exec(ctx, `DROP TRIGGER reject_recovery_commit ON dashboard_event_recoveries`); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendEventRecovery(ctx, claim, page); err != nil {
		t.Fatal(err)
	}
	state, err = s.EventRecovery(ctx, state.Plan.ID)
	if err != nil || state.Status != events.RecoveryReplaying || state.Cursor != "middle" || state.Scanned != 2 || state.Added != 1 || state.LeaseToken != "" {
		t.Fatal("incorrect private replay progress", err)
	}
	if err = s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, recoveryReceipt(state), nil); !errors.Is(err, events.ErrRecoveryStale) {
		t.Fatal("partially replayed source resumed", err)
	}
	claim, err = s.ClaimEventRecovery(ctx, state.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AppendEventRecovery(ctx, claim, eventPage(target, "head-including-concurrent-arrival", sourceEvent(target, 3))); err != nil {
		t.Fatal(err)
	}
	state, err = s.EventRecovery(ctx, state.Plan.ID)
	if err != nil || state.Status != events.RecoveryReady || state.Pages != 2 {
		t.Fatal("terminal page did not become ready", err)
	}
	if _, err = s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs); !errors.Is(err, events.ErrPaused) {
		t.Fatal("ready source resumed implicitly", err)
	}
	if err = s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, events.ReconciliationReceipt{}, nil); !errors.Is(err, events.ErrReconciliation) {
		t.Fatal("missing reconciliation accepted", err)
	}
	if err = s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, recoveryReceipt(state), nil); err != nil {
		t.Fatal(err)
	}
	feed, err = s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil || feed.Cursor != "head-including-concurrent-arrival" || feed.Checkpoint.RecoveryEpoch != "2" || feed.LastPosition != 3 {
		t.Fatal("resume skipped consumed head or retained old epoch", err)
	}
	var processed, closed bool
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM dashboard_source_events),(SELECT processed_at IS NOT NULL FROM dashboard_source_events WHERE event_id=$1::uuid),(SELECT resolved_at IS NOT NULL AND recovery_receipt IS NOT NULL FROM dashboard_event_gaps WHERE id=$2::uuid)`, original.EventID, state.Plan.GapID).Scan(&count, &processed, &closed); err != nil || count != 3 || !processed || !closed {
		t.Fatal("replay duplicated/reset work or lost gap audit", err)
	}
}

func TestEventRecoveryLeaseSupersessionAndSourcePins(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	state := planPausedRecovery(t, s, initializedFeed(t, s, c), c, string(events.CursorExpired), events.RecoveryUncertainty{})
	claims := make(chan events.RecoveryState, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim, err := s.ClaimEventRecovery(ctx, state.Plan.ID)
			if err == nil {
				claims <- claim
			} else if !errors.Is(err, events.ErrLease) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(claims)
	if len(claims) != 1 {
		t.Fatal("multiple recovery leases")
	}
	old := <-claims
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_event_recoveries SET lease_expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.ClaimEventRecovery(ctx, state.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AppendEventRecovery(ctx, old, eventPage(c, "old-lease")); !errors.Is(err, events.ErrLease) {
		t.Fatal("old lease committed", err)
	}
	gap, feed, err := s.EventRecoveryGap(ctx, c.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	newPlan, err := s.PlanEventRecovery(ctx, gap, feed.Generation, 1, c, events.RecoveryUncertainty{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AppendEventRecovery(ctx, fresh, eventPage(c, "superseded")); err == nil {
		t.Fatal("superseded plan committed")
	}
	if _, err = s.PlanEventRecovery(ctx, gap, feed.Generation, 1, c, events.RecoveryUncertainty{}); !errors.Is(err, events.ErrRecoveryStale) {
		t.Fatal("stale operator generation reset recovery", err)
	}
	if err = s.VerifySourceIdentity(ctx, c.DeploymentID, c.ControlInstanceID, "2", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimEventRecovery(ctx, newPlan.Plan.ID); !errors.Is(err, events.ErrRecoveryStale) {
		t.Fatal("new source epoch escaped plan pin", err)
	}
}

func TestEventRecoveryScopeRemovalRequiresAtomicGuard(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	c.NamespaceIDs = append(c.NamespaceIDs, "40000000-0000-4000-8000-000000000002")
	target := c
	target.NamespaceIDs = []string{c.NamespaceIDs[1], "40000000-0000-4000-8000-000000000003"}
	state := planPausedRecovery(t, s, initializedFeed(t, s, c), target, string(events.ScopeChanged), events.RecoveryUncertainty{})
	state = readyRecovery(t, s, state)
	if len(state.Plan.RemovedNamespaceIDs) != 1 || state.Plan.RemovedNamespaceIDs[0] != c.NamespaceIDs[0] || len(state.Plan.AddedNamespaceIDs) != 1 {
		t.Fatal("scope intersection misclassified")
	}
	if err := s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, recoveryReceipt(state), nil); !errors.Is(err, events.ErrReconciliation) {
		t.Fatal("scope-removal receipt replaced real revocation", err)
	}
	if _, err := s.Pool.Exec(ctx, `CREATE TABLE synthetic_scope_revocation(namespace_id uuid PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("synthetic guard interruption")
	guard := func(ctx context.Context, tx pgx.Tx, plan events.RecoveryPlan) error {
		_, err := tx.Exec(ctx, `INSERT INTO synthetic_scope_revocation VALUES($1::uuid)`, plan.RemovedNamespaceIDs[0])
		if err != nil {
			return err
		}
		return injected
	}
	if err := s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, recoveryReceipt(state), guard); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM synthetic_scope_revocation`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed guard committed partial revocation", err)
	}
	guard = func(ctx context.Context, tx pgx.Tx, plan events.RecoveryPlan) error {
		_, err := tx.Exec(ctx, `INSERT INTO synthetic_scope_revocation VALUES($1::uuid)`, plan.RemovedNamespaceIDs[0])
		if err != nil {
			return err
		}
		return s.RevokeRemovedNotificationScopes(ctx, tx, plan)
	}
	if err := s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, recoveryReceipt(state), guard); err != nil {
		t.Fatal(err)
	}
	feed, err := s.ClaimFeed(ctx, target.DeploymentID, target.NamespaceIDs)
	if err != nil || len(feed.NamespaceIDs) != 2 || feed.NamespaceIDs[0] != target.NamespaceIDs[0] {
		t.Fatal("scope recovery did not apply exact new configured scope", err)
	}
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_notification_scope_revocations WHERE deployment_id=$1::uuid AND namespace_id=$2::uuid`, c.DeploymentID, c.NamespaceIDs[0]).Scan(&count); err != nil || count != 1 {
		t.Fatal("production scope denial not committed with recovery apply", err)
	}
}

func TestEventRecoveryCapacityCleanupIsBoundedAndPreservesPending(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	feed := initializedFeed(t, s, c)
	for batch := 0; batch < 3; batch++ {
		items := make([]events.Event, 0, 200)
		for n := batch*200 + 1; n <= (batch+1)*200; n++ {
			items = append(items, sourceEvent(c, n))
		}
		if err := s.AppendFeed(ctx, feed, eventPage(c, "capacity-page-"+items[0].Position, items...)); err != nil {
			t.Fatal(err)
		}
		var err error
		feed, err = s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
		if err != nil {
			t.Fatal(err)
		}
	}
	state := planPausedRecovery(t, s, feed, c, "capacity", events.RecoveryUncertainty{})
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET expires_at=clock_timestamp()-interval '1 second',last_seen_at=clock_timestamp()-interval '32 days'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneCapacityEvents(ctx, c.DeploymentID, state.Plan.FeedGeneration, 1, c); err != nil || n != 0 {
		t.Fatal("capacity cleanup discarded pending evaluation", n, err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET processed_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	wrong := c
	wrong.RecoveryEpoch = "2"
	if _, err := s.PruneCapacityEvents(ctx, c.DeploymentID, state.Plan.FeedGeneration, 1, wrong); !errors.Is(err, events.ErrRecoveryStale) {
		t.Fatal("capacity cleanup accepted different epoch", err)
	}
	extended := c
	extended.RetentionSeconds = "5184000"
	if n, err := s.PruneCapacityEvents(ctx, c.DeploymentID, state.Plan.FeedGeneration, 1, extended); err != nil || n != 0 {
		t.Fatal("current longer source retention not honored", n, err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET last_seen_at=clock_timestamp()-interval '62 days'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneCapacityEvents(ctx, c.DeploymentID, state.Plan.FeedGeneration, 1, c); err != nil || n != 500 {
		t.Fatal("capacity cleanup bound failed", n, err)
	}
	var status, cursor string
	var retained int64
	var watermark time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT status,cursor,retained_count,pruned_recorded_through FROM dashboard_event_feeds`).Scan(&status, &cursor, &retained, &watermark); err != nil || status != "paused" || cursor != feed.Cursor || retained != 100 || !watermark.Equal(sourceEvent(c, 1).RecordedAt) {
		t.Fatal("capacity cleanup reset continuation or lost watermark", err)
	}
	if _, err := s.ClaimEventRecovery(ctx, state.Plan.ID); !errors.Is(err, events.ErrRecoveryStale) {
		t.Fatal("plan with obsolete dedup uncertainty survived cleanup", err)
	}
	gap, paused, err := s.EventRecoveryGap(ctx, c.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	replanned, err := s.PlanEventRecovery(ctx, gap, paused.Generation, 1, c, events.RecoveryUncertainty{})
	if err != nil || replanned.Cursor != cursor || replanned.Plan.Uncertainty.SuppressRecordedThrough == nil || !replanned.Plan.Uncertainty.SuppressRecordedThrough.Equal(watermark) {
		t.Fatal("capacity replan lost original cursor or conservative uncertainty", err)
	}
}

func TestEventRecoveryConflictingFactsStayQuarantined(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	feed := initializedFeed(t, s, c)
	original := sourceEvent(c, 2)
	if err := s.AppendFeed(ctx, feed, eventPage(c, "prior", original)); err != nil {
		t.Fatal(err)
	}
	feed, err := s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	state := planPausedRecovery(t, s, feed, c, string(events.CursorExpired), events.RecoveryUncertainty{})
	claim, err := s.ClaimEventRecovery(ctx, state.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	original.Outcome = "failure"
	if err = s.AppendEventRecovery(ctx, claim, eventPage(c, "changed", sourceEvent(c, 1), original)); !errors.Is(err, events.ErrRecoveryQuarantined) {
		t.Fatal("changed original facts not quarantined", err)
	}
	state, err = s.EventRecovery(ctx, state.Plan.ID)
	if err != nil || state.Status != events.RecoveryQuarantined || state.Pages != 0 {
		t.Fatal("conflict committed progress", err)
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_source_events`).Scan(&count); err != nil || count != 1 {
		t.Fatal("conflict inserted earlier page rows", err)
	}
	gap, feed, err := s.EventRecoveryGap(ctx, c.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PlanEventRecovery(ctx, gap, feed.Generation, 1, c, events.RecoveryUncertainty{}); !errors.Is(err, events.ErrRecoveryQuarantined) {
		t.Fatal("new plan bypassed immutable fact conflict", err)
	}
}

func TestEventRecoveryCapacityContinuesOldCursorOnlyAfterSpace(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	feed := initializedFeed(t, s, c)
	if err := s.PauseFeed(ctx, feed, "capacity"); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifySourceIdentity(ctx, c.DeploymentID, c.ControlInstanceID, c.RecoveryEpoch, 1); err != nil {
		t.Fatal(err)
	}
	gap, feed, err := s.EventRecoveryGap(ctx, c.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_event_feeds SET retained_count=1000000`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PlanEventRecovery(ctx, gap, feed.Generation, 1, c, events.RecoveryUncertainty{}); !errors.Is(err, events.ErrCapacity) {
		t.Fatal("capacity reset discarded pending work", err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_event_feeds SET retained_count=0`); err != nil {
		t.Fatal(err)
	}
	state, err := s.PlanEventRecovery(ctx, gap, feed.Generation, 1, c, events.RecoveryUncertainty{})
	if err != nil || state.Plan.Mode != events.RecoveryContinue || state.Cursor != c.HeadCursor || state.Cursor == c.OldestCursor {
		t.Fatal("capacity incorrectly replayed retained history", err)
	}
}

func TestEventRecoveryCapacityCompoundCauseRequiresNewPinnedPlan(t *testing.T) {
	for _, change := range []string{"epoch", "scope"} {
		t.Run(change, func(t *testing.T) {
			s := testDB(t)
			ctx := t.Context()
			c := eventCheckpoint()
			feed := initializedFeed(t, s, c)
			if err := s.AppendFeed(ctx, feed, eventPage(c, "old-capacity-cursor", sourceEvent(c, 1))); err != nil {
				t.Fatal(err)
			}
			feed, err := s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
			if err != nil {
				t.Fatal(err)
			}
			oldPlan := planPausedRecovery(t, s, feed, c, "capacity", events.RecoveryUncertainty{})
			if oldPlan.Plan.Mode != events.RecoveryContinue {
				t.Fatal("unchanged capacity must continue original cursor")
			}
			if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET processed_at=clock_timestamp(),expires_at=clock_timestamp()-interval '1 second',last_seen_at=clock_timestamp()-interval '32 days'`); err != nil {
				t.Fatal(err)
			}
			target := c
			target.OldestCursor = "new-config-retained-lower-bound"
			effective := string(events.SourceChanged)
			if change == "epoch" {
				target.RecoveryEpoch = "2"
			} else {
				target.NamespaceIDs = []string{"40000000-0000-4000-8000-000000000002"}
				effective = string(events.ScopeChanged)
			}
			if err = s.VerifySourceIdentity(ctx, target.DeploymentID, target.ControlInstanceID, target.RecoveryEpoch, 2); err != nil {
				t.Fatal(err)
			}
			if _, err = s.ClaimEventRecovery(ctx, oldPlan.Plan.ID); !errors.Is(err, events.ErrRecoveryStale) {
				t.Fatal("old continuation plan ignored changed source", err)
			}
			if n, err := s.PruneCapacityEvents(ctx, c.DeploymentID, oldPlan.Plan.FeedGeneration, 2, target); err != nil || n != 1 {
				t.Fatal("safe compound cleanup blocked", n, err)
			}
			gap, paused, err := s.EventRecoveryGap(ctx, c.DeploymentID)
			if err != nil {
				t.Fatal(err)
			}
			state, err := s.PlanEventRecovery(ctx, gap, paused.Generation, 2, target, events.RecoveryUncertainty{})
			if err != nil || state.Plan.Reason != "capacity" || state.Plan.EffectiveReason != effective || state.Plan.Mode != events.RecoveryRetained || state.Cursor != target.OldestCursor || state.Plan.Digest == oldPlan.Plan.Digest || state.Plan.Uncertainty.SuppressRecordedThrough == nil {
				t.Fatal("compound recovery lost original cause, explicit policy or safety floor", err)
			}
			var originalReason, priorCursor string
			if err = s.Pool.QueryRow(ctx, `SELECT reason,prior_cursor FROM dashboard_event_gaps WHERE id=$1::uuid`, gap).Scan(&originalReason, &priorCursor); err != nil || originalReason != "capacity" || priorCursor != "old-capacity-cursor" {
				t.Fatal("compound recovery rewrote original gap evidence", err)
			}
			state = readyRecovery(t, s, state)
			if err = s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, recoveryReceipt(oldPlan), s.RevokeRemovedNotificationScopes); !errors.Is(err, events.ErrReconciliation) {
				t.Fatal("old approval authorized changed recovery cause", err)
			}
			if err = s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, recoveryReceipt(state), s.RevokeRemovedNotificationScopes); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEventRecoveryPrunedWatermarkAndRestoreUncertainty(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	feed := initializedFeed(t, s, c)
	original := sourceEvent(c, 1)
	if err := s.AppendFeed(ctx, feed, eventPage(c, "old", original)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET processed_at=clock_timestamp(),expires_at=clock_timestamp()-interval '1 second',last_seen_at=clock_timestamp()-interval '32 days'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneSourceEvents(ctx, c.DeploymentID); err != nil || n != 1 {
		t.Fatal("prune failed", n, err)
	}
	var watermark time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT pruned_recorded_through FROM dashboard_event_feeds`).Scan(&watermark); err != nil || !watermark.Equal(original.RecordedAt) {
		t.Fatal("prune lost dedup uncertainty", err)
	}
	if n, err := s.PruneSourceEvents(ctx, c.DeploymentID); err != nil || n != 0 {
		t.Fatal(err)
	}
	feed, err := s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := original.RecordedAt.Add(time.Second)
	state := planPausedRecovery(t, s, feed, c, string(events.CursorExpired), events.RecoveryUncertainty{DashboardRestored: true, SuppressRecordedThrough: &cutoff})
	if state.Plan.Uncertainty.SuppressRecordedThrough == nil || !state.Plan.Uncertainty.SuppressRecordedThrough.Equal(cutoff) {
		t.Fatal("restore cutoff lowered by older prune watermark")
	}
	claim, err := s.ClaimEventRecovery(ctx, state.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	second := sourceEvent(c, 2)
	second.RecordedAt = cutoff.Add(time.Nanosecond)
	if err = s.AppendEventRecovery(ctx, claim, eventPage(c, "restored-head", original, second)); err != nil {
		t.Fatal(err)
	}
	var oldSuppressed, freshSuppressed bool
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT notification_suppressed FROM dashboard_source_events WHERE event_id=$1::uuid),(SELECT notification_suppressed FROM dashboard_source_events WHERE event_id=$2::uuid)`, original.EventID, second.EventID).Scan(&oldSuppressed, &freshSuppressed); err != nil || !oldSuppressed || freshSuppressed {
		t.Fatal("uncertain restored events became new alerts", err)
	}
	state, err = s.EventRecovery(ctx, state.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, recoveryReceipt(state), nil); err != nil {
		t.Fatal(err)
	}
	if err = s.Pool.QueryRow(ctx, `SELECT suppress_recorded_through FROM dashboard_event_feeds`).Scan(&watermark); err != nil || !watermark.Equal(cutoff) {
		t.Fatal("late-arrival suppression cutoff not persisted", err)
	}
}

func TestEventRecoveryApplyRejectsStaleRegistryAndReceipt(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	state := readyRecovery(t, s, planPausedRecovery(t, s, initializedFeed(t, s, c), c, string(events.CursorExpired), events.RecoveryUncertainty{}))
	r := recoveryReceipt(state)
	r.PlanDigest = "different-plan"
	if err := s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, r, nil); !errors.Is(err, events.ErrReconciliation) {
		t.Fatal("receipt from other plan accepted", err)
	}
	if err := s.VerifySourceIdentity(ctx, c.DeploymentID, c.ControlInstanceID, c.RecoveryEpoch, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, recoveryReceipt(state), nil); !errors.Is(err, events.ErrRecoveryStale) {
		t.Fatal("changed configuration escaped apply pin", err)
	}
}
