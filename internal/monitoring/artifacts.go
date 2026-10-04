package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

type ArtifactQuery struct {
	Scope     api.Scope `json:"scope"`
	JobID     string    `json:"jobId"`
	RunNumber string    `json:"runNumber,omitempty"`
	Limit     int       `json:"limit"`
	Cursor    string    `json:"-"`
}
type ArtifactSourcePage struct {
	Items      []api.Artifact
	Total      string
	NextCursor string
	AsOf       time.Time
}
type ArtifactSource interface {
	Artifacts(context.Context, Actor, ArtifactQuery) (ArtifactSourcePage, error)
}

// resourceAuthority discovers only the source being read. Cursors carry no
// permission; they must match a newly verified source/epoch/grant stamp.
func (e *Engine) resourceAuthority(ctx context.Context, a Actor, scope api.Scope, capability string) (string, error) {
	src := e.sources[scope.DeploymentID]
	if src == nil || scope.NamespaceID == "" {
		return "", ErrNotFound
	}
	var d Discovery
	err := e.call(ctx, scope.DeploymentID, func(c context.Context) error { var err error; d, err = src.Discover(c, a); return err })
	if err != nil {
		return "", err
	}
	if d.Deployment.ID != scope.DeploymentID || d.InstanceID == "" || d.RecoveryEpoch == "" || d.ServiceTime.IsZero() {
		return "", ErrAuthority
	}
	for _, n := range d.Deployment.Namespaces {
		if n.ID == scope.NamespaceID && validGrant(n, e.Now()) && slices.Contains(n.Capabilities, capability) {
			return d.InstanceID + "/" + d.RecoveryEpoch + "/" + n.AuthorizationVersion, nil
		}
	}
	return "", ErrForbidden
}

type artifactCursor struct {
	CursorIdentity
	Expires      time.Time `json:"expires"`
	Authority    string    `json:"authority"`
	SourceCursor string    `json:"sourceCursor"`
	LastID       string    `json:"lastId"`
}

func (e *Engine) Artifacts(ctx context.Context, a Actor, q ArtifactQuery, cursor string) (api.ArtifactPage, error) {
	out := api.ArtifactPage{Page: api.Page[api.Artifact]{Items: []api.Artifact{}, Sources: []api.SourceStatus{}, Completeness: "complete", FetchedAt: e.Now()}}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.JobID == "" || len(q.JobID) > 128 || q.Limit < 1 || q.Limit > 100 || q.RunNumber != "" && !positiveInt64(q.RunNumber) {
		return out, failure("invalid_request", "The artifact selector or page size is invalid.")
	}
	authority, err := e.resourceAuthority(ctx, a, q.Scope, "artifacts.read")
	if err != nil {
		return out, err
	}
	src, ok := e.sources[q.Scope.DeploymentID].(ArtifactSource)
	if !ok {
		return out, failure("unsupported_contract", "This source does not support bounded artifact metadata.")
	}
	q.Cursor = ""
	state := artifactCursor{CursorIdentity: CursorIdentity{AccountID: a.Account.ID, QueryHash: groupHash(struct {
		Kind  string
		Query ArtifactQuery
	}{"artifacts", q}), Initial: cursor == ""}, Expires: e.Now().Add(15 * time.Minute), Authority: authority}
	if cursor != "" {
		if !strings.HasPrefix(cursor, "a.") || len(cursor) > 512 {
			return out, ErrCursor
		}
		key := strings.TrimPrefix(cursor, "a.")
		data, version, err := e.cursors.Load(ctx, key)
		if err != nil {
			return out, ErrCursor
		}
		expected := state
		if json.Unmarshal(data, &state) != nil || state.AccountID != expected.AccountID || state.QueryHash != expected.QueryHash || state.Authority != authority || !e.Now().Before(state.Expires) || len(state.SourceCursor) > 512 {
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
	var page ArtifactSourcePage
	err = e.call(ctx, q.Scope.DeploymentID, func(c context.Context) error { var err error; page, err = src.Artifacts(c, a, q); return err })
	if err != nil {
		return out, err
	}
	if len(page.Items) > q.Limit || page.AsOf.IsZero() || !validDecimal(page.Total) || len(page.NextCursor) > 512 || page.NextCursor != "" && (page.NextCursor == state.SourceCursor || len(page.Items) == 0) {
		return out, ErrSource
	}
	total, _ := new(big.Int).SetString(page.Total, 10) // bounded decimal validated above
	countOrder := total.Cmp(big.NewInt(int64(len(page.Items))))
	if countOrder < 0 || page.NextCursor != "" && countOrder == 0 {
		return out, ErrSource
	}
	last := state.LastID
	for _, item := range page.Items {
		if item.ID != item.ExecutionID+"/"+item.Name || item.ID <= last || item.Name == "" || len(item.Name) > 128 || !positiveInt64(item.RunNumber) || q.RunNumber != "" && item.RunNumber != q.RunNumber || !validDecimal(item.SizeBytes) || item.PublishedAt.IsZero() || item.Availability != "metadata_only" {
			return out, ErrSource
		}
		last = item.ID
	}
	after, err := e.resourceAuthority(ctx, a, q.Scope, "artifacts.read")
	if err != nil {
		return out, err
	}
	if after != authority {
		return out, ErrCursor
	}
	out.Items = page.Items
	if out.Items == nil {
		out.Items = []api.Artifact{}
	}
	out.Total = page.Total
	out.Sources = e.groupStatus(q.Scope, page.AsOf)
	if page.NextCursor != "" {
		state.SourceCursor = page.NextCursor
		state.LastID = last
		data, err := encodeState(state)
		if err != nil {
			return api.ArtifactPage{}, err
		}
		key, err := e.cursors.Create(ctx, data, state.Expires)
		if err != nil {
			return api.ArtifactPage{}, err
		}
		out.NextCursor = "a." + key
	}
	return out, nil
}

func positiveInt64(s string) bool {
	if !validDecimal(s) || s == "0" {
		return false
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}
