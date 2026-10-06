package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"math/big"
	"slices"
	"strings"
	"sync"
	"time"
)

func (e *Engine) targetAuthorize(scopes []api.Scope, ds map[string]discoveryResult) ([]api.Scope, map[api.Scope]string, error) {
	selected, grants, err := e.authorize(Query{Scopes: scopes}, ds)
	if err != nil {
		return nil, nil, err
	}
	for scope := range grants {
		found := false
		for _, ns := range ds[scope.DeploymentID].value.Deployment.Namespaces {
			if ns.ID == scope.NamespaceID && slices.Contains(ns.Capabilities, "targets.read") {
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

// Catalogs are grouped by source, then immutable creation/UUID order within a
// source. Only one source page is buffered, so large generation descriptions do
// not multiply by every authorized namespace.
type targetBuffer struct {
	Scope       api.Scope        `json:"scope"`
	Authority   string           `json:"authority"`
	Cutoff      time.Time        `json:"cutoff"`
	Cursor      string           `json:"cursor"`
	Rows        []api.Target     `json:"rows,omitempty"`
	LastID      string           `json:"lastId,omitempty"`
	LastCreated time.Time        `json:"lastCreated"`
	Total       string           `json:"total"`
	Done        bool             `json:"done"`
	Excluded    bool             `json:"excluded"`
	Status      api.SourceStatus `json:"status"`
}
type targetBrowseState struct {
	CursorIdentity
	Expires  time.Time       `json:"expires"`
	Buffers  []targetBuffer  `json:"buffers"`
	Response *api.TargetPage `json:"response,omitempty"`
}

func targetHash(v any) string {
	return groupHash(struct {
		Resource string
		Query    any
	}{"targets/v1", v})
}
func excludeTarget(b *targetBuffer, now time.Time, err error) {
	b.Excluded = true
	b.Rows = nil
	b.Status = statusFor(b.Scope, now, err)
}
func (e *Engine) readTargets(ctx context.Context, a Actor, b *targetBuffer, limit int) (TargetSourcePage, error) {
	src, ok := e.sources[b.Scope.DeploymentID].(TargetSource)
	if !ok {
		return TargetSourcePage{}, errTargetsUnsupported
	}
	var p TargetSourcePage
	err := e.call(ctx, b.Scope.DeploymentID, func(c context.Context) error {
		var err error
		p, err = src.Targets(c, a, TargetSourceQuery{NamespaceID: b.Scope.NamespaceID, Limit: limit, CreatedBefore: b.Cutoff, Cursor: b.Cursor})
		return err
	})
	if err != nil {
		return p, err
	}
	if p.CreatedBefore.IsZero() {
		p.CreatedBefore = b.Cutoff
	}
	// The initial probe may narrow the creation window. Once its total has
	// been recorded, even another first-page fetch must keep that same window.
	if p.CreatedBefore.IsZero() || p.CreatedBefore.After(b.Cutoff) || b.Total != "" && !p.CreatedBefore.Equal(b.Cutoff) {
		return TargetSourcePage{}, ErrSource
	}
	if p.Items == nil || len(p.Items) > limit || p.AsOf.IsZero() || !validDecimal(p.Total) || len(p.NextCursor) > 1024 || p.NextCursor != "" && (p.NextCursor == b.Cursor || len(p.Items) == 0) {
		return TargetSourcePage{}, ErrSource
	}
	total, _ := new(big.Int).SetString(p.Total, 10)
	if total.Cmp(big.NewInt(int64(len(p.Items)))) < 0 || p.NextCursor != "" && total.Cmp(big.NewInt(int64(len(p.Items)))) <= 0 {
		return TargetSourcePage{}, ErrSource
	}
	lastID, lastCreated := b.LastID, b.LastCreated
	size := 0
	for _, t := range p.Items {
		if t.Scope != b.Scope || t.TargetID == "" || t.CreatedAt.IsZero() || t.CreatedAt.After(p.CreatedBefore) || t.AsOf.IsZero() || t.Generation.ID == "" || lastID != "" && (t.CreatedAt.After(lastCreated) || t.CreatedAt.Equal(lastCreated) && t.TargetID >= lastID) {
			return TargetSourcePage{}, ErrSource
		}
		raw, _ := json.Marshal(t)
		size += len(raw)
		// Control budgets raw rows; source qualification adds bounded metadata.
		if size > 3<<20 {
			return TargetSourcePage{}, ErrSource
		}
		lastID, lastCreated = t.TargetID, t.CreatedAt
	}
	return p, nil
}
func (e *Engine) Targets(ctx context.Context, a Actor, q TargetQuery, cursor string) (api.TargetPage, error) {
	page := api.TargetPage{Page: api.Page[api.Target]{Items: []api.Target{}, Sources: []api.SourceStatus{}, Completeness: "complete", FetchedAt: e.Now()}, Total: "0", Totals: []api.TargetTotal{}}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 200 {
		return page, failure("invalid_request", "Target page size must be between 1 and 200.")
	}
	q.Scopes = slices.Clone(q.Scopes)
	slices.SortFunc(q.Scopes, scopeCompare)
	q.Scopes = slices.Compact(q.Scopes)
	hash := targetHash(q)
	ds := e.discover(ctx, a)
	scopes, grants, err := e.targetAuthorize(q.Scopes, ds)
	if err != nil {
		return page, err
	}
	state := targetBrowseState{CursorIdentity: CursorIdentity{AccountID: a.Account.ID, QueryHash: hash, Initial: cursor == ""}, Expires: e.Now().Add(15 * time.Minute)}
	key := ""
	version := uint64(0)
	if cursor != "" {
		if !strings.HasPrefix(cursor, "t.") || len(cursor) > 512 {
			return page, ErrCursor
		}
		key = strings.TrimPrefix(cursor, "t.")
		data, v, err := e.cursors.Load(ctx, key)
		if err != nil {
			return page, ErrCursor
		}
		version = v
		if json.Unmarshal(data, &state) != nil || state.AccountID != a.Account.ID || state.QueryHash != hash || !e.Now().Before(state.Expires) || len(state.Buffers) > 320 {
			return page, ErrCursor
		}
		for i := range state.Buffers {
			b := &state.Buffers[i]
			if b.Excluded {
				continue
			}
			if ds[b.Scope.DeploymentID].err != nil {
				if state.Response != nil {
					return page, ErrCursor
				}
				excludeTarget(b, e.Now(), ErrAuthority)
			} else if grants[b.Scope] == "" || grants[b.Scope] != b.Authority {
				return page, ErrCursor
			}
		}
		if state.Response != nil {
			return *state.Response, nil
		}
		state.Initial = false
	} else {
		for _, scope := range scopes {
			b := targetBuffer{Scope: scope, Authority: grants[scope], Cutoff: ds[scope.DeploymentID].value.ServiceTime}
			if b.Authority == "" {
				excludeTarget(&b, e.Now(), ErrAuthority)
			}
			state.Buffers = append(state.Buffers, b)
		}
		var wg sync.WaitGroup
		gate := make(chan struct{}, 4)
		for i := range state.Buffers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				gate <- struct{}{}
				defer func() { <-gate }()
				b := &state.Buffers[i]
				if b.Excluded {
					return
				}
				p, err := e.readTargets(ctx, a, b, 1)
				if err != nil {
					excludeTarget(b, e.Now(), err)
					return
				}
				b.Cutoff = p.CreatedBefore
				b.Total = p.Total
				b.Done = p.Total == "0"
				b.Status = api.SourceStatus{Scope: b.Scope, Status: "available", AsOf: &p.AsOf, FetchedAt: e.Now()}
			}()
		}
		wg.Wait()
	}
	bytes := 0
browse:
	for i := range state.Buffers {
		b := &state.Buffers[i]
		for !b.Excluded && (len(b.Rows) > 0 || !b.Done) && len(page.Items) < q.Limit {
			if len(b.Rows) == 0 {
				p, err := e.readTargets(ctx, a, b, min(q.Limit-len(page.Items), 20))
				if err != nil {
					excludeTarget(b, e.Now(), err)
					break
				}
				if p.Total != b.Total {
					return api.TargetPage{}, ErrCursor
				}
				b.Rows = p.Items
				b.Cursor = p.NextCursor
				b.Done = p.NextCursor == ""
				b.Status = api.SourceStatus{Scope: b.Scope, Status: "available", AsOf: &p.AsOf, FetchedAt: e.Now()}
				if len(p.Items) > 0 {
					last := p.Items[len(p.Items)-1]
					b.LastID, b.LastCreated = last.TargetID, last.CreatedAt
				}
			}
			if len(b.Rows) == 0 {
				break
			}
			raw, _ := json.Marshal(b.Rows[0])
			if bytes+len(raw) > 2<<20 && len(page.Items) > 0 {
				break browse
			}
			bytes += len(raw)
			page.Items = append(page.Items, b.Rows[0])
			b.Rows = b.Rows[1:]
		}
		if len(page.Items) == q.Limit {
			break
		}
	}
	final := e.discover(ctx, a)
	selected := make([]api.Scope, 0, len(state.Buffers))
	for _, b := range state.Buffers {
		selected = append(selected, b.Scope)
	}
	_, current, err := e.targetAuthorize(selected, final)
	if err != nil {
		return api.TargetPage{}, err
	}
	total := new(big.Int)
	more := false
	success := len(state.Buffers) == 0
	for i := range state.Buffers {
		b := &state.Buffers[i]
		if final[b.Scope.DeploymentID].err != nil {
			excludeTarget(b, e.Now(), ErrAuthority)
		} else if !b.Excluded && current[b.Scope] != b.Authority {
			return api.TargetPage{}, ErrCursor
		}
		if b.Excluded {
			page.Items = slices.DeleteFunc(page.Items, func(t api.Target) bool { return t.Scope == b.Scope })
			page.Completeness = "partial"
		} else {
			success = true
			more = more || len(b.Rows) > 0 || !b.Done
			n, ok := new(big.Int).SetString(b.Total, 10)
			if !ok {
				return api.TargetPage{}, ErrSource
			}
			total.Add(total, n)
			page.Totals = append(page.Totals, api.TargetTotal{Scope: b.Scope, Total: b.Total, AsOf: *b.Status.AsOf})
		}
		page.Sources = append(page.Sources, b.Status)
	}
	if !success {
		return api.TargetPage{}, ErrSource
	}
	page.Total = total.String()
	next := ""
	if more {
		data, err := encodeState(state)
		if err != nil {
			return api.TargetPage{}, err
		}
		next, err = e.cursors.Create(ctx, data, state.Expires)
		if err != nil {
			return api.TargetPage{}, err
		}
		page.NextCursor = "t." + next
	}
	if cursor != "" {
		state.Response = &page
		for i := range state.Buffers {
			state.Buffers[i].Rows = nil
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
				return e.Targets(ctx, a, q, cursor)
			}
			return api.TargetPage{}, err
		}
	}
	return page, nil
}
func (e *Engine) Target(ctx context.Context, a Actor, scope api.Scope, id string) (api.TargetDetail, error) {
	var out api.TargetDetail
	if id == "" || len(id) > 128 {
		return out, ErrNotFound
	}
	authority, err := e.resourceAuthority(ctx, a, scope, "targets.read")
	if err != nil {
		return out, err
	}
	src, ok := e.sources[scope.DeploymentID].(TargetSource)
	if !ok {
		return out, errTargetsUnsupported
	}
	var target api.Target
	err = e.call(ctx, scope.DeploymentID, func(c context.Context) error { var err error; target, err = src.Target(c, a, scope, id); return err })
	if err != nil {
		return out, err
	}
	if target.Scope != scope || target.TargetID != id || target.AsOf.IsZero() || target.Generation.ID == "" {
		return out, ErrSource
	}
	after, err := e.resourceAuthority(ctx, a, scope, "targets.read")
	if err != nil {
		return out, err
	}
	if after != authority {
		return out, ErrCursor
	}
	return api.TargetDetail{Target: target, Sources: e.groupStatus(scope, target.AsOf), Completeness: "complete", FetchedAt: e.Now()}, nil
}

type targetPartitionCursor struct {
	CursorIdentity
	Expires      time.Time `json:"expires"`
	Authority    string    `json:"authority"`
	SourceCursor string    `json:"sourceCursor"`
	LastName     string    `json:"lastName"`
	Total        string    `json:"total"`
}

func (e *Engine) TargetPartitions(ctx context.Context, a Actor, q TargetPartitionQuery, cursor string) (api.TargetPartitionPage, error) {
	out := api.TargetPartitionPage{Page: api.Page[api.TargetPartition]{Items: []api.TargetPartition{}, Sources: []api.SourceStatus{}, Completeness: "complete", FetchedAt: e.Now()}, TargetID: q.TargetID, GenerationID: q.GenerationID}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 200 || q.TargetID == "" || len(q.TargetID) > 128 || q.GenerationID == "" || len(q.GenerationID) > 128 {
		return out, failure("invalid_request", "A target generation and bounded page size are required.")
	}
	authority, err := e.resourceAuthority(ctx, a, q.Scope, "targets.read")
	if err != nil {
		return out, err
	}
	src, ok := e.sources[q.Scope.DeploymentID].(TargetSource)
	if !ok {
		return out, errTargetsUnsupported
	}
	q.Cursor = ""
	state := targetPartitionCursor{CursorIdentity: CursorIdentity{AccountID: a.Account.ID, QueryHash: targetHash(q), Initial: cursor == ""}, Expires: e.Now().Add(15 * time.Minute), Authority: authority}
	if cursor != "" {
		if !strings.HasPrefix(cursor, "p.") || len(cursor) > 512 {
			return out, ErrCursor
		}
		key := strings.TrimPrefix(cursor, "p.")
		data, version, err := e.cursors.Load(ctx, key)
		if err != nil {
			return out, ErrCursor
		}
		expected := state
		if json.Unmarshal(data, &state) != nil || state.AccountID != expected.AccountID || state.QueryHash != expected.QueryHash || state.Authority != authority || !e.Now().Before(state.Expires) || len(state.SourceCursor) > 1024 {
			return out, ErrCursor
		}
		if version == 1 && state.Initial {
			if err = e.cursors.Advance(ctx, key, version, data); err != nil && !errors.Is(err, ErrCursor) {
				return out, err
			}
		}
		state.Initial = false
	}
	q.Cursor = state.SourceCursor
	var page TargetPartitionSourcePage
	err = e.call(ctx, q.Scope.DeploymentID, func(c context.Context) error { var err error; page, err = src.TargetPartitions(c, a, q); return err })
	if err != nil {
		return out, err
	}
	if page.Items == nil || len(page.Items) > q.Limit || page.AsOf.IsZero() || !validDecimal(page.Total) || state.Total != "" && state.Total != page.Total || len(page.NextCursor) > 1024 || page.NextCursor != "" && (page.NextCursor == state.SourceCursor || len(page.Items) == 0) {
		return out, ErrSource
	}
	total, _ := new(big.Int).SetString(page.Total, 10)
	if total.Cmp(big.NewInt(int64(len(page.Items)))) < 0 || page.NextCursor != "" && total.Cmp(big.NewInt(int64(len(page.Items)))) <= 0 {
		return out, ErrSource
	}
	last := state.LastName
	for _, item := range page.Items {
		if item.Name <= last || len(item.Name) > 128 {
			return out, ErrSource
		}
		last = item.Name
	}
	after, err := e.resourceAuthority(ctx, a, q.Scope, "targets.read")
	if err != nil {
		return out, err
	}
	if after != authority {
		return out, ErrCursor
	}
	out.Items = page.Items
	out.Total = page.Total
	out.Sources = e.groupStatus(q.Scope, page.AsOf)
	if page.NextCursor != "" {
		state.LastName = last
		state.Total = page.Total
		state.SourceCursor = page.NextCursor
		data, err := encodeState(state)
		if err != nil {
			return api.TargetPartitionPage{}, err
		}
		key, err := e.cursors.Create(ctx, data, state.Expires)
		if err != nil {
			return api.TargetPartitionPage{}, err
		}
		out.NextCursor = "p." + key
	}
	return out, nil
}
