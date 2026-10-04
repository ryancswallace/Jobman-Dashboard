package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

// This source supplies deterministic pages, not a Control database or executor.
// The actual production Engine and PostgreSQL cursor store perform every read,
// authority check, quota check and versioned advance in the tests below.
type postgresGroupSource struct {
	now     time.Time
	revoked atomic.Bool
	hook    func()
}

func (*postgresGroupSource) ID() string { return "source" }
func (s *postgresGroupSource) Discover(context.Context, monitoring.Actor) (monitoring.Discovery, error) {
	d := monitoring.Discovery{InstanceID: "instance", RecoveryEpoch: "1", ServiceTime: s.now, Deployment: api.Deployment{ID: s.ID()}}
	if !s.revoked.Load() {
		d.Deployment.Namespaces = []api.Namespace{{ID: "scope", AuthorizationVersion: "1", AuthorizationCheckedAt: s.now, AuthorizationExpiresAt: s.now.Add(time.Hour), Capabilities: []string{"jobs.read", "groups.read"}}}
	}
	return d, nil
}
func (*postgresGroupSource) Jobs(context.Context, monitoring.Actor, monitoring.SourceQuery) (monitoring.JobPage, error) {
	return monitoring.JobPage{}, monitoring.ErrNotFound
}
func (*postgresGroupSource) Job(context.Context, monitoring.Actor, api.Scope, string) (api.Job, error) {
	return api.Job{}, monitoring.ErrNotFound
}
func (*postgresGroupSource) Summary(context.Context, monitoring.Actor, api.Scope, api.Window) (monitoring.Counts, error) {
	return monitoring.Counts{}, monitoring.ErrNotFound
}
func (*postgresGroupSource) Workloads(context.Context, monitoring.Actor, monitoring.GroupSourceQuery) (monitoring.WorkloadSourcePage, error) {
	return monitoring.WorkloadSourcePage{}, monitoring.ErrNotFound
}
func (*postgresGroupSource) Workload(context.Context, monitoring.Actor, api.Scope, string, string) (api.Workload, error) {
	return api.Workload{}, monitoring.ErrNotFound
}
func (*postgresGroupSource) GraphNeighborhood(context.Context, monitoring.Actor, api.Scope, string, string, int, int) (monitoring.GraphNeighborhoodSource, error) {
	return monitoring.GraphNeighborhoodSource{}, monitoring.ErrNotFound
}
func (s *postgresGroupSource) WorkloadChildren(_ context.Context, _ monitoring.Actor, scope api.Scope, _, _ string, after, limit int) (monitoring.WorkloadChildSourcePage, error) {
	p := monitoring.WorkloadChildSourcePage{Total: "10000", AsOf: s.now}
	end := min(after+1+limit, 10000)
	for i := after + 1; i < end; i++ {
		id := fmt.Sprintf("node-%05d", i)
		p.Items = append(p.Items, api.WorkloadChild{ID: id, Index: strconv.Itoa(i), Job: api.Job{ID: id, Scope: scope}})
	}
	if end < 10000 {
		next := end - 1
		p.NextIndex = &next
	}
	return p, nil
}
func (s *postgresGroupSource) GraphEdges(_ context.Context, _ monitoring.Actor, _ api.Scope, _ string, q monitoring.GraphEdgeQuery) (monitoring.GraphEdgeSourcePage, error) {
	start, _ := strconv.Atoi(q.Cursor)
	end := min(start+q.Limit, 100000)
	p := monitoring.GraphEdgeSourcePage{Total: "100000", AsOf: s.now}
	for i := start; i < end; i++ {
		from, to := 0, i+1
		if i >= 9999 {
			from = 1 + (i-9999)/10
			to = from + 1 + (i-9999)%10
		}
		p.Items = append(p.Items, api.GraphEdge{FromJobID: fmt.Sprintf("node-%05d", from), ToJobID: fmt.Sprintf("node-%05d", to)})
	}
	if end < 100000 {
		p.NextCursor = strconv.Itoa(end)
	}
	if s.hook != nil {
		s.hook()
	}
	return p, nil
}

