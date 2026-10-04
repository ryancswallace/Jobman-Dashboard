// Package notifications defines personal notification intent. A matching rule
// never grants access: evaluation, delivery and inbox reads separately require
// current represented-user authorization and a healthy pinned source epoch.
package notifications

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

const (
	MaximumRulesPerAccount = 100
	MaximumNamespaces      = 320
	MaximumWatchedJobs     = 100
	MaximumSources         = 32
	MaximumNameBytes       = 120
	MaximumInputBytes      = 64 << 10
	MaximumRuleBytes       = 512 << 10

	ScopeWatchedJobs   = "watched_jobs"
	ScopeMyJobs        = "my_jobs"
	ScopeNamespaceJobs = "namespace_jobs"
	OutcomeSelected    = "selected"
	OutcomeAllTerminal = "all_terminal"

	ActivationPending      = "pending"
	ActivationActive       = "active"
	ActivationInaccessible = "inaccessible"
	ActivationDisabled     = "disabled"
)

var (
	ErrInvalid            = errors.New("invalid notification rule")
	ErrUnsupportedOutcome = errors.New("unsupported individual terminal outcome")
	ErrTransition         = errors.New("invalid notification rule transition")
	ErrInactiveFeed       = errors.New("notification activation requires an initialized matching source feed")
	ErrConflict           = errors.New("notification rule revision conflict")
	ErrNotFound           = errors.New("notification rule not found or inaccessible")
	ErrCapacity           = errors.New("notification rule or history capacity reached")
	ErrRateLimited        = errors.New("notification rule mutation rate limit reached")
)

type NamespaceRef struct {
	DeploymentID string `json:"deploymentId"`
	NamespaceID  string `json:"namespaceId"`
}

type JobRef struct {
	DeploymentID string `json:"deploymentId"`
	NamespaceID  string `json:"namespaceId"`
	JobID        string `json:"jobId"`
}

func (j JobRef) Namespace() NamespaceRef { return NamespaceRef{j.DeploymentID, j.NamespaceID} }

// RuleInput is the complete personal intent accepted from a client. Source
// identity, owner mapping, activation intervals and revisions are server-owned.
type RuleInput struct {
	Name        string         `json:"name"`
	Enabled     bool           `json:"enabled"`
	Scope       string         `json:"scope"`
	Namespaces  []NamespaceRef `json:"namespaces"`
	Jobs        []JobRef       `json:"jobs"`
	OutcomeMode string         `json:"outcomeMode"`
	Outcomes    []string       `json:"outcomes"`
}

// Canonical owns its slices, keeps explicit selections, and makes equivalent
// orderings/duplicates identical. Unused editor values are cleared when changing
// scope or selecting all_terminal; they do not silently narrow the active mode.
func (in RuleInput) Canonical() (RuleInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !utf8.ValidString(in.Name) || len(in.Name) == 0 || len(in.Name) > MaximumNameBytes || strings.ContainsFunc(in.Name, unicode.IsControl) || len(in.Namespaces) < 1 || len(in.Namespaces) > MaximumNamespaces || len(in.Jobs) > MaximumWatchedJobs || len(in.Outcomes) > 6 {
		return RuleInput{}, ErrInvalid
	}
	if in.Scope != ScopeWatchedJobs && in.Scope != ScopeMyJobs && in.Scope != ScopeNamespaceJobs {
		return RuleInput{}, ErrInvalid
	}
	in.Namespaces = slices.Clone(in.Namespaces)
	sources := map[string]bool{}
	for _, ref := range in.Namespaces {
		if !validNamespace(ref) {
			return RuleInput{}, ErrInvalid
		}
		sources[ref.DeploymentID] = true
	}
	if len(sources) > MaximumSources {
		return RuleInput{}, ErrInvalid
	}
	slices.SortFunc(in.Namespaces, compareNamespace)
	in.Namespaces = slices.Compact(in.Namespaces)
	if in.Scope == ScopeWatchedJobs {
		if len(in.Jobs) == 0 {
			return RuleInput{}, ErrInvalid
		}
		in.Jobs = slices.Clone(in.Jobs)
		for _, job := range in.Jobs {
			if !uuid(job.JobID) || !slices.Contains(in.Namespaces, job.Namespace()) {
				return RuleInput{}, ErrInvalid
			}
		}
		slices.SortFunc(in.Jobs, compareJob)
		in.Jobs = slices.Compact(in.Jobs)
	} else {
		in.Jobs = []JobRef{}
	}
	switch in.OutcomeMode {
	case OutcomeSelected:
		if len(in.Outcomes) == 0 {
			return RuleInput{}, ErrInvalid
		}
		in.Outcomes = slices.Clone(in.Outcomes)
		for _, outcome := range in.Outcomes {
			switch outcome {
			case "success", "failure", "cancelled", "timed_out", "aborted", "lost":
			default:
				return RuleInput{}, ErrUnsupportedOutcome
			}
		}
		slices.Sort(in.Outcomes)
		in.Outcomes = slices.Compact(in.Outcomes)
	case OutcomeAllTerminal:
		in.Outcomes = []string{}
	default:
		return RuleInput{}, ErrInvalid
	}
	encoded, err := json.Marshal(in)
	if err != nil || len(encoded) > MaximumInputBytes {
		return RuleInput{}, ErrInvalid
	}
	return in, nil
}

