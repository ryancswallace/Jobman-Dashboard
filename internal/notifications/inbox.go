package notifications

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

const MaximumInboxPage = 50
const InboxCursorLifetime = 10 * time.Minute

// Inbox history preserves the matching intent, not its current enabled state.
// Other namespaces, activation identities and represented principals stay private.
type InboxMatch struct {
	RuleID      string   `json:"ruleId"`
	Revision    string   `json:"revision"`
	Name        string   `json:"name"`
	Scope       string   `json:"scope"`
	OutcomeMode string   `json:"outcomeMode"`
	Outcomes    []string `json:"outcomes"`
}
type InboxDelivery struct {
	Total   string            `json:"total"`
	ByState map[string]string `json:"byState"`
}
type InboxItem struct {
	ID                  string        `json:"id"`
	Job                 JobRef        `json:"job"`
	ControlInstanceID   string        `json:"controlInstanceId"`
	EventID             string        `json:"eventId"`
	Outcome             string        `json:"outcome"`
	EventAt             time.Time     `json:"eventAt"`
	ObservedCompletedAt *time.Time    `json:"observedCompletedAt,omitempty"`
	CreatedAt           time.Time     `json:"createdAt"`
	ExpiresAt           time.Time     `json:"expiresAt"`
	Read                bool          `json:"read"`
	ReadAt              *time.Time    `json:"readAt,omitempty"`
	MatchedRules        []InboxMatch  `json:"matchedRules"`
	JobName             string        `json:"jobName,omitempty"`
	JobAvailability     string        `json:"jobAvailability"`
	Delivery            InboxDelivery `json:"delivery"`
}
type InboxPage struct {
	Items              []InboxItem `json:"items"`
	NextCursor         string      `json:"nextCursor,omitempty"`
	UnreadCount        string      `json:"unreadCount"`
	Completeness       string      `json:"completeness"`
	UnavailableSources int         `json:"unavailableSources"`
	InaccessibleScopes int         `json:"inaccessibleScopes"`
	FetchedAt          time.Time   `json:"fetchedAt"`
}
type InboxQuery struct {
	// Nil selects all currently authorized namespaces; an explicit empty list
	// selects none. Canonicalization preserves this security distinction.
	Scopes []NamespaceRef `json:"scopes"`
	Unread bool           `json:"unread"`
	Limit  int            `json:"limit"`
	Cursor string         `json:"-"`
}

func (q InboxQuery) Canonical() (InboxQuery, error) {
	if q.Limit < 1 || q.Limit > MaximumInboxPage || len(q.Scopes) > MaximumNamespaces || len(q.Cursor) > 128 || strings.ContainsAny(q.Cursor, "\r\n\t ") {
		return q, ErrInvalid
	}
	if q.Scopes != nil {
		q.Scopes = slices.Clone(q.Scopes)
		slices.SortFunc(q.Scopes, func(a, b NamespaceRef) int {
			if a.DeploymentID != b.DeploymentID {
				return strings.Compare(a.DeploymentID, b.DeploymentID)
			}
			return strings.Compare(a.NamespaceID, b.NamespaceID)
		})
		for i, r := range q.Scopes {
			if !validNamespace(r) || i > 0 && r == q.Scopes[i-1] {
				return q, ErrInvalid
			}
		}
	}
	return q, nil
}

// All persistence records are private; the service must authorize and project.
type InboxRecord struct {
	ID, AccountID        string
	Event                events.Event
	CreatedAt, ExpiresAt time.Time
	ReadAt               *time.Time
	Matches              []InboxMatch
	Delivery             InboxDelivery
}
type InboxPosition struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}
type InboxSelection struct {
	Authorities []AuthorizationProof
	SnapshotAt  time.Time
	After       *InboxPosition
	Unread      bool
	Limit       int
}
type InboxRecords struct {
	Items                 []InboxRecord
	More                  bool
	SnapshotAt, FetchedAt time.Time
	UnreadCount           string
}

// The final validation is a local transactional account/alias/proof-expiry
// fence after all represented-user network reads have completed.
type InboxRepository interface {
	ListNotificationInbox(context.Context, monitoring.Actor, InboxSelection) (InboxRecords, error)
	NotificationInbox(context.Context, monitoring.Actor, string) (InboxRecord, error)
	SetNotificationInboxRead(context.Context, monitoring.Actor, string, bool, AuthorizationProof) (InboxRecord, error)
	ValidateNotificationInboxOwner(context.Context, monitoring.Actor, []AuthorizationProof) error
}

