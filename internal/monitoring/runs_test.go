package monitoring

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

const runJobID = "10000000-0000-4000-8000-000000000001"

type runFixture struct {
	*testSource
	corrupt string
}

func runFact(n int) api.JobRun {
	return api.JobRun{ID: fmt.Sprintf("20000000-0000-4000-8000-%012d", n), Number: strconv.Itoa(n), Phase: "terminal", DesiredState: "run", Outcome: "future", CreatedAt: clock.Add(-time.Hour), UpdatedAt: clock}
}
func (s *runFixture) Runs(_ context.Context, _ Actor, q RunQuery) (RunSourcePage, error) {
	start := 8
	if q.Cursor != "" {
		start, _ = strconv.Atoi(q.Cursor)
	}
	out := RunSourcePage{Items: []api.JobRun{}, Total: "8", AsOf: clock}
	for n := start; n > 0 && len(out.Items) < q.Limit; n-- {
		out.Items = append(out.Items, runFact(n))
	}
	if start-len(out.Items) > 0 {
		out.NextCursor = strconv.Itoa(start - len(out.Items))
	}
	switch s.corrupt {
	case "order":
		out.Items[1] = out.Items[0]
	case "total":
		out.Total = "1"
	case "changed_total":
		if q.Cursor != "" {
			out.Total = "9"
		}
	case "loop":
		out.NextCursor = q.Cursor
	case "identity":
		out.Items[0].ID = "invalid"
	case "execution":
		out.Items[0].ExecutionID = runJobID
	case "expired":
		s.version = "changed"
	case "revoke":
		s.revoked = true
	}
	return out, nil
}
func (s *runFixture) Run(_ context.Context, _ Actor, _ api.Scope, _, id string) (RunSourceDetail, error) {
	r := runFact(8)
	if s.corrupt == "identity" {
		r.ID = runJobID
	}
	if s.corrupt == "revoke" {
		s.revoked = true
	}
	return RunSourceDetail{Run: r, AsOf: clock}, nil
}
func TestRunPagingAuthorityAndBounds(t *testing.T) {
	for _, mode := range []string{"valid", "actor", "job", "source", "scope", "limit", "epoch", "grant", "revoked", "expired", "changed_total"} {
		t.Run(mode, func(t *testing.T) {
			s := &runFixture{testSource: source("a", 0, time.Minute)}
			e := groupEngine(t, s)
			a := actor("one")
			q := RunQuery{Scope: api.Scope{DeploymentID: "a", NamespaceID: "ns"}, JobID: runJobID, Limit: 3}
			first, err := e.Runs(t.Context(), a, q, "")
			if err != nil || len(first.Items) != 3 || first.Items[0].Number != "8" || first.NextCursor == "" {
				t.Fatalf("first=%+v,%v", first, err)
			}
			switch mode {
			case "actor":
				a = actor("two")
			case "job":
				q.JobID = "10000000-0000-4000-8000-000000000002"
			case "source":
				q.Scope.DeploymentID = "b"
			case "scope":
				q.Scope.NamespaceID = "other"
			case "limit":
				q.Limit = 2
			case "epoch":
				s.epoch = "2"
			case "grant":
				s.version = "2"
			case "revoked":
				s.revoked = true
			case "expired":
				e.Now = func() time.Time { return clock.Add(11 * time.Minute) }
			case "changed_total":
				s.corrupt = mode
			}
			second, err := e.Runs(t.Context(), a, q, first.NextCursor)
			if mode != "valid" {
				if err == nil || len(second.Items) > 0 {
					t.Fatalf("unsafe continuation=%+v,%v", second, err)
				}
				return
			}
			if err != nil || second.Items[0].Number != "5" {
				t.Fatalf("second=%+v,%v", second, err)
			}
			third, err := e.Runs(t.Context(), a, q, second.NextCursor)
			if err != nil || len(third.Items) != 2 || third.NextCursor != "" {
				t.Fatalf("third=%+v,%v", third, err)
			}
			detail, err := e.Run(t.Context(), a, q.Scope, q.JobID, first.Items[0].ID)
			if err != nil || detail.Run != first.Items[0] {
				t.Fatalf("detail=%+v,%v", detail, err)
			}
		})
	}
	for _, mode := range []string{"order", "total", "identity", "execution", "expired", "revoke"} {
		t.Run("source_"+mode, func(t *testing.T) {
			s := &runFixture{testSource: source("a", 0, time.Minute), corrupt: mode}
			e := groupEngine(t, s)
			page, err := e.Runs(t.Context(), actor("one"), RunQuery{Scope: api.Scope{DeploymentID: "a", NamespaceID: "ns"}, JobID: runJobID, Limit: 3}, "")
			if err == nil || len(page.Items) > 0 {
				t.Fatalf("corrupt page=%+v,%v", page, err)
			}
		})
	}
	for _, mode := range []string{"identity", "revoke"} {
		s := &runFixture{testSource: source("a", 0, time.Minute), corrupt: mode}
		if _, err := groupEngine(t, s).Run(t.Context(), actor("one"), api.Scope{DeploymentID: "a", NamespaceID: "ns"}, runJobID, runFact(8).ID); err == nil {
			t.Fatal("unsafe run detail")
		}
	}
}
