package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

const maximumRecoveryHistoryPerSource = 100

// PruneCapacityEvents provides a bounded operator path out of a storage pause.
// Only a fresh, pinned checkpoint proving the same source instance and current
// configuration can authorize cleanup; pending and unexpired identities remain
// protected even when a capacity pause is followed by an epoch/scope change.
// Evaluation
// of already durable events during a capacity-only pause separately requires
// the same current-source checks and current represented-user authorization.
func (s *Store) PruneCapacityEvents(ctx context.Context, deployment string, expectedGeneration, configurationRevision int64, checkpoint events.Checkpoint) (int64, error) {
	if checkpoint.Validate() != nil || checkpoint.DeploymentID != deployment || expectedGeneration < 1 || configurationRevision < 1 {
		return 0, events.ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if held, err := lockNotificationEventPruning(ctx, tx); err != nil || held {
		return 0, err
	}
	var prior []byte
	var namespaces []string
	var retention int64
	err = tx.QueryRow(ctx, `SELECT f.checkpoint,f.namespace_ids::text[],f.retention_seconds FROM dashboard_event_feeds f
 JOIN dashboard_event_gaps g ON g.deployment_id=f.deployment_id AND g.resolved_at IS NULL
 WHERE f.deployment_id=$1::uuid AND f.generation=$2 AND f.status='paused' AND g.reason='capacity' FOR UPDATE OF f,g`, deployment, expectedGeneration).Scan(&prior, &namespaces, &retention)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, events.ErrRecoveryStale
	}
	if err != nil {
		return 0, err
	}
	var old events.Checkpoint
	if json.Unmarshal(prior, &old) != nil || old.Validate() != nil || old.ControlInstanceID != checkpoint.ControlInstanceID || !slices.Equal(namespaces, old.NamespaceIDs) {
		return 0, events.ErrRecoveryStale
	}
	oldEpoch, _ := strconv.ParseInt(old.RecoveryEpoch, 10, 64)
	newEpoch, _ := strconv.ParseInt(checkpoint.RecoveryEpoch, 10, 64)
	if newEpoch < oldEpoch {
		return 0, events.ErrRecoveryStale
	}
	if err = checkRecoveryRegistry(ctx, tx, events.RecoveryPlan{DeploymentID: deployment, ConfigurationRevision: configurationRevision, Checkpoint: checkpoint}); err != nil {
		return 0, err
	}
	var quarantined bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_event_recoveries WHERE deployment_id=$1::uuid AND status='quarantined')`, deployment).Scan(&quarantined); err != nil {
		return 0, err
	}
	if quarantined {
		return 0, events.ErrRecoveryQuarantined
	}
	currentRetention, _ := strconv.ParseInt(checkpoint.RetentionSeconds, 10, 64)
	retention = max(retention, currentRetention)
	removed, recordedThrough, err := pruneSourceEventsTx(ctx, tx, deployment, retention)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_event_feeds SET retained_count=retained_count-$2,retention_seconds=$3,pruned_recorded_through=GREATEST(pruned_recorded_through,$4::timestamptz),generation=generation+CASE WHEN $2>0 THEN 1 ELSE 0 END,updated_at=clock_timestamp() WHERE deployment_id=$1::uuid`, deployment, removed, retention, recordedThrough); err != nil {
		return 0, err
	}
	if removed > 0 {
		// A plan's fingerprint includes the prior deduplication safety floor.
		// Replan after pruning rather than mutating that approved policy in place.
		if _, err = tx.Exec(ctx, `UPDATE dashboard_event_recoveries SET status='superseded',revision=revision+1,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE deployment_id=$1::uuid AND status IN ('replaying','ready')`, deployment); err != nil {
			return 0, err
		}
	}
	return removed, tx.Commit(ctx)
}

