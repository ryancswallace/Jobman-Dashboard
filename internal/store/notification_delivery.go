package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
	"github.com/ryancswallace/jobman-dashboard/internal/push"
)

type NotificationDeliveryStore struct {
	evaluation           *NotificationEvaluationStore
	devices              *NotificationDeviceStore
	topics, environments []string
}

func NewNotificationDeliveryStore(e *NotificationEvaluationStore, d *NotificationDeviceStore, providers []notifications.DeviceTopic) (*NotificationDeliveryStore, error) {
	if e == nil || d == nil || e.store != d.store || len(providers) < 1 || len(providers) > 16 {
		return nil, notifications.ErrEvaluationInvalid
	}
	s := &NotificationDeliveryStore{evaluation: e, devices: d}
	seen := map[notifications.DeviceTopic]bool{}
	for _, p := range providers {
		if !d.policy.Allows(p.Topic, p.Environment) || seen[p] {
			return nil, notifications.ErrEvaluationInvalid
		}
		seen[p] = true
		s.topics = append(s.topics, p.Topic)
		s.environments = append(s.environments, p.Environment)
	}
	return s, nil
}

var _ notifications.DeliveryRepository = (*NotificationDeliveryStore)(nil)

func readDeliveryInbox(ctx context.Context, tx pgx.Tx, id, account string) (events.Event, time.Time, error) {
	var e events.Event
	var expiry time.Time
	var payload []byte
	err := tx.QueryRow(ctx, `SELECT event_payload,expires_at FROM dashboard_notification_inbox WHERE id=$1::uuid AND account_id=$2::uuid FOR SHARE`, id, account).Scan(&payload, &expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, expiry, notifications.ErrEvaluationLease
	}
	if err != nil {
		return e, expiry, err
	}
	if json.Unmarshal(payload, &e) != nil || e.Validate() != nil {
		return e, expiry, notifications.ErrEvaluationInvalid
	}
	return e, expiry, nil
}

