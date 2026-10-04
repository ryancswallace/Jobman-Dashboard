package store

import (
	"errors"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

func TestNotificationDeliveryRestoreHoldFencesFeedsAndRequiresRecovery(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	cp := eventCheckpoint()
	feed := initializedFeed(t, s, cp)
	pending := planPausedRecovery(t, s, feed, cp, string(events.CursorExpired), events.RecoveryUncertainty{})
	lease, err := s.ClaimEventRecovery(ctx, pending.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	second := cp
	second.DeploymentID = "20000000-0000-4000-8000-000000000002"
	active := initializedFeed(t, s, second)
	state, err := s.NotificationDeliveryControl(ctx)
	if err != nil || state.Held {
		t.Fatal(state, err)
	}
	cutoff := cp.AsOf.Add(time.Hour)
	held, err := s.HoldNotifications(ctx, state.Generation, &cutoff)
	if err != nil || !held.Held || held.Generation != state.Generation+1 || !held.RestoreRecordedThrough.Equal(cutoff) {
		t.Fatal(held, err)
	}
	if err = s.AppendEventRecovery(ctx, lease, eventPage(cp, "stale-recovery")); !errors.Is(err, events.ErrRecoveryStale) {
		t.Fatal("restored lease survived", err)
	}
	if err = s.AppendFeed(ctx, active, eventPage(second, "stale-active")); !errors.Is(err, events.ErrLease) {
		t.Fatal("active lease survived restore", err)
	}
	for _, checkpoint := range []events.Checkpoint{cp, second} {
		gap, paused, err := s.EventRecoveryGap(ctx, checkpoint.DeploymentID)
		if err != nil || paused.Status != "paused" {
			t.Fatal(paused, err)
		}
		if _, err = s.ResumeNotifications(ctx, held.Generation, 1, []events.Checkpoint{cp, second}); !errors.Is(err, events.ErrRecoveryStale) {
			t.Fatal("resumed unreconciled source", err)
		}
		if err = s.VerifySourceIdentity(ctx, checkpoint.DeploymentID, checkpoint.ControlInstanceID, checkpoint.RecoveryEpoch, 1); err != nil {
			t.Fatal(err)
		}
		recovery, err := s.PlanEventRecovery(ctx, gap, paused.Generation, 1, checkpoint, events.RecoveryUncertainty{})
		if err != nil || recovery.Plan.Uncertainty.SuppressRecordedThrough == nil || !recovery.Plan.Uncertainty.SuppressRecordedThrough.Equal(cutoff) {
			t.Fatal("plan lost restore floor", err)
		}
		ready := readyRecovery(t, s, recovery)
		if err = s.ApplyEventRecovery(ctx, ready.Plan.ID, ready.Revision, recoveryReceipt(ready), s.RevokeRemovedNotificationScopes); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.ResumeNotifications(ctx, held.Generation, 1, []events.Checkpoint{cp}); !errors.Is(err, events.ErrRecoveryStale) {
		t.Fatal("partial source set resumed", err)
	}
	wrong := second
	wrong.RecoveryEpoch = "2"
	if _, err = s.ResumeNotifications(ctx, held.Generation, 1, []events.Checkpoint{cp, wrong}); !errors.Is(err, events.ErrRecoveryStale) {
		t.Fatal("changed source resumed", err)
	}
	resumed, err := s.ResumeNotifications(ctx, held.Generation, 1, []events.Checkpoint{cp, second})
	if err != nil || resumed.Held || resumed.Generation != held.Generation+1 || !resumed.RestoreRecordedThrough.Equal(cutoff) {
		t.Fatal(resumed, err)
	}
	if _, err = s.ResumeNotifications(ctx, held.Generation, 1, []events.Checkpoint{cp, second}); !errors.Is(err, events.ErrRecoveryStale) {
		t.Fatal("stale hold release accepted", err)
	}
	for _, checkpoint := range []events.Checkpoint{cp, second} {
		f, err := s.NotificationFeed(ctx, checkpoint.DeploymentID)
		if err != nil || f.Cursor != "exact-consumed-head" {
			t.Fatal("hold release skipped to fresh head", f.Cursor, err)
		}
	}
}
func TestNotificationDeliveryHoldIsAtomicMonotonicAndIdempotent(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	cp := eventCheckpoint()
	feed := initializedFeed(t, s, cp)
	initial, err := s.NotificationDeliveryControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `CREATE FUNCTION reject_test_hold() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic hold interruption'; END $$; CREATE TRIGGER reject_test_hold BEFORE UPDATE ON dashboard_notification_delivery_control FOR EACH ROW EXECUTE FUNCTION reject_test_hold()`); err != nil {
		t.Fatal(err)
	}
	cutoff := cp.AsOf.Add(time.Hour)
	if _, err = s.HoldNotifications(ctx, initial.Generation, &cutoff); err == nil {
		t.Fatal("injected hold interruption absent")
	}
	got, err := s.NotificationFeed(ctx, cp.DeploymentID)
	if err != nil || got.Status != feed.Status || got.Generation != feed.Generation {
		t.Fatal("partial hold survived rollback", got, err)
	}
	if _, err = s.Pool.Exec(ctx, `DROP TRIGGER reject_test_hold ON dashboard_notification_delivery_control`); err != nil {
		t.Fatal(err)
	}
	held, err := s.HoldNotifications(ctx, initial.Generation, &cutoff)
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.HoldNotifications(ctx, held.Generation, &cutoff)
	if err != nil || same.Generation != held.Generation {
		t.Fatal("retry mutated hold", err)
	}
	earlier := cutoff.Add(-time.Minute)
	same, err = s.HoldNotifications(ctx, held.Generation, &earlier)
	if err != nil || same.Generation != held.Generation || !same.RestoreRecordedThrough.Equal(cutoff) {
		t.Fatal("uncertainty narrowed", same, err)
	}
	for _, query := range []string{`DELETE FROM dashboard_notification_delivery_control`, `UPDATE dashboard_notification_delivery_control SET generation=generation+1,restore_recorded_through=NULL`, `UPDATE dashboard_notification_delivery_control SET generation=generation-1`} {
		if _, err = s.Pool.Exec(ctx, query); err == nil {
			t.Fatal("delivery control rolled back")
		}
	}
}
