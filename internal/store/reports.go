package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/reports"
)

const reportColumns = `t.id::text,t.subject,t.state,t.created_at,t.expires_at,t.attempts,COALESCE(t.lease_token::text,''),t.lease_expires_at,t.failure_code,t.object`

func scanReport(row pgx.Row) (reports.Task, error) {
	var task reports.Task
	var subject, object []byte
	err := row.Scan(&task.ID, &subject, &task.State, &task.CreatedAt, &task.ExpiresAt, &task.Attempts, &task.LeaseToken, &task.LeaseExpiresAt, &task.FailureCode, &object)
	if err != nil {
		return task, err
	}
	if json.Unmarshal(subject, &task.Subject) != nil || task.Subject.Validate() != nil {
		return reports.Task{}, reports.ErrInvalid
	}
	if object != nil {
		task.Object = new(reports.Object)
		if json.Unmarshal(object, task.Object) != nil || task.Object.Validate() != nil || task.Object.ID != task.ID {
			return reports.Task{}, reports.ErrInvalid
		}
	}
	return task, nil
}

// EnqueueReport runs only after source/job authorization and snapshot selection.
// All admission, deduplication, requester binding and audit writes commit together.
func (s *Store) EnqueueReport(ctx context.Context, a monitoring.Actor, subject reports.Subject, key string) (reports.Task, error) {
	var empty reports.Task
	fingerprint, err := subject.Fingerprint()
	if err != nil {
		return empty, err
	}
	requestFingerprint, err := subject.RequestFingerprint()
	if err != nil {
		return empty, err
	}
	keyHash, err := reports.IdempotencyHash(key)
	if err != nil {
		return empty, err
	}
	encoded, err := json.Marshal(subject)
	if err != nil || len(encoded) > 4096 {
		return empty, reports.ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72108003005)`); err != nil {
		return empty, err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_accounts a JOIN dashboard_identity_aliases i ON i.account_id=a.id WHERE a.id=$1::uuid AND a.directory_id=$2::uuid AND a.disabled_at IS NULL AND i.issuer=$3 AND i.subject=$4)`, a.Account.ID, a.DirectoryID, a.Issuer, a.Subject).Scan(&active)
	if err != nil {
		return empty, err
	}
	if !active {
		return empty, monitoring.ErrForbidden
	}
	var priorID string
	var priorKey []byte
	err = tx.QueryRow(ctx, `SELECT task_id::text,request_key FROM dashboard_report_idempotency WHERE account_id=$1::uuid AND key_hash=$2`, a.Account.ID, keyHash).Scan(&priorID, &priorKey)
	if err == nil {
		if !bytes.Equal(priorKey, requestFingerprint) {
			return empty, reports.ErrConflict
		}
		task, err := scanReport(tx.QueryRow(ctx, `SELECT `+reportColumns+` FROM dashboard_report_tasks t WHERE t.id=$1::uuid AND t.expires_at>clock_timestamp()`, priorID))
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, reports.ErrTaskNotFound
		}
		if err != nil {
			return empty, err
		}
		// The current caller alias was verified above and source-authorized by
		// the service. Refresh its background candidate without changing task
		// ownership, immutable subject, or the idempotent response identity.
		tag, err := tx.Exec(ctx, `UPDATE dashboard_report_requesters SET issuer=$3,subject=$4 WHERE task_id=$1::uuid AND account_id=$2::uuid`, priorID, a.Account.ID, a.Issuer, a.Subject)
		if err != nil {
			return empty, err
		}
		if tag.RowsAffected() != 1 {
			return empty, reports.ErrTaskNotFound
		}
		if err = tx.Commit(ctx); err != nil {
			return empty, err
		}
		return task, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, err
	}
	var recent int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM dashboard_report_idempotency WHERE account_id=$1::uuid AND created_at>clock_timestamp()-interval '1 minute'`, a.Account.ID).Scan(&recent); err != nil {
		return empty, err
	}
	if recent >= 10 {
		return empty, reports.ErrLimit
	}
	// Existing ready objects can be shared only through an independently owned
	// requester link and later current source checks. Failed tasks are not reused.
	task, err := scanReport(tx.QueryRow(ctx, `SELECT `+reportColumns+` FROM dashboard_report_tasks t WHERE t.equivalent_key=$1 AND t.state<>'failed' AND t.expires_at>clock_timestamp() ORDER BY (t.state='ready') DESC,t.created_at DESC LIMIT 1`, fingerprint))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return empty, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var global, personal, retained int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM dashboard_report_tasks`).Scan(&retained); err != nil {
			return empty, err
		}
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM dashboard_report_tasks WHERE state IN ('queued','collecting','analyzing')`).Scan(&global); err != nil {
			return empty, err
		}
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM dashboard_report_requesters r JOIN dashboard_report_tasks t ON t.id=r.task_id WHERE r.account_id=$1::uuid AND t.state IN ('queued','collecting','analyzing')`, a.Account.ID).Scan(&personal); err != nil {
			return empty, err
		}
		if global >= reports.MaximumPending || personal >= reports.MaximumAccountPending || retained >= reports.MaximumRetainedTasks {
			return empty, reports.ErrLimit
		}
		id, idErr := newID()
		if idErr != nil {
			return empty, idErr
		}
		task, err = scanReport(tx.QueryRow(ctx, `INSERT INTO dashboard_report_tasks AS t(id,deployment_id,namespace_id,job_id,equivalent_key,subject,state) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,'queued') RETURNING `+reportColumns, id, subject.DeploymentID, subject.NamespaceID, subject.JobID, fingerprint, encoded))
		if err != nil {
			return empty, err
		}
	}
	var bound bool
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_or(account_id=$2::uuid),false) FROM dashboard_report_requesters WHERE task_id=$1::uuid`, task.ID, a.Account.ID).Scan(&count, &bound); err != nil {
		return empty, err
	}
	if !bound && count >= reports.MaximumRequesters {
		return empty, reports.ErrLimit
	}
	if !bound && task.State != "ready" {
		var pending int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM dashboard_report_requesters r JOIN dashboard_report_tasks t ON t.id=r.task_id WHERE r.account_id=$1::uuid AND t.state IN ('queued','collecting','analyzing')`, a.Account.ID).Scan(&pending); err != nil {
			return empty, err
		}
		if pending >= reports.MaximumAccountPending {
			return empty, reports.ErrLimit
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO dashboard_report_requesters(task_id,account_id,issuer,subject) VALUES($1::uuid,$2::uuid,$3,$4) ON CONFLICT(task_id,account_id) DO UPDATE SET issuer=EXCLUDED.issuer,subject=EXCLUDED.subject`, task.ID, a.Account.ID, a.Issuer, a.Subject); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO dashboard_report_idempotency(account_id,key_hash,request_key,task_id) VALUES($1::uuid,$2,$3,$4::uuid)`, a.Account.ID, keyHash, requestFingerprint, task.ID); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO dashboard_audit(account_id,action,resource_kind,resource_id) VALUES($1::uuid,'diagnosis.request','report_task',$2)`, a.Account.ID, task.ID); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return task, nil
}

// ClaimReport never holds a transaction over source, filesystem or analyzer I/O.
// A unique lease token fences any older worker after a restart or lost lease.
func (s *Store) ClaimReport(ctx context.Context) (reports.Claim, error) {
	var claim reports.Claim
	token, err := newID()
	if err != nil {
		return claim, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return claim, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE dashboard_report_tasks SET state='failed',failure_code='worker_interrupted',lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE id IN (SELECT id FROM dashboard_report_tasks WHERE state IN ('collecting','analyzing') AND lease_expires_at<=clock_timestamp() AND attempts>=3 ORDER BY lease_expires_at LIMIT 32 FOR UPDATE SKIP LOCKED)`)
	if err != nil {
		return claim, err
	}
	claim.Task, err = scanReport(tx.QueryRow(ctx, `UPDATE dashboard_report_tasks AS t SET state='collecting',attempts=attempts+1,lease_token=$1::uuid,lease_expires_at=clock_timestamp()+interval '90 seconds',updated_at=clock_timestamp(),failure_code='' WHERE t.id=(SELECT id FROM dashboard_report_tasks WHERE expires_at>clock_timestamp() AND attempts<3 AND ((state='queued' AND next_attempt_at<=clock_timestamp()) OR (state IN ('collecting','analyzing') AND lease_expires_at<=clock_timestamp())) ORDER BY next_attempt_at,created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING `+reportColumns, token))
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.Commit(ctx); err != nil {
			return claim, err
		}
		return claim, reports.ErrLease
	}
	if err != nil {
		return claim, err
	}
	rows, err := tx.Query(ctx, `SELECT a.id::text,a.display_name,a.directory_id::text,i.issuer,i.subject FROM dashboard_report_requesters r JOIN dashboard_accounts a ON a.id=r.account_id JOIN dashboard_identity_aliases i ON i.account_id=a.id AND i.issuer=r.issuer AND i.subject=r.subject WHERE r.task_id=$1::uuid AND a.disabled_at IS NULL ORDER BY r.requested_at,r.account_id LIMIT 32`, claim.Task.ID)
	if err != nil {
		return claim, err
	}
	for rows.Next() {
		var actor monitoring.Actor
		if err = rows.Scan(&actor.Account.ID, &actor.Account.DisplayName, &actor.DirectoryID, &actor.Issuer, &actor.Subject); err != nil {
			rows.Close()
			return claim, err
		}
		claim.Requesters = append(claim.Requesters, actor)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return claim, err
	}
	if err = tx.Commit(ctx); err != nil {
		return reports.Claim{}, err
	}
	return claim, nil
}

