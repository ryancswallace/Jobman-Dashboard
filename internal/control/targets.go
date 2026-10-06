package control

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type targetAuthority struct {
	APIVersion             string    `json:"apiVersion"`
	Kind                   string    `json:"kind"`
	Namespace              string    `json:"namespace"`
	NamespaceID            string    `json:"namespaceId"`
	AsOf                   time.Time `json:"asOf"`
	RecoveryEpoch          string    `json:"recoveryEpoch"`
	AuthorizationVersion   string    `json:"authorizationVersion"`
	AuthorizationCheckedAt time.Time `json:"authorizationCheckedAt"`
	AuthorizationExpiresAt time.Time `json:"authorizationExpiresAt"`
}

func (c *Client) validTargetAuthority(v targetAuthority, d discovery, n api.Namespace, kind string) bool {
	now := c.now()
	return v.APIVersion == contract && v.Kind == kind && v.Namespace == n.Name && v.NamespaceID == n.ID && v.RecoveryEpoch == d.RecoveryEpoch && v.AuthorizationVersion == n.AuthorizationVersion && !v.AsOf.IsZero() && !v.AsOf.After(now.Add(5*time.Second)) && !v.AuthorizationCheckedAt.IsZero() && !v.AuthorizationCheckedAt.After(now.Add(5*time.Second)) && v.AuthorizationExpiresAt.After(v.AuthorizationCheckedAt) && !v.AuthorizationExpiresAt.After(v.AuthorizationCheckedAt.Add(120*time.Second)) && now.Before(v.AuthorizationExpiresAt)
}

type catalogTarget struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Kind       string               `json:"kind"`
	State      string               `json:"state"`
	Revision   string               `json:"revision"`
	CreatedAt  time.Time            `json:"createdAt"`
	UpdatedAt  time.Time            `json:"updatedAt"`
	Generation api.TargetGeneration `json:"generation"`
}

