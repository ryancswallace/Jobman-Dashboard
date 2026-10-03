package control

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

var artifactName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,126}[a-z0-9])?$`)
var artifactChecksum = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (c *Client) Artifacts(ctx context.Context, a monitoring.Actor, q monitoring.ArtifactQuery) (monitoring.ArtifactSourcePage, error) {
	var out monitoring.ArtifactSourcePage
	if !uuid(q.JobID) || q.Limit < 1 || q.Limit > 100 || len(q.Cursor) > 512 || q.RunNumber != "" && (!decimal(q.RunNumber) || q.RunNumber == "0") {
		return out, monitoring.ErrSource
	}
	d, ns, err := c.authorize(ctx, a, q.Scope, "artifacts.read")
	if err != nil {
		return out, err
	}
	params := url.Values{"limit": {strconv.Itoa(q.Limit)}}
	if q.Cursor != "" {
		params.Set("pageToken", q.Cursor)
	}
	if q.RunNumber != "" {
		params.Set("runNumber", q.RunNumber)
	}
	var response struct {
		APIVersion             string    `json:"apiVersion"`
		Kind                   string    `json:"kind"`
		AsOf                   time.Time `json:"asOf"`
		Namespace              string    `json:"namespace"`
		NamespaceID            string    `json:"namespaceId"`
		JobID                  string    `json:"jobId"`
		RecoveryEpoch          string    `json:"recoveryEpoch"`
		AuthorizationVersion   string    `json:"authorizationVersion"`
		AuthorizationCheckedAt time.Time `json:"authorizationCheckedAt"`
		AuthorizationExpiresAt time.Time `json:"authorizationExpiresAt"`
		Total                  string    `json:"total"`
		NextCursor             string    `json:"nextPageToken"`
		Items                  []struct {
			RunID              string    `json:"runId"`
			RunNumber          string    `json:"runNumber"`
			ExecutionID        string    `json:"executionId"`
			TargetGenerationID string    `json:"targetGenerationId"`
			Name               string    `json:"name"`
			ByteLength         string    `json:"byteLength"`
			Checksum           string    `json:"checksum"`
			PublishedAt        time.Time `json:"publishedAt"`
		} `json:"items"`
	}
	if err = c.get(ctx, a, "artifacts.read", ns.ID, "/v1/namespaces/"+ns.Name+"/jobs/"+q.JobID+"/artifact-metadata", params, &response); err != nil {
		return out, err
	}
	if response.APIVersion != contract || response.Kind != "ArtifactList" || response.Namespace != ns.Name || response.NamespaceID != ns.ID || response.JobID != q.JobID || response.RecoveryEpoch != d.RecoveryEpoch || response.AuthorizationVersion != ns.AuthorizationVersion || !decimal(response.Total) || len(response.Items) > q.Limit || len(response.NextCursor) > 512 || response.AsOf.IsZero() || response.AsOf.After(c.now().Add(5*time.Second)) || response.AuthorizationCheckedAt.IsZero() || response.AuthorizationCheckedAt.After(c.now().Add(5*time.Second)) || !c.now().Before(response.AuthorizationExpiresAt) {
		return out, monitoring.ErrSource
	}
	out = monitoring.ArtifactSourcePage{Items: []api.Artifact{}, Total: response.Total, NextCursor: response.NextCursor, AsOf: response.AsOf}
	last := ""
	for _, item := range response.Items {
		id := item.ExecutionID + "/" + item.Name
		if !uuid(item.RunID) || !uuid(item.ExecutionID) || !uuid(item.TargetGenerationID) || !decimal(item.RunNumber) || item.RunNumber == "0" || q.RunNumber != "" && q.RunNumber != item.RunNumber || !artifactName.MatchString(item.Name) || !decimal(item.ByteLength) || !artifactChecksum.MatchString(item.Checksum) || item.PublishedAt.IsZero() || id <= last {
			return monitoring.ArtifactSourcePage{}, monitoring.ErrSource
		}
		last = id
		out.Items = append(out.Items, api.Artifact{ID: id, Name: item.Name, RunID: item.RunID, RunNumber: item.RunNumber, ExecutionID: item.ExecutionID, TargetGenerationID: item.TargetGenerationID, SizeBytes: item.ByteLength, Checksum: item.Checksum, PublishedAt: item.PublishedAt, Availability: "metadata_only"})
	}
	total, _ := strconv.ParseInt(response.Total, 10, 64) // decimal validated above
	if total < int64(len(out.Items)) || response.NextCursor != "" && (response.NextCursor == q.Cursor || len(out.Items) == 0 || total <= int64(len(out.Items))) {
		return monitoring.ArtifactSourcePage{}, monitoring.ErrSource
	}
	if err = c.recheck(ctx, a, d, ns, "artifacts.read"); err != nil {
		return monitoring.ArtifactSourcePage{}, err
	}
	return out, nil
}