// MonitoringChanged excludes only the display name. An explicit enable, scope,
// selected namespace/job or outcome edit starts new monitoring intervals;
// delayed candidates under prior intervals remain audit, not delivery authority.
func MonitoringChanged(before, after RuleInput) (bool, error) {
	a, err := before.Canonical()
	if err != nil {
		return false, err
	}
	b, err := after.Canonical()
	if err != nil {
		return false, err
	}
	a.Name, b.Name = "", ""
	return !equalJSON(a, b), nil
}

// Boundary is private persisted provenance, not a client-supplied grant or a
// public API response. HeadCursor stays opaque. PrincipalID is the independently
// verified canonical represented principal for this specific Control instance.
type Boundary struct {
	ControlInstanceID string    `json:"controlInstanceId"`
	RecoveryEpoch     string    `json:"recoveryEpoch"`
	PrincipalID       string    `json:"principalId"`
	HeadCursor        string    `json:"headCursor"`
	NotBefore         time.Time `json:"notBefore"`
}

// Activation is one immutable interval for one source-qualified namespace. A
// sibling namespace can keep its interval when only this namespace loses access.
// Any status/boundary replacement gets a fresh ID; historical versions retain
// the prior interval without allowing it to authorize future delivery.
type Activation struct {
	ID           string    `json:"id"`
	DeploymentID string    `json:"deploymentId"`
	NamespaceID  string    `json:"namespaceId"`
	Status       string    `json:"status"`
	Boundary     *Boundary `json:"boundary,omitempty"`
}

func (a Activation) Namespace() NamespaceRef { return NamespaceRef{a.DeploymentID, a.NamespaceID} }

