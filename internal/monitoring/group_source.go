package monitoring

import (
	"context"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

// GroupSource is optional. A missing implementation is an unsupported source,
// never a successful empty catalog. Each method reauthorizes groups.read.
type GroupSource interface {
	Workloads(context.Context, Actor, GroupSourceQuery) (WorkloadSourcePage, error)
	Workload(context.Context, Actor, api.Scope, string, string) (api.Workload, error)
	WorkloadChildren(context.Context, Actor, api.Scope, string, string, int, int) (WorkloadChildSourcePage, error)
	GraphEdges(context.Context, Actor, api.Scope, string, GraphEdgeQuery) (GraphEdgeSourcePage, error)
	GraphNeighborhood(context.Context, Actor, api.Scope, string, string, int, int) (GraphNeighborhoodSource, error)
}
type GroupQuery struct {
	Scopes []api.Scope `json:"scopes"`
	Kind   string      `json:"kind"`
	Limit  int         `json:"limit"`
}
type GroupSourceQuery struct {
	GroupQuery
	NamespaceID   string
	CreatedBefore time.Time
	Cursor        string
}
type WorkloadSourcePage struct {
	Items      []api.Workload
	Total      string
	NextCursor string
	AsOf       time.Time
}
type WorkloadChildSourcePage struct {
	Items     []api.WorkloadChild
	Total     string
	NextIndex *int
	AsOf      time.Time
}
type GraphEdgeQuery struct {
	Limit     int    `json:"limit"`
	NodeID    string `json:"nodeId,omitempty"`
	Direction string `json:"direction,omitempty"`
	Cursor    string `json:"-"`
}
type GraphEdgeSourcePage struct {
	Items      []api.GraphEdge
	Total      string
	NextCursor string
	AsOf       time.Time
}
type GraphNeighborhoodSource struct {
	api.GraphNeighborhood
	AsOf time.Time
}

func ValidGroupKind(kind string) bool {
	return kind == "collection" || kind == "array" || kind == "graph"
}
