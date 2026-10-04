package notifications

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type reconciliationRepository struct {
	candidates []ReconciliationCandidate
	verify     func(ReconciliationCandidate) error
}

func (r *reconciliationRepository) NotificationReconciliationCandidates(context.Context, string, string) ([]ReconciliationCandidate, error) {
	return r.candidates, nil
}
func (r *reconciliationRepository) VerifyNotificationReconciliationCandidate(_ context.Context, c ReconciliationCandidate) error {
	if r.verify != nil {
		return r.verify(c)
	}
	return nil
}

type reconciliationSource struct {
	monitoring.Source
	discover func(monitoring.Actor) (monitoring.Discovery, error)
	jobs     func(monitoring.SourceQuery) (monitoring.JobPage, error)
	job      func(api.Scope, string) (api.Job, error)
}

func (s *reconciliationSource) ID() string { return fixtureCheckpoint().DeploymentID }
func (s *reconciliationSource) Discover(_ context.Context, a monitoring.Actor) (monitoring.Discovery, error) {
	return s.discover(a)
}
func (s *reconciliationSource) Jobs(_ context.Context, _ monitoring.Actor, q monitoring.SourceQuery) (monitoring.JobPage, error) {
	return s.jobs(q)
}
func (s *reconciliationSource) Job(_ context.Context, _ monitoring.Actor, q api.Scope, id string) (api.Job, error) {
	return s.job(q, id)
}
func reconciliationSetup(t *testing.T) (*Reconciler, *reconciliationRepository, *reconciliationSource, events.RecoveryPlan) {
	t.Helper()
	cp := fixtureCheckpoint()
	p := events.RecoveryPlan{ID: fixtureID(10, 1), GapID: fixtureID(11, 1), DeploymentID: cp.DeploymentID, FeedGeneration: 3, ConfigurationRevision: 1, Reason: string(events.CursorExpired), EffectiveReason: string(events.CursorExpired), Mode: events.RecoveryRetained, PriorCheckpoint: &cp, PriorNamespaceIDs: cp.NamespaceIDs, PriorCursor: cp.HeadCursor, Checkpoint: cp, RemovedNamespaceIDs: []string{}, AddedNamespaceIDs: []string{}, CreatedAt: fixtureTime()}
	p.Digest = p.Fingerprint()
	repo := &reconciliationRepository{candidates: []ReconciliationCandidate{{Actor: monitoring.Actor{Account: api.Account{ID: fixtureID(9, 1)}}, Rule: fixtureRule(t), Namespace: fixtureRef(1, 1)}}}
	source := &reconciliationSource{}
	source.discover = func(monitoring.Actor) (monitoring.Discovery, error) {
		return monitoring.Discovery{InstanceID: cp.ControlInstanceID, RecoveryEpoch: cp.RecoveryEpoch, PrincipalID: fixtureID(7, 1), Deployment: api.Deployment{ID: cp.DeploymentID, Namespaces: []api.Namespace{{ID: cp.NamespaceIDs[0], Capabilities: []string{"namespace.read", "jobs.read"}, AuthorizationExpiresAt: fixtureTime().Add(time.Hour)}}}}, nil
	}
	r, err := NewReconciler(repo, []monitoring.Source{source})
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return fixtureTime().Add(time.Minute) }
	return r, repo, source, p
}
func reconciliationJob(n int) api.Job {
	return api.Job{ID: fixtureID(6, n), Scope: api.Scope{DeploymentID: fixtureID(2, 1), NamespaceID: fixtureID(4, 1)}, Phase: "terminal", Outcome: "failure"}
}
func TestRecoveryReconciliationScopesAndSourceCutoff(t *testing.T) {
	for _, scope := range []string{ScopeNamespaceJobs, ScopeMyJobs, ScopeWatchedJobs} {
		t.Run(scope, func(t *testing.T) {
			r, repo, s, p := reconciliationSetup(t)
			repo.candidates[0].Rule.Scope = scope
			calls := 0
			if scope == ScopeWatchedJobs {
				repo.candidates[0].Rule.Jobs = []JobRef{{fixtureID(2, 1), fixtureID(4, 1), fixtureID(6, 1)}}
			}
			s.jobs = func(q monitoring.SourceQuery) (monitoring.JobPage, error) {
				calls++
				if q.NamespaceID != p.Checkpoint.NamespaceIDs[0] || q.Phase != "terminal" || q.Limit != 200 || !q.CreatedBefore.Equal(p.Checkpoint.AsOf) || (q.Owner == "me") != (scope == ScopeMyJobs) {
					t.Fatal("scan escaped explicit intent", q)
				}
				if calls == 1 {
					return monitoring.JobPage{Items: []api.Job{reconciliationJob(1)}, NextCursor: "next"}, nil
				}
				if q.Cursor != "next" {
					t.Fatal("cursor not continued")
				}
				return monitoring.JobPage{Items: []api.Job{reconciliationJob(2)}}, nil
			}
			s.job = func(q api.Scope, id string) (api.Job, error) {
				calls++
				if q.DeploymentID != p.DeploymentID || q.NamespaceID != p.Checkpoint.NamespaceIDs[0] || id != fixtureID(6, 1) {
					t.Fatal("watched scope escaped")
				}
				return reconciliationJob(1), nil
			}
			got, err := r.Reconcile(t.Context(), p)
			if err != nil {
				t.Fatal(err)
			}
			want := "partial"
			count := int64(2)
			if scope == ScopeNamespaceJobs {
				want = "complete"
			}
			if scope == ScopeWatchedJobs {
				count = 1
			}
			if got.Namespaces[0].Status != want || got.Namespaces[0].JobsRead != count || got.Validate(p) != nil {
				t.Fatal(got)
			}
		})
	}
}
func TestRecoveryReconciliationLateAuthorityClearsEveryExit(t *testing.T) {
	for _, exit := range []string{"exhausted", "budget", "failed-page"} {
		for _, failure := range []error{monitoring.ErrForbidden, monitoring.ErrAuthority, ErrConflict} {
			t.Run(fmt.Sprint(exit, failure), func(t *testing.T) {
				r, repo, s, p := reconciliationSetup(t)
				calls := 0
				lost := false
				repo.verify = func(ReconciliationCandidate) error {
					if lost {
						return failure
					}
					return nil
				}
				s.jobs = func(q monitoring.SourceQuery) (monitoring.JobPage, error) {
					calls++
					if exit == "failed-page" && calls == 2 {
						lost = true
						return monitoring.JobPage{}, errors.New("transport unavailable")
					}
					if exit == "exhausted" {
						lost = true
						return monitoring.JobPage{Items: []api.Job{reconciliationJob(1)}}, nil
					}
					if calls == 50 {
						lost = true
					}
					return monitoring.JobPage{Items: []api.Job{reconciliationJob(calls)}, NextCursor: fmt.Sprint(calls)}, nil
				}
				got, err := r.Reconcile(t.Context(), p)
				if err != nil {
					t.Fatal(err)
				}
				n := got.Namespaces[0]
				want := "unavailable"
				if errors.Is(failure, monitoring.ErrForbidden) {
					want = "inaccessible"
				}
				if n.Status != want || n.JobsRead != 0 || n.AsOf != nil {
					t.Fatal("stale coverage survived final authority", n)
				}
			})
		}
	}
}
func TestRecoveryReconciliationMixedDenialAndOutageIsUnavailable(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		r, repo, s, p := reconciliationSetup(t)
		other := repo.candidates[0]
		other.Actor.Account.ID = fixtureID(9, 2)
		repo.candidates = append(repo.candidates, other)
		s.discover = func(a monitoring.Actor) (monitoring.Discovery, error) {
			if (a.Account.ID == fixtureID(9, 1)) != reverse {
				return monitoring.Discovery{}, monitoring.ErrForbidden
			}
			return monitoring.Discovery{}, monitoring.ErrAuthority
		}
		got, err := r.Reconcile(t.Context(), p)
		if err != nil || got.Namespaces[0].Status != "unavailable" {
			t.Fatal("outage was represented as confirmed denial", got, err)
		}
	}
}

