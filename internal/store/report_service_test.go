package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/reports"
	"github.com/ryancswallace/jobman-diagnose/diagnosis"
	"github.com/ryancswallace/jobman/diagnostic"
)

type reportSource struct {
	snapshot            reports.Snapshot
	denied              bool
	discoveries, denyAt int
	changeAt            int
	change              func()
}

func (s *reportSource) ID() string { return s.snapshot.Value.Source.DeploymentID }
func (s *reportSource) Discover(context.Context, monitoring.Actor) (monitoring.Discovery, error) {
	s.discoveries++
	if s.changeAt > 0 && s.discoveries == s.changeAt {
		s.change()
	}
	if s.denied || s.denyAt > 0 && s.discoveries >= s.denyAt {
		return monitoring.Discovery{}, monitoring.ErrForbidden
	}
	return monitoring.Discovery{InstanceID: s.snapshot.Value.Source.ControlInstanceID, RecoveryEpoch: s.snapshot.RecoveryEpoch, Deployment: api.Deployment{ID: s.ID(), Status: "available", Namespaces: []api.Namespace{{ID: s.snapshot.Value.Source.NamespaceID, Capabilities: []string{"jobs.read", "evidence.read", "logs.read"}, AuthorizationExpiresAt: time.Now().Add(time.Minute)}}}}, nil
}
func (s *reportSource) DiagnosticSnapshot(_ context.Context, _ monitoring.Actor, selection diagnostic.SharedSelection) (reports.Snapshot, error) {
	if s.denied {
		return reports.Snapshot{}, monitoring.ErrForbidden
	}
	v := s.snapshot
	if selection.JobID != v.Value.Job.ID || selection.NamespaceID != v.Value.Source.NamespaceID || selection.ControlInstanceID != v.Value.Source.ControlInstanceID {
		return reports.Snapshot{}, monitoring.ErrNotFound
	}
	if selection.ExpectedJobRevision != 0 && selection.ExpectedJobRevision != v.Value.Job.Revision {
		return reports.Snapshot{}, &api.Error{Code: "snapshot_changed", Message: "changed"}
	}
	v.Value.CapturedAt = time.Now().UTC()
	return v, nil
}
func newReportService(t *testing.T, s *Store) (*reports.Service, *reportSource, *reports.ObjectStore) {
	t.Helper()
	raw, err := os.ReadFile("../reports/testdata/shared-control-failure-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	core, err := diagnostic.Decode(bytes.NewReader(raw), diagnostic.DecodeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	source := &reportSource{snapshot: reports.Snapshot{RecoveryEpoch: "1", Value: diagnostic.SharedSnapshot{Kind: diagnostic.SharedSnapshotKind, SchemaVersion: 1, CapturedAt: core.CapturedAt, Source: core.Shared.Source, JobmanVersion: core.Source.JobmanVersion, Platform: core.Source.Platform, Job: diagnostic.SharedJob{ID: core.Subject.JobID, Revision: core.Subject.JobRevision, Phase: core.Subject.Phase, Outcome: core.Subject.Outcome}, Runs: core.Shared.Runs, Metadata: core.Consistency.Metadata, Items: core.Items, Logs: core.Shared.Logs, Omissions: []diagnostic.Omission{}, RedactionNotices: []diagnostic.RedactionNotice{}}}}
	objects, err := reports.OpenObjects(filepath.Join(t.TempDir(), "objects"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = objects.Close() })
	service, err := reports.NewService(reports.ServiceConfig{Sources: []reports.Source{source}, Queue: s, Objects: objects, CompanionVersion: "synthetic-test"})
	if err != nil {
		t.Fatal(err)
	}
	return service, source, objects
}
func sourceRequest(s *reportSource, key string) reports.Request {
	return reports.Request{Scope: api.Scope{DeploymentID: s.ID(), NamespaceID: s.snapshot.Value.Source.NamespaceID}, JobID: s.snapshot.Value.Job.ID, Profile: "metadata", IdempotencyKey: key}
}

func TestReportServiceRealDatabaseEngineObjectsAndCurrentAuthorization(t *testing.T) {
	db := testDB(t)
	a := reportActor(t, db, 1)
	b := reportActor(t, db, 2)
	service, source, _ := newReportService(t, db)
	r := sourceRequest(source, "synthetic-service-request")
	queued, err := service.Request(t.Context(), a, r)
	if err != nil || queued.Task.State != "queued" || queued.Task.Subject.RunID != "" {
		t.Fatalf("queue %v", err)
	}
	if err = service.RunOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	ready, err := service.Get(t.Context(), a, queued.Task.ID)
	if err != nil || ready.Task.State != "ready" || ready.Outdated || ready.Pair == nil {
		t.Fatalf("ready %v", err)
	}
	if err = diagnosis.ValidateAgainstEvidence(ready.Pair.Report, ready.Pair.Evidence); err != nil {
		t.Fatal(err)
	}
	if ready.Pair.Report.Mode != diagnosis.ModeDeterministic || ready.Pair.Report.Disclosure.ProviderInvoked || len(ready.Pair.Report.Findings) == 0 {
		t.Fatal("report not actual deterministic diagnosis")
	}
	if _, err = service.Get(t.Context(), b, queued.Task.ID); !errors.Is(err, reports.ErrTaskNotFound) {
		t.Fatal("cross-account task disclosed")
	}
	page, err := service.List(t.Context(), a, r.Scope, r.JobID, "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor != "" || page.Items[0].Pair != nil {
		t.Fatalf("catalog %v", err)
	}
	source.snapshot.Value.Job.Revision++
	stale, err := service.Get(t.Context(), a, queued.Task.ID)
	if err != nil || !stale.Outdated || stale.Pair.Report.ReportID != ready.Pair.Report.ReportID {
		t.Fatalf("point-in-time report relabeled %v", err)
	}
	replay, err := service.Request(t.Context(), a, r)
	if err != nil || replay.Task.ID != queued.Task.ID || !replay.Outdated {
		t.Fatalf("idempotent retry lost original %v", err)
	}
	r.IdempotencyKey = "synthetic-new-revision-request"
	next, err := service.Request(t.Context(), a, r)
	if err != nil || next.Task.ID == queued.Task.ID {
		t.Fatal("new revision reused old request")
	}
	page, err = service.List(t.Context(), a, r.Scope, r.JobID, "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor != next.Task.ID {
		t.Fatalf("page1 %v", err)
	}
	page, err = service.List(t.Context(), a, r.Scope, r.JobID, page.NextCursor, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].Task.ID != queued.Task.ID {
		t.Fatalf("page2 %v", err)
	}
	if _, err = service.List(t.Context(), b, r.Scope, r.JobID, next.Task.ID, 1); !errors.Is(err, reports.ErrTaskNotFound) {
		t.Fatal("cursor not account bound")
	}
	source.denyAt = source.discoveries + 2
	if _, err = service.Get(t.Context(), a, queued.Task.ID); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("access removed during stored-pair read was retained")
	}
	source.denyAt = 0
	source.snapshot.RecoveryEpoch = "2"
	if _, err = service.Get(t.Context(), a, queued.Task.ID); err == nil {
		t.Fatal("restored source returned stale envelope")
	}
	source.snapshot.RecoveryEpoch = "1"
	r.Profile = "include_log_tail"
	if _, err = service.Request(t.Context(), a, r); !errors.Is(err, reports.ErrRedactionUnavailable) {
		t.Fatal("log profile ignored missing policy")
	}
}

