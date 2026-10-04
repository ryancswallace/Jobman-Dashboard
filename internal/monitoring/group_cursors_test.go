package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

type cursorCeilingSource struct {
	*groupFixture
	edgeHook   func()
	nextSuffix string
}

func (s *cursorCeilingSource) WorkloadChildren(_ context.Context, _ Actor, scope api.Scope, _, _ string, after, limit int) (WorkloadChildSourcePage, error) {
	page := WorkloadChildSourcePage{Total: "10000", AsOf: clock}
	end := min(after+1+limit, 10000)
	for i := after + 1; i < end; i++ {
		id := fmt.Sprintf("node-%05d", i)
		page.Items = append(page.Items, api.WorkloadChild{ID: id, Index: strconv.Itoa(i), Job: api.Job{ID: id, Scope: scope}})
	}
	if end < 10000 {
		next := end - 1
		page.NextIndex = &next
	}
	return page, nil
}

func (s *cursorCeilingSource) GraphEdges(_ context.Context, _ Actor, _ api.Scope, _ string, q GraphEdgeQuery) (GraphEdgeSourcePage, error) {
	start, _ := strconv.Atoi(q.Cursor)
	end := min(start+q.Limit, 100000)
	page := GraphEdgeSourcePage{Total: "100000", AsOf: clock}
	for i := start; i < end; i++ {
		from, to := 0, i+1
		if i >= 9999 {
			from = 1 + (i-9999)/10
			to = from + 1 + (i-9999)%10
		}
		page.Items = append(page.Items, api.GraphEdge{FromJobID: fmt.Sprintf("node-%05d", from), ToJobID: fmt.Sprintf("node-%05d", to)})
	}
	if end < 100000 {
		page.NextCursor = strconv.Itoa(end) + s.nextSuffix
	}
	if s.edgeHook != nil {
		s.edgeHook()
	}
	return page, nil
}

func TestGroupTraversalCeilingUsesTwoBoundedRows(t *testing.T) {
	s := &cursorCeilingSource{groupFixture: groupSource("a", 1)}
	e := groupEngine(t, s)
	a := actor("one")
	scope := api.Scope{DeploymentID: "a", NamespaceID: "ns"}
	store := e.cursors.(*MemoryCursors)
	// The fixture store has a stricter 100/account row limit than PostgreSQL's
	// 1000; both must admit a complete legitimate 1200-page traversal. Lower
	// the global cap further to prove this test cannot hide per-page row growth.
	store.Max = 3
	cursor := ""
	children := map[string]bool{}
	for page := 0; page < 200; page++ {
		p, err := e.WorkloadChildren(t.Context(), a, scope, "graph", "graph", 50, cursor)
		if err != nil || p.Total != "10000" || len(p.Items) != 50 {
			t.Fatalf("child page %d: %v", page, err)
		}
		for i, child := range p.Items {
			if child.ID != fmt.Sprintf("node-%05d", page*50+i) || children[child.ID] {
				t.Fatal("child lost, duplicated or reordered")
			}
			children[child.ID] = true
		}
		cursor = p.NextCursor
		if (cursor == "") != (page == 199) {
			t.Fatal("child continuation differs")
		}
	}
	cursor = ""
	edges := map[string]bool{}
	previous := ""
	for page := 0; page < 1000; page++ {
		p, err := e.GraphEdges(t.Context(), a, scope, "graph", GraphEdgeQuery{Limit: 100}, cursor)
		if err != nil || p.Total != "100000" || len(p.Items) != 100 {
			t.Fatalf("edge page %d: %v", page, err)
		}
		for _, edge := range p.Items {
			key := edge.FromJobID + "/" + edge.ToJobID
			if edges[key] || key <= previous {
				t.Fatal("edge duplicated or reordered")
			}
			edges[key], previous = true, key
		}
		cursor = p.NextCursor
		if (cursor == "") != (page == 999) {
			t.Fatal("edge continuation differs")
		}
		for _, record := range store.records {
			var history groupCursorHistory
			if json.Unmarshal(record.data, &history) != nil || len(history.Points) > groupCursorWindow || len(record.data) > 20<<10 || !history.Expires.Equal(clock.Add(15*time.Minute)) {
				t.Fatal("traversal exceeded its finite selector/byte/expiry bounds")
			}
		}
	}
	if len(children) != 10000 || len(edges) != 100000 || len(store.records) != 2 {
		t.Fatal("complete navigation did not retain exactly two bounded rows")
	}
}

