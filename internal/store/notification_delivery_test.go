package store

import (
	"errors"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
	"github.com/ryancswallace/jobman-dashboard/internal/push"
)

type deliveryFixture struct {
	*evaluationFixture
	d            *NotificationDeviceStore
	delivery     *NotificationDeliveryStore
	actor        monitoring.Actor
	rule         notifications.Rule
	registration notifications.DeviceRegistration
	view         notifications.DeviceView
}

func deliverySetup(t *testing.T) *deliveryFixture {
	t.Helper()
	x := evaluationSetup(t)
	a := reportActor(t, x.s, 800)
	r := x.rule(t, a)
	d := deviceStore(t, x.s)
	registration, view := deviceBind(t, d, a, 1)
	repo, err := NewNotificationDeliveryStore(x.repo, d, []notifications.DeviceTopic{{Topic: registration.Topic, Environment: registration.Environment}})
	if err != nil {
		t.Fatal(err)
	}
	x.event(t, 1)
	x.fanout(t)
	c := x.claim(t)
	result, err := x.repo.CommitNotificationEvaluation(t.Context(), c, x.fence(t), x.authorized(t, c))
	if err != nil || result.Deliveries != 1 {
		t.Fatal(result, err)
	}
	return &deliveryFixture{x, d, repo, a, r, registration, view}
}
func (x *deliveryFixture) claimDelivery(t *testing.T) notifications.DeliveryClaim {
	t.Helper()
	c, err := x.delivery.ClaimNotificationDelivery(t.Context(), x.fence(t))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func (x *deliveryFixture) proof(t *testing.T, c notifications.DeliveryClaim) notifications.EvaluationDecision {
	return x.authorized(t, notifications.EvaluationClaim{Actor: c.Actor, Event: c.Event, Fence: c.Fence})
}
func (x *deliveryFixture) prepare(t *testing.T, c notifications.DeliveryClaim) notifications.DeliveryHandoff {
	t.Helper()
	h, err := x.delivery.PrepareNotificationDelivery(t.Context(), c, x.fence(t), x.proof(t, c))
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func (x *deliveryFixture) due(t *testing.T) {
	deviceExec(t, x.s, `UPDATE dashboard_notification_deliveries SET next_attempt_at=clock_timestamp() WHERE state='pending'`)
}

func TestNotificationDeliveryStableAttemptsReplayAndAtomicAcknowledgement(t *testing.T) {
	x := deliverySetup(t)
	ctx := t.Context()
	c := x.claimDelivery(t)
	h := x.prepare(t, c)
	if h.Device.Token != x.registration.Token || h.Claim.Attempts != 1 {
		t.Fatal("wrong handoff")
	}
	if _, err := x.delivery.PrepareNotificationDelivery(ctx, c, x.fence(t), x.proof(t, c)); !errors.Is(err, notifications.ErrEvaluationLease) {
		t.Fatal("same attempt prepared twice", err)
	}
	// Simulate process loss after provider handoff without fabricating an ack.
	deviceExec(t, x.s, `UPDATE dashboard_notification_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second'`)
	next := x.claimDelivery(t)
	if next.ID != c.ID || next.InboxID != c.InboxID || next.LeaseToken == c.LeaseToken {
		t.Fatal("unstable retry identity")
	}
	if err := x.delivery.FinishNotificationDelivery(ctx, h, push.Result{Outcome: "accepted", ProviderID: c.ID}); !errors.Is(err, notifications.ErrEvaluationLease) {
		t.Fatal("stale ack accepted", err)
	}
	h = x.prepare(t, next)
	deviceExec(t, x.s, `CREATE FUNCTION reject_delivery_resolution() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic resolution interruption'; END $$; CREATE TRIGGER reject_delivery_resolution BEFORE UPDATE OF state ON dashboard_notification_deliveries FOR EACH ROW EXECUTE FUNCTION reject_delivery_resolution()`)
	if err := x.delivery.FinishNotificationDelivery(ctx, h, push.Result{Outcome: "accepted", ProviderID: c.ID}); err == nil {
		t.Fatal("injected failure absent")
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_delivery_attempts WHERE outcome!='unknown'`); n != 0 {
		t.Fatal("partial ack survived", n)
	}
	deviceExec(t, x.s, `DROP TRIGGER reject_delivery_resolution ON dashboard_notification_deliveries`)
	if err := x.delivery.FinishNotificationDelivery(ctx, h, push.Result{Outcome: "accepted", ProviderID: c.ID}); err != nil {
		t.Fatal(err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_delivery_attempts WHERE outcome='unknown' AND ambiguous`); n != 1 {
		t.Fatal("crash ambiguity erased", n)
	}
	if n := x.count(t, `SELECT pending_deliveries FROM dashboard_notification_work_quota`); n != 0 {
		t.Fatal("resolved quota leaked", n)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_deliveries WHERE state='accepted' AND attempts=2`); n != 1 {
		t.Fatal("acceptance missing", n)
	}
}

func TestNotificationDeliveryRetryProviderCooldownAndTokenRotation(t *testing.T) {
	x := deliverySetup(t)
	ctx := t.Context()
	c := x.claimDelivery(t)
	h := x.prepare(t, c)
	if err := x.delivery.FinishNotificationDelivery(ctx, h, push.Result{Outcome: "retry", Reason: "transport_unavailable", RetryAfter: time.Minute, Ambiguous: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.delivery.ClaimNotificationDelivery(ctx, x.fence(t)); !errors.Is(err, notifications.ErrEvaluationEmpty) {
		t.Fatal("retry ignored delay", err)
	}
	v, err := x.d.Refresh(ctx, x.actor, x.registration.InstallationID, x.view.Revision, notifications.DeviceRefresh{InstallationSecret: x.registration.InstallationSecret, Token: "abcd1234", Permission: "authorized"})
	if err != nil {
		t.Fatal(err)
	}
	x.view = v
	x.due(t)
	c = x.claimDelivery(t)
	if c.Device.TokenVersion != 2 {
		t.Fatal("retry did not follow same binding rotation")
	}
	h = x.prepare(t, c)
	if h.Device.Token != "abcd1234" {
		t.Fatal("old token returned")
	}
	if err = x.delivery.FinishNotificationDelivery(ctx, h, push.Result{Outcome: "provider_error", Reason: "InvalidProviderToken", RetryAfter: 15 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	// A provider configuration failure also pauses newly enqueued work for
	// another device using that topic/environment, not just this retry.
	deviceBind(t, x.d, x.actor, 2)
	x.event(t, 2)
	x.fanout(t)
	evaluation := x.claim(t)
	result, err := x.repo.CommitNotificationEvaluation(ctx, evaluation, x.fence(t), x.authorized(t, evaluation))
	if err != nil || result.Deliveries != 2 {
		t.Fatal("new device did not enqueue independently", result, err)
	}
	x.due(t)
	if _, err = x.delivery.ClaimNotificationDelivery(ctx, x.fence(t)); !errors.Is(err, notifications.ErrEvaluationEmpty) {
		t.Fatal("provider circuit ignored", err)
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_provider_health SET retry_not_before=clock_timestamp()-interval '1 second'`)
	c = x.claimDelivery(t)
	h = x.prepare(t, c)
	if err = x.delivery.FinishNotificationDelivery(ctx, h, push.Result{Outcome: "accepted", ProviderID: c.ID}); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationDeliveryProofExpiryDuringHandoffRollsBack(t *testing.T) {
	x := deliverySetup(t)
	c := x.claimDelivery(t)
	deviceExec(t, x.s, `CREATE SEQUENCE delivery_expiry_probe; CREATE FUNCTION delay_delivery_attempt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM nextval('delivery_expiry_probe'); PERFORM pg_sleep(1.1); RETURN NEW; END $$; CREATE TRIGGER delay_delivery_attempt BEFORE INSERT ON dashboard_notification_delivery_attempts FOR EACH ROW EXECUTE FUNCTION delay_delivery_attempt()`)
	p := x.proof(t, c)
	p.Proof.ExpiresAt = p.Proof.CheckedAt.Add(time.Second)
	h, err := x.delivery.PrepareNotificationDelivery(t.Context(), c, x.fence(t), p)
	if !errors.Is(err, notifications.ErrEvaluationSource) || h.Device.Token != "" {
		t.Fatal("expired proof admitted a handoff", err)
	}
	var reached bool
	if err = x.s.Pool.QueryRow(t.Context(), `SELECT is_called FROM delivery_expiry_probe`).Scan(&reached); err != nil || !reached {
		t.Fatal("test did not reach delayed write", err)
	}
	if x.count(t, `SELECT count(*) FROM dashboard_notification_delivery_attempts`) != 0 || x.count(t, `SELECT attempts FROM dashboard_notification_deliveries`) != 0 {
		t.Fatal("rejected handoff left a partial attempt")
	}
	if x.count(t, `SELECT pending_deliveries FROM dashboard_notification_work_quota`) != 1 {
		t.Fatal("rejected handoff resolved pending work")
	}
}

func TestNotificationDeliveryFinalAccessDeviceAndRestoreFences(t *testing.T) {
	for _, mode := range []string{"disable-rule", "device-muted", "device-removed", "device-switched", "credential-pending", "token-rotated", "hold", "hold-generation", "source-epoch", "proof-expired", "principal-changed", "namespace-denied", "job-missing", "restore-floor", "alias-removed", "account-disabled", "forged-expiry", "forged-attempt"} {
		t.Run(mode, func(t *testing.T) {
			x := deliverySetup(t)
			ctx := t.Context()
			c := x.claimDelivery(t)
			p := x.proof(t, c)
			f := x.fence(t)
			expectErr := false
			switch mode {
			case "disable-rule":
				in := x.rule.RuleInput
				in.Enabled = false
				if _, err := x.s.UpdateNotificationRule(ctx, x.actor, x.rule.ID, x.rule.Revision, in, notificationIntervals(t, in, notifications.ActivationDisabled)); err != nil {
					t.Fatal(err)
				}
			case "device-muted":
				if _, err := x.d.Settings(ctx, x.actor, x.registration.InstallationID, x.view.Revision, notifications.DeviceSettings{Label: x.view.Label, Enabled: true, Muted: true}); err != nil {
					t.Fatal(err)
				}
			case "device-removed":
				if err := x.d.Remove(ctx, x.actor, x.registration.InstallationID, x.view.Revision); err != nil {
					t.Fatal(err)
				}
			case "device-switched":
				other := reportActor(t, x.s, 801)
				if _, err := attemptDeviceBind(ctx, x.d, other, x.view.Revision, x.registration, true); err != nil {
					t.Fatal(err)
				}
			case "credential-pending":
				in := revocationInput(x.registration)
				in.RevocationID, _ = newID()
				if _, err := x.d.ReserveRevocation(ctx, x.actor, x.registration.InstallationID, x.view.Revision, in); err != nil {
					t.Fatal(err)
				}
			case "token-rotated":
				if _, err := x.d.Refresh(ctx, x.actor, x.registration.InstallationID, x.view.Revision, notifications.DeviceRefresh{InstallationSecret: x.registration.InstallationSecret, Token: "abcd1234", Permission: "authorized"}); err != nil {
					t.Fatal(err)
				}
				expectErr = true
			case "hold":
				state, err := x.s.NotificationDeliveryControl(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = x.s.HoldNotifications(ctx, state.Generation, nil); err != nil {
					t.Fatal(err)
				}
				expectErr = true
			case "hold-generation":
				deviceExec(t, x.s, `UPDATE dashboard_notification_delivery_control SET generation=generation+2`)
				expectErr = true
			case "source-epoch":
				f.Checkpoint.RecoveryEpoch = "2"
				expectErr = true
			case "proof-expired":
				p.Proof.ExpiresAt = x.now.Add(-time.Second)
				expectErr = true
			case "principal-changed":
				p.Proof.PrincipalID = "70000000-0000-4000-8000-000000000099"
			case "namespace-denied":
				p.Outcome = notifications.EvaluationInaccessible
				p.Proof = notifications.AuthorizationProof{}
			case "job-missing":
				p.Outcome = notifications.EvaluationResourceInaccessible
				p.Proof = notifications.AuthorizationProof{}
			case "restore-floor":
				deviceExec(t, x.s, `UPDATE dashboard_event_feeds SET suppress_recorded_through=clock_timestamp()`)
			case "alias-removed":
				deviceExec(t, x.s, `DELETE FROM dashboard_identity_aliases WHERE account_id=$1::uuid`, x.actor.Account.ID)
				expectErr = true
			case "account-disabled":
				deviceExec(t, x.s, `UPDATE dashboard_accounts SET disabled_at=clock_timestamp() WHERE id=$1::uuid`, x.actor.Account.ID)
			case "forged-expiry":
				c.ExpiresAt = c.ExpiresAt.Add(time.Hour)
				expectErr = true
			case "forged-attempt":
				c.Attempts++
				expectErr = true
			}
			h, err := x.delivery.PrepareNotificationDelivery(ctx, c, f, p)
			if expectErr {
				if err == nil || errors.Is(err, notifications.ErrEvaluationAdvanced) {
					t.Fatal("fence not rejected", err)
				}
			} else if !errors.Is(err, notifications.ErrEvaluationAdvanced) {
				t.Fatal("ineligible delivery not resolved", err)
			}
			if h.Device.Token != "" || x.count(t, `SELECT count(*) FROM dashboard_notification_delivery_attempts`) != 0 {
				t.Fatal("ineligible provider handoff prepared")
			}
			if mode == "namespace-denied" || mode == "principal-changed" {
				if n := x.count(t, `SELECT count(*) FROM dashboard_notification_activation_revocations`); n != 1 {
					t.Fatal("lost interval not revoked")
				}
			}
			if mode == "job-missing" {
				if n := x.count(t, `SELECT count(*) FROM dashboard_notification_activation_revocations`); n != 0 {
					t.Fatal("missing job revoked namespace")
				}
			}
		})
	}
}

func TestNotificationDeliveryTokenInvalidationIsAtomicAndVersionFenced(t *testing.T) {
	for _, mode := range []string{"current", "old-410", "rotated", "audit-rollback"} {
		t.Run(mode, func(t *testing.T) {
			x := deliverySetup(t)
			ctx := t.Context()
			c := x.claimDelivery(t)
			h := x.prepare(t, c)
			invalidAt := h.Device.RegisteredAt.Add(time.Millisecond)
			if mode == "old-410" {
				invalidAt = h.Device.RegisteredAt.Add(-time.Second)
			}
			if mode == "rotated" {
				if _, err := x.d.Refresh(ctx, x.actor, x.registration.InstallationID, x.view.Revision, notifications.DeviceRefresh{InstallationSecret: x.registration.InstallationSecret, Token: x.registration.Token, Permission: "authorized"}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "audit-rollback" {
				deviceExec(t, x.s, `CREATE FUNCTION reject_delivery_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='notification_device.token_invalidated' THEN RAISE EXCEPTION 'synthetic audit interruption'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_delivery_audit BEFORE INSERT ON dashboard_audit FOR EACH ROW EXECUTE FUNCTION reject_delivery_audit()`)
			}
			err := x.delivery.FinishNotificationDelivery(ctx, h, push.Result{Outcome: "token_invalid", Reason: "Unregistered", ProviderID: c.ID, TokenInvalidAt: &invalidAt})
			if mode == "audit-rollback" {
				if err == nil {
					t.Fatal("audit failure absent")
				}
				if x.count(t, `SELECT count(*) FROM dashboard_notification_delivery_attempts WHERE outcome!='unknown'`) != 0 {
					t.Fatal("partial acknowledgement")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			n := x.count(t, `SELECT count(*) FROM dashboard_notification_device_bindings WHERE token_invalidated_at IS NOT NULL`)
			if mode == "current" && n != 1 || mode != "current" && n != 0 {
				t.Fatal("invalid token fence", mode, n)
			}
		})
	}
}

func TestNotificationDeliveryAcknowledgementSurvivesLaterHold(t *testing.T) {
	x := deliverySetup(t)
	ctx := t.Context()
	c := x.claimDelivery(t)
	h := x.prepare(t, c)
	state, err := x.s.NotificationDeliveryControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = x.s.HoldNotifications(ctx, state.Generation, nil); err != nil {
		t.Fatal(err)
	}
	if err = x.delivery.FinishNotificationDelivery(ctx, h, push.Result{Outcome: "accepted", ProviderID: c.ID}); err != nil {
		t.Fatal("hold erased actual prior acceptance", err)
	}
}

func TestNotificationDeliveryExpiryPrunesLocallyDuringHold(t *testing.T) {
	ctx := t.Context()
	y := deliverySetup(t)
	state, err := y.s.NotificationDeliveryControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = y.s.HoldNotifications(ctx, state.Generation, nil); err != nil {
		t.Fatal(err)
	}
	deviceExec(t, y.s, `UPDATE dashboard_notification_deliveries SET expires_at=clock_timestamp()-interval '1 second'`)
	if n, err := y.s.ExpireNotificationDeliveries(ctx); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if n := y.count(t, `SELECT pending_deliveries FROM dashboard_notification_work_quota`); n != 0 {
		t.Fatal("expired quota leaked")
	}
	if n := y.count(t, `SELECT count(*) FROM dashboard_notification_inbox`); n != 1 {
		t.Fatal("push expiry erased inbox")
	}
}
