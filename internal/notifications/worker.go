package notifications

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type EvaluationFeeds interface {
	NotificationFeed(context.Context, string) (events.Feed, error)
}

var errVerifiedNamespaceLoss = errors.New("current discovery confirms namespace access loss")

// Evaluator performs represented-user network reads outside SQL transactions.
// A service-authorized feed alone never permits an inbox insert.
type Evaluator struct {
	repository EvaluationRepository
	feeds      EvaluationFeeds
	sources    map[string]RuleSource
	events     map[string]events.Source
	ids        []string
	revision   int64
	slots      chan struct{}
	now        func() time.Time
}

func NewEvaluator(repository EvaluationRepository, feeds EvaluationFeeds, sources []RuleSource, eventSources []events.Source, configurationRevision int64) (*Evaluator, error) {
	if repository == nil || feeds == nil || len(sources) < 1 || len(sources) > MaximumSources || len(eventSources) != len(sources) || configurationRevision < 1 {
		return nil, ErrEvaluationInvalid
	}
	w := &Evaluator{repository: repository, feeds: feeds, sources: map[string]RuleSource{}, events: map[string]events.Source{}, revision: configurationRevision, slots: make(chan struct{}, 4), now: time.Now}
	for _, s := range sources {
		if s == nil || !uuid(s.ID()) || w.sources[s.ID()] != nil {
			return nil, ErrEvaluationInvalid
		}
		w.sources[s.ID()] = s
		w.ids = append(w.ids, s.ID())
	}
	for _, s := range eventSources {
		if s == nil || w.sources[s.SourceID()] == nil || w.events[s.SourceID()] != nil {
			return nil, ErrEvaluationInvalid
		}
		ns := s.NamespaceIDs()
		if len(ns) < 1 || len(ns) > MaximumNamespaces {
			return nil, ErrEvaluationInvalid
		}
		for i, id := range ns {
			if !uuid(id) || i > 0 && ns[i-1] >= id {
				return nil, ErrEvaluationInvalid
			}
		}
		w.events[s.SourceID()] = s
	}
	return w, nil
}

func (w *Evaluator) fence(ctx context.Context, id string) (SourceFence, error) {
	s := w.events[id]
	if s == nil {
		return SourceFence{}, ErrEvaluationInvalid
	}
	c, err := s.Checkpoint(ctx)
	if err != nil {
		return SourceFence{}, err
	}
	verified := w.now().UTC()
	if c.Validate() != nil || c.DeploymentID != id || !slices.Equal(c.NamespaceIDs, s.NamespaceIDs()) {
		return SourceFence{}, ErrEvaluationSource
	}
	f, err := w.feeds.NotificationFeed(ctx, id)
	if err != nil {
		return SourceFence{}, err
	}
	// The repository decides whether a paused feed is a pure capacity pause.
	// Never infer permission to bypass a recovery gap in this network layer.
	if f.DeploymentID != id || f.Checkpoint.Validate() != nil || f.Checkpoint.ControlInstanceID != c.ControlInstanceID || f.Checkpoint.RecoveryEpoch != c.RecoveryEpoch || !slices.Equal(f.NamespaceIDs, c.NamespaceIDs) || !slices.Equal(f.Checkpoint.NamespaceIDs, c.NamespaceIDs) {
		return SourceFence{}, ErrEvaluationSource
	}
	result := SourceFence{Checkpoint: c, FeedGeneration: f.Generation, ConfigurationRevision: w.revision, VerifiedAt: verified}
	return result, result.Validate(w.now())
}

// Step gives fanout and account evaluation a turn even when fanout admission is
// full. Each account has an independent durable retry time. Sixteen evaluations
// bound one pass; a busy source then yields to the other configured sources.
func (w *Evaluator) Step(ctx context.Context, id string) (progress bool, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	select {
	case w.slots <- struct{}{}:
		defer func() { <-w.slots }()
	case <-ctx.Done():
		return false, ctx.Err()
	}
	f, err := w.fence(ctx, id)
	if err != nil {
		return false, err
	}
	claim, err := w.repository.ClaimNotificationFanout(ctx, f)
	if err == nil {
		_, err = w.repository.AppendNotificationFanout(ctx, claim)
		if err == nil {
			progress = true
		}
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		_ = w.repository.ReleaseNotificationFanout(cleanup, claim)
		done()
	}
	if err != nil && !errors.Is(err, ErrEvaluationEmpty) && !errors.Is(err, ErrEvaluationCapacity) && !errors.Is(err, ErrEvaluationLease) {
		return progress, err
	}
	for range 16 {
		f, err = w.fence(ctx, id)
		if err != nil {
			return progress, err
		}
		c, err := w.repository.ClaimNotificationEvaluation(ctx, f)
		if errors.Is(err, ErrEvaluationEmpty) {
			return progress, nil
		}
		if errors.Is(err, ErrEvaluationAdvanced) {
			progress = true
			continue
		}
		if err != nil {
			return progress, err
		}
		if err = w.evaluate(ctx, c); err != nil {
			// Deferring one account must not stop other due accounts in this pass.
			// The durable store schedules its next attempt independently.
			if ctx.Err() != nil {
				return progress, ctx.Err()
			}
			resultErr = err
		}
		progress = true
	}
	return progress, resultErr
}

