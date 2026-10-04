package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

type evaluationFixture struct {
	s    *Store
	repo *NotificationEvaluationStore
	cp   events.Checkpoint
	feed events.Feed
	now  time.Time
}

func evaluationSetup(t *testing.T) *evaluationFixture {
	t.Helper()
	s := testDB(t)
	var now time.Time
	if err := s.Pool.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	cp := eventCheckpoint()
	cp.AsOf = now.Add(-time.Minute)
	feed := initializedFeed(t, s, cp)
	if err := s.VerifySourceIdentity(t.Context(), cp.DeploymentID, cp.ControlInstanceID, cp.RecoveryEpoch, 1); err != nil {
		t.Fatal(err)
	}
	policy, err := notifications.NewDevicePolicy([]notifications.DeviceTopic{{Topic: "test.jobman.dashboard", Environment: "sandbox"}})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewNotificationEvaluationStore(s, "https://synthetic.example", policy)
	if err != nil {
		t.Fatal(err)
	}
	return &evaluationFixture{s, repo, cp, feed, now}
}
func (x *evaluationFixture) rule(t *testing.T, a monitoring.Actor) notifications.Rule {
	t.Helper()
	in := notificationInput(x.cp)
	rule, err := x.s.CreateNotificationRule(t.Context(), a, in, activeNotificationIntervals(t, x.cp, x.feed))
	if err != nil {
		t.Fatal(err)
	}
	return rule
}
func (x *evaluationFixture) event(t *testing.T, n int) events.Event {
	t.Helper()
	e := sourceEvent(x.cp, n)
	e.Outcome = "failure"
	e.RecordedAt = x.now.Add(-30 * time.Second)
	if err := x.s.AppendFeed(t.Context(), x.feed, eventPage(x.cp, fmt.Sprint("event-", n), e)); err != nil {
		t.Fatal(err)
	}
	var err error
	x.feed, err = x.s.ClaimFeed(t.Context(), x.cp.DeploymentID, x.cp.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func (x *evaluationFixture) fence(t *testing.T) notifications.SourceFence {
	t.Helper()
	feed, err := x.s.NotificationFeed(t.Context(), x.cp.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err = x.s.Pool.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	return notifications.SourceFence{Checkpoint: feed.Checkpoint, FeedGeneration: feed.Generation, ConfigurationRevision: 1, VerifiedAt: now}
}
func (x *evaluationFixture) fanout(t *testing.T) {
	t.Helper()
	for range 10 {
		claim, err := x.repo.ClaimNotificationFanout(t.Context(), x.fence(t))
		if err != nil {
			t.Fatal(err)
		}
		progress, err := x.repo.AppendNotificationFanout(t.Context(), claim)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Done {
			return
		}
	}
	t.Fatal("unbounded fanout")
}
func (x *evaluationFixture) claim(t *testing.T) notifications.EvaluationClaim {
	t.Helper()
	claim, err := x.repo.ClaimNotificationEvaluation(t.Context(), x.fence(t))
	if err != nil {
		t.Fatal(err)
	}
	return claim
}
func (x *evaluationFixture) authorized(t *testing.T, c notifications.EvaluationClaim) notifications.EvaluationDecision {
	t.Helper()
	var now time.Time
	if err := x.s.Pool.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	return notifications.EvaluationDecision{Outcome: notifications.EvaluationAuthorized, Proof: notifications.AuthorizationProof{AccountID: c.Actor.Account.ID, Namespace: notifications.NamespaceRef{DeploymentID: c.Event.DeploymentID, NamespaceID: c.Event.NamespaceID}, ControlInstanceID: c.Fence.Checkpoint.ControlInstanceID, RecoveryEpoch: c.Fence.Checkpoint.RecoveryEpoch, PrincipalID: "70000000-0000-4000-8000-000000000001", Version: "synthetic-current-proof", CheckedAt: now, ExpiresAt: now.Add(120 * time.Second)}}
}
func (x *evaluationFixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := x.s.Pool.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNotificationEvaluationFanoutPagesAndOverlapInboxAtomicity(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	alice := reportActor(t, x.s, 601)
	bob := reportActor(t, x.s, 602)
	x.rule(t, alice)
	x.rule(t, alice)
	x.rule(t, bob)
	d := deviceStore(t, x.s)
	_, _ = deviceBind(t, d, alice, 1)
	_, _ = deviceBind(t, d, bob, 2)
	x.event(t, 1)
	x.fanout(t)
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_evaluations`); n != 2 {
		t.Fatal("overlap created multiple account tasks", n)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_evaluation_matches`); n != 3 {
		t.Fatal("lost matched rule context", n)
	}
	first := x.claim(t)
	if err := x.repo.DeferNotificationEvaluation(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := x.claim(t)
	if second.Actor.Account.ID == first.Actor.Account.ID {
		t.Fatal("deferred account blocked healthy owner")
	}
	if _, err := x.s.Pool.Exec(ctx, `CREATE FUNCTION reject_test_notification_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic publication interruption'; END $$; CREATE TRIGGER reject_test_notification_commit BEFORE UPDATE OF state ON dashboard_notification_evaluations FOR EACH ROW EXECUTE FUNCTION reject_test_notification_commit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := x.repo.CommitNotificationEvaluation(ctx, second, x.fence(t), x.authorized(t, second)); err == nil {
		t.Fatal("injected commit failure absent")
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox`); n != 0 {
		t.Fatal("partial inbox survived rollback", n)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_deliveries`); n != 0 {
		t.Fatal("partial device queue survived rollback", n)
	}
	deviceExec(t, x.s, `DROP TRIGGER reject_test_notification_commit ON dashboard_notification_evaluations`)
	result, err := x.repo.CommitNotificationEvaluation(ctx, second, x.fence(t), x.authorized(t, second))
	if err != nil || result.State != "complete" || result.Deliveries != 1 {
		t.Fatal(result, err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_source_events WHERE processed_at IS NOT NULL`); n != 0 {
		t.Fatal("source marked processed with deferred account", n)
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_evaluations SET next_attempt_at=clock_timestamp() WHERE state='pending'`)
	first = x.claim(t)
	result, err = x.repo.CommitNotificationEvaluation(ctx, first, x.fence(t), x.authorized(t, first))
	if err != nil || result.Deliveries != 1 {
		t.Fatal(result, err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox_matches`); n != 3 {
		t.Fatal("inbox lost overlap evidence", n)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_source_events WHERE processed_at IS NOT NULL`); n != 1 {
		t.Fatal("resolved source not processed", n)
	}
	if n := x.count(t, `SELECT pending_evaluations FROM dashboard_notification_work_quota`); n != 0 {
		t.Fatal("pending quota leaked", n)
	}
	if _, err = x.repo.CommitNotificationEvaluation(ctx, first, x.fence(t), x.authorized(t, first)); !errors.Is(err, notifications.ErrEvaluationLease) {
		t.Fatal("committed lease replay accepted", err)
	}
	// Inbox insertion time controls its lifetime independently of source time.
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox WHERE expires_at-created_at BETWEEN interval '29 days 23 hours' AND interval '30 days 1 hour'`); n != 2 {
		t.Fatal("incorrect inbox lifetime", n)
	}
}

func TestNotificationEvaluationBoundedFanoutAndCapacityRollback(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	for i := 0; i < 6; i++ {
		a := reportActor(t, x.s, 610+i)
		for j := 0; j < 9; j++ {
			x.rule(t, a)
		}
	}
	x.event(t, 1)
	claim, err := x.repo.ClaimNotificationFanout(ctx, x.fence(t))
	if err != nil {
		t.Fatal(err)
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_work_quota SET pending_evaluations=100000`)
	if _, err = x.repo.AppendNotificationFanout(ctx, claim); !errors.Is(err, notifications.ErrEvaluationCapacity) {
		t.Fatal("pending cap bypassed", err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_evaluations`); n != 0 {
		t.Fatal("capacity failure partially admitted tasks", n)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_fanout WHERE after_rule_id IS NOT NULL`); n != 0 {
		t.Fatal("capacity failure advanced fanout cursor", n)
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_work_quota SET pending_evaluations=0`)
	first, err := x.repo.AppendNotificationFanout(ctx, claim)
	if err != nil || first.Done || first.RulesRead != 50 {
		t.Fatal(first, err)
	}
	if _, err = x.repo.ClaimNotificationEvaluation(ctx, x.fence(t)); !errors.Is(err, notifications.ErrEvaluationEmpty) {
		t.Fatal("evaluated incomplete fanout", err)
	}
	claim, err = x.repo.ClaimNotificationFanout(ctx, x.fence(t))
	if err != nil {
		t.Fatal(err)
	}
	second, err := x.repo.AppendNotificationFanout(ctx, claim)
	if err != nil || !second.Done || second.RulesRead != 4 {
		t.Fatal(second, err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_evaluation_matches`); n != 54 {
		t.Fatal("page lost or duplicated matches", n)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_evaluations`); n != 6 {
		t.Fatal("account fanout count", n)
	}
}

func TestNotificationEvaluationLeaseTakeoverAndIdentityIsolation(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	a := reportActor(t, x.s, 620)
	x.rule(t, a)
	x.event(t, 1)
	var wg sync.WaitGroup
	claims := make(chan notifications.FanoutClaim, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := x.repo.ClaimNotificationFanout(ctx, x.fence(t))
			if err == nil {
				claims <- c
			} else if !errors.Is(err, notifications.ErrEvaluationEmpty) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(claims)
	if len(claims) != 1 {
		t.Fatal("multiple fanout claims", len(claims))
	}
	old := <-claims
	deviceExec(t, x.s, `UPDATE dashboard_notification_fanout SET lease_expires_at=clock_timestamp()-interval '1 second'`)
	fresh, err := x.repo.ClaimNotificationFanout(ctx, x.fence(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = x.repo.AppendNotificationFanout(ctx, old); !errors.Is(err, notifications.ErrEvaluationLease) {
		t.Fatal("stale fanout lease committed", err)
	}
	if _, err = x.repo.AppendNotificationFanout(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	first := x.claim(t)
	deviceExec(t, x.s, `UPDATE dashboard_notification_evaluations SET lease_expires_at=clock_timestamp()-interval '1 second'`)
	next := x.claim(t)
	if _, err = x.repo.CommitNotificationEvaluation(ctx, first, x.fence(t), x.authorized(t, first)); !errors.Is(err, notifications.ErrEvaluationLease) {
		t.Fatal("expired evaluation published", err)
	}
	forged := next
	forged.Actor.Account.ID = reportActor(t, x.s, 621).Account.ID
	if _, err = x.repo.CommitNotificationEvaluation(ctx, forged, x.fence(t), x.authorized(t, forged)); err == nil {
		t.Fatal("foreign account reused lease")
	}
	wrong := x.authorized(t, next)
	wrong.Proof.Namespace.NamespaceID = "40000000-0000-4000-8000-000000000099"
	if _, err = x.repo.CommitNotificationEvaluation(ctx, next, x.fence(t), wrong); !errors.Is(err, notifications.ErrEvaluationInvalid) {
		t.Fatal("other namespace proof accepted", err)
	}
	for _, field := range []string{"instance", "epoch"} {
		mixed := x.authorized(t, next)
		if field == "instance" {
			mixed.Proof.ControlInstanceID = "30000000-0000-4000-8000-000000000099"
		} else {
			mixed.Proof.RecoveryEpoch = "2"
		}
		if _, err = x.repo.CommitNotificationEvaluation(ctx, next, x.fence(t), mixed); !errors.Is(err, notifications.ErrEvaluationInvalid) {
			t.Fatal("mixed source proof accepted", field, err)
		}
	}
	if _, err = x.repo.CommitNotificationEvaluation(ctx, next, x.fence(t), x.authorized(t, next)); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationEvaluationCurrentRuleAndDeviceFences(t *testing.T) {
	for _, mode := range []string{"rename", "disable-one", "disable-all", "deny-interval", "scope-watermark", "principal-change", "device-switch", "device-refresh", "device-mute", "device-not-ready", "expired-proof", "missing-alias"} {
		t.Run(mode, func(t *testing.T) {
			x := evaluationSetup(t)
			ctx := t.Context()
			a := reportActor(t, x.s, 630)
			r1, r2 := x.rule(t, a), x.rule(t, a)
			d := deviceStore(t, x.s)
			registration, view := deviceBind(t, d, a, 1)
			x.event(t, 1)
			x.fanout(t)
			c := x.claim(t)
			decision := x.authorized(t, c)
			disable := func(rule notifications.Rule) {
				in := rule.RuleInput
				in.Enabled = false
				if _, err := x.s.UpdateNotificationRule(ctx, a, rule.ID, rule.Revision, in, notificationIntervals(t, in, notifications.ActivationDisabled)); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "rename":
				input := r1.RuleInput
				input.Name = "Renamed after original match"
				if _, err := x.s.UpdateNotificationRule(ctx, a, r1.ID, r1.Revision, input, r1.Activation); err != nil {
					t.Fatal(err)
				}
			case "disable-one":
				disable(r1)
			case "disable-all":
				disable(r1)
				disable(r2)
			case "deny-interval":
				if err := x.s.DisableNotificationActivations(ctx, a, r1.ID, r1.Revision, []string{r1.Activation[0].ID}); err != nil {
					t.Fatal(err)
				}
			case "scope-watermark":
				deviceExec(t, x.s, `INSERT INTO dashboard_notification_scope_revocations(deployment_id,namespace_id,revoked_through) VALUES($1::uuid,$2::uuid,clock_timestamp())`, x.cp.DeploymentID, x.cp.NamespaceIDs[0])
			case "principal-change":
				decision.Proof.PrincipalID = "70000000-0000-4000-8000-000000000099"
			case "device-switch":
				other := reportActor(t, x.s, 631)
				if _, err := attemptDeviceBind(ctx, d, other, view.Revision, registration, true); err != nil {
					t.Fatal(err)
				}
			case "device-refresh":
				if _, err := d.Refresh(ctx, a, registration.InstallationID, view.Revision, notifications.DeviceRefresh{InstallationSecret: registration.InstallationSecret, Token: registration.Token, Permission: "authorized"}); err != nil {
					t.Fatal(err)
				}
			case "device-mute":
				if _, err := d.Settings(ctx, a, registration.InstallationID, view.Revision, notifications.DeviceSettings{Label: view.Label, Enabled: true, Muted: true}); err != nil {
					t.Fatal(err)
				}
			case "device-not-ready":
				deviceExec(t, x.s, `DELETE FROM dashboard_notification_device_revocations WHERE target_binding_id=(SELECT current_binding_id FROM dashboard_notification_installations WHERE id=$1::uuid)`, registration.InstallationID)
			case "expired-proof":
				decision.Proof.ExpiresAt = x.now.Add(-time.Second)
			case "missing-alias":
				deviceExec(t, x.s, `DELETE FROM dashboard_identity_aliases WHERE issuer=$1 AND subject=$2`, a.Issuer, a.Subject)
			}
			got, err := x.repo.CommitNotificationEvaluation(ctx, c, x.fence(t), decision)
			if mode == "expired-proof" || mode == "missing-alias" {
				if err == nil {
					t.Fatal("stale authority committed")
				}
				if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox`); n != 0 {
					t.Fatal("stale authority produced inbox")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			suppressed := mode == "disable-all" || mode == "scope-watermark" || mode == "principal-change"
			if suppressed {
				if got.State != "suppressed" || got.InboxID != "" {
					t.Fatal(got)
				}
				if mode == "principal-change" && x.count(t, `SELECT count(*) FROM dashboard_audit WHERE account_id=$1::uuid AND action='notification_rule.activations_revoked' AND resource_kind='notification_rule' AND resource_id IN ($2,$3)`, a.Account.ID, r1.ID, r2.ID) != 2 {
					t.Fatal("canonical principal change omitted per-rule audit")
				}
				return
			}
			if got.State != "complete" {
				t.Fatal(got)
			}
			if mode == "device-switch" || mode == "device-mute" || mode == "device-not-ready" {
				if got.Deliveries != 0 {
					t.Fatal("ineligible binding queued", got)
				}
			} else if got.Deliveries != 1 {
				t.Fatal(got)
			}
			if mode == "device-refresh" && x.count(t, `SELECT count(*) FROM dashboard_notification_deliveries WHERE token_version=2`) != 1 {
				t.Fatal("old token version queued")
			}
			if mode == "rename" && x.count(t, `SELECT count(*) FROM dashboard_notification_inbox_matches WHERE rule_revision=1`) != 2 {
				t.Fatal("name edit replaced immutable matching context")
			}
			if mode == "disable-one" || mode == "deny-interval" {
				if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox_matches`); n != 1 {
					t.Fatal("revoked overlap persisted", n)
				}
			}
		})
	}
}

func TestNotificationEvaluationHoldCutoffsAndSourceGapFences(t *testing.T) {
	for _, mode := range []string{"hold", "hold-resume", "restore-floor", "feed-floor", "pruned-floor", "capacity", "compound-epoch", "scope-gap", "source-epoch", "stale-fence"} {
		t.Run(mode, func(t *testing.T) {
			x := evaluationSetup(t)
			ctx := t.Context()
			a := reportActor(t, x.s, 640)
			x.rule(t, a)
			e := x.event(t, 1)
			x.fanout(t)
			c := x.claim(t)
			f := x.fence(t)
			switch mode {
			case "hold", "hold-resume":
				state, err := x.s.NotificationDeliveryControl(ctx)
				if err != nil {
					t.Fatal(err)
				}
				held, err := x.s.HoldNotifications(ctx, state.Generation, nil)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "hold-resume" {
					if _, err = x.s.ResumeNotifications(ctx, held.Generation, 1, []events.Checkpoint{f.Checkpoint}); err != nil {
						t.Fatal(err)
					}
				}
			case "restore-floor":
				deviceExec(t, x.s, `UPDATE dashboard_notification_delivery_control SET generation=generation+1,restore_recorded_through=$1`, e.RecordedAt)
			case "feed-floor":
				deviceExec(t, x.s, `UPDATE dashboard_event_feeds SET suppress_recorded_through=$1`, e.RecordedAt)
			case "pruned-floor":
				deviceExec(t, x.s, `UPDATE dashboard_event_feeds SET pruned_recorded_through=$1`, e.RecordedAt)
			case "capacity", "compound-epoch", "scope-gap":
				reason := "capacity"
				if mode == "scope-gap" {
					reason = string(events.ScopeChanged)
				}
				if err := x.s.PauseFeed(ctx, x.feed, reason); err != nil {
					t.Fatal(err)
				}
				f = x.fence(t)
				if mode == "compound-epoch" {
					f.Checkpoint.RecoveryEpoch = "2"
					if err := x.s.VerifySourceIdentity(ctx, x.cp.DeploymentID, x.cp.ControlInstanceID, "2", 1); err != nil {
						t.Fatal(err)
					}
				}
			case "source-epoch":
				f.Checkpoint.RecoveryEpoch = "2"
				if err := x.s.VerifySourceIdentity(ctx, x.cp.DeploymentID, x.cp.ControlInstanceID, "2", 1); err != nil {
					t.Fatal(err)
				}
			case "stale-fence":
				f.VerifiedAt = x.now.Add(-time.Minute)
			}
			result, err := x.repo.CommitNotificationEvaluation(ctx, c, f, x.authorized(t, c))
			switch mode {
			case "capacity":
				if err != nil || result.State != "complete" {
					t.Fatal("pure capacity could not drain", result, err)
				}
			case "feed-floor", "pruned-floor":
				if err != nil || result.State != "suppressed" {
					t.Fatal("cutoff ignored", result, err)
				}
			default:
				if err == nil {
					t.Fatal("hold/source fence bypassed", result)
				}
			}
			if mode != "capacity" && x.count(t, `SELECT count(*) FROM dashboard_notification_inbox`) != 0 {
				t.Fatal("held source produced inbox")
			}
		})
	}
}

func TestNotificationEvaluationBacklogRetentionAndOriginalIdentityReplay(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	a := reportActor(t, x.s, 650)
	x.cp.AsOf = x.now.Add(-49 * time.Hour)
	x.rule(t, a)
	deviceBind(t, deviceStore(t, x.s), a, 1)
	event := sourceEvent(x.cp, 1)
	event.Outcome = "failure"
	event.RecordedAt = x.now.Add(-48 * time.Hour)
	if err := x.s.AppendFeed(ctx, x.feed, eventPage(x.cp, "old-event", event)); err != nil {
		t.Fatal(err)
	}
	var err error
	x.feed, err = x.s.ClaimFeed(ctx, x.cp.DeploymentID, x.cp.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	x.fanout(t)
	c := x.claim(t)
	result, err := x.repo.CommitNotificationEvaluation(ctx, c, x.fence(t), x.authorized(t, c))
	if err != nil || result.State != "complete" || result.Deliveries != 0 {
		t.Fatal("backlog did not remain inbox-only", result, err)
	}
	deviceExec(t, x.s, `UPDATE dashboard_source_events SET expires_at=clock_timestamp()-interval '1 day',last_seen_at=clock_timestamp()-interval '36 days'`)
	if n, err := x.s.PruneSourceEvents(ctx, x.cp.DeploymentID); err != nil || n != 1 {
		t.Fatal("resolved source dedup cleanup", n, err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_evaluations`); n != 0 {
		t.Fatal("dedup tombstone did not follow source retention", n)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox`); n != 1 {
		t.Fatal("source retention erased live inbox", n)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox_matches`); n != 1 {
		t.Fatal("source retention erased live inbox context", n)
	}
	event.Position = "2"
	if err = x.s.AppendFeed(ctx, x.feed, eventPage(x.cp, "republished-original", event)); err != nil {
		t.Fatal(err)
	}
	x.fanout(t)
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_evaluations`); n != 0 {
		t.Fatal("pruned floor reopened original identity", n)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox`); n != 1 {
		t.Fatal("republication duplicated inbox", n)
	}
}

func TestNotificationEvaluationHardCapsAndDisabledAccount(t *testing.T) {
	for _, mode := range []string{"event-account-cap", "delivery-cap", "disabled-account", "confirmed-denial", "nil-device-policy"} {
		t.Run(mode, func(t *testing.T) {
			x := evaluationSetup(t)
			ctx := t.Context()
			a := reportActor(t, x.s, 660)
			rule := x.rule(t, a)
			deviceBind(t, deviceStore(t, x.s), a, 1)
			x.event(t, 1)
			if mode == "event-account-cap" {
				claim, err := x.repo.ClaimNotificationFanout(ctx, x.fence(t))
				if err != nil {
					t.Fatal(err)
				}
				deviceExec(t, x.s, `UPDATE dashboard_notification_fanout SET account_count=10000`)
				if _, err = x.repo.AppendNotificationFanout(ctx, claim); !errors.Is(err, notifications.ErrEvaluationCapacity) {
					t.Fatal("event fanout cap bypassed", err)
				}
				if n := x.count(t, `SELECT count(*) FROM dashboard_notification_evaluations`); n != 0 {
					t.Fatal("event-cap failure partially inserted work", n)
				}
				return
			}
			x.fanout(t)
			if mode == "disabled-account" {
				deviceExec(t, x.s, `UPDATE dashboard_accounts SET disabled_at=clock_timestamp() WHERE id=$1::uuid`, a.Account.ID)
				if _, err := x.repo.ClaimNotificationEvaluation(ctx, x.fence(t)); !errors.Is(err, notifications.ErrEvaluationAdvanced) {
					t.Fatal("disabled account delegated", err)
				}
				if n := x.count(t, `SELECT count(*) FROM dashboard_notification_evaluations WHERE state='suppressed'`); n != 1 {
					t.Fatal("disabled account not resolved", n)
				}
				return
			}
			c := x.claim(t)
			decision := x.authorized(t, c)
			if mode == "nil-device-policy" {
				x.repo.devices = nil
			}
			if mode == "confirmed-denial" {
				decision = notifications.EvaluationDecision{Outcome: notifications.EvaluationInaccessible}
				deviceExec(t, x.s, `CREATE FUNCTION reject_test_denial_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit interruption'; END $$; CREATE TRIGGER reject_test_denial_audit BEFORE INSERT ON dashboard_audit FOR EACH ROW WHEN (NEW.action='notification_rule.activations_revoked') EXECUTE FUNCTION reject_test_denial_audit()`)
				if _, err := x.repo.CommitNotificationEvaluation(ctx, c, x.fence(t), decision); err == nil {
					t.Fatal("audit failure did not fail denial transaction")
				}
				if x.count(t, `SELECT count(*) FROM dashboard_notification_activation_revocations`) != 0 || x.count(t, `SELECT count(*) FROM dashboard_audit WHERE action='notification_rule.activations_revoked'`) != 0 || x.count(t, `SELECT count(*) FROM dashboard_notification_evaluations WHERE state='pending' AND lease_token=$1::uuid`, c.LeaseToken) != 1 || x.count(t, `SELECT pending_evaluations FROM dashboard_notification_work_quota`) != 1 || x.count(t, `SELECT count(*) FROM dashboard_source_events WHERE processed_at IS NOT NULL`) != 0 {
					t.Fatal("audit failure left a partial denial or resolved work")
				}
				deviceExec(t, x.s, `DROP TRIGGER reject_test_denial_audit ON dashboard_audit`)
			}
			if mode == "delivery-cap" {
				deviceExec(t, x.s, `UPDATE dashboard_notification_work_quota SET pending_deliveries=1000000`)
			}
			got, err := x.repo.CommitNotificationEvaluation(ctx, c, x.fence(t), decision)
			if mode == "delivery-cap" {
				if !errors.Is(err, notifications.ErrEvaluationCapacity) {
					t.Fatal("delivery cap bypassed", err)
				}
				if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox`); n != 0 {
					t.Fatal("partial inbox at delivery cap", n)
				}
				if n := x.count(t, `SELECT pending_evaluations FROM dashboard_notification_work_quota`); n != 1 {
					t.Fatal("capacity failure consumed account task", n)
				}
				deviceExec(t, x.s, `UPDATE dashboard_notification_work_quota SET pending_deliveries=0`)
				if _, err = x.repo.CommitNotificationEvaluation(ctx, c, x.fence(t), decision); err != nil {
					t.Fatal("capacity retry could not commit", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "confirmed-denial" {
				if got.State != "suppressed" {
					t.Fatal(got)
				}
				if n := x.count(t, `SELECT count(*) FROM dashboard_notification_activation_revocations WHERE activation_id=$1::uuid`, rule.Activation[0].ID); n != 1 {
					t.Fatal("confirmed denial did not fence interval", n)
				}
				if _, err := x.repo.CommitNotificationEvaluation(ctx, c, x.fence(t), decision); !errors.Is(err, notifications.ErrEvaluationLease) {
					t.Fatal("resolved denial replay accepted", err)
				}
				if err := x.s.DisableNotificationActivations(ctx, a, rule.ID, rule.Revision, []string{rule.Activation[0].ID}); err != nil {
					t.Fatal(err)
				}
				if n := x.count(t, `SELECT count(*) FROM dashboard_audit WHERE account_id=$1::uuid AND action='notification_rule.activations_revoked' AND resource_kind='notification_rule' AND resource_id=$2`, a.Account.ID, rule.ID); n != 1 {
					t.Fatal("confirmed denial audit absent or duplicated", n)
				}
			} else if got.State != "complete" || got.Deliveries != 0 {
				t.Fatal("nil provider policy changed inbox eligibility", got)
			}
		})
	}
}

func TestNotificationEvaluationProofExpiryDuringCommitRollsBack(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	a := reportActor(t, x.s, 670)
	x.rule(t, a)
	x.event(t, 1)
	x.fanout(t)
	c := x.claim(t)
	deviceExec(t, x.s, `CREATE SEQUENCE notification_proof_expiry_probe; CREATE FUNCTION delay_test_inbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM nextval('notification_proof_expiry_probe'); PERFORM pg_sleep(1.1); RETURN NEW; END $$; CREATE TRIGGER delay_test_inbox BEFORE INSERT ON dashboard_notification_inbox FOR EACH ROW EXECUTE FUNCTION delay_test_inbox()`)
	proof := x.authorized(t, c)
	proof.Proof.ExpiresAt = proof.Proof.CheckedAt.Add(time.Second)
	if _, err := x.repo.CommitNotificationEvaluation(ctx, c, x.fence(t), proof); !errors.Is(err, notifications.ErrEvaluationSource) {
		t.Fatal("expired proof published after lock/write delay", err)
	}
	var reached bool
	if err := x.s.Pool.QueryRow(ctx, `SELECT is_called FROM notification_proof_expiry_probe`).Scan(&reached); err != nil || !reached {
		t.Fatal("test did not reach final proof expiry fence", err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox`); n != 0 {
		t.Fatal("expired proof left partial inbox", n)
	}
	if n := x.count(t, `SELECT pending_evaluations FROM dashboard_notification_work_quota`); n != 1 {
		t.Fatal("expired proof resolved pending task", n)
	}
}

func TestNotificationEvaluationOriginalKeysStaySourceQualified(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	a := reportActor(t, x.s, 680)
	x.rule(t, a)
	other := x.cp
	other.DeploymentID = "20000000-0000-4000-8000-000000000002"
	other.ControlInstanceID = "30000000-0000-4000-8000-000000000002"
	feed := initializedFeed(t, x.s, other)
	if err := x.s.VerifySourceIdentity(ctx, other.DeploymentID, other.ControlInstanceID, other.RecoveryEpoch, 1); err != nil {
		t.Fatal(err)
	}
	y := &evaluationFixture{x.s, x.repo, other, feed, x.now}
	y.rule(t, a)
	e1, e2 := x.event(t, 1), y.event(t, 1)
	if e1.EventID != e2.EventID {
		t.Fatal("test requires colliding original UUID")
	}
	for _, f := range []*evaluationFixture{x, y} {
		f.fanout(t)
		c := f.claim(t)
		if _, err := f.repo.CommitNotificationEvaluation(ctx, c, f.fence(t), f.authorized(t, c)); err != nil {
			t.Fatal(err)
		}
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox`); n != 2 {
		t.Fatal("same UUID collapsed across sources", n)
	}
}

func TestNotificationEvaluationMissingAliasDefersWithoutBlockingOtherAccounts(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	a, b := reportActor(t, x.s, 690), reportActor(t, x.s, 691)
	x.rule(t, a)
	x.rule(t, b)
	x.event(t, 1)
	x.fanout(t)
	deviceExec(t, x.s, `DELETE FROM dashboard_identity_aliases WHERE issuer=$1 AND subject=$2`, a.Issuer, a.Subject)
	deviceExec(t, x.s, `UPDATE dashboard_notification_evaluations SET next_attempt_at=clock_timestamp()-interval '1 minute' WHERE account_id=$1::uuid`, a.Account.ID)
	if _, err := x.repo.ClaimNotificationEvaluation(ctx, x.fence(t)); !errors.Is(err, notifications.ErrEvaluationAdvanced) {
		t.Fatal("missing alias did not defer independently", err)
	}
	c := x.claim(t)
	if c.Actor.Account.ID != b.Account.ID {
		t.Fatal("healthy account was not independently claimable")
	}
	if _, err := x.repo.CommitNotificationEvaluation(ctx, c, x.fence(t), x.authorized(t, c)); err != nil {
		t.Fatal(err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_evaluations WHERE account_id=$1::uuid AND state='pending' AND next_attempt_at>clock_timestamp()`, a.Account.ID); n != 1 {
		t.Fatal("missing alias was treated as lost AD access", n)
	}
}

func TestNotificationEvaluationLocalStopNeedsNoRemoteProof(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	a := reportActor(t, x.s, 695)
	rule := x.rule(t, a)
	x.event(t, 1)
	x.fanout(t)
	in := rule.RuleInput
	in.Enabled = false
	if _, err := x.s.UpdateNotificationRule(ctx, a, rule.ID, rule.Revision, in, notificationIntervals(t, in, notifications.ActivationDisabled)); err != nil {
		t.Fatal(err)
	}
	if _, err := x.repo.ClaimNotificationEvaluation(ctx, x.fence(t)); !errors.Is(err, notifications.ErrEvaluationAdvanced) {
		t.Fatal("local stop required remote authorization", err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_evaluations WHERE state='suppressed'`); n != 1 {
		t.Fatal("local stop left pending work", n)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_source_events WHERE processed_at IS NOT NULL`); n != 1 {
		t.Fatal("local stop did not finish source work", n)
	}
}

func TestNotificationEvaluationResourceAbsenceDoesNotRevokeNamespace(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	a := reportActor(t, x.s, 696)
	rule := x.rule(t, a)
	x.event(t, 1)
	x.fanout(t)
	c := x.claim(t)
	result, err := x.repo.CommitNotificationEvaluation(ctx, c, x.fence(t), notifications.EvaluationDecision{Outcome: notifications.EvaluationResourceInaccessible})
	if err != nil || result.State != "suppressed" || result.InboxID != "" {
		t.Fatal("absent resource produced content", result, err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_activation_revocations WHERE activation_id=$1::uuid`, rule.Activation[0].ID); n != 0 {
		t.Fatal("job absence revoked entire namespace interval", n)
	}
	later := sourceEvent(x.cp, 2)
	later.Outcome = "failure"
	later.RecordedAt = x.now.Add(-20 * time.Second)
	later.JobID = "60000000-0000-4000-8000-000000000002"
	if err = x.s.AppendFeed(ctx, x.feed, eventPage(x.cp, "second-job-event", later)); err != nil {
		t.Fatal(err)
	}
	x.fanout(t)
	next := x.claim(t)
	if next.Event.JobID != later.JobID {
		t.Fatal("wrong subsequent job claimed")
	}
	result, err = x.repo.CommitNotificationEvaluation(ctx, next, x.fence(t), x.authorized(t, next))
	if err != nil || result.State != "complete" {
		t.Fatal("resource suppression disabled future namespace jobs", result, err)
	}
}
