package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

// RevokeRemovedNotificationScopes is the production recovery-apply guard. Its
// transaction is supplied by ApplyEventRecovery while the source is locked and
// paused. At most320 source-qualified watermarks change; immutable rule versions
// and individual denial records are retained. There is no account/network loop.
func (s *Store) RevokeRemovedNotificationScopes(ctx context.Context, tx pgx.Tx, plan events.RecoveryPlan) error {
	if tx == nil || plan.Validate() != nil {
		return events.ErrInvalid
	}
	var paused bool
	err := tx.QueryRow(ctx, `SELECT status='paused' AND generation=$2 FROM dashboard_event_feeds WHERE deployment_id=$1::uuid FOR UPDATE`, plan.DeploymentID, plan.FeedGeneration).Scan(&paused)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !paused {
		return events.ErrRecoveryStale
	}
	if err != nil {
		return err
	}
	if len(plan.RemovedNamespaceIDs) == 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_scope_revocations(deployment_id,namespace_id,revoked_through)
 SELECT $1::uuid,id,clock_timestamp() FROM unnest($2::uuid[]) AS removed(id)
 ON CONFLICT(deployment_id,namespace_id) DO UPDATE SET revoked_through=GREATEST(dashboard_notification_scope_revocations.revoked_through,excluded.revoked_through)`, plan.DeploymentID, plan.RemovedNamespaceIDs)
	return err
}
