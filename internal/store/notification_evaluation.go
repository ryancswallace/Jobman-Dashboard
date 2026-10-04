package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

// NotificationEvaluationStore performs only bounded SQL/domain work. All remote
// authorization/source requests belong to the caller, outside transactions.
type NotificationEvaluationStore struct {
	store   *Store
	issuer  string
	devices *notifications.DevicePolicy
}

func NewNotificationEvaluationStore(s *Store, issuer string, devices *notifications.DevicePolicy) (*NotificationEvaluationStore, error) {
	if s == nil || s.Pool == nil || issuer == "" || len(issuer) > 512 {
		return nil, notifications.ErrEvaluationInvalid
	}
	return &NotificationEvaluationStore{s, issuer, devices}, nil
}

var _ notifications.EvaluationRepository = (*NotificationEvaluationStore)(nil)

type evaluationGate struct {
	control                 DeliveryControl
	now                     time.Time
	evaluations, deliveries int
}

func (s *NotificationEvaluationStore) begin(ctx context.Context) (pgx.Tx, evaluationGate, error) {
	var g evaluationGate
	tx, err := s.store.Pool.Begin(ctx)
	if err != nil {
		return nil, g, err
	}
	fail := func(err error) (pgx.Tx, evaluationGate, error) { tx.Rollback(ctx); return nil, g, err }
	err = tx.QueryRow(ctx, `SELECT generation,held,restore_recorded_through,clock_timestamp() FROM dashboard_notification_delivery_control WHERE singleton FOR SHARE`).Scan(&g.control.Generation, &g.control.Held, &g.control.RestoreRecordedThrough, &g.now)
	if err != nil {
		return fail(err)
	}
	if g.control.Held {
		return fail(notifications.ErrEvaluationHeld)
	}
	// All work admission/resolve paths use this row before any account/feed/work
	// locks. No network or provider operation can run while it is held.
	err = tx.QueryRow(ctx, `SELECT pending_evaluations,pending_deliveries FROM dashboard_notification_work_quota WHERE singleton FOR UPDATE`).Scan(&g.evaluations, &g.deliveries)
	if err != nil {
		return fail(err)
	}
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&g.now); err != nil {
		return fail(err)
	}
	return tx, g, nil
}
func (s *NotificationEvaluationStore) source(ctx context.Context, tx pgx.Tx, g evaluationGate, f notifications.SourceFence) (*time.Time, error) {
	if f.Validate(g.now) != nil {
		return nil, notifications.ErrEvaluationSource
	}
	var status string
	var generation int64
	var data []byte
	var namespaces []string
	var floor *time.Time
	err := tx.QueryRow(ctx, `SELECT status,generation,checkpoint,namespace_ids::text[],GREATEST(suppress_recorded_through,pruned_recorded_through) FROM dashboard_event_feeds WHERE deployment_id=$1::uuid FOR SHARE`, f.Checkpoint.DeploymentID).Scan(&status, &generation, &data, &namespaces, &floor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notifications.ErrEvaluationSource
	}
	if err != nil {
		return nil, err
	}
	var cp events.Checkpoint
	if generation != f.FeedGeneration || json.Unmarshal(data, &cp) != nil || cp.Validate() != nil || cp.DeploymentID != f.Checkpoint.DeploymentID || cp.ControlInstanceID != f.Checkpoint.ControlInstanceID || cp.RecoveryEpoch != f.Checkpoint.RecoveryEpoch || !slices.Equal(cp.NamespaceIDs, namespaces) || !slices.Equal(namespaces, f.Checkpoint.NamespaceIDs) {
		return nil, notifications.ErrEvaluationSource
	}
	if status == "paused" {
		var pure bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_event_gaps WHERE deployment_id=$1::uuid AND resolved_at IS NULL AND reason='capacity') AND NOT EXISTS(SELECT 1 FROM dashboard_event_recoveries WHERE deployment_id=$1::uuid AND status='quarantined')`, cp.DeploymentID).Scan(&pure)
		if err != nil {
			return nil, err
		}
		if !pure {
			return nil, notifications.ErrEvaluationSource
		}
	} else if status != "active" {
		return nil, notifications.ErrEvaluationSource
	}
	if err = checkRecoveryRegistry(ctx, tx, events.RecoveryPlan{DeploymentID: cp.DeploymentID, ConfigurationRevision: f.ConfigurationRevision, Checkpoint: f.Checkpoint}); err != nil {
		return nil, notifications.ErrEvaluationSource
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, err
	}
	if f.Validate(now) != nil {
		return nil, notifications.ErrEvaluationSource
	}
	return laterRecoveryTime(floor, g.control.RestoreRecordedThrough), nil
}
func evaluationEvent(ctx context.Context, tx pgx.Tx, key notifications.EventKey) (events.Event, bool, error) {
	var e events.Event
	var payload, digest []byte
	var suppressed bool
	err := tx.QueryRow(ctx, `SELECT payload,fact_digest,notification_suppressed FROM dashboard_source_events WHERE deployment_id=$1::uuid AND control_instance_id=$2::uuid AND event_id=$3::uuid AND processed_at IS NULL FOR UPDATE`, key.DeploymentID, key.ControlInstanceID, key.EventID).Scan(&payload, &digest, &suppressed)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, false, notifications.ErrEvaluationLease
	}
	if err != nil {
		return e, false, err
	}
	if json.Unmarshal(payload, &e) != nil || e.Validate() != nil || notifications.Key(e) != key {
		return e, false, notifications.ErrEvaluationInvalid
	}
	_, actual, err := eventDigest(e)
	if err != nil || !bytes.Equal(actual, digest) {
		return e, false, notifications.ErrEvaluationInvalid
	}
	return e, suppressed, nil
}
func suppressedEvent(e events.Event, flag bool, floor *time.Time) bool {
	return flag || e.Imported || e.Reconciliation || floor != nil && !e.RecordedAt.After(*floor)
}
func finishNotificationEvent(ctx context.Context, tx pgx.Tx, key notifications.EventKey) error {
	_, err := tx.Exec(ctx, `UPDATE dashboard_source_events e SET processed_at=clock_timestamp() WHERE e.deployment_id=$1::uuid AND e.control_instance_id=$2::uuid AND e.event_id=$3::uuid AND e.processed_at IS NULL AND EXISTS(SELECT 1 FROM dashboard_notification_fanout f WHERE f.deployment_id=e.deployment_id AND f.control_instance_id=e.control_instance_id AND f.event_id=e.event_id AND f.done) AND NOT EXISTS(SELECT 1 FROM dashboard_notification_evaluations w WHERE w.deployment_id=e.deployment_id AND w.control_instance_id=e.control_instance_id AND w.event_id=e.event_id AND w.state='pending')`, key.DeploymentID, key.ControlInstanceID, key.EventID)
	return err
}
func (s *NotificationEvaluationStore) ClaimNotificationFanout(ctx context.Context, f notifications.SourceFence) (notifications.FanoutClaim, error) {
	var claim notifications.FanoutClaim
	tx, g, err := s.begin(ctx)
	if err != nil {
		return claim, err
	}
	defer tx.Rollback(ctx)
	if _, err = s.source(ctx, tx, g, f); err != nil {
		return claim, err
	}
	key := notifications.EventKey{DeploymentID: f.Checkpoint.DeploymentID}
	err = tx.QueryRow(ctx, `SELECT e.control_instance_id::text,e.event_id::text FROM dashboard_source_events e LEFT JOIN dashboard_notification_fanout w USING(deployment_id,control_instance_id,event_id) WHERE e.deployment_id=$1::uuid AND e.processed_at IS NULL AND COALESCE(w.done,false)=false AND (w.lease_token IS NULL OR w.lease_expires_at<=clock_timestamp()) ORDER BY e.first_seen_at,e.event_id LIMIT 1 FOR UPDATE OF e SKIP LOCKED`, key.DeploymentID).Scan(&key.ControlInstanceID, &key.EventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return claim, notifications.ErrEvaluationEmpty
	}
	if err != nil {
		return claim, err
	}
	if key.ControlInstanceID != f.Checkpoint.ControlInstanceID {
		return claim, notifications.ErrEvaluationSource
	}
	token, err := newID()
	if err != nil {
		return claim, err
	}
	encoded, err := json.Marshal(f)
	if err != nil {
		return claim, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO dashboard_notification_fanout(deployment_id,control_instance_id,event_id,lease_token,lease_expires_at,lease_hold_generation,lease_fence) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,clock_timestamp()+interval '30 seconds',$5,$6) ON CONFLICT(deployment_id,control_instance_id,event_id) DO UPDATE SET lease_token=excluded.lease_token,lease_expires_at=excluded.lease_expires_at,lease_hold_generation=excluded.lease_hold_generation,lease_fence=excluded.lease_fence,revision=dashboard_notification_fanout.revision+1 RETURNING revision`, key.DeploymentID, key.ControlInstanceID, key.EventID, token, g.control.Generation, encoded).Scan(&claim.Revision)
	if err != nil {
		return claim, err
	}
	claim.Key = key
	claim.LeaseToken = token
	claim.HoldGeneration = g.control.Generation
	claim.Fence = f
	return claim, tx.Commit(ctx)
}
func (s *NotificationEvaluationStore) AppendNotificationFanout(ctx context.Context, c notifications.FanoutClaim) (notifications.FanoutProgress, error) {
	var result notifications.FanoutProgress
	if c.Key.Validate() != nil || !eventUUID(c.LeaseToken) || c.Revision < 1 {
		return result, notifications.ErrEvaluationInvalid
	}
	tx, g, err := s.begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	floor, err := s.source(ctx, tx, g, c.Fence)
	if err != nil {
		return result, err
	}
	e, suppressed, err := evaluationEvent(ctx, tx, c.Key)
	if err != nil {
		return result, err
	}
	var after string
	var count int
	var fence []byte
	var held int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(after_rule_id::text,''),account_count,lease_hold_generation,lease_fence FROM dashboard_notification_fanout WHERE deployment_id=$1::uuid AND control_instance_id=$2::uuid AND event_id=$3::uuid AND revision=$4 AND lease_token=$5::uuid AND lease_expires_at>clock_timestamp() AND NOT done FOR UPDATE`, c.Key.DeploymentID, c.Key.ControlInstanceID, c.Key.EventID, c.Revision, c.LeaseToken).Scan(&after, &count, &held, &fence)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, notifications.ErrEvaluationLease
	}
	if err != nil {
		return result, err
	}
	var original notifications.SourceFence
	if json.Unmarshal(fence, &original) != nil || !original.SameSource(c.Fence) || held != g.control.Generation || held != c.HoldGeneration {
		return result, notifications.ErrEvaluationLease
	}
	rules := []notifications.Rule{}
	if !suppressedEvent(e, suppressed, floor) && slices.Contains(c.Fence.Checkpoint.NamespaceIDs, e.NamespaceID) {
		rows, err := tx.Query(ctx, `SELECT r.id::text,r.account_id::text,r.revision,r.payload FROM dashboard_notification_rule_scopes n JOIN dashboard_notification_rules r ON r.id=n.rule_id AND r.account_id=n.account_id WHERE n.deployment_id=$1::uuid AND n.namespace_id=$2::uuid AND r.enabled AND r.deleted_at IS NULL AND ($3='' OR r.id>NULLIF($3,'')::uuid) ORDER BY r.id LIMIT 51`, e.DeploymentID, e.NamespaceID, after)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var id, account string
			var revision int64
			var data []byte
			if err = rows.Scan(&id, &account, &revision, &data); err != nil {
				break
			}
			var rule notifications.Rule
			rule, err = decodeNotificationRule(data, id, account, revision)
			if err != nil {
				break
			}
			rules = append(rules, rule)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return result, err
		}
	}
	result.Done = len(rules) <= notifications.MaximumFanoutRules
	if !result.Done {
		rules = rules[:notifications.MaximumFanoutRules]
	}
	for _, rule := range rules {
		result.RulesRead++
		after = rule.ID
		match, err := notifications.MatchVersion(rule, e)
		if err != nil {
			return result, err
		}
		if match == nil {
			continue
		}
		id, err := newID()
		if err != nil {
			return result, err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO dashboard_notification_evaluations(id,account_id,deployment_id,control_instance_id,event_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid) ON CONFLICT(account_id,deployment_id,control_instance_id,event_id) DO NOTHING`, id, rule.AccountID, e.DeploymentID, e.ControlInstanceID, e.EventID)
		if err != nil {
			return result, err
		}
		if tag.RowsAffected() == 1 {
			result.AccountsAdded++
			count++
			if count > notifications.MaximumEventAccounts || g.evaluations+result.AccountsAdded > notifications.MaximumPendingEvaluations {
				return result, notifications.ErrEvaluationCapacity
			}
		} else {
			if err = tx.QueryRow(ctx, `SELECT id::text FROM dashboard_notification_evaluations WHERE account_id=$1::uuid AND deployment_id=$2::uuid AND control_instance_id=$3::uuid AND event_id=$4::uuid AND state='pending'`, rule.AccountID, e.DeploymentID, e.ControlInstanceID, e.EventID).Scan(&id); err != nil {
				return result, err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_evaluation_matches(evaluation_id,rule_id,rule_revision,activation_id) VALUES($1::uuid,$2::uuid,$3,$4::uuid) ON CONFLICT DO NOTHING`, id, match.RuleID, match.Revision, match.ActivationID); err != nil {
			return result, err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE dashboard_notification_fanout SET after_rule_id=NULLIF($4,'')::uuid,account_count=$5,done=$6,revision=revision+1,lease_token=NULL,lease_expires_at=NULL,lease_hold_generation=NULL,lease_fence=NULL WHERE deployment_id=$1::uuid AND control_instance_id=$2::uuid AND event_id=$3::uuid AND lease_token=$7::uuid AND lease_expires_at>clock_timestamp()`, e.DeploymentID, e.ControlInstanceID, e.EventID, after, count, result.Done, c.LeaseToken)
	if err != nil {
		return result, err
	}
	if tag.RowsAffected() != 1 {
		return result, notifications.ErrEvaluationLease
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_work_quota SET pending_evaluations=pending_evaluations+$1 WHERE singleton`, result.AccountsAdded); err != nil {
		return result, err
	}
	if err = finishNotificationEvent(ctx, tx, c.Key); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (s *NotificationEvaluationStore) ReleaseNotificationFanout(ctx context.Context, c notifications.FanoutClaim) error {
	if c.Key.Validate() != nil || !eventUUID(c.LeaseToken) {
		return notifications.ErrEvaluationInvalid
	}
	_, err := s.store.Pool.Exec(ctx, `UPDATE dashboard_notification_fanout SET lease_token=NULL,lease_expires_at=NULL,lease_hold_generation=NULL,lease_fence=NULL WHERE deployment_id=$1::uuid AND control_instance_id=$2::uuid AND event_id=$3::uuid AND revision=$4 AND lease_token=$5::uuid`, c.Key.DeploymentID, c.Key.ControlInstanceID, c.Key.EventID, c.Revision, c.LeaseToken)
	return err
}

func evaluationMatches(ctx context.Context, tx pgx.Tx, id string, e events.Event) ([]notifications.MatchedRule, error) {
	rows, err := tx.Query(ctx, `SELECT m.rule_id::text,v.account_id::text,m.rule_revision,m.activation_id::text,v.payload FROM dashboard_notification_evaluation_matches m JOIN dashboard_notification_rule_versions v ON v.rule_id=m.rule_id AND v.revision=m.rule_revision WHERE m.evaluation_id=$1::uuid ORDER BY m.rule_id LIMIT 101`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []notifications.MatchedRule{}
	for rows.Next() {
		var rid, account, activation string
		var rev int64
		var data []byte
		if err = rows.Scan(&rid, &account, &rev, &activation, &data); err != nil {
			return nil, err
		}
		rule, err := decodeNotificationRule(data, rid, account, rev)
		if err != nil {
			return nil, err
		}
		match, err := notifications.MatchVersion(rule, e)
		if err != nil || match == nil || match.ActivationID != activation {
			return nil, notifications.ErrEvaluationInvalid
		}
		result = append(result, notifications.MatchedRule{Match: *match, Rule: rule})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 || len(result) > notifications.MaximumRulesPerAccount {
		return nil, notifications.ErrEvaluationInvalid
	}
	return result, nil
}
func resolveEvaluation(ctx context.Context, tx pgx.Tx, id, state, inbox string, key notifications.EventKey) error {
	tag, err := tx.Exec(ctx, `UPDATE dashboard_notification_evaluations SET state=$2,inbox_id=NULLIF($3,'')::uuid,resolved_at=clock_timestamp(),revision=revision+1,lease_token=NULL,lease_expires_at=NULL,lease_hold_generation=NULL,lease_fence=NULL WHERE id=$1::uuid AND state='pending'`, id, state, inbox)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return notifications.ErrEvaluationLease
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_work_quota SET pending_evaluations=pending_evaluations-1 WHERE singleton`); err != nil {
		return err
	}
	return finishNotificationEvent(ctx, tx, key)
}
func (s *NotificationEvaluationStore) actor(ctx context.Context, tx pgx.Tx, account string) (monitoring.Actor, bool, error) {
	var a monitoring.Actor
	var disabled *time.Time
	err := tx.QueryRow(ctx, `SELECT id::text,directory_id::text,display_name,disabled_at FROM dashboard_accounts WHERE id=$1::uuid FOR SHARE`, account).Scan(&a.Account.ID, &a.DirectoryID, &a.Account.DisplayName, &disabled)
	if err != nil {
		return a, false, err
	}
	if disabled != nil {
		return a, true, nil
	}
	a.Issuer = s.issuer
	err = tx.QueryRow(ctx, `SELECT subject FROM dashboard_identity_aliases WHERE account_id=$1::uuid AND issuer=$2 ORDER BY verified_at DESC,subject LIMIT 1 FOR SHARE`, account, s.issuer).Scan(&a.Subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, false, nil
	}
	return a, false, err
}
func (s *NotificationEvaluationStore) ClaimNotificationEvaluation(ctx context.Context, f notifications.SourceFence) (notifications.EvaluationClaim, error) {
	var c notifications.EvaluationClaim
	if f.Checkpoint.Validate() != nil {
		return c, notifications.ErrEvaluationSource
	}
	tx, g, err := s.begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx)
	var account string
	key := notifications.EventKey{DeploymentID: f.Checkpoint.DeploymentID}
	err = tx.QueryRow(ctx, `SELECT w.id::text,w.account_id::text,w.control_instance_id::text,w.event_id::text FROM dashboard_notification_evaluations w JOIN dashboard_notification_fanout f USING(deployment_id,control_instance_id,event_id) WHERE w.deployment_id=$1::uuid AND w.state='pending' AND f.done AND w.next_attempt_at<=clock_timestamp() AND (w.lease_token IS NULL OR w.lease_expires_at<=clock_timestamp()) ORDER BY w.next_attempt_at,w.id LIMIT 1`, key.DeploymentID).Scan(&c.ID, &account, &key.ControlInstanceID, &key.EventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, notifications.ErrEvaluationEmpty
	}
	if err != nil {
		return c, err
	}
	actor, disabled, err := s.actor(ctx, tx, account)
	if err != nil {
		return c, err
	}
	floor, err := s.source(ctx, tx, g, f)
	if err != nil {
		return c, err
	}
	if key.ControlInstanceID != f.Checkpoint.ControlInstanceID {
		return c, notifications.ErrEvaluationSource
	}
	e, suppressed, err := evaluationEvent(ctx, tx, key)
	if err != nil {
		return c, err
	}
	if suppressedEvent(e, suppressed, floor) || disabled || !slices.Contains(f.Checkpoint.NamespaceIDs, e.NamespaceID) {
		if err = resolveEvaluation(ctx, tx, c.ID, "suppressed", "", key); err != nil {
			return c, err
		}
		if err = tx.Commit(ctx); err != nil {
			return c, err
		}
		return notifications.EvaluationClaim{}, notifications.ErrEvaluationAdvanced
	}
	if actor.Subject == "" {
		if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_evaluations SET next_attempt_at=clock_timestamp()+interval '60 seconds',revision=revision+1,lease_token=NULL,lease_expires_at=NULL,lease_hold_generation=NULL,lease_fence=NULL WHERE id=$1::uuid`, c.ID); err != nil {
			return c, err
		}
		if err = tx.Commit(ctx); err != nil {
			return c, err
		}
		return notifications.EvaluationClaim{}, notifications.ErrEvaluationAdvanced
	}
	// Current local stops and monotonic denials need no remote authorization
	// request. They resolve only after the healthy source/hold fences above.
	matches, err := currentEvaluationMatches(ctx, tx, c.ID, e, actor.Account.ID, "", false)
	if err != nil {
		return c, err
	}
	if len(matches) == 0 {
		if err = resolveEvaluation(ctx, tx, c.ID, "suppressed", "", key); err != nil {
			return c, err
		}
		if err = tx.Commit(ctx); err != nil {
			return c, err
		}
		return notifications.EvaluationClaim{}, notifications.ErrEvaluationAdvanced
	}
	token, err := newID()
	if err != nil {
		return c, err
	}
	encoded, err := json.Marshal(f)
	if err != nil {
		return c, err
	}
	err = tx.QueryRow(ctx, `UPDATE dashboard_notification_evaluations SET revision=revision+1,attempts=attempts+1,lease_token=$2::uuid,lease_expires_at=clock_timestamp()+interval '30 seconds',lease_hold_generation=$3,lease_fence=$4 WHERE id=$1::uuid AND state='pending' AND (lease_token IS NULL OR lease_expires_at<=clock_timestamp()) RETURNING revision,attempts`, c.ID, token, g.control.Generation, encoded).Scan(&c.Revision, &c.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, notifications.ErrEvaluationLease
	}
	if err != nil {
		return c, err
	}
	c.LeaseToken = token
	c.HoldGeneration = g.control.Generation
	c.Fence = f
	c.Actor = actor
	c.Event = e
	c.Matches = matches
	return c, tx.Commit(ctx)
}

func lockEvaluationClaim(ctx context.Context, tx pgx.Tx, g evaluationGate, c notifications.EvaluationClaim) (notifications.SourceFence, error) {
	var original notifications.SourceFence
	var data []byte
	var held int64
	var account string
	var key notifications.EventKey
	err := tx.QueryRow(ctx, `SELECT account_id::text,deployment_id::text,control_instance_id::text,event_id::text,lease_hold_generation,lease_fence FROM dashboard_notification_evaluations WHERE id=$1::uuid AND revision=$2 AND lease_token=$3::uuid AND lease_expires_at>clock_timestamp() AND state='pending' FOR UPDATE`, c.ID, c.Revision, c.LeaseToken).Scan(&account, &key.DeploymentID, &key.ControlInstanceID, &key.EventID, &held, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return original, notifications.ErrEvaluationLease
	}
	if err != nil {
		return original, err
	}
	if account != c.Actor.Account.ID || key != notifications.Key(c.Event) || held != g.control.Generation || held != c.HoldGeneration || json.Unmarshal(data, &original) != nil || !original.SameSource(c.Fence) {
		return original, notifications.ErrEvaluationLease
	}
	return original, nil
}
func currentEvaluationMatches(ctx context.Context, tx pgx.Tx, id string, e events.Event, account, principal string, deny bool) ([]notifications.MatchedRule, error) {
	matches, err := evaluationMatches(ctx, tx, id, e)
	if err != nil {
		return nil, err
	}
	result := []notifications.MatchedRule{}
	for _, m := range matches {
		if m.Match.AccountID != account {
			return nil, notifications.ErrEvaluationInvalid
		}
		current, err := readNotificationRule(ctx, tx, account, m.Match.RuleID)
		if errors.Is(err, notifications.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		applicable, err := notifications.StillApplicable(m.Match, current)
		if err != nil {
			return nil, err
		}
		if !applicable {
			continue
		}
		var revoked bool
		err = tx.QueryRow(ctx, `SELECT r.activation_id IS NOT NULL OR COALESCE(a.origin_recorded_at<=n.revoked_through,false) FROM dashboard_notification_activations a LEFT JOIN dashboard_notification_activation_revocations r ON r.activation_id=a.id LEFT JOIN dashboard_notification_scope_revocations n ON n.deployment_id=a.deployment_id AND n.namespace_id=a.namespace_id WHERE a.id=$1::uuid`, m.Match.ActivationID).Scan(&revoked)
		if err != nil {
			return nil, err
		}
		if revoked {
			continue
		}
		wrongPrincipal := false
		if principal != "" {
			for _, a := range current.Activation {
				if a.ID == m.Match.ActivationID {
					wrongPrincipal = a.Boundary.PrincipalID != principal
					break
				}
			}
		}
		if deny || wrongPrincipal {
			tag, err := tx.Exec(ctx, `INSERT INTO dashboard_notification_activation_revocations(activation_id) VALUES($1::uuid) ON CONFLICT DO NOTHING`, m.Match.ActivationID)
			if err != nil {
				return nil, err
			}
			// Each evaluation has at most one matching activation per rule. Audit
			// only a newly inserted denial, within the same publication transaction.
			if tag.RowsAffected() > 0 {
				if _, err = tx.Exec(ctx, `INSERT INTO dashboard_audit(account_id,action,resource_kind,resource_id) VALUES($1::uuid,'notification_rule.activations_revoked','notification_rule',$2)`, account, m.Match.RuleID); err != nil {
					return nil, err
				}
			}
			continue
		}
		result = append(result, m)
	}
	return result, nil
}
func validEvaluationClaim(c notifications.EvaluationClaim) bool {
	return eventUUID(c.ID) && eventUUID(c.LeaseToken) && c.Revision > 0 && c.HoldGeneration > 0 && c.Event.Validate() == nil && eventUUID(c.Actor.Account.ID)
}
func (s *NotificationEvaluationStore) CommitNotificationEvaluation(ctx context.Context, c notifications.EvaluationClaim, f notifications.SourceFence, d notifications.EvaluationDecision) (notifications.EvaluationResult, error) {
	result := notifications.EvaluationResult{}
	if !validEvaluationClaim(c) || d.Outcome != notifications.EvaluationAuthorized && d.Outcome != notifications.EvaluationInaccessible && d.Outcome != notifications.EvaluationResourceInaccessible && d.Outcome != notifications.EvaluationDefer {
		return result, notifications.ErrEvaluationInvalid
	}
	if d.Outcome == notifications.EvaluationDefer {
		err := s.DeferNotificationEvaluation(ctx, c)
		return notifications.EvaluationResult{State: "pending"}, err
	}
	tx, g, err := s.begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if d.Outcome == notifications.EvaluationAuthorized {
		if d.Proof.Validate(g.now) != nil || d.Proof.AccountID != c.Actor.Account.ID || d.Proof.Namespace != (notifications.NamespaceRef{DeploymentID: c.Event.DeploymentID, NamespaceID: c.Event.NamespaceID}) || d.Proof.ControlInstanceID != f.Checkpoint.ControlInstanceID || d.Proof.RecoveryEpoch != f.Checkpoint.RecoveryEpoch || !c.Fence.SameSource(f) {
			return result, notifications.ErrEvaluationInvalid
		}
		// Device mutations take this lock before their account/installation locks.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72108003010)`); err != nil {
			return result, err
		}
	}
	actor, disabled, err := s.actor(ctx, tx, c.Actor.Account.ID)
	if err != nil {
		return result, err
	}
	if !disabled && (actor.DirectoryID != c.Actor.DirectoryID || actor.Issuer != c.Actor.Issuer || actor.Subject != c.Actor.Subject) {
		return result, notifications.ErrEvaluationLease
	}
	floor, err := s.source(ctx, tx, g, f)
	if err != nil {
		return result, err
	}
	e, suppressed, err := evaluationEvent(ctx, tx, notifications.Key(c.Event))
	if err != nil {
		return result, err
	}
	original, err := lockEvaluationClaim(ctx, tx, g, c)
	if err != nil {
		return result, err
	}
	if !original.SameSource(f) {
		return result, notifications.ErrEvaluationSource
	}
	matches := []notifications.MatchedRule{}
	if !disabled && !suppressedEvent(e, suppressed, floor) && slices.Contains(f.Checkpoint.NamespaceIDs, e.NamespaceID) && d.Outcome != notifications.EvaluationResourceInaccessible {
		matches, err = currentEvaluationMatches(ctx, tx, c.ID, e, actor.Account.ID, d.Proof.PrincipalID, d.Outcome == notifications.EvaluationInaccessible)
		if err != nil {
			return result, err
		}
	}
	if len(matches) == 0 {
		if err = resolveEvaluationLease(ctx, tx, c, "suppressed", "", notifications.Key(e)); err != nil {
			return result, err
		}
		result.State = "suppressed"
		return result, tx.Commit(ctx)
	}
	if d.Outcome != notifications.EvaluationAuthorized {
		return result, notifications.ErrEvaluationInvalid
	}
	// The historical token-free event facts are copied. No names, commands, log
	// bytes or remote paths are fetched or stored in notification content.
	inbox, err := newID()
	if err != nil {
		return result, err
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return result, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO dashboard_notification_inbox(id,account_id,deployment_id,control_instance_id,event_id,namespace_id,job_id,recorded_at,event_payload) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,$8,$9) ON CONFLICT(account_id,deployment_id,control_instance_id,event_id) DO NOTHING`, inbox, actor.Account.ID, e.DeploymentID, e.ControlInstanceID, e.EventID, e.NamespaceID, e.JobID, e.RecordedAt, payload)
	if err != nil {
		return result, err
	}
	if tag.RowsAffected() == 0 {
		if err = tx.QueryRow(ctx, `SELECT id::text FROM dashboard_notification_inbox WHERE account_id=$1::uuid AND deployment_id=$2::uuid AND control_instance_id=$3::uuid AND event_id=$4::uuid`, actor.Account.ID, e.DeploymentID, e.ControlInstanceID, e.EventID).Scan(&inbox); err != nil {
			return result, err
		}
	} else {
		for _, m := range matches {
			if _, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_inbox_matches(inbox_id,rule_id,rule_revision,activation_id) VALUES($1::uuid,$2::uuid,$3,$4::uuid)`, inbox, m.Match.RuleID, m.Match.Revision, m.Match.ActivationID); err != nil {
				return result, err
			}
		}
		result.Deliveries, err = s.enqueue(ctx, tx, g, actor.Account.ID, inbox, e.DeploymentID, e.RecordedAt.Add(notifications.PushLifetime))
		if err != nil {
			return result, err
		}
	}
	// A lock wait must not extend remote evidence beyond its actual expiry.
	var publicationTime time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&publicationTime); err != nil {
		return result, err
	}
	if d.Proof.Validate(publicationTime) != nil || f.Validate(publicationTime) != nil {
		return result, notifications.ErrEvaluationSource
	}
	if err = resolveEvaluationLease(ctx, tx, c, "complete", inbox, notifications.Key(e)); err != nil {
		return result, err
	}
	result.State = "complete"
	result.InboxID = inbox
	return result, tx.Commit(ctx)
}
func resolveEvaluationLease(ctx context.Context, tx pgx.Tx, c notifications.EvaluationClaim, state, inbox string, key notifications.EventKey) error {
	// Recheck expiry at the final write, including when a device/account lock
	// consumed most of the lease. No expired worker may publish an inbox prefix.
	var live bool
	if err := tx.QueryRow(ctx, `SELECT lease_token=$2::uuid AND revision=$3 AND lease_expires_at>clock_timestamp() FROM dashboard_notification_evaluations WHERE id=$1::uuid`, c.ID, c.LeaseToken, c.Revision).Scan(&live); err != nil {
		return err
	}
	if !live {
		return notifications.ErrEvaluationLease
	}
	return resolveEvaluation(ctx, tx, c.ID, state, inbox, key)
}
func (s *NotificationEvaluationStore) enqueue(ctx context.Context, tx pgx.Tx, g evaluationGate, account, inbox, deployment string, deadline time.Time) (int, error) {
	if s.devices == nil || !deadline.After(g.now) {
		return 0, nil
	}
	// Bound future-clock anomalies too; the source adapter normally prevents them.
	deadline = minTime(deadline, g.now.Add(notifications.PushLifetime))
	rows, err := tx.Query(ctx, `SELECT i.id::text,b.id::text,b.token_version,b.topic,b.environment FROM dashboard_notification_device_bindings b JOIN dashboard_notification_installations i ON i.id=b.installation_id AND i.current_binding_id=b.id WHERE b.account_id=$1::uuid AND b.state='bound' AND b.enabled AND NOT b.muted AND b.permission IN ('authorized','provisional','ephemeral') AND b.token_ciphertext IS NOT NULL AND b.token_invalidated_at IS NULL AND `+notificationDeviceReadySQL+` ORDER BY i.id LIMIT 51 FOR SHARE OF i,b`, account)
	if err != nil {
		return 0, err
	}
	candidates := []notifications.DeviceCandidate{}
	for rows.Next() {
		var c notifications.DeviceCandidate
		var topic, environment string
		c.AccountID = account
		if err = rows.Scan(&c.InstallationID, &c.BindingID, &c.TokenVersion, &topic, &environment); err != nil {
			break
		}
		if s.devices.Allows(topic, environment) {
			candidates = append(candidates, c)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(candidates) > notifications.MaximumBoundDevices || g.deliveries+len(candidates) > notifications.MaximumPendingDeliveries {
		return 0, notifications.ErrEvaluationCapacity
	}
	for _, c := range candidates {
		id, err := newID()
		if err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_deliveries(id,inbox_id,account_id,installation_id,binding_id,token_version,expires_at,deployment_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8::uuid)`, id, inbox, account, c.InstallationID, c.BindingID, c.TokenVersion, deadline, deployment); err != nil {
			return 0, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_work_quota SET pending_deliveries=pending_deliveries+$1 WHERE singleton`, len(candidates)); err != nil {
		return 0, err
	}
	return len(candidates), nil
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Deferral needs no new source/network proof: it publishes no content or grant.
// It still consults the hold/floor and exact lease, and only leaves work pending.
// Suppression is resolved by a later source-fenced claim, so an outage/gap is
// never bypassed even when a cutoff already proves this event ineligible.
func (s *NotificationEvaluationStore) DeferNotificationEvaluation(ctx context.Context, c notifications.EvaluationClaim) error {
	if !validEvaluationClaim(c) {
		return notifications.ErrEvaluationInvalid
	}
	tx, g, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var floor *time.Time
	err = tx.QueryRow(ctx, `SELECT GREATEST(suppress_recorded_through,pruned_recorded_through) FROM dashboard_event_feeds WHERE deployment_id=$1::uuid FOR SHARE`, c.Event.DeploymentID).Scan(&floor)
	if err != nil {
		return err
	}
	floor = laterRecoveryTime(floor, g.control.RestoreRecordedThrough)
	e, suppressed, err := evaluationEvent(ctx, tx, notifications.Key(c.Event))
	if err != nil {
		return err
	}
	if _, err = lockEvaluationClaim(ctx, tx, g, c); err != nil {
		return err
	}
	delay := notifications.EvaluationRetryDelay(c.Attempts)
	if suppressedEvent(e, suppressed, floor) {
		delay = 5 * time.Second
	}
	tag, err := tx.Exec(ctx, `UPDATE dashboard_notification_evaluations SET next_attempt_at=clock_timestamp()+$4*interval '1 second',revision=revision+1,lease_token=NULL,lease_expires_at=NULL,lease_hold_generation=NULL,lease_fence=NULL WHERE id=$1::uuid AND revision=$2 AND lease_token=$3::uuid AND lease_expires_at>clock_timestamp()`, c.ID, c.Revision, c.LeaseToken, int64(delay/time.Second))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return notifications.ErrEvaluationLease
	}
	return tx.Commit(ctx)
}