func TestRecoveryReconciliationPreservesActivePrincipalBinding(t *testing.T) {
	for _, phase := range []string{"before-scan", "after-scan"} {
		for _, principal := range []string{"", "invalid-principal", fixtureID(7, 2)} {
			t.Run(phase+"/"+principal, func(t *testing.T) {
				r, repo, source, plan := reconciliationSetup(t)
				repo.candidates[0].Rule.Scope = ScopeMyJobs
				original := source.discover
				calls := 0
				source.discover = func(a monitoring.Actor) (monitoring.Discovery, error) {
					d, err := original(a)
					if phase == "before-scan" || calls > 0 {
						d.PrincipalID = principal
					}
					return d, err
				}
				source.jobs = func(monitoring.SourceQuery) (monitoring.JobPage, error) {
					calls++
					return monitoring.JobPage{Items: []api.Job{reconciliationJob(1)}}, nil
				}
				receipt, err := r.Reconcile(t.Context(), plan)
				if err != nil {
					t.Fatal(err)
				}
				wantCalls := 0
				if phase == "after-scan" {
					wantCalls = 1
				}
				wantStatus := "unavailable"
				if uuid(principal) {
					wantStatus = "inaccessible"
				}
				got := receipt.Namespaces[0]
				if calls != wantCalls || got.Status != wantStatus || got.JobsRead != 0 || got.AsOf != nil {
					t.Fatal("old interval accepted missing/rebound principal", calls, got)
				}
			})
		}
	}
}

