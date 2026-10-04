package notifications

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

// RuleRepository returns private models. No model may be serialized directly to
// a client; Views filter source references using current represented authority.
type RuleRepository interface {
	CreateNotificationRule(context.Context, monitoring.Actor, RuleInput, []Activation) (Rule, error)
	UpdateNotificationRule(context.Context, monitoring.Actor, string, int64, RuleInput, []Activation) (Rule, error)
	TransitionNotificationRule(context.Context, monitoring.Actor, string, int64, []Activation) (Rule, error)
	DeleteNotificationRule(context.Context, monitoring.Actor, string, int64) (Rule, error)
	NotificationRule(context.Context, monitoring.Actor, string) (Rule, error)
	ListNotificationRules(context.Context, monitoring.Actor) ([]Rule, error)
	NotificationActivationRevocations(context.Context, monitoring.Actor, string, int64) ([]string, error)
	DisableNotificationActivations(context.Context, monitoring.Actor, string, int64, []string) error
	NotificationFeed(context.Context, string) (events.Feed, error)
}

type RuleSource interface {
	ID() string
	Discover(context.Context, monitoring.Actor) (monitoring.Discovery, error)
	Job(context.Context, monitoring.Actor, api.Scope, string) (api.Job, error)
}

type RuleService struct {
	repository RuleRepository
	cursors    monitoring.CursorStore
	sources    map[string]RuleSource
	feeds      map[string]events.Source
	slots      chan struct{}
	now        func() time.Time
}

func NewRuleService(repository RuleRepository, sources []RuleSource, feeds []events.Source, cursors monitoring.CursorStore) (*RuleService, error) {
	if repository == nil || cursors == nil || len(sources) < 1 || len(sources) > MaximumSources || len(feeds) > MaximumSources {
		return nil, ErrInvalid
	}
	s := &RuleService{repository: repository, cursors: cursors, sources: map[string]RuleSource{}, feeds: map[string]events.Source{}, slots: make(chan struct{}, 4), now: time.Now}
	for _, source := range sources {
		if source == nil || !uuid(source.ID()) || s.sources[source.ID()] != nil {
			return nil, ErrInvalid
		}
		s.sources[source.ID()] = source
	}
	for _, feed := range feeds {
		if feed == nil || s.sources[feed.SourceID()] == nil || s.feeds[feed.SourceID()] != nil {
			return nil, ErrInvalid
		}
		s.feeds[feed.SourceID()] = feed
	}
	return s, nil
}

type ruleAccess struct {
	discovery monitoring.Discovery
	err       error
}

func (s *RuleService) network(ctx context.Context, action func() error) error {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
		return action()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// parallel starts at most four short-lived workers, never a goroutine per job.
func parallel(n int, action func(int)) {
	var wg sync.WaitGroup
	work := make(chan int)
	for i := 0; i < min(n, 4); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				action(j)
			}
		}()
	}
	for i := 0; i < n; i++ {
		work <- i
	}
	close(work)
	wg.Wait()
}
func (s *RuleService) discover(ctx context.Context, actor monitoring.Actor, refs []NamespaceRef) map[string]ruleAccess {
	ids := []string{}
	for _, ref := range refs {
		if !slices.Contains(ids, ref.DeploymentID) {
			ids = append(ids, ref.DeploymentID)
		}
	}
	results := make([]ruleAccess, len(ids))
	parallel(len(ids), func(i int) {
		source := s.sources[ids[i]]
		if source == nil {
			results[i].err = monitoring.ErrForbidden
			return
		}
		results[i].err = s.network(ctx, func() error { var err error; results[i].discovery, err = source.Discover(ctx, actor); return err })
		d := results[i].discovery
		if results[i].err == nil && (d.Deployment.ID != ids[i] || !uuid(d.InstanceID) || !positiveDecimal(d.RecoveryEpoch) || len(d.Deployment.Namespaces) > MaximumNamespaces) {
			results[i].err = monitoring.ErrAuthority
		}
	})
	result := map[string]ruleAccess{}
	for i, id := range ids {
		result[id] = results[i]
	}
	return result
}
func (s *RuleService) namespace(access ruleAccess, ref NamespaceRef) (api.Namespace, error) {
	if access.err != nil {
		return api.Namespace{}, access.err
	}
	for _, ns := range access.discovery.Deployment.Namespaces {
		if ns.ID == ref.NamespaceID {
			if !ns.AuthorizationExpiresAt.After(s.now()) {
				return api.Namespace{}, monitoring.ErrAuthority
			}
			if !slices.Contains(ns.Capabilities, "namespace.read") || !slices.Contains(ns.Capabilities, "jobs.read") {
				return api.Namespace{}, monitoring.ErrForbidden
			}
			return ns, nil
		}
	}
	return api.Namespace{}, monitoring.ErrForbidden
}
func denied(err error) bool {
	return errors.Is(err, monitoring.ErrForbidden) || errors.Is(err, monitoring.ErrNotFound)
}
func newActivationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&15 | 64
	value[8] = value[8]&63 | 128
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

