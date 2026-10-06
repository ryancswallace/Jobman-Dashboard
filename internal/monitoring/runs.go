package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

type RunQuery struct {
	Scope  api.Scope `json:"scope"`
	JobID  string    `json:"jobId"`
	Limit  int       `json:"limit"`
	Cursor string    `json:"-"`
}
type RunSourcePage struct {
	Items             []api.JobRun
	Total, NextCursor string
	AsOf              time.Time
}
type RunSourceDetail struct {
	Run  api.JobRun
	AsOf time.Time
}
type RunSource interface {
	Runs(context.Context, Actor, RunQuery) (RunSourcePage, error)
	Run(context.Context, Actor, api.Scope, string, string) (RunSourceDetail, error)
}

var runUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func ValidRunID(id string) bool {
	return runUUID.MatchString(id) && id != "00000000-0000-0000-0000-000000000000"
}

// ValidJobRun preserves unknown enum values but rejects fabricated/incomplete
// execution identities and unbounded source strings.
func ValidJobRun(r api.JobRun) bool {
	if !ValidRunID(r.ID) || !positiveInt64(r.Number) || r.Phase == "" || r.DesiredState == "" || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		return false
	}
	for _, s := range []string{r.Phase, r.DesiredState, r.Outcome, r.ExecutionPhase, r.Backend, r.Confidence} {
		if len(s) > 64 {
			return false
		}
	}
	if r.ExecutionID == "" {
		return r.ExecutionPhase == "" && r.TargetID == "" && r.TargetGenerationID == "" && r.Backend == "" && r.Confidence == ""
	}
	return ValidRunID(r.ExecutionID) && ValidRunID(r.TargetID) && ValidRunID(r.TargetGenerationID) && r.ExecutionPhase != "" && r.Backend != ""
}

type runCursor struct {
	CursorIdentity
	Expires      time.Time `json:"expires"`
	Authority    string    `json:"authority"`
	SourceCursor string    `json:"sourceCursor"`
	LastNumber   int64     `json:"lastNumber"`
	Total        string    `json:"total"`
}

func (e *Engine) Runs(ctx context.Context, a Actor, q RunQuery, cursor string) (api.RunPage, error) {
	out := api.RunPage{Page: api.Page[api.JobRun]{Items: []api.JobRun{}, Completeness: "complete", Sources: []api.SourceStatus{}}, Total: "0"}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if !ValidRunID(q.JobID) || q.Limit < 1 || q.Limit > 100 {
		return out, failure("invalid_request", "The run history request is invalid. Refresh the list and select a run.")
	}
	authority, err := e.resourceAuthority(ctx, a, q.Scope, "jobs.read")
	if err != nil {
		return out, err
	}
	src, ok := e.sources[q.Scope.DeploymentID].(RunSource)
	if !ok {
		return out, failure("unsupported_contract", "This Jobman Control deployment does not support run history. Ask your administrator about upgrading it.")
	}
	q.Cursor = ""
	state := runCursor{CursorIdentity: CursorIdentity{AccountID: a.Account.ID, QueryHash: groupHash(struct {
		Kind  string
		Query RunQuery
	}{"runs", q}), Initial: cursor == ""}, Expires: e.Now().Add(10 * time.Minute), Authority: authority}
	if cursor != "" {
		if !strings.HasPrefix(cursor, "r.") || len(cursor) > 512 {
			return out, ErrCursor
		}
		key := strings.TrimPrefix(cursor, "r.")
		data, version, loadErr := e.cursors.Load(ctx, key)
		if loadErr != nil {
			return out, ErrCursor
		}
		expected := state
		if json.Unmarshal(data, &state) != nil || state.AccountID != expected.AccountID || state.QueryHash != expected.QueryHash || state.Authority != authority || !e.Now().Before(state.Expires) || state.LastNumber < 1 || len(state.SourceCursor) > 1024 || !validDecimal(state.Total) {
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
	var page RunSourcePage
	err = e.call(ctx, q.Scope.DeploymentID, func(c context.Context) error { var readErr error; page, readErr = src.Runs(c, a, q); return readErr })
	if err != nil {
		return out, err
	}
	if len(page.Items) > q.Limit || page.AsOf.IsZero() || !validDecimal(page.Total) || state.Total != "" && state.Total != page.Total || len(page.NextCursor) > 1024 || page.NextCursor != "" && (page.NextCursor == state.SourceCursor || len(page.Items) == 0) {
		return out, ErrSource
	}
	total, _ := new(big.Int).SetString(page.Total, 10)
	cmp := total.Cmp(big.NewInt(int64(len(page.Items))))
	if cmp < 0 || page.NextCursor != "" && cmp == 0 {
		return out, ErrSource
	}
	last := state.LastNumber
	seen := map[string]bool{}
	for _, run := range page.Items {
		if !ValidJobRun(run) || seen[run.ID] {
			return out, ErrSource
		}
		seen[run.ID] = true
		n, _ := strconv.ParseInt(run.Number, 10, 64)
		if last != 0 && n >= last {
			return out, ErrSource
		}
		last = n
	}
	after, err := e.resourceAuthority(ctx, a, q.Scope, "jobs.read")
	if err != nil {
		return out, err
	}
	if after != authority {
		return out, ErrCursor
	}
	out.Items = page.Items
	if out.Items == nil {
		out.Items = []api.JobRun{}
	}
	out.Total = page.Total
	out.Sources = e.groupStatus(q.Scope, page.AsOf)
	out.FetchedAt = e.Now()
	if page.NextCursor != "" {
		state.SourceCursor = page.NextCursor
		state.LastNumber = last
		state.Total = page.Total
		data, encodeErr := encodeState(state)
		if encodeErr != nil {
			return api.RunPage{}, encodeErr
		}
		key, createErr := e.cursors.Create(ctx, data, state.Expires)
		if createErr != nil {
			return api.RunPage{}, createErr
		}
		out.NextCursor = "r." + key
	}
	return out, nil
}
func (e *Engine) Run(ctx context.Context, a Actor, scope api.Scope, jobID, runID string) (api.RunDetail, error) {
	var out api.RunDetail
	if !ValidRunID(jobID) || !ValidRunID(runID) {
		return out, ErrNotFound
	}
	before, err := e.resourceAuthority(ctx, a, scope, "jobs.read")
	if err != nil {
		return out, err
	}
	src, ok := e.sources[scope.DeploymentID].(RunSource)
	if !ok {
		return out, failure("unsupported_contract", "This Jobman Control deployment does not support run history. Ask your administrator about upgrading it.")
	}
	var detail RunSourceDetail
	err = e.call(ctx, scope.DeploymentID, func(c context.Context) error {
		var readErr error
		detail, readErr = src.Run(c, a, scope, jobID, runID)
		return readErr
	})
	if err != nil {
		return out, err
	}
	if detail.Run.ID != runID || !ValidJobRun(detail.Run) || detail.AsOf.IsZero() {
		return out, ErrSource
	}
	after, err := e.resourceAuthority(ctx, a, scope, "jobs.read")
	if err != nil {
		return out, err
	}
	if after != before {
		return out, ErrCursor
	}
	return api.RunDetail{Run: detail.Run, Completeness: "complete", Sources: e.groupStatus(scope, detail.AsOf), FetchedAt: e.Now()}, nil
}
