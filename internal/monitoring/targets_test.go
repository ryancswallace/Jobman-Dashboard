package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type targetFixture struct {
	*testSource
	noCapability  bool
	targetRows    []api.Target
	generation    string
	partitionHook func()
	corrupt       string
}

func newTargetFixture(id string, n int) *targetFixture {
	s := &targetFixture{testSource: source(id, 0, time.Minute), generation: "generation-1"}
	for i := 0; i < n; i++ {
		s.targetRows = append(s.targetRows, api.Target{Scope: api.Scope{DeploymentID: id, NamespaceID: "ns"}, TargetID: fmt.Sprintf("target-%03d", n-i), Name: "configured", Kind: "slurm", State: "active", Revision: "1", CreatedAt: clock.Add(-time.Duration(i) * time.Minute), UpdatedAt: clock, AsOf: clock, Generation: api.TargetGeneration{ID: s.generation, Number: "9007199254740993", PartitionCount: "7"}})
	}
	return s
}
func (s *targetFixture) Discover(ctx context.Context, a Actor) (Discovery, error) {
	d, err := s.testSource.Discover(ctx, a)
	if !s.noCapability {
		for i := range d.Deployment.Namespaces {
			d.Deployment.Namespaces[i].Capabilities = append(d.Deployment.Namespaces[i].Capabilities, "targets.read")
		}
	}
	return d, err
}
func (s *targetFixture) Targets(_ context.Context, _ Actor, q TargetSourceQuery) (TargetSourcePage, error) {
	if s.fail {
		return TargetSourcePage{}, ErrSource
	}
	start, _ := strconv.Atoi(q.Cursor)
	if start < 0 || start > len(s.targetRows) {
		return TargetSourcePage{}, ErrCursor
	}
	end := min(start+q.Limit, len(s.targetRows))
	if s.pageSize > 0 {
		end = min(end, start+s.pageSize)
	}
	p := TargetSourcePage{Items: append([]api.Target{}, s.targetRows[start:end]...), Total: strconv.Itoa(len(s.targetRows)), AsOf: clock}
	if end < len(s.targetRows) {
		p.NextCursor = strconv.Itoa(end)
	}
	switch s.corrupt {
	case "scope":
		p.Items[0].NamespaceID = "other"
	case "loop":
		p.NextCursor = q.Cursor
	case "total":
		p.Total = ""
	}
	if s.hook != nil {
		s.hook()
	}
	return p, nil
}
func (s *targetFixture) Target(_ context.Context, _ Actor, scope api.Scope, id string) (api.Target, error) {
	for _, t := range s.targetRows {
		if t.Scope == scope && t.TargetID == id {
			return t, nil
		}
	}
	return api.Target{}, ErrNotFound
}
func (s *targetFixture) TargetPartitions(_ context.Context, _ Actor, q TargetPartitionQuery) (TargetPartitionSourcePage, error) {
	if s.generation != q.GenerationID {
		return TargetPartitionSourcePage{}, ErrTargetChanged
	}
	start, _ := strconv.Atoi(q.Cursor)
	end := min(start+q.Limit, 7)
	p := TargetPartitionSourcePage{Items: []api.TargetPartition{}, Total: "7", AsOf: clock}
	for i := start; i < end; i++ {
		p.Items = append(p.Items, api.TargetPartition{Name: fmt.Sprintf("partition-%d", i), IsDefault: i == 0})
	}
	if end < 7 {
		p.NextCursor = strconv.Itoa(end)
	}
	if s.partitionHook != nil {
		s.partitionHook()
	}
	return p, nil
}
func TestTargetsAggregatePagingReplayAndPartialSources(t *testing.T) {
	a, b := newTargetFixture("a", 7), newTargetFixture("b", 5)
	e := groupEngine(t, a, b)
	q := TargetQuery{Limit: 3}
	first, err := e.Targets(t.Context(), actor("alice"), q, "")
	if err != nil || first.Total != "12" || len(first.Totals) != 2 || len(first.Items) != 3 {
		t.Fatalf("first %+v %v", first, err)
	}
	seen := map[string]bool{}
	p := first
	for {
		for _, target := range p.Items {
			key := target.DeploymentID + target.TargetID
			if seen[key] {
				t.Fatal("duplicate", key)
			}
			seen[key] = true
		}
		if p.NextCursor == "" {
			break
		}
		cursor := p.NextCursor
		p, err = e.Targets(t.Context(), actor("alice"), q, cursor)
		if err != nil {
			t.Fatal(err)
		}
		replay, err := e.Targets(t.Context(), actor("alice"), q, cursor)
		if err != nil || !reflect.DeepEqual(p, replay) {
			t.Fatal("replay differs", err)
		}
	}
	if len(seen) != 12 {
		t.Fatalf("lost targets %d", len(seen))
	}
	b.fail = true
	partial, err := e.Targets(t.Context(), actor("alice"), q, "")
	if err != nil || partial.Total != "7" || partial.Completeness != "partial" || len(partial.Totals) != 1 || len(partial.Sources) != 2 {
		t.Fatalf("partial %+v %v", partial, err)
	}
}
func TestTargetCatalogAndPartitionsCursorIsolation(t *testing.T) {
	for _, mode := range []string{"actor", "limit", "scope", "grant", "epoch", "capability", "revoked", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			s := newTargetFixture("a", 7)
			e := groupEngine(t, s)
			a := actor("alice")
			q := TargetQuery{Limit: 2}
			p, err := e.Targets(t.Context(), a, q, "")
			if err != nil {
				t.Fatal(err)
			}
			pq := TargetPartitionQuery{Scope: api.Scope{DeploymentID: "a", NamespaceID: "ns"}, TargetID: s.targetRows[0].TargetID, GenerationID: s.generation, Limit: 2}
			parts, err := e.TargetPartitions(t.Context(), a, pq, "")
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "actor":
				a = actor("bob")
			case "limit":
				q.Limit = 1
				pq.Limit = 1
			case "scope":
				q.Scopes = []api.Scope{{DeploymentID: "a", NamespaceID: "other"}}
				pq.Scope.NamespaceID = "other"
			case "grant":
				s.version = "2"
			case "epoch":
				s.epoch = "2"
			case "capability":
				s.noCapability = true
			case "revoked":
				s.revoked = true
			case "expiry":
				e.Now = func() time.Time { return clock.Add(16 * time.Minute) }
			}
			if out, err := e.Targets(t.Context(), a, q, p.NextCursor); err == nil || len(out.Items) > 0 {
				t.Fatalf("unsafe catalog %+v %v", out, err)
			}
			if out, err := e.TargetPartitions(t.Context(), a, pq, parts.NextCursor); err == nil || len(out.Items) > 0 {
				t.Fatalf("unsafe partition %+v %v", out, err)
			}
		})
	}
}
func TestTargetGenerationChangeAndFinalAuthorization(t *testing.T) {
	for _, mode := range []string{"target", "generation", "replacement", "final-authorization"} {
		t.Run(mode, func(t *testing.T) {
			s := newTargetFixture("a", 1)
			e := groupEngine(t, s)
			q := TargetPartitionQuery{Scope: s.targetRows[0].Scope, TargetID: s.targetRows[0].TargetID, GenerationID: s.generation, Limit: 2}
			p, err := e.TargetPartitions(t.Context(), actor("alice"), q, "")
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "target":
				q.TargetID = "different"
			case "generation":
				q.GenerationID = "different"
			case "replacement":
				s.generation = "different"
			case "final-authorization":
				s.partitionHook = func() { s.version = "2" }
			}
			out, err := e.TargetPartitions(t.Context(), actor("alice"), q, p.NextCursor)
			if err == nil || len(out.Items) > 0 {
				t.Fatalf("unsafe %+v %v", out, err)
			}
		})
	}
}
func TestTargetsResponseByteBudgetPreservesContinuation(t *testing.T) {
	s := newTargetFixture("a", 4)
	s.pageSize = 1
	for i := range s.targetRows {
		s.targetRows[i].Generation.Capabilities = []string{strings.Repeat("x", 700000)}
	}
	e := groupEngine(t, s)
	q := TargetQuery{Limit: 4}
	p, err := e.Targets(t.Context(), actor("alice"), q, "")
	if err != nil || len(p.Items) != 2 || p.NextCursor == "" {
		t.Fatalf("bounded first page %+v %v", len(p.Items), err)
	}
	raw, _ := json.Marshal(p.Items)
	if len(raw) > 2<<20 {
		t.Fatal("unbounded response")
	}
	next, err := e.Targets(t.Context(), actor("alice"), q, p.NextCursor)
	if err != nil || len(next.Items) != 2 || next.NextCursor != "" || next.Items[0].TargetID == p.Items[1].TargetID {
		t.Fatalf("bounded continuation %+v %v", len(next.Items), err)
	}
}

