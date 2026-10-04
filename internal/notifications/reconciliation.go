package notifications

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

// ReconciliationCandidate is private verified rule-owner intent. It contains no
// reusable token; Control must independently verify its current directory grant.
type ReconciliationCandidate struct {
	Actor     monitoring.Actor
	Rule      Rule
	Namespace NamespaceRef
}
type ReconciliationRepository interface {
	NotificationReconciliationCandidates(context.Context, string, string) ([]ReconciliationCandidate, error)
	VerifyNotificationReconciliationCandidate(context.Context, ReconciliationCandidate) error
}
type Reconciler struct {
	repository ReconciliationRepository
	sources    map[string]monitoring.Source
	now        func() time.Time
}

func NewReconciler(repository ReconciliationRepository, sources []monitoring.Source) (*Reconciler, error) {
	if repository == nil || len(sources) < 1 || len(sources) > 32 {
		return nil, ErrInvalid
	}
	r := &Reconciler{repository: repository, sources: map[string]monitoring.Source{}, now: time.Now}
	for _, source := range sources {
		if source == nil || !uuid(source.ID()) || r.sources[source.ID()] != nil {
			return nil, ErrInvalid
		}
		r.sources[source.ID()] = source
	}
	return r, nil
}
func (r *Reconciler) authority(ctx context.Context, c ReconciliationCandidate, p events.RecoveryPlan) error {
	if err := r.repository.VerifyNotificationReconciliationCandidate(ctx, c); err != nil {
		return err
	}
	d, err := r.sources[p.DeploymentID].Discover(ctx, c.Actor)
	if err != nil {
		return err
	}
	if d.Deployment.ID != p.DeploymentID || d.InstanceID != p.Checkpoint.ControlInstanceID || d.RecoveryEpoch != p.Checkpoint.RecoveryEpoch {
		return monitoring.ErrSource
	}
	for _, activation := range c.Rule.Activation {
		if activation.Namespace() != c.Namespace || activation.Status != ActivationActive {
			continue
		}
		// An existing interval belongs to the verified Control principal that
		// activated it. A new canonical mapping requires explicit revalidation;
		// it cannot silently transfer a "my jobs" observation to another owner.
		if !uuid(d.PrincipalID) {
			return monitoring.ErrAuthority
		}
		if d.PrincipalID != activation.Boundary.PrincipalID {
			return monitoring.ErrForbidden
		}
	}
	for _, ns := range d.Deployment.Namespaces {
		if ns.ID == c.Namespace.NamespaceID && ns.AuthorizationExpiresAt.After(r.now()) && slices.Contains(ns.Capabilities, "namespace.read") && slices.Contains(ns.Capabilities, "jobs.read") {
			return r.repository.VerifyNotificationReconciliationCandidate(ctx, c)
		}
	}
	return monitoring.ErrForbidden
}

