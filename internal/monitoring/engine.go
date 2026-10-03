package monitoring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

type Engine struct {
	sources   map[string]Source
	cursors   CursorStore
	Now       func() time.Time
	Timeout   time.Duration
	gate      chan struct{}
	perSource map[string]chan struct{}
}

func New(sources []Source, cursors CursorStore) (*Engine, error) {
	e := &Engine{sources: make(map[string]Source), cursors: cursors, Now: time.Now, Timeout: 1500 * time.Millisecond, gate: make(chan struct{}, 64), perSource: make(map[string]chan struct{})}
	for _, s := range sources {
		if s.ID() == "" || e.sources[s.ID()] != nil {
			return nil, fmt.Errorf("duplicate or empty deployment ID")
		}
		e.sources[s.ID()] = s
		e.perSource[s.ID()] = make(chan struct{}, 8)
	}
	if len(sources) == 0 || len(sources) > 32 || cursors == nil {
		return nil, fmt.Errorf("configure 1–32 sources and cursor storage")
	}
	return e, nil
}

func (e *Engine) call(ctx context.Context, id string, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	select {
	case e.gate <- struct{}{}:
	case <-ctx.Done():
		return ErrSource
	}
	defer func() { <-e.gate }()
	select {
	case e.perSource[id] <- struct{}{}:
	case <-ctx.Done():
		return ErrSource
	}
	defer func() { <-e.perSource[id] }()
	return fn(ctx)
}

type discoveryResult struct {
	value Discovery
	err   error
}

func (e *Engine) discover(ctx context.Context, a Actor) map[string]discoveryResult {
	result := make(map[string]discoveryResult)
	var mu sync.Mutex
	var wg sync.WaitGroup
	fanout := make(chan struct{}, 4)
	for id, src := range e.sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case fanout <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				result[id] = discoveryResult{err: ErrSource}
				mu.Unlock()
				return
			}
			defer func() { <-fanout }()
			var d Discovery
			err := e.call(ctx, id, func(c context.Context) error { var err error; d, err = src.Discover(c, a); return err })
			if err == nil && (d.Deployment.ID != id || d.InstanceID == "" || d.RecoveryEpoch == "" || d.ServiceTime.IsZero()) {
				err = failure("unsupported_contract", "The source is missing required identity or clock metadata.")
			}
			mu.Lock()
			result[id] = discoveryResult{d, err}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return result
}

func (e *Engine) Bootstrap(ctx context.Context, a Actor) (api.Bootstrap, error) {
	b := api.Bootstrap{APIVersion: api.Version, Account: a.Account, Deployments: []api.Deployment{}, Preferences: api.DefaultPreferences(), Limits: api.DefaultLimits(), Completeness: "complete"}
	var unavailable bool
	for _, r := range e.discover(ctx, a) {
		if r.err != nil {
			unavailable = true
			b.Completeness = "partial"
			continue
		}
		d := r.value.Deployment
		d.Namespaces = slices.DeleteFunc(slices.Clone(d.Namespaces), func(n api.Namespace) bool { return !validGrant(n, e.Now()) })
		if len(d.Namespaces) > 0 {
			b.Deployments = append(b.Deployments, d)
		}
	}
	slices.SortFunc(b.Deployments, func(a, b api.Deployment) int { return strings.Compare(a.ID, b.ID) })
	if len(b.Deployments) == 0 && unavailable {
		return b, ErrAuthority
	}
	return b, nil
}

func validGrant(n api.Namespace, now time.Time) bool {
	return n.ID != "" && n.AuthorizationVersion != "" && !n.AuthorizationCheckedAt.IsZero() && now.Before(n.AuthorizationExpiresAt) && slices.Contains(n.Capabilities, "jobs.read")
}

