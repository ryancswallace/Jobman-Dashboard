package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/operations"
)

func (s *Store) OperationalReportPressure(parent context.Context, deployments []string) (operations.ReportPressure, error) {
	ids, e := operations.CanonicalStatusDeployments(deployments)
	if e != nil {
		return operations.ReportPressure{}, e
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	tx, e := s.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if e != nil {
		return operations.ReportPressure{}, operations.ErrStatusUnavailable
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='2s'; SET LOCAL lock_timeout='500ms'`); e != nil {
		return operations.ReportPressure{}, operations.ErrStatusUnavailable
	}
	var p operations.ReportPressure
	e = tx.QueryRow(ctx, `SELECT transaction_timestamp(),count(*)::text,count(*) FILTER(WHERE lease_expires_at>transaction_timestamp())::text,min(created_at) FROM dashboard_report_tasks WHERE deployment_id=ANY($1::uuid[]) AND state IN('queued','collecting','analyzing')`, ids).Scan(&p.ObservedAt, &p.Pending, &p.Leased, &p.OldestAt)
	if e != nil || p.Validate() != nil || tx.Commit(ctx) != nil {
		return operations.ReportPressure{}, operations.ErrStatusUnavailable
	}
	return p, nil
}
