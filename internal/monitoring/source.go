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
}

type Discovery struct {
	Deployment    api.Deployment
	InstanceID    string
	RecoveryEpoch string
	ServiceTime   time.Time
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

func failure(code, message string) error { return &api.Error{Code: code, Message: message} }

var (
	ErrCursor    = failure("cursor_expired", "This browse session expired or its access changed. Refresh the list.")
	ErrForbidden = failure("forbidden", "This scope is not currently authorized.")
	ErrAuthority = failure("authorization_unavailable", "Current source authorization could not be verified.")
	ErrSource    = failure("source_unavailable", "The source is unavailable. Retry when private connectivity is restored.")
	ErrNotFound  = failure("not_found_or_inaccessible", "The resource is absent or inaccessible.")
)