func mapTarget(t catalogTarget, scope api.Scope, asOf time.Time) (api.Target, error) {
	g := t.Generation
	if !uuid(t.ID) || !artifactName.MatchString(t.Name) || t.Kind == "" || len(t.Kind) > 64 || t.State == "" || len(t.State) > 64 || !decimal(t.Revision) || t.Revision == "0" || t.CreatedAt.IsZero() || t.UpdatedAt.Before(t.CreatedAt) || !uuid(g.ID) || !decimal(g.Number) || g.Number == "0" || g.ExecutionBackend == "" || len(g.ExecutionBackend) > 64 || g.Transport == "" || len(g.Transport) > 64 || !decimal(g.PartitionCount) || len(g.Partitions) > 200 || len(g.ArtifactStores) > 64 || g.Provider.Kind == "" || len(g.Provider.Kind) > 64 || len(g.Provider.Region) > 2<<20 || len(g.Provider.ClusterName) > 128 {
		return api.Target{}, monitoring.ErrSource
	}
	for _, set := range [][]string{g.Runtimes, g.OperatingSystems, g.Architectures, g.Capabilities} {
		if set == nil || len(set) > 1024 {
			return api.Target{}, monitoring.ErrSource
		}
		seen := map[string]bool{}
		for _, v := range set {
			if v == "" || len(v) > 128 || seen[v] {
				return api.Target{}, monitoring.ErrSource
			}
			seen[v] = true
		}
	}
	total, _ := strconv.ParseInt(g.PartitionCount, 10, 64)
	if g.Partitions == nil || g.ArtifactStores == nil || total < int64(len(g.Partitions)) || g.PartitionsTruncated != (total > int64(len(g.Partitions))) {
		return api.Target{}, monitoring.ErrSource
	}
	if !validPartitions(g.Partitions, "") {
		return api.Target{}, monitoring.ErrSource
	}
	stores := slices.Clone(g.ArtifactStores)
	if g.LogStore != nil {
		stores = append(stores, *g.LogStore)
	}
	for _, store := range stores {
		if !artifactName.MatchString(store.Name) || !decimal(store.Version) || store.Version == "0" {
			return api.Target{}, monitoring.ErrSource
		}
	}
	return api.Target{Scope: scope, TargetID: t.ID, Name: t.Name, Kind: t.Kind, State: t.State, Revision: t.Revision, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, AsOf: asOf, Generation: g}, nil
}
func validPartitions(items []api.TargetPartition, last string) bool {
	for _, p := range items {
		if !artifactName.MatchString(p.Name) || p.Name <= last {
			return false
		}
		last = p.Name
	}
	return true
}
func (c *Client) targetAuthorize(ctx context.Context, a monitoring.Actor, scope api.Scope) (discovery, api.Namespace, error) {
	d, n, err := c.authorize(ctx, a, scope, "targets.read")
	if err == nil && !slices.Contains(d.features, "target-catalogs") {
		err = &api.Error{Code: "unsupported_contract", Message: "This source does not support bounded target monitoring."}
	}
	return d, n, err
}
func (c *Client) Targets(ctx context.Context, a monitoring.Actor, q monitoring.TargetSourceQuery) (monitoring.TargetSourcePage, error) {
	var out monitoring.TargetSourcePage
	if q.Limit < 1 || q.Limit > 200 || q.CreatedBefore.IsZero() || len(q.Cursor) > 1024 {
		return out, monitoring.ErrSource
	}
	scope := api.Scope{DeploymentID: c.ID(), NamespaceID: q.NamespaceID}
	d, n, err := c.targetAuthorize(ctx, a, scope)
	if err != nil {
		return out, err
	}
	params := url.Values{"limit": {strconv.Itoa(q.Limit)}, "createdBefore": {q.CreatedBefore.UTC().Format(time.RFC3339Nano)}}
	if q.Cursor != "" {
		params.Set("pageToken", q.Cursor)
	}
	var v struct {
		targetAuthority
		CreatedBefore time.Time       `json:"createdBefore"`
		Total         string          `json:"total"`
		Items         []catalogTarget `json:"items"`
		Next          string          `json:"nextPageToken"`
	}
	if err = c.get(ctx, a, "targets.read", n.ID, "/v1/namespaces/"+n.Name+"/target-catalog", params, &v); err != nil {
		return out, err
	}
	// Newer Control sources clamp the initial wall-clock cutoff to their
	// committed creation watermark. Continuations must retain that exact value.
	if !c.validTargetAuthority(v.targetAuthority, d, n, "TargetCatalog") || v.CreatedBefore.IsZero() || v.CreatedBefore.After(q.CreatedBefore) || q.Cursor != "" && !v.CreatedBefore.Equal(q.CreatedBefore) || !decimal(v.Total) || v.Items == nil || len(v.Items) > q.Limit || len(v.Next) > 1024 || v.Next != "" && (v.Next == q.Cursor || len(v.Items) == 0) {
		return out, monitoring.ErrSource
	}
	out = monitoring.TargetSourcePage{Items: []api.Target{}, Total: v.Total, NextCursor: v.Next, AsOf: v.AsOf, CreatedBefore: v.CreatedBefore}
	bytes := 0
	for _, raw := range v.Items {
		item, err := mapTarget(raw, scope, v.AsOf)
		if err != nil || item.CreatedAt.After(v.CreatedBefore) {
			return monitoring.TargetSourcePage{}, monitoring.ErrSource
		}
		b, _ := json.Marshal(raw)
		bytes += len(b)
		if bytes > 2<<20 {
			return monitoring.TargetSourcePage{}, monitoring.ErrSource
		}
		if len(out.Items) > 0 {
			last := out.Items[len(out.Items)-1]
			if item.CreatedAt.After(last.CreatedAt) || item.CreatedAt.Equal(last.CreatedAt) && item.TargetID >= last.TargetID {
				return monitoring.TargetSourcePage{}, monitoring.ErrSource
			}
		}
		out.Items = append(out.Items, item)
	}
	total, _ := strconv.ParseInt(v.Total, 10, 64)
	if total < int64(len(out.Items)) || v.Next != "" && total <= int64(len(out.Items)) || q.Cursor == "" && v.Next == "" && total != int64(len(out.Items)) {
		return monitoring.TargetSourcePage{}, monitoring.ErrSource
	}
	if err = c.recheck(ctx, a, d, n, "targets.read"); err != nil {
		return monitoring.TargetSourcePage{}, err
	}
	return out, nil
}
func (c *Client) Target(ctx context.Context, a monitoring.Actor, scope api.Scope, id string) (api.Target, error) {
	if !uuid(id) {
		return api.Target{}, monitoring.ErrNotFound
	}
	d, n, err := c.targetAuthorize(ctx, a, scope)
	if err != nil {
		return api.Target{}, err
	}
	var v struct {
		targetAuthority
		Target catalogTarget `json:"target"`
	}
	if err = c.get(ctx, a, "targets.read", n.ID, "/v1/namespaces/"+n.Name+"/target-catalog/"+id, nil, &v); err != nil {
		return api.Target{}, err
	}
	if !c.validTargetAuthority(v.targetAuthority, d, n, "TargetSnapshot") || v.Target.ID != id {
		return api.Target{}, monitoring.ErrSource
	}
	out, err := mapTarget(v.Target, scope, v.AsOf)
	if err != nil {
		return out, err
	}
	if err = c.recheck(ctx, a, d, n, "targets.read"); err != nil {
		return api.Target{}, err
	}
	return out, nil
}
func (c *Client) TargetPartitions(ctx context.Context, a monitoring.Actor, q monitoring.TargetPartitionQuery) (monitoring.TargetPartitionSourcePage, error) {
	var out monitoring.TargetPartitionSourcePage
	if !uuid(q.TargetID) || !uuid(q.GenerationID) || q.Limit < 1 || q.Limit > 200 || len(q.Cursor) > 1024 {
		return out, monitoring.ErrNotFound
	}
	d, n, err := c.targetAuthorize(ctx, a, q.Scope)
	if err != nil {
		return out, err
	}
	params := url.Values{"generationId": {q.GenerationID}, "limit": {strconv.Itoa(q.Limit)}}
	if q.Cursor != "" {
		params.Set("pageToken", q.Cursor)
	}
	var v struct {
		targetAuthority
		TargetID     string                `json:"targetId"`
		GenerationID string                `json:"generationId"`
		Total        string                `json:"total"`
		Items        []api.TargetPartition `json:"items"`
		Next         string                `json:"nextPageToken"`
	}
	if err = c.get(ctx, a, "targets.read", n.ID, "/v1/namespaces/"+n.Name+"/target-catalog/"+q.TargetID+"/partitions", params, &v); err != nil {
		return out, err
	}
	if !c.validTargetAuthority(v.targetAuthority, d, n, "TargetPartitionList") || v.TargetID != q.TargetID || v.GenerationID != q.GenerationID || !decimal(v.Total) || v.Items == nil || len(v.Items) > q.Limit || !validPartitions(v.Items, "") || len(v.Next) > 1024 || v.Next != "" && (len(v.Items) == 0 || v.Next == q.Cursor) {
		return out, monitoring.ErrSource
	}
	total, _ := strconv.ParseInt(v.Total, 10, 64)
	if total < int64(len(v.Items)) || v.Next != "" && total <= int64(len(v.Items)) || q.Cursor == "" && v.Next == "" && total != int64(len(v.Items)) {
		return out, monitoring.ErrSource
	}
	if err = c.recheck(ctx, a, d, n, "targets.read"); err != nil {
		return out, err
	}
	return monitoring.TargetPartitionSourcePage{Items: v.Items, Total: v.Total, NextCursor: v.Next, AsOf: v.AsOf}, nil
}
