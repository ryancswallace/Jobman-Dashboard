package fixtures

import (
	"context"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func (s *Source) Runs(ctx context.Context, a monitoring.Actor, q monitoring.RunQuery) (monitoring.RunSourcePage, error) {
	job, err := s.Job(ctx, a, q.Scope, q.JobID)
	if err != nil {
		return monitoring.RunSourcePage{}, err
	}
	if q.Cursor != "" {
		return monitoring.RunSourcePage{}, monitoring.ErrCursor
	}
	run := api.JobRun{ID: job.ID, Number: "1", Phase: job.Phase, DesiredState: job.DesiredState, Outcome: job.Outcome, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, ExecutionID: job.ID, ExecutionPhase: job.Phase, TargetID: targetFixtureID, TargetGenerationID: s.ID(), Backend: "slurm", Confidence: job.Confidence}
	return monitoring.RunSourcePage{Items: []api.JobRun{run}, Total: "1", AsOf: s.Now()}, nil
}
func (s *Source) Run(ctx context.Context, a monitoring.Actor, scope api.Scope, jobID, runID string) (monitoring.RunSourceDetail, error) {
	page, err := s.Runs(ctx, a, monitoring.RunQuery{Scope: scope, JobID: jobID, Limit: 1})
	if err != nil {
		return monitoring.RunSourceDetail{}, err
	}
	if runID != jobID {
		return monitoring.RunSourceDetail{}, monitoring.ErrNotFound
	}
	return monitoring.RunSourceDetail{Run: page.Items[0], AsOf: page.AsOf}, nil
}
