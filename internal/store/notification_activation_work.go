package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

type NotificationActivationStore struct{ identities *NotificationEvaluationStore }

func NewNotificationActivationStore(s *Store, issuer string) (*NotificationActivationStore, error) {
	e, err := NewNotificationEvaluationStore(s, issuer, nil)
	if err != nil {
		return nil, err
	}
	return &NotificationActivationStore{e}, nil
}

var _ notifications.ActivationRepository = (*NotificationActivationStore)(nil)

func syncNotificationActivationWork(ctx context.Context, tx pgx.Tx, r notifications.Rule) error {
	pending := false
	if r.Enabled && r.DeletedAt == nil {
		for _, a := range r.Activation {
			if a.Status == notifications.ActivationPending {
				pending = true
				break
			}
		}
	}
	if !pending {
		_, err := tx.Exec(ctx, `DELETE FROM dashboard_notification_activation_work WHERE rule_id=$1::uuid`, r.ID)
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO dashboard_notification_activation_work(rule_id,account_id,rule_revision) VALUES($1::uuid,$2::uuid,$3) ON CONFLICT(rule_id) DO UPDATE SET rule_revision=excluded.rule_revision,attempts=0,next_attempt_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL`, r.ID, r.AccountID, r.Revision)
	return err
}

// The queue is an indexed projection of current pending rules, not a recurring
// scan of historical versions/accounts. Rule writes reset it transactionally.
func (s *NotificationActivationStore) ClaimNotificationActivation(ctx context.Context) (notifications.ActivationClaim, error) {
	var c notifications.ActivationClaim
	tx, err := s.identities.store.Pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx)
	var account string
	err = tx.QueryRow(ctx, `SELECT rule_id::text,account_id::text FROM dashboard_notification_activation_work WHERE next_attempt_at<=clock_timestamp() AND (lease_token IS NULL OR lease_expires_at<=clock_timestamp()) ORDER BY next_attempt_at,rule_id LIMIT 1`).Scan(&c.RuleID, &account)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, notifications.ErrEvaluationEmpty
	}
	if err != nil {
		return c, err
	}
	// Match rule writes' account→work order; never hold work while waiting for an
	// account that a concurrent user edit already owns.
	a, disabled, err := s.identities.actor(ctx, tx, account)
	if err != nil {
		return c, err
	}
	err = tx.QueryRow(ctx, `SELECT rule_revision,attempts FROM dashboard_notification_activation_work WHERE rule_id=$1::uuid AND next_attempt_at<=clock_timestamp() AND (lease_token IS NULL OR lease_expires_at<=clock_timestamp()) FOR UPDATE SKIP LOCKED`, c.RuleID).Scan(&c.Revision, &c.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, notifications.ErrEvaluationEmpty
	}
	if err != nil {
		return c, err
	}
	r, readErr := readNotificationRule(ctx, tx, account, c.RuleID)
	if readErr != nil && !errors.Is(readErr, notifications.ErrNotFound) {
		return c, readErr
	}
	drop := func() (notifications.ActivationClaim, error) {
		if _, err = tx.Exec(ctx, `DELETE FROM dashboard_notification_activation_work WHERE rule_id=$1::uuid`, c.RuleID); err != nil {
			return c, err
		}
		if err = tx.Commit(ctx); err != nil {
			return c, err
		}
		return notifications.ActivationClaim{}, notifications.ErrEvaluationAdvanced
	}
	if disabled || errors.Is(readErr, notifications.ErrNotFound) || !r.Enabled {
		return drop()
	}
	if r.Revision != c.Revision {
		return c, notifications.ErrConflict
	}
	var pending bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_notification_rule_scopes n JOIN dashboard_notification_activations a ON a.id=n.activation_id LEFT JOIN dashboard_notification_activation_revocations r ON r.activation_id=a.id LEFT JOIN dashboard_notification_scope_revocations s ON s.deployment_id=a.deployment_id AND s.namespace_id=a.namespace_id WHERE n.rule_id=$1::uuid AND convert_from(a.payload,'UTF8')::jsonb->>'status'='pending' AND r.activation_id IS NULL AND (s.revoked_through IS NULL OR a.origin_recorded_at>s.revoked_through))`, c.RuleID).Scan(&pending)
	if err != nil {
		return c, err
	}
	if !pending {
		return drop()
	}
	if a.Subject == "" {
		if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_activation_work SET next_attempt_at=clock_timestamp()+interval '60 seconds',lease_token=NULL,lease_expires_at=NULL WHERE rule_id=$1::uuid`, c.RuleID); err != nil {
			return c, err
		}
		if err = tx.Commit(ctx); err != nil {
			return c, err
		}
		return notifications.ActivationClaim{}, notifications.ErrEvaluationAdvanced
	}
	c.LeaseToken, err = newID()
	if err != nil {
		return c, err
	}
	err = tx.QueryRow(ctx, `UPDATE dashboard_notification_activation_work SET lease_token=$2::uuid,lease_expires_at=clock_timestamp()+interval '30 seconds',attempts=attempts+1 WHERE rule_id=$1::uuid RETURNING attempts`, c.RuleID, c.LeaseToken).Scan(&c.Attempts)
	if err != nil {
		return c, err
	}
	c.Actor = a
	return c, tx.Commit(ctx)
}
func (s *NotificationActivationStore) ReleaseNotificationActivation(ctx context.Context, c notifications.ActivationClaim) error {
	if !eventUUID(c.RuleID) || !eventUUID(c.LeaseToken) || c.Revision < 1 {
		return notifications.ErrInvalid
	}
	_, err := s.identities.store.Pool.Exec(ctx, `UPDATE dashboard_notification_activation_work SET lease_token=NULL,lease_expires_at=NULL,next_attempt_at=clock_timestamp()+$4*interval '1 second' WHERE rule_id=$1::uuid AND rule_revision=$2 AND lease_token=$3::uuid`, c.RuleID, c.Revision, c.LeaseToken, int64(notifications.EvaluationRetryDelay(c.Attempts)/time.Second))
	return err
}
