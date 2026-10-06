package monitoring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

type detailTestSource struct {
	*testSource
	revoke      bool
	wrongSource bool
}

func (s *detailTestSource) JobDetail(ctx context.Context, a Actor, scope api.Scope, id string) (api.JobDetail, error) {
	job, err := s.Job(ctx, a, scope, id)
	if err != nil {
		return api.JobDetail{}, err
	}
	if s.revoke {
		s.mu.Lock()
		s.revoked = true
		s.mu.Unlock()
	}
	if s.wrongSource {
		job.DeploymentID = "other"
	}
	return api.JobDetail{Job: job, Execution: &api.JobExecution{Command: api.JobCommand{Executable: "synthetic-private-command", Args: []string{}}, WorkingDirectory: "/synthetic"}}, nil
}
func TestDetailExecutionUsesCurrentSourceAuthorization(t *testing.T) {
	for _, mode := range []string{"allowed", "revoked", "wrong-source", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			base := source("a", 1, time.Minute)
			e := engine(t, base)
			if mode != "legacy" {
				e.sources["a"] = &detailTestSource{testSource: base, revoke: mode == "revoked", wrongSource: mode == "wrong-source"}
			}
			result, err := e.Job(context.Background(), actor("one"), api.Scope{DeploymentID: "a", NamespaceID: "ns"}, "job-000")
			switch mode {
			case "allowed":
				if err != nil || result.Execution == nil || result.Execution.Command.Executable != "synthetic-private-command" || !result.FetchedAt.Equal(clock) {
					t.Fatal("authorized detail was not preserved")
				}
			case "legacy":
				if err != nil || result.Execution != nil || result.ExecutionUnavailableReason != "unsupported" {
					t.Fatal("legacy source did not retain useful job metadata")
				}
			case "revoked":
				if !errors.Is(err, ErrForbidden) || result.Execution != nil || result.Job.ID != "" {
					t.Fatal("revoked job command escaped")
				}
			case "wrong-source":
				if !errors.Is(err, ErrNotFound) || result.Execution != nil || result.Job.ID != "" {
					t.Fatal("wrong-source job command escaped")
				}
			}
		})
	}
}
