package store

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

func TestNotificationScopeRecoveryDenialIsAtomicAndDoesNotResurrect(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	actor := reportActor(t, s, 301)
	checkpoint := eventCheckpoint()
	checkpoint.NamespaceIDs = append(checkpoint.NamespaceIDs, "40000000-0000-4000-8000-000000000002")
	feed := initializedFeed(t, s, checkpoint)
	rule, err := s.CreateNotificationRule(ctx, actor, notificationInput(checkpoint), activeNotificationIntervals(t, checkpoint, feed))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PauseFeed(ctx, feed, string(events.ScopeChanged)); err != nil {
		t.Fatal(err)
	}
	gap, paused, err := s.EventRecoveryGap(ctx, checkpoint.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	next := checkpoint
	next.NamespaceIDs = []string{checkpoint.NamespaceIDs[1]}
	if err = s.VerifySourceIdentity(ctx, checkpoint.DeploymentID, checkpoint.ControlInstanceID, checkpoint.RecoveryEpoch, 2); err != nil {
		t.Fatal(err)
	}
	recovery, err := s.PlanEventRecovery(ctx, gap, paused.Generation, 2, next, events.RecoveryUncertainty{})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeRemovedNotificationScopes(ctx, tx, recovery.Plan); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	revoked, err := s.NotificationActivationRevocations(ctx, actor, rule.ID, rule.Revision)
	if err != nil || len(revoked) != 0 {
		t.Fatal("scope denial survived failed recovery", revoked, err)
	}
	tx, err = s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeRemovedNotificationScopes(ctx, tx, recovery.Plan); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	revoked, err = s.NotificationActivationRevocations(ctx, actor, rule.ID, rule.Revision)
	if err != nil || !slices.Equal(revoked, []string{rule.Activation[0].ID}) {
		t.Fatal("removed scope did not suppress exactly its interval", revoked, err)
	}
	in := rule.RuleInput
	in.Name = "Name-only update cannot restore removed scope"
	updated, err := s.UpdateNotificationRule(ctx, actor, rule.ID, rule.Revision, in, rule.Activation)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err = s.NotificationActivationRevocations(ctx, actor, updated.ID, updated.Revision)
	if err != nil || !slices.Equal(revoked, []string{rule.Activation[0].ID}) {
		t.Fatal("name edit resurrected removed interval", revoked, err)
	}
	for _, query := range []string{`UPDATE dashboard_notification_scope_revocations SET revoked_through=revoked_through-interval '1 second'`, `DELETE FROM dashboard_notification_scope_revocations`} {
		if _, err = s.Pool.Exec(ctx, query); err == nil {
			t.Fatal("scope denial rolled backward")
		}
	}
	// A revalidated replacement interval is distinct even while activation waits
	// for the operator to finish source recovery. It cannot inherit the old grant.
	fresh := slices.Clone(updated.Activation)
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	fresh[0] = notifications.Activation{ID: id, DeploymentID: checkpoint.DeploymentID, NamespaceID: checkpoint.NamespaceIDs[0], Status: notifications.ActivationPending}
	updated, err = s.TransitionNotificationRule(ctx, actor, updated.ID, updated.Revision, fresh)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err = s.NotificationActivationRevocations(ctx, actor, updated.ID, updated.Revision)
	if err != nil || len(revoked) != 0 {
		t.Fatal("fresh pending interval inherited old denial", revoked, err)
	}
	var created, cutoff time.Time
	if err = s.Pool.QueryRow(ctx, `SELECT v.recorded_at,n.revoked_through FROM dashboard_notification_activations a JOIN dashboard_notification_rule_versions v ON v.rule_id=a.rule_id AND v.revision=a.created_revision JOIN dashboard_notification_scope_revocations n ON n.deployment_id=a.deployment_id AND n.namespace_id=a.namespace_id WHERE a.id=$1::uuid`, id).Scan(&created, &cutoff); err != nil || !created.After(cutoff) {
		t.Fatal("new interval timestamp not after denial", err)
	}
	stale := recovery.Plan
	stale.FeedGeneration++
	stale.Digest = stale.Fingerprint()
	tx, err = s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = s.RevokeRemovedNotificationScopes(ctx, tx, stale); !errors.Is(err, events.ErrRecoveryStale) {
		t.Fatal("stale recovery wrote denial", err)
	}
}