func TestReportWorkerDenialChangeAndPublishedObjectRecovery(t *testing.T) {
	for _, mode := range []string{"revoked", "disabled", "changed", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			db := testDB(t)
			a := reportActor(t, db, 1)
			service, source, objects := newReportService(t, db)
			r := sourceRequest(source, "synthetic-worker-request")
			queued, err := service.Request(t.Context(), a, r)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "revoked":
				source.denied = true
			case "disabled":
				if _, err = db.Pool.Exec(t.Context(), `UPDATE dashboard_accounts SET disabled_at=clock_timestamp() WHERE id=$1::uuid`, a.Account.ID); err != nil {
					t.Fatal(err)
				}
			case "changed":
				source.snapshot.Value.Job.Revision++
			case "recovery":
				if err = service.RunOne(t.Context()); err != nil {
					t.Fatal(err)
				}
				ready, err := service.Get(t.Context(), a, queued.Task.ID)
				if err != nil {
					t.Fatal(err)
				}
				original := ready.Pair.Report.ReportID
				// Model a restored task row from before its ready commit while the
				// fsynced object survived. The worker must recover identical IDs.
				if _, err = db.Pool.Exec(t.Context(), `UPDATE dashboard_report_tasks SET state='queued',object=NULL,attempts=0 WHERE id=$1::uuid`, queued.Task.ID); err != nil {
					t.Fatal(err)
				}
				if err = service.RunOne(t.Context()); err != nil {
					t.Fatal(err)
				}
				again, err := service.Get(t.Context(), a, queued.Task.ID)
				if err != nil || again.Pair.Report.ReportID != original {
					t.Fatalf("publication recovery %v", err)
				}
				return
			}
			if err = service.RunOne(t.Context()); err == nil {
				t.Fatal("ineligible task completed")
			}
			var state, code string
			if err = db.Pool.QueryRow(t.Context(), `SELECT state,failure_code FROM dashboard_report_tasks WHERE id=$1::uuid`, queued.Task.ID).Scan(&state, &code); err != nil {
				t.Fatal(err)
			}
			want := "forbidden"
			if mode == "changed" {
				want = "snapshot_changed"
			}
			if state != "failed" || code != want {
				t.Fatalf("wrong failure %s/%s", state, code)
			}
			if _, _, err = objects.Recover(t.Context(), queued.Task.Subject, queued.Task.ID); err == nil {
				t.Fatal("ineligible task published object")
			}
		})
	}
}

