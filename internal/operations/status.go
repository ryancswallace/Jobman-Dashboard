package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaximumStatusDeployments = 32

var (
	ErrStatusInvalid     = errors.New("invalid operator status request or snapshot")
	ErrStatusUnavailable = errors.New("operator status unavailable")
)

// Deployment comes only from trusted operator configuration. Names are JSON
// display text; metric labels use the bounded, stable deployment UUID alone.
type Deployment struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type StatusRepository interface {
	OperationalStatus(context.Context, []string) (HealthSnapshot, error)
}

// HealthSnapshot is a local database observation, not a live source, directory,
// provider, or dependency readiness check. All counts are exact decimal strings.
// It intentionally contains no payload, account, job, event, namespace, cursor,
// installation, topic, signing-key, or token identifiers.
type HealthSnapshot struct {
	ObservedAt time.Time      `json:"observedAt"`
	Sources    []SourceHealth `json:"sources"`
	Activation QueueHealth    `json:"activation"`
	Hold       HoldHealth     `json:"hold"`
	Provider   ProviderHealth `json:"provider"`
}

type SourceHealth struct {
	DeploymentID string       `json:"deploymentId"`
	DisplayName  string       `json:"displayName,omitempty"`
	State        string       `json:"state"`
	Feed         *FeedHealth  `json:"feed,omitempty"`
	Events       *Backlog     `json:"events,omitempty"`
	Fanout       *Backlog     `json:"fanout,omitempty"`
	Evaluation   *QueueHealth `json:"evaluation,omitempty"`
	Delivery     *QueueHealth `json:"delivery,omitempty"`
}

type FeedHealth struct {
	LastError               string             `json:"lastError,omitempty"`
	LastSuccessAt           *time.Time         `json:"lastSuccessAt,omitempty"`
	RetainedEvents          string             `json:"retainedEvents"`
	RetentionSeconds        string             `json:"retentionSeconds"`
	PrunedRecordedThrough   *time.Time         `json:"prunedRecordedThrough,omitempty"`
	SuppressRecordedThrough *time.Time         `json:"suppressRecordedThrough,omitempty"`
	Checkpoint              *SourceObservation `json:"checkpoint,omitempty"`
}

type SourceObservation struct {
	AsOf                time.Time  `json:"asOf"`
	UnpublishedEvents   string     `json:"unpublishedEvents"`
	OldestUnpublishedAt *time.Time `json:"oldestUnpublishedAt,omitempty"`
}

type Backlog struct {
	Pending  string     `json:"pending"`
	OldestAt *time.Time `json:"oldestAt,omitempty"`
}

// Due excludes a currently leased row. OldestAt is insertion time except for
// activation work, whose existing schema records the current rule update time;
// that lower-bound age proxy is documented and named separately in metrics.
type QueueHealth struct {
	Backlog
	Due    string `json:"due"`
	Leased string `json:"leased"`
}

type HoldHealth struct {
	Held                   bool       `json:"held"`
	Generation             string     `json:"generation"`
	RestoreRecordedThrough *time.Time `json:"restoreRecordedThrough,omitempty"`
	UpdatedAt              time.Time  `json:"updatedAt"`
}

// Provider health aggregates the shared database cooldown across all provider
// identities. It neither probes APNs nor reports successful phone presentation.
type ProviderHealth struct {
	RecordedFailures string     `json:"recordedFailures"`
	BackingOff       string     `json:"backingOff"`
	LastFailureAt    *time.Time `json:"lastFailureAt,omitempty"`
	RetryNotBefore   *time.Time `json:"retryNotBefore,omitempty"`
}

func CanonicalStatusDeployments(ids []string) ([]string, error) {
	if len(ids) > MaximumStatusDeployments {
		return nil, ErrStatusInvalid
	}
	out := append([]string{}, ids...)
	slices.Sort(out)
	for i, id := range out {
		if !identityUUID(id) || i > 0 && out[i-1] == id {
			return nil, ErrStatusInvalid
		}
	}
	return out, nil
}