func (a Activation) Validate() error {
	if !uuid(a.ID) || !validNamespace(a.Namespace()) {
		return ErrInvalid
	}
	switch a.Status {
	case ActivationActive:
		b := a.Boundary
		if b == nil || !uuid(b.ControlInstanceID) || !positiveDecimal(b.RecoveryEpoch) || !uuid(b.PrincipalID) || !events.ValidCursor(b.HeadCursor) || !validTime(b.NotBefore) {
			return ErrInvalid
		}
	case ActivationPending, ActivationInaccessible, ActivationDisabled:
		if a.Boundary != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// NewActivation accepts a feed already initialized in durable storage. The
// bridge must capture this checkpoint after verifying current represented-user
// access and source ownership. Its persistence transaction must recheck the
// feed identity/epoch and expected rule revision before publishing the interval.
func NewActivation(id string, namespace NamespaceRef, feed events.Feed, checkpoint events.Checkpoint, principalID string) (Activation, error) {
	if checkpoint.Validate() != nil || feed.Status != "active" || feed.Generation <= 0 || feed.Checkpoint.Validate() != nil || !events.ValidCursor(feed.Cursor) || feed.DeploymentID != namespace.DeploymentID || checkpoint.DeploymentID != namespace.DeploymentID || feed.Checkpoint.DeploymentID != namespace.DeploymentID || feed.Checkpoint.ControlInstanceID != checkpoint.ControlInstanceID || feed.Checkpoint.RecoveryEpoch != checkpoint.RecoveryEpoch || !slices.Equal(feed.NamespaceIDs, feed.Checkpoint.NamespaceIDs) || !slices.Equal(feed.NamespaceIDs, checkpoint.NamespaceIDs) || !slices.Contains(feed.NamespaceIDs, namespace.NamespaceID) {
		return Activation{}, ErrInactiveFeed
	}
	activation := Activation{ID: id, DeploymentID: namespace.DeploymentID, NamespaceID: namespace.NamespaceID, Status: ActivationActive,
		Boundary: &Boundary{ControlInstanceID: checkpoint.ControlInstanceID, RecoveryEpoch: checkpoint.RecoveryEpoch, PrincipalID: principalID, HeadCursor: checkpoint.HeadCursor, NotBefore: checkpoint.AsOf.UTC()}}
	if err := activation.Validate(); err != nil {
		return Activation{}, err
	}
	return activation, nil
}

// Rule snapshots are immutable history records. They are bounded storage
// models: API projection must omit private boundaries and inaccessible details.
type Rule struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	Revision  int64  `json:"revision,string"`
	RuleInput
	Activation []Activation `json:"activation"`
	CreatedAt  time.Time    `json:"createdAt"`
	UpdatedAt  time.Time    `json:"updatedAt"`
	DeletedAt  *time.Time   `json:"deletedAt,omitempty"`
}

func (r Rule) Validate() error {
	canonical, err := r.RuleInput.Canonical()
	if err != nil {
		return err
	}
	if !uuid(r.ID) || !uuid(r.AccountID) || r.Revision <= 0 || !validTime(r.CreatedAt) || !validTime(r.UpdatedAt) || r.UpdatedAt.Before(r.CreatedAt) || !equalJSON(r.RuleInput, canonical) || len(r.Activation) != len(r.Namespaces) {
		return ErrInvalid
	}
	if r.DeletedAt != nil && (!validTime(*r.DeletedAt) || !r.DeletedAt.Equal(r.UpdatedAt) || r.Enabled) {
		return ErrInvalid
	}
	ids := map[string]bool{}
	for index, a := range r.Activation {
		if a.Validate() != nil || a.Namespace() != r.Namespaces[index] || ids[a.ID] || !r.Enabled && a.Status != ActivationDisabled || r.Enabled && a.Status == ActivationDisabled {
			return ErrInvalid
		}
		ids[a.ID] = true
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > MaximumRuleBytes {
		return ErrInvalid
	}
	return nil
}

// ValidateTransition is a persistence invariant, not an authorization check.
// Callers must additionally reject reused interval IDs from older history and
// perform compare-and-swap against the stored revision in the same transaction.
func ValidateTransition(previous, next Rule) error {
	if previous.Validate() != nil || next.Validate() != nil || previous.ID != next.ID || previous.AccountID != next.AccountID || previous.Revision == int64(^uint64(0)>>1) || next.Revision != previous.Revision+1 || !next.CreatedAt.Equal(previous.CreatedAt) || next.UpdatedAt.Before(previous.UpdatedAt) || previous.DeletedAt != nil {
		return ErrTransition
	}
	changed, err := MonitoringChanged(previous.RuleInput, next.RuleInput)
	if err != nil {
		return err
	}
	prior := map[string]Activation{}
	for _, activation := range previous.Activation {
		prior[activation.ID] = activation
	}
	for _, activation := range next.Activation {
		if old, exists := prior[activation.ID]; exists && (changed || !equalJSON(old, activation)) {
			return ErrTransition
		}
	}
	return nil
}

// Match records which historical intent produced a candidate. It carries no
// current authorization; overlapping rules attach distinct Matches to one
// account/source/original-event inbox identity in the persistence layer.
type Match struct {
	AccountID         string `json:"accountId"`
	RuleID            string `json:"ruleId"`
	Revision          int64  `json:"revision,string"`
	ActivationID      string `json:"activationId"`
	DeploymentID      string `json:"deploymentId"`
	NamespaceID       string `json:"namespaceId"`
	ControlInstanceID string `json:"controlInstanceId"`
	EventID           string `json:"eventId"`
}

// MatchVersion interprets one validated historical rule snapshot. RecordedAt
// must be strictly after the source-clock activation time: an old unpublished
// outbox event must not become a new subscriber's notification. Observed
// completion time and opaque cursor contents are never activation substitutes.
// The caller must gate source recovery/epoch separately; event positions may be
// reassigned after recovery and are not compared across epochs here. Explicit
// reconciliation of the same Control may keep an original active interval for
// late recoverable events. AD grant loss/regrant instead replaces that interval;
// a restart alone is neither a grant loss nor permission to create a new one.
func MatchVersion(rule Rule, event events.Event) (*Match, error) {
	if err := rule.Validate(); err != nil {
		return nil, err
	}
	if event.Validate() != nil {
		return nil, events.ErrInvalid
	}
	if !rule.Enabled || rule.DeletedAt != nil || event.Imported || event.Reconciliation {
		return nil, nil
	}
	ref := NamespaceRef{event.DeploymentID, event.NamespaceID}
	index, found := slices.BinarySearchFunc(rule.Namespaces, ref, compareNamespace)
	if !found {
		return nil, nil
	}
	activation := rule.Activation[index]
	if activation.Status != ActivationActive || activation.Boundary.ControlInstanceID != event.ControlInstanceID || !event.RecordedAt.After(activation.Boundary.NotBefore) {
		return nil, nil
	}
	if rule.OutcomeMode == OutcomeSelected && !slices.Contains(rule.Outcomes, event.Outcome) {
		return nil, nil
	}
	switch rule.Scope {
	case ScopeMyJobs:
		if event.OwnerPrincipalID != activation.Boundary.PrincipalID {
			return nil, nil
		}
	case ScopeWatchedJobs:
		if _, found := slices.BinarySearchFunc(rule.Jobs, JobRef{event.DeploymentID, event.NamespaceID, event.JobID}, compareJob); !found {
			return nil, nil
		}
	}
	return &Match{AccountID: rule.AccountID, RuleID: rule.ID, Revision: rule.Revision, ActivationID: activation.ID, DeploymentID: event.DeploymentID, NamespaceID: event.NamespaceID, ControlInstanceID: event.ControlInstanceID, EventID: event.EventID}, nil
}

// StillApplicable suppresses disabled/deleted/replaced intervals immediately,
// including candidates awaiting delivery. Name-only revisions may preserve the
// exact same activation. It is deliberately independent of current user access,
// source health, device eligibility and overlapping matches, checked elsewhere.
func StillApplicable(match Match, current Rule) (bool, error) {
	if err := current.Validate(); err != nil {
		return false, err
	}
	if !uuid(match.AccountID) || !uuid(match.RuleID) || match.Revision <= 0 || !uuid(match.ActivationID) || !validNamespace(NamespaceRef{match.DeploymentID, match.NamespaceID}) || !uuid(match.ControlInstanceID) || !uuid(match.EventID) {
		return false, ErrInvalid
	}
	if !current.Enabled || current.DeletedAt != nil || current.ID != match.RuleID || current.AccountID != match.AccountID || current.Revision < match.Revision {
		return false, nil
	}
	index, found := slices.BinarySearchFunc(current.Namespaces, NamespaceRef{match.DeploymentID, match.NamespaceID}, compareNamespace)
	if !found {
		return false, nil
	}
	activation := current.Activation[index]
	return activation.Status == ActivationActive && activation.ID == match.ActivationID && activation.Boundary.ControlInstanceID == match.ControlInstanceID, nil
}

func validNamespace(ref NamespaceRef) bool { return uuid(ref.DeploymentID) && uuid(ref.NamespaceID) }
func compareNamespace(a, b NamespaceRef) int {
	if result := strings.Compare(a.DeploymentID, b.DeploymentID); result != 0 {
		return result
	}
	return strings.Compare(a.NamespaceID, b.NamespaceID)
}
func compareJob(a, b JobRef) int {
	if result := compareNamespace(a.Namespace(), b.Namespace()); result != 0 {
		return result
	}
	return strings.Compare(a.JobID, b.JobID)
}
func equalJSON(a, b any) bool {
	x, errX := json.Marshal(a)
	y, errY := json.Marshal(b)
	return errX == nil && errY == nil && bytes.Equal(x, y)
}
func positiveDecimal(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == value
}
func validTime(value time.Time) bool {
	return !value.IsZero() && value.Year() > 0 && value.Year() <= 9999
}
func uuid(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
