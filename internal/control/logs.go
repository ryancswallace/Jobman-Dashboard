package control

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/logs"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

// Manifest uses the bounded metadata contract, never the legacy unbounded
// /logs document. Store coordinates remain server-side and are not authority.
func (c *Client) Manifest(ctx context.Context, a monitoring.Actor, q logs.ManifestQuery) (logs.Manifest, error) {
	var result logs.Manifest
	if !uuid(q.JobID) || (q.RunNumber != "" && (!decimal(q.RunNumber) || q.RunNumber == "0")) || (q.Stream != "stdout" && q.Stream != "stderr") || q.Limit < 1 || q.Limit > 100 || q.TailBytes < 0 || q.TailBytes > logs.MaxChunkBytes {
		return result, monitoring.ErrSource
	}
	selectors := 0
	if q.TailBytes > 0 {
		selectors++
	}
	if q.Offset != nil {
		selectors++
		if *q.Offset < 0 {
			return result, monitoring.ErrSource
		}
	}
	if q.AfterSequence != nil {
		selectors++
		if *q.AfterSequence < 0 {
			return result, monitoring.ErrSource
		}
	}
	if selectors > 1 {
		return result, monitoring.ErrSource
	}
	d, ns, err := c.authorize(ctx, a, q.Scope, "logs.read")
	if err != nil {
		return result, err
	}
	params := url.Values{"stream": {q.Stream}, "limit": {strconv.Itoa(q.Limit)}}
	if q.RunNumber != "" {
		params.Set("runNumber", q.RunNumber)
	}
	if q.TailBytes > 0 {
		params.Set("tailBytes", strconv.Itoa(q.TailBytes))
	}
	if q.Offset != nil {
		params.Set("fromOffset", strconv.FormatInt(*q.Offset, 10))
	}
	if q.AfterSequence != nil {
		params.Set("afterSequence", strconv.FormatInt(*q.AfterSequence, 10))
	}
	var response struct {
		APIVersion           string    `json:"apiVersion"`
		Kind                 string    `json:"kind"`
		AsOf                 time.Time `json:"asOf"`
		NamespaceID          string    `json:"namespaceId"`
		NamespaceName        string    `json:"namespace"`
		RecoveryEpoch        string    `json:"recoveryEpoch"`
		AuthorizationVersion string    `json:"authorizationVersion"`
		JobID                string    `json:"jobId"`
		TargetGenerationID   string    `json:"targetGenerationId"`
		RunID                string    `json:"runId"`
		RunNumber            string    `json:"runNumber"`
		ExecutionID          string    `json:"executionId"`
		Stream               string    `json:"stream"`
		ManifestRevision     string    `json:"manifestRevision"`
		State                string    `json:"state"`
		Truncated            bool      `json:"truncated"`
		ByteLength           string    `json:"byteLength"`
		LastSequence         string    `json:"lastSequence"`
		Chunks               []struct {
			Sequence     string    `json:"sequence"`
			ByteOffset   string    `json:"byteOffset"`
			ByteLength   string    `json:"byteLength"`
			Checksum     string    `json:"checksum"`
			StoreName    string    `json:"storeName"`
			StoreVersion string    `json:"storeVersion"`
			ObjectKey    string    `json:"objectKey"`
			CapturedAt   time.Time `json:"capturedAt"`
		} `json:"chunks"`
	}
	if err = c.get(ctx, a, "logs.read", ns.ID, "/v1/namespaces/"+ns.Name+"/jobs/"+q.JobID+"/log-chunks", params, &response); err != nil {
		return result, err
	}
	if response.APIVersion != contract || response.Kind != "LogChunkList" || response.NamespaceID != ns.ID || response.NamespaceName != ns.Name || response.JobID != q.JobID || response.Stream != q.Stream || response.RecoveryEpoch != d.RecoveryEpoch || response.AuthorizationVersion != ns.AuthorizationVersion || len(response.Chunks) > q.Limit || !decimal(response.ByteLength) || !decimal(response.LastSequence) || !decimal(response.ManifestRevision) {
		return result, monitoring.ErrSource
	}
	length, _ := strconv.ParseInt(response.ByteLength, 10, 64)
	sequence, _ := strconv.ParseInt(response.LastSequence, 10, 64)
	result = logs.Manifest{Scope: q.Scope, NamespaceName: ns.Name, JobID: q.JobID, TargetGenerationID: response.TargetGenerationID, RunID: response.RunID, RunNumber: response.RunNumber, ExecutionID: response.ExecutionID, Stream: response.Stream, InstanceID: d.InstanceID, RecoveryEpoch: d.RecoveryEpoch, AuthorizationVersion: ns.AuthorizationVersion, AuthorizationExpiresAt: ns.AuthorizationExpiresAt, AsOf: response.AsOf, Revision: response.ManifestRevision, State: response.State, Truncated: response.Truncated, ByteLength: length, LastSequence: sequence, Chunks: make([]logs.Chunk, 0, len(response.Chunks))}
	for _, ch := range response.Chunks {
		if !decimal(ch.Sequence) || ch.Sequence == "0" || !decimal(ch.ByteOffset) || !decimal(ch.ByteLength) || !decimal(ch.StoreVersion) || ch.StoreVersion == "0" {
			return logs.Manifest{}, monitoring.ErrSource
		}
		seq, _ := strconv.ParseInt(ch.Sequence, 10, 64)
		offset, _ := strconv.ParseInt(ch.ByteOffset, 10, 64)
		size, _ := strconv.ParseInt(ch.ByteLength, 10, 64)
		if q.AfterSequence != nil && seq <= *q.AfterSequence {
			return logs.Manifest{}, monitoring.ErrSource
		}
		result.Chunks = append(result.Chunks, logs.Chunk{Sequence: seq, ByteOffset: offset, ByteLength: size, StoreName: ch.StoreName, StoreVersion: ch.StoreVersion, ObjectKey: ch.ObjectKey, Checksum: ch.Checksum, CapturedAt: ch.CapturedAt})
	}
	if err = c.recheck(ctx, a, d, ns, "logs.read"); err != nil {
		return logs.Manifest{}, err
	}
	return result, nil
}