// EventRecoveryGap returns only private operator state. Neither this method nor
// its cursors may be exposed through an ordinary authenticated user API.
func (s *Store) EventRecoveryGap(ctx context.Context, deployment string) (gapID string, feed events.Feed, err error) {
	if !eventUUID(deployment) {
		return "", feed, events.ErrInvalid
	}
	var checkpoint []byte
	err = s.Pool.QueryRow(ctx, `SELECT g.id::text,f.deployment_id::text,f.namespace_ids::text[],f.status,f.generation,f.checkpoint,f.cursor,f.last_position
 FROM dashboard_event_feeds f JOIN dashboard_event_gaps g ON g.deployment_id=f.deployment_id AND g.resolved_at IS NULL
 WHERE f.deployment_id=$1::uuid AND f.status='paused'`, deployment).Scan(&gapID, &feed.DeploymentID, &feed.NamespaceIDs, &feed.Status, &feed.Generation, &checkpoint, &feed.Cursor, &feed.LastPosition)
	if errors.Is(err, pgx.ErrNoRows) {
		err = events.ErrRecoveryStale
	}
	if err == nil && checkpoint != nil && (json.Unmarshal(checkpoint, &feed.Checkpoint) != nil || feed.Checkpoint.Validate() != nil) {
		err = events.ErrInvalid
	}
	return
}

// PlanEventRecovery is an explicit operator action. It supersedes an unfinished
// plan only through the feed generation CAS, never a quarantined factual
// conflict. Its checkpoint must have been fetched outside this transaction by
// the pinned source adapter. No HTTP or represented-user work occurs here.
func (s *Store) PlanEventRecovery(ctx context.Context, gapID string, expectedGeneration, configurationRevision int64, checkpoint events.Checkpoint, uncertainty events.RecoveryUncertainty) (events.RecoveryState, error) {
	var result events.RecoveryState
	if !eventUUID(gapID) || expectedGeneration < 1 || expectedGeneration == int64(^uint64(0)>>1) || configurationRevision < 1 || checkpoint.Validate() != nil || uncertainty.Validate() != nil {
		return result, events.ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	plan := events.RecoveryPlan{GapID: gapID, DeploymentID: checkpoint.DeploymentID, ConfigurationRevision: configurationRevision, Checkpoint: checkpoint, Uncertainty: uncertainty}
	var prior []byte
	var retained int64
	var pruned, suppressed *time.Time
	err = tx.QueryRow(ctx, `SELECT g.reason,f.namespace_ids::text[],f.checkpoint,f.cursor,f.last_position,f.retained_count,f.pruned_recorded_through,f.suppress_recorded_through,clock_timestamp()
 FROM dashboard_event_feeds f JOIN dashboard_event_gaps g ON g.deployment_id=f.deployment_id
 WHERE g.id=$1::uuid AND g.resolved_at IS NULL AND f.deployment_id=$2::uuid AND f.generation=$3 AND f.status='paused'
 FOR UPDATE OF f,g`, gapID, checkpoint.DeploymentID, expectedGeneration).Scan(&plan.Reason, &plan.PriorNamespaceIDs, &prior, &plan.PriorCursor, &plan.PriorLastPosition, &retained, &pruned, &suppressed, &plan.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, events.ErrRecoveryStale
	}
	if err != nil {
		return result, err
	}
	if prior != nil {
		plan.PriorCheckpoint = &events.Checkpoint{}
		if json.Unmarshal(prior, plan.PriorCheckpoint) != nil {
			return result, events.ErrInvalid
		}
	}
	var count int
	var quarantined bool
	if err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_or(status='quarantined' AND gap_id=$2::uuid),false) FROM dashboard_event_recoveries WHERE deployment_id=$1::uuid`, checkpoint.DeploymentID, gapID).Scan(&count, &quarantined); err != nil {
		return result, err
	}
	if plan.Reason == "event_conflict" || quarantined {
		return result, events.ErrRecoveryQuarantined
	}
	if count >= maximumRecoveryHistoryPerSource || plan.Reason == "capacity" && retained >= events.MaximumRetainedEventsPerSource {
		return result, events.ErrCapacity
	}
	plan.Uncertainty.SuppressRecordedThrough = laterRecoveryTime(plan.Uncertainty.SuppressRecordedThrough, laterRecoveryTime(pruned, suppressed))
	plan.EffectiveReason = plan.Reason
	if plan.Reason == "capacity" && plan.PriorCheckpoint != nil {
		if plan.PriorCheckpoint.RecoveryEpoch != checkpoint.RecoveryEpoch {
			plan.EffectiveReason = string(events.SourceChanged)
		} else if !slices.Equal(plan.PriorNamespaceIDs, checkpoint.NamespaceIDs) {
			plan.EffectiveReason = string(events.ScopeChanged)
		}
	}
	plan.Mode = events.RecoveryRetained
	if plan.EffectiveReason == "capacity" {
		plan.Mode = events.RecoveryContinue
	}
	plan.FeedGeneration = expectedGeneration + 1
	plan.ID, err = newID()
	if err != nil {
		return result, err
	}
	plan.CreatedAt = plan.CreatedAt.UTC()
	plan.RemovedNamespaceIDs, plan.AddedNamespaceIDs = events.NamespaceChanges(plan.PriorNamespaceIDs, checkpoint.NamespaceIDs)
	plan.Digest = plan.Fingerprint()
	if err = plan.Validate(); err != nil {
		return result, err
	}
	if err = checkRecoveryRegistry(ctx, tx, plan); err != nil {
		return result, err
	}
	encoded, err := json.Marshal(plan)
	if err != nil || len(encoded) > 128<<10 {
		return result, events.ErrInvalid
	}
	checkpointBytes, err := json.Marshal(checkpoint)
	if err != nil || len(checkpointBytes) > 32768 {
		return result, events.ErrInvalid
	}
	result = events.RecoveryState{Plan: plan, Status: events.RecoveryReplaying, Revision: 1, Cursor: checkpoint.OldestCursor, Checkpoint: checkpoint}
	if plan.Mode == events.RecoveryContinue {
		result.Cursor, result.LastPosition = plan.PriorCursor, plan.PriorLastPosition
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_event_recoveries SET status='superseded',revision=revision+1,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE deployment_id=$1::uuid AND status IN ('replaying','ready')`, plan.DeploymentID); err != nil {
		return events.RecoveryState{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO dashboard_event_recoveries(id,gap_id,deployment_id,plan,status,cursor,last_position,checkpoint) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'replaying',$5,$6,$7)`, plan.ID, plan.GapID, plan.DeploymentID, encoded, result.Cursor, result.LastPosition, checkpointBytes); err != nil {
		return events.RecoveryState{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_event_feeds SET generation=$2,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE deployment_id=$1::uuid`, plan.DeploymentID, plan.FeedGeneration); err != nil {
		return events.RecoveryState{}, err
	}
	return result, tx.Commit(ctx)
}

