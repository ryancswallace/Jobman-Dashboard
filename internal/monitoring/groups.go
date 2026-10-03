package monitoring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

var errGroupsUnsupported = failure("unsupported_contract", "This source does not support bounded workload monitoring.")

func groupHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(append([]byte("workloads/v1:"), b...))
	return hex.EncodeToString(sum[:])
}
func validDecimal(s string) bool {
	if s == "" || len(s) > 40 || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func compareWorkloads(a, b api.Workload) int {
	return compareJobs(api.Job{Scope: a.Scope, ID: a.ID, CreatedAt: a.CreatedAt}, api.Job{Scope: b.Scope, ID: b.ID, CreatedAt: b.CreatedAt})
}

func (e *Engine) groupAuthorize(scopes []api.Scope, ds map[string]discoveryResult) ([]api.Scope, map[api.Scope]string, error) {
	selected, grants, err := e.authorize(Query{Scopes: scopes}, ds)
	if err != nil {
		return nil, nil, err
	}
	for scope := range grants {
		found := false
		for _, ns := range ds[scope.DeploymentID].value.Deployment.Namespaces {
			if ns.ID == scope.NamespaceID && slices.Contains(ns.Capabilities, "groups.read") {
				found = true
				break
			}
		}
		if !found {
			delete(grants, scope)
		}
	}
	result := []api.Scope{}
	for _, scope := range selected {
		if grants[scope] != "" || ds[scope.DeploymentID].err != nil || scope.NamespaceID == "" {
			result = append(result, scope)
		} else if scopes != nil {
			return nil, nil, ErrForbidden
		}
	}
	return result, grants, nil
}

type workloadBuffer struct {
	Scope     api.Scope        `json:"scope"`
	Authority string           `json:"authority"`
	Cutoff    time.Time        `json:"cutoff"`
	Cursor    string           `json:"cursor"`
	Rows      []api.Workload   `json:"rows"`
	Done      bool             `json:"done"`
	Excluded  bool             `json:"excluded"`
	Total     string           `json:"total"`
	Status    api.SourceStatus `json:"status"`
	Last      *api.Workload    `json:"last,omitempty"`
}
type workloadBrowseState struct {
	CursorIdentity
	Expires  time.Time         `json:"expires"`
	Buffers  []workloadBuffer  `json:"buffers"`
	Response *api.WorkloadPage `json:"response,omitempty"`
}

func (e *Engine) fillWorkloads(ctx context.Context, a Actor, q GroupQuery, b *workloadBuffer) error {
	if len(b.Rows) > 0 || b.Done || b.Excluded {
		return nil
	}
	src, ok := e.sources[b.Scope.DeploymentID].(GroupSource)
	if !ok {
		return errGroupsUnsupported
	}
	var page WorkloadSourcePage
	err := e.call(ctx, b.Scope.DeploymentID, func(c context.Context) error {
		var err error
		page, err = src.Workloads(c, a, GroupSourceQuery{GroupQuery: q, NamespaceID: b.Scope.NamespaceID, CreatedBefore: b.Cutoff, Cursor: b.Cursor})
		return err
	})
	if err != nil {
		return err
	}
	if len(page.Items) > q.Limit || page.AsOf.IsZero() || !validDecimal(page.Total) || len(page.NextCursor) > 8192 || (page.NextCursor != "" && (page.NextCursor == b.Cursor || len(page.Items) == 0)) {
		return errGroupsUnsupported
	}
	last := b.Last
	for i := range page.Items {
		w := &page.Items[i]
		if w.Scope != b.Scope || w.ID == "" || w.Kind != q.Kind || w.CreatedAt.IsZero() || w.CreatedAt.After(b.Cutoff) || w.AsOf.IsZero() || !validDecimal(w.TotalChildren) || (last != nil && compareWorkloads(*last, *w) >= 0) {
			return errGroupsUnsupported
		}
		last = w
	}
	if last != nil {
		copy := *last
		b.Last = &copy
	}
	b.Rows = page.Items
	b.Cursor = page.NextCursor
	b.Done = page.NextCursor == ""
	b.Total = page.Total
	b.Status = api.SourceStatus{Scope: b.Scope, Status: "available", AsOf: &page.AsOf, FetchedAt: e.Now()}
	return nil
}
func excludeWorkloadBuffer(b *workloadBuffer, now time.Time, err error) {
	b.Excluded = true
	b.Rows = nil
	b.Status = statusFor(b.Scope, now, err)
}

// Workloads merges live, source-qualified catalogs using immutable creation/UUID
// keysets. The cutoff, authorization and unconsumed rows stay in server storage.
func (e *Engine) Workloads(ctx context.Context, a Actor, q GroupQuery, cursor string) (api.WorkloadPage, error) {
	page := api.WorkloadPage{Page: api.Page[api.Workload]{Items: []api.Workload{}, Sources: []api.SourceStatus{}, Completeness: "complete", FetchedAt: e.Now()}, Total: "0", Totals: []api.WorkloadTotal{}}
	if !ValidGroupKind(q.Kind) {
		return page, ErrNotFound
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 200 {
		return page, failure("invalid_request", "Workload page size must be between 1 and 200.")
	}
	q.Scopes = slices.Clone(q.Scopes)
	slices.SortFunc(q.Scopes, scopeCompare)
	q.Scopes = slices.Compact(q.Scopes)
	hash := groupHash(q)
	original := q
	ds := e.discover(ctx, a)
	scopes, grants, err := e.groupAuthorize(q.Scopes, ds)
	if err != nil {
		return page, err
	}
	state := workloadBrowseState{CursorIdentity: CursorIdentity{AccountID: a.Account.ID, QueryHash: hash, Initial: cursor == ""}, Expires: e.Now().Add(15 * time.Minute)}
	key := ""
	version := uint64(0)
	if cursor != "" {
		if !strings.HasPrefix(cursor, "w.") || len(cursor) > 512 {
			return page, ErrCursor
		}
		key = strings.TrimPrefix(cursor, "w.")
		data, v, err := e.cursors.Load(ctx, key)
		if err != nil {
			return page, ErrCursor
		}
		version = v
		if json.Unmarshal(data, &state) != nil || state.AccountID != a.Account.ID || state.QueryHash != hash || !e.Now().Before(state.Expires) {
			return page, ErrCursor
		}
		authorities := map[api.Scope]string{}
		for i := range state.Buffers {
			b := &state.Buffers[i]
			authorities[b.Scope] = b.Authority
			if ds[b.Scope.DeploymentID].err != nil {
				if state.Response != nil {
					return page, ErrCursor
				}
				excludeWorkloadBuffer(b, e.Now(), ErrAuthority)
			} else if !b.Excluded && (grants[b.Scope] == "" || grants[b.Scope] != b.Authority) {
				return page, ErrCursor
			}
		}
		if state.Response != nil {
			for _, w := range state.Response.Items {
				if grants[w.Scope] == "" || grants[w.Scope] != authorities[w.Scope] {
					return page, ErrCursor
				}
			}
			return *state.Response, nil
		}
		state.Initial = false
	} else {
		for _, scope := range scopes {
			b := workloadBuffer{Scope: scope, Authority: grants[scope], Cutoff: ds[scope.DeploymentID].value.ServiceTime}
			if b.Authority == "" {
				excludeWorkloadBuffer(&b, e.Now(), ErrAuthority)
			}
			state.Buffers = append(state.Buffers, b)
		}
	}
	q.Scopes = []api.Scope{}
	for _, b := range state.Buffers {
		q.Scopes = append(q.Scopes, b.Scope)
	}
	var wg sync.WaitGroup
	fanout := make(chan struct{}, 4)
	for i := range state.Buffers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fanout <- struct{}{}
			defer func() { <-fanout }()
			b := &state.Buffers[i]
			if err := e.fillWorkloads(ctx, a, q, b); err != nil {
				excludeWorkloadBuffer(b, e.Now(), err)
			}
		}()
	}
	wg.Wait()
	for len(page.Items) < q.Limit {
		best := -1
		for i := range state.Buffers {
			b := &state.Buffers[i]
			if err := e.fillWorkloads(ctx, a, q, b); err != nil {
				excludeWorkloadBuffer(b, e.Now(), err)
			}
			if len(b.Rows) > 0 && (best < 0 || compareWorkloads(b.Rows[0], state.Buffers[best].Rows[0]) < 0) {
				best = i
			}
		}
		if best < 0 {
			break
		}
		b := &state.Buffers[best]
		page.Items = append(page.Items, b.Rows[0])
		b.Rows = b.Rows[1:]
	}
	final := e.discover(ctx, a)
	_, current, err := e.groupAuthorize(q.Scopes, final)
	if err != nil {
		return page, err
	}
	success := len(state.Buffers) == 0
	more := false
	total := new(big.Int)
	for i := range state.Buffers {
		b := &state.Buffers[i]
		if final[b.Scope.DeploymentID].err != nil {
			excludeWorkloadBuffer(b, e.Now(), ErrAuthority)
		} else if !b.Excluded && current[b.Scope] != b.Authority {
			return page, ErrCursor
		}
		if b.Excluded {
			page.Items = slices.DeleteFunc(page.Items, func(w api.Workload) bool { return w.Scope == b.Scope })
			page.Completeness = "partial"
		} else {
			success = true
			more = more || len(b.Rows) > 0 || !b.Done
			n, ok := new(big.Int).SetString(b.Total, 10)
			if !ok {
				return page, errGroupsUnsupported
			}
			total.Add(total, n)
			page.Totals = append(page.Totals, api.WorkloadTotal{Scope: b.Scope, Total: b.Total, AsOf: *b.Status.AsOf})
		}
		page.Sources = append(page.Sources, b.Status)
	}
	if !success {
		return page, ErrSource
	}
	page.Total = total.String()
	next := ""
	if more {
		data, err := encodeState(state)
		if err != nil {
			return page, err
		}
		next, err = e.cursors.Create(ctx, data, state.Expires)
		if err != nil {
			return page, err
		}
		page.NextCursor = "w." + next
	}
	if cursor != "" {
		state.Response = &page
		for i := range state.Buffers {
			state.Buffers[i].Rows = nil
			state.Buffers[i].Last = nil
		}
		data, err := encodeState(state)
		if err == nil {
			err = e.cursors.Advance(ctx, key, version, data)
		}
		if err != nil {
			if next != "" {
				_ = e.cursors.Advance(ctx, next, 1, nil)
			}
			if errors.Is(err, ErrCursor) {
				return e.Workloads(ctx, a, original, cursor)
			}
			return page, err
		}
	}
	return page, nil
}

