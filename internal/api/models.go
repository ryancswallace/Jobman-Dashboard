// Package api contains Dashboard's wire DTOs. Source enums deliberately remain
// strings: additive source states must stay inspectable rather than crash clients.
package api

import "time"

const Version = "jobman.dashboard/v1"

type Account struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type Scope struct {
	DeploymentID string `json:"deploymentId"`
	NamespaceID  string `json:"namespaceId"`
}

type Namespace struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Roles                  []string  `json:"roles"`
	Capabilities           []string  `json:"capabilities"`
	AuthorizationVersion   string    `json:"authorizationVersion"`
	AuthorizationCheckedAt time.Time `json:"authorizationCheckedAt"`
	AuthorizationExpiresAt time.Time `json:"authorizationExpiresAt"`
}

type Deployment struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Status     string      `json:"status"`
	Namespaces []Namespace `json:"namespaces"`
}

type Preferences struct {
	Revision       string `json:"revision"`
	Timezone       string `json:"timezone"`
	Appearance     string `json:"appearance"`
	RefreshSeconds int    `json:"refreshSeconds"`
}

type Limits struct {
	DefaultPageSize int `json:"defaultPageSize"`
	MaxPageSize     int `json:"maxPageSize"`
	LogReadBytes    int `json:"logReadBytes"`
	LogBufferBytes  int `json:"logBufferBytes"`
	GraphNodes      int `json:"graphNodes"`
	GraphEdges      int `json:"graphEdges"`
}

func DefaultLimits() Limits           { return Limits{50, 200, 262144, 2097152, 200, 500} }
func DefaultPreferences() Preferences { return Preferences{"1", "UTC", "system", 5} }

type Bootstrap struct {
	APIVersion   string       `json:"apiVersion"`
	Account      Account      `json:"account"`
	Deployments  []Deployment `json:"deployments"`
	Preferences  Preferences  `json:"preferences"`
	Limits       Limits       `json:"limits"`
	CSRFToken    string       `json:"csrfToken,omitempty"`
	FixtureMode  bool         `json:"fixtureMode,omitempty"`
	Completeness string       `json:"completeness"`
}

type Owner struct {
	ID            string `json:"id"`
	DisplayName   string `json:"displayName,omitempty"`
	IsCurrentUser bool   `json:"isCurrentUser"`
}

type Scheduler struct {
	State      string     `json:"state,omitempty"`
	JobID      string     `json:"jobId,omitempty"`
	ObservedAt *time.Time `json:"observedAt,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	Cluster    string     `json:"cluster,omitempty"`
}

type Lifecycle struct {
	StartedRecordedAt   *time.Time `json:"startedRecordedAt,omitempty"`
	StartedProvenance   string     `json:"startedProvenance,omitempty"`
	CompletedRecordedAt *time.Time `json:"completedRecordedAt,omitempty"`
	CompletedProvenance string     `json:"completedProvenance,omitempty"`
}
type RunReference struct {
	ID          string `json:"id"`
	Number      string `json:"number"`
	ExecutionID string `json:"executionId,omitempty"`
}
type GroupReference struct {
	CollectionID    string `json:"collectionId,omitempty"`
	CollectionIndex *int   `json:"collectionIndex,omitempty"`
	GraphID         string `json:"graphId,omitempty"`
	GraphIndex      *int   `json:"graphIndex,omitempty"`
}

type Job struct {
	Scope
	ID                 string            `json:"id"`
	Name               string            `json:"name,omitempty"`
	TargetID           string            `json:"targetId"`
	TargetGeneration   string            `json:"targetGeneration,omitempty"`
	TargetGenerationID string            `json:"targetGenerationId,omitempty"`
	Backend            string            `json:"backend,omitempty"`
	Revision           string            `json:"revision"`
	Owner              *Owner            `json:"owner,omitempty"`
	CreatedAt          time.Time         `json:"createdAt"`
	UpdatedAt          time.Time         `json:"updatedAt"`
	StartedAt          *time.Time        `json:"startedAt,omitempty"`
	CompletedAt        *time.Time        `json:"completedAt,omitempty"`
	DesiredState       string            `json:"desiredState"`
	Phase              string            `json:"phase"`
	Outcome            string            `json:"outcome,omitempty"`
	Confidence         string            `json:"confidence"`
	Labels             map[string]string `json:"labels"`
	Scheduler          *Scheduler        `json:"scheduler,omitempty"`
	Disposition        string            `json:"disposition,omitempty"`
	Imported           bool              `json:"imported"`
	Lifecycle          *Lifecycle        `json:"lifecycle,omitempty"`
	CurrentRun         *RunReference     `json:"currentRun,omitempty"`
	Group              *GroupReference   `json:"group,omitempty"`
}

type SourceStatus struct {
	Scope
	Status    string     `json:"status"`
	AsOf      *time.Time `json:"asOf,omitempty"`
	FetchedAt time.Time  `json:"fetchedAt"`
	Message   string     `json:"message,omitempty"`
}

type Page[T any] struct {
	Items        []T            `json:"items"`
	NextCursor   string         `json:"nextCursor,omitempty"`
	Completeness string         `json:"completeness"`
	Sources      []SourceStatus `json:"sources"`
	FetchedAt    time.Time      `json:"fetchedAt"`
}

type JobDetail struct {
	Job       Job       `json:"job"`
	FetchedAt time.Time `json:"fetchedAt"`
}

type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Null count means no authoritative contribution was available. A partial
// response's integer is the sum of successful sources, never a failed-source zero.
type Overview struct {
	Active                *int64            `json:"active"`
	AwaitingExecution     *int64            `json:"awaitingExecution"`
	Running               *int64            `json:"running"`
	EvidenceAttention     *int64            `json:"evidenceAttention"`
	MissingCompletionTime *int64            `json:"missingCompletionTime"`
	Terminal              map[string]*int64 `json:"terminal"`
	Window                Window            `json:"window"`
	Completeness          string            `json:"completeness"`
	Sources               []SourceStatus    `json:"sources"`
	FetchedAt             time.Time         `json:"fetchedAt"`
}

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