func (e *Engine) authorize(q Query, ds map[string]discoveryResult) ([]api.Scope, map[api.Scope]string, error) {
	grants := make(map[api.Scope]string)
	for id, r := range ds {
		if r.err != nil {
			continue
		}
		for _, n := range r.value.Deployment.Namespaces {
			if validGrant(n, e.Now()) {
				grants[api.Scope{DeploymentID: id, NamespaceID: n.ID}] = r.value.InstanceID + "/" + r.value.RecoveryEpoch + "/" + n.AuthorizationVersion
			}
		}
	}
	scopes := slices.Clone(q.Scopes)
	if scopes == nil {
		for s := range grants {
			scopes = append(scopes, s)
		}
		for id, r := range ds {
			if r.err != nil {
				scopes = append(scopes, api.Scope{DeploymentID: id})
			}
		}
	}
	if len(scopes) > 320 {
		return nil, nil, failure("invalid_request", "Too many selected namespaces.")
	}
	slices.SortFunc(scopes, scopeCompare)
	scopes = slices.Compact(scopes)
	for _, s := range scopes {
		if s.NamespaceID == "" {
			continue
		}
		if _, ok := grants[s]; !ok {
			if r, known := ds[s.DeploymentID]; known && r.err != nil {
				continue
			}
			return nil, nil, ErrForbidden
		}
	}
	return scopes, grants, nil
}

func scopeCompare(a, b api.Scope) int {
	if c := strings.Compare(a.DeploymentID, b.DeploymentID); c != 0 {
		return c
	}
	return strings.Compare(a.NamespaceID, b.NamespaceID)
}

func validateQuery(q *Query) error {
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 200 {
		return failure("invalid_request", "Page size must be between 1 and 200.")
	}
	for _, s := range []string{q.Phase, q.Outcome, q.Owner} {
		if len(s) > 128 {
			return failure("invalid_request", "Filter is too long.")
		}
	}
	if q.CompletedFrom != nil && q.CompletedTo != nil && !q.CompletedFrom.Before(*q.CompletedTo) {
		return failure("invalid_request", "Completion window must have an increasing start and end.")
	}
	return nil
}

type sourceBuffer struct {
	Scope     api.Scope        `json:"scope"`
	Authority string           `json:"authority"`
	Cutoff    time.Time        `json:"cutoff"`
	Cursor    string           `json:"cursor"`
	Rows      []api.Job        `json:"rows"`
	Done      bool             `json:"done"`
	Excluded  bool             `json:"excluded"`
	Status    api.SourceStatus `json:"status"`
	LastRead  *api.Job         `json:"lastRead,omitempty"`
}

type browseState struct {
	AccountID string             `json:"accountId"`
	QueryHash string             `json:"queryHash"`
	Buffers   []sourceBuffer     `json:"buffers"`
	Expires   time.Time          `json:"expires"`
	Response  *api.Page[api.Job] `json:"response,omitempty"`
	Initial   bool               `json:"initial,omitempty"`
}

