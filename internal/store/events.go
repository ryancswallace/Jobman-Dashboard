package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

func feedNamespaces(ids []string) ([]string, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	if len(ids) < 1 || len(ids) > 320 || len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return nil, events.ErrInvalid
	}
	for _, id := range ids {
		if !eventUUID(id) {
			return nil, events.ErrInvalid
		}
	}
	return ids, nil
}
func eventUUID(id string) bool {
	// Reuse the source contract's UUID validation without accepting PostgreSQL's
	// alternate UUID spellings as distinct source identities.
	e := events.Event{DeploymentID: id, ControlInstanceID: id, EventID: id, NamespaceID: id, JobID: id, OwnerPrincipalID: id, Position: "1", JobRevision: "1", OldPhase: "running", NewPhase: "terminal", Outcome: "success", RecordedAt: time.Unix(1, 0)}
	return e.Validate() == nil
}

// ClaimFeed creates only a previously unknown source. Existing cursors are
// never reset by restart or configuration changes. A namespace edit opens an
// explicit gap, including when no source request can be made successfully.
func (s *Store) ClaimFeed(ctx context.Context, deployment string, namespaces []string) (events.Feed, error) {
	var feed events.Feed
	namespaces, err := feedNamespaces(namespaces)
	if err != nil || !eventUUID(deployment) {
		return feed, events.ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return feed, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72108003006)`); err != nil {
		return feed, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM dashboard_event_feeds`).Scan(&count); err != nil {
		return feed, err
	}
	if count >= 32 {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_event_feeds WHERE deployment_id=$1::uuid)`, deployment).Scan(&exists); err != nil {
			return feed, err
		}
		if !exists {
			return feed, events.ErrCapacity
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO dashboard_event_feeds(deployment_id,namespace_ids,status) VALUES($1::uuid,$2::uuid[],'initializing') ON CONFLICT DO NOTHING`, deployment, namespaces); err != nil {
		return feed, err
	}
	var encoded []byte
	var leased bool
	err = tx.QueryRow(ctx, `SELECT deployment_id::text,namespace_ids::text[],status,generation,checkpoint,cursor,last_position,COALESCE(lease_expires_at>clock_timestamp(),false) FROM dashboard_event_feeds WHERE deployment_id=$1::uuid FOR UPDATE`, deployment).Scan(&feed.DeploymentID, &feed.NamespaceIDs, &feed.Status, &feed.Generation, &encoded, &feed.Cursor, &feed.LastPosition, &leased)
	if err != nil {
		return feed, err
	}
	if encoded != nil && (json.Unmarshal(encoded, &feed.Checkpoint) != nil || feed.Checkpoint.Validate() != nil) {
		return feed, events.ErrInvalid
	}
	if feed.Status == "paused" {
		return feed, events.ErrPaused
	}
	if !slices.Equal(feed.NamespaceIDs, namespaces) {
		if err = pauseFeedTx(ctx, tx, feed, string(events.ScopeChanged)); err != nil {
			return feed, err
		}
		if err = tx.Commit(ctx); err != nil {
			return feed, err
		}
		return feed, events.ErrPaused
	}
	if leased {
		return feed, events.ErrLease
	}
	feed.LeaseToken, err = newID()
	if err != nil {
		return feed, err
	}
	_, err = tx.Exec(ctx, `UPDATE dashboard_event_feeds SET lease_token=$2::uuid,lease_expires_at=clock_timestamp()+interval '30 seconds',updated_at=clock_timestamp() WHERE deployment_id=$1::uuid`, deployment, feed.LeaseToken)
	if err != nil {
		return feed, err
	}
	return feed, tx.Commit(ctx)
}

// Feed row locks serialize only this source's local commit; no source request
// occurs inside a transaction or while holding a database row lock.
func lockFeed(ctx context.Context, tx pgx.Tx, feed events.Feed) (int64, error) {
	var count int64
	err := tx.QueryRow(ctx, `SELECT retained_count FROM dashboard_event_feeds WHERE deployment_id=$1::uuid AND generation=$2 AND lease_token=$3::uuid AND lease_expires_at>clock_timestamp() AND status=$4 AND cursor=$5 FOR UPDATE`, feed.DeploymentID, feed.Generation, feed.LeaseToken, feed.Status, feed.Cursor).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		err = events.ErrLease
	}
	return count, err
}