func (w *Evaluator) authority(ctx context.Context, c EvaluationClaim) (AuthorizationProof, error) {
	s := w.sources[c.Event.DeploymentID]
	if s == nil {
		return AuthorizationProof{}, ErrEvaluationSource
	}
	d, err := s.Discover(ctx, c.Actor)
	if err != nil {
		return AuthorizationProof{}, err
	}
	if d.Deployment.ID != c.Event.DeploymentID || d.Deployment.Status != "available" || d.InstanceID != c.Fence.Checkpoint.ControlInstanceID || d.RecoveryEpoch != c.Fence.Checkpoint.RecoveryEpoch || len(d.Deployment.Namespaces) > MaximumNamespaces {
		return AuthorizationProof{}, ErrEvaluationSource
	}
	seen := map[string]bool{}
	var selected *api.Namespace
	for i := range d.Deployment.Namespaces {
		ns := &d.Deployment.Namespaces[i]
		if !uuid(ns.ID) || seen[ns.ID] {
			return AuthorizationProof{}, monitoring.ErrAuthority
		}
		seen[ns.ID] = true
		if ns.ID == c.Event.NamespaceID {
			selected = ns
		}
	}
	if selected == nil {
		return AuthorizationProof{}, errVerifiedNamespaceLoss
	}
	p := AuthorizationProof{AccountID: c.Actor.Account.ID, Namespace: NamespaceRef{c.Event.DeploymentID, c.Event.NamespaceID}, ControlInstanceID: d.InstanceID, RecoveryEpoch: d.RecoveryEpoch, PrincipalID: d.PrincipalID, Version: selected.AuthorizationVersion, CheckedAt: selected.AuthorizationCheckedAt, ExpiresAt: selected.AuthorizationExpiresAt}
	// Stale or malformed evidence is an outage, never a durable access removal.
	if p.Validate(w.now()) != nil {
		return AuthorizationProof{}, monitoring.ErrAuthority
	}
	if !slices.Contains(selected.Capabilities, "namespace.read") || !slices.Contains(selected.Capabilities, "jobs.read") {
		return AuthorizationProof{}, errVerifiedNamespaceLoss
	}
	return p, nil
}

func (w *Evaluator) evaluate(ctx context.Context, c EvaluationClaim) (resultErr error) {
	committed := false
	defer func() {
		if !committed {
			cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer done()
			_ = w.repository.DeferNotificationEvaluation(cleanup, c)
		}
	}()
	d, f, err := w.VerifyEvent(ctx, c.Actor, c.Event, c.Fence)
	if err != nil {
		return err
	}
	_, err = w.repository.CommitNotificationEvaluation(ctx, c, f, d)
	committed = err == nil
	return err
}

// CurrentSourceFence checks live source identity and the durable local feed.
func (w *Evaluator) CurrentSourceFence(ctx context.Context, id string) (SourceFence, error) {
	return w.fence(ctx, id)
}

// VerifyEvent is shared by initial inbox evaluation and provider delivery. It
// returns evidence, never a cached grant. Persistence must recheck actual expiry
// and all current local fences immediately before publishing or handing off.
func (w *Evaluator) VerifyEvent(ctx context.Context, actor monitoring.Actor, event events.Event, fence SourceFence) (EvaluationDecision, SourceFence, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := EvaluationClaim{Actor: actor, Event: event, Fence: fence}
	if c.Event.Validate() != nil || !uuid(c.Actor.Account.ID) || c.Event.DeploymentID != c.Fence.Checkpoint.DeploymentID || c.Event.ControlInstanceID != c.Fence.Checkpoint.ControlInstanceID {
		return EvaluationDecision{}, SourceFence{}, ErrEvaluationInvalid
	}
	d := EvaluationDecision{Outcome: EvaluationAuthorized}
	before, err := w.authority(ctx, c)
	if err != nil {
		if !errors.Is(err, errVerifiedNamespaceLoss) {
			return EvaluationDecision{}, SourceFence{}, err
		}
		d.Outcome = EvaluationInaccessible
	} else {
		job, jobErr := w.sources[c.Event.DeploymentID].Job(ctx, c.Actor, api.Scope{DeploymentID: c.Event.DeploymentID, NamespaceID: c.Event.NamespaceID}, c.Event.JobID)
		after, authErr := w.authority(ctx, c)
		if authErr != nil {
			if !errors.Is(authErr, errVerifiedNamespaceLoss) {
				return EvaluationDecision{}, SourceFence{}, authErr
			}
			d.Outcome = EvaluationInaccessible
		} else if before.PrincipalID != after.PrincipalID || before.Version != after.Version {
			return EvaluationDecision{}, SourceFence{}, monitoring.ErrAuthority
		} else if jobErr != nil {
			if !denied(jobErr) {
				return EvaluationDecision{}, SourceFence{}, jobErr
			}
			// Deleting a historical job does not opt the owner out of future jobs
			// in an otherwise still-authorized namespace.
			d.Outcome = EvaluationResourceInaccessible
		} else {
			if job.ID != c.Event.JobID || job.DeploymentID != c.Event.DeploymentID || job.NamespaceID != c.Event.NamespaceID {
				return EvaluationDecision{}, SourceFence{}, monitoring.ErrSource
			}
			d.Proof = after
		}
	}
	f, err := w.fence(ctx, c.Event.DeploymentID)
	if err != nil {
		return EvaluationDecision{}, SourceFence{}, err
	}
	if !c.Fence.SameSource(f) {
		return EvaluationDecision{}, SourceFence{}, ErrEvaluationSource
	}
	return d, f, nil
}

func (w *Evaluator) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for _, id := range w.ids {
		workers.Add(1)
		go func() {
			defer workers.Done()
			failures := 0
			for ctx.Err() == nil {
				progress, err := w.Step(ctx, id)
				delay := 5 * time.Second
				if progress {
					failures = 0
					delay = 100 * time.Millisecond
				} else if err != nil {
					failures++
					delay = time.Duration(1<<min(failures, 6)) * time.Second
				} else {
					failures = 0
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	}
	workers.Wait()
}
