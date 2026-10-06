package monitoring

import (
	"context"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"time"
)

type TargetQuery struct {
	Scopes []api.Scope `json:"scopes"`
	Limit  int         `json:"limit"`
}
type TargetSourceQuery struct {
	NamespaceID   string
	Limit         int
	CreatedBefore time.Time
	Cursor        string
}
type TargetSourcePage struct {
	Items      []api.Target
	Total      string
	NextCursor string
	AsOf       time.Time
	// CreatedBefore is the effective source watermark. Zero means the source
	// used the requested cutoff (the original internal source contract).
	CreatedBefore time.Time
}
type TargetPartitionQuery struct {
	Scope        api.Scope `json:"scope"`
	TargetID     string    `json:"targetId"`
	GenerationID string    `json:"generationId"`
	Limit        int       `json:"limit"`
	Cursor       string    `json:"-"`
}
type TargetPartitionSourcePage struct {
	Items      []api.TargetPartition
	Total      string
	NextCursor string
	AsOf       time.Time
}
type TargetSource interface {
	Targets(context.Context, Actor, TargetSourceQuery) (TargetSourcePage, error)
	Target(context.Context, Actor, api.Scope, string) (api.Target, error)
	TargetPartitions(context.Context, Actor, TargetPartitionQuery) (TargetPartitionSourcePage, error)
}

var ErrTargetChanged = failure("target_changed", "The target generation changed. Refresh the target before browsing partitions.")
var errTargetsUnsupported = failure("unsupported_contract", "This source does not support bounded target monitoring.")
