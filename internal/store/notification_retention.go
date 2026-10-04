package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type NotificationRetentionStats struct{ Inboxes, Deliveries, Attempts, RuleVersions, Rules, ActivationPayloads, Revocations int64 }

// PruneNotifications performs one bounded local pass. Expired private inbox
// content is independent of source health. Authorization history retirement is
// conservative: all sources must be current and no operator hold may be active.
// Unknown provider attempts keep minimal journal records indefinitely.
func (s *Store) PruneNotifications(ctx context.Context) (NotificationRetentionStats, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var stats NotificationRetentionStats
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return stats, err
	}
	defer tx.Rollback(ctx)
	var held bool
	if err = tx.QueryRow(ctx, `SELECT held FROM dashboard_notification_delivery_control WHERE singleton FOR SHARE`).Scan(&held); err != nil {
		return stats, err
	}
	if _, err = tx.Exec(ctx, `SELECT pending_deliveries FROM dashboard_notification_work_quota WHERE singleton FOR UPDATE`); err != nil {
		return stats, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM dashboard_notification_inbox WHERE id IN (SELECT i.id FROM dashboard_notification_inbox i WHERE i.expires_at<=statement_timestamp() AND NOT EXISTS(SELECT 1 FROM dashboard_notification_deliveries d WHERE d.inbox_id=i.id AND d.state='pending') ORDER BY i.expires_at,i.id LIMIT 100 FOR UPDATE OF i SKIP LOCKED)`)
	if err != nil {
		return stats, err
	}
	stats.Inboxes = tag.RowsAffected()
	if held {
		return stats, tx.Commit(ctx)
	}
	// Lock feed rows in the same canonical order as other multi-source work.
	if err = lockNotificationRetentionFeeds(ctx, tx); err != nil {
		return stats, err
	}
	tag, err = tx.Exec(ctx, `DELETE FROM dashboard_notification_delivery_attempts WHERE (delivery_id,number) IN (
 SELECT a.delivery_id,a.number FROM dashboard_notification_delivery_attempts a JOIN dashboard_notification_deliveries d ON d.id=a.delivery_id JOIN dashboard_event_feeds f ON f.deployment_id=d.deployment_id
 WHERE d.state<>'pending' AND d.resolved_at<=statement_timestamp()-GREATEST(3024000,f.retention_seconds+432000)*interval '1 second' AND a.outcome<>'unknown' AND f.status='active' AND f.last_success_at>statement_timestamp()-interval '60 seconds'
 ORDER BY d.resolved_at,a.delivery_id,a.number LIMIT 500 FOR UPDATE OF a SKIP LOCKED)`)
	if err != nil {
		return stats, err
	}
	stats.Attempts = tag.RowsAffected()
	tag, err = tx.Exec(ctx, `DELETE FROM dashboard_notification_deliveries WHERE id IN (
 SELECT d.id FROM dashboard_notification_deliveries d JOIN dashboard_event_feeds f ON f.deployment_id=d.deployment_id WHERE d.state<>'pending' AND d.lease_token IS NULL AND d.resolved_at<=statement_timestamp()-GREATEST(3024000,f.retention_seconds+432000)*interval '1 second' AND f.status='active' AND f.last_success_at>statement_timestamp()-interval '60 seconds'
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_delivery_attempts a WHERE a.delivery_id=d.id AND a.outcome='unknown') ORDER BY d.resolved_at,d.id LIMIT 100 FOR UPDATE OF d SKIP LOCKED)`)
	if err != nil {
		return stats, err
	}
	stats.Deliveries = tag.RowsAffected()
	if err = tx.Commit(ctx); err != nil {
		return stats, err
	}
	// Small account batches avoid holding a quota row across all organization
	// history. Every account transaction follows hold→quota→account→feed order.
	accounts, err := s.notificationRetentionAccounts(ctx)
	if err != nil {
		return stats, err
	}
	for _, account := range accounts {
		part, err := s.pruneNotificationAccount(ctx, account)
		if err != nil {
			return stats, err
		}
		stats.RuleVersions += part.RuleVersions
		stats.Rules += part.Rules
		stats.ActivationPayloads += part.ActivationPayloads
		stats.Revocations += part.Revocations
	}
	return stats, nil
}
func lockNotificationRetentionFeeds(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT deployment_id FROM dashboard_event_feeds ORDER BY deployment_id FOR SHARE`)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	return err
}
func (s *Store) pruneNotificationAccount(ctx context.Context, account string) (NotificationRetentionStats, error) {
	var stats NotificationRetentionStats
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return stats, err
	}
	defer tx.Rollback(ctx)
	var held bool
	if err = tx.QueryRow(ctx, `SELECT held FROM dashboard_notification_delivery_control WHERE singleton FOR SHARE`).Scan(&held); err != nil {
		return stats, err
	}
	if held {
		return stats, tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `SELECT pending_deliveries FROM dashboard_notification_work_quota WHERE singleton FOR UPDATE`); err != nil {
		return stats, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM dashboard_accounts WHERE id=$1::uuid FOR UPDATE`, account); err != nil {
		return stats, err
	}
	if err = lockNotificationRetentionFeeds(ctx, tx); err != nil {
		return stats, err
	}
	var healthy bool
	if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM dashboard_event_feeds WHERE status<>'active' OR last_success_at IS NULL OR last_success_at<=statement_timestamp()-interval '60 seconds')`).Scan(&healthy); err != nil {
		return stats, err
	}
	if !healthy {
		return stats, tx.Commit(ctx)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM dashboard_notification_rule_versions WHERE (rule_id,revision) IN (
 SELECT v.rule_id,v.revision FROM dashboard_notification_rule_versions v JOIN dashboard_notification_rules r ON r.id=v.rule_id
 WHERE v.account_id=$1::uuid AND v.revision<>r.revision AND v.recorded_at<=statement_timestamp()-dashboard_notification_retention_seconds()*interval '1 second'
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_evaluation_matches m WHERE m.rule_id=v.rule_id AND m.rule_revision=v.revision)
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_inbox_matches m WHERE m.rule_id=v.rule_id AND m.rule_revision=v.revision)
 ORDER BY v.recorded_at,v.rule_id,v.revision LIMIT 20 FOR UPDATE OF v SKIP LOCKED)`, account)
	if err != nil {
		return stats, err
	}
	stats.RuleVersions = tag.RowsAffected()
	// Remove an expired deleted rule only after all older versions have retired.
	// Its current-version FK is deferred, allowing child-then-parent deletion.
	rows, err := tx.Query(ctx, `SELECT r.id::text,r.revision FROM dashboard_notification_rules r WHERE r.account_id=$1::uuid AND r.deleted_at<=statement_timestamp()-dashboard_notification_retention_seconds()*interval '1 second'
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_rule_versions v WHERE v.rule_id=r.id AND (v.revision<>r.revision OR v.recorded_at>statement_timestamp()-dashboard_notification_retention_seconds()*interval '1 second'))
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_evaluation_matches m WHERE m.rule_id=r.id)
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_inbox_matches m WHERE m.rule_id=r.id)
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_rule_scopes n WHERE n.rule_id=r.id)
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_activation_work w WHERE w.rule_id=r.id)
 ORDER BY r.deleted_at,r.id LIMIT 20 FOR UPDATE OF r SKIP LOCKED`, account)
	if err != nil {
		return stats, err
	}
	type retiredRule struct {
		id       string
		revision int64
	}
	rules := []retiredRule{}
	for rows.Next() {
		var r retiredRule
		if err = rows.Scan(&r.id, &r.revision); err != nil {
			break
		}
		rules = append(rules, r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return stats, err
	}
	for _, r := range rules {
		tag, err = tx.Exec(ctx, `DELETE FROM dashboard_notification_rule_versions WHERE rule_id=$1::uuid AND revision=$2`, r.id, r.revision)
		if err != nil {
			return stats, err
		}
		stats.RuleVersions += tag.RowsAffected()
		tag, err = tx.Exec(ctx, `DELETE FROM dashboard_notification_rules WHERE id=$1::uuid`, r.id)
		if err != nil {
			return stats, err
		}
		stats.Rules += tag.RowsAffected()
	}
	tag, err = tx.Exec(ctx, `UPDATE dashboard_notification_activations SET payload=NULL,retired_at=statement_timestamp() WHERE id IN (
 SELECT a.id FROM dashboard_notification_activations a WHERE a.account_id=$1::uuid AND a.retired_at IS NULL AND a.origin_recorded_at<=statement_timestamp()-dashboard_notification_retention_seconds()*interval '1 second'
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_version_activations v WHERE v.activation_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_rule_scopes n WHERE n.activation_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_evaluation_matches m WHERE m.activation_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM dashboard_notification_inbox_matches m WHERE m.activation_id=a.id)
 ORDER BY a.origin_recorded_at,a.id LIMIT 100 FOR UPDATE OF a SKIP LOCKED)`, account)
	if err != nil {
		return stats, err
	}
	stats.ActivationPayloads = tag.RowsAffected()
	tag, err = tx.Exec(ctx, `DELETE FROM dashboard_notification_activation_revocations WHERE activation_id IN (SELECT r.activation_id FROM dashboard_notification_activation_revocations r JOIN dashboard_notification_activations a ON a.id=r.activation_id WHERE a.account_id=$1::uuid AND a.retired_at IS NOT NULL ORDER BY r.activation_id LIMIT 100)`, account)
	if err != nil {
		return stats, err
	}
	stats.Revocations = tag.RowsAffected()
	return stats, tx.Commit(ctx)
}

// Claim only account IDs; there is no long-held maintenance transaction. A
// crash may skip a pass, but wrapping guarantees future cleanup opportunities.
func (s *Store) notificationRetentionAccounts(ctx context.Context) ([]string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var after *string
	if err = tx.QueryRow(ctx, `SELECT after_account_id::text FROM dashboard_notification_retention_progress WHERE singleton FOR UPDATE`).Scan(&after); err != nil {
		return nil, err
	}
	read := func(after *string) ([]string, error) {
		rows, err := tx.Query(ctx, `SELECT id::text FROM dashboard_accounts WHERE $1::uuid IS NULL OR id>$1::uuid ORDER BY id LIMIT 20`, after)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	ids, err := read(after)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 && after != nil {
		ids, err = read(nil)
		if err != nil {
			return nil, err
		}
	}
	var last *string
	if len(ids) > 0 {
		last = &ids[len(ids)-1]
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_retention_progress SET after_account_id=$1::uuid WHERE singleton`, last); err != nil {
		return nil, err
	}
	return ids, tx.Commit(ctx)
}