func postgresGroupEngine(t *testing.T, s *Store) (*monitoring.Engine, *postgresGroupSource, monitoring.Actor, api.Scope) {
	t.Helper()
	var now time.Time
	if err := s.Pool.QueryRow(t.Context(), "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	src := &postgresGroupSource{now: now}
	e, err := monitoring.New([]monitoring.Source{src}, s)
	if err != nil {
		t.Fatal(err)
	}
	e.Now = func() time.Time { return now }
	return e, src, monitoring.Actor{Account: api.Account{ID: "reader"}}, api.Scope{DeploymentID: src.ID(), NamespaceID: "scope"}
}

func TestPostgresGroupTraversalCeiling(t *testing.T) {
	s := testDB(t)
	e, _, actor, scope := postgresGroupEngine(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cursor := ""
	for page := 0; page < 200; page++ {
		p, err := e.WorkloadChildren(ctx, actor, scope, "graph", "ceiling", 50, cursor)
		if err != nil || p.Total != "10000" || len(p.Items) != 50 || (p.NextCursor == "") != (page == 199) {
			t.Fatalf("bounded child page %d: %v", page, err)
		}
		for offset, child := range p.Items {
			if child.ID != fmt.Sprintf("node-%05d", page*50+offset) {
				t.Fatal("child lost, duplicated or reordered")
			}
		}
		cursor = p.NextCursor
	}
	cursor, previous := "", ""
	seen := make(map[string]bool, 100000)
	cursors := []string{""}
	for page := 0; page < 1000; page++ {
		p, err := e.GraphEdges(ctx, actor, scope, "ceiling", monitoring.GraphEdgeQuery{Limit: 100}, cursor)
		if err != nil || p.Total != "100000" || len(p.Items) != 100 || (p.NextCursor == "") != (page == 999) {
			t.Fatalf("bounded dependency page %d: %v", page, err)
		}
		for _, edge := range p.Items {
			key := edge.FromJobID + "/" + edge.ToJobID
			if seen[key] || key <= previous {
				t.Fatal("dependency lost, duplicated or reordered")
			}
			seen[key], previous = true, key
		}
		cursor = p.NextCursor
		cursors = append(cursors, cursor)
	}
	if len(seen) != 100000 {
		t.Fatal("incomplete dependency traversal")
	}
	rows, err := s.Pool.Query(ctx, "SELECT payload,expires_at FROM dashboard_browse_sessions")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		var data []byte
		var expires time.Time
		if err := rows.Scan(&data, &expires); err != nil {
			t.Fatal(err)
		}
		var history struct {
			Format int               `json:"format"`
			Points []json.RawMessage `json:"points"`
		}
		if json.Unmarshal(data, &history) != nil || history.Format != 1 || len(history.Points) != 66 || len(data) > 20<<10 || !expires.Equal(e.Now().Add(15*time.Minute)) {
			t.Fatal("cursor selector, byte or original expiry bound changed")
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil || count != 2 {
		t.Fatalf("1200 pages retained %d rows instead of two: %v", count, err)
	}
	// Replay the retained window through PostgreSQL without truncating later
	// selectors. The earliest evicted selector must require an explicit refresh.
	for _, index := range []int{934, 935, 950, 998, 935} {
		p, err := e.GraphEdges(ctx, actor, scope, "ceiling", monitoring.GraphEdgeQuery{Limit: 100}, cursors[index])
		if err != nil || p.NextCursor != cursors[index+1] {
			t.Fatalf("retained PostgreSQL replay %d: %v", index, err)
		}
	}
	if _, err := e.GraphEdges(ctx, actor, scope, "ceiling", monitoring.GraphEdgeQuery{Limit: 100}, cursors[933]); !errors.Is(err, monitoring.ErrCursor) {
		t.Fatal("evicted PostgreSQL selector accepted")
	}
}

func TestPostgresGroupTraversalCASAndExpiry(t *testing.T) {
	s := testDB(t)
	e, src, actor, scope := postgresGroupEngine(t, s)
	q := monitoring.GraphEdgeQuery{Limit: 100}
	first, err := e.GraphEdges(t.Context(), actor, scope, "ceiling", q, "")
	if err != nil {
		t.Fatal(err)
	}
	// A visited row survives replacement of unused polling continuations.
	second, err := e.GraphEdges(t.Context(), actor, scope, "ceiling", q, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Split(first.NextCursor, ".")[1]
	before, version, err := s.Load(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	arrived, release := make(chan struct{}, 8), make(chan struct{})
	src.hook = func() { arrived <- struct{}{}; <-release }
	var wg sync.WaitGroup
	errs, cursors := make(chan error, 8), make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			p, err := e.GraphEdges(t.Context(), actor, scope, "ceiling", q, second.NextCursor)
			errs <- err
			cursors <- p.NextCursor
		})
	}
	for range 8 {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			close(release)
			wg.Wait()
			t.Fatal("concurrent PostgreSQL readers did not reach the barrier")
		}
	}
	close(release)
	wg.Wait()
	src.hook = nil
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal("concurrent PostgreSQL advance failed", err)
		}
		if cursor := <-cursors; cursor != "g."+key+".3" {
			t.Fatal("concurrent PostgreSQL advance forked history")
		}
	}
	after, advanced, err := s.Load(t.Context(), key)
	if err != nil || advanced != version+1 || string(after) == string(before) {
		t.Fatal("concurrent advance was lost or committed more than once")
	}
	for range 6 {
		if _, err := e.GraphEdges(t.Context(), actor, scope, "ceiling", q, ""); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM dashboard_browse_sessions").Scan(&count); err != nil || count != 4 {
		t.Fatal("initial polling pruned visited history or grew unbounded", err)
	}
	if _, err := e.GraphEdges(t.Context(), actor, scope, "ceiling", q, first.NextCursor); err != nil {
		t.Fatal("visited Back selector was pruned", err)
	}
	src.hook = func() { src.revoked.Store(true) }
	if _, err := e.GraphEdges(t.Context(), actor, scope, "ceiling", q, "g."+key+".3"); err == nil {
		t.Fatal("post-read revocation published a continuation")
	}
	unchanged, unchangedVersion, err := s.Load(t.Context(), key)
	if err != nil || string(unchanged) != string(after) || unchangedVersion != advanced {
		t.Fatal("failed final authorization changed PostgreSQL cursor state")
	}
	src.hook = nil
	src.revoked.Store(false)
	if _, err := s.Pool.Exec(t.Context(), "UPDATE dashboard_browse_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", key); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GraphEdges(t.Context(), actor, scope, "ceiling", q, first.NextCursor); !errors.Is(err, monitoring.ErrCursor) {
		t.Fatal("database-expired traversal accepted")
	}
	if err := s.Advance(t.Context(), key, advanced, after); !errors.Is(err, monitoring.ErrCursor) {
		t.Fatal("expired PostgreSQL CAS succeeded")
	}
}
