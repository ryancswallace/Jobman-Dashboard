package notifications

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type evaluationMemory struct {
	feed                            events.Feed
	claims                          []EvaluationClaim
	decisions                       []EvaluationDecision
	deferred                        []string
	committed                       []string
	fanoutErr, appendErr, commitErr error
	advanced, releases, fanouts     int
	commitHook                      func(SourceFence)
}

func (m *evaluationMemory) NotificationFeed(context.Context, string) (events.Feed, error) {
	return m.feed, nil
}
func (m *evaluationMemory) ClaimNotificationFanout(_ context.Context, f SourceFence) (FanoutClaim, error) {
	if m.fanoutErr != nil {
		return FanoutClaim{}, m.fanoutErr
	}
	return FanoutClaim{Key: Key(fixtureEvent()), LeaseToken: fixtureID(50, 1), Revision: 1, HoldGeneration: 1, Fence: f}, nil
}
func (m *evaluationMemory) AppendNotificationFanout(context.Context, FanoutClaim) (FanoutProgress, error) {
	m.fanouts++
	return FanoutProgress{Done: true}, m.appendErr
}
func (m *evaluationMemory) ReleaseNotificationFanout(context.Context, FanoutClaim) error {
	m.releases++
	return nil
}
func (m *evaluationMemory) ClaimNotificationEvaluation(_ context.Context, f SourceFence) (EvaluationClaim, error) {
	if m.advanced > 0 {
		m.advanced--
		return EvaluationClaim{}, ErrEvaluationAdvanced
	}
	if len(m.claims) == 0 {
		return EvaluationClaim{}, ErrEvaluationEmpty
	}
	c := m.claims[0]
	m.claims = m.claims[1:]
	c.Fence = f
	return c, nil
}
func (m *evaluationMemory) CommitNotificationEvaluation(_ context.Context, c EvaluationClaim, f SourceFence, d EvaluationDecision) (EvaluationResult, error) {
	if m.commitHook != nil {
		m.commitHook(f)
	}
	if m.commitErr != nil {
		return EvaluationResult{}, m.commitErr
	}
	m.decisions = append(m.decisions, d)
	m.committed = append(m.committed, c.ID)
	return EvaluationResult{State: "complete"}, nil
}
func (m *evaluationMemory) DeferNotificationEvaluation(ctx context.Context, c EvaluationClaim) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.deferred = append(m.deferred, c.ID)
	return nil
}

func workerFixture(t *testing.T) (*Evaluator, *evaluationMemory, *ruleTestSource) {
	t.Helper()
	cp := fixtureCheckpoint()
	s := &ruleTestSource{checkpoint: cp, configured: slices.Clone(cp.NamespaceIDs), discovery: monitoring.Discovery{Deployment: api.Deployment{ID: cp.DeploymentID, Status: "available", Namespaces: []api.Namespace{{ID: cp.NamespaceIDs[0], AuthorizationVersion: "3", AuthorizationCheckedAt: fixtureTime(), AuthorizationExpiresAt: fixtureTime().Add(time.Minute), Capabilities: []string{"namespace.read", "jobs.read"}}}}, InstanceID: cp.ControlInstanceID, RecoveryEpoch: cp.RecoveryEpoch, PrincipalID: fixtureID(7, 1)}}
	c := EvaluationClaim{ID: fixtureID(40, 1), LeaseToken: fixtureID(41, 1), Revision: 1, HoldGeneration: 1, Actor: monitoring.Actor{Account: api.Account{ID: fixtureID(9, 1)}, Issuer: "https://identity.test", Subject: "alice", DirectoryID: fixtureID(10, 1)}, Event: fixtureEvent(), Attempts: 1}
	m := &evaluationMemory{feed: fixtureFeed(), claims: []EvaluationClaim{c}, fanoutErr: ErrEvaluationEmpty}
	w, err := NewEvaluator(m, m, []RuleSource{s}, []events.Source{s}, 2)
	if err != nil {
		t.Fatal(err)
	}
	w.now = fixtureTime
	return w, m, s
}

func TestEvaluatorAuthorizesOriginalEventWithCurrentProof(t *testing.T) {
	w, m, s := workerFixture(t)
	m.commitHook = func(f SourceFence) {
		if f.ConfigurationRevision != 2 || f.Checkpoint.ControlInstanceID != s.checkpoint.ControlInstanceID {
			t.Fatal("wrongsource fence")
		}
	}
	progress, err := w.Step(context.Background(), s.ID())
	if err != nil || !progress || len(m.decisions) != 1 || len(m.deferred) != 0 || s.jobReads != 1 || s.discoverReads != 2 {
		t.Fatalf("progress=%v err=%v decisions=%v deferred=%v reads=%d/%d", progress, err, m.decisions, m.deferred, s.jobReads, s.discoverReads)
	}
	p := m.decisions[0].Proof
	if m.decisions[0].Outcome != EvaluationAuthorized || p.PrincipalID != s.discovery.PrincipalID || p.CheckedAt != fixtureTime() || p.ExpiresAt != fixtureTime().Add(time.Minute) || p.RecoveryEpoch != s.checkpoint.RecoveryEpoch {
		t.Fatal("source proof not preserved", p)
	}
}