func TestTargetSourceQualificationDoesNotRejectLegalRawByteFilledPage(t *testing.T) {
	s := newTargetFixture("a", 4)
	rawBytes := func(items []api.Target) int {
		size := 2
		for _, item := range items {
			raw, _ := json.Marshal(item)
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			delete(m, "deploymentId")
			delete(m, "namespaceId")
			delete(m, "asOf")
			m["id"] = m["targetId"]
			delete(m, "targetId")
			raw, _ = json.Marshal(m)
			size += len(raw) + 1
		}
		return size
	}
	// Control accepts long AWS-region strings matching its existing regex. Its
	// raw catalog fits 2MiB; adding source qualification crosses that boundary.
	for i := range s.targetRows {
		s.targetRows[i].Generation.Provider = api.TargetProvider{Kind: "aws-parallelcluster", Region: "us-a-1", ClusterName: "Research"}
	}
	padding := ((2 << 20) - rawBytes(s.targetRows) - 100) / len(s.targetRows)
	for i := range s.targetRows {
		s.targetRows[i].Generation.Provider.Region = "us-" + strings.Repeat("a", padding+1) + "-1"
	}
	if n := rawBytes(s.targetRows); n > 2<<20 || n < (2<<20)-200 {
		t.Fatalf("fixture not near raw bound: %d", n)
	}
	enriched, _ := json.Marshal(s.targetRows)
	if len(enriched) <= 2<<20 {
		t.Fatal("fixture did not cross enrichment bound")
	}
	e := groupEngine(t, s)
	q := TargetQuery{Limit: 4}
	first, err := e.Targets(t.Context(), actor("alice"), q, "")
	if err != nil || len(first.Items) != 3 || first.NextCursor == "" {
		t.Fatalf("enriched page rejected: %d %v", len(first.Items), err)
	}
	second, err := e.Targets(t.Context(), actor("alice"), q, first.NextCursor)
	if err != nil || len(second.Items) != 1 || second.Items[0].TargetID != s.targetRows[3].TargetID || second.NextCursor != "" {
		t.Fatalf("enriched continuation lost: %d %v", len(second.Items), err)
	}
	// A single legal near-bound row is delivered within the 3MiB exception.
	s = newTargetFixture("a", 1)
	s.targetRows[0].Generation.Provider = api.TargetProvider{Kind: "aws-parallelcluster", Region: "us-a-1", ClusterName: "Research"}
	n := (2 << 20) - rawBytes(s.targetRows) - 10
	s.targetRows[0].Generation.Provider.Region = "us-" + strings.Repeat("a", n+1) + "-1"
	e = groupEngine(t, s)
	single, err := e.Targets(t.Context(), actor("alice"), TargetQuery{Limit: 1}, "")
	if err != nil || len(single.Items) != 1 || single.NextCursor != "" {
		t.Fatalf("single row rejected: %d %v", len(single.Items), err)
	}
}