func (s *Store) AnalyzeReport(ctx context.Context, id, lease string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE dashboard_report_tasks SET state='analyzing',updated_at=clock_timestamp() WHERE id=$1::uuid AND lease_token=$2::uuid AND state='collecting' AND lease_expires_at>clock_timestamp()`, id, lease)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return reports.ErrLease
	}
	return nil
}

// CompleteReport is called only after pair verification, durable object write,
// and a final current authorization check. A failed lease leaves an orphan for
// the bounded object janitor, never a ready reference to incomplete bytes.
func (s *Store) CompleteReport(ctx context.Context, id, lease string, object reports.Object, actor monitoring.Actor) error {
	if object.Validate() != nil || object.ID != id {
		return reports.ErrInvalid
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return reports.ErrInvalid
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE dashboard_report_tasks SET state='ready',object=$3,lease_token=NULL,lease_expires_at=NULL,failure_code='',updated_at=clock_timestamp() WHERE id=$1::uuid AND lease_token=$2::uuid AND state='analyzing' AND lease_expires_at>clock_timestamp() AND expires_at>clock_timestamp() AND EXISTS (SELECT 1 FROM dashboard_report_requesters r JOIN dashboard_accounts a ON a.id=r.account_id JOIN dashboard_identity_aliases i ON i.account_id=a.id WHERE r.task_id=$1::uuid AND a.id=$4::uuid AND a.directory_id=$5::uuid AND a.disabled_at IS NULL AND i.issuer=$6 AND i.subject=$7)`, id, lease, encoded, actor.Account.ID, actor.DirectoryID, actor.Issuer, actor.Subject)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return reports.ErrLease
	}
	return nil
}