func laterRecoveryTime(a, b *time.Time) *time.Time {
	if b != nil && (a == nil || b.After(*a)) {
		a = b
	}
	if a == nil {
		return nil
	}
	value := a.UTC()
	return &value
}

func checkRecoveryRegistry(ctx context.Context, tx pgx.Tx, plan events.RecoveryPlan) error {
	var instance, epoch string
	var revision int64
	err := tx.QueryRow(ctx, `SELECT control_instance_id::text,recovery_epoch,configuration_revision FROM dashboard_source_identities WHERE deployment_id=$1::uuid FOR SHARE`, plan.DeploymentID).Scan(&instance, &epoch, &revision)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (instance != plan.Checkpoint.ControlInstanceID || epoch != plan.Checkpoint.RecoveryEpoch || revision != plan.ConfigurationRevision) {
		return events.ErrRecoveryStale
	}
	return err
}

const recoverySelect = `SELECT plan,status,revision,cursor,last_position,checkpoint,pages,scanned,added,COALESCE(lease_token::text,'') FROM dashboard_event_recoveries WHERE id=$1::uuid`

func scanRecovery(row pgx.Row) (events.RecoveryState, error) {
	var state events.RecoveryState
	var plan, checkpoint []byte
	err := row.Scan(&plan, &state.Status, &state.Revision, &state.Cursor, &state.LastPosition, &checkpoint, &state.Pages, &state.Scanned, &state.Added, &state.LeaseToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, events.ErrRecoveryStale
	}
	if err != nil {
		return state, err
	}
	if json.Unmarshal(plan, &state.Plan) != nil || state.Plan.Validate() != nil || json.Unmarshal(checkpoint, &state.Checkpoint) != nil || state.Checkpoint.Validate() != nil {
		return events.RecoveryState{}, events.ErrInvalid
	}
	return state, nil
}

