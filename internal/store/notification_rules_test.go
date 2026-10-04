package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

func notificationInput(c events.Checkpoint) notifications.RuleInput {
	in := notifications.RuleInput{Name: "Failures", Enabled: true, Scope: notifications.ScopeNamespaceJobs, OutcomeMode: notifications.OutcomeSelected, Outcomes: []string{"failure"}, Jobs: []notifications.JobRef{}, Namespaces: []notifications.NamespaceRef{}}
	for _, id := range c.NamespaceIDs {
		in.Namespaces = append(in.Namespaces, notifications.NamespaceRef{DeploymentID: c.DeploymentID, NamespaceID: id})
	}
	return in
}

func notificationIntervals(t *testing.T, in notifications.RuleInput, status string) []notifications.Activation {
	t.Helper()
	result := make([]notifications.Activation, len(in.Namespaces))
	for i, ref := range in.Namespaces {
		id, err := newID()
		if err != nil {
			t.Fatal(err)
		}
		result[i] = notifications.Activation{ID: id, DeploymentID: ref.DeploymentID, NamespaceID: ref.NamespaceID, Status: status}
	}
	return result
}

func activeNotificationIntervals(t *testing.T, c events.Checkpoint, feed events.Feed) []notifications.Activation {
	t.Helper()
	result := notificationIntervals(t, notificationInput(c), notifications.ActivationPending)
	for i, a := range result {
		var err error
		result[i], err = notifications.NewActivation(a.ID, a.Namespace(), feed, c, "70000000-0000-4000-8000-000000000001")
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestNotificationRulesCurrentAliasOwnershipAndHistory(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	alice, bob := reportActor(t, s, 101), reportActor(t, s, 102)
	in := notificationInput(eventCheckpoint())
	rule, err := s.CreateNotificationRule(ctx, alice, in, notificationIntervals(t, in, notifications.ActivationPending))
	if err != nil || rule.Validate() != nil || rule.Revision != 1 {
		t.Fatal("pending creation needs no healthy source", err)
	}
	for _, wrong := range []monitoring.Actor{bob, {Account: alice.Account, DirectoryID: bob.DirectoryID, Issuer: alice.Issuer, Subject: alice.Subject}} {
		if _, err := s.NotificationRule(ctx, wrong, rule.ID); err == nil {
			t.Fatal("foreign identity read rule")
		}
		if _, err := s.DeleteNotificationRule(ctx, wrong, rule.ID, rule.Revision); err == nil {
			t.Fatal("foreign identity deleted rule")
		}
	}
	alias, err := s.ResolveIdentity(ctx, auth.Identity{Issuer: "https://second-synthetic.example", Subject: "verified-alias", DirectoryID: alice.DirectoryID, DisplayName: "Alice"})
	if err != nil || alias.Account.ID != alice.Account.ID {
		t.Fatal("second verified alias", err)
	}
	if _, err = s.Pool.Exec(ctx, `DELETE FROM dashboard_identity_aliases WHERE issuer=$1 AND subject=$2`, alice.Issuer, alice.Subject); err != nil {
		t.Fatal(err)
	}
	if _, err = s.NotificationRule(ctx, alice, rule.ID); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("removed alias retained read authority", err)
	}
	in.Name = "Renamed failures"
	updated, err := s.UpdateNotificationRule(ctx, alias, rule.ID, 1, in, rule.Activation)
	if err != nil || updated.Revision != 2 || updated.Activation[0].ID != rule.Activation[0].ID {
		t.Fatal("same immutable account lost rules or name changed interval", err)
	}
	listed, err := s.ListNotificationRules(ctx, alias)
	if err != nil || len(listed) != 1 || listed[0].Revision != 2 {
		t.Fatal("current alias list", err)
	}
	var original []byte
	if err = s.Pool.QueryRow(ctx, `SELECT payload FROM dashboard_notification_rule_versions WHERE rule_id=$1::uuid AND revision=1`, rule.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	var prior notifications.Rule
	if json.Unmarshal(original, &prior) != nil || prior.Name != "Failures" {
		t.Fatal("original revision changed")
	}
	for _, query := range []string{`UPDATE dashboard_notification_rule_versions SET payload=payload`, `UPDATE dashboard_notification_activations SET payload=payload`} {
		if _, err := s.Pool.Exec(ctx, query); err == nil {
			t.Fatal("immutable history updated")
		}
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_accounts SET disabled_at=clock_timestamp() WHERE id=$1::uuid`, alias.Account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListNotificationRules(ctx, alias); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("disabled account listed rules", err)
	}
	if _, err = s.UpdateNotificationRule(ctx, alias, rule.ID, 2, in, rule.Activation); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("disabled account mutated rules", err)
	}
}

func TestNotificationRulesConcurrentRevisionAndAtomicFailure(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	alice := reportActor(t, s, 101)
	in := notificationInput(eventCheckpoint())
	rule, err := s.CreateNotificationRule(ctx, alice, in, notificationIntervals(t, in, notifications.ActivationPending))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			input := in
			input.Name = fmt.Sprintf("concurrent-%d", i)
			_, err := s.UpdateNotificationRule(ctx, alice, rule.ID, 1, input, rule.Activation)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	won, conflicted := 0, 0
	for err := range results {
		if err == nil {
			won++
		} else if errors.Is(err, notifications.ErrConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if won != 1 || conflicted != 7 {
		t.Fatalf("revision race winners=%d conflicts=%d", won, conflicted)
	}
	current, err := s.NotificationRule(ctx, alice, rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool.Exec(ctx, `CREATE FUNCTION reject_notification_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic interruption'; END $$; CREATE TRIGGER reject_notification_history BEFORE INSERT ON dashboard_notification_rule_versions FOR EACH ROW EXECUTE FUNCTION reject_notification_history()`)
	if err != nil {
		t.Fatal(err)
	}
	in.Name = "must roll back"
	if _, err = s.UpdateNotificationRule(ctx, alice, rule.ID, 2, in, rule.Activation); err == nil {
		t.Fatal("injected failure not observed")
	}
	after, err := s.NotificationRule(ctx, alice, rule.ID)
	if err != nil || after.Revision != 2 || after.Name != current.Name {
		t.Fatal("current version committed without history", err)
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_audit WHERE resource_kind='notification_rule'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("rolled back mutation left audit", err)
	}
}

func TestNotificationRulesActivationBoundariesAndImmediateStops(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	alice := reportActor(t, s, 101)
	c := eventCheckpoint()
	c.NamespaceIDs = append(c.NamespaceIDs, "40000000-0000-4000-8000-000000000002")
	feed := initializedFeed(t, s, c)
	in := notificationInput(c)
	rule, err := s.CreateNotificationRule(ctx, alice, in, notificationIntervals(t, in, notifications.ActivationPending))
	if err != nil {
		t.Fatal(err)
	}
	active := activeNotificationIntervals(t, c, feed)
	rule, err = s.TransitionNotificationRule(ctx, alice, rule.ID, rule.Revision, active)
	if err != nil || rule.Revision != 2 {
		t.Fatal("activation transition", err)
	}
	event := sourceEvent(c, 1)
	event.Outcome = "failure"
	match, err := notifications.MatchVersion(rule, event)
	if err != nil || match == nil {
		t.Fatal("new event not matched", err)
	}
	removed := slices.Clone(active)
	removed[0] = notificationIntervals(t, in, notifications.ActivationInaccessible)[0]
	lost, err := s.TransitionNotificationRule(ctx, alice, rule.ID, rule.Revision, removed)
	if err != nil || lost.Activation[1].ID != active[1].ID {
		t.Fatal("one namespace loss replaced sibling", err)
	}
	if applicable, _ := notifications.StillApplicable(*match, lost); applicable {
		t.Fatal("grant loss kept old candidate applicable")
	}
	if _, err = s.TransitionNotificationRule(ctx, alice, rule.ID, lost.Revision, active); !errors.Is(err, notifications.ErrTransition) {
		t.Fatal("retired activation UUID resurrected", err)
	}
	// A feed that changes after the service's probe rejects a NEW interval, even
	// though its private candidate was valid at construction time.
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_event_feeds SET status='paused' WHERE deployment_id=$1::uuid`, c.DeploymentID); err != nil {
		t.Fatal(err)
	}
	fresh := activeNotificationIntervals(t, c, feed)
	if _, err = s.TransitionNotificationRule(ctx, alice, rule.ID, lost.Revision, fresh); !errors.Is(err, notifications.ErrInactiveFeed) {
		t.Fatal("paused feed activated", err)
	}
	// Approved epoch recovery can preserve old intervals, but installing a new
	// interval from an old checkpoint remains forbidden.
	recovered := c
	recovered.RecoveryEpoch = "2"
	encoded, _ := json.Marshal(recovered)
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_event_feeds SET status='active',checkpoint=$2 WHERE deployment_id=$1::uuid`, c.DeploymentID, encoded); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionNotificationRule(ctx, alice, rule.ID, lost.Revision, fresh); !errors.Is(err, notifications.ErrInactiveFeed) {
		t.Fatal("stale epoch activated", err)
	}
	in.Name = "Preserved after approved recovery"
	lost, err = s.UpdateNotificationRule(ctx, alice, rule.ID, lost.Revision, in, lost.Activation)
	if err != nil || lost.Activation[1].Boundary.RecoveryEpoch != "1" {
		t.Fatal("historical interval lost during rename", err)
	}
	in.Enabled = false
	disabled, err := s.UpdateNotificationRule(ctx, alice, rule.ID, lost.Revision, in, notificationIntervals(t, in, notifications.ActivationDisabled))
	if err != nil || disabled.Enabled {
		t.Fatal("disable", err)
	}
	if applicable, _ := notifications.StillApplicable(*match, disabled); applicable {
		t.Fatal("disabled rule kept delayed candidate applicable")
	}
	deleted, err := s.DeleteNotificationRule(ctx, alice, rule.ID, disabled.Revision)
	if err != nil || deleted.DeletedAt == nil || deleted.Enabled {
		t.Fatal("delete did not append tombstone", err)
	}
	if _, err = s.NotificationRule(ctx, alice, rule.ID); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatal("deleted rule returned as current", err)
	}
	listed, err := s.ListNotificationRules(ctx, alice)
	if err != nil || len(listed) != 0 {
		t.Fatal("deleted rule remained visible", err)
	}
}

// Seed already-retained private history in one transaction. This avoids sleeps
// or weakening immutable-history triggers merely to exercise admission limits.
func seedNotificationHistory(t *testing.T, s *Store, rule notifications.Rule, count int, recent bool) notifications.Rule {
	t.Helper()
	ctx := t.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	batch := &pgx.Batch{}
	recorded := time.Now().UTC().Add(-time.Hour)
	if recent {
		recorded = time.Now().UTC()
	}
	for range count {
		rule.Revision++
		encoded, _ := json.Marshal(rule)
		batch.Queue(`INSERT INTO dashboard_notification_rule_versions(rule_id,revision,account_id,payload,regular_mutation,recorded_at) VALUES($1::uuid,$2,$3::uuid,$4,true,$5)`, rule.ID, rule.Revision, rule.AccountID, encoded, recorded)
	}
	if err = tx.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(rule)
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_rules SET revision=$2,payload=$3 WHERE id=$1::uuid`, rule.ID, rule.Revision, encoded); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return rule
}

func TestNotificationRulesHistoryAndRateQuotasCannotBlockStops(t *testing.T) {
	for _, which := range []string{"rate", "history"} {
		t.Run(which, func(t *testing.T) {
			s := testDB(t)
			ctx := t.Context()
			alice := reportActor(t, s, 101)
			in := notificationInput(eventCheckpoint())
			rule, err := s.CreateNotificationRule(ctx, alice, in, notificationIntervals(t, in, notifications.ActivationPending))
			if err != nil {
				t.Fatal(err)
			}
			limit, expected := 10, notifications.ErrRateLimited
			if which == "history" {
				limit, expected = 1000, notifications.ErrCapacity
			}
			rule = seedNotificationHistory(t, s, rule, limit-1, which == "rate")
			in.Name = "quota blocked"
			if _, err = s.UpdateNotificationRule(ctx, alice, rule.ID, rule.Revision, in, rule.Activation); !errors.Is(err, expected) {
				t.Fatal("normal edit bypassed admission", err)
			}
			in.Enabled = false
			disabled, err := s.UpdateNotificationRule(ctx, alice, rule.ID, rule.Revision, in, notificationIntervals(t, in, notifications.ActivationDisabled))
			if err != nil {
				t.Fatal("quota delayed disable", err)
			}
			unchanged, err := s.UpdateNotificationRule(ctx, alice, disabled.ID, disabled.Revision, disabled.RuleInput, disabled.Activation)
			if err != nil || unchanged.Revision != disabled.Revision {
				t.Fatal("no-op disable consumed reserved version", err)
			}
			in.Enabled = true
			if _, err = s.UpdateNotificationRule(ctx, alice, disabled.ID, disabled.Revision, in, notificationIntervals(t, in, notifications.ActivationPending)); !errors.Is(err, expected) {
				t.Fatal("reenable consumed stop reserve", err)
			}
			if _, err = s.DeleteNotificationRule(ctx, alice, disabled.ID, disabled.Revision); err != nil {
				t.Fatal("quota delayed deletion", err)
			}
			var count, stops int
			if err = s.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE NOT regular_mutation) FROM dashboard_notification_rule_versions`).Scan(&count, &stops); err != nil || count != limit+2 || stops != 2 {
				t.Fatal("history was removed or repeated no-op recorded", count, stops, err)
			}
		})
	}
}