func (s *Store) FailReport(ctx context.Context, id, lease, code string) error {
	transient := code == "source_unavailable" || code == "authorization_unavailable"
	switch code {
	case "source_unavailable", "authorization_unavailable", "forbidden", "snapshot_changed", "invalid_evidence", "analysis_failed":
	default:
		return reports.ErrInvalid
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE dashboard_report_tasks SET state=CASE WHEN $4 AND attempts<3 THEN 'queued' ELSE 'failed' END,next_attempt_at=clock_timestamp()+(attempts*attempts)*interval '1 second',failure_code=$3,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1::uuid AND lease_token=$2::uuid AND state IN ('collecting','analyzing') AND lease_expires_at>clock_timestamp()`, id, lease, code, transient)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return reports.ErrLease
	}
	return nil
}

func (s *Store) ReportTask(ctx context.Context, account, id string) (reports.Task, error) {
	task, err := scanReport(s.Pool.QueryRow(ctx, `SELECT `+reportColumns+` FROM dashboard_report_tasks t JOIN dashboard_report_requesters r ON r.task_id=t.id JOIN dashboard_accounts a ON a.id=r.account_id WHERE t.id=$1::uuid AND r.account_id=$2::uuid AND a.disabled_at IS NULL AND t.expires_at>clock_timestamp()`, id, account))
	if errors.Is(err, pgx.ErrNoRows) {
		err = reports.ErrTaskNotFound
	}
	return task, err
}

// VerifyReportActor rechecks the persisted verified alias without storing or
// reusing a bearer token. Source authorization remains a separate live check.
func (s *Store) VerifyReportActor(ctx context.Context, id string, a monitoring.Actor) error {
	var active bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_report_requesters r JOIN dashboard_accounts a ON a.id=r.account_id JOIN dashboard_identity_aliases i ON i.account_id=a.id JOIN dashboard_report_tasks t ON t.id=r.task_id WHERE r.task_id=$1::uuid AND a.id=$2::uuid AND a.directory_id=$3::uuid AND a.disabled_at IS NULL AND i.issuer=$4 AND i.subject=$5 AND t.expires_at>clock_timestamp())`, id, a.Account.ID, a.DirectoryID, a.Issuer, a.Subject).Scan(&active)
	if err != nil {
		return err
	}
	if !active {
		return monitoring.ErrForbidden
	}
	return nil
}