func (s *Store) InitializeFeed(ctx context.Context, feed events.Feed, c events.Checkpoint) error {
	if c.Validate() != nil || c.DeploymentID != feed.DeploymentID || !slices.Equal(c.NamespaceIDs, feed.NamespaceIDs) || feed.Status != "initializing" {
		return events.ErrInvalid
	}
	encoded, err := json.Marshal(c)
	if err != nil || len(encoded) > 32768 {
		return events.ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockFeed(ctx, tx, feed); err != nil {
		return err
	}
	retention, _ := strconv.ParseInt(c.RetentionSeconds, 10, 64)
	tag, err := tx.Exec(ctx, `UPDATE dashboard_event_feeds SET checkpoint=$4,cursor=$5,retention_seconds=GREATEST(retention_seconds,$6),status='active',generation=generation+1,last_success_at=clock_timestamp(),last_error='',lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE deployment_id=$1::uuid AND generation=$2 AND lease_token=$3::uuid AND lease_expires_at>clock_timestamp()`, feed.DeploymentID, feed.Generation, feed.LeaseToken, encoded, c.HeadCursor, retention)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return events.ErrLease
	}
	return tx.Commit(ctx)
}

func eventDigest(e events.Event) ([]byte, []byte, error) {
	if e.Validate() != nil {
		return nil, nil, events.ErrInvalid
	}
	e.RecordedAt = e.RecordedAt.UTC()
	if e.ObservedCompletedAt != nil {
		value := e.ObservedCompletedAt.UTC()
		e.ObservedCompletedAt = &value
	}
	payload, err := json.Marshal(e)
	if err != nil || len(payload) > 4096 {
		return nil, nil, events.ErrInvalid
	}
	// Feed positions may be reassigned after source recovery. The identity and
	// factual payload may not change; neither epoch nor position permits repeats.
	e.Position = ""
	facts, err := json.Marshal(e)
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(facts)
	return payload, sum[:], nil
}

func (s *Store) AppendFeed(ctx context.Context, feed events.Feed, page events.Page) error {
	if page.Validate() != nil || feed.Status != "active" || page.DeploymentID != feed.DeploymentID {
		return events.ErrInvalid
	}
	if page.ControlInstanceID != feed.Checkpoint.ControlInstanceID || page.RecoveryEpoch != feed.Checkpoint.RecoveryEpoch || !slices.Equal(page.NamespaceIDs, feed.NamespaceIDs) {
		return &events.RecoveryError{Reason: events.SourceChanged}
	}
	if len(page.Items) > 0 && page.NextCursor == feed.Cursor {
		return events.ErrInvalid
	}
	lastPosition := feed.LastPosition
	for _, item := range page.Items {
		position, _ := strconv.ParseInt(item.Position, 10, 64)
		if position <= lastPosition {
			return events.ErrInvalid
		}
		lastPosition = position
	}
	encoded, err := json.Marshal(page.Checkpoint)
	if err != nil || len(encoded) > 32768 {
		return events.ErrInvalid
	}
	retention, _ := strconv.ParseInt(page.RetentionSeconds, 10, 64)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	count, err := lockFeed(ctx, tx, feed)
	if err != nil {
		return err
	}
	added := int64(0)
	for _, item := range page.Items {
		payload, digest, err := eventDigest(item)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO dashboard_source_events(deployment_id,control_instance_id,event_id,namespace_id,job_id,recorded_at,payload,fact_digest,expires_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,clock_timestamp()+$9*interval '1 second') ON CONFLICT DO NOTHING`, item.DeploymentID, item.ControlInstanceID, item.EventID, item.NamespaceID, item.JobID, item.RecordedAt, payload, digest, retention+86400)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			added++
			continue
		}
		var existing []byte
		if err = tx.QueryRow(ctx, `SELECT fact_digest FROM dashboard_source_events WHERE deployment_id=$1::uuid AND control_instance_id=$2::uuid AND event_id=$3::uuid`, item.DeploymentID, item.ControlInstanceID, item.EventID).Scan(&existing); err != nil {
			return err
		}
		if !bytes.Equal(existing, digest) {
			return events.ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE dashboard_source_events SET last_seen_at=clock_timestamp(),expires_at=GREATEST(expires_at,clock_timestamp()+$4*interval '1 second') WHERE deployment_id=$1::uuid AND control_instance_id=$2::uuid AND event_id=$3::uuid`, item.DeploymentID, item.ControlInstanceID, item.EventID, retention+86400); err != nil {
			return err
		}
	}
	if count+added > events.MaximumRetainedEventsPerSource {
		return events.ErrCapacity
	}
	tag, err := tx.Exec(ctx, `UPDATE dashboard_event_feeds SET checkpoint=$4,cursor=$5,retained_count=retained_count+$6,last_position=$7,retention_seconds=GREATEST(retention_seconds,$8),generation=generation+1,last_success_at=clock_timestamp(),last_error='',lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE deployment_id=$1::uuid AND generation=$2 AND lease_token=$3::uuid AND lease_expires_at>clock_timestamp()`, feed.DeploymentID, feed.Generation, feed.LeaseToken, encoded, page.NextCursor, added, lastPosition, retention)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return events.ErrLease
	}
	return tx.Commit(ctx)
}

