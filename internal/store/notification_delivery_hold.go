package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

// DeliveryControl is private operator state. Every evaluation transaction and
// final provider handoff must recheck this gate; feed capacity is no exception.
type DeliveryControl struct {
	Generation             int64      `json:"generation,string"`
	Held                   bool       `json:"held"`
	RestoreRecordedThrough *time.Time `json:"restoreRecordedThrough,omitempty"`
}
type deliveryControlQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readDeliveryControl(ctx context.Context, q deliveryControlQueryer, lock bool) (DeliveryControl, error) {
	var state DeliveryControl
	query := `SELECT generation,held,restore_recorded_through FROM dashboard_notification_delivery_control WHERE singleton`
	if lock {
		query += " FOR UPDATE"
	}
	err := q.QueryRow(ctx, query).Scan(&state.Generation, &state.Held, &state.RestoreRecordedThrough)
	return state, err
}
func (s *Store) NotificationDeliveryControl(ctx context.Context) (DeliveryControl, error) {
	return readDeliveryControl(ctx, s.Pool, false)
}

// HoldNotifications pauses new work. A restore cutoff also forces every retained
// source through explicit recovery, invalidates outstanding feed/recovery leases,
// and raises its suppression floor atomically. Operators obtain the cutoff from
// outside the restored database; we never guess from a stale local checkpoint.
func (s *Store) HoldNotifications(ctx context.Context, expectedGeneration int64, restoreCutoff *time.Time) (DeliveryControl, error) {
	if expectedGeneration < 1 || restoreCutoff != nil && (restoreCutoff.Year() < 1970 || restoreCutoff.Year() > 9999) {
		return DeliveryControl{}, events.ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return DeliveryControl{}, err
	}
	defer tx.Rollback(ctx)
	state, err := readDeliveryControl(ctx, tx, true)
	if err != nil {
		return state, err
	}
	if state.Generation != expectedGeneration {
		return state, events.ErrRecoveryStale
	}
	if state.Held && (restoreCutoff == nil || state.RestoreRecordedThrough != nil && !restoreCutoff.After(*state.RestoreRecordedThrough)) {
		return state, tx.Commit(ctx)
	}
	if restoreCutoff != nil {
		// The feed-admission lock fixes the complete <=32-source set while restoring.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72108003006)`); err != nil {
			return state, err
		}
		rows, err := tx.Query(ctx, `SELECT deployment_id::text,status,generation,checkpoint,cursor FROM dashboard_event_feeds ORDER BY deployment_id FOR UPDATE`)
		if err != nil {
			return state, err
		}
		feeds := []events.Feed{}
		for rows.Next() {
			var f events.Feed
			var data []byte
			if err = rows.Scan(&f.DeploymentID, &f.Status, &f.Generation, &data, &f.Cursor); err != nil {
				break
			}
			if data != nil && (json.Unmarshal(data, &f.Checkpoint) != nil || f.Checkpoint.Validate() != nil) {
				err = events.ErrInvalid
				break
			}
			feeds = append(feeds, f)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return state, err
		}
		if len(feeds) > 32 {
			return state, events.ErrCapacity
		}
		for _, f := range feeds {
			if f.Status != "paused" {
				if err = pauseFeedTx(ctx, tx, f, string(events.CursorInvalid)); err != nil {
					return state, err
				}
			} else {
				if _, err = tx.Exec(ctx, `UPDATE dashboard_event_feeds SET generation=generation+1,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE deployment_id=$1::uuid`, f.DeploymentID); err != nil {
					return state, err
				}
			}
			if _, err = tx.Exec(ctx, `UPDATE dashboard_event_feeds SET suppress_recorded_through=GREATEST(suppress_recorded_through,$2::timestamptz) WHERE deployment_id=$1::uuid`, f.DeploymentID, restoreCutoff); err != nil {
				return state, err
			}
			if _, err = tx.Exec(ctx, `UPDATE dashboard_event_recoveries SET status='superseded',revision=revision+1,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE deployment_id=$1::uuid AND status IN ('replaying','ready')`, f.DeploymentID); err != nil {
				return state, err
			}
		}
	}
	state.Held = true
	state.Generation++
	state.RestoreRecordedThrough = laterRecoveryTime(state.RestoreRecordedThrough, restoreCutoff)
	_, err = tx.Exec(ctx, `UPDATE dashboard_notification_delivery_control SET held=true,generation=$1,restore_recorded_through=$2,updated_at=clock_timestamp() WHERE singleton`, state.Generation, state.RestoreRecordedThrough)
	if err != nil {
		return state, err
	}
	return state, tx.Commit(ctx)
}