func TestTargetCursorKeepsMaximumScopeMetadataWithinExistingEncodingCeiling(t *testing.T) {
	state := targetBrowseState{CursorIdentity: CursorIdentity{AccountID: "11111111-1111-4111-8111-111111111111", QueryHash: strings.Repeat("a", 64)}, Expires: clock.Add(time.Minute)}
	for i := 0; i < 320; i++ {
		scope := api.Scope{DeploymentID: fmt.Sprintf("%08d-1111-4111-8111-111111111111", i/10), NamespaceID: fmt.Sprintf("%08d-2222-4222-8222-222222222222", i)}
		state.Buffers = append(state.Buffers, targetBuffer{Scope: scope, Authority: strings.Repeat("a", 100), Cutoff: clock, Total: "9223372036854775807", Status: api.SourceStatus{Scope: scope, Status: "available", AsOf: &clock, FetchedAt: clock}})
	}
	// Largest enriched page plus all admitted scope metadata must fit the old
	// 4MiB cursor ceiling. Memoized replies clear the unconsumed source buffer.
	row := newTargetFixture("a", 1).targetRows[0]
	row.Generation.Provider.Region = strings.Repeat("a", 3<<20-4096)
	state.Buffers[0].Rows = []api.Target{row}
	data, err := encodeState(state)
	if err != nil || len(data) > 4<<20 {
		t.Fatalf("bounded state encoding %d %v", len(data), err)
	}
	state.Buffers[0].Rows = nil
	state.Response = &api.TargetPage{Page: api.Page[api.Target]{Items: []api.Target{row}}, Total: "1", Totals: []api.TargetTotal{}}
	data, err = encodeState(state)
	if err != nil || len(data) > 4<<20 {
		t.Fatalf("bounded replay encoding %d %v", len(data), err)
	}
}

