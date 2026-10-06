// Package fixtures supplies deterministic synthetic Controls only for explicit
// loopback development mode. It is never an automatic integration fallback.
package fixtures

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type Source struct {
	DeploymentID string
	Name         string
	Now          func() time.Time
	rows         []api.Job
}

const NamespaceID = "33333333-3333-4333-8333-333333333333"
const AccountID = "44444444-4444-4444-8444-444444444444"

func Sources(now time.Time) []monitoring.Source {
	result := []monitoring.Source{}
	for i, id := range []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"} {
		s := &Source{DeploymentID: id, Name: []string{"Lab East", "Lab West"}[i], Now: time.Now}
		for n := 0; n < 120; n++ {
			created := now.Add(-time.Duration(n) * 17 * time.Minute)
			phase, outcome, confidence := "terminal", []string{"success", "failure", "cancelled", "timed_out", "aborted", "lost"}[n%6], "current"
			var completed *time.Time
			if n < 8 {
				phase = "running"
				outcome = ""
				if n%3 == 0 {
					confidence = "stale"
				}
			} else if n < 12 {
				phase = "accepted_execution"
				outcome = ""
			} else {
				v := created.Add(5 * time.Minute)
				completed = &v
			}
			s.rows = append(s.rows, api.Job{Scope: api.Scope{DeploymentID: id, NamespaceID: NamespaceID}, ID: fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", n), Name: fmt.Sprintf("Synthetic analysis %03d", n), TargetID: "slurm-lab", TargetGeneration: "1", Backend: "slurm", Revision: "1", Owner: &api.Owner{ID: AccountID, DisplayName: "Lab Alice", IsCurrentUser: true}, CreatedAt: created, UpdatedAt: created.Add(5 * time.Minute), CompletedAt: completed, Phase: phase, Outcome: outcome, Confidence: confidence, DesiredState: "run", Labels: map[string]string{"dataset": "synthetic"}})
		}
		result = append(result, s)
	}
	return result
}

func (s *Source) ID() string { return s.DeploymentID }
func (s *Source) Discover(_ context.Context, a monitoring.Actor) (monitoring.Discovery, error) {
	if a.Account.ID != AccountID {
		return monitoring.Discovery{}, monitoring.ErrForbidden
	}
	now := s.Now()
	return monitoring.Discovery{Deployment: api.Deployment{ID: s.DeploymentID, Name: s.Name, Status: "available", Namespaces: []api.Namespace{{ID: NamespaceID, Name: "Research", Roles: []string{"viewer", "submitter"}, Capabilities: []string{"namespace.read", "jobs.read", "groups.read", "logs.read", "artifacts.read", "targets.read", "evidence.read", "reports.read", "diagnosis.request", "jobs.submit"}, AuthorizationVersion: "1", AuthorizationCheckedAt: now, AuthorizationExpiresAt: now.Add(120 * time.Second)}}}, InstanceID: s.DeploymentID, RecoveryEpoch: "1", ServiceTime: now}, nil
}
func (s *Source) allow(a monitoring.Actor, n string) error {
	if a.Account.ID != AccountID || n != NamespaceID {
		return monitoring.ErrForbidden
	}
	return nil
}
func (s *Source) Jobs(_ context.Context, a monitoring.Actor, q monitoring.SourceQuery) (monitoring.JobPage, error) {
	if err := s.allow(a, q.NamespaceID); err != nil {
		return monitoring.JobPage{}, err
	}
	rows := []api.Job{}
	for _, j := range s.rows {
		if j.CreatedAt.After(q.CreatedBefore) || (q.Phase != "" && !phaseMatches(q.Phase, j.Phase)) || (q.Outcome != "" && j.Outcome != q.Outcome) || (q.Owner != "" && q.Owner != "me" && q.Owner != j.Owner.ID) {
			continue
		}
		if q.CompletedFrom != nil && (j.CompletedAt == nil || j.CompletedAt.Before(*q.CompletedFrom)) {
			continue
		}
		if q.CompletedTo != nil && (j.CompletedAt == nil || !j.CompletedAt.Before(*q.CompletedTo)) {
			continue
		}
		if q.JobID != "" && j.ID != q.JobID {
			continue
		}
		if q.Attention && (j.Phase == "terminal" || !slices.Contains([]string{"stale", "uncertain", "lost"}, j.Confidence)) {
			continue
		}
		rows = append(rows, j)
	}
	offset := 0
	if q.Cursor != "" {
		var err error
		offset, err = strconv.Atoi(q.Cursor)
		if err != nil || offset < 0 || offset > len(rows) {
			return monitoring.JobPage{}, monitoring.ErrCursor
		}
	}
	end := min(offset+q.Limit, len(rows))
	next := ""
	if end < len(rows) {
		next = strconv.Itoa(end)
	}
	return monitoring.JobPage{Items: slices.Clone(rows[offset:end]), NextCursor: next, AsOf: s.Now()}, nil
}
func (s *Source) Job(_ context.Context, a monitoring.Actor, scope api.Scope, id string) (api.Job, error) {
	if err := s.allow(a, scope.NamespaceID); err != nil {
		return api.Job{}, err
	}
	for _, j := range s.rows {
		if j.ID == id && j.Scope == scope {
			return j, nil
		}
	}
	return api.Job{}, monitoring.ErrNotFound
}

func (s *Source) JobDetail(ctx context.Context, a monitoring.Actor, scope api.Scope, id string) (api.JobDetail, error) {
	job, err := s.Job(ctx, a, scope, id)
	if err != nil {
		return api.JobDetail{}, err
	}
	job.TargetName, job.Partition = "Synthetic Slurm", "cpu"
	job.ConfidenceUpdatedAt = &job.UpdatedAt
	return api.JobDetail{Job: job, Execution: &api.JobExecution{
		Command:          api.JobCommand{Executable: "/usr/bin/python3", Args: []string{"analysis.py", "--dataset", "synthetic sample", "--output", "results/report.json"}},
		WorkingDirectory: "workspace:/synthetic",
	}}, nil
}
func (s *Source) Summary(_ context.Context, a monitoring.Actor, scope api.Scope, w api.Window) (monitoring.Counts, error) {
	if err := s.allow(a, scope.NamespaceID); err != nil {
		return monitoring.Counts{}, err
	}
	c := monitoring.Counts{Terminal: make(map[string]int64), AsOf: s.Now()}
	for _, j := range s.rows {
		if j.Phase != "terminal" {
			c.Active++
			if j.Phase == "running" {
				c.Running++
			}
			if slices.Contains([]string{"accepted", "assigning", "accepted_execution"}, j.Phase) {
				c.AwaitingExecution++
			}
			if slices.Contains([]string{"stale", "uncertain", "lost"}, j.Confidence) {
				c.EvidenceAttention++
			}
		} else if j.CompletedAt == nil {
			c.MissingCompletionTime++
		} else if !j.CompletedAt.Before(w.From) && j.CompletedAt.Before(w.To) {
			c.Terminal[j.Outcome]++
		}
	}
	return c, nil
}

func phaseMatches(filter, phase string) bool {
	switch filter {
	case "active":
		return phase != "terminal"
	case "awaiting":
		return slices.Contains([]string{"accepted", "assigning", "accepted_execution"}, phase)
	default:
		return filter == phase
	}
}
