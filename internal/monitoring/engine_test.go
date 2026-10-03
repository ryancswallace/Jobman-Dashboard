package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

var clock = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

type testSource struct {
	id            string
	rows          []api.Job
	mu            sync.Mutex
	version       string
	epoch         string
	revoked       bool
	fail          bool
	failDiscovery bool
	calls         int
	pageSize      int
	hook          func()
}

func (s *testSource) ID() string { return s.id }
func (s *testSource) Discover(_ context.Context, _ Actor) (Discovery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failDiscovery {
		return Discovery{}, ErrSource
	}
	ns := []api.Namespace{}
	if !s.revoked {
		ns = append(ns, api.Namespace{ID: "ns", Name: "Research", Roles: []string{"viewer"}, Capabilities: []string{"jobs.read"}, AuthorizationVersion: s.version, AuthorizationCheckedAt: clock, AuthorizationExpiresAt: clock.Add(time.Minute)})
	}
	return Discovery{Deployment: api.Deployment{ID: s.id, Name: s.id, Namespaces: ns}, InstanceID: s.id, RecoveryEpoch: s.epoch, ServiceTime: clock}, nil
}
func (s *testSource) Jobs(_ context.Context, _ Actor, q SourceQuery) (JobPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.fail {
		return JobPage{}, ErrSource
	}
	start := 0
	if q.Cursor != "" {
		if _, err := fmt.Sscanf(q.Cursor, "offset-%d", &start); err != nil {
			return JobPage{}, err
		}
	}
	end := min(start+q.Limit, len(s.rows))
	if s.pageSize > 0 {
		end = min(end, start+s.pageSize)
	}
	items := slices.Clone(s.rows[start:end])
	next := ""
	if end < len(s.rows) {
		next = fmt.Sprintf("offset-%d", end)
	}
	if s.hook != nil {
		s.hook()
	}
	return JobPage{Items: items, NextCursor: next, AsOf: clock}, nil
}
func (s *testSource) Job(_ context.Context, _ Actor, scope api.Scope, id string) (api.Job, error) {
	for _, j := range s.rows {
		if j.ID == id && j.Scope == scope {
			return j, nil
		}
	}
	return api.Job{}, ErrNotFound
}
func (s *testSource) Summary(_ context.Context, _ Actor, _ api.Scope, _ api.Window) (Counts, error) {
	if s.fail {
		return Counts{}, ErrSource
	}
	return Counts{Active: int64(len(s.rows)), Terminal: map[string]int64{"future-outcome": 2}, AsOf: clock}, nil
}
func source(id string, count int, spacing time.Duration) *testSource {
	s := &testSource{id: id, version: "1", epoch: "1"}
	for i := 0; i < count; i++ {
		s.rows = append(s.rows, api.Job{Scope: api.Scope{DeploymentID: id, NamespaceID: "ns"}, ID: fmt.Sprintf("job-%03d", i), CreatedAt: clock.Add(-time.Duration(i) * spacing), Phase: "running", DesiredState: "cancel", Confidence: "stale", Revision: "9007199254740993", Labels: map[string]string{}})
	}
	slices.SortFunc(s.rows, compareJobs)
	return s
}