func (m InboxMatch) Validate() error {
	if !uuid(m.RuleID) || !positiveDecimal(m.Revision) || len(m.Name) < 1 || len(m.Name) > MaximumNameBytes || !utf8.ValidString(m.Name) || strings.ContainsFunc(m.Name, unicode.IsControl) || !slices.Contains([]string{ScopeWatchedJobs, ScopeMyJobs, ScopeNamespaceJobs}, m.Scope) || m.Outcomes == nil || len(m.Outcomes) > 6 {
		return ErrInvalid
	}
	if m.OutcomeMode == OutcomeAllTerminal {
		if len(m.Outcomes) != 0 {
			return ErrInvalid
		}
		return nil
	}
	if m.OutcomeMode != OutcomeSelected || len(m.Outcomes) < 1 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, outcome := range m.Outcomes {
		if seen[outcome] || !slices.Contains([]string{"success", "failure", "cancelled", "timed_out", "aborted", "lost"}, outcome) {
			return ErrInvalid
		}
		seen[outcome] = true
	}
	return nil
}

func (i InboxItem) Validate() error {
	if !uuid(i.ID) || !uuid(i.EventID) || !uuid(i.ControlInstanceID) || !validNamespace(i.Job.Namespace()) || !uuid(i.Job.JobID) || len(i.Outcome) < 1 || len(i.Outcome) > 64 || !validTime(i.EventAt) || !validTime(i.CreatedAt) || !i.ExpiresAt.After(i.CreatedAt) || i.Read != (i.ReadAt != nil) || i.ReadAt != nil && !validTime(*i.ReadAt) || i.ObservedCompletedAt != nil && !validTime(*i.ObservedCompletedAt) || !slices.Contains([]string{"available", "missing", "unavailable"}, i.JobAvailability) || len(i.JobName) > 1024 || !utf8.ValidString(i.JobName) || i.JobName != "" && i.JobAvailability != "available" || len(i.MatchedRules) < 1 || len(i.MatchedRules) > MaximumRulesPerAccount {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, m := range i.MatchedRules {
		if m.Validate() != nil || seen[m.RuleID] {
			return ErrInvalid
		}
		seen[m.RuleID] = true
	}
	total, err := strconv.ParseInt(i.Delivery.Total, 10, 64)
	if err != nil || total < 0 || total > MaximumBoundDevices || strconv.FormatInt(total, 10) != i.Delivery.Total || i.Delivery.ByState == nil {
		return ErrInvalid
	}
	var sum int64
	for state, count := range i.Delivery.ByState {
		n, e := strconv.ParseInt(count, 10, 64)
		if e != nil || n < 1 || n > MaximumBoundDevices || strconv.FormatInt(n, 10) != count || len(state) < 1 || len(state) > 32 || strings.ContainsFunc(state, unicode.IsControl) {
			return ErrInvalid
		}
		sum += n
	}
	if sum != total {
		return ErrInvalid
	}
	return nil
}
func (p InboxPage) Validate() error {
	if p.Items == nil || len(p.Items) > MaximumInboxPage || !validTime(p.FetchedAt) || p.UnavailableSources < 0 || p.UnavailableSources > MaximumSources || p.InaccessibleScopes < 0 || p.InaccessibleScopes > MaximumNamespaces || p.Completeness != "complete" && p.Completeness != "partial" || len(p.NextCursor) > 128 || strings.ContainsFunc(p.NextCursor, unicode.IsControl) {
		return ErrInvalid
	}
	n, err := strconv.ParseInt(p.UnreadCount, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != p.UnreadCount {
		return ErrInvalid
	}
	if (p.UnavailableSources > 0 || p.InaccessibleScopes > 0) && p.Completeness != "partial" {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for j, i := range p.Items {
		if i.Validate() != nil || seen[i.ID] || i.JobAvailability == "unavailable" && p.Completeness != "partial" || j > 0 && (i.CreatedAt.After(p.Items[j-1].CreatedAt) || i.CreatedAt.Equal(p.Items[j-1].CreatedAt) && i.ID >= p.Items[j-1].ID) {
			return ErrInvalid
		}
		seen[i.ID] = true
	}
	return nil
}
