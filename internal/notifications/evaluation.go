package notifications

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

const (
	MaximumFanoutRules        = 50
	MaximumEventAccounts      = 10000
	MaximumPendingEvaluations = 100000
	MaximumPendingDeliveries  = 1000000
	EvaluationLease           = 30 * time.Second
	MaximumSourceFenceAge     = 30 * time.Second
	InboxLifetime             = 30 * 24 * time.Hour
	PushLifetime              = 24 * time.Hour
)

var (
	ErrEvaluationInvalid  = errors.New("invalid notification evaluation")
	ErrEvaluationLease    = errors.New("notification evaluation lease expired or changed")
	ErrEvaluationHeld     = errors.New("notification delivery is held")
	ErrEvaluationSource   = errors.New("notification source fence is stale or unavailable")
	ErrEvaluationCapacity = errors.New("notification pending work capacity reached")
	ErrEvaluationEmpty    = errors.New("no notification work is currently available")
	ErrEvaluationAdvanced = errors.New("notification work advanced without remote evaluation")
)

// SourceFence is private worker evidence, never an HTTP input. The worker must
// fetch its checkpoint from the configured service immediately before calling
// the repository; a durable feed checkpoint alone is not a source health check.
type SourceFence struct {
	Checkpoint            events.Checkpoint
	FeedGeneration        int64
	ConfigurationRevision int64
	VerifiedAt            time.Time
}

func (f SourceFence) Validate(now time.Time) error {
	if f.Checkpoint.Validate() != nil || f.FeedGeneration < 1 || f.ConfigurationRevision < 1 || f.VerifiedAt.IsZero() || f.VerifiedAt.After(now.Add(5*time.Second)) || now.Sub(f.VerifiedAt) > MaximumSourceFenceAge {
		return ErrEvaluationSource
	}
	return nil
}

// SameSource permits a refreshed ingestion generation, but never a new epoch,
// source instance, configured namespace set or operator configuration revision.
func (f SourceFence) SameSource(other SourceFence) bool {
	return f.Checkpoint.DeploymentID == other.Checkpoint.DeploymentID && f.Checkpoint.ControlInstanceID == other.Checkpoint.ControlInstanceID && f.Checkpoint.RecoveryEpoch == other.Checkpoint.RecoveryEpoch && slices.Equal(f.Checkpoint.NamespaceIDs, other.Checkpoint.NamespaceIDs) && f.ConfigurationRevision == other.ConfigurationRevision
}

type EventKey struct{ DeploymentID, ControlInstanceID, EventID string }

func Key(e events.Event) EventKey { return EventKey{e.DeploymentID, e.ControlInstanceID, e.EventID} }
func (k EventKey) Validate() error {
	if !uuid(k.DeploymentID) || !uuid(k.ControlInstanceID) || !uuid(k.EventID) {
		return ErrEvaluationInvalid
	}
	return nil
}

type FanoutClaim struct {
	Key            EventKey
	LeaseToken     string
	Revision       int64
	HoldGeneration int64
	Fence          SourceFence
}
type FanoutProgress struct {
	RulesRead, AccountsAdded int
	Done                     bool
}

// MatchedRule retains the immutable intent that matched the original event.
// No stored match grants current access. Commits and provider handoff must also
// check current rule intervals, local identity and remote directory authority.
type MatchedRule struct {
	Match Match
	Rule  Rule
}
type EvaluationClaim struct {
	ID             string
	LeaseToken     string
	Revision       int64
	HoldGeneration int64
	Fence          SourceFence
	Actor          monitoring.Actor
	Event          events.Event
	Matches        []MatchedRule
	Attempts       int64
}

// AuthorizationProof is produced by a represented-user read, not by a token
// claim or an event owner. ExpiresAt retains Control's actual proof lifetime.
type AuthorizationProof struct {
	AccountID                        string
	Namespace                        NamespaceRef
	ControlInstanceID, RecoveryEpoch string
	PrincipalID                      string
	Version                          string
	CheckedAt, ExpiresAt             time.Time
}

func (p AuthorizationProof) Validate(now time.Time) error {
	if !uuid(p.AccountID) || !validNamespace(p.Namespace) || !uuid(p.ControlInstanceID) || !positiveDecimal(p.RecoveryEpoch) || !uuid(p.PrincipalID) || p.Version == "" || len(p.Version) > 128 || p.CheckedAt.IsZero() || p.CheckedAt.After(now.Add(5*time.Second)) || now.Sub(p.CheckedAt) > 120*time.Second || !p.ExpiresAt.After(now) || p.ExpiresAt.After(p.CheckedAt.Add(120*time.Second)) {
		return ErrEvaluationInvalid
	}
	return nil
}

const (
	EvaluationAuthorized   = "authorized"
	EvaluationInaccessible = "inaccessible"
	// Resource absence with a still-authorized namespace suppresses only this
	// event. It is not evidence that the account lost its namespace grant.
	EvaluationResourceInaccessible = "resource_inaccessible"
	EvaluationDefer                = "defer"
)

type EvaluationDecision struct {
	Outcome string
	Proof   AuthorizationProof
}
type EvaluationResult struct {
	State, InboxID string
	Deliveries     int
}

// Retry delay remains bounded without abandoning unavailable accounts. An
// account's independent retry time never stalls fanout or another account.
func EvaluationRetryDelay(attempt int64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 7 {
		attempt = 7
	}
	return min(5*time.Second*time.Duration(1<<uint(attempt-1)), 5*time.Minute)
}

type EvaluationRepository interface {
	ClaimNotificationFanout(context.Context, SourceFence) (FanoutClaim, error)
	AppendNotificationFanout(context.Context, FanoutClaim) (FanoutProgress, error)
	ReleaseNotificationFanout(context.Context, FanoutClaim) error
	ClaimNotificationEvaluation(context.Context, SourceFence) (EvaluationClaim, error)
	CommitNotificationEvaluation(context.Context, EvaluationClaim, SourceFence, EvaluationDecision) (EvaluationResult, error)
	DeferNotificationEvaluation(context.Context, EvaluationClaim) error
}
