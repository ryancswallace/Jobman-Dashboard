package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/reports"
)

func reportActor(t *testing.T, s *Store, n int) monitoring.Actor {
	t.Helper()
	a, err := s.ResolveIdentity(t.Context(), auth.Identity{Issuer: "https://synthetic.example", Subject: fmt.Sprintf("actor-%d", n), DirectoryID: fmt.Sprintf("10000000-0000-4000-8000-%012d", n), DisplayName: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func reportSubject() reports.Subject {
	return reports.Subject{Scope: api.Scope{DeploymentID: "20000000-0000-4000-8000-000000000001", NamespaceID: "30000000-0000-4000-8000-000000000001"}, ControlInstanceID: "40000000-0000-4000-8000-000000000001", JobID: "50000000-0000-4000-8000-000000000001", RecoveryEpoch: "1", Revision: "1", Profile: "metadata", SnapshotFingerprint: strings.Repeat("a", 64), EngineVersion: "test", CollectorVersion: "test", CompanionVersion: "test", JobmanVersion: "test"}
}
func reportObject(id string) reports.Object {
	return reports.Object{ID: id, SHA256: strings.Repeat("a", 64), ReportID: "sha256:" + strings.Repeat("b", 64), EvidenceID: "sha256:" + strings.Repeat("c", 64), AnalysisEvidenceID: "sha256:" + strings.Repeat("d", 64)}
}

func TestReportQueueConcurrentDedupOwnershipAndLeaseFencing(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	alice, bob := reportActor(t, s, 1), reportActor(t, s, 2)
	subject := reportSubject()
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := alice
			if i%2 == 1 {
				a = bob
			}
			task, err := s.EnqueueReport(ctx, a, subject, fmt.Sprintf("synthetic-request-%02d", i))
			if err != nil {
				t.Error(err)
				return
			}
			ids <- task.ID
		}()
	}
	wg.Wait()
	close(ids)
	taskID := ""
	for id := range ids {
		if taskID != "" && taskID != id {
			t.Fatal("equivalent request duplicated task")
		}
		taskID = id
	}
	if taskID == "" {
		t.Fatal("no task admitted")
	}
	advanced := subject
	advanced.Revision = "2"
	advanced.SnapshotFingerprint = strings.Repeat("f", 64)
	if replay, err := s.EnqueueReport(ctx, alice, advanced, "synthetic-request-00"); err != nil || replay.ID != taskID || replay.Subject.Revision != subject.Revision {
		t.Fatalf("HTTP retry after source advance lost original task: %v", err)
	}
	changed := subject
	changed.Profile = "include_log_tail"
	if _, err := s.EnqueueReport(ctx, alice, changed, "synthetic-request-00"); !errors.Is(err, reports.ErrConflict) {
		t.Fatalf("idempotency did not bind subject: %v", err)
	}
	if _, err := s.ReportTask(ctx, "10000000-0000-4000-8000-000000000009", taskID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("unrelated account read shared task")
	}
	claim, err := s.ClaimReport(ctx)
	if err != nil || claim.Task.ID != taskID || len(claim.Requesters) != 2 || claim.Task.Attempts != 1 {
		t.Fatalf("claim: %#v %v", claim, err)
	}
	if _, err = s.ClaimReport(ctx); !errors.Is(err, reports.ErrLease) {
		t.Fatal("active lease double claimed")
	}
	oldLease := claim.Task.LeaseToken
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_report_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1::uuid`, taskID); err != nil {
		t.Fatal(err)
	}
	claim, err = s.ClaimReport(ctx)
	if err != nil || claim.Task.LeaseToken == oldLease || claim.Task.Attempts != 2 {
		t.Fatalf("stale worker not fenced: %v", err)
	}
	if err = s.AnalyzeReport(ctx, taskID, oldLease); !errors.Is(err, reports.ErrLease) {
		t.Fatal("old worker advanced task")
	}
	if err = s.AnalyzeReport(ctx, taskID, claim.Task.LeaseToken); err != nil {
		t.Fatal(err)
	}
	object := reportObject(taskID)
	if err = s.CompleteReport(ctx, taskID, oldLease, object); !errors.Is(err, reports.ErrLease) {
		t.Fatal("stale worker published object")
	}
	if err = s.CompleteReport(ctx, taskID, claim.Task.LeaseToken, object); err != nil {
		t.Fatal(err)
	}
	task, err := s.ReportTask(ctx, bob.Account.ID, taskID)
	if err != nil || task.State != "ready" || task.Object == nil || task.Object.ReportID != object.ReportID {
		t.Fatalf("ready pair unavailable: %v", err)
	}
	reused, err := s.EnqueueReport(ctx, alice, subject, "synthetic-ready-reuse")
	if err != nil || reused.ID != taskID {
		t.Fatal("ready pair was not reused")
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_accounts SET disabled_at=clock_timestamp() WHERE id=$1::uuid`, alice.Account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnqueueReport(ctx, alice, subject, "synthetic-disabled-user"); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("disabled account admitted")
	}
	if _, err = s.ReportTask(ctx, alice.Account.ID, taskID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("disabled account read task")
	}
}