func (s *Store) EventRecovery(ctx context.Context, id string) (events.RecoveryState, error) {
	if !eventUUID(id) {
		return events.RecoveryState{}, events.ErrInvalid
	}
	state, err := scanRecovery(s.Pool.QueryRow(ctx, recoverySelect, id))
	state.LeaseToken = "" // Status reads cannot borrow a worker's lease.
	return state, err
}

// lockRecovery uses the same feed-first ordering as ingestion/pruning/planning.
// All callers recheck the immutable registry inside the short DB transaction.
func lockRecovery(ctx context.Context, tx pgx.Tx, id string) (events.RecoveryState, int64, error) {
	var deployment string
	if err := tx.QueryRow(ctx, `SELECT deployment_id::text FROM dashboard_event_recoveries WHERE id=$1::uuid`, id).Scan(&deployment); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = events.ErrRecoveryStale
		}
		return events.RecoveryState{}, 0, err
	}
	var generation, retained int64
	var status string
	if err := tx.QueryRow(ctx, `SELECT status,generation,retained_count FROM dashboard_event_feeds WHERE deployment_id=$1::uuid FOR UPDATE`, deployment).Scan(&status, &generation, &retained); err != nil {
		return events.RecoveryState{}, 0, err
	}
	state, err := scanRecovery(tx.QueryRow(ctx, recoverySelect+` FOR UPDATE`, id))
	if err != nil {
		return state, 0, err
	}
	if status != "paused" || generation != state.Plan.FeedGeneration || state.Status != events.RecoveryReplaying && state.Status != events.RecoveryReady || state.Plan.DeploymentID != deployment {
		return state, 0, events.ErrRecoveryStale
	}
	var open bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_event_gaps WHERE id=$1::uuid AND deployment_id=$2::uuid AND resolved_at IS NULL)`, state.Plan.GapID, deployment).Scan(&open); err != nil {
		return state, 0, err
	}
	if !open {
		return state, 0, events.ErrRecoveryStale
	}
	if err = checkRecoveryRegistry(ctx, tx, state.Plan); err != nil {
		return state, 0, err
	}
	return state, retained, nil
}

func (s *Store) ClaimEventRecovery(ctx context.Context, id string) (events.RecoveryState, error) {
	if !eventUUID(id) {
		return events.RecoveryState{}, events.ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return events.RecoveryState{}, err
	}
	defer tx.Rollback(ctx)
	state, _, err := lockRecovery(ctx, tx, id)
	if err != nil {
		return state, err
	}
	if state.Status != events.RecoveryReplaying || state.Pages >= events.MaximumRecoveryPages {
		return state, events.ErrRecoveryStale
	}
	var leased bool
	if err = tx.QueryRow(ctx, `SELECT COALESCE(lease_expires_at>clock_timestamp(),false) FROM dashboard_event_recoveries WHERE id=$1::uuid`, id).Scan(&leased); err != nil {
		return state, err
	}
	if leased {
		return state, events.ErrLease
	}
	state.LeaseToken, err = newID()
	if err != nil {
		return state, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_event_recoveries SET lease_token=$2::uuid,lease_expires_at=clock_timestamp()+interval '30 seconds',updated_at=clock_timestamp() WHERE id=$1::uuid`, id, state.LeaseToken); err != nil {
		return state, err
	}
	return state, tx.Commit(ctx)
}

func (s *Store) ReleaseEventRecovery(ctx context.Context, state events.RecoveryState) error {
	if !eventUUID(state.Plan.ID) || !eventUUID(state.LeaseToken) {
		return events.ErrInvalid
	}
	_, err := s.Pool.Exec(ctx, `UPDATE dashboard_event_recoveries SET lease_token=NULL,lease_expires_at=NULL WHERE id=$1::uuid AND revision=$2 AND lease_token=$3::uuid`, state.Plan.ID, state.Revision, state.LeaseToken)
	return err
}