// Immutable inbox matches establish original intent; only surviving current
// intervals can justify a new provider handoff. Historical display is separate.
func currentDeliveryMatches(ctx context.Context, tx pgx.Tx, inbox, account string, e events.Event, principal string, deny bool) (int, error) {
	rows, err := tx.Query(ctx, `SELECT m.rule_id::text,v.account_id::text,m.rule_revision,m.activation_id::text,v.payload FROM dashboard_notification_inbox_matches m JOIN dashboard_notification_rule_versions v ON v.rule_id=m.rule_id AND v.revision=m.rule_revision WHERE m.inbox_id=$1::uuid ORDER BY m.rule_id LIMIT 101`, inbox)
	if err != nil {
		return 0, err
	}
	matches := []notifications.Match{}
	for rows.Next() {
		var rid, owner, activation string
		var rev int64
		var data []byte
		if err = rows.Scan(&rid, &owner, &rev, &activation, &data); err != nil {
			break
		}
		var r notifications.Rule
		r, err = decodeNotificationRule(data, rid, owner, rev)
		if err != nil {
			break
		}
		var m *notifications.Match
		m, err = notifications.MatchVersion(r, e)
		if err != nil || m == nil || m.ActivationID != activation || owner != account {
			err = notifications.ErrEvaluationInvalid
			break
		}
		matches = append(matches, *m)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(matches) == 0 || len(matches) > notifications.MaximumRulesPerAccount {
		return 0, notifications.ErrEvaluationInvalid
	}
	valid := 0
	for _, m := range matches {
		r, err := readNotificationRule(ctx, tx, account, m.RuleID)
		if errors.Is(err, notifications.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}
		applicable, err := notifications.StillApplicable(m, r)
		if err != nil {
			return 0, err
		}
		if !applicable {
			continue
		}
		var revoked bool
		err = tx.QueryRow(ctx, `SELECT r.activation_id IS NOT NULL OR COALESCE(a.origin_recorded_at<=n.revoked_through,false) FROM dashboard_notification_activations a LEFT JOIN dashboard_notification_activation_revocations r ON r.activation_id=a.id LEFT JOIN dashboard_notification_scope_revocations n ON n.deployment_id=a.deployment_id AND n.namespace_id=a.namespace_id WHERE a.id=$1::uuid`, m.ActivationID).Scan(&revoked)
		if err != nil {
			return 0, err
		}
		if revoked {
			continue
		}
		wrongPrincipal := false
		if principal != "" {
			for _, a := range r.Activation {
				if a.ID == m.ActivationID {
					wrongPrincipal = a.Boundary.PrincipalID != principal
					break
				}
			}
		}
		if deny || wrongPrincipal {
			tag, err := tx.Exec(ctx, `INSERT INTO dashboard_notification_activation_revocations(activation_id) VALUES($1::uuid) ON CONFLICT DO NOTHING`, m.ActivationID)
			if err != nil {
				return 0, err
			}
			if tag.RowsAffected() > 0 {
				if _, err = tx.Exec(ctx, `INSERT INTO dashboard_audit(account_id,action,resource_kind,resource_id) VALUES($1::uuid,'notification_rule.activations_revoked','notification_rule',$2)`, account, m.RuleID); err != nil {
					return 0, err
				}
			}
			continue
		}
		valid++
	}
	return valid, nil
}

func resolveDelivery(ctx context.Context, tx pgx.Tx, id, state string) error {
	tag, err := tx.Exec(ctx, `UPDATE dashboard_notification_deliveries SET state=$2,resolved_at=clock_timestamp(),revision=revision+1,lease_token=NULL,lease_expires_at=NULL,lease_hold_generation=NULL,lease_fence=NULL WHERE id=$1::uuid AND state='pending'`, id, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return notifications.ErrEvaluationLease
	}
	_, err = tx.Exec(ctx, `UPDATE dashboard_notification_work_quota SET pending_deliveries=pending_deliveries-1 WHERE singleton`)
	return err
}

func (s *NotificationDeliveryStore) ClaimNotificationDelivery(ctx context.Context, f notifications.SourceFence) (notifications.DeliveryClaim, error) {
	var c notifications.DeliveryClaim
	if f.Checkpoint.Validate() != nil {
		return c, notifications.ErrEvaluationSource
	}
	tx, g, err := s.evaluation.begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72108003010)`); err != nil {
		return c, err
	}
	var account string
	err = tx.QueryRow(ctx, `SELECT d.id::text,d.inbox_id::text,d.account_id::text,d.installation_id::text,d.binding_id::text,d.expires_at,d.attempts FROM dashboard_notification_deliveries d JOIN dashboard_notification_device_bindings b ON b.id=d.binding_id LEFT JOIN dashboard_notification_provider_health p ON p.topic=b.topic AND p.environment=b.environment WHERE d.deployment_id=$1::uuid AND d.state='pending' AND d.next_attempt_at<=clock_timestamp() AND (d.lease_token IS NULL OR d.lease_expires_at<=clock_timestamp()) AND (p.retry_not_before IS NULL OR p.retry_not_before<=clock_timestamp()) AND EXISTS(SELECT 1 FROM unnest($2::text[],$3::text[]) AS configured(topic,environment) WHERE configured.topic=b.topic AND configured.environment=b.environment) ORDER BY d.next_attempt_at,d.id LIMIT 1`, f.Checkpoint.DeploymentID, s.topics, s.environments).Scan(&c.ID, &c.InboxID, &account, &c.Device.InstallationID, &c.Device.BindingID, &c.ExpiresAt, &c.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, notifications.ErrEvaluationEmpty
	}
	if err != nil {
		return c, err
	}
	a, disabled, err := s.evaluation.actor(ctx, tx, account)
	if err != nil {
		return c, err
	}
	floor, err := s.evaluation.source(ctx, tx, g, f)
	if err != nil {
		return c, err
	}
	e, expiry, err := readDeliveryInbox(ctx, tx, c.InboxID, account)
	if err != nil {
		return c, err
	}
	resolve := func(state string) (notifications.DeliveryClaim, error) {
		if err = resolveDelivery(ctx, tx, c.ID, state); err != nil {
			return notifications.DeliveryClaim{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return notifications.DeliveryClaim{}, err
		}
		return notifications.DeliveryClaim{}, notifications.ErrEvaluationAdvanced
	}
	if !c.ExpiresAt.After(g.now) || !expiry.After(g.now) {
		return resolve("expired")
	}
	if disabled || suppressedEvent(e, false, floor) || e.DeploymentID != f.Checkpoint.DeploymentID || e.ControlInstanceID != f.Checkpoint.ControlInstanceID || !slices.Contains(f.Checkpoint.NamespaceIDs, e.NamespaceID) {
		return resolve("suppressed")
	}
	if c.Attempts >= notifications.MaximumDeliveryAttempts {
		return resolve("failed")
	}
	if a.Subject == "" {
		_, err = tx.Exec(ctx, `UPDATE dashboard_notification_deliveries SET next_attempt_at=clock_timestamp()+interval '60 seconds',revision=revision+1,lease_token=NULL,lease_expires_at=NULL,lease_hold_generation=NULL,lease_fence=NULL WHERE id=$1::uuid`, c.ID)
		if err != nil {
			return c, err
		}
		if err = tx.Commit(ctx); err != nil {
			return c, err
		}
		return notifications.DeliveryClaim{}, notifications.ErrEvaluationAdvanced
	}
	n, err := currentDeliveryMatches(ctx, tx, c.InboxID, account, e, "", false)
	if err != nil {
		return c, err
	}
	if n == 0 {
		return resolve("suppressed")
	}
	i, err := deliveryInstallation(ctx, tx, c.Device.InstallationID, false)
	if err != nil {
		return c, err
	}
	b, err := currentDevice(ctx, tx, i)
	if errors.Is(err, notifications.ErrDeviceNotFound) {
		return resolve("suppressed")
	}
	if err != nil {
		return c, err
	}
	if b.id != c.Device.BindingID || b.account != account || !s.devices.eligible(b) {
		return resolve("suppressed")
	}
	// Token rotation can redirect a retry within the same exact binding. Account
	// switching never transfers an old delivery to the new binding generation.
	c.Device.AccountID = account
	c.Device.TokenVersion = b.tokenVersion
	c.LeaseToken, err = newID()
	if err != nil {
		return c, err
	}
	encoded, err := json.Marshal(f)
	if err != nil {
		return c, err
	}
	err = tx.QueryRow(ctx, `UPDATE dashboard_notification_deliveries SET revision=revision+1,claims=claims+1,token_version=$3,lease_token=$2::uuid,lease_expires_at=clock_timestamp()+interval '60 seconds',lease_hold_generation=$4,lease_fence=$5 WHERE id=$1::uuid AND state='pending' AND (lease_token IS NULL OR lease_expires_at<=clock_timestamp()) RETURNING revision,claims`, c.ID, c.LeaseToken, b.tokenVersion, g.control.Generation, encoded).Scan(&c.Revision, &c.Claims)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, notifications.ErrEvaluationLease
	}
	if err != nil {
		return c, err
	}
	c.Actor = a
	c.Event = e
	c.Fence = f
	c.HoldGeneration = g.control.Generation
	return c, tx.Commit(ctx)
}

func validDeliveryClaim(c notifications.DeliveryClaim) bool {
	return eventUUID(c.ID) && eventUUID(c.InboxID) && eventUUID(c.LeaseToken) && c.Revision > 0 && c.HoldGeneration > 0 && c.Event.Validate() == nil && eventUUID(c.Actor.Account.ID) && c.Device.AccountID == c.Actor.Account.ID && eventUUID(c.Device.InstallationID) && eventUUID(c.Device.BindingID) && c.Device.TokenVersion > 0
}
func lockDeliveryClaim(ctx context.Context, tx pgx.Tx, c notifications.DeliveryClaim) (notifications.SourceFence, error) {
	var original notifications.SourceFence
	var data []byte
	var held, version, attempts int64
	var expires time.Time
	var inbox, account, installation, binding string
	err := tx.QueryRow(ctx, `SELECT inbox_id::text,account_id::text,installation_id::text,binding_id::text,token_version,lease_hold_generation,lease_fence,expires_at,attempts FROM dashboard_notification_deliveries WHERE id=$1::uuid AND revision=$2 AND lease_token=$3::uuid AND lease_expires_at>clock_timestamp() AND state='pending' FOR UPDATE`, c.ID, c.Revision, c.LeaseToken).Scan(&inbox, &account, &installation, &binding, &version, &held, &data, &expires, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return original, notifications.ErrEvaluationLease
	}
	if err != nil {
		return original, err
	}
	if inbox != c.InboxID || account != c.Actor.Account.ID || installation != c.Device.InstallationID || binding != c.Device.BindingID || version != c.Device.TokenVersion || held != c.HoldGeneration || !expires.Equal(c.ExpiresAt) || attempts != c.Attempts || json.Unmarshal(data, &original) != nil || !original.SameSource(c.Fence) {
		return original, notifications.ErrEvaluationLease
	}
	return original, nil
}

func (s *NotificationDeliveryStore) PrepareNotificationDelivery(ctx context.Context, c notifications.DeliveryClaim, f notifications.SourceFence, d notifications.EvaluationDecision) (notifications.DeliveryHandoff, error) {
	var result notifications.DeliveryHandoff
	if !validDeliveryClaim(c) || d.Outcome != notifications.EvaluationAuthorized && d.Outcome != notifications.EvaluationInaccessible && d.Outcome != notifications.EvaluationResourceInaccessible {
		return result, notifications.ErrEvaluationInvalid
	}
	tx, g, err := s.evaluation.begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if g.control.Generation != c.HoldGeneration {
		return result, notifications.ErrEvaluationLease
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72108003010)`); err != nil {
		return result, err
	}
	a, disabled, err := s.evaluation.actor(ctx, tx, c.Actor.Account.ID)
	if err != nil {
		return result, err
	}
	if !disabled && (a.DirectoryID != c.Actor.DirectoryID || a.Issuer != c.Actor.Issuer || a.Subject != c.Actor.Subject) {
		return result, notifications.ErrEvaluationLease
	}
	floor, err := s.evaluation.source(ctx, tx, g, f)
	if err != nil {
		return result, err
	}
	e, expiry, err := readDeliveryInbox(ctx, tx, c.InboxID, c.Actor.Account.ID)
	if err != nil {
		return result, err
	}
	original, err := lockDeliveryClaim(ctx, tx, c)
	if err != nil {
		return result, err
	}
	if !original.SameSource(f) || notifications.Key(e) != notifications.Key(c.Event) {
		return result, notifications.ErrEvaluationSource
	}
	resolve := func(state string) (notifications.DeliveryHandoff, error) {
		if err = resolveDelivery(ctx, tx, c.ID, state); err != nil {
			return result, err
		}
		if err = tx.Commit(ctx); err != nil {
			return result, err
		}
		return result, notifications.ErrEvaluationAdvanced
	}
	if !c.ExpiresAt.After(g.now) || !expiry.After(g.now) {
		return resolve("expired")
	}
	if disabled || suppressedEvent(e, false, floor) || !slices.Contains(f.Checkpoint.NamespaceIDs, e.NamespaceID) || d.Outcome == notifications.EvaluationResourceInaccessible {
		return resolve("suppressed")
	}
	if d.Outcome == notifications.EvaluationAuthorized && (d.Proof.Validate(g.now) != nil || d.Proof.AccountID != a.Account.ID || d.Proof.Namespace != (notifications.NamespaceRef{DeploymentID: e.DeploymentID, NamespaceID: e.NamespaceID}) || d.Proof.ControlInstanceID != f.Checkpoint.ControlInstanceID || d.Proof.RecoveryEpoch != f.Checkpoint.RecoveryEpoch) {
		return result, notifications.ErrEvaluationInvalid
	}
	n, err := currentDeliveryMatches(ctx, tx, c.InboxID, a.Account.ID, e, d.Proof.PrincipalID, d.Outcome == notifications.EvaluationInaccessible)
	if err != nil {
		return result, err
	}
	if n == 0 {
		return resolve("suppressed")
	}
	if d.Outcome != notifications.EvaluationAuthorized {
		return result, notifications.ErrEvaluationInvalid
	}
	i, err := deliveryInstallation(ctx, tx, c.Device.InstallationID, false)
	if err != nil {
		return result, err
	}
	b, err := currentDevice(ctx, tx, i)
	if errors.Is(err, notifications.ErrDeviceNotFound) {
		return resolve("suppressed")
	}
	if err != nil {
		return result, err
	}
	if b.id != c.Device.BindingID || b.account != a.Account.ID || !s.devices.eligible(b) {
		return resolve("suppressed")
	}
	if b.tokenVersion != c.Device.TokenVersion {
		return result, notifications.ErrEvaluationLease
	}
	var providerHeld bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_notification_provider_health WHERE topic=$1 AND environment=$2 AND retry_not_before>clock_timestamp())`, b.view.Topic, b.view.Environment).Scan(&providerHeld); err != nil {
		return result, err
	}
	if providerHeld {
		return result, notifications.ErrEvaluationHeld
	}
	token, err := s.devices.cipher.Open(notifications.DeviceTokenBinding{InstallationID: i.id, BindingID: b.id, AccountID: b.account, Topic: b.view.Topic, Environment: b.view.Environment, TokenVersion: b.tokenVersion}, notifications.EncryptedDeviceToken{KeyID: b.keyID, Ciphertext: b.ciphertext})
	if err != nil {
		return result, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return result, err
	}
	if d.Proof.Validate(now) != nil || f.Validate(now) != nil || !c.ExpiresAt.After(now) || !expiry.After(now) {
		return result, notifications.ErrEvaluationSource
	}
	var attempts int64
	err = tx.QueryRow(ctx, `UPDATE dashboard_notification_deliveries SET attempts=attempts+1 WHERE id=$1::uuid AND revision=$2 AND lease_token=$3::uuid AND lease_expires_at>clock_timestamp() AND attempts=$4 AND attempts<128 RETURNING attempts`, c.ID, c.Revision, c.LeaseToken, c.Attempts).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, notifications.ErrEvaluationLease
	}
	if err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_delivery_attempts(delivery_id,number,token_version) VALUES($1::uuid,$2,$3)`, c.ID, attempts, b.tokenVersion); err != nil {
		return result, err
	}
	// A write may wait after the first freshness check. Do not publish a token
	// handoff or retain an attempt when authority expires during that wait.
	var live bool
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp(),lease_expires_at>clock_timestamp() FROM dashboard_notification_deliveries WHERE id=$1::uuid`, c.ID).Scan(&now, &live); err != nil {
		return result, err
	}
	if !live {
		return result, notifications.ErrEvaluationLease
	}
	if d.Proof.Validate(now) != nil || f.Validate(now) != nil || !c.ExpiresAt.After(now) || !expiry.After(now) {
		return result, notifications.ErrEvaluationSource
	}
	c.Attempts = attempts
	result = notifications.DeliveryHandoff{Claim: c, Device: notifications.DeviceHandoff{DeviceCandidate: c.Device, Topic: b.view.Topic, Environment: b.view.Environment, Token: token, RegisteredAt: b.registered}, AuthorizationExpiresAt: d.Proof.ExpiresAt}
	return result, tx.Commit(ctx)
}

// Deferral publishes nothing and must remain possible during an operator hold.
// Exact live lease fencing prevents a late worker from delaying its successor.
func (s *NotificationDeliveryStore) DeferNotificationDelivery(ctx context.Context, c notifications.DeliveryClaim) error {
	if !validDeliveryClaim(c) {
		return notifications.ErrEvaluationInvalid
	}
	tag, err := s.evaluation.store.Pool.Exec(ctx, `UPDATE dashboard_notification_deliveries SET next_attempt_at=clock_timestamp()+$4*interval '1 second',revision=revision+1,lease_token=NULL,lease_expires_at=NULL,lease_hold_generation=NULL,lease_fence=NULL WHERE id=$1::uuid AND revision=$2 AND lease_token=$3::uuid AND lease_expires_at>clock_timestamp() AND state='pending'`, c.ID, c.Revision, c.LeaseToken, int64(notifications.EvaluationRetryDelay(c.Claims)/time.Second))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return notifications.ErrEvaluationLease
	}
	return nil
}

// Provider acknowledgements record a handoff that already happened. A later
// operator hold or access removal cannot erase that fact; neither permits a new
// send. Only the exact live attempt/lease can resolve the pending delivery.
func (s *NotificationDeliveryStore) FinishNotificationDelivery(ctx context.Context, h notifications.DeliveryHandoff, r push.Result) error {
	c := h.Claim
	if !validDeliveryClaim(c) || c.Attempts < 1 || c.Attempts > notifications.MaximumDeliveryAttempts || h.Device.DeviceCandidate != c.Device {
		return notifications.ErrEvaluationInvalid
	}
	if !validProviderResult(c.ID, r) {
		return notifications.ErrEvaluationInvalid
	}
	tx, err := s.evaluation.store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pending_deliveries FROM dashboard_notification_work_quota WHERE singleton FOR UPDATE`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72108003010)`); err != nil {
		return err
	}
	if _, err = lockDeliveryClaim(ctx, tx, c); err != nil {
		return err
	}
	var topic, environment string
	if err = tx.QueryRow(ctx, `SELECT topic,environment FROM dashboard_notification_device_bindings WHERE id=$1::uuid AND installation_id=$2::uuid AND account_id=$3::uuid`, c.Device.BindingID, c.Device.InstallationID, c.Actor.Account.ID).Scan(&topic, &environment); err != nil {
		return err
	}
	if h.Device.Topic != topic || h.Device.Environment != environment {
		return notifications.ErrEvaluationInvalid
	}
	if r.Outcome == "token_invalid" {
		if _, err = s.devices.invalidateTx(ctx, tx, c.Device, r.TokenInvalidAt); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE dashboard_notification_delivery_attempts SET completed_at=clock_timestamp(),outcome=$3,reason=$4,provider_id=NULLIF($5,'')::uuid,ambiguous=$6 WHERE delivery_id=$1::uuid AND number=$2 AND token_version=$7 AND outcome='unknown'`, c.ID, c.Attempts, r.Outcome, r.Reason, r.ProviderID, r.Ambiguous, c.Device.TokenVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return notifications.ErrEvaluationLease
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	state := ""
	switch r.Outcome {
	case "accepted":
		state = "accepted"
	case "token_invalid", "rejected":
		state = "failed"
	}
	if state == "" && (!c.ExpiresAt.After(now) || c.Attempts >= notifications.MaximumDeliveryAttempts) {
		state = "expired"
		if c.Attempts >= notifications.MaximumDeliveryAttempts {
			state = "failed"
		}
	}
	if r.Outcome == "provider_error" {
		delay := max(15*time.Minute, r.RetryAfter)
		_, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_provider_health(topic,environment,retry_not_before,last_failure_at,last_reason,failures) VALUES($1,$2,clock_timestamp()+$3*interval '1 second',clock_timestamp(),$4,1) ON CONFLICT(topic,environment) DO UPDATE SET retry_not_before=GREATEST(dashboard_notification_provider_health.retry_not_before,excluded.retry_not_before),last_failure_at=excluded.last_failure_at,last_reason=excluded.last_reason,failures=dashboard_notification_provider_health.failures+1`, h.Device.Topic, h.Device.Environment, int64(delay/time.Second), r.Reason)
		if err != nil {
			return err
		}
	}
	if state != "" {
		err = resolveDelivery(ctx, tx, c.ID, state)
	} else {
		delay := max(notifications.DeliveryRetryDelay(c.Attempts), r.RetryAfter)
		_, err = tx.Exec(ctx, `UPDATE dashboard_notification_deliveries SET next_attempt_at=clock_timestamp()+$2*interval '1 second',revision=revision+1,lease_token=NULL,lease_expires_at=NULL,lease_hold_generation=NULL,lease_fence=NULL WHERE id=$1::uuid`, c.ID, int64(delay/time.Second))
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func validProviderResult(id string, r push.Result) bool {
	if r.ProviderID != "" && r.ProviderID != id || r.RetryAfter < 0 || r.RetryAfter > time.Hour || r.TokenInvalidAt != nil && (r.TokenInvalidAt.IsZero() || r.TokenInvalidAt.Year() < 1 || r.TokenInvalidAt.Year() > 9999) {
		return false
	}
	switch r.Outcome {
	case "accepted":
		return r.ProviderID == id && r.Reason == "" && !r.Ambiguous
	case "retry", "token_invalid", "rejected", "provider_error":
	default:
		return false
	}
	// Never persist arbitrary upstream strings, even from another provider adapter.
	switch r.Reason {
	case "transport_unavailable", "invalid_provider_response", "provider_response", "provider_unavailable", "BadDeviceToken", "DeviceTokenNotForTopic", "ExpiredToken", "Unregistered", "IdleTimeout", "TooManyRequests", "TooManyProviderTokenUpdates", "ExpiredProviderToken", "InvalidProviderToken", "MissingProviderToken", "BadTopic", "TopicDisallowed", "Forbidden", "BadCertificate", "BadCertificateEnvironment", "PayloadTooLarge", "BadCollapseId", "BadExpirationDate", "BadMessageId", "BadPriority", "BadPath", "InvalidPushType", "MissingDeviceToken", "MissingTopic", "DuplicateHeaders", "MethodNotAllowed":
		return true
	}
	return false
}

// Expiry is local retention work: a source outage or global hold cannot keep a
// dead push attempt in pending quota forever. No inbox fact is deleted here.
func (s *Store) ExpireNotificationDeliveries(ctx context.Context) (int64, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pending_deliveries FROM dashboard_notification_work_quota WHERE singleton FOR UPDATE`); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `UPDATE dashboard_notification_deliveries SET state='expired',resolved_at=clock_timestamp(),revision=revision+1,lease_token=NULL,lease_expires_at=NULL,lease_hold_generation=NULL,lease_fence=NULL WHERE id IN (SELECT id FROM dashboard_notification_deliveries WHERE state='pending' AND expires_at<=clock_timestamp() AND (lease_token IS NULL OR lease_expires_at<=clock_timestamp()) ORDER BY expires_at,id LIMIT 500 FOR UPDATE SKIP LOCKED)`)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_work_quota SET pending_deliveries=pending_deliveries-$1 WHERE singleton`, tag.RowsAffected()); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}