func TestReportQueueLimitsRetriesAndPairedRetention(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	a := reportActor(t, s, 1)
	subject := reportSubject()
	for i := range reports.MaximumAccountPending {
		next := subject
		next.Revision = fmt.Sprint(i + 1)
		if _, err := s.EnqueueReport(ctx, a, next, fmt.Sprintf("synthetic-limit-%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	next := subject
	next.Revision = "100"
	if _, err := s.EnqueueReport(ctx, a, next, "synthetic-limit-overflow"); !errors.Is(err, reports.ErrLimit) {
		t.Fatalf("personal queue ceiling absent: %v", err)
	}
	claim, err := s.ClaimReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FailReport(ctx, claim.Task.ID, claim.Task.LeaseToken, "invalid_evidence"); err != nil {
		t.Fatal(err)
	}
	task, err := s.ReportTask(ctx, a.Account.ID, claim.Task.ID)
	if err != nil || task.State != "failed" {
		t.Fatal("invalid evidence was retried")
	}
	// Focus subsequent claims on one task and exercise both transport retries and
	// recovery of a worker that exhausts its final lease without reporting failure.
	claim, err = s.ClaimReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_report_tasks SET next_attempt_at=clock_timestamp()+interval '1 day' WHERE id<>$1::uuid`, claim.Task.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.FailReport(ctx, claim.Task.ID, claim.Task.LeaseToken, "source_unavailable"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_report_tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1::uuid`, claim.Task.ID); err != nil {
			t.Fatal(err)
		}
		claim, err = s.ClaimReport(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_report_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1::uuid`, claim.Task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.ClaimReport(ctx); !errors.Is(err, reports.ErrLease) {
		t.Fatal("exhausted task kept retrying")
	}
	task, err = s.ReportTask(ctx, a.Account.ID, claim.Task.ID)
	if err != nil || task.State != "failed" || task.FailureCode != "worker_interrupted" {
		t.Fatalf("final expired lease not durably failed: %v", err)
	}
	// An entire pair reference and all request bindings expire together.
	object := reportObject(task.ID)
	data, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_report_tasks SET state='ready',object=$2,created_at=clock_timestamp()-interval '31 days',expires_at=clock_timestamp()-interval '1 second' WHERE id=$1::uuid`, task.ID, data); err != nil {
		t.Fatal(err)
	}
	objects, err := s.DeleteExpiredReports(ctx)
	if err != nil || len(objects) != 1 || objects[0].ID != task.ID {
		t.Fatalf("paired retention: %v", err)
	}
	var bindings int
	if err = s.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM dashboard_report_requesters WHERE task_id=$1::uuid)+(SELECT count(*) FROM dashboard_report_idempotency WHERE task_id=$1::uuid)`, task.ID).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatal("expired request bindings survived")
	}
}