// prepare verifies every selected namespace and watched job before accepting
// intent. A feed outage after successful current authorization yields pending
// monitoring, never an inferred activation covering the outage interval.
func (s *RuleService) prepare(ctx context.Context, actor monitoring.Actor, input RuleInput) ([]Activation, error) {
	before := s.discover(ctx, actor, input.Namespaces)
	for _, ref := range input.Namespaces {
		if feed := s.feeds[ref.DeploymentID]; feed != nil && !slices.Contains(feed.NamespaceIDs(), ref.NamespaceID) {
			return nil, monitoring.ErrForbidden
		}
		if _, err := s.namespace(before[ref.DeploymentID], ref); err != nil {
			return nil, err
		}
	}
	jobErrors := make([]error, len(input.Jobs))
	parallel(len(input.Jobs), func(i int) {
		ref := input.Jobs[i]
		jobErrors[i] = s.network(ctx, func() error {
			job, err := s.sources[ref.DeploymentID].Job(ctx, actor, api.Scope{DeploymentID: ref.DeploymentID, NamespaceID: ref.NamespaceID}, ref.JobID)
			if err != nil {
				return err
			}
			if job.DeploymentID != ref.DeploymentID || job.NamespaceID != ref.NamespaceID || job.ID != ref.JobID {
				return monitoring.ErrSource
			}
			return nil
		})
	})
	for _, err := range jobErrors {
		if err != nil {
			return nil, err
		}
	}
	type boundary struct {
		feed       events.Feed
		checkpoint events.Checkpoint
		err        error
	}
	captures := map[string]*boundary{}
	ids := []string{}
	for _, ref := range input.Namespaces {
		if captures[ref.DeploymentID] == nil {
			captures[ref.DeploymentID] = &boundary{}
			ids = append(ids, ref.DeploymentID)
		}
	}
	if input.Enabled {
		parallel(len(ids), func(i int) {
			id := ids[i]
			b := captures[id]
			feedSource := s.feeds[id]
			if feedSource == nil {
				b.err = ErrInactiveFeed
				return
			}
			b.feed, b.err = s.repository.NotificationFeed(ctx, id)
			if b.err != nil {
				return
			}
			if b.feed.Status != "active" {
				b.err = ErrInactiveFeed
				return
			}
			b.err = s.network(ctx, func() error { var err error; b.checkpoint, err = feedSource.Checkpoint(ctx); return err })
		})
	}
	after := s.discover(ctx, actor, input.Namespaces)
	activations := make([]Activation, len(input.Namespaces))
	for i, ref := range input.Namespaces {
		prior, err := s.namespace(before[ref.DeploymentID], ref)
		if err != nil {
			return nil, err
		}
		current, err := s.namespace(after[ref.DeploymentID], ref)
		if err != nil {
			return nil, err
		}
		a, z := before[ref.DeploymentID].discovery, after[ref.DeploymentID].discovery
		if a.InstanceID != z.InstanceID || a.RecoveryEpoch != z.RecoveryEpoch || a.PrincipalID != z.PrincipalID || prior.AuthorizationVersion != current.AuthorizationVersion {
			return nil, monitoring.ErrAuthority
		}
		id, err := newActivationID()
		if err != nil {
			return nil, err
		}
		activations[i] = Activation{ID: id, DeploymentID: ref.DeploymentID, NamespaceID: ref.NamespaceID, Status: ActivationDisabled}
		if !input.Enabled {
			continue
		}
		activations[i].Status = ActivationPending
		b := captures[ref.DeploymentID]
		if b.err != nil {
			if errors.Is(b.err, ErrInactiveFeed) || errors.Is(b.err, events.ErrUnavailable) || errors.Is(b.err, events.ErrAuthority) || errors.Is(b.err, events.ErrRecoveryRequired) {
				continue
			}
			return nil, b.err
		}
		if b.checkpoint.ControlInstanceID != z.InstanceID || b.checkpoint.RecoveryEpoch != z.RecoveryEpoch {
			return nil, monitoring.ErrAuthority
		}
		active, err := NewActivation(id, ref, b.feed, b.checkpoint, z.PrincipalID)
		if errors.Is(err, ErrInactiveFeed) {
			continue
		}
		if err != nil {
			return nil, err
		}
		activations[i] = active
	}
	return activations, nil
}