func (s *Store) ListReportTasks(ctx context.Context, account string, scope api.Scope, job, before string, limit int) ([]reports.Task, error) {
	if limit < 1 || limit > 21 {
		return nil, reports.ErrInvalid
	}
	var cursor reports.Task
	var err error
	if before != "" {
		cursor, err = s.ReportTask(ctx, account, before)
		if err != nil {
			return nil, err
		}
		if cursor.Subject.Scope != scope || cursor.Subject.JobID != job {
			return nil, reports.ErrTaskNotFound
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+reportColumns+` FROM dashboard_report_tasks t JOIN dashboard_report_requesters r ON r.task_id=t.id JOIN dashboard_accounts a ON a.id=r.account_id WHERE r.account_id=$1::uuid AND a.disabled_at IS NULL AND t.deployment_id=$2::uuid AND t.namespace_id=$3::uuid AND t.job_id=$4::uuid AND t.expires_at>clock_timestamp() AND ($5='' OR (t.created_at,t.id)<($6,$7::uuid)) ORDER BY t.created_at DESC,t.id DESC LIMIT $8`, account, scope.DeploymentID, scope.NamespaceID, job, before, cursor.CreatedAt, nullableReportID(before), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []reports.Task{}
	for rows.Next() {
		item, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func nullableReportID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func (s *Store) ReportObjectReferenced(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_report_tasks WHERE id=$1::uuid)`, id).Scan(&exists)
	return exists, err
}

// DeleteExpiredReports returns private object references after removing their
// entire task/requester/idempotency family. The filesystem janitor also reclaims
// unreferenced objects left by crashes, after a grace period.
func (s *Store) DeleteExpiredReports(ctx context.Context) ([]reports.Object, error) {
	rows, err := s.Pool.Query(ctx, `DELETE FROM dashboard_report_tasks WHERE id IN (SELECT id FROM dashboard_report_tasks WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 100 FOR UPDATE SKIP LOCKED) RETURNING object`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	objects := []reports.Object{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if data != nil {
			var object reports.Object
			if json.Unmarshal(data, &object) != nil || object.Validate() != nil {
				return nil, reports.ErrInvalid
			}
			objects = append(objects, object)
		}
	}
	return objects, rows.Err()
}