func queryHash(q Query) string {
	b, _ := json.Marshal(q)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Newest first, then stable source-qualified lexical identity. This ordering is
// shared by upstream keysets and the cross-source merge, including equal times.
func compareJobs(a, b api.Job) int {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		return 1
	}
	if c := scopeCompare(a.Scope, b.Scope); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

func (e *Engine) fill(ctx context.Context, a Actor, q Query, b *sourceBuffer) error {
	if len(b.Rows) > 0 || b.Done || b.Excluded {
		return nil
	}
	var page JobPage
	err := e.call(ctx, b.Scope.DeploymentID, func(c context.Context) error {
		var err error
		page, err = e.sources[b.Scope.DeploymentID].Jobs(c, a, SourceQuery{Query: q, NamespaceID: b.Scope.NamespaceID, CreatedBefore: b.Cutoff, Cursor: b.Cursor})
		return err
	})
	if err != nil {
		return err
	}
	if len(page.Items) > q.Limit || page.AsOf.IsZero() || (len(page.Items) == 0 && page.NextCursor != "") || (page.NextCursor != "" && page.NextCursor == b.Cursor) {
		return failure("unsupported_contract", "The source returned an invalid bounded page.")
	}
	last := b.LastRead
	for i := range page.Items {
		j := &page.Items[i]
		if j.Scope != b.Scope || j.ID == "" || j.CreatedAt.After(b.Cutoff) || (last != nil && compareJobs(*last, *j) >= 0) {
			return failure("unsupported_contract", "The source returned invalid scope or ordering.")
		}
		last = j
	}
	if last != nil {
		copy := *last
		b.LastRead = &copy
	}
	b.Rows = page.Items
	b.Cursor = page.NextCursor
	b.Done = page.NextCursor == ""
	b.Status = api.SourceStatus{Scope: b.Scope, Status: "available", AsOf: &page.AsOf, FetchedAt: e.Now()}
	return nil
}

func statusFor(s api.Scope, now time.Time, err error) api.SourceStatus {
	code := "source_unavailable"
	var ae *api.Error
	if errors.As(err, &ae) {
		code = ae.Code
	}
	status := "unavailable"
	if code == "authorization_unavailable" || code == "forbidden" {
		status = "authorization_unavailable"
	}
	if code == "unsupported_contract" {
		status = "unsupported"
	}
	return api.SourceStatus{Scope: s, Status: status, FetchedAt: now, Message: "This source contribution is unavailable. Refresh to retry."}
}

func (e *Engine) Jobs(ctx context.Context, a Actor, q Query, cursor string) (api.Page[api.Job], error) {
	page := api.Page[api.Job]{Items: []api.Job{}, Sources: []api.SourceStatus{}, Completeness: "complete", FetchedAt: e.Now()}
	if err := validateQuery(&q); err != nil {
		return page, err
	}
	q.Scopes = slices.Clone(q.Scopes)
	slices.SortFunc(q.Scopes, scopeCompare)
	q.Scopes = slices.Compact(q.Scopes)
	fingerprint := queryHash(q)
	originalQuery := q
	ds := e.discover(ctx, a)
	scopes, grants, err := e.authorize(q, ds)
	if err != nil {
		return page, err
	}
	q.Scopes = scopes
	state := browseState{AccountID: a.Account.ID, QueryHash: fingerprint, Expires: e.Now().Add(15 * time.Minute), Initial: cursor == ""}
	var key string
	var version uint64
	if cursor != "" {
		parts := strings.Split(cursor, ".")
		if len(parts) != 2 {
			return page, ErrCursor
		}
		key = parts[0]
		if parts[1] != "1" {
			return page, ErrCursor
		}
		data, v, err := e.cursors.Load(ctx, key)
		if err != nil {
			return page, ErrCursor
		}
		version = v
		if json.Unmarshal(data, &state) != nil || state.AccountID != a.Account.ID || state.QueryHash != fingerprint || !e.Now().Before(state.Expires) {
			return page, ErrCursor
		}
		for i := range state.Buffers {
			b := &state.Buffers[i]
			if ds[b.Scope.DeploymentID].err != nil {
				if state.Response != nil {
					return page, ErrCursor
				}
				b.Excluded = true
				b.Rows = nil
				b.Status = statusFor(b.Scope, e.Now(), ErrAuthority)
				continue
			}
			if !b.Excluded && grants[b.Scope] != b.Authority {
				return page, ErrCursor
			}
		}
		// GET retries and back navigation replay exactly this page, but only
		// after checking the current account, query and every source grant.
		if state.Response != nil {
			return *state.Response, nil
		}
		state.Initial = false
		q.Scopes = []api.Scope{}
		for _, b := range state.Buffers {
			q.Scopes = append(q.Scopes, b.Scope)
		}
	} else {
		for _, s := range scopes {
			b := sourceBuffer{Scope: s, Authority: grants[s], Cutoff: ds[s.DeploymentID].value.ServiceTime}
			if grants[s] == "" {
				b.Excluded = true
				b.Status = statusFor(s, e.Now(), ErrAuthority)
			}
			state.Buffers = append(state.Buffers, b)
		}
	}
	// Prime at most four source calls concurrently. Each source has a separate
	// eight-call budget; interactive global work is bounded independently.
	var wg sync.WaitGroup
	fanout := make(chan struct{}, 4)
	for i := range state.Buffers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fanout <- struct{}{}
			defer func() { <-fanout }()
			b := &state.Buffers[i]
			if err := e.fill(ctx, a, q, b); err != nil {
				b.Excluded = true
				b.Rows = nil
				b.Status = statusFor(b.Scope, e.Now(), err)
			}
		}()
	}
	wg.Wait()
	for len(page.Items) < q.Limit {
		best := -1
		for i := range state.Buffers {
			b := &state.Buffers[i]
			if err := e.fill(ctx, a, q, b); err != nil {
				b.Excluded = true
				b.Rows = nil
				b.Status = statusFor(b.Scope, e.Now(), err)
			}
			if len(b.Rows) > 0 && (best < 0 || compareJobs(b.Rows[0], state.Buffers[best].Rows[0]) < 0) {
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
	finalDiscovery := e.discover(ctx, a)
	_, finalGrants, err := e.authorize(q, finalDiscovery)
	if err != nil {
		return page, err
	}
	for i := range state.Buffers {
		b := &state.Buffers[i]
		if finalDiscovery[b.Scope.DeploymentID].err != nil {
			b.Excluded = true
			b.Rows = nil
			b.Status = statusFor(b.Scope, e.Now(), ErrAuthority)
			page.Items = slices.DeleteFunc(page.Items, func(j api.Job) bool { return j.Scope == b.Scope })
		} else if !b.Excluded && finalGrants[b.Scope] != b.Authority {
			return page, ErrCursor
		}
	}
	more := false
	success := len(state.Buffers) == 0
	for _, b := range state.Buffers {
		page.Sources = append(page.Sources, b.Status)
		if b.Excluded {
			page.Completeness = "partial"
		} else {
			success = true
			if len(b.Rows) > 0 || !b.Done {
				more = true
			}
		}
	}
	if !success {
		return page, ErrSource
	}
	var data []byte
	if more {
		data, err = encodeState(state)
		if err != nil {
			return page, err
		}
	}
	var nextKey string
	if more {
		nextKey, err = e.cursors.Create(ctx, data, state.Expires)
		if err != nil {
			return page, err
		}
		page.NextCursor = nextKey + ".1"
	}
	if cursor != "" {
		state.Response = &page
		// Replay only needs source authority and the response. Remainders now
		// belong to nextKey, avoiding duplicate persistent row buffers.
		for i := range state.Buffers {
			state.Buffers[i].Rows = nil
			state.Buffers[i].LastRead = nil
		}
		cached, encodeErr := encodeState(state)
		if encodeErr == nil {
			err = e.cursors.Advance(ctx, key, version, cached)
		} else {
			err = encodeErr
		}
		if err != nil {
			if nextKey != "" {
				_ = e.cursors.Advance(ctx, nextKey, 1, nil)
			}
			// A concurrent identical GET may have won publication. Re-enter the
			// normal authorization path before returning its replayable response.
			if errors.Is(err, ErrCursor) {
				return e.Jobs(ctx, a, originalQuery, cursor)
			}
			return page, err
		}
	}
	return page, nil
}

func (e *Engine) Job(ctx context.Context, a Actor, s api.Scope, id string) (api.JobDetail, error) {
	var result api.JobDetail
	_, grants, err := e.authorize(Query{Scopes: []api.Scope{s}}, e.discover(ctx, a))
	if err != nil {
		return result, err
	}
	if grants[s] == "" {
		return result, ErrAuthority
	}
	err = e.call(ctx, s.DeploymentID, func(c context.Context) error {
		var err error
		result.Job, err = e.sources[s.DeploymentID].Job(c, a, s, id)
		return err
	})
	if err != nil {
		return result, err
	}
	if result.Job.Scope != s || result.Job.ID != id {
		return api.JobDetail{}, ErrNotFound
	}
	_, next, err := e.authorize(Query{Scopes: []api.Scope{s}}, e.discover(ctx, a))
	if err != nil {
		return api.JobDetail{}, err
	}
	if next[s] != grants[s] {
		return api.JobDetail{}, ErrForbidden
	}
	result.FetchedAt = e.Now()
	return result, nil
}

func (e *Engine) Overview(ctx context.Context, a Actor, q Query, w api.Window) (api.Overview, error) {
	o := api.Overview{Terminal: make(map[string]*int64), Window: w, Completeness: "complete", Sources: []api.SourceStatus{}, FetchedAt: e.Now()}
	for _, k := range []string{"success", "failure", "cancelled", "timed_out", "aborted", "lost", "unknown"} {
		o.Terminal[k] = nil
	}
	if w.From.IsZero() || !w.From.Before(w.To) {
		return o, failure("invalid_request", "Supply an increasing completion window.")
	}
	scopes, grants, err := e.authorize(q, e.discover(ctx, a))
	if err != nil {
		return o, err
	}
	q.Scopes = scopes
	type contribution struct {
		counts Counts
		err    error
	}
	results := make(map[api.Scope]contribution)
	var mu sync.Mutex
	var wg sync.WaitGroup
	fanout := make(chan struct{}, 4)
	for _, s := range scopes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fanout <- struct{}{}
			defer func() { <-fanout }()
			var c Counts
			var err error
			if grants[s] == "" {
				err = ErrAuthority
			} else {
				err = e.call(ctx, s.DeploymentID, func(ctx context.Context) error {
					var err error
					c, err = e.sources[s.DeploymentID].Summary(ctx, a, s, w)
					return err
				})
			}
			mu.Lock()
			results[s] = contribution{c, err}
			mu.Unlock()
		}()
	}
	wg.Wait()
	_, next, err := e.authorize(q, e.discover(ctx, a))
	if err != nil {
		return o, err
	}
	totals := Counts{Terminal: make(map[string]int64)}
	success := len(scopes) == 0
	for _, s := range scopes {
		r := results[s]
		if next[s] == "" {
			r.err = ErrAuthority
		} else if next[s] != grants[s] {
			return o, ErrForbidden
		}
		var combined Counts
		if r.err == nil {
			combined, r.err = addCounts(totals, r.counts)
		}
		if r.err != nil {
			o.Completeness = "partial"
			o.Sources = append(o.Sources, statusFor(s, e.Now(), r.err))
			continue
		}
		success = true
		totals = combined
		stamp := r.counts.AsOf
		o.Sources = append(o.Sources, api.SourceStatus{Scope: s, Status: "available", AsOf: &stamp, FetchedAt: e.Now()})
	}
	if !success {
		return o, ErrSource
	}
	o.Active = &totals.Active
	o.AwaitingExecution = &totals.AwaitingExecution
	o.Running = &totals.Running
	o.EvidenceAttention = &totals.EvidenceAttention
	o.MissingCompletionTime = &totals.MissingCompletionTime
	for k := range o.Terminal {
		v := totals.Terminal[k]
		o.Terminal[k] = &v
	}
	return o, nil
}