func TestNotificationRulesFeedBindingAndHistoricalIDRejection(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	alice := reportActor(t, s, 101)
	c := eventCheckpoint()
	feed := initializedFeed(t, s, c)
	in := notificationInput(c)
	active := activeNotificationIntervals(t, c, feed)
	for _, wrong := range []string{"instance", "namespace"} {
		candidate := activeNotificationIntervals(t, c, feed)
		if wrong == "instance" {
			candidate[0].Boundary.ControlInstanceID = "30000000-0000-4000-8000-000000000009"
		} else {
			in.Namespaces[0].NamespaceID = "40000000-0000-4000-8000-000000000009"
			candidate[0].NamespaceID = in.Namespaces[0].NamespaceID
		}
		if _, err := s.CreateNotificationRule(ctx, alice, in, candidate); !errors.Is(err, notifications.ErrInactiveFeed) {
			t.Fatal("bad feed binding accepted", wrong, err)
		}
		in = notificationInput(c)
	}
	rule, err := s.CreateNotificationRule(ctx, alice, in, active)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateNotificationRule(ctx, alice, in, active); !errors.Is(err, notifications.ErrTransition) {
		t.Fatal("activation ID shared by two rules", err)
	}
	in.Outcomes = []string{"success"}
	if _, err = s.UpdateNotificationRule(ctx, alice, rule.ID, rule.Revision, in, active); !errors.Is(err, notifications.ErrTransition) {
		t.Fatal("material edit preserved prior activation", err)
	}
	current, err := s.NotificationRule(ctx, alice, rule.ID)
	if err != nil || current.Revision != rule.Revision {
		t.Fatal("invalid transition mutated current rule", err)
	}
}

