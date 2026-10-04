package store

import (
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

func TestEventFeedLatePublicationRespectsRecoveryCutoff(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	cutoff := c.AsOf.Add(time.Hour)
	state := planPausedRecovery(t, s, initializedFeed(t, s, c), c, string(events.CursorExpired), events.RecoveryUncertainty{DashboardRestored: true, SuppressRecordedThrough: &cutoff})
	state = readyRecovery(t, s, state)
	if err := s.ApplyEventRecovery(ctx, state.Plan.ID, state.Revision, recoveryReceipt(state), nil); err != nil {
		t.Fatal(err)
	}
	before, equal, after := sourceEvent(c, 1), sourceEvent(c, 2), sourceEvent(c, 3)
	before.RecordedAt = cutoff.Add(-time.Second)
	equal.RecordedAt = cutoff
	after.RecordedAt = cutoff.Add(time.Microsecond)
	feed, err := s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	// These records did not appear in the retained replay. The source publishes
	// them later through its normal outbox, after recovery was already applied.
	if err = s.AppendFeed(ctx, feed, eventPage(c, "late-publication", before, equal, after)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		item       events.Event
		suppressed bool
	}{{before, true}, {equal, true}, {after, false}} {
		var suppressed bool
		if err = s.Pool.QueryRow(ctx, `SELECT notification_suppressed FROM dashboard_source_events WHERE deployment_id=$1::uuid AND event_id=$2::uuid`, c.DeploymentID, tc.item.EventID).Scan(&suppressed); err != nil || suppressed != tc.suppressed {
			t.Fatalf("late event %s suppression=%v, want %v: %v", tc.item.EventID, suppressed, tc.suppressed, err)
		}
	}
	// A subsequent publication of identical facts must preserve both completed
	// work and suppression independently of the current cutoff comparison.
	processedAt := cutoff.Add(2 * time.Second)
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET processed_at=$2 WHERE event_id=$1::uuid`, before.EventID, processedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET notification_suppressed=true WHERE event_id=$1::uuid`, after.EventID); err != nil {
		t.Fatal(err)
	}
	feed, err = s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	before.Position, after.Position = "4", "5"
	if err = s.AppendFeed(ctx, feed, eventPage(c, "late-repeat", before, after)); err != nil {
		t.Fatal(err)
	}
	var count int
	var processed time.Time
	var allSuppressed bool
	if err = s.Pool.QueryRow(ctx, `SELECT count(*),bool_and(notification_suppressed),(SELECT processed_at FROM dashboard_source_events WHERE event_id=$1::uuid) FROM dashboard_source_events`, before.EventID).Scan(&count, &allSuppressed, &processed); err != nil || count != 3 || !allSuppressed || !processed.Equal(processedAt) {
		t.Fatal("republication resurrected suppressed or processed work", count, allSuppressed, processed, err)
	}
}

func TestEventFeedNewSourceInheritsRestoreCutoff(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	cutoff := c.AsOf.Add(time.Hour)
	control, err := s.NotificationDeliveryControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.HoldNotifications(ctx, control.Generation, &cutoff); err != nil {
		t.Fatal(err)
	}
	// A source first configured after the restore has no previous feed row for
	// HoldNotifications to update, but it still carries the same uncertainty.
	feed := initializedFeed(t, s, c)
	var inherited time.Time
	if err = s.Pool.QueryRow(ctx, `SELECT suppress_recorded_through FROM dashboard_event_feeds WHERE deployment_id=$1::uuid`, c.DeploymentID).Scan(&inherited); err != nil || !inherited.Equal(cutoff) {
		t.Fatal("new source lost the global restore cutoff", inherited, err)
	}
	item := sourceEvent(c, 1)
	item.RecordedAt = cutoff
	if err = s.AppendFeed(ctx, feed, eventPage(c, "new-source-late-publication", item)); err != nil {
		t.Fatal(err)
	}
	var suppressed bool
	if err = s.Pool.QueryRow(ctx, `SELECT notification_suppressed FROM dashboard_source_events WHERE deployment_id=$1::uuid AND event_id=$2::uuid`, c.DeploymentID, item.EventID).Scan(&suppressed); err != nil || !suppressed {
		t.Fatal("new source admitted an unsuppressed pre-restore event", err)
	}
}
