package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type memoryRules struct {
	ownerDenied bool
	mu          sync.Mutex
	rules       map[string]Rule
	revoked     map[string]bool
	feeds       map[string]events.Feed
	feedReads   int
	capacity    bool
	feedHook    func()
}

func (m *memoryRules) CreateNotificationRule(_ context.Context, a monitoring.Actor, in RuleInput, activation []Activation) (Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.capacity {
		return Rule{}, ErrCapacity
	}
	r := Rule{ID: fixtureID(1, len(m.rules)+1), AccountID: a.Account.ID, Revision: 1, RuleInput: in, Activation: activation, CreatedAt: fixtureTime(), UpdatedAt: fixtureTime()}
	if err := r.Validate(); err != nil {
		return Rule{}, err
	}
	m.rules[r.ID] = cloneRule(r)
	return cloneRule(r), nil
}
func (m *memoryRules) NotificationRule(_ context.Context, a monitoring.Actor, id string) (Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ownerDenied {
		return Rule{}, monitoring.ErrForbidden
	}
	r, ok := m.rules[id]
	if !ok || r.AccountID != a.Account.ID || r.DeletedAt != nil {
		return Rule{}, ErrNotFound
	}
	return cloneRule(r), nil
}
func (m *memoryRules) ListNotificationRules(_ context.Context, a monitoring.Actor) ([]Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []Rule{}
	for _, r := range m.rules {
		if r.AccountID == a.Account.ID && r.DeletedAt == nil {
			result = append(result, cloneRule(r))
		}
	}
	return result, nil
}
func (m *memoryRules) UpdateNotificationRule(ctx context.Context, a monitoring.Actor, id string, rev int64, in RuleInput, activation []Activation) (Rule, error) {
	previous, err := m.NotificationRule(ctx, a, id)
	if err != nil {
		return Rule{}, err
	}
	if previous.Revision != rev {
		return Rule{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	r := previous
	r.RuleInput = in
	r.Activation = activation
	if equalJSON(previous, r) {
		return r, nil
	}
	if m.capacity && (!previous.Enabled || in.Enabled) {
		return Rule{}, ErrCapacity
	}
	r.Revision++
	r.UpdatedAt = r.UpdatedAt.Add(time.Second)
	if err := ValidateTransition(previous, r); err != nil {
		return Rule{}, err
	}
	m.rules[id] = cloneRule(r)
	return cloneRule(r), nil
}
func (m *memoryRules) TransitionNotificationRule(ctx context.Context, a monitoring.Actor, id string, rev int64, activation []Activation) (Rule, error) {
	r, err := m.NotificationRule(ctx, a, id)
	if err != nil {
		return Rule{}, err
	}
	return m.UpdateNotificationRule(ctx, a, id, rev, r.RuleInput, activation)
}
func (m *memoryRules) DeleteNotificationRule(ctx context.Context, a monitoring.Actor, id string, rev int64) (Rule, error) {
	r, err := m.NotificationRule(ctx, a, id)
	if err != nil {
		return Rule{}, err
	}
	if r.Revision != rev {
		return Rule{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rules, id)
	return r, nil
}
func (m *memoryRules) NotificationActivationRevocations(ctx context.Context, a monitoring.Actor, id string, rev int64) ([]string, error) {
	r, err := m.NotificationRule(ctx, a, id)
	if err != nil {
		return nil, err
	}
	if r.Revision != rev {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []string{}
	for _, v := range r.Activation {
		if m.revoked[v.ID] {
			result = append(result, v.ID)
		}
	}
	return result, nil
}
func (m *memoryRules) DisableNotificationActivations(ctx context.Context, a monitoring.Actor, id string, rev int64, ids []string) error {
	r, err := m.NotificationRule(ctx, a, id)
	if err != nil {
		return err
	}
	if r.Revision != rev {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		m.revoked[id] = true
	}
	return nil
}
func (m *memoryRules) NotificationFeed(_ context.Context, id string) (events.Feed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.feedReads++
	if m.feedHook != nil {
		m.feedHook()
	}
	v, ok := m.feeds[id]
	if !ok {
		return events.Feed{}, ErrInactiveFeed
	}
	return v, nil
}

type ruleTestSource struct {
	discoverHook                             func()
	mu                                       sync.Mutex
	discovery                                monitoring.Discovery
	checkpoint                               events.Checkpoint
	configured                               []string
	discoverError, errorCheckpoint, jobError error
	jobReads, discoverReads, checkpointReads int
	checkpointHook                           func()
}

func (s *ruleTestSource) ID() string             { return s.checkpoint.DeploymentID }
func (s *ruleTestSource) SourceID() string       { return s.ID() }
func (s *ruleTestSource) NamespaceIDs() []string { return slices.Clone(s.configured) }
func (s *ruleTestSource) Discover(context.Context, monitoring.Actor) (monitoring.Discovery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discoverReads++
	if s.discoverHook != nil {
		s.discoverHook()
	}
	d := s.discovery
	d.Deployment.Namespaces = slices.Clone(d.Deployment.Namespaces)
	return d, s.discoverError
}
func (s *ruleTestSource) Checkpoint(context.Context) (events.Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkpointReads++
	if s.checkpointHook != nil {
		s.checkpointHook()
	}
	return s.checkpoint, s.errorCheckpoint
}
func (s *ruleTestSource) Read(context.Context, string, int) (events.Page, error) {
	return events.Page{}, errors.New("not used for rule setup")
}
func (s *ruleTestSource) Job(_ context.Context, _ monitoring.Actor, scope api.Scope, id string) (api.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobReads++
	return api.Job{Scope: scope, ID: id}, s.jobError
}
func serviceFixture(t *testing.T, count int) (*RuleService, *memoryRules, []*ruleTestSource, monitoring.Actor) {
	t.Helper()
	repo := &memoryRules{rules: map[string]Rule{}, revoked: map[string]bool{}, feeds: map[string]events.Feed{}}
	sources := []RuleSource{}
	feeds := []events.Source{}
	result := []*ruleTestSource{}
	for i := 1; i <= count; i++ {
		c := fixtureCheckpoint()
		c.DeploymentID = fixtureID(2, i)
		c.ControlInstanceID = fixtureID(3, i)
		d := monitoring.Discovery{Deployment: api.Deployment{ID: c.DeploymentID, Namespaces: []api.Namespace{{ID: fixtureID(4, 1), AuthorizationVersion: "1", AuthorizationExpiresAt: fixtureTime().Add(time.Minute), Capabilities: []string{"namespace.read", "jobs.read"}}}}, InstanceID: c.ControlInstanceID, RecoveryEpoch: c.RecoveryEpoch, PrincipalID: fixtureID(7, i)}
		source := &ruleTestSource{checkpoint: c, discovery: d, configured: slices.Clone(c.NamespaceIDs)}
		sources = append(sources, source)
		feeds = append(feeds, source)
		result = append(result, source)
		repo.feeds[c.DeploymentID] = events.Feed{DeploymentID: c.DeploymentID, NamespaceIDs: slices.Clone(c.NamespaceIDs), Checkpoint: c, Cursor: c.HeadCursor, Status: "active", Generation: 1}
	}
	cursors := monitoring.NewMemoryCursors()
	cursors.Now = fixtureTime
	service, err := NewRuleService(repo, sources, feeds, cursors)
	if err != nil {
		t.Fatal(err)
	}
	service.now = fixtureTime
	return service, repo, result, monitoring.Actor{Account: api.Account{ID: fixtureID(9, 1)}}
}
func TestRuleServiceCapturesSourceOwnerAndHidesPrivateBoundary(t *testing.T) {
	s, repo, sources, a := serviceFixture(t, 2)
	in := fixtureInput()
	in.Scope = ScopeMyJobs
	in.Namespaces = append(in.Namespaces, fixtureRef(2, 1))
	view, err := s.Create(context.Background(), a, in)
	if err != nil || len(view.Scopes) != 2 {
		t.Fatal(view, err)
	}
	r := repo.rules[view.ID]
	for i, v := range r.Activation {
		if v.Boundary.PrincipalID != sources[i].discovery.PrincipalID || view.Scopes[i].Status != ActivationActive {
			t.Fatal("canonical source identity lost")
		}
	}
	encoded, _ := json.Marshal(view)
	for _, secret := range []string{"headCursor", "principalId", "activationId", "accountId", r.Activation[0].ID, r.Activation[0].Boundary.HeadCursor} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("private provenance exposed", secret)
		}
	}
}
func TestRuleServicePendingRequiresVerifiedAccessAndCommittedFeed(t *testing.T) {
	for _, mode := range []string{"uninitialized", "paused", "checkpoint-outage", "directory-outage", "forbidden", "watched-job-denied", "grant-changed"} {
		t.Run(mode, func(t *testing.T) {
			s, repo, sources, a := serviceFixture(t, 1)
			in := fixtureInput()
			source := sources[0]
			wantPending := true
			switch mode {
			case "uninitialized":
				delete(repo.feeds, source.ID())
			case "paused":
				f := repo.feeds[source.ID()]
				f.Status = "paused"
				repo.feeds[source.ID()] = f
			case "checkpoint-outage":
				source.errorCheckpoint = events.ErrUnavailable
			case "directory-outage":
				source.discoverError = monitoring.ErrAuthority
				wantPending = false
			case "forbidden":
				source.discovery.Deployment.Namespaces = nil
				wantPending = false
			case "watched-job-denied":
				in.Scope = ScopeWatchedJobs
				in.Jobs = []JobRef{{fixtureID(2, 1), fixtureID(4, 1), fixtureID(6, 1)}}
				source.jobError = monitoring.ErrNotFound
				wantPending = false
			case "grant-changed":
				source.checkpointHook = func() { source.discovery.Deployment.Namespaces[0].AuthorizationVersion = "2" }
				wantPending = false
			}
			view, err := s.Create(context.Background(), a, in)
			if wantPending {
				if err != nil || view.Scopes[0].Status != ActivationPending {
					t.Fatal(view, err)
				}
			} else if err == nil || len(repo.rules) != 0 {
				t.Fatal("unverified rule persisted", view, err)
			}
		})
	}
}
func TestRuleServiceConfirmedDenialCannotResurrectAtQuota(t *testing.T) {
	s, repo, sources, a := serviceFixture(t, 1)
	ctx := context.Background()
	v, err := s.Create(ctx, a, fixtureInput())
	if err != nil {
		t.Fatal(err)
	}
	old := repo.rules[v.ID].Activation[0].ID
	grants := sources[0].discovery.Deployment.Namespaces
	sources[0].discovery.Deployment.Namespaces = nil
	repo.capacity = true
	denied, err := s.Get(ctx, a, v.ID)
	if err != nil || denied.InaccessibleScopes != 1 || len(denied.Scopes) != 0 || !repo.revoked[old] {
		t.Fatal("denial did not persist", denied, err)
	}
	sources[0].discovery.Deployment.Namespaces = grants
	restored, err := s.Get(ctx, a, v.ID)
	if err != nil || restored.Scopes[0].Status != ActivationInaccessible {
		t.Fatal("grant silently resurrected", restored, err)
	}
	automatic, err := s.Revalidate(ctx, a, v.ID, 1, false)
	if err != nil || automatic.Scopes[0].Status != ActivationInaccessible {
		t.Fatal("automatic reactivation", automatic, err)
	}
	_, err = s.Revalidate(ctx, a, v.ID, 1, true)
	if !errors.Is(err, ErrCapacity) || repo.rules[v.ID].Activation[0].ID != old {
		t.Fatal("quota bypass", err)
	}
	repo.capacity = false
	active, err := s.Revalidate(ctx, a, v.ID, 1, true)
	if err != nil || active.Scopes[0].Status != ActivationActive || repo.rules[v.ID].Activation[0].ID == old {
		t.Fatal("explicit fresh activation failed", active, err)
	}
}
func TestRuleServiceSourceOutageRedactsWithoutRevokingAndStopWorks(t *testing.T) {
	s, repo, sources, a := serviceFixture(t, 1)
	ctx := context.Background()
	in := fixtureInput()
	in.Scope = ScopeWatchedJobs
	in.Jobs = []JobRef{{fixtureID(2, 1), fixtureID(4, 1), fixtureID(6, 1)}}
	v, err := s.Create(ctx, a, in)
	if err != nil {
		t.Fatal(err)
	}
	old := repo.rules[v.ID].Activation[0].ID
	sources[0].discoverError = monitoring.ErrSource
	repo.capacity = true
	hidden, err := s.Get(ctx, a, v.ID)
	if err != nil || hidden.UnavailableScopes != 1 || len(hidden.Jobs) != 0 || len(hidden.Scopes) != 0 || repo.revoked[old] {
		t.Fatal(hidden, err)
	}
	stopped, err := s.SetEnabled(ctx, a, v.ID, 1, false)
	if err != nil || stopped.Enabled || repo.rules[v.ID].Enabled {
		t.Fatal("outage blocked stop", stopped, err)
	}
	if err := s.Delete(ctx, a, v.ID, 2); err != nil {
		t.Fatal("outage blocked removal", err)
	}
}
func TestRuleServiceRevalidationKeepsHealthySourceIndependent(t *testing.T) {
	s, repo, sources, a := serviceFixture(t, 2)
	ctx := context.Background()
	in := fixtureInput()
	in.Namespaces = append(in.Namespaces, fixtureRef(2, 1))
	for _, source := range sources {
		source.errorCheckpoint = events.ErrUnavailable
	}
	v, err := s.Create(ctx, a, in)
	if err != nil {
		t.Fatal(err)
	}
	old := repo.rules[v.ID].Activation[0].ID
	sources[0].discoverError = monitoring.ErrAuthority
	sources[1].errorCheckpoint = nil
	active, err := s.Revalidate(ctx, a, v.ID, 1, false)
	if err != nil || active.UnavailableScopes != 1 || len(active.Scopes) != 1 || active.Scopes[0].DeploymentID != fixtureID(2, 2) || active.Scopes[0].Status != ActivationActive || repo.rules[v.ID].Activation[0].ID != old {
		t.Fatal(active, err)
	}
}
func TestRuleServiceConfiguredScopeRemovalRequiresFreshInterval(t *testing.T) {
	s, repo, sources, a := serviceFixture(t, 1)
	ctx := context.Background()
	v, err := s.Create(ctx, a, fixtureInput())
	if err != nil {
		t.Fatal(err)
	}
	old := repo.rules[v.ID].Activation[0].ID
	sources[0].configured = []string{fixtureID(4, 2)}
	removed, err := s.Get(ctx, a, v.ID)
	if err != nil || removed.Scopes[0].Status != ActivationInaccessible || !repo.revoked[old] {
		t.Fatal(removed, err)
	}
	sources[0].configured = []string{fixtureID(4, 1)}
	restored, err := s.Get(ctx, a, v.ID)
	if err != nil || restored.Scopes[0].Status != ActivationInaccessible {
		t.Fatal("scope re-add resurrected old boundary", restored, err)
	}
}
func TestRuleServiceFeedReadsAreBoundedPerList(t *testing.T) {
	s, repo, _, a := serviceFixture(t, 1)
	ctx := context.Background()
	for i := 1; i <= MaximumRulesPerAccount; i++ {
		r := fixtureRule(t)
		r.ID = fixtureID(1, i)
		repo.rules[r.ID] = r
	}
	page, err := s.List(ctx, a, "", 20)
	if err != nil || len(page.Items) != 20 || page.NextCursor == "" || repo.feedReads != 1 {
		t.Fatal("unbounded per-rule feed reads", len(page.Items), repo.feedReads, err)
	}
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, err = s.List(ctx, a, cursor, 20)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, v := range page.Items {
			if seen[v.ID] {
				t.Fatal("repeated rule")
			}
			seen[v.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != MaximumRulesPerAccount || pages != 5 {
		t.Fatal("rule traversal incomplete", len(seen), pages)
	}
	other := a
	other.Account.ID = fixtureID(9, 2)
	if _, err = s.List(ctx, other, cursor, 20); !errors.Is(err, monitoring.ErrCursor) {
		t.Fatal("cursor crossed account", err)
	}
	if _, err = s.List(ctx, a, cursor, 10); !errors.Is(err, monitoring.ErrCursor) {
		t.Fatal("cursor changed page size", err)
	}
}
func TestRuleServiceNameEditPreservesIntervalAndStaleRevisionFails(t *testing.T) {
	s, repo, _, a := serviceFixture(t, 1)
	ctx := context.Background()
	in := fixtureInput()
	v, err := s.Create(ctx, a, in)
	if err != nil {
		t.Fatal(err)
	}
	old := repo.rules[v.ID].Activation[0].ID
	in.Name = "Personal failures"
	v, err = s.Update(ctx, a, v.ID, 1, in)
	if err != nil || v.Revision != "2" || repo.rules[v.ID].Activation[0].ID != old {
		t.Fatal(v, err)
	}
	if _, err = s.SetEnabled(ctx, a, v.ID, 1, false); !errors.Is(err, ErrConflict) {
		t.Fatal("stale stop accepted", err)
	}
	in.Outcomes = []string{"success"}
	v, err = s.Update(ctx, a, v.ID, 2, in)
	if err != nil || repo.rules[v.ID].Activation[0].ID == old {
		t.Fatal("outcome edit reused boundary", v, err)
	}
}

func TestRuleServiceFinalAuthorityFenceAfterPersistenceReads(t *testing.T) {
	for _, mode := range []string{"lost", "changed", "outage"} {
		t.Run(mode, func(t *testing.T) {
			s, repo, sources, a := serviceFixture(t, 1)
			ctx := context.Background()
			v, err := s.Create(ctx, a, fixtureInput())
			if err != nil {
				t.Fatal(err)
			}
			old := repo.rules[v.ID].Activation[0].ID
			repo.feedHook = func() {
				source := sources[0]
				source.mu.Lock()
				defer source.mu.Unlock()
				switch mode {
				case "lost":
					source.discovery.Deployment.Namespaces = nil
				case "changed":
					source.discovery.Deployment.Namespaces[0].AuthorizationVersion = "2"
				case "outage":
					source.discoverError = monitoring.ErrAuthority
				}
			}
			result, err := s.Get(ctx, a, v.ID)
			if err != nil || len(result.Scopes) != 0 || len(result.Jobs) != 0 {
				t.Fatal("stale references emitted", result, err)
			}
			if mode == "lost" {
				if result.InaccessibleScopes != 1 || !repo.revoked[old] {
					t.Fatal("late denial not persisted", result)
				}
			} else if result.UnavailableScopes != 1 || repo.revoked[old] {
				t.Fatal("uncertainty treated as confirmed loss", result)
			}
		})
	}
}
func TestRuleServicePendingNamespacesCaptureOneBoundaryPerSource(t *testing.T) {
	s, repo, sources, a := serviceFixture(t, 1)
	source := sources[0]
	in := fixtureInput()
	in.Namespaces = []NamespaceRef{}
	source.configured = []string{}
	source.discovery.Deployment.Namespaces = []api.Namespace{}
	for i := 1; i <= MaximumNamespaces; i++ {
		ref := fixtureRef(1, i)
		in.Namespaces = append(in.Namespaces, ref)
		source.configured = append(source.configured, ref.NamespaceID)
		source.discovery.Deployment.Namespaces = append(source.discovery.Deployment.Namespaces, api.Namespace{ID: ref.NamespaceID, AuthorizationVersion: "1", AuthorizationExpiresAt: fixtureTime().Add(time.Minute), Capabilities: []string{"namespace.read", "jobs.read"}})
	}
	source.checkpoint.NamespaceIDs = slices.Clone(source.configured)
	feed := repo.feeds[source.ID()]
	feed.NamespaceIDs = slices.Clone(source.configured)
	feed.Checkpoint = source.checkpoint
	repo.feeds[source.ID()] = feed
	source.errorCheckpoint = events.ErrUnavailable
	view, err := s.Create(context.Background(), a, in)
	if err != nil {
		t.Fatal(err)
	}
	source.errorCheckpoint = nil
	source.checkpointReads = 0
	source.discoverReads = 0
	repo.feedReads = 0
	view, err = s.Revalidate(context.Background(), a, view.ID, 1, false)
	if err != nil || len(view.Scopes) != MaximumNamespaces || source.checkpointReads != 1 || source.discoverReads > 5 || repo.feedReads > 2 {
		t.Fatal("per-namespace network explosion", len(view.Scopes), source.checkpointReads, source.discoverReads, repo.feedReads, err)
	}
	for _, scope := range view.Scopes {
		if scope.Status != ActivationActive {
			t.Fatal("pending scope did not activate")
		}
	}
}

func TestRuleServiceRechecksLocalOwnerAfterFinalSourceCall(t *testing.T) {
	s, repo, sources, a := serviceFixture(t, 1)
	ctx := context.Background()
	v, err := s.Create(ctx, a, fixtureInput())
	if err != nil {
		t.Fatal(err)
	}
	source := sources[0]
	source.discoverReads = 0
	source.discoverHook = func() {
		if source.discoverReads == 2 {
			repo.mu.Lock()
			repo.ownerDenied = true
			repo.mu.Unlock()
		}
	}
	if _, err := s.Get(ctx, a, v.ID); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("local account or alias revocation during source request was ignored", err)
	}
}

func TestRuleServiceCanonicalOwnerChangeRequiresExplicitRevalidation(t *testing.T) {
	s, repo, sources, a := serviceFixture(t, 1)
	ctx := context.Background()
	input := fixtureInput()
	input.Scope = ScopeMyJobs
	view, err := s.Create(ctx, a, input)
	if err != nil {
		t.Fatal(err)
	}
	old := repo.rules[view.ID].Activation[0].ID
	sources[0].discovery.PrincipalID = fixtureID(7, 99)
	view, err = s.Get(ctx, a, view.ID)
	if err != nil || view.Scopes[0].Status != ActivationInaccessible || !repo.revoked[old] {
		t.Fatal("new canonical owner inherited old subscription", view, err)
	}
	view, err = s.Revalidate(ctx, a, view.ID, 1, true)
	if err != nil || view.Scopes[0].Status != ActivationActive || repo.rules[view.ID].Activation[0].Boundary.PrincipalID != fixtureID(7, 99) {
		t.Fatal("explicit owner revalidation failed", view, err)
	}
}
