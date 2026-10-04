package fixtures

import (
	"context"
	"fmt"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"strconv"
	"time"
)

const targetFixtureID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

func (s *Source) fixtureTarget() api.Target {
	now := s.Now()
	partitions := make([]api.TargetPartition, 200)
	for i := range partitions {
		partitions[i] = api.TargetPartition{Name: fmt.Sprintf("partition-%03d", i), IsDefault: i == 0}
	}
	return api.Target{Scope: api.Scope{DeploymentID: s.ID(), NamespaceID: NamespaceID}, TargetID: targetFixtureID, Name: "synthetic-slurm", Kind: "slurm", State: "active", Revision: "4", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: now, AsOf: now, Generation: api.TargetGeneration{ID: s.ID(), Number: "9007199254740993", ExecutionBackend: "slurm", Transport: "agent-api", Runtimes: []string{"native", "container"}, OperatingSystems: []string{"linux"}, Architectures: []string{"x86_64"}, Capabilities: []string{"arrays", "collections"}, Partitions: partitions, PartitionCount: "205", PartitionsTruncated: true, LogStore: &api.TargetStore{Name: "synthetic-logs", Version: "1"}, ArtifactStores: []api.TargetStore{}, Provider: api.TargetProvider{Kind: "on-prem"}}}
}
func (s *Source) Targets(ctx context.Context, a monitoring.Actor, q monitoring.TargetSourceQuery) (monitoring.TargetSourcePage, error) {
	if _, err := s.Discover(ctx, a); err != nil {
		return monitoring.TargetSourcePage{}, err
	}
	if q.NamespaceID != NamespaceID {
		return monitoring.TargetSourcePage{}, monitoring.ErrForbidden
	}
	items := []api.Target{}
	t := s.fixtureTarget()
	if !t.CreatedAt.After(q.CreatedBefore) {
		items = append(items, t)
	}
	return monitoring.TargetSourcePage{Items: items, Total: strconv.Itoa(len(items)), AsOf: s.Now()}, nil
}
func (s *Source) Target(ctx context.Context, a monitoring.Actor, scope api.Scope, id string) (api.Target, error) {
	if _, err := s.Discover(ctx, a); err != nil {
		return api.Target{}, err
	}
	if scope.DeploymentID != s.ID() || scope.NamespaceID != NamespaceID || id != targetFixtureID {
		return api.Target{}, monitoring.ErrNotFound
	}
	return s.fixtureTarget(), nil
}
func (s *Source) TargetPartitions(ctx context.Context, a monitoring.Actor, q monitoring.TargetPartitionQuery) (monitoring.TargetPartitionSourcePage, error) {
	t, err := s.Target(ctx, a, q.Scope, q.TargetID)
	if err != nil {
		return monitoring.TargetPartitionSourcePage{}, err
	}
	if t.Generation.ID != q.GenerationID {
		return monitoring.TargetPartitionSourcePage{}, monitoring.ErrTargetChanged
	}
	start := 0
	if q.Cursor != "" {
		start, err = strconv.Atoi(q.Cursor)
		if err != nil || start < 0 || start >= 205 {
			return monitoring.TargetPartitionSourcePage{}, monitoring.ErrCursor
		}
	}
	end := min(start+q.Limit, 205)
	out := monitoring.TargetPartitionSourcePage{Items: []api.TargetPartition{}, Total: "205", AsOf: s.Now()}
	for i := start; i < end; i++ {
		out.Items = append(out.Items, api.TargetPartition{Name: fmt.Sprintf("partition-%03d", i), IsDefault: i == 0})
	}
	if end < 205 {
		out.NextCursor = strconv.Itoa(end)
	}
	return out, nil
}