func TestReportCatalogRechecksSourceIdentityAtFinalCapture(t *testing.T) {
	for _, kind := range []string{"epoch", "instance"} {
		t.Run(kind, func(t *testing.T) {
			db := testDB(t)
			a := reportActor(t, db, 1)
			service, source, _ := newReportService(t, db)
			r := sourceRequest(source, "synthetic-final-source-change")
			if _, err := service.Request(t.Context(), a, r); err != nil {
				t.Fatal(err)
			}
			source.changeAt = source.discoveries + 3
			source.change = func() {
				if kind == "epoch" {
					source.snapshot.RecoveryEpoch = "2"
				} else {
					source.snapshot.Value.Source.ControlInstanceID = "80000000-0000-4000-8000-000000000001"
				}
			}
			page, err := service.List(t.Context(), a, r.Scope, r.JobID, "", 20)
			if err == nil || len(page.Items) > 0 {
				t.Fatal("catalog exposed rows after final source identity change")
			}
		})
	}
}

func TestReportsFollowImmutableAccountAcrossVerifiedAliases(t *testing.T) {
	db := testDB(t)
	a := reportActor(t, db, 1)
	service, source, _ := newReportService(t, db)
	r := sourceRequest(source, "synthetic-account-alias-request")
	original, err := service.Request(t.Context(), a, r)
	if err != nil {
		t.Fatal(err)
	}
	alias, err := db.ResolveIdentity(t.Context(), auth.Identity{Issuer: "https://synthetic-other-client.example", Subject: "approved-native-alias", DirectoryID: a.DirectoryID, DisplayName: "Same person"})
	if err != nil || alias.Account.ID != a.Account.ID {
		t.Fatal("verified aliases did not share account", err)
	}
	if _, err = service.Get(t.Context(), alias, original.Task.ID); err != nil {
		t.Fatal("approved alias could not access account task", err)
	}
	if page, err := service.List(t.Context(), alias, r.Scope, r.JobID, "", 20); err != nil || len(page.Items) != 1 {
		t.Fatal("approved alias could not list account task", err)
	}
	replay, err := service.Request(t.Context(), alias, r)
	if err != nil || replay.Task.ID != original.Task.ID {
		t.Fatal("alias changed idempotency ownership", err)
	}
	var candidate string
	if err = db.Pool.QueryRow(t.Context(), `SELECT subject FROM dashboard_report_requesters WHERE task_id=$1::uuid AND account_id=$2::uuid`, original.Task.ID, a.Account.ID).Scan(&candidate); err != nil || candidate != alias.Subject {
		t.Fatal("authorized replay did not refresh worker alias", err)
	}
	if _, err = db.Pool.Exec(t.Context(), `DELETE FROM dashboard_identity_aliases WHERE account_id=$1::uuid AND issuer=$2 AND subject=$3`, a.Account.ID, a.Issuer, a.Subject); err != nil {
		t.Fatal(err)
	}
	if err = service.RunOne(t.Context()); err != nil {
		t.Fatal("worker did not use current verified alias", err)
	}
	if result, err := service.Get(t.Context(), alias, original.Task.ID); err != nil || result.Pair == nil {
		t.Fatal("same-account report unavailable", err)
	}
	if _, err = service.Get(t.Context(), a, original.Task.ID); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("removed alias retained access")
	}
}
