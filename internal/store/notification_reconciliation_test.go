package store

import (
	"errors"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

func TestNotificationReconciliationUsesOnlyCurrentScopesAndIdentity(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	actor := reportActor(t, s, 401)
	cp := eventCheckpoint()
	in := notificationInput(cp)
	in.Namespaces = append(in.Namespaces, notifications.NamespaceRef{DeploymentID: cp.DeploymentID, NamespaceID: "40000000-0000-4000-8000-000000000002"})
	rule, err := s.CreateNotificationRule(ctx, actor, in, notificationIntervals(t, in, notifications.ActivationPending))
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := s.NotificationReconciliationCandidates(ctx, cp.DeploymentID, cp.NamespaceIDs[0])
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	original := candidates[0]
	if err = s.VerifyNotificationReconciliationCandidate(ctx, original); err != nil {
		t.Fatal(err)
	}
	// A material scope edit removes its index entry atomically with the rule update.
	in.Namespaces = in.Namespaces[1:]
	rule, err = s.UpdateNotificationRule(ctx, actor, rule.ID, rule.Revision, in, notificationIntervals(t, in, notifications.ActivationPending))
	if err != nil {
		t.Fatal(err)
	}
	candidates, err = s.NotificationReconciliationCandidates(ctx, cp.DeploymentID, cp.NamespaceIDs[0])
	if err != nil || len(candidates) != 0 {
		t.Fatal("historical scope became current", candidates, err)
	}
	if err = s.VerifyNotificationReconciliationCandidate(ctx, original); !errors.Is(err, notifications.ErrConflict) {
		t.Fatal("old candidate survived CAS", err)
	}
	candidates, err = s.NotificationReconciliationCandidates(ctx, cp.DeploymentID, in.Namespaces[0].NamespaceID)
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	current := candidates[0]
	if err = s.DisableNotificationActivations(ctx, actor, rule.ID, rule.Revision, []string{rule.Activation[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err = s.VerifyNotificationReconciliationCandidate(ctx, current); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("monotonic denial ignored", err)
	}
	// Candidate lookup does not grant access; final local ownership must still hold.
	if _, err = s.Pool.Exec(ctx, `DELETE FROM dashboard_identity_aliases WHERE issuer=$1 AND subject=$2`, actor.Issuer, actor.Subject); err != nil {
		t.Fatal(err)
	}
	candidates, err = s.NotificationReconciliationCandidates(ctx, cp.DeploymentID, in.Namespaces[0].NamespaceID)
	if err != nil || len(candidates) != 0 {
		t.Fatal("removed alias returned", candidates, err)
	}
	if err = s.VerifyNotificationReconciliationCandidate(ctx, current); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("removed alias retained candidate", err)
	}
}
func TestNotificationReconciliationDisabledDeletedAndSourceQualified(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	actor := reportActor(t, s, 402)
	cp := eventCheckpoint()
	in := notificationInput(cp)
	rule, err := s.CreateNotificationRule(ctx, actor, in, notificationIntervals(t, in, notifications.ActivationPending))
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := s.NotificationReconciliationCandidates(ctx, "20000000-0000-4000-8000-000000000099", cp.NamespaceIDs[0])
	if err != nil || len(candidates) != 0 {
		t.Fatal("cross-source candidate", err)
	}
	in.Enabled = false
	rule, err = s.UpdateNotificationRule(ctx, actor, rule.ID, rule.Revision, in, notificationIntervals(t, in, notifications.ActivationDisabled))
	if err != nil {
		t.Fatal(err)
	}
	candidates, err = s.NotificationReconciliationCandidates(ctx, cp.DeploymentID, cp.NamespaceIDs[0])
	if err != nil || len(candidates) != 0 {
		t.Fatal("disabled rule candidate", err)
	}
	if _, err = s.DeleteNotificationRule(ctx, actor, rule.ID, rule.Revision); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_notification_rule_scopes WHERE rule_id=$1::uuid`, rule.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted scope index retained", count, err)
	}
}