func TestSameTimestampControlKeysetsRemainDescendingAcrossPageBoundaries(t *testing.T) {
	a, b := source("a", 7, 0), source("b", 7, 0)
	a.pageSize = 2
	b.pageSize = 2
	e := engine(t, a, b)
	var got []string
	cursor := ""
	for range 10 {
		page, err := e.Jobs(context.Background(), actor("one"), Query{Limit: 3}, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range page.Items {
			got = append(got, job.DeploymentID+"/"+job.ID)
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	var expected []string
	for _, id := range []string{"a", "b"} {
		for i := 6; i >= 0; i-- {
			expected = append(expected, fmt.Sprintf("%s/job-%03d", id, i))
		}
	}
	if !slices.Equal(got, expected) {
		t.Fatalf("equal-time keyset skipped or reordered jobs: got %v; want %v", got, expected)
	}
}
func engine(t *testing.T, ss ...*testSource) *Engine {
	t.Helper()
	sources := []Source{}
	for _, s := range ss {
		sources = append(sources, s)
	}
	store := NewMemoryCursors()
	store.Now = func() time.Time { return clock }
	e, err := New(sources, store)
	if err != nil {
		t.Fatal(err)
	}
	e.Now = store.Now
	return e
}
func actor(id string) Actor { return Actor{Account: api.Account{ID: id, DisplayName: id}} }

func TestMergeRetainsUnconsumedRowsAcrossUnequalSourcePages(t *testing.T) {
	a, b := source("a", 17, time.Minute), source("b", 13, 37*time.Second)
	e := engine(t, a, b)
	cursor := ""
	all := []api.Job{}
	for i := 0; i < 20; i++ {
		page, err := e.Jobs(context.Background(), actor("one"), Query{Limit: 4}, cursor)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Items...)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	expected := append(slices.Clone(a.rows), b.rows...)
	slices.SortFunc(expected, compareJobs)
	if len(all) != len(expected) {
		t.Fatalf("got %d rows, want %d", len(all), len(expected))
	}
	for i, j := range all {
		if j.Scope != expected[i].Scope || j.ID != expected[i].ID {
			t.Fatalf("row %d is %s/%s, want %s/%s", i, j.DeploymentID, j.ID, expected[i].DeploymentID, expected[i].ID)
		}
	}
	if a.calls > 6 || b.calls > 5 {
		t.Fatalf("unbounded page requests a=%d b=%d", a.calls, b.calls)
	}
}

func TestCursorCannotCrossAccountQueryAuthorizationOrRecoveryEpoch(t *testing.T) {
	for _, change := range []string{"account", "filter", "permission", "epoch"} {
		t.Run(change, func(t *testing.T) {
			s := source("a", 8, time.Minute)
			e := engine(t, s)
			a := actor("one")
			q := Query{Limit: 2}
			p, err := e.Jobs(context.Background(), a, q, "")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "account":
				a = actor("two")
			case "filter":
				q.Phase = "terminal"
			case "permission":
				s.version = "2"
			case "epoch":
				s.epoch = "2"
			}
			if _, err = e.Jobs(context.Background(), a, q, p.NextCursor); err == nil {
				t.Fatal("accepted invalid cursor")
			}
		})
	}
}

func TestCursorRetryAndBackNavigationReplaySamePage(t *testing.T) {
	s := source("a", 9, time.Minute)
	e := engine(t, s)
	ctx := context.Background()
	a := actor("one")
	q := Query{Limit: 2}
	first, err := e.Jobs(ctx, a, q, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.Jobs(ctx, a, q, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Jobs(ctx, a, q, second.NextCursor); err != nil {
		t.Fatal(err)
	}
	replay, err := e.Jobs(ctx, a, q, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Items) != len(second.Items) || replay.Items[0].ID != second.Items[0].ID || replay.NextCursor != second.NextCursor {
		t.Fatal("retry changed page contents or continuation")
	}
	s.revoked = true
	if _, err := e.Jobs(ctx, a, q, first.NextCursor); err == nil {
		t.Fatal("cached page bypassed revocation")
	}
}

func TestExcludedRefillRowsCannotSurvivePublicationOrRevokedReplay(t *testing.T) {
	a, b := source("a", 10, time.Minute), source("b", 20, 2*time.Minute)
	a.pageSize = 1
	e := engine(t, a, b)
	ctx := context.Background()
	who := actor("one")
	q := Query{Limit: 2}
	first, err := e.Jobs(ctx, who, q, "")
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first: %+v %v", first, err)
	}
	// A has a buffered row. It can contribute that row before its next read
	// fails; an unscoped query must still discard its whole partial contribution.
	a.fail = true
	second, err := e.Jobs(ctx, who, q, first.NextCursor)
	if err != nil || second.Completeness != "partial" || second.NextCursor == "" {
		t.Fatalf("second: %+v %v", second, err)
	}
	for _, job := range second.Items {
		if job.DeploymentID == "a" {
			t.Fatal("failed refill left an emitted source row in the response")
		}
	}
	// Also reject an older persisted cursor written before this fix, whose
	// cached rows and excluded buffers disagree after that scope is revoked.
	key := strings.Split(first.NextCursor, ".")[0]
	data, version, err := e.cursors.Load(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	var state browseState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	state.Response.Items = append(state.Response.Items, a.rows[1])
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.cursors.Advance(ctx, key, version, data); err != nil {
		t.Fatal(err)
	}
	a.revoked = true
	if _, err := e.Jobs(ctx, who, q, first.NextCursor); err == nil {
		t.Fatal("excluded cached source bypassed current authorization after revocation")
	}
}

func TestGrantRemovedDuringReadNeverPublishesRows(t *testing.T) {
	s := source("a", 2, time.Minute)
	e := engine(t, s)
	s.hook = func() { s.revoked = true }
	p, err := e.Jobs(context.Background(), actor("one"), Query{Limit: 2}, "")
	if err == nil {
		t.Fatalf("leaked page %#v", p)
	}
}

func TestUnavailableSourceIsExplicitPartialAndNeverRejoinsCursor(t *testing.T) {
	a, b := source("a", 5, time.Minute), source("b", 5, time.Minute)
	b.fail = true
	e := engine(t, a, b)
	p, err := e.Jobs(context.Background(), actor("one"), Query{Limit: 2}, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Completeness != "partial" || len(p.Sources) != 2 {
		t.Fatalf("missing partial source state: %#v", p)
	}
	b.fail = false
	next, err := e.Jobs(context.Background(), actor("one"), Query{Limit: 2}, p.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range next.Items {
		if j.DeploymentID != "a" {
			t.Fatal("late source inserted into existing merge")
		}
	}
}

func TestOverviewUnknownOutcomesAndNoSyntheticZero(t *testing.T) {
	a, b := source("a", 3, time.Minute), source("b", 6, time.Minute)
	b.fail = true
	e := engine(t, a, b)
	w := api.Window{From: clock.Add(-24 * time.Hour), To: clock}
	o, err := e.Overview(context.Background(), actor("one"), Query{}, w)
	if err != nil {
		t.Fatal(err)
	}
	if o.Completeness != "partial" || *o.Active != 3 || *o.Terminal["unknown"] != 2 {
		t.Fatalf("wrong counts: %#v", o)
	}
	a.fail = true
	o, err = e.Overview(context.Background(), actor("one"), Query{}, w)
	if err == nil || o.Active != nil {
		t.Fatal("unavailable total represented as a known count")
	}
}

func TestSourceCannotReturnAnotherNamespacesRow(t *testing.T) {
	s := source("a", 2, time.Minute)
	s.rows[0].NamespaceID = "private"
	e := engine(t, s)
	if _, err := e.Jobs(context.Background(), actor("one"), Query{}, ""); err == nil {
		t.Fatal("accepted cross-namespace upstream rows")
	}
}

func TestIntentConfidenceAndWideRevisionRemainSeparate(t *testing.T) {
	s := source("a", 1, time.Minute)
	e := engine(t, s)
	p, err := e.Jobs(context.Background(), actor("one"), Query{}, "")
	if err != nil {
		t.Fatal(err)
	}
	j := p.Items[0]
	if j.Phase != "running" || j.Outcome != "" || j.DesiredState != "cancel" || j.Confidence != "stale" || j.Revision != "9007199254740993" {
		t.Fatal("job meaning changed")
	}
}

func TestDiscoveryOutageKeepsHealthySourceUsefulAndPartial(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			a, b := source("a", 4, time.Minute), source("b", 4, time.Minute)
			b.failDiscovery = true
			e := engine(t, a, b)
			q := Query{Limit: 2}
			if explicit {
				q.Scopes = []api.Scope{{DeploymentID: "a", NamespaceID: "ns"}, {DeploymentID: "b", NamespaceID: "ns"}}
			}
			p, err := e.Jobs(context.Background(), actor("one"), q, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Items) != 2 || p.Completeness != "partial" || len(p.Sources) != 2 {
				t.Fatalf("outage hid healthy jobs or partial status: %#v", p)
			}
			o, err := e.Overview(context.Background(), actor("one"), q, api.Window{From: clock.Add(-24 * time.Hour), To: clock})
			if err != nil {
				t.Fatal(err)
			}
			if o.Completeness != "partial" || *o.Active != 4 {
				t.Fatalf("invalid partial overview: %#v", o)
			}
			b.failDiscovery = false
			next, err := e.Jobs(context.Background(), actor("one"), q, p.NextCursor)
			if err != nil {
				t.Fatal(err)
			}
			for _, j := range next.Items {
				if j.DeploymentID != "a" {
					t.Fatal("recovered source rejoined incomplete cursor")
				}
			}
		})
	}
}

func TestExplicitEmptyScopeNeverExpandsToAllAuthorized(t *testing.T) {
	e := engine(t, source("a", 4, time.Minute))
	q := Query{Scopes: []api.Scope{}, Limit: 2}
	p, err := e.Jobs(context.Background(), actor("one"), q, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 0 || len(p.Sources) != 0 {
		t.Fatal("empty scope broadened")
	}
	o, err := e.Overview(context.Background(), actor("one"), q, api.Window{From: clock.Add(-24 * time.Hour), To: clock})
	if err != nil || o.Active == nil || *o.Active != 0 {
		t.Fatalf("empty overview: %#v %v", o, err)
	}
}

func TestInvalidSummaryCannotContributeToTotals(t *testing.T) {
	valid := Counts{Active: 5, AsOf: clock, Terminal: map[string]int64{}}
	for _, bad := range []Counts{{Active: -1, AsOf: clock}, {Active: 2, Running: 3, AsOf: clock}, {Active: 1}, {Active: 9007199254740991, AsOf: clock}, {Active: 1, AsOf: clock, Terminal: map[string]int64{"success": -1}}} {
		if _, err := addCounts(valid, bad); err == nil {
			t.Fatalf("accepted invalid counts: %#v", bad)
		}
	}
}

func TestPollingKeepsOnlyThreeUnusedContinuationsAndPreservesVisitedPage(t *testing.T) {
	e := engine(t, source("a", 12, time.Minute))
	q := Query{Limit: 2}
	a := actor("one")
	ctx := context.Background()
	first, err := e.Jobs(ctx, a, q, "")
	if err != nil {
		t.Fatal(err)
	}
	visited, err := e.Jobs(ctx, a, q, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		if _, err := e.Jobs(ctx, a, q, ""); err != nil {
			t.Fatal(err)
		}
	}
	store := e.cursors.(*MemoryCursors)
	if len(store.records) > 5 {
		t.Fatalf("polling accumulated %d records", len(store.records))
	}
	replay, err := e.Jobs(ctx, a, q, first.NextCursor)
	if err != nil || replay.NextCursor != visited.NextCursor {
		t.Fatalf("visited page lost: %v", err)
	}
}