// Reconcile makes a bounded best-effort comparison of available terminal state.
// It never creates events or inbox records and stores no names/logs/job content.
// Whole-namespace scans may be complete; narrower my/watched scopes and budgets
// are explicitly partial. Lack of a current rule owner is unavailable, not zero.
func (r *Reconciler) Reconcile(ctx context.Context, p events.RecoveryPlan) (events.ReconciliationReceipt, error) {
	if p.Validate() != nil || r.sources[p.DeploymentID] == nil {
		return events.ReconciliationReceipt{}, events.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	receipt := events.ReconciliationReceipt{PlanDigest: p.Digest, AcknowledgedGap: true, Namespaces: make([]events.NamespaceReconciliation, len(p.Checkpoint.NamespaceIDs))}
	var reads atomic.Int64
	parallel(len(receipt.Namespaces), func(i int) {
		ns := p.Checkpoint.NamespaceIDs[i]
		result := events.NamespaceReconciliation{NamespaceID: ns, Status: "unavailable"}
		candidates, err := r.repository.NotificationReconciliationCandidates(ctx, p.DeploymentID, ns)
		if err != nil || len(candidates) > 32 {
			receipt.Namespaces[i] = result
			return
		}
		deniedCount := 0
		for _, candidate := range candidates {
			if ctx.Err() != nil {
				break
			}
			if candidate.Rule.Validate() != nil || !candidate.Rule.Enabled || candidate.Rule.DeletedAt != nil || candidate.Namespace != (NamespaceRef{p.DeploymentID, ns}) || !slices.Contains(candidate.Rule.Namespaces, candidate.Namespace) {
				continue
			}
			if err := r.authority(ctx, candidate, p); err != nil {
				if denied(err) {
					deniedCount++
				}
				continue
			}
			result = r.scan(ctx, p, candidate, &reads)
			// A valid current owner establishes the scope of this observation. Do not
			// sum repeated scans by different owners and call them unique job totals.
			if result.Status == "complete" || result.Status == "partial" {
				break
			}
			if result.Status == "inaccessible" {
				deniedCount++
			}
		}
		if result.Status != "complete" && result.Status != "partial" {
			result = events.NamespaceReconciliation{NamespaceID: ns, Status: "unavailable"}
			if len(candidates) > 0 && deniedCount == len(candidates) {
				result.Status = "inaccessible"
			}
		}
		receipt.Namespaces[i] = result
	})
	receipt.CompletedAt = r.now().UTC()
	if err := receipt.Validate(p); err != nil {
		return events.ReconciliationReceipt{}, err
	}
	return receipt, nil
}
func (r *Reconciler) scan(ctx context.Context, p events.RecoveryPlan, c ReconciliationCandidate, reads *atomic.Int64) (result events.NamespaceReconciliation) {
	asOf := p.Checkpoint.AsOf
	result = events.NamespaceReconciliation{NamespaceID: c.Namespace.NamespaceID, Status: "partial", AsOf: &asOf}
	defer func() {
		if err := r.authority(ctx, c, p); err != nil {
			// Every exit, including budgets and failed later pages, requires a final
			// source/local authorization check before retaining any coverage count.
			result = events.NamespaceReconciliation{NamespaceID: c.Namespace.NamespaceID, Status: "unavailable"}
			if denied(err) {
				result.Status = "inaccessible"
			}
		}
	}()
	source := r.sources[p.DeploymentID]
	failed := func(err error) events.NamespaceReconciliation {
		if denied(err) || errors.Is(err, monitoring.ErrAuthority) {
			result.JobsRead = 0
		}
		if result.JobsRead == 0 {
			result.AsOf = nil
			result.Status = "unavailable"
			if denied(err) {
				result.Status = "inaccessible"
			}
		}
		return result
	}
	if c.Rule.Scope == ScopeWatchedJobs {
		for _, job := range c.Rule.Jobs {
			if job.Namespace() != c.Namespace {
				continue
			}
			if reads.Add(1) > 100000 {
				return result
			}
			value, err := source.Job(ctx, c.Actor, api.Scope{DeploymentID: p.DeploymentID, NamespaceID: c.Namespace.NamespaceID}, job.JobID)
			if err != nil {
				return failed(err)
			}
			if value.DeploymentID != p.DeploymentID || value.NamespaceID != c.Namespace.NamespaceID || value.ID != job.JobID {
				return failed(monitoring.ErrSource)
			}
			// The receipt counts inspected records; an active watched job is still an
			// observation and cannot become a synthetic terminal transition.
			result.JobsRead++
		}
	} else {
		cursor := ""
		seen := map[string]bool{}
		owner := ""
		if c.Rule.Scope == ScopeMyJobs {
			owner = "me"
		}
		for page := 0; page < 50; page++ {
			if reads.Add(200) > 100000 {
				return result
			}
			value, err := source.Jobs(ctx, c.Actor, monitoring.SourceQuery{Query: monitoring.Query{Limit: 200, Phase: "terminal", Owner: owner}, NamespaceID: c.Namespace.NamespaceID, CreatedBefore: p.Checkpoint.AsOf, Cursor: cursor})
			if err != nil {
				return failed(err)
			}
			if len(value.Items) > 200 || len(value.NextCursor) > 8192 {
				return failed(monitoring.ErrSource)
			}
			for _, job := range value.Items {
				if job.DeploymentID != p.DeploymentID || job.NamespaceID != c.Namespace.NamespaceID || !uuid(job.ID) || seen[job.ID] {
					return failed(monitoring.ErrSource)
				}
				seen[job.ID] = true
			}
			result.JobsRead += int64(len(value.Items))
			if value.NextCursor == "" {
				if c.Rule.Scope == ScopeNamespaceJobs {
					result.Status = "complete"
				}
				break
			}
			if value.NextCursor == cursor {
				return failed(monitoring.ErrSource)
			}
			cursor = value.NextCursor
		}
	}
	return result
}

var _ events.TerminalReconciler = (*Reconciler)(nil)