func (s *Store) AppendEventRecovery(ctx context.Context, claim events.RecoveryState, page events.Page) error {
	if claim.Plan.Validate() != nil || page.Validate() != nil || !eventUUID(claim.LeaseToken) || claim.Status != events.RecoveryReplaying || page.DeploymentID != claim.Plan.DeploymentID || page.ControlInstanceID != claim.Plan.Checkpoint.ControlInstanceID || page.RecoveryEpoch != claim.Plan.Checkpoint.RecoveryEpoch || !slices.Equal(page.NamespaceIDs, claim.Plan.Checkpoint.NamespaceIDs) || len(page.Items) > 0 && page.NextCursor == claim.Cursor {
		return events.ErrInvalid
	}
	last := claim.LastPosition
	for _, item := range page.Items {
		position, _ := strconv.ParseInt(item.Position, 10, 64)
		if position <= last {
			return events.ErrInvalid
		}
		last = position
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	state, retained, err := lockRecovery(ctx, tx, claim.Plan.ID)
	if err != nil {
		return err
	}
	if state.Revision != claim.Revision || state.Cursor != claim.Cursor || state.LeaseToken != claim.LeaseToken || state.Plan.Digest != claim.Plan.Digest || state.LastPosition != claim.LastPosition || state.Status != events.RecoveryReplaying {
		return events.ErrLease
	}
	var alive bool
	if err = tx.QueryRow(ctx, `SELECT lease_expires_at>clock_timestamp() FROM dashboard_event_recoveries WHERE id=$1::uuid`, state.Plan.ID).Scan(&alive); err != nil {
		return err
	}
	if !alive {
		return events.ErrLease
	}
	if state.Pages >= events.MaximumRecoveryPages || state.Scanned+int64(len(page.Items)) > events.MaximumRecoveryEvents {
		return events.ErrCapacity
	}
	// Validate every existing fact before inserting anything, so quarantine can
	// be committed without committing any prefix of a conflicting source page.
	for _, item := range page.Items {
		_, digest, err := eventDigest(item)
		if err != nil {
			return err
		}
		var existing []byte
		err = tx.QueryRow(ctx, `SELECT fact_digest FROM dashboard_source_events WHERE deployment_id=$1::uuid AND control_instance_id=$2::uuid AND event_id=$3::uuid`, item.DeploymentID, item.ControlInstanceID, item.EventID).Scan(&existing)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, digest) {
			tag, err := tx.Exec(ctx, `UPDATE dashboard_event_recoveries SET status='quarantined',revision=revision+1,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1::uuid AND revision=$2 AND lease_token=$3::uuid AND lease_expires_at>clock_timestamp()`, state.Plan.ID, state.Revision, claim.LeaseToken)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return events.ErrLease
			}
			if err = tx.Commit(ctx); err != nil {
				return err
			}
			return events.ErrRecoveryQuarantined
		}
	}
	retention, _ := strconv.ParseInt(page.RetentionSeconds, 10, 64)
	var added int64
	for _, item := range page.Items {
		payload, digest, _ := eventDigest(item)
		tag, err := tx.Exec(ctx, `INSERT INTO dashboard_source_events(deployment_id,control_instance_id,event_id,namespace_id,job_id,recorded_at,payload,fact_digest,expires_at,notification_suppressed)
 VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,clock_timestamp()+$9*interval '1 second',$10) ON CONFLICT DO NOTHING`, item.DeploymentID, item.ControlInstanceID, item.EventID, item.NamespaceID, item.JobID, item.RecordedAt, payload, digest, retention+86400, state.Plan.Uncertainty.Suppresses(item))
		if err != nil {
			return err
		}
		added += tag.RowsAffected()
		if tag.RowsAffected() == 0 {
			if _, err = tx.Exec(ctx, `UPDATE dashboard_source_events SET last_seen_at=clock_timestamp(),expires_at=GREATEST(expires_at,clock_timestamp()+$4*interval '1 second'),notification_suppressed=notification_suppressed OR $5 WHERE deployment_id=$1::uuid AND control_instance_id=$2::uuid AND event_id=$3::uuid`, item.DeploymentID, item.ControlInstanceID, item.EventID, retention+86400, state.Plan.Uncertainty.Suppresses(item)); err != nil {
				return err
			}
		}
	}
	if retained+added > events.MaximumRetainedEventsPerSource {
		return events.ErrCapacity
	}
	status := events.RecoveryReplaying
	if !page.HasMore {
		status = events.RecoveryReady
	}
	encoded, err := json.Marshal(page.Checkpoint)
	if err != nil || len(encoded) > 32768 {
		return events.ErrInvalid
	}
	tag, err := tx.Exec(ctx, `UPDATE dashboard_event_recoveries SET status=$4,cursor=$5,last_position=$6,checkpoint=$7,pages=pages+1,scanned=scanned+$8,added=added+$9,revision=revision+1,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
 WHERE id=$1::uuid AND revision=$2 AND lease_token=$3::uuid AND lease_expires_at>clock_timestamp()`, state.Plan.ID, state.Revision, claim.LeaseToken, status, page.NextCursor, last, encoded, len(page.Items), added)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return events.ErrLease
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_event_feeds SET retained_count=retained_count+$2,retention_seconds=GREATEST(retention_seconds,$3),updated_at=clock_timestamp() WHERE deployment_id=$1::uuid`, state.Plan.DeploymentID, added, retention); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecoveryScopeGuard must revoke/tombstone removed namespace activations, or
// validate durable tombstones, inside this same transaction. It must perform no
// HTTP or other external I/O. A caller-supplied receipt/boolean is not a guard.
// Root notification integration supplies the guard; nil is rejected on removal.
type RecoveryScopeGuard func(context.Context, pgx.Tx, events.RecoveryPlan) error

func (s *Store) ApplyEventRecovery(ctx context.Context, id string, expectedRevision int64, receipt events.ReconciliationReceipt, scopeGuard RecoveryScopeGuard) error {
	if !eventUUID(id) || expectedRevision < 1 {
		return events.ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	state, _, err := lockRecovery(ctx, tx, id)
	if err != nil {
		return err
	}
	if state.Revision != expectedRevision || state.Status != events.RecoveryReady || state.Pages == 0 || state.Cursor != state.Checkpoint.HeadCursor || state.Checkpoint.ControlInstanceID != state.Plan.Checkpoint.ControlInstanceID || state.Checkpoint.RecoveryEpoch != state.Plan.Checkpoint.RecoveryEpoch || !slices.Equal(state.Checkpoint.NamespaceIDs, state.Plan.Checkpoint.NamespaceIDs) {
		return events.ErrRecoveryStale
	}
	if err = receipt.Validate(state.Plan); err != nil {
		return err
	}
	if len(state.Plan.RemovedNamespaceIDs) > 0 {
		if scopeGuard == nil {
			return events.ErrReconciliation
		}
		if err = scopeGuard(ctx, tx, state.Plan); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(state.Checkpoint)
	if err != nil {
		return err
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	// The bounded gap receipt references the detailed private plan/receipt rather
	// than overflowing its original 8-KiB bound with 320 namespace statuses.
	gapReceipt, err := json.Marshal(struct {
		RecoveryID              string                     `json:"recoveryId"`
		PlanDigest              string                     `json:"planDigest"`
		Uncertainty             events.RecoveryUncertainty `json:"uncertainty"`
		CompleteDeliveryClaimed bool                       `json:"completeDeliveryClaimed"`
	}{state.Plan.ID, state.Plan.Digest, state.Plan.Uncertainty, false})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_event_feeds SET namespace_ids=$2::uuid[],checkpoint=$3,cursor=$4,last_position=$5,status='active',generation=generation+1,last_success_at=clock_timestamp(),last_error='',suppress_recorded_through=GREATEST(suppress_recorded_through,$6::timestamptz),lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE deployment_id=$1::uuid`, state.Plan.DeploymentID, state.Checkpoint.NamespaceIDs, encoded, state.Cursor, state.LastPosition, state.Plan.Uncertainty.SuppressRecordedThrough); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_event_recoveries SET status='applied',revision=revision+1,reconciliation_receipt=$2,scope_removal_checked=true,updated_at=clock_timestamp() WHERE id=$1::uuid`, id, receiptBytes); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_event_gaps SET resolved_at=clock_timestamp(),recovery_receipt=$2 WHERE id=$1::uuid`, state.Plan.GapID, gapReceipt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
