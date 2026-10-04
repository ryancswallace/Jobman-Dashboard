package control

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type runResponse struct {
	APIVersion             string       `json:"apiVersion"`
	Kind                   string       `json:"kind"`
	AsOf                   time.Time    `json:"asOf"`
	Namespace              string       `json:"namespace"`
	NamespaceID            string       `json:"namespaceId"`
	JobID                  string       `json:"jobId"`
	RecoveryEpoch          string       `json:"recoveryEpoch"`
	AuthorizationVersion   string       `json:"authorizationVersion"`
	AuthorizationCheckedAt time.Time    `json:"authorizationCheckedAt"`
	AuthorizationExpiresAt time.Time    `json:"authorizationExpiresAt"`
	Total                  string       `json:"total"`
	NextCursor             string       `json:"nextPageToken"`
	Items                  []api.JobRun `json:"items"`
	Run                    api.JobRun   `json:"run"`
}

func (c *Client) validRunResponse(v runResponse, d discovery, ns api.Namespace, jobID, kind string) bool {
	return v.APIVersion == contract && v.Kind == kind && v.Namespace == ns.Name && v.NamespaceID == ns.ID && v.JobID == jobID && v.RecoveryEpoch == d.RecoveryEpoch && v.AuthorizationVersion == ns.AuthorizationVersion && !v.AsOf.IsZero() && !v.AsOf.After(c.now().Add(5*time.Second)) && !v.AuthorizationCheckedAt.IsZero() && !v.AuthorizationCheckedAt.After(c.now().Add(5*time.Second)) && c.now().Before(v.AuthorizationExpiresAt)
}
func (c *Client) Runs(ctx context.Context, a monitoring.Actor, q monitoring.RunQuery) (monitoring.RunSourcePage, error) {
	var out monitoring.RunSourcePage
	if !uuid(q.JobID) || q.Limit < 1 || q.Limit > 100 || len(q.Cursor) > 1024 {
		return out, monitoring.ErrSource
	}
	d, ns, err := c.authorize(ctx, a, q.Scope, "jobs.read")
	if err != nil {
		return out, err
	}
	if !slices.Contains(d.features, "bounded-run-catalog") {
		return out, &api.Error{Code: "unsupported_contract", Message: "This source does not support bounded run monitoring."}
	}
	params := url.Values{"limit": {strconv.Itoa(q.Limit)}}
	if q.Cursor != "" {
		params.Set("pageToken", q.Cursor)
	}
	var v runResponse
	if err = c.get(ctx, a, "jobs.read", ns.ID, "/v1/namespaces/"+ns.Name+"/jobs/"+q.JobID+"/runs", params, &v); err != nil {
		return out, err
	}
	if !c.validRunResponse(v, d, ns, q.JobID, "RunList") || !decimal(v.Total) || len(v.Items) > q.Limit || len(v.NextCursor) > 1024 {
		return out, monitoring.ErrSource
	}
	last := int64(0)
	seen := map[string]bool{}
	for _, r := range v.Items {
		if !monitoring.ValidJobRun(r) || seen[r.ID] {
			return out, monitoring.ErrSource
		}
		seen[r.ID] = true
		n, _ := strconv.ParseInt(r.Number, 10, 64)
		if last != 0 && n >= last {
			return out, monitoring.ErrSource
		}
		last = n
	}
	total, _ := strconv.ParseInt(v.Total, 10, 64)
	if total < int64(len(v.Items)) || v.NextCursor != "" && (v.NextCursor == q.Cursor || len(v.Items) == 0 || total <= int64(len(v.Items))) {
		return out, monitoring.ErrSource
	}
	if err = c.recheck(ctx, a, d, ns, "jobs.read"); err != nil {
		return out, err
	}
	return monitoring.RunSourcePage{Items: v.Items, Total: v.Total, NextCursor: v.NextCursor, AsOf: v.AsOf}, nil
}
func (c *Client) Run(ctx context.Context, a monitoring.Actor, scope api.Scope, jobID, runID string) (monitoring.RunSourceDetail, error) {
	var out monitoring.RunSourceDetail
	if !uuid(jobID) || !uuid(runID) {
		return out, monitoring.ErrSource
	}
	d, ns, err := c.authorize(ctx, a, scope, "jobs.read")
	if err != nil {
		return out, err
	}
	if !slices.Contains(d.features, "bounded-run-catalog") {
		return out, &api.Error{Code: "unsupported_contract", Message: "This source does not support bounded run monitoring."}
	}
	var v runResponse
	if err = c.get(ctx, a, "jobs.read", ns.ID, "/v1/namespaces/"+ns.Name+"/jobs/"+jobID+"/runs/"+runID, nil, &v); err != nil {
		return out, err
	}
	if !c.validRunResponse(v, d, ns, jobID, "RunDetail") || !monitoring.ValidJobRun(v.Run) || v.Run.ID != runID {
		return out, monitoring.ErrSource
	}
	if err = c.recheck(ctx, a, d, ns, "jobs.read"); err != nil {
		return out, err
	}
	return monitoring.RunSourceDetail{Run: v.Run, AsOf: v.AsOf}, nil
}