// Summary counts are JSON safe integers. Reject malformed or overflowing source
// contributions as unavailable; they cannot corrupt otherwise useful totals.
func addCounts(total, c Counts) (Counts, error) {
	invalid := failure("unsupported_contract", "The source returned invalid summary counts.")
	if c.AsOf.IsZero() || c.AwaitingExecution > c.Active || c.Running > c.Active || c.EvidenceAttention > c.Active {
		return total, invalid
	}
	result := Counts{Terminal: make(map[string]int64)}
	for k, v := range total.Terminal {
		result.Terminal[k] = v
	}
	const maxCount = int64(9007199254740991)
	pairs := [][3]*int64{{&total.Active, &c.Active, &result.Active}, {&total.AwaitingExecution, &c.AwaitingExecution, &result.AwaitingExecution}, {&total.Running, &c.Running, &result.Running}, {&total.EvidenceAttention, &c.EvidenceAttention, &result.EvidenceAttention}, {&total.MissingCompletionTime, &c.MissingCompletionTime, &result.MissingCompletionTime}}
	for _, p := range pairs {
		if *p[1] < 0 || *p[1] > maxCount-*p[0] {
			return total, invalid
		}
		*p[2] = *p[0] + *p[1]
	}
	for k, v := range c.Terminal {
		if !slices.Contains([]string{"success", "failure", "cancelled", "timed_out", "aborted", "lost"}, k) {
			k = "unknown"
		}
		if v < 0 || v > maxCount-result.Terminal[k] {
			return total, invalid
		}
		result.Terminal[k] += v
	}
	return result, nil
}