func TestGroupTraversalBackReplayAndConcurrentAdvance(t *testing.T) {
	s := &cursorCeilingSource{groupFixture: groupSource("a", 1)}
	e := groupEngine(t, s)
	a := actor("one")
	scope := api.Scope{DeploymentID: "a", NamespaceID: "ns"}
	q := GraphEdgeQuery{Limit: 100}
	cursors := []string{""}
	for range 80 {
		p, err := e.GraphEdges(t.Context(), a, scope, "graph", q, cursors[len(cursors)-1])
		if err != nil {
			t.Fatal(err)
		}
		cursors = append(cursors, p.NextCursor)
	}
	key, _, _ := groupPosition(cursors[80])
	before, version, err := e.cursors.Load(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range []int{15, 16, 40, 79, 40} {
		p, err := e.GraphEdges(t.Context(), a, scope, "graph", q, cursors[page])
		if err != nil || p.NextCursor != cursors[page+1] {
			t.Fatalf("retained Back/replay page %d lost its original successor: %v", page, err)
		}
	}
	after, afterVersion, err := e.cursors.Load(t.Context(), key)
	if err != nil || string(before) != string(after) || version != afterVersion {
		t.Fatal("Back traversal modified or evicted forward history")
	}
	if _, err := e.GraphEdges(t.Context(), a, scope, "graph", q, cursors[14]); !errors.Is(err, ErrCursor) {
		t.Fatal("evicted Back selector remained usable")
	}
	var wg sync.WaitGroup
	arrived, release := make(chan struct{}, 8), make(chan struct{})
	s.edgeHook = func() { arrived <- struct{}{}; <-release }
	results := make(chan string, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			page, err := e.GraphEdges(t.Context(), a, scope, "graph", q, cursors[80])
			results <- page.NextCursor
			errs <- err
		})
	}
	for range 8 {
		select {
		case <-arrived:
		case <-time.After(time.Second):
			close(release)
			wg.Wait()
			t.Fatal("concurrent readers did not capture the same prior version")
		}
	}
	close(release)
	wg.Wait()
	s.edgeHook = nil
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal("concurrent advance failed", err)
		}
		if cursor := <-results; cursor != groupToken(key, 81) {
			t.Fatal("concurrent advance forked its successor")
		}
	}
	data, _, _ := e.cursors.Load(t.Context(), key)
	var history groupCursorHistory
	_ = json.Unmarshal(data, &history)
	if history.First != 16 || len(history.Points) != groupCursorWindow {
		t.Fatal("concurrent advance moved the window more than once")
	}
	s.nextSuffix = "changed"
	if _, err := e.GraphEdges(t.Context(), a, scope, "graph", q, cursors[40]); !errors.Is(err, ErrCursor) {
		t.Fatal("replayed page forked its immutable successor")
	}
	s.nextSuffix = ""
	s.edgeHook = func() { s.revoked = true }
	if _, err := e.GraphEdges(t.Context(), a, scope, "graph", q, groupToken(key, 81)); err == nil {
		t.Fatal("post-read namespace loss published a continuation")
	}
	unchanged, _, _ := e.cursors.Load(t.Context(), key)
	if string(unchanged) != string(data) {
		t.Fatal("failed replay or final authorization changed history")
	}
}

