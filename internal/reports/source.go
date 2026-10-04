package reports

import (
	"context"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman/diagnostic"
)

// Snapshot combines the public core facts with Control's separately verified
// recovery epoch. Both are obtained from the same authorized source response.
type Snapshot struct {
	Value         diagnostic.SharedSnapshot
	RecoveryEpoch string
}

type Source interface {
	ID() string
	Discover(context.Context, monitoring.Actor) (monitoring.Discovery, error)
	DiagnosticSnapshot(context.Context, monitoring.Actor, diagnostic.SharedSelection) (Snapshot, error)
}
