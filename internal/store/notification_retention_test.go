package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
	"github.com/ryancswallace/jobman-dashboard/internal/push"
)

func retentionPass(t *testing.T, s *Store) NotificationRetentionStats {
	t.Helper()
	stats, err := s.PruneNotifications(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

func expireInbox(t *testing.T, s *Store) {
	t.Helper()
	deviceExec(t, s, `UPDATE dashboard_notification_inbox SET created_at=clock_timestamp()-interval '31 days',expires_at=clock_timestamp()-interval '1 day'`)
}

func acceptDelivery(t *testing.T, x *deliveryFixture) notifications.DeliveryClaim {
	t.Helper()
	c := x.claimDelivery(t)
	h := x.prepare(t, c)
	if err := x.delivery.FinishNotificationDelivery(t.Context(), h, push.Result{Outcome: "accepted", ProviderID: c.ID}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNotificationRetentionInboxAndJournalHaveSeparateLifetimes(t *testing.T) {
	x := deliverySetup(t)
	// An old inbox cannot be removed while its delivery is still pending.
	expireInbox(t, x.s)
	if got := retentionPass(t, x.s); got.Inboxes != 0 || got.Deliveries != 0 {
		t.Fatal("pending delivery lost required content", got)
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_inbox SET expires_at=clock_timestamp()+interval '1 day'`)
	c := acceptDelivery(t, x)
	expireInbox(t, x.s)
	if got := retentionPass(t, x.s); got.Inboxes != 1 || got.Deliveries != 0 || got.Attempts != 0 {
		t.Fatal("inbox and dedup lifetimes coupled", got)
	}
	var original string
	var current *string
	if err := x.s.Pool.QueryRow(t.Context(), `SELECT original_inbox_id::text,inbox_id::text FROM dashboard_notification_deliveries WHERE id=$1::uuid`, c.ID).Scan(&original, &current); err != nil || original != c.InboxID || current != nil {
		t.Fatal("immutable journal identity lost", err)
	}
	if _, err := x.s.Pool.Exec(t.Context(), `UPDATE dashboard_notification_deliveries SET original_inbox_id=$1::uuid`, x.actor.Account.ID); err == nil {
		t.Fatal("original inbox identity was mutable")
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_deliveries SET resolved_at=clock_timestamp()-interval '34 days'`)
	if got := retentionPass(t, x.s); got.Deliveries != 0 || got.Attempts != 0 {
		t.Fatal("journal expired before 35 days", got)
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_deliveries SET resolved_at=clock_timestamp()-interval '36 days'`)
	if got := retentionPass(t, x.s); got.Deliveries != 1 || got.Attempts != 1 {
		t.Fatal("resolved journal did not retire", got)
	}
	if x.count(t, `SELECT pending_deliveries FROM dashboard_notification_work_quota`) != 0 {
		t.Fatal("retention changed pending quota")
	}
}

func TestNotificationRetentionUnknownHandoffKeepsMinimalJournal(t *testing.T) {
	x := deliverySetup(t)
	c := x.claimDelivery(t)
	x.prepare(t, c) // The process loses the provider result.
	deviceExec(t, x.s, `UPDATE dashboard_notification_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second'`)
	acceptDelivery(t, x)
	expireInbox(t, x.s)
	deviceExec(t, x.s, `UPDATE dashboard_notification_deliveries SET resolved_at=clock_timestamp()-interval '100 days'`)
	got := retentionPass(t, x.s)
	if got.Inboxes != 1 || got.Attempts != 1 || got.Deliveries != 0 {
		t.Fatal("uncertain attempt or large inbox lifetime incorrect", got)
	}
	if x.count(t, `SELECT count(*) FROM dashboard_notification_delivery_attempts WHERE outcome='unknown' AND ambiguous`) != 1 || x.count(t, `SELECT count(*) FROM dashboard_notification_deliveries WHERE inbox_id IS NULL AND original_inbox_id=$1::uuid`, c.InboxID) != 1 {
		t.Fatal("minimal uncertain journal lost")
	}
	// Known source payload can retire independently; the immutable unknown
	// provider journal remains. Its delivery identity is never reclassified.
	deviceExec(t, x.s, `UPDATE dashboard_source_events SET expires_at=clock_timestamp()-interval '1 day',last_seen_at=clock_timestamp()-interval '100 days'`)
	if n, err := x.s.PruneSourceEvents(t.Context(), x.cp.DeploymentID); err != nil || n != 1 {
		t.Fatal("resolved source payload remained coupled to minimal journal", n, err)
	}
	if got = retentionPass(t, x.s); got.Deliveries != 0 || x.count(t, `SELECT count(*) FROM dashboard_notification_delivery_attempts`) != 1 {
		t.Fatal("unknown journal retired on later pass", got)
	}
}

func TestNotificationRetentionSourceHealthAndRetentionHighWater(t *testing.T) {
	x := deliverySetup(t)
	acceptDelivery(t, x)
	expireInbox(t, x.s)
	deviceExec(t, x.s, `UPDATE dashboard_notification_deliveries SET resolved_at=clock_timestamp()-interval '94 days'`)
	// A larger source replay window is adopted by normal ingestion. A later
	// smaller advertised value cannot shorten this durable high-water.
	x.cp.RetentionSeconds = "7776000"
	x.event(t, 2)
	x.cp.RetentionSeconds = "2592000"
	x.event(t, 3)
	if x.count(t, `SELECT retention_seconds FROM dashboard_event_feeds`) != 90*86400 {
		t.Fatal("source retention high-water decreased")
	}
	if got := retentionPass(t, x.s); got.Inboxes != 1 || got.Deliveries != 0 {
		t.Fatal("replay+5 day horizon ignored", got)
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_deliveries SET resolved_at=clock_timestamp()-interval '96 days'; UPDATE dashboard_event_feeds SET last_success_at=clock_timestamp()-interval '2 minutes'`)
	if got := retentionPass(t, x.s); got.Deliveries != 0 || got.Attempts != 0 {
		t.Fatal("outage erased provider history", got)
	}
	deviceExec(t, x.s, `UPDATE dashboard_event_feeds SET last_success_at=clock_timestamp()`)
	if got := retentionPass(t, x.s); got.Deliveries != 1 || got.Attempts != 1 {
		t.Fatal("healthy resolved history did not expire", got)
	}
}

func TestNotificationRetentionHoldProtectsDedupAndPendingDelivery(t *testing.T) {
	x := deliverySetup(t)
	deviceExec(t, x.s, `UPDATE dashboard_source_events SET expires_at=clock_timestamp()-interval '1 day',last_seen_at=clock_timestamp()-interval '36 days'`)
	if n, err := x.s.PruneSourceEvents(t.Context(), x.cp.DeploymentID); err != nil || n != 0 {
		t.Fatal("pending provider work lost event/account identity", n, err)
	}
	acceptDelivery(t, x)
	expireInbox(t, x.s)
	deviceExec(t, x.s, `UPDATE dashboard_notification_deliveries SET resolved_at=clock_timestamp()-interval '36 days'`)
	control, err := x.s.NotificationDeliveryControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = x.s.HoldNotifications(t.Context(), control.Generation, nil); err != nil {
		t.Fatal(err)
	}
	if got := retentionPass(t, x.s); got.Inboxes != 1 || got.Deliveries != 0 || got.Attempts != 0 {
		t.Fatal("hold did not separate expired content from replay history", got)
	}
	if n, err := x.s.PruneSourceEvents(t.Context(), x.cp.DeploymentID); err != nil || n != 0 {
		t.Fatal("restore hold lost event/account identity", n, err)
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_delivery_control SET held=false,generation=generation+1`)
	if got := retentionPass(t, x.s); got.Deliveries != 1 || got.Attempts != 1 {
		t.Fatal("released hold did not permit resolved cleanup", got)
	}
	if n, err := x.s.PruneSourceEvents(t.Context(), x.cp.DeploymentID); err != nil || n != 1 {
		t.Fatal("released hold did not permit dedup cleanup", n, err)
	}
}

func TestNotificationRetentionCapacityRecoveryPreservesPendingWork(t *testing.T) {
	for _, pending := range []string{"evaluation", "delivery", "hold"} {
		t.Run(pending, func(t *testing.T) {
			var x *evaluationFixture
			if pending == "evaluation" {
				x = evaluationSetup(t)
				x.rule(t, reportActor(t, x.s, 960))
				x.event(t, 1)
				x.fanout(t)
			} else {
				delivery := deliverySetup(t)
				x = delivery.evaluationFixture
				if pending == "hold" {
					acceptDelivery(t, delivery)
					state, err := x.s.NotificationDeliveryControl(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if _, err = x.s.HoldNotifications(t.Context(), state.Generation, nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			state := planPausedRecovery(t, x.s, x.feed, x.cp, "capacity", events.RecoveryUncertainty{})
			deviceExec(t, x.s, `UPDATE dashboard_source_events SET expires_at=clock_timestamp()-interval '1 day',last_seen_at=clock_timestamp()-interval '36 days'`)
			if n, err := x.s.PruneCapacityEvents(t.Context(), x.cp.DeploymentID, state.Plan.FeedGeneration, 1, x.cp); err != nil || n != 0 {
				t.Fatal("capacity cleanup lost required event/account identity", n, err)
			}
			if x.count(t, `SELECT count(*) FROM dashboard_source_events`) != 1 || x.count(t, `SELECT count(*) FROM dashboard_notification_evaluations`) != 1 {
				t.Fatal("capacity recovery cascaded account work")
			}
		})
	}
}

// Insert realistic past snapshots without weakening any immutable trigger.
// All rows live only in the random disposable schema created by testDB.
func retentionRule(t *testing.T, x *evaluationFixture, actor monitoring.Actor, count int, rotate bool) (notifications.Rule, []string) {
	t.Helper()
	ctx := t.Context()
	id, _ := newID()
	origin := x.now.Add(-36 * 24 * time.Hour)
	r := notifications.Rule{ID: id, AccountID: actor.Account.ID, RuleInput: notificationInput(x.cp), Revision: 1, CreatedAt: origin, UpdatedAt: origin, Activation: activeNotificationIntervals(t, x.cp, x.feed)}
	tx, err := x.s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_rules(id,account_id,revision,enabled,payload,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3,true,$4,$5,$5)`, id, actor.Account.ID, count, []byte("pending"), origin); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	batch := &pgx.Batch{}
	var encoded []byte
	for i := 1; i <= count; i++ {
		r.Revision = int64(i)
		if i > 1 && rotate {
			r.Activation = activeNotificationIntervals(t, x.cp, x.feed)
		}
		if err = r.Validate(); err != nil {
			t.Fatal(err)
		}
		encoded, _ = json.Marshal(r)
		batch.Queue(`INSERT INTO dashboard_notification_rule_versions(rule_id,revision,account_id,payload,regular_mutation,recorded_at) VALUES($1::uuid,$2,$3::uuid,$4,true,$5)`, id, r.Revision, actor.Account.ID, encoded, origin)
		for _, a := range r.Activation {
			if i == 1 || rotate {
				payload, _ := json.Marshal(a)
				ids = append(ids, a.ID)
				batch.Queue(`INSERT INTO dashboard_notification_activations(id,rule_id,created_revision,deployment_id,namespace_id,payload) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5::uuid,$6)`, a.ID, id, r.Revision, a.DeploymentID, a.NamespaceID, payload)
			}
			batch.Queue(`INSERT INTO dashboard_notification_version_activations(rule_id,revision,activation_id) VALUES($1::uuid,$2,$3::uuid)`, id, r.Revision, a.ID)
		}
	}
	batch.Queue(`UPDATE dashboard_notification_rules SET payload=$2 WHERE id=$1::uuid`, id, encoded)
	for _, a := range r.Activation {
		batch.Queue(`INSERT INTO dashboard_notification_rule_scopes(rule_id,account_id,deployment_id,namespace_id,activation_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid)`, id, actor.Account.ID, a.DeploymentID, a.NamespaceID, a.ID)
	}
	if err = tx.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return r, ids
}

func TestNotificationRetentionRestoresHistoryCapacityPreservingOrigin(t *testing.T) {
	x := evaluationSetup(t)
	actor := reportActor(t, x.s, 901)
	r, ids := retentionRule(t, x, actor, 1000, false)
	in := r.RuleInput
	in.Name = "After retention"
	if _, err := x.s.UpdateNotificationRule(t.Context(), actor, r.ID, r.Revision, in, r.Activation); !errors.Is(err, notifications.ErrCapacity) {
		t.Fatal("seed did not exhaust retained snapshot quota", err)
	}
	if got := retentionPass(t, x.s); got.RuleVersions != 20 || got.ActivationPayloads != 0 {
		t.Fatal("bounded quota recovery retired current interval", got)
	}
	if x.count(t, `SELECT count(*) FROM dashboard_notification_rule_versions WHERE rule_id=$1::uuid AND revision=1`, r.ID) != 0 {
		t.Fatal("creation version was not retired")
	}
	updated, err := x.s.UpdateNotificationRule(t.Context(), actor, r.ID, r.Revision, in, r.Activation)
	if err != nil || updated.Revision != 1001 {
		t.Fatal("expired history did not release capacity", err)
	}
	if err = x.s.DisableNotificationActivations(t.Context(), actor, r.ID, updated.Revision, ids); err != nil {
		t.Fatal(err)
	}
	revoked, err := x.s.NotificationActivationRevocations(t.Context(), actor, r.ID, updated.Revision)
	if err != nil || len(revoked) != 1 || revoked[0] != ids[0] {
		t.Fatal("origin-version pruning weakened current denial", revoked, err)
	}
	var origin time.Time
	if err = x.s.Pool.QueryRow(t.Context(), `SELECT origin_recorded_at FROM dashboard_notification_activations WHERE id=$1::uuid`, ids[0]).Scan(&origin); err != nil || !origin.Equal(r.CreatedAt) {
		t.Fatal("original authority timestamp changed", err)
	}
}

func TestNotificationRetentionRetiredActivationCannotResurrect(t *testing.T) {
	x := evaluationSetup(t)
	actor := reportActor(t, x.s, 902)
	r, ids := retentionRule(t, x, actor, 2, true)
	deviceExec(t, x.s, `INSERT INTO dashboard_notification_activation_revocations(activation_id) VALUES($1::uuid)`, ids[0])
	if _, err := x.s.Pool.Exec(t.Context(), `DELETE FROM dashboard_notification_activation_revocations WHERE activation_id=$1::uuid`, ids[0]); err == nil {
		t.Fatal("live override was removable")
	}
	if got := retentionPass(t, x.s); got.RuleVersions != 1 || got.ActivationPayloads != 1 || got.Revocations != 1 {
		t.Fatal("old payload did not retire to identity", got)
	}
	if x.count(t, `SELECT count(*) FROM dashboard_notification_activations WHERE id=$1::uuid AND payload IS NULL AND retired_at IS NOT NULL AND rule_id=$2::uuid AND account_id=$3::uuid`, ids[0], r.ID, actor.Account.ID) != 1 {
		t.Fatal("permanent activation identity lost")
	}
	for _, query := range []string{
		`DELETE FROM dashboard_notification_activations WHERE id=$1::uuid`,
		`UPDATE dashboard_notification_activations SET payload='restored',retired_at=NULL WHERE id=$1::uuid`,
		`INSERT INTO dashboard_notification_version_activations(rule_id,revision,activation_id) SELECT rule_id,2,id FROM dashboard_notification_activations WHERE id=$1::uuid`,
	} {
		if _, err := x.s.Pool.Exec(t.Context(), query, ids[0]); err == nil {
			t.Fatal("retired identity was reused")
		}
	}
	in := r.RuleInput
	in.Outcomes = []string{"success"}
	intervals := activeNotificationIntervals(t, x.cp, x.feed)
	intervals[0].ID = ids[0]
	if _, err := x.s.UpdateNotificationRule(t.Context(), actor, r.ID, r.Revision, in, intervals); !errors.Is(err, notifications.ErrTransition) {
		t.Fatal("retired interval bypassed ordinary API admission", err)
	}
}

func TestNotificationRetentionHoldOutageAndCurrentReferences(t *testing.T) {
	x := evaluationSetup(t)
	actor := reportActor(t, x.s, 903)
	r, _ := retentionRule(t, x, actor, 2, true)
	control, err := x.s.NotificationDeliveryControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = x.s.HoldNotifications(t.Context(), control.Generation, nil); err != nil {
		t.Fatal(err)
	}
	if got := retentionPass(t, x.s); got.RuleVersions != 0 || got.ActivationPayloads != 0 {
		t.Fatal("restore hold lost authority history", got)
	}
	// Direct state setup only in this disposable schema, to isolate retention
	// health gates from the separately tested operator recovery protocol.
	deviceExec(t, x.s, `UPDATE dashboard_notification_delivery_control SET held=false,generation=generation+1; UPDATE dashboard_event_feeds SET last_success_at=clock_timestamp()-interval '2 minutes'`)
	if got := retentionPass(t, x.s); got.RuleVersions != 0 {
		t.Fatal("outage lost authority history", got)
	}
	deviceExec(t, x.s, `UPDATE dashboard_event_feeds SET last_success_at=clock_timestamp()`)
	// Pending and completed evaluation matches keep exact historical context.
	x.event(t, 1)
	x.fanout(t)
	deviceExec(t, x.s, `UPDATE dashboard_source_events SET expires_at=clock_timestamp()-interval '1 day',last_seen_at=clock_timestamp()-interval '36 days'`)
	if n, err := x.s.PruneSourceEvents(t.Context(), x.cp.DeploymentID); err != nil || n != 0 {
		t.Fatal("completed fanout pruned pending account work", n, err)
	}
	in := r.RuleInput
	in.Outcomes = []string{"success"}
	if _, err = x.s.UpdateNotificationRule(t.Context(), actor, r.ID, r.Revision, in, activeNotificationIntervals(t, x.cp, x.feed)); err != nil {
		t.Fatal(err)
	}
	if got := retentionPass(t, x.s); got.RuleVersions != 1 || x.count(t, `SELECT count(*) FROM dashboard_notification_rule_versions WHERE rule_id=$1::uuid AND revision=2`, r.ID) != 1 {
		t.Fatal("pending evaluation lost exact historical version", got)
	}
}

func TestNotificationRetentionDeletedRulesAndRoundRobin(t *testing.T) {
	x := evaluationSetup(t)
	for i := 0; i < 23; i++ {
		actor := reportActor(t, x.s, 920+i)
		r, _ := retentionRule(t, x, actor, 2, false)
		if i == 22 {
			// Deleted history is inserted as an already-old valid snapshot.
			// This avoids modifying any immutable history/activation trigger.
			in := r.RuleInput
			in.Enabled = false
			at := r.UpdatedAt
			r.Revision++
			r.Enabled = false
			r.DeletedAt = &at
			r.Activation = notificationIntervals(t, in, notifications.ActivationDisabled)
			if err := r.Validate(); err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(r)
			tx, err := x.s.Pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			if _, err = tx.Exec(t.Context(), `INSERT INTO dashboard_notification_rule_versions(rule_id,revision,account_id,payload,regular_mutation,recorded_at) VALUES($1::uuid,$2,$3::uuid,$4,false,$5)`, r.ID, r.Revision, actor.Account.ID, data, at); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(t.Context(), `UPDATE dashboard_notification_rules SET revision=$2,enabled=false,deleted_at=updated_at,payload=$3 WHERE id=$1::uuid`, r.ID, r.Revision, data); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(t.Context(), `DELETE FROM dashboard_notification_rule_scopes WHERE rule_id=$1::uuid`, r.ID); err != nil {
				t.Fatal(err)
			}
			for _, a := range r.Activation {
				payload, _ := json.Marshal(a)
				if _, err = tx.Exec(t.Context(), `INSERT INTO dashboard_notification_activations(id,rule_id,created_revision,deployment_id,namespace_id,payload) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5::uuid,$6)`, a.ID, r.ID, r.Revision, a.DeploymentID, a.NamespaceID, payload); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(t.Context(), `INSERT INTO dashboard_notification_version_activations(rule_id,revision,activation_id) VALUES($1::uuid,$2,$3::uuid)`, r.ID, r.Revision, a.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}
	total := NotificationRetentionStats{}
	for range 3 {
		got := retentionPass(t, x.s)
		if got.RuleVersions > 40 {
			t.Fatal("account pass unbounded", got)
		}
		total.RuleVersions += got.RuleVersions
		total.Rules += got.Rules
	}
	if total.RuleVersions != 25 || total.Rules != 1 || x.count(t, `SELECT count(*) FROM dashboard_notification_rule_versions`) != 22 {
		t.Fatal(fmt.Sprintf("round-robin cleanup skipped accounts or current history: %+v", total))
	}
}