func TestGroupTraversalBindingExpiryAndLegacy(t *testing.T) {
	for _, mode := range []string{"account", "query", "scope", "kind", "grant", "epoch", "revoked", "expired", "position", "legacy alias"} {
		t.Run(mode, func(t *testing.T) {
			s := &cursorCeilingSource{groupFixture: groupSource("a", 1)}
			e := groupEngine(t, s)
			a := actor("one")
			scope := api.Scope{DeploymentID: "a", NamespaceID: "ns"}
			q := GraphEdgeQuery{Limit: 100}
			page, err := e.GraphEdges(t.Context(), a, scope, "graph", q, "")
			if err != nil {
				t.Fatal(err)
			}
			cursor, id := page.NextCursor, "graph"
			switch mode {
			case "account":
				a = actor("two")
			case "query":
				q.Limit = 50
			case "scope":
				scope.NamespaceID = "other"
			case "kind":
				id = "other"
			case "grant":
				s.version = "2"
			case "epoch":
				s.epoch = "2"
			case "revoked":
				s.revoked = true
			case "expired":
				e.cursors.(*MemoryCursors).Now = func() time.Time { return clock.Add(16 * time.Minute) }
			case "position":
				cursor = strings.TrimSuffix(cursor, ".1") + ".2"
			case "legacy alias":
				key, _, _ := groupPosition(cursor)
				cursor = "r." + key
			}
			if _, err := e.GraphEdges(t.Context(), a, scope, id, q, cursor); err == nil {
				t.Fatal("changed authority/query/position accepted")
			}
		})
	}
	s := &cursorCeilingSource{groupFixture: groupSource("a", 1)}
	e := groupEngine(t, s)
	a := actor("one")
	scope := api.Scope{DeploymentID: "a", NamespaceID: "ns"}
	_, authority, err := e.authorizedGroup(t.Context(), a, scope)
	if err != nil {
		t.Fatal(err)
	}
	legacy := groupResourceCursor{CursorIdentity: CursorIdentity{AccountID: a.Account.ID, QueryHash: groupHash(groupResourceQuery{Scope: scope, Kind: "graph", ID: "graph", Operation: "edges", Limit: 100}), Initial: true}, Expires: clock.Add(3 * time.Minute), Authority: authority, AfterIndex: -1, SourceCursor: "100", LastEdge: "node-00000/node-00100"}
	raw, _ := encodeState(legacy)
	key, err := e.cursors.Create(t.Context(), raw, legacy.Expires)
	if err != nil {
		t.Fatal(err)
	}
	page, err := e.GraphEdges(t.Context(), a, scope, "graph", GraphEdgeQuery{Limit: 100}, "r."+key)
	if err != nil || !strings.HasPrefix(page.NextCursor, "g.") {
		t.Fatal("legacy selector failed to continue", err)
	}
	nextKey, _, _ := groupPosition(page.NextCursor)
	data, _, _ := e.cursors.Load(t.Context(), nextKey)
	var history groupCursorHistory
	_ = json.Unmarshal(data, &history)
	if !history.Expires.Equal(legacy.Expires) {
		t.Fatal("legacy continuation extended expiry")
	}
	replayed, err := e.GraphEdges(t.Context(), a, scope, "graph", GraphEdgeQuery{Limit: 100}, "r."+key)
	if err != nil || !reflect.DeepEqual(page.Items, replayed.Items) {
		t.Fatal("legacy Back selector was destroyed", err)
	}
}

func TestGroupTraversalPositionRejectsMalformedSelectors(t *testing.T) {
	key := strings.Repeat("A", 43)
	if got, position, err := groupPosition("g." + key + ".1"); err != nil || got != key || position != 1 {
		t.Fatal("canonical position rejected")
	}
	for _, token := range []string{"g." + key, "g." + key + ".0", "g." + key + ".01", "g." + key + ".-1", "g." + key + ".100001", "g." + key + ".1.extra", "g." + key[:42] + "B.1", "g." + key[:42] + ".1"} {
		if _, _, err := groupPosition(token); err == nil {
			t.Fatal("noncanonical/out-of-range position accepted")
		}
	}
}