// This source models Control's committed-visible watermark, including an insert
// committed after the metadata probe but before the first data-page request.
type watermarkTargetFixture struct {
	*targetFixture
	queries []TargetSourceQuery
	change  time.Duration
}

func (s *watermarkTargetFixture) Targets(_ context.Context, _ Actor, q TargetSourceQuery) (TargetSourcePage, error) {
	s.queries = append(s.queries, q)
	if len(s.queries) == 2 {
		late := s.targetRows[0]
		late.TargetID = "late-insert"
		late.CreatedAt = late.CreatedAt.Add(time.Second)
		s.targetRows = append([]api.Target{late}, s.targetRows...)
	}
	cutoff := minTime(q.CreatedBefore, s.targetRows[0].CreatedAt)
	if len(s.queries) > 1 {
		cutoff = cutoff.Add(s.change)
	}
	rows := []api.Target{}
	for _, row := range s.targetRows {
		if !row.CreatedAt.After(cutoff) {
			rows = append(rows, row)
		}
	}
	start, _ := strconv.Atoi(q.Cursor)
	if start > len(rows) {
		return TargetSourcePage{}, ErrSource
	}
	end := min(start+q.Limit, len(rows))
	p := TargetSourcePage{Items: rows[start:end], Total: strconv.Itoa(len(rows)), AsOf: clock, CreatedBefore: cutoff}
	if end < len(rows) {
		p.NextCursor = strconv.Itoa(end)
	}
	return p, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func TestTargetsPinEachSourceWatermarkBeforeDataAndReplay(t *testing.T) {
	a := &watermarkTargetFixture{targetFixture: newTargetFixture("a", 3)}
	b := &watermarkTargetFixture{targetFixture: newTargetFixture("b", 3)}
	for _, item := range []struct {
		source *watermarkTargetFixture
		age    time.Duration
	}{{a, time.Hour}, {b, 2 * time.Hour}} {
		for i := range item.source.targetRows {
			item.source.targetRows[i].CreatedAt = item.source.targetRows[i].CreatedAt.Add(-item.age)
		}
	}
	e := groupEngine(t, a, b)
	q := TargetQuery{Limit: 2}
	page, err := e.Targets(t.Context(), actor("alice"), q, "")
	seen := map[string]bool{}
	for pages := 0; ; pages++ {
		if err != nil || pages > 5 || page.Total != "6" || page.Completeness != "complete" {
			t.Fatal("stable source watermarks were not preserved", err)
		}
		for _, item := range page.Items {
			key := item.DeploymentID + item.TargetID
			if item.TargetID == "late-insert" || seen[key] {
				t.Fatal("late or duplicate target entered the pinned browse")
			}
			seen[key] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor := page.NextCursor
		page, err = e.Targets(t.Context(), actor("alice"), q, cursor)
		before := len(a.queries) + len(b.queries)
		replay, replayErr := e.Targets(t.Context(), actor("alice"), q, cursor)
		if replayErr != nil || !reflect.DeepEqual(page, replay) || len(a.queries)+len(b.queries) != before {
			t.Fatal("replay changed the page or refetched source rows", replayErr)
		}
	}
	if len(seen) != 6 {
		t.Fatal("pinned pagination lost an original target")
	}
	for _, item := range []struct {
		source *watermarkTargetFixture
		cutoff time.Time
	}{{a, clock.Add(-time.Hour)}, {b, clock.Add(-2 * time.Hour)}} {
		if len(item.source.queries) < 3 || item.source.queries[1].Cursor != "" {
			t.Fatal("test did not exercise probe, first data fetch and continuation")
		}
		for _, query := range item.source.queries[1:] {
			if !query.CreatedBefore.Equal(item.cutoff) {
				t.Fatal("source-specific effective cutoff was not persisted")
			}
		}
	}
}

func TestTargetsRejectWatermarkChangeAfterProbe(t *testing.T) {
	for _, change := range []time.Duration{-time.Second, time.Second} {
		t.Run(change.String(), func(t *testing.T) {
			source := &watermarkTargetFixture{targetFixture: newTargetFixture("a", 3), change: change}
			for i := range source.targetRows {
				source.targetRows[i].CreatedAt = source.targetRows[i].CreatedAt.Add(-time.Hour)
			}
			e := groupEngine(t, source)
			if _, err := e.Targets(t.Context(), actor("alice"), TargetQuery{Limit: 2}, ""); err == nil {
				t.Fatal("changed watermark accepted between probe and data fetch")
			}
		})
	}
}
