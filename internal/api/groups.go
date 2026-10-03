package api

import "time"

// Workload is a source wrapper summary, never an additional job count.
type Workload struct {
	Scope
	ID                string            `json:"id"`
	Name              string            `json:"name,omitempty"`
	Kind              string            `json:"kind"`
	Revision          string            `json:"revision"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt,omitempty"`
	AsOf              time.Time         `json:"asOf"`
	TotalChildren     string            `json:"totalChildren"`
	Counts            map[string]string `json:"counts"`
	Concurrency       string            `json:"concurrency,omitempty"`
	FailurePolicy     string            `json:"failurePolicy,omitempty"`
	UnsatisfiedPolicy string            `json:"unsatisfiedPolicy,omitempty"`
	Phase             string            `json:"phase,omitempty"`
	Outcome           string            `json:"outcome,omitempty"`
	ArrayPolicy       string            `json:"arrayPolicy,omitempty"`
	ArrayMode         string            `json:"arrayMode,omitempty"`
	ArrayID           string            `json:"arrayId,omitempty"`
}
type WorkloadTotal struct {
	Scope
	Total string    `json:"total"`
	AsOf  time.Time `json:"asOf"`
}
type WorkloadPage struct {
	Page[Workload]
	Total  string          `json:"total"`
	Totals []WorkloadTotal `json:"totals"`
}
type WorkloadChild struct {
	ID               string            `json:"id"`
	Name             string            `json:"name,omitempty"`
	Index            string            `json:"index"`
	Job              Job               `json:"job"`
	Disposition      string            `json:"disposition,omitempty"`
	TaskIndex        string            `json:"taskIndex,omitempty"`
	DependencyCounts map[string]string `json:"dependencyCounts,omitempty"`
}
type WorkloadChildrenPage struct {
	Page[WorkloadChild]
	Total string `json:"total"`
}
type WorkloadDetail struct {
	Workload     Workload        `json:"workload"`
	Children     []WorkloadChild `json:"children"`
	Total        string          `json:"total"`
	NextCursor   string          `json:"nextCursor,omitempty"`
	Completeness string          `json:"completeness"`
	Sources      []SourceStatus  `json:"sources"`
	FetchedAt    time.Time       `json:"fetchedAt"`
}
type GraphEdge struct {
	From            string   `json:"from"`
	To              string   `json:"to"`
	FromJobID       string   `json:"fromJobId"`
	ToJobID         string   `json:"toJobId"`
	Predicate       string   `json:"predicate"`
	Outcomes        []string `json:"outcomes"`
	UpstreamPhase   string   `json:"upstreamPhase"`
	UpstreamOutcome string   `json:"upstreamOutcome,omitempty"`
	State           string   `json:"state"`
}
type GraphEdgePage struct {
	Page[GraphEdge]
	Total string `json:"total"`
}
type GraphNeighborhood struct {
	CenterID     string          `json:"centerId"`
	Nodes        []WorkloadChild `json:"nodes"`
	Edges        []GraphEdge     `json:"edges"`
	TotalNodes   string          `json:"totalNodes"`
	TotalEdges   string          `json:"totalEdges"`
	OmittedNodes string          `json:"omittedNodes"`
	OmittedEdges string          `json:"omittedEdges"`
	Completeness string          `json:"completeness"`
	Sources      []SourceStatus  `json:"sources"`
	FetchedAt    time.Time       `json:"fetchedAt"`
}