type groupResourceQuery struct {
	Scope     api.Scope `json:"scope"`
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Operation string    `json:"operation"`
	Limit     int       `json:"limit"`
	NodeID    string    `json:"nodeId,omitempty"`
	Direction string    `json:"direction,omitempty"`
}
type groupResourceCursor struct {
	CursorIdentity
	Expires      time.Time `json:"expires"`
	Authority    string    `json:"authority"`
	AfterIndex   int       `json:"afterIndex"`
	SourceCursor string    `json:"sourceCursor"`
	LastEdge     string    `json:"lastEdge,omitempty"`
}

func (e *Engine) authorizedGroup(ctx context.Context, a Actor, scope api.Scope) (GroupSource, string, error) {
	_, grants, err := e.groupAuthorize([]api.Scope{scope}, e.discover(ctx, a))
	if err != nil {
		return nil, "", err
	}
	if grants[scope] == "" {
		return nil, "", ErrAuthority
	}
	src, ok := e.sources[scope.DeploymentID].(GroupSource)
	if !ok {
		return nil, "", errGroupsUnsupported
	}
	return src, grants[scope], nil
}
func (e *Engine) groupCursor(ctx context.Context, a Actor, q groupResourceQuery, authority, cursor string) (groupResourceCursor, error) {
	state := groupResourceCursor{CursorIdentity: CursorIdentity{AccountID: a.Account.ID, QueryHash: groupHash(q), Initial: cursor == ""}, Expires: e.Now().Add(15 * time.Minute), Authority: authority, AfterIndex: -1}
	if cursor == "" {
		return state, nil
	}
	if !strings.HasPrefix(cursor, "r.") || len(cursor) > 512 {
		return state, ErrCursor
	}
	key := strings.TrimPrefix(cursor, "r.")
	data, version, err := e.cursors.Load(ctx, key)
	if err != nil {
		return state, ErrCursor
	}
	expected := state
	if json.Unmarshal(data, &state) != nil || state.AccountID != expected.AccountID || state.QueryHash != expected.QueryHash || state.Authority != authority || !e.Now().Before(state.Expires) {
		return state, ErrCursor
	}
	// Mark a visited initial continuation so polling cannot evict Back history.
	if version == 1 && state.Initial {
		if err = e.cursors.Advance(ctx, key, version, data); err != nil && !errors.Is(err, ErrCursor) {
			return state, err
		}
	}
	state.Initial = false
	return state, nil
}
func (e *Engine) saveGroupCursor(ctx context.Context, state groupResourceCursor) (string, error) {
	data, err := encodeState(state)
	if err != nil {
		return "", err
	}
	key, err := e.cursors.Create(ctx, data, state.Expires)
	if err != nil {
		return "", err
	}
	return "r." + key, nil
}
func (e *Engine) recheckGroup(ctx context.Context, a Actor, scope api.Scope, before string) error {
	_, after, err := e.authorizedGroup(ctx, a, scope)
	if err != nil {
		return err
	}
	if before != after {
		return ErrCursor
	}
	return nil
}
func (e *Engine) groupStatus(scope api.Scope, asOf time.Time) []api.SourceStatus {
	return []api.SourceStatus{{Scope: scope, Status: "available", AsOf: &asOf, FetchedAt: e.Now()}}
}

