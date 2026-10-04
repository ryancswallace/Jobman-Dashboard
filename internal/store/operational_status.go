package store

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/operations"
)

var _ operations.StatusRepository = (*Store)(nil)

// OperationalStatus reads a bounded, transactionally consistent local snapshot.
// It does no network authorization/probing and acquires no worker row locks.
// Query or deadline failure returns no snapshot, never a misleading empty queue.
func (s *Store) OperationalStatus(ctx context.Context, deployments []string) (operations.HealthSnapshot, error) {
	ids, err := operations.CanonicalStatusDeployments(deployments)
	if err != nil {
		return operations.HealthSnapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return operations.HealthSnapshot{}, operations.ErrStatusUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='2s'; SET LOCAL lock_timeout='500ms'`); err != nil {
		return operations.HealthSnapshot{}, operations.ErrStatusUnavailable
	}
	var out operations.HealthSnapshot
	out.Sources = make([]operations.SourceHealth, len(ids))
	index := make(map[string]int, len(ids))
	for i, id := range ids {
		out.Sources[i] = operations.SourceHealth{DeploymentID: id, State: "uninitialized"}
		index[id] = i
	}
	if err = tx.QueryRow(ctx, `SELECT transaction_timestamp(),held,generation::text,restore_recorded_through,updated_at FROM dashboard_notification_delivery_control WHERE singleton`).Scan(&out.ObservedAt, &out.Hold.Held, &out.Hold.Generation, &out.Hold.RestoreRecordedThrough, &out.Hold.UpdatedAt); err != nil {
		return operations.HealthSnapshot{}, operations.ErrStatusUnavailable
	}
	if err = statusFeeds(ctx, tx, ids, index, &out); err != nil {
		return operations.HealthSnapshot{}, operations.ErrStatusUnavailable
	}
	if err = statusEvents(ctx, tx, ids, index, &out); err != nil {
		return operations.HealthSnapshot{}, operations.ErrStatusUnavailable
	}
	if err = statusQueues(ctx, tx, ids, index, &out); err != nil {
		return operations.HealthSnapshot{}, operations.ErrStatusUnavailable
	}
	if err = tx.QueryRow(ctx, `SELECT count(*)::text,count(*) FILTER(WHERE next_attempt_at<=$1 AND (lease_expires_at IS NULL OR lease_expires_at<=$1))::text,count(*) FILTER(WHERE lease_expires_at>$1)::text,min(r.updated_at) FROM dashboard_notification_activation_work w JOIN dashboard_notification_rules r ON r.id=w.rule_id`, out.ObservedAt).Scan(&out.Activation.Pending, &out.Activation.Due, &out.Activation.Leased, &out.Activation.OldestAt); err != nil {
		return operations.HealthSnapshot{}, operations.ErrStatusUnavailable
	}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(failures),0)::text,count(*) FILTER(WHERE retry_not_before>$1)::text,max(last_failure_at),max(retry_not_before) FILTER(WHERE retry_not_before>$1) FROM dashboard_notification_provider_health`, out.ObservedAt).Scan(&out.Provider.RecordedFailures, &out.Provider.BackingOff, &out.Provider.LastFailureAt, &out.Provider.RetryNotBefore); err != nil {
		return operations.HealthSnapshot{}, operations.ErrStatusUnavailable
	}
	if out.Validate() != nil || tx.Commit(ctx) != nil {
		return operations.HealthSnapshot{}, operations.ErrStatusUnavailable
	}
	return out, nil
}