// RuleView deliberately contains no account/directory/principal IDs, private
// source cursors or activation UUIDs. Hidden scope counts remain useful while
// forbidden/unavailable references and watched job IDs are omitted.
type RuleView struct {
	ID                 string          `json:"id"`
	Revision           string          `json:"revision"`
	Name               string          `json:"name"`
	Enabled            bool            `json:"enabled"`
	Scope              string          `json:"scope"`
	OutcomeMode        string          `json:"outcomeMode"`
	Outcomes           []string        `json:"outcomes"`
	Scopes             []RuleScopeView `json:"scopes"`
	Jobs               []JobRef        `json:"jobs"`
	InaccessibleScopes int             `json:"inaccessibleScopes"`
	UnavailableScopes  int             `json:"unavailableScopes"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
}
type RuleScopeView struct {
	NamespaceRef
	Status      string     `json:"status"`
	ActivatedAt *time.Time `json:"activatedAt,omitempty"`
}

type feedSnapshot struct {
	feed events.Feed
	err  error
}

func (s *RuleService) feedSnapshots(ctx context.Context, refs []NamespaceRef) map[string]feedSnapshot {
	ids := []string{}
	for _, ref := range refs {
		if !slices.Contains(ids, ref.DeploymentID) {
			ids = append(ids, ref.DeploymentID)
		}
	}
	snapshots := make([]feedSnapshot, len(ids))
	parallel(len(ids), func(i int) {
		if s.feeds[ids[i]] == nil {
			snapshots[i].err = ErrInactiveFeed
			return
		}
		snapshots[i].feed, snapshots[i].err = s.repository.NotificationFeed(ctx, ids[i])
	})
	result := map[string]feedSnapshot{}
	for i, id := range ids {
		result[id] = snapshots[i]
	}
	return result
}
func (s *RuleService) present(ctx context.Context, actor monitoring.Actor, rule Rule) (RuleView, error) {
	views, err := s.project(ctx, actor, []Rule{rule})
	if err != nil {
		return RuleView{}, err
	}
	return views[0], nil
}
func (s *RuleService) project(ctx context.Context, actor monitoring.Actor, rules []Rule) ([]RuleView, error) {
	refs := []NamespaceRef{}
	for _, rule := range rules {
		refs = append(refs, rule.Namespaces...)
	}
	before := s.discover(ctx, actor, refs)
	feeds := s.feedSnapshots(ctx, refs)
	// Read current source authority after feed snapshots, then verify local
	// account/alias/revision and newest denial overrides after the network calls.
	// Namespace expiry is checked again while projecting each scope.
	after := s.discover(ctx, actor, refs)
	revoked := make([][]string, len(rules))
	for i, rule := range rules {
		if rule.Validate() != nil || rule.AccountID != actor.Account.ID {
			return nil, ErrInvalid
		}
		var err error
		revoked[i], err = s.repository.NotificationActivationRevocations(ctx, actor, rule.ID, rule.Revision)
		if err != nil {
			return nil, err
		}
	}
	result := make([]RuleView, 0, len(rules))
	for i, rule := range rules {
		view, err := s.view(ctx, actor, rule, before, after, feeds, revoked[i])
		if err != nil {
			return nil, err
		}
		result = append(result, view)
	}
	return result, nil
}
func (s *RuleService) view(ctx context.Context, actor monitoring.Actor, rule Rule, before, access map[string]ruleAccess, feeds map[string]feedSnapshot, revoked []string) (RuleView, error) {
	lost := []string{}
	result := RuleView{ID: rule.ID, Revision: strconv.FormatInt(rule.Revision, 10), Name: rule.Name, Enabled: rule.Enabled, Scope: rule.Scope, OutcomeMode: rule.OutcomeMode, Outcomes: slices.Clone(rule.Outcomes), Scopes: []RuleScopeView{}, Jobs: []JobRef{}, CreatedAt: rule.CreatedAt, UpdatedAt: rule.UpdatedAt}
	visible := map[NamespaceRef]bool{}
	for _, a := range rule.Activation {
		current, err := s.namespace(access[a.DeploymentID], a.Namespace())
		if err == nil {
			prior, priorErr := s.namespace(before[a.DeploymentID], a.Namespace())
			x, y := before[a.DeploymentID].discovery, access[a.DeploymentID].discovery
			if priorErr != nil || prior.AuthorizationVersion != current.AuthorizationVersion || x.InstanceID != y.InstanceID || x.RecoveryEpoch != y.RecoveryEpoch || x.PrincipalID != y.PrincipalID {
				err = monitoring.ErrAuthority
			}
		}
		if err != nil {
			if denied(err) {
				result.InaccessibleScopes++
				if a.Status == ActivationActive || a.Status == ActivationPending {
					lost = append(lost, a.ID)
				}
			} else {
				result.UnavailableScopes++
			}
			continue
		}
		scope := RuleScopeView{NamespaceRef: a.Namespace(), Status: a.Status}
		if slices.Contains(revoked, a.ID) {
			scope.Status = ActivationInaccessible
		}
		if configured := s.feeds[a.DeploymentID]; configured != nil && !slices.Contains(configured.NamespaceIDs(), a.NamespaceID) {
			scope.Status = ActivationInaccessible
			if a.Status == ActivationActive || a.Status == ActivationPending {
				lost = append(lost, a.ID)
			}
		}
		if scope.Status == ActivationActive && uuid(access[a.DeploymentID].discovery.PrincipalID) && a.Boundary.PrincipalID != access[a.DeploymentID].discovery.PrincipalID {
			// A verified canonical-owner change is not an automatic subscription
			// transfer. Require a new explicit interval under the new mapping.
			scope.Status = ActivationInaccessible
			lost = append(lost, a.ID)
		}
		if scope.Status == ActivationActive {
			d := access[a.DeploymentID].discovery
			// A new epoch pauses display until explicit feed reconciliation. Preserved
			// same-Control intervals remain valid only when the durable feed is active.
			snapshot := feeds[a.DeploymentID]
			feed, err := snapshot.feed, snapshot.err
			if err != nil || s.feeds[a.DeploymentID] == nil || feed.Status != "active" || !slices.Contains(feed.NamespaceIDs, a.NamespaceID) || !slices.Contains(feed.Checkpoint.NamespaceIDs, a.NamespaceID) || feed.Checkpoint.ControlInstanceID != d.InstanceID || feed.Checkpoint.RecoveryEpoch != d.RecoveryEpoch || a.Boundary.ControlInstanceID != d.InstanceID || a.Boundary.PrincipalID != d.PrincipalID {
				scope.Status = "unavailable"
			} else {
				at := a.Boundary.NotBefore
				scope.ActivatedAt = &at
			}
		}
		result.Scopes = append(result.Scopes, scope)
		visible[a.Namespace()] = true
	}
	if len(lost) > 0 {
		if err := s.repository.DisableNotificationActivations(ctx, actor, rule.ID, rule.Revision, lost); err != nil {
			return RuleView{}, err
		}
	}
	for _, job := range rule.Jobs {
		if visible[job.Namespace()] {
			result.Jobs = append(result.Jobs, job)
		}
	}
	return result, nil
}
func (s *RuleService) Get(ctx context.Context, actor monitoring.Actor, id string) (RuleView, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rule, err := s.repository.NotificationRule(ctx, actor, id)
	if err != nil {
		return RuleView{}, err
	}
	return s.present(ctx, actor, rule)
}

type RulePage struct {
	Items      []RuleView `json:"items"`
	NextCursor string     `json:"nextCursor,omitempty"`
}
type ruleCursor struct {
	monitoring.CursorIdentity
	After string `json:"after"`
	Limit int    `json:"limit"`
}

func (s *RuleService) List(ctx context.Context, actor monitoring.Actor, cursor string, limit int) (RulePage, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 20 || len(cursor) > 128 {
		return RulePage{}, ErrInvalid
	}
	after := ""
	if cursor != "" {
		data, _, err := s.cursors.Load(ctx, cursor)
		if err != nil {
			return RulePage{}, monitoring.ErrCursor
		}
		var state ruleCursor
		if len(data) > 1024 || json.Unmarshal(data, &state) != nil || state.AccountID != actor.Account.ID || state.QueryHash != "notification-rules/v1" || state.Limit != limit || !uuid(state.After) {
			return RulePage{}, monitoring.ErrCursor
		}
		after = state.After
	}
	rules, err := s.repository.ListNotificationRules(ctx, actor)
	if err != nil {
		return RulePage{}, err
	}
	if len(rules) > MaximumRulesPerAccount {
		return RulePage{}, ErrCapacity
	}
	slices.SortFunc(rules, func(a, b Rule) int { return strings.Compare(a.ID, b.ID) })
	selected := make([]Rule, 0, limit+1)
	for _, rule := range rules {
		if rule.ID > after {
			selected = append(selected, rule)
			if len(selected) > limit {
				break
			}
		}
	}
	more := len(selected) > limit
	if more {
		selected = selected[:limit]
	}
	views, err := s.project(ctx, actor, selected)
	if err != nil {
		return RulePage{}, err
	}
	page := RulePage{Items: views}
	if more {
		state := ruleCursor{CursorIdentity: monitoring.CursorIdentity{AccountID: actor.Account.ID, QueryHash: "notification-rules/v1", Initial: cursor == ""}, After: selected[len(selected)-1].ID, Limit: limit}
		data, _ := json.Marshal(state)
		page.NextCursor, err = s.cursors.Create(ctx, data, s.now().Add(10*time.Minute))
		if err != nil {
			return RulePage{}, err
		}
	}
	return page, nil
}
func (s *RuleService) Create(ctx context.Context, actor monitoring.Actor, input RuleInput) (RuleView, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	input, err := input.Canonical()
	if err != nil {
		return RuleView{}, err
	}
	activation, err := s.prepare(ctx, actor, input)
	if err != nil {
		return RuleView{}, err
	}
	rule, err := s.repository.CreateNotificationRule(ctx, actor, input, activation)
	if err != nil {
		return RuleView{}, err
	}
	return s.present(ctx, actor, rule)
}
func (s *RuleService) Update(ctx context.Context, actor monitoring.Actor, id string, revision int64, input RuleInput) (RuleView, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	input, err := input.Canonical()
	if err != nil {
		return RuleView{}, err
	}
	previous, err := s.repository.NotificationRule(ctx, actor, id)
	if err != nil {
		return RuleView{}, err
	}
	if previous.Revision != revision {
		return RuleView{}, ErrConflict
	}
	changed, err := MonitoringChanged(previous.RuleInput, input)
	if err != nil {
		return RuleView{}, err
	}
	activation := previous.Activation
	if changed {
		if previous.Enabled && !input.Enabled {
			// Disabling an existing rule needs no working Control or feed and must not
			// accept unrelated scope edits that have not been authorized.
			intent := input
			intent.Enabled = previous.Enabled
			changedOther, _ := MonitoringChanged(previous.RuleInput, intent)
			if changedOther {
				return RuleView{}, ErrInvalid
			}
			activation = make([]Activation, len(input.Namespaces))
			for i, ref := range input.Namespaces {
				id, err := newActivationID()
				if err != nil {
					return RuleView{}, err
				}
				activation[i] = Activation{ID: id, DeploymentID: ref.DeploymentID, NamespaceID: ref.NamespaceID, Status: ActivationDisabled}
			}
		} else {
			activation, err = s.prepare(ctx, actor, input)
			if err != nil {
				return RuleView{}, err
			}
		}
	}
	rule, err := s.repository.UpdateNotificationRule(ctx, actor, id, revision, input, activation)
	if err != nil {
		return RuleView{}, err
	}
	return s.present(ctx, actor, rule)
}
func (s *RuleService) Delete(ctx context.Context, actor monitoring.Actor, id string, revision int64) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.repository.DeleteNotificationRule(ctx, actor, id, revision)
	return err
}

// SetEnabled lets an owner stop a rule without reconstructing references that
// are deliberately redacted during a source outage or loss of access.
func (s *RuleService) SetEnabled(ctx context.Context, actor monitoring.Actor, id string, revision int64, enabled bool) (RuleView, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rule, err := s.repository.NotificationRule(ctx, actor, id)
	if err != nil {
		return RuleView{}, err
	}
	if rule.Revision != revision {
		return RuleView{}, ErrConflict
	}
	input := rule.RuleInput
	input.Enabled = enabled
	return s.Update(ctx, actor, id, revision, input)
}

// Revalidate is explicit user intent to restore an inaccessible scope. Automatic
// pending activation must pass explicit=false and never clear a denied interval.
func (s *RuleService) Revalidate(ctx context.Context, actor monitoring.Actor, id string, revision int64, explicit bool) (RuleView, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rule, err := s.repository.NotificationRule(ctx, actor, id)
	if err != nil {
		return RuleView{}, err
	}
	if rule.Revision != revision {
		return RuleView{}, ErrConflict
	}
	if !rule.Enabled {
		return s.present(ctx, actor, rule)
	}
	revoked, err := s.repository.NotificationActivationRevocations(ctx, actor, id, revision)
	if err != nil {
		return RuleView{}, err
	}
	// Group pending/revoked scopes by source so 320 namespaces do not cause
	// 960 repeated discovery calls. Current denials are recorded independently;
	// unavailable siblings retain their interval and cannot block healthy sources.
	next := slices.Clone(rule.Activation)
	access := s.discover(ctx, actor, rule.Namespaces)
	ids := []string{}
	selections := map[string][]int{}
	lost := []string{}
	for i, a := range rule.Activation {
		needs := a.Status == ActivationPending || explicit && (a.Status == ActivationInaccessible || slices.Contains(revoked, a.ID))
		if !needs || !explicit && slices.Contains(revoked, a.ID) {
			continue
		}
		_, err := s.namespace(access[a.DeploymentID], a.Namespace())
		if err != nil {
			if denied(err) {
				lost = append(lost, a.ID)
			}
			continue
		}
		if configured := s.feeds[a.DeploymentID]; configured != nil && !slices.Contains(configured.NamespaceIDs(), a.NamespaceID) {
			lost = append(lost, a.ID)
			continue
		}
		if len(selections[a.DeploymentID]) == 0 {
			ids = append(ids, a.DeploymentID)
		}
		selections[a.DeploymentID] = append(selections[a.DeploymentID], i)
	}
	if len(lost) > 0 {
		if err := s.repository.DisableNotificationActivations(ctx, actor, id, revision, lost); err != nil {
			return RuleView{}, err
		}
	}
	type candidate struct {
		values []Activation
		err    error
	}
	candidates := make([]candidate, len(ids))
	parallel(len(ids), func(i int) {
		sourceID := ids[i]
		input := rule.RuleInput
		input.Namespaces = []NamespaceRef{}
		input.Jobs = []JobRef{}
		for _, index := range selections[sourceID] {
			input.Namespaces = append(input.Namespaces, rule.Activation[index].Namespace())
		}
		for _, job := range rule.Jobs {
			if slices.Contains(input.Namespaces, job.Namespace()) {
				input.Jobs = append(input.Jobs, job)
			}
		}
		candidates[i].values, candidates[i].err = s.prepare(ctx, actor, input)
	})
	for i, sourceID := range ids {
		c := candidates[i]
		if denied(c.err) {
			// A permission changed during the before/after fence. Re-discover exact
			// denied namespaces on projection; do not revoke unaffected siblings.
			continue
		}
		if c.err != nil {
			if errors.Is(c.err, monitoring.ErrSource) || errors.Is(c.err, monitoring.ErrAuthority) || errors.Is(c.err, events.ErrUnavailable) || errors.Is(c.err, events.ErrAuthority) || errors.Is(c.err, events.ErrRecoveryRequired) {
				continue
			}
			return RuleView{}, c.err
		}
		for j, index := range selections[sourceID] {
			if c.values[j].Status == ActivationActive {
				next[index] = c.values[j]
			}
		}
	}

	updated, err := s.repository.TransitionNotificationRule(ctx, actor, id, revision, next)
	if err != nil {
		return RuleView{}, err
	}
	return s.present(ctx, actor, updated)
}