func statusText(s string) bool {
	if len(s) > 200 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func statusCount(s string) bool {
	if len(s) == 0 || len(s) > 40 || len(s) > 1 && s[0] == '0' {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func countLE(a, b string) bool             { return len(a) < len(b) || len(a) == len(b) && a <= b }
func statusTime(t time.Time) bool          { return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 }
func optionalStatusTime(t *time.Time) bool { return t == nil || statusTime(*t) }
func validBacklog(b Backlog) bool {
	return statusCount(b.Pending) && optionalStatusTime(b.OldestAt) && (b.Pending == "0") == (b.OldestAt == nil)
}
func validQueue(q QueueHealth) bool {
	if !validBacklog(q.Backlog) || !statusCount(q.Due) || !statusCount(q.Leased) {
		return false
	}
	// These database counts are bounded by queue quotas and fit in uint64.
	p, e1 := strconv.ParseUint(q.Pending, 10, 64)
	d, e2 := strconv.ParseUint(q.Due, 10, 64)
	l, e3 := strconv.ParseUint(q.Leased, 10, 64)
	return e1 == nil && e2 == nil && e3 == nil && d <= p && l <= p-d
}
func validFeedError(s string) bool {
	return slices.Contains([]string{"", "source_unavailable", "event_cursor_expired", "source_recovery_changed", "event_cursor_scope_changed", "invalid_cursor", "event_conflict", "capacity"}, s)
}

func (s HealthSnapshot) Validate() error {
	if !statusTime(s.ObservedAt) || len(s.Sources) > MaximumStatusDeployments || !validQueue(s.Activation) || !statusCount(s.Hold.Generation) || s.Hold.Generation == "0" || !statusTime(s.Hold.UpdatedAt) || !optionalStatusTime(s.Hold.RestoreRecordedThrough) {
		return ErrStatusInvalid
	}
	p := s.Provider
	if !statusCount(p.RecordedFailures) || !statusCount(p.BackingOff) || !optionalStatusTime(p.LastFailureAt) || !optionalStatusTime(p.RetryNotBefore) || (p.RecordedFailures == "0") != (p.LastFailureAt == nil) || (p.BackingOff == "0") != (p.RetryNotBefore == nil) {
		return ErrStatusInvalid
	}
	for i, source := range s.Sources {
		if !identityUUID(source.DeploymentID) || i > 0 && s.Sources[i-1].DeploymentID >= source.DeploymentID || !statusText(source.DisplayName) {
			return ErrStatusInvalid
		}
		if source.State == "uninitialized" {
			if source.Feed != nil || source.Events != nil || source.Fanout != nil || source.Evaluation != nil || source.Delivery != nil {
				return ErrStatusInvalid
			}
			continue
		}
		if !slices.Contains([]string{"initializing", "active", "paused"}, source.State) || source.Feed == nil || source.Events == nil || source.Fanout == nil || source.Evaluation == nil || source.Delivery == nil || !validBacklog(*source.Events) || !validBacklog(*source.Fanout) || !countLE(source.Fanout.Pending, source.Events.Pending) || !validQueue(*source.Evaluation) || !validQueue(*source.Delivery) {
			return ErrStatusInvalid
		}
		f := source.Feed
		retention, err := strconv.ParseInt(f.RetentionSeconds, 10, 64)
		if !validFeedError(f.LastError) || !optionalStatusTime(f.LastSuccessAt) || !statusCount(f.RetainedEvents) || !statusCount(f.RetentionSeconds) || err != nil || retention < 86400 || retention > 31536000 || !optionalStatusTime(f.PrunedRecordedThrough) || !optionalStatusTime(f.SuppressRecordedThrough) || source.State == "active" && f.Checkpoint == nil {
			return ErrStatusInvalid
		}
		if c := f.Checkpoint; c != nil && (!statusTime(c.AsOf) || !validBacklog(Backlog{Pending: c.UnpublishedEvents, OldestAt: c.OldestUnpublishedAt})) {
			return ErrStatusInvalid
		}
	}
	return nil
}

// WriteStatus is the operator CLI boundary. It buffers a fully validated result
// before writing; repository failure never yields a partial success/zero snapshot.
// The caller must supply a restricted local output or restricted operator listener.
func WriteStatus(ctx context.Context, repo StatusRepository, deployments []Deployment, format string, dst io.Writer) error {
	if repo == nil || dst == nil || format != "json" && format != "prometheus" {
		return ErrStatusInvalid
	}
	ids := make([]string, len(deployments))
	names := make(map[string]string, len(deployments))
	for i, d := range deployments {
		if !statusText(d.Name) {
			return ErrStatusInvalid
		}
		ids[i], names[d.ID] = d.ID, d.Name
	}
	ids, err := CanonicalStatusDeployments(ids)
	if err != nil {
		return err
	}
	snapshot, err := repo.OperationalStatus(ctx, ids)
	if err != nil {
		return ErrStatusUnavailable
	}
	if len(snapshot.Sources) != len(ids) || snapshot.Validate() != nil {
		return ErrStatusUnavailable
	}
	for i := range ids {
		if snapshot.Sources[i].DeploymentID != ids[i] {
			return ErrStatusUnavailable
		}
		snapshot.Sources[i].DisplayName = names[ids[i]]
	}
	var out bytes.Buffer
	if format == "json" {
		encoder := json.NewEncoder(&out)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(snapshot)
	} else {
		err = writePrometheus(&out, snapshot)
	}
	if err != nil || out.Len() > 262144 || ctx.Err() != nil {
		return ErrStatusUnavailable
	}
	_, err = io.Copy(dst, &out)
	return err
}

func quoteMetric(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(s) + `"`
}