func statusFeeds(ctx context.Context, tx pgx.Tx, ids []string, index map[string]int, out *operations.HealthSnapshot) error {
	rows, err := tx.Query(ctx, `SELECT deployment_id::text,namespace_ids::text[],status,last_error,last_success_at,retained_count::text,retention_seconds::text,pruned_recorded_through,suppress_recorded_through,checkpoint FROM dashboard_event_feeds WHERE deployment_id=ANY($1::uuid[]) ORDER BY deployment_id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, state string
		var namespaces []string
		var data []byte
		f := &operations.FeedHealth{}
		if err = rows.Scan(&id, &namespaces, &state, &f.LastError, &f.LastSuccessAt, &f.RetainedEvents, &f.RetentionSeconds, &f.PrunedRecordedThrough, &f.SuppressRecordedThrough, &data); err != nil {
			return err
		}
		i, ok := index[id]
		if !ok || out.Sources[i].Feed != nil {
			return operations.ErrStatusUnavailable
		}
		if len(data) > 0 {
			var c events.Checkpoint
			if len(data) > 32768 || json.Unmarshal(data, &c) != nil || c.Validate() != nil || c.DeploymentID != id || !slices.Equal(c.NamespaceIDs, namespaces) {
				return operations.ErrStatusUnavailable
			}
			f.Checkpoint = &operations.SourceObservation{AsOf: c.AsOf, UnpublishedEvents: c.BacklogCount, OldestUnpublishedAt: c.OldestUnpublishedRecordedAt}
		}
		zero := func() *operations.QueueHealth {
			return &operations.QueueHealth{Backlog: operations.Backlog{Pending: "0"}, Due: "0", Leased: "0"}
		}
		out.Sources[i] = operations.SourceHealth{DeploymentID: id, State: state, Feed: f, Events: &operations.Backlog{Pending: "0"}, Fanout: &operations.Backlog{Pending: "0"}, Evaluation: zero(), Delivery: zero()}
	}
	return rows.Err()
}

func statusEvents(ctx context.Context, tx pgx.Tx, ids []string, index map[string]int, out *operations.HealthSnapshot) error {
	rows, err := tx.Query(ctx, `SELECT e.deployment_id::text,count(*)::text,min(e.first_seen_at),count(*) FILTER(WHERE f.done IS DISTINCT FROM true)::text,min(e.first_seen_at) FILTER(WHERE f.done IS DISTINCT FROM true)
 FROM dashboard_source_events e LEFT JOIN dashboard_notification_fanout f USING(deployment_id,control_instance_id,event_id)
 WHERE e.deployment_id=ANY($1::uuid[]) AND e.processed_at IS NULL GROUP BY e.deployment_id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var events, fanout operations.Backlog
		if err = rows.Scan(&id, &events.Pending, &events.OldestAt, &fanout.Pending, &fanout.OldestAt); err != nil {
			return err
		}
		i, ok := index[id]
		if !ok || out.Sources[i].Feed == nil {
			return operations.ErrStatusUnavailable
		}
		out.Sources[i].Events, out.Sources[i].Fanout = &events, &fanout
	}
	return rows.Err()
}

func statusQueues(ctx context.Context, tx pgx.Tx, ids []string, index map[string]int, out *operations.HealthSnapshot) error {
	// Both arms use source-qualified partial indexes over pending work. Fixed SQL
	// arms avoid table-name interpolation and return at most 2*32 aggregate rows.
	rows, err := tx.Query(ctx, `SELECT 'evaluation',deployment_id::text,count(*)::text,count(*) FILTER(WHERE next_attempt_at<=$2 AND (lease_expires_at IS NULL OR lease_expires_at<=$2))::text,count(*) FILTER(WHERE lease_expires_at>$2)::text,min(created_at)
 FROM dashboard_notification_evaluations WHERE deployment_id=ANY($1::uuid[]) AND state='pending' GROUP BY deployment_id
 UNION ALL
 SELECT 'delivery',deployment_id::text,count(*)::text,count(*) FILTER(WHERE next_attempt_at<=$2 AND (lease_expires_at IS NULL OR lease_expires_at<=$2))::text,count(*) FILTER(WHERE lease_expires_at>$2)::text,min(created_at)
 FROM dashboard_notification_deliveries WHERE deployment_id=ANY($1::uuid[]) AND state='pending' GROUP BY deployment_id`, ids, out.ObservedAt)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id string
		var q operations.QueueHealth
		if err = rows.Scan(&kind, &id, &q.Pending, &q.Due, &q.Leased, &q.OldestAt); err != nil {
			return err
		}
		i, ok := index[id]
		if !ok || out.Sources[i].Feed == nil {
			return operations.ErrStatusUnavailable
		}
		switch kind {
		case "evaluation":
			out.Sources[i].Evaluation = &q
		case "delivery":
			out.Sources[i].Delivery = &q
		default:
			return operations.ErrStatusUnavailable
		}
	}
	return rows.Err()
}
