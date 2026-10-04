package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

var _ notifications.InboxRepository = (*Store)(nil)

func inboxTransaction(ctx context.Context, s *Store, a monitoring.Actor) (pgx.Tx, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='3s'; SET LOCAL lock_timeout='2s'`); err == nil {
		err = lockNotificationOwner(ctx, tx, a, false)
	}
	if err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func inboxAuthorities(ctx context.Context, tx pgx.Tx, a monitoring.Actor, proofs []notifications.AuthorizationProof) ([]byte, time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, now, err
	}
	if len(proofs) > notifications.MaximumNamespaces {
		return nil, now, notifications.ErrInvalid
	}
	refs := make([]map[string]string, 0, len(proofs))
	seen := map[notifications.NamespaceRef]bool{}
	for _, p := range proofs {
		if p.AccountID != a.Account.ID || p.Validate(now) != nil || seen[p.Namespace] {
			return nil, now, monitoring.ErrAuthority
		}
		seen[p.Namespace] = true
		refs = append(refs, map[string]string{"deployment_id": p.Namespace.DeploymentID, "namespace_id": p.Namespace.NamespaceID, "control_instance_id": p.ControlInstanceID, "recovery_epoch": p.RecoveryEpoch})
	}
	data, err := json.Marshal(refs)
	if err != nil {
		return nil, now, err
	}
	// Source replacement/recovery must not reinterpret an old history reference.
	var valid bool
	err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM jsonb_to_recordset($1::jsonb) AS p(deployment_id uuid,control_instance_id uuid,recovery_epoch text) LEFT JOIN dashboard_source_identities s ON s.deployment_id=p.deployment_id WHERE s.deployment_id IS NULL OR s.control_instance_id<>p.control_instance_id OR s.recovery_epoch<>p.recovery_epoch)`, data).Scan(&valid)
	if err != nil {
		return nil, now, err
	}
	if !valid {
		return nil, now, monitoring.ErrAuthority
	}
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, now, err
	}
	for _, p := range proofs {
		if p.Validate(now) != nil {
			return nil, now, monitoring.ErrAuthority
		}
	}
	return data, now, nil
}
func (s *Store) ValidateNotificationInboxOwner(ctx context.Context, a monitoring.Actor, proofs []notifications.AuthorizationProof) error {
	tx, err := inboxTransaction(ctx, s, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, _, err = inboxAuthorities(ctx, tx, a, proofs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const inboxColumns = `i.id::text,i.account_id::text,i.event_payload,i.deployment_id::text,i.control_instance_id::text,i.event_id::text,i.namespace_id::text,i.job_id::text,i.recorded_at,i.created_at,i.expires_at,i.read_at`

type inboxScanner interface{ Scan(...any) error }

func scanInbox(row inboxScanner) (notifications.InboxRecord, error) {
	var r notifications.InboxRecord
	var raw []byte
	var e events.Event
	err := row.Scan(&r.ID, &r.AccountID, &raw, &e.DeploymentID, &e.ControlInstanceID, &e.EventID, &e.NamespaceID, &e.JobID, &e.RecordedAt, &r.CreatedAt, &r.ExpiresAt, &r.ReadAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, monitoring.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if !eventUUID(r.ID) || !eventUUID(r.AccountID) || len(raw) > 4096 || json.Unmarshal(raw, &r.Event) != nil || r.Event.Validate() != nil || r.Event.DeploymentID != e.DeploymentID || r.Event.ControlInstanceID != e.ControlInstanceID || r.Event.EventID != e.EventID || r.Event.NamespaceID != e.NamespaceID || r.Event.JobID != e.JobID || !r.Event.RecordedAt.Equal(e.RecordedAt) || !r.ExpiresAt.After(r.CreatedAt) {
		return r, monitoring.ErrSource
	}
	r.Matches = []notifications.InboxMatch{}
	r.Delivery = notifications.InboxDelivery{Total: "0", ByState: map[string]string{}}
	return r, nil
}
func inboxContext(ctx context.Context, tx pgx.Tx, r *notifications.InboxRecord) error {
	// Project bounded display fields in SQL; never return complete historical
	// rule scopes, principal IDs or activation checkpoints to a client.
	rows, err := tx.Query(ctx, `SELECT m.rule_id::text,m.rule_revision::text,jsonb_build_object('name',p->'name','scope',p->'scope','outcomeMode',p->'outcomeMode','outcomes',p->'outcomes') FROM dashboard_notification_inbox_matches m JOIN dashboard_notification_rule_versions v ON v.rule_id=m.rule_id AND v.revision=m.rule_revision CROSS JOIN LATERAL (SELECT convert_from(v.payload,'UTF8')::jsonb AS p) projected WHERE m.inbox_id=$1::uuid AND v.account_id=$2::uuid ORDER BY m.rule_id LIMIT 101`, r.ID, r.AccountID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m notifications.InboxMatch
		var data []byte
		if err = rows.Scan(&m.RuleID, &m.Revision, &data); err != nil {
			break
		}
		if len(data) > 2048 || json.Unmarshal(data, &m) != nil || m.Validate() != nil {
			err = monitoring.ErrSource
			break
		}
		r.Matches = append(r.Matches, m)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if len(r.Matches) < 1 || len(r.Matches) > notifications.MaximumRulesPerAccount {
		return monitoring.ErrSource
	}
	rows, err = tx.Query(ctx, `SELECT state,count(*)::text FROM dashboard_notification_deliveries WHERE inbox_id=$1::uuid AND account_id=$2::uuid GROUP BY state ORDER BY state`, r.ID, r.AccountID)
	if err != nil {
		return err
	}
	var total int64
	for rows.Next() {
		var state, count string
		if err = rows.Scan(&state, &count); err != nil {
			break
		}
		n, e := strconv.ParseInt(count, 10, 64)
		if e != nil || n < 1 || n > notifications.MaximumBoundDevices || len(state) > 32 {
			err = monitoring.ErrSource
			break
		}
		total += n
		r.Delivery.ByState[state] = count
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if total > notifications.MaximumBoundDevices {
		return monitoring.ErrSource
	}
	r.Delivery.Total = strconv.FormatInt(total, 10)
	return nil
}
func readInbox(ctx context.Context, tx pgx.Tx, a monitoring.Actor, id string) (notifications.InboxRecord, error) {
	if !eventUUID(id) {
		return notifications.InboxRecord{}, monitoring.ErrNotFound
	}
	r, err := scanInbox(tx.QueryRow(ctx, `SELECT `+inboxColumns+` FROM dashboard_notification_inbox i WHERE i.id=$1::uuid AND i.account_id=$2::uuid AND i.expires_at>clock_timestamp()`, id, a.Account.ID))
	if err != nil {
		return r, err
	}
	err = inboxContext(ctx, tx, &r)
	return r, err
}

// NotificationInbox is a private owner lookup used to discover the scope that
// must be authorized. Its result cannot be sent directly to an HTTP client.
func (s *Store) NotificationInbox(ctx context.Context, a monitoring.Actor, id string) (notifications.InboxRecord, error) {
	tx, err := inboxTransaction(ctx, s, a)
	if err != nil {
		return notifications.InboxRecord{}, err
	}
	defer tx.Rollback(ctx)
	r, err := readInbox(ctx, tx, a, id)
	if err != nil {
		return r, err
	}
	return r, tx.Commit(ctx)
}

const inboxScopeSQL = `EXISTS(SELECT 1 FROM jsonb_to_recordset($2::jsonb) AS p(deployment_id uuid,namespace_id uuid,control_instance_id uuid) WHERE p.deployment_id=i.deployment_id AND p.namespace_id=i.namespace_id AND p.control_instance_id=i.control_instance_id)`

func (s *Store) ListNotificationInbox(ctx context.Context, a monitoring.Actor, q notifications.InboxSelection) (notifications.InboxRecords, error) {
	out := notifications.InboxRecords{Items: []notifications.InboxRecord{}, UnreadCount: "0"}
	if q.Limit < 1 || q.Limit > notifications.MaximumInboxPage || q.After != nil && (!eventUUID(q.After.ID) || q.After.CreatedAt.IsZero()) {
		return out, notifications.ErrInvalid
	}
	tx, err := inboxTransaction(ctx, s, a)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	scopes, now, err := inboxAuthorities(ctx, tx, a, q.Authorities)
	if err != nil {
		return out, err
	}
	out.SnapshotAt = q.SnapshotAt
	if out.SnapshotAt.IsZero() {
		out.SnapshotAt = now
	}
	if out.SnapshotAt.After(now.Add(time.Second)) {
		return out, notifications.ErrInvalid
	}
	var afterTime *time.Time
	var afterID *string
	if q.After != nil {
		afterTime = &q.After.CreatedAt
		afterID = &q.After.ID
	}
	rows, err := tx.Query(ctx, `SELECT `+inboxColumns+` FROM dashboard_notification_inbox i WHERE i.account_id=$1::uuid AND `+inboxScopeSQL+` AND i.expires_at>clock_timestamp() AND i.created_at<=$3 AND (NOT $4::boolean OR i.read_at IS NULL) AND ($5::timestamptz IS NULL OR (i.created_at,i.id)<($5,$6::uuid)) ORDER BY i.created_at DESC,i.id DESC LIMIT $7`, a.Account.ID, scopes, out.SnapshotAt, q.Unread, afterTime, afterID, q.Limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r notifications.InboxRecord
		r, err = scanInbox(rows)
		if err != nil {
			break
		}
		out.Items = append(out.Items, r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Items) > q.Limit {
		out.More = true
		out.Items = out.Items[:q.Limit]
	}
	for i := range out.Items {
		if err = inboxContext(ctx, tx, &out.Items[i]); err != nil {
			return out, err
		}
	}
	err = tx.QueryRow(ctx, `SELECT count(*)::text FROM dashboard_notification_inbox i WHERE i.account_id=$1::uuid AND `+inboxScopeSQL+` AND i.expires_at>clock_timestamp() AND i.read_at IS NULL`, a.Account.ID, scopes).Scan(&out.UnreadCount)
	if err != nil {
		return out, err
	}
	if _, out.FetchedAt, err = inboxAuthorities(ctx, tx, a, q.Authorities); err != nil {
		return out, err
	}
	for _, r := range out.Items {
		if !r.ExpiresAt.After(out.FetchedAt) {
			return out, monitoring.ErrAuthority
		}
	}
	return out, tx.Commit(ctx)
}
func (s *Store) SetNotificationInboxRead(ctx context.Context, a monitoring.Actor, id string, read bool, p notifications.AuthorizationProof) (notifications.InboxRecord, error) {
	var empty notifications.InboxRecord
	if !eventUUID(id) {
		return empty, monitoring.ErrNotFound
	}
	tx, err := inboxTransaction(ctx, s, a)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	if _, _, err = inboxAuthorities(ctx, tx, a, []notifications.AuthorizationProof{p}); err != nil {
		return empty, err
	}
	tag, err := tx.Exec(ctx, `UPDATE dashboard_notification_inbox SET read_at=CASE WHEN $3::boolean THEN COALESCE(read_at,clock_timestamp()) ELSE NULL END WHERE id=$1::uuid AND account_id=$2::uuid AND deployment_id=$4::uuid AND namespace_id=$5::uuid AND control_instance_id=$6::uuid AND expires_at>clock_timestamp()`, id, a.Account.ID, read, p.Namespace.DeploymentID, p.Namespace.NamespaceID, p.ControlInstanceID)
	if err != nil {
		return empty, err
	}
	if tag.RowsAffected() != 1 {
		return empty, monitoring.ErrNotFound
	}
	r, err := readInbox(ctx, tx, a, id)
	if err != nil {
		return empty, err
	}
	if _, _, err = inboxAuthorities(ctx, tx, a, []notifications.AuthorizationProof{p}); err != nil {
		return empty, err
	}
	return r, tx.Commit(ctx)
}
