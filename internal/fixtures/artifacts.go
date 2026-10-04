package fixtures

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func (s *Source) Artifacts(ctx context.Context, a monitoring.Actor, q monitoring.ArtifactQuery) (monitoring.ArtifactSourcePage, error) {
	job, err := s.Job(ctx, a, q.Scope, q.JobID)
	if err != nil {
		return monitoring.ArtifactSourcePage{}, err
	}
	out := monitoring.ArtifactSourcePage{Items: []api.Artifact{}, Total: "0", AsOf: s.Now()}
	if q.RunNumber != "" && q.RunNumber != "1" {
		return out, nil
	}
	start := 0
	if q.Cursor != "" {
		start, err = strconv.Atoi(q.Cursor)
		if err != nil || start < 0 || start >= 55 {
			return out, monitoring.ErrCursor
		}
	}
	end := min(start+q.Limit, 55)
	for i := start; i < end; i++ {
		name := fmt.Sprintf("synthetic-result-%03d", i)
		out.Items = append(out.Items, api.Artifact{ID: job.ID + "/" + name, Name: name, RunID: job.ID, RunNumber: "1", ExecutionID: job.ID, TargetGenerationID: s.ID(), SizeBytes: "120", Checksum: "sha256:" + strings.Repeat("0", 64), PublishedAt: job.CreatedAt, Availability: "metadata_only"})
	}
	out.Total = "55"
	if end < 55 {
		out.NextCursor = strconv.Itoa(end)
	}
	return out, nil
}