func TestEvaluatorDistinguishesResourceLossNamespaceLossAndOutage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(*Evaluator, *evaluationMemory, *ruleTestSource)
		outcome   string
		deferWork bool
	}{
		{"deleted_job", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) { s.jobError = monitoring.ErrNotFound }, EvaluationResourceInaccessible, false},
		{"job_forbidden_namespace_still_readable", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) { s.jobError = monitoring.ErrForbidden }, EvaluationResourceInaccessible, false},
		{"namespace_removed", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) { s.discovery.Deployment.Namespaces = nil }, EvaluationInaccessible, false},
		{"capability_removed", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) {
			s.discovery.Deployment.Namespaces[0].Capabilities = []string{"namespace.read"}
		}, EvaluationInaccessible, false},
		{"stale_evidence_and_removed_capability", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) {
			s.discovery.Deployment.Namespaces[0].Capabilities = nil
			s.discovery.Deployment.Namespaces[0].AuthorizationExpiresAt = fixtureTime()
		}, "", true},
		{"directory_outage", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) { s.discoverError = monitoring.ErrAuthority }, "", true},
		{"discovery_missing_route", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) { s.discoverError = monitoring.ErrNotFound }, "", true},
		{"discovery_service_forbidden", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) { s.discoverError = monitoring.ErrForbidden }, "", true},
		{"job_outage", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) { s.jobError = monitoring.ErrSource }, "", true},
		{"missing_principal", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) { s.discovery.PrincipalID = "" }, "", true},
		{"late_directory_outage", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) {
			s.discoverHook = func() {
				if s.discoverReads == 2 {
					s.discoverError = monitoring.ErrAuthority
				}
			}
		}, "", true},
		{"late_namespace_loss", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) {
			s.discoverHook = func() {
				if s.discoverReads == 2 {
					s.discovery.Deployment.Namespaces = nil
				}
			}
		}, EvaluationInaccessible, false},
		{"late_version_change", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) {
			s.discoverHook = func() {
				if s.discoverReads == 2 {
					s.discovery.Deployment.Namespaces[0].AuthorizationVersion = "4"
				}
			}
		}, "", true},
		{"late_principal_change", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) {
			s.discoverHook = func() {
				if s.discoverReads == 2 {
					s.discovery.PrincipalID = fixtureID(7, 2)
				}
			}
		}, "", true},
		{"epoch_changes_during_job", func(_ *Evaluator, _ *evaluationMemory, s *ruleTestSource) {
			s.discoverHook = func() {
				if s.discoverReads == 2 {
					s.discovery.RecoveryEpoch = "2"
				}
			}
		}, "", true},
		{"source_changes_before_commit", func(_ *Evaluator, m *evaluationMemory, s *ruleTestSource) {
			s.checkpointHook = func() {
				if s.checkpointReads == 3 {
					s.checkpoint.RecoveryEpoch = "2"
					m.feed.Checkpoint.RecoveryEpoch = "2"
				}
			}
		}, "", true},
		{"commit_lease_lost", func(_ *Evaluator, m *evaluationMemory, _ *ruleTestSource) { m.commitErr = ErrEvaluationLease }, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, m, s := workerFixture(t)
			tc.change(w, m, s)
			_, _ = w.Step(context.Background(), s.ID())
			if tc.deferWork {
				if len(m.decisions) != 0 || len(m.deferred) != 1 {
					t.Fatalf("published or abandoned unavailable work: decisions=%v deferred=%v", m.decisions, m.deferred)
				}
			} else if len(m.decisions) != 1 || m.decisions[0].Outcome != tc.outcome || len(m.deferred) != 0 {
				t.Fatalf("wrong access decision: %v %v", m.decisions, m.deferred)
			}
		})
	}
}

func TestEvaluatorIndependentAccountsAndCapacityDrain(t *testing.T) {
	w, m, s := workerFixture(t)
	second := m.claims[0]
	second.ID = fixtureID(40, 2)
	second.Actor.Account.ID = fixtureID(9, 2)
	m.claims = append(m.claims, second)
	m.fanoutErr = nil
	m.appendErr = ErrEvaluationCapacity
	s.discoverHook = func() {
		if s.discoverReads == 1 {
			s.discoverError = monitoring.ErrAuthority
		} else {
			s.discoverError = nil
		}
	}
	progress, _ := w.Step(context.Background(), s.ID())
	if !progress || m.fanouts != 1 || m.releases != 1 || len(m.deferred) != 1 || len(m.committed) != 1 || m.committed[0] != second.ID {
		t.Fatalf("one unavailable account/capacity stalled others: %+v", m)
	}
}

func TestEvaluatorBoundsLocalProgressAndFencesBeforeClaim(t *testing.T) {
	w, m, s := workerFixture(t)
	m.advanced = 100
	progress, err := w.Step(context.Background(), s.ID())
	if !progress || err != nil || m.advanced != 84 || len(m.decisions) != 0 {
		t.Fatal("unbounded local drain", m.advanced, err)
	}
	for _, mutate := range []func(*evaluationMemory, *ruleTestSource){func(m *evaluationMemory, _ *ruleTestSource) { m.feed.Checkpoint.RecoveryEpoch = "2" }, func(_ *evaluationMemory, s *ruleTestSource) { s.checkpoint.NamespaceIDs = []string{fixtureID(4, 2)} }, func(m *evaluationMemory, _ *ruleTestSource) { m.feed.Generation = 0 }} {
		w, m, s = workerFixture(t)
		mutate(m, s)
		progress, err = w.Step(context.Background(), s.ID())
		if progress || !errors.Is(err, ErrEvaluationSource) || len(m.claims) != 1 || s.discoverReads != 0 {
			t.Fatal("unfenced work claimed", progress, err)
		}
	}
}

func TestEvaluatorCancellationDefersClaimWithoutKeepingShutdownAlive(t *testing.T) {
	w, m, s := workerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	s.discoverHook = func() { cancel(); s.discoverError = context.Canceled }
	_, err := w.Step(ctx, s.ID())
	if !errors.Is(err, context.Canceled) || len(m.deferred) != 1 || len(m.decisions) != 0 {
		t.Fatal("cancelled claim not returned", err, m.deferred)
	}
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workers did not join")
	}
}