func TestRecoveryReconciliationPendingIntervalUsesCurrentPrincipal(t *testing.T) {
	r, repo, source, plan := reconciliationSetup(t)
	rule := &repo.candidates[0].Rule
	rule.Scope = ScopeMyJobs
	rule.Activation[0].Status = ActivationPending
	rule.Activation[0].Boundary = nil
	original := source.discover
	source.discover = func(a monitoring.Actor) (monitoring.Discovery, error) {
		d, err := original(a)
		d.PrincipalID = fixtureID(7, 2)
		return d, err
	}
	source.jobs = func(q monitoring.SourceQuery) (monitoring.JobPage, error) {
		if q.Owner != "me" {
			t.Fatal("pending observation escaped current owner")
		}
		return monitoring.JobPage{Items: []api.Job{reconciliationJob(1)}}, nil
	}
	receipt, err := r.Reconcile(t.Context(), plan)
	if err != nil || receipt.Namespaces[0].Status != "partial" || receipt.Namespaces[0].JobsRead != 1 {
		t.Fatal("pending interval incorrectly bound a historic principal", receipt, err)
	}
}
func TestRecoveryReconciliationLimitsAndInvalidPages(t *testing.T) {
	for _, mode := range []string{"page-budget", "duplicate", "wrong-source", "cyclic-cursor", "epoch"} {
		t.Run(mode, func(t *testing.T) {
			r, _, s, p := reconciliationSetup(t)
			calls := 0
			original := s.discover
			if mode == "epoch" {
				s.discover = func(a monitoring.Actor) (monitoring.Discovery, error) {
					d, e := original(a)
					d.RecoveryEpoch = "9007199254740994"
					return d, e
				}
			}
			s.jobs = func(q monitoring.SourceQuery) (monitoring.JobPage, error) {
				calls++
				j := reconciliationJob(calls)
				cursor := fmt.Sprint(calls)
				if mode == "duplicate" {
					return monitoring.JobPage{Items: []api.Job{j, j}}, nil
				}
				if mode == "wrong-source" {
					j.DeploymentID = fixtureID(2, 2)
				}
				if mode == "cyclic-cursor" {
					cursor = "same"
				}
				return monitoring.JobPage{Items: []api.Job{j}, NextCursor: cursor}, nil
			}
			got, err := r.Reconcile(t.Context(), p)
			if err != nil {
				t.Fatal(err)
			}
			n := got.Namespaces[0]
			if mode == "page-budget" && (calls != 50 || n.Status != "partial" || n.JobsRead != 50) {
				t.Fatal("unbounded or dishonest coverage", calls, n)
			}
			if (mode == "duplicate" || mode == "wrong-source" || mode == "epoch") && (n.Status != "unavailable" || n.JobsRead != 0) {
				t.Fatal("invalid facts accepted", n)
			}
			if mode == "epoch" && calls != 0 {
				t.Fatal("read changed source")
			}
			if mode == "cyclic-cursor" && calls != 2 {
				t.Fatal("cursor loop", calls)
			}
		})
	}
}