func seedNotificationRules(t *testing.T, s *Store, actor monitoring.Actor, input notifications.RuleInput, count int) []notifications.Rule {
	t.Helper()
	ctx := t.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	batch := &pgx.Batch{}
	rules := make([]notifications.Rule, count)
	for i := range count {
		id, err := newID()
		if err != nil {
			t.Fatal(err)
		}
		old := time.Now().UTC().Add(-time.Hour)
		rule := notifications.Rule{ID: id, AccountID: actor.Account.ID, Revision: 1, RuleInput: input, Activation: notificationIntervals(t, input, notifications.ActivationPending), CreatedAt: old, UpdatedAt: old}
		if err = rule.Validate(); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(rule)
		batch.Queue(`INSERT INTO dashboard_notification_rules(id,account_id,revision,enabled,payload,created_at,updated_at) VALUES($1::uuid,$2::uuid,1,true,$3,$4,$4)`, id, actor.Account.ID, encoded, old)
		batch.Queue(`INSERT INTO dashboard_notification_rule_versions(rule_id,revision,account_id,payload,regular_mutation,recorded_at) VALUES($1::uuid,1,$2::uuid,$3,true,$4)`, id, actor.Account.ID, encoded, old)
		for _, activation := range rule.Activation {
			data, _ := json.Marshal(activation)
			batch.Queue(`INSERT INTO dashboard_notification_activations(id,rule_id,created_revision,deployment_id,namespace_id,payload) VALUES($1::uuid,$2::uuid,1,$3::uuid,$4::uuid,$5)`, activation.ID, id, activation.DeploymentID, activation.NamespaceID, data)
		}
		rules[i] = rule
	}
	if err = tx.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestNotificationRulesConcurrentAccountLimitAndFullStopReserve(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	alice := reportActor(t, s, 101)
	in := notificationInput(eventCheckpoint())
	rules := seedNotificationRules(t, s, alice, in, 99)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		activation := notificationIntervals(t, in, notifications.ActivationPending)
		wg.Go(func() {
			_, err := s.CreateNotificationRule(ctx, alice, in, activation)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	won, denied := 0, 0
	for err := range results {
		if err == nil {
			won++
		} else if errors.Is(err, notifications.ErrCapacity) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if won != 1 || denied != 7 {
		t.Fatalf("account quota race admitted %d denied %d", won, denied)
	}
	// Reach exactly1,000 retained records with100 live rules. Every live rule
	// can still stop and then delete; these200 records use the full reserve.
	seedNotificationHistory(t, s, rules[0], 900, false)
	listed, err := s.ListNotificationRules(ctx, alice)
	if err != nil || len(listed) != 100 {
		t.Fatal("bounded account catalog", err)
	}
	for _, rule := range listed {
		input := rule.RuleInput
		input.Enabled = false
		disabled, err := s.UpdateNotificationRule(ctx, alice, rule.ID, rule.Revision, input, notificationIntervals(t, input, notifications.ActivationDisabled))
		if err != nil {
			t.Fatal("stop reserve exhausted before all rules disabled", err)
		}
		if _, err = s.DeleteNotificationRule(ctx, alice, rule.ID, disabled.Revision); err != nil {
			t.Fatal("stop reserve exhausted before all rules deleted", err)
		}
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_notification_rule_versions`).Scan(&count); err != nil || count != 1200 {
		t.Fatal("hard history boundary", count, err)
	}
	if _, err = s.CreateNotificationRule(ctx, alice, in, notificationIntervals(t, in, notifications.ActivationPending)); !errors.Is(err, notifications.ErrCapacity) {
		t.Fatal("deletion reopened exhausted historical quota", err)
	}
	bob := reportActor(t, s, 102)
	if _, err = s.CreateNotificationRule(ctx, bob, in, notificationIntervals(t, in, notifications.ActivationPending)); err != nil {
		t.Fatal("one account exhausted another account quota", err)
	}
}

func TestNotificationRuleRevocationsAreAtomicMonotonicAndQuotaIndependent(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	alice, bob := reportActor(t, s, 101), reportActor(t, s, 102)
	c := eventCheckpoint()
	c.NamespaceIDs = append(c.NamespaceIDs, "40000000-0000-4000-8000-000000000002")
	feed := initializedFeed(t, s, c)
	in := notificationInput(c)
	rule, err := s.CreateNotificationRule(ctx, alice, in, activeNotificationIntervals(t, c, feed))
	if err != nil {
		t.Fatal(err)
	}
	first, second := rule.Activation[0].ID, rule.Activation[1].ID
	unknown := "80000000-0000-4000-8000-000000000001"
	for _, attempt := range []struct {
		actor    monitoring.Actor
		revision int64
		ids      []string
	}{
		{bob, rule.Revision, []string{first}},
		{alice, rule.Revision + 1, []string{first}},
		{alice, rule.Revision, []string{first, unknown}},
		{alice, rule.Revision, []string{first, first}},
	} {
		if err = s.DisableNotificationActivations(ctx, attempt.actor, rule.ID, attempt.revision, attempt.ids); err == nil {
			t.Fatal("invalid revocation admitted")
		}
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_notification_activation_revocations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid selection partially revoked", err)
	}
	for range 2 {
		if err = s.DisableNotificationActivations(ctx, alice, rule.ID, rule.Revision, []string{first}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_audit WHERE action='notification_rule.activations_revoked'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("idempotent revocation repeated audit", err)
	}
	revoked, err := s.NotificationActivationRevocations(ctx, alice, rule.ID, rule.Revision)
	if err != nil || !slices.Equal(revoked, []string{first}) {
		t.Fatal("denial affected sibling", revoked, err)
	}
	for _, query := range []string{`UPDATE dashboard_notification_activation_revocations SET revoked_at=clock_timestamp()`, `DELETE FROM dashboard_notification_activation_revocations`} {
		if _, err := s.Pool.Exec(ctx, query); err == nil {
			t.Fatal("durable denial reversed")
		}
	}
	if _, err = s.NotificationActivationRevocations(ctx, bob, rule.ID, rule.Revision); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatal("other account read denial state", err)
	}
	// Regrant installs a fresh source-clock boundary and leaves the historical
	// tombstone intact; the unaffected namespace retains its interval.
	fresh := activeNotificationIntervals(t, c, feed)
	fresh[1] = rule.Activation[1]
	rule, err = s.TransitionNotificationRule(ctx, alice, rule.ID, rule.Revision, fresh)
	if err != nil || rule.Activation[0].ID == first {
		t.Fatal("regrant did not replace interval", err)
	}
	revoked, err = s.NotificationActivationRevocations(ctx, alice, rule.ID, rule.Revision)
	if err != nil || len(revoked) != 0 {
		t.Fatal("old denial affected newly authorized interval", err)
	}
	if err = s.DisableNotificationActivations(ctx, alice, rule.ID, rule.Revision, []string{first}); !errors.Is(err, notifications.ErrConflict) {
		t.Fatal("retired ID was treated as current", err)
	}
	if _, err = s.NotificationActivationRevocations(ctx, alice, rule.ID, rule.Revision-1); !errors.Is(err, notifications.ErrConflict) {
		t.Fatal("stale revision read current denial state", err)
	}
	rule = seedNotificationHistory(t, s, rule, MaximumNotificationVersions-2, true)
	if err = s.DisableNotificationActivations(ctx, alice, rule.ID, rule.Revision, []string{second}); err != nil {
		t.Fatal("full history and rate quota delayed grant denial", err)
	}
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_notification_rule_versions`).Scan(&count); err != nil || count != MaximumNotificationVersions {
		t.Fatal("revocation consumed version quota", err)
	}
	revoked, err = s.NotificationActivationRevocations(ctx, alice, rule.ID, rule.Revision)
	if err != nil || !slices.Equal(revoked, []string{second}) {
		t.Fatal("quota denial not durably visible", err)
	}
	if _, err = s.TransitionNotificationRule(ctx, alice, rule.ID, rule.Revision, activeNotificationIntervals(t, c, feed)); !errors.Is(err, notifications.ErrCapacity) {
		t.Fatal("regrant bypassed full history quota", err)
	}
}

func TestNotificationFeedSnapshotNeverClaimsOrChangesSource(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	if _, err := s.NotificationFeed(ctx, c.DeploymentID); !errors.Is(err, notifications.ErrInactiveFeed) {
		t.Fatal("unknown source silently initialized", err)
	}
	feed := initializedFeed(t, s, c)
	for _, status := range []string{"active", "paused"} {
		if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_event_feeds SET status=$2 WHERE deployment_id=$1::uuid`, c.DeploymentID, status); err != nil {
			t.Fatal(err)
		}
		read, err := s.NotificationFeed(ctx, c.DeploymentID)
		if err != nil || read.Status != status || read.Generation != feed.Generation || read.Cursor != feed.Cursor || read.LeaseToken != "" || read.Checkpoint.ControlInstanceID != c.ControlInstanceID {
			t.Fatal("source snapshot changed feed or exposed worker lease", err)
		}
		var lease string
		if err = s.Pool.QueryRow(ctx, `SELECT lease_token::text FROM dashboard_event_feeds WHERE deployment_id=$1::uuid`, c.DeploymentID).Scan(&lease); err != nil || lease != feed.LeaseToken {
			t.Fatal("read changed ingestion lease", err)
		}
	}
}