// ResumeNotifications is operator-only. Fresh service checkpoints for the exact
// retained feed set must prove each source was recovered under this configuration.
// No cursor is moved by releasing the global hold. A fresh checkpoint head is
// never used to skip source events that have not been consumed.
func (s *Store) ResumeNotifications(ctx context.Context, expectedGeneration, configurationRevision int64, checkpoints []events.Checkpoint) (DeliveryControl, error) {
	if expectedGeneration < 1 || configurationRevision < 1 || len(checkpoints) < 1 || len(checkpoints) > 32 {
		return DeliveryControl{}, events.ErrInvalid
	}
	checkpoints = slices.Clone(checkpoints)
	slices.SortFunc(checkpoints, func(a, b events.Checkpoint) int {
		if a.DeploymentID < b.DeploymentID {
			return -1
		}
		if a.DeploymentID > b.DeploymentID {
			return 1
		}
		return 0
	})
	for i, c := range checkpoints {
		if c.Validate() != nil || i > 0 && checkpoints[i-1].DeploymentID == c.DeploymentID {
			return DeliveryControl{}, events.ErrInvalid
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return DeliveryControl{}, err
	}
	defer tx.Rollback(ctx)
	state, err := readDeliveryControl(ctx, tx, true)
	if err != nil {
		return state, err
	}
	if state.Generation != expectedGeneration || !state.Held {
		return state, events.ErrRecoveryStale
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72108003006)`); err != nil {
		return state, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM dashboard_event_feeds`).Scan(&count); err != nil {
		return state, err
	}
	if count != len(checkpoints) {
		return state, events.ErrRecoveryStale
	}
	for _, cp := range checkpoints {
		var encoded []byte
		var ns []string
		var cutoff *time.Time
		err = tx.QueryRow(ctx, `SELECT checkpoint,namespace_ids::text[],suppress_recorded_through FROM dashboard_event_feeds WHERE deployment_id=$1::uuid AND status='active' FOR SHARE`, cp.DeploymentID).Scan(&encoded, &ns, &cutoff)
		if errors.Is(err, pgx.ErrNoRows) {
			return state, events.ErrRecoveryStale
		}
		if err != nil {
			return state, err
		}
		var current events.Checkpoint
		if json.Unmarshal(encoded, &current) != nil || current.Validate() != nil || current.ControlInstanceID != cp.ControlInstanceID || current.RecoveryEpoch != cp.RecoveryEpoch || !slices.Equal(current.NamespaceIDs, cp.NamespaceIDs) || !slices.Equal(ns, cp.NamespaceIDs) || state.RestoreRecordedThrough != nil && (cutoff == nil || cutoff.Before(*state.RestoreRecordedThrough)) {
			return state, events.ErrRecoveryStale
		}
		if err = checkRecoveryRegistry(ctx, tx, events.RecoveryPlan{DeploymentID: cp.DeploymentID, ConfigurationRevision: configurationRevision, Checkpoint: cp}); err != nil {
			return state, err
		}
	}
	state.Held = false
	state.Generation++
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_delivery_control SET held=false,generation=$1,updated_at=clock_timestamp() WHERE singleton`, state.Generation); err != nil {
		return state, err
	}
	return state, tx.Commit(ctx)
}