func pauseFeedTx(ctx context.Context, tx pgx.Tx, feed events.Feed, reason string) error {
	if !slices.Contains([]string{string(events.CursorExpired), string(events.SourceChanged), string(events.ScopeChanged), string(events.CursorInvalid), "event_conflict", "capacity"}, reason) {
		return events.ErrInvalid
	}
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO dashboard_event_gaps(id,deployment_id,reason,prior_checkpoint,prior_cursor) SELECT $2::uuid,deployment_id,$3,checkpoint,cursor FROM dashboard_event_feeds WHERE deployment_id=$1::uuid ON CONFLICT(deployment_id) WHERE resolved_at IS NULL DO NOTHING`, feed.DeploymentID, id, reason)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE dashboard_event_feeds SET status='paused',generation=generation+1,last_error=$2,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE deployment_id=$1::uuid`, feed.DeploymentID, reason)
	return err
}

func (s *Store) PauseFeed(ctx context.Context, feed events.Feed, reason string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockFeed(ctx, tx, feed); err != nil {
		return err
	}
	if err = pauseFeedTx(ctx, tx, feed, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ReleaseFeed(ctx context.Context, feed events.Feed, code string) error {
	if code != "" && code != "source_unavailable" {
		return events.ErrInvalid
	}
	_, err := s.Pool.Exec(ctx, `UPDATE dashboard_event_feeds SET lease_token=NULL,lease_expires_at=NULL,last_error=$4,updated_at=clock_timestamp() WHERE deployment_id=$1::uuid AND generation=$2 AND lease_token=$3::uuid`, feed.DeploymentID, feed.Generation, feed.LeaseToken, code)
	return err
}

// PruneSourceEvents preserves identity tombstones for the full configured
// replay window plus a day, extended on replay. Cleanup and admission serialize
// on the same feed row so the explicit per-source storage budget stays exact.
func (s *Store) PruneSourceEvents(ctx context.Context, deployment string) (int64, error) {
	if !eventUUID(deployment) {
		return 0, events.ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var retention int64
	if err = tx.QueryRow(ctx, `SELECT retention_seconds FROM dashboard_event_feeds WHERE deployment_id=$1::uuid AND status='active' AND last_success_at>clock_timestamp()-interval '60 seconds' FOR UPDATE`, deployment).Scan(&retention); errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM dashboard_source_events WHERE (deployment_id,control_instance_id,event_id) IN (SELECT deployment_id,control_instance_id,event_id FROM dashboard_source_events WHERE deployment_id=$1::uuid AND processed_at IS NOT NULL AND expires_at<=statement_timestamp() AND last_seen_at<=statement_timestamp()-$2*interval '1 second' ORDER BY last_seen_at LIMIT 500)`, deployment, retention+86400)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_event_feeds SET retained_count=retained_count-$2 WHERE deployment_id=$1::uuid`, deployment, tag.RowsAffected()); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}
