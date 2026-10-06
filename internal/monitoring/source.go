// Package monitoring composes independently authorized Control reads. It never
// establishes permission from a cursor, cached row, or user-supplied scope.
package monitoring

import (
	"context"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"time"
)

// Actor is constructed only by the authentication boundary, never request JSON.
type Actor struct {
	Account     api.Account
	Issuer      string
	Subject     string
	DirectoryID string
	CSRFToken   string
}

type Discovery struct {
	Deployment    api.Deployment
	InstanceID    string
	RecoveryEpoch string
	ServiceTime   time.Time
	// PrincipalID is private verified per-Control identity for owner matching.
	// It is not a Dashboard account ID or a client-supplied identity claim.
	PrincipalID string `json:"-"`
}

type Query struct {
	Scopes        []api.Scope `json:"scopes"`
	Limit         int         `json:"limit"`
	Phase         string      `json:"phase,omitempty"`
	Outcome       string      `json:"outcome,omitempty"`
	Owner         string      `json:"owner,omitempty"`
	JobID         string      `json:"jobId,omitempty"`
	Attention     bool        `json:"attention,omitempty"`
	WindowHours   int         `json:"windowHours,omitempty"`
	CompletedFrom *time.Time  `json:"completedFrom,omitempty"`
	CompletedTo   *time.Time  `json:"completedTo,omitempty"`
}

type SourceQuery struct {
	Query
	NamespaceID   string
	CreatedBefore time.Time
	Cursor        string
}

type JobPage struct {
	Items      []api.Job
	NextCursor string
	AsOf       time.Time
}

type Counts struct {
	Active                int64
	AwaitingExecution     int64
	Running               int64
	EvidenceAttention     int64
	MissingCompletionTime int64
	Terminal              map[string]int64
	AsOf                  time.Time
}

// Every implementation must perform source authorization on each operation,
// including after reading bytes. Discovery is not an authorization cache.
type Source interface {
	ID() string
	Discover(context.Context, Actor) (Discovery, error)
	Jobs(context.Context, Actor, SourceQuery) (JobPage, error)
	Job(context.Context, Actor, api.Scope, string) (api.Job, error)
	Summary(context.Context, Actor, api.Scope, api.Window) (Counts, error)
}

// JobDetailSource adds detail-only sensitive execution metadata without putting
// command contents in the ordinary Job model or paginated cursor snapshots.
type JobDetailSource interface {
	JobDetail(context.Context, Actor, api.Scope, string) (api.JobDetail, error)
}

func failure(code, message string) error { return &api.Error{Code: code, Message: message} }

var (
	ErrCursor    = failure("cursor_expired", "This browse session expired or its access changed. Refresh the list.")
	ErrForbidden = failure("forbidden", "You do not currently have permission to view this information.")
	ErrAuthority = failure("authorization_unavailable", "Your access could not be checked with Jobman Control. Try again shortly.")
	ErrSource    = failure("source_unavailable", "Monitoring data is unavailable. Check your private network or VPN connection. If the problem continues, contact your administrator.")
	ErrNotFound  = failure("not_found_or_inaccessible", "This item could not be found, or you do not have access to it.")
)