func (e *Engine) WorkloadChildren(ctx context.Context, a Actor, scope api.Scope, kind, id string, limit int, cursor string) (api.WorkloadChildrenPage, error) {
	out := api.WorkloadChildrenPage{Page: api.Page[api.WorkloadChild]{Items: []api.WorkloadChild{}, Sources: []api.SourceStatus{}, Completeness: "complete", FetchedAt: e.Now()}}
	if !ValidGroupKind(kind) || id == "" {
		return out, ErrNotFound
	}
	if limit < 1 || limit > 200 {
		return out, failure("invalid_request", "Child page size must be between 1 and 200.")
	}
	src, authority, err := e.authorizedGroup(ctx, a, scope)
	if err != nil {
		return out, err
	}
	q := groupResourceQuery{Scope: scope, Kind: kind, ID: id, Operation: "children", Limit: limit}
	state, err := e.groupCursor(ctx, a, q, authority, cursor)
	if err != nil {
		return out, err
	}
	var page WorkloadChildSourcePage
	err = e.call(ctx, scope.DeploymentID, func(c context.Context) error {
		var err error
		page, err = src.WorkloadChildren(c, a, scope, kind, id, state.AfterIndex, limit)
		return err
	})
	if err != nil {
		return out, err
	}
	if len(page.Items) > limit || page.AsOf.IsZero() || !validDecimal(page.Total) {
		return out, errGroupsUnsupported
	}
	last := state.AfterIndex
	seen := map[string]bool{}
	for _, child := range page.Items {
		index, err := strconv.Atoi(child.Index)
		if err != nil || index <= last || index > 9999 || child.ID == "" || child.ID != child.Job.ID || child.Job.Scope != scope || seen[child.ID] {
			return out, errGroupsUnsupported
		}
		last = index
		seen[child.ID] = true
	}
	if page.NextIndex != nil && (*page.NextIndex != last || len(page.Items) == 0) {
		return out, errGroupsUnsupported
	}
	if err = e.recheckGroup(ctx, a, scope, authority); err != nil {
		return out, err
	}
	out.Items = page.Items
	if out.Items == nil {
		out.Items = []api.WorkloadChild{}
	}
	out.Total = page.Total
	out.Sources = e.groupStatus(scope, page.AsOf)
	if page.NextIndex != nil {
		state.AfterIndex = *page.NextIndex
		out.NextCursor, err = e.saveGroupCursor(ctx, state)
	}
	return out, err
}
func (e *Engine) Workload(ctx context.Context, a Actor, scope api.Scope, kind, id string, limit int, cursor string) (api.WorkloadDetail, error) {
	out := api.WorkloadDetail{}
	if !ValidGroupKind(kind) || id == "" {
		return out, ErrNotFound
	}
	src, authority, err := e.authorizedGroup(ctx, a, scope)
	if err != nil {
		return out, err
	}
	err = e.call(ctx, scope.DeploymentID, func(c context.Context) error {
		var err error
		out.Workload, err = src.Workload(c, a, scope, kind, id)
		return err
	})
	if err != nil {
		return out, err
	}
	if out.Workload.Scope != scope || out.Workload.ID != id || out.Workload.Kind != kind || out.Workload.AsOf.IsZero() || !validDecimal(out.Workload.TotalChildren) {
		return api.WorkloadDetail{}, errGroupsUnsupported
	}
	children, err := e.WorkloadChildren(ctx, a, scope, kind, id, limit, cursor)
	if err != nil {
		return api.WorkloadDetail{}, err
	}
	if err = e.recheckGroup(ctx, a, scope, authority); err != nil {
		return api.WorkloadDetail{}, err
	}
	out.Children = children.Items
	out.Total = children.Total
	out.NextCursor = children.NextCursor
	out.Completeness = children.Completeness
	out.Sources = children.Sources
	out.FetchedAt = e.Now()
	return out, nil
}
func (e *Engine) GraphEdges(ctx context.Context, a Actor, scope api.Scope, id string, q GraphEdgeQuery, cursor string) (api.GraphEdgePage, error) {
	out := api.GraphEdgePage{Page: api.Page[api.GraphEdge]{Items: []api.GraphEdge{}, Completeness: "complete", Sources: []api.SourceStatus{}, FetchedAt: e.Now()}}
	if id == "" || q.Limit < 1 || q.Limit > 500 || (q.Direction != "" && q.NodeID == "") || !slices.Contains([]string{"", "incoming", "outgoing"}, q.Direction) {
		return out, failure("invalid_request", "Dependency filters or page size are invalid.")
	}
	src, authority, err := e.authorizedGroup(ctx, a, scope)
	if err != nil {
		return out, err
	}
	state, err := e.groupCursor(ctx, a, groupResourceQuery{Scope: scope, Kind: "graph", ID: id, Operation: "edges", Limit: q.Limit, NodeID: q.NodeID, Direction: q.Direction}, authority, cursor)
	if err != nil {
		return out, err
	}
	q.Cursor = state.SourceCursor
	var page GraphEdgeSourcePage
	err = e.call(ctx, scope.DeploymentID, func(c context.Context) error {
		var err error
		page, err = src.GraphEdges(c, a, scope, id, q)
		return err
	})
	if err != nil {
		return out, err
	}
	if len(page.Items) > q.Limit || page.AsOf.IsZero() || !validDecimal(page.Total) || len(page.NextCursor) > 8192 || (page.NextCursor != "" && (page.NextCursor == state.SourceCursor || len(page.Items) == 0)) {
		return out, errGroupsUnsupported
	}
	last := state.LastEdge
	for _, edge := range page.Items {
		key := edge.FromJobID + "/" + edge.ToJobID
		if edge.FromJobID == "" || edge.ToJobID == "" || (last != "" && key <= last) || (q.NodeID != "" && edge.FromJobID != q.NodeID && edge.ToJobID != q.NodeID) || (q.Direction == "incoming" && edge.ToJobID != q.NodeID) || (q.Direction == "outgoing" && edge.FromJobID != q.NodeID) {
			return out, errGroupsUnsupported
		}
		last = key
	}
	if err = e.recheckGroup(ctx, a, scope, authority); err != nil {
		return out, err
	}
	out.Items = page.Items
	if out.Items == nil {
		out.Items = []api.GraphEdge{}
	}
	out.Total = page.Total
	out.Sources = e.groupStatus(scope, page.AsOf)
	if page.NextCursor != "" {
		state.SourceCursor = page.NextCursor
		state.LastEdge = last
		out.NextCursor, err = e.saveGroupCursor(ctx, state)
	}
	return out, err
}
func (e *Engine) GraphNeighborhood(ctx context.Context, a Actor, scope api.Scope, id, node string, maxNodes, maxEdges int) (api.GraphNeighborhood, error) {
	out := api.GraphNeighborhood{}
	if id == "" || node == "" || maxNodes < 1 || maxNodes > 200 || maxEdges < 1 || maxEdges > 500 {
		return out, failure("invalid_request", "Neighborhood identity or bounds are invalid.")
	}
	src, authority, err := e.authorizedGroup(ctx, a, scope)
	if err != nil {
		return out, err
	}
	var page GraphNeighborhoodSource
	err = e.call(ctx, scope.DeploymentID, func(c context.Context) error {
		var err error
		page, err = src.GraphNeighborhood(c, a, scope, id, node, maxNodes, maxEdges)
		return err
	})
	if err != nil {
		return out, err
	}
	if page.AsOf.IsZero() || page.CenterID != node || len(page.Nodes) > maxNodes || len(page.Edges) > maxEdges {
		return out, errGroupsUnsupported
	}
	ids := map[string]bool{}
	for _, child := range page.Nodes {
		if child.Job.Scope != scope || child.ID == "" || child.ID != child.Job.ID || ids[child.ID] {
			return out, errGroupsUnsupported
		}
		ids[child.ID] = true
	}
	if !ids[node] {
		return out, errGroupsUnsupported
	}
	edgeIDs := map[string]bool{}
	for _, edge := range page.Edges {
		key := edge.FromJobID + "/" + edge.ToJobID
		if !ids[edge.FromJobID] || !ids[edge.ToJobID] || edgeIDs[key] {
			return out, errGroupsUnsupported
		}
		edgeIDs[key] = true
	}
	for _, v := range []string{page.TotalNodes, page.TotalEdges, page.OmittedNodes, page.OmittedEdges} {
		if !validDecimal(v) {
			return out, errGroupsUnsupported
		}
	}
	for _, counts := range []struct {
		total, omitted string
		returned       int
	}{{page.TotalNodes, page.OmittedNodes, len(page.Nodes)}, {page.TotalEdges, page.OmittedEdges, len(page.Edges)}} {
		total, _ := new(big.Int).SetString(counts.total, 10)
		omitted, _ := new(big.Int).SetString(counts.omitted, 10)
		if total.Cmp(omitted.Add(omitted, big.NewInt(int64(counts.returned)))) != 0 {
			return out, errGroupsUnsupported
		}
	}
	if err = e.recheckGroup(ctx, a, scope, authority); err != nil {
		return out, err
	}
	out = page.GraphNeighborhood
	out.Completeness = "complete"
	out.Sources = e.groupStatus(scope, page.AsOf)
	out.FetchedAt = e.Now()
	return out, nil
}
