package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/operations"
)

func TestOperationalReportPressureStatesAndScope(t *testing.T) {
	s := testDB(t)
	actor := reportActor(t, s, 1)
	source := reportSubject().DeploymentID
	states := []string{"queued", "collecting", "analyzing", "ready", "failed"}
	for i, state := range states {
		subject := reportSubject()
		subject.JobID = fmt.Sprintf("50000000-0000-4000-8000-%012d", i+1)
		task, err := s.EnqueueReport(t.Context(), actor, subject, fmt.Sprintf("synthetic-pressure-request-%02d", i))
		if err != nil {
			t.Fatal(err)
		}
		deviceExec(t, s, `UPDATE dashboard_report_tasks SET state=$2,created_at=clock_timestamp()-interval '2 minutes', lease_token=CASE WHEN $2 IN('collecting','analyzing') THEN '10000000-0000-4000-8000-000000000099'::uuid END, lease_expires_at=CASE WHEN $2='collecting' THEN clock_timestamp()+interval '30 seconds' WHEN $2='analyzing' THEN clock_timestamp()-interval '1 second' END, object=CASE WHEN $2='ready' THEN 'x'::bytea END WHERE id=$1::uuid`, task.ID, state)
	}
	p, err := s.OperationalReportPressure(t.Context(), []string{source})
	if err != nil || p.Pending != "3" || p.Leased != "1" || p.OldestAt == nil || p.ObservedAt.Sub(*p.OldestAt) < time.Minute {
		t.Fatal("wrong report queue projection", p, err)
	}
	for _, ids := range [][]string{nil, {"20000000-0000-4000-8000-000000000002"}} {
		p, err = s.OperationalReportPressure(t.Context(), ids)
		if err != nil || p.Pending != "0" || p.Leased != "0" || p.OldestAt != nil {
			t.Fatal("scope broadened", p, err)
		}
	}
	deviceExec(t, s, `UPDATE dashboard_report_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE lease_token IS NOT NULL`)
	p, err = s.OperationalReportPressure(t.Context(), []string{source})
	if err != nil || p.Leased != "0" || p.Pending != "3" {
		t.Fatal("expired lease counted live", p, err)
	}
}
func TestOperationalReportPressureCancellationAndLockBound(t *testing.T) {
	s := testDB(t)
	ids := []string{reportSubject().DeploymentID}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p, err := s.OperationalReportPressure(ctx, ids)
	if !errors.Is(err, operations.ErrStatusUnavailable) || !p.ObservedAt.IsZero() {
		t.Fatal("cancelled query published", err)
	}
	if _, err = s.OperationalReportPressure(t.Context(), append(ids, ids[0])); !errors.Is(err, operations.ErrStatusInvalid) {
		t.Fatal("duplicate scope accepted", err)
	}
	tx, err := s.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), `LOCK TABLE dashboard_report_tasks IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	p, err = s.OperationalReportPressure(t.Context(), ids)
	if !errors.Is(err, operations.ErrStatusUnavailable) || !p.ObservedAt.IsZero() || time.Since(start) > 3*time.Second {
		t.Fatal("unbounded or partial pressure result", err, time.Since(start))
	}
}
