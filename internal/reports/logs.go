package reports

import (
	"context"
	"encoding/base64"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman/diagnostic"
)

type LogService interface {
	Read(context.Context, monitoring.Actor, logs.Request) (api.LogRange, error)
}

// PinnedLogs reads only references present in a previously validated snapshot.
// The ordinary log service may safely return immutable bytes while a stream
// grows; diagnosis has a stricter contract and rejects any manifest change.
type PinnedLogs struct {
	Snapshot Snapshot
	Actor    monitoring.Actor
	Source   logs.ManifestSource
	Logs     LogService
}

func (p PinnedLogs) ReadLogTail(ctx context.Context, r diagnostic.SharedLogRequest) (diagnostic.SharedLogTail, error) {
	var empty diagnostic.SharedLogTail
	v := p.Snapshot.Value
	s := r.Selection
	if p.Source == nil || p.Logs == nil || diagnostic.ValidateSharedSnapshot(v) != nil || s.DeploymentID != v.Source.DeploymentID || s.ControlInstanceID != v.Source.ControlInstanceID || s.NamespaceID != v.Source.NamespaceID || s.JobID != v.Job.ID || s.ExpectedJobRevision != v.Job.Revision || r.MaxBytes < 1 || r.MaxBytes > diagnostic.SharedMaximumTailBytes {
		return empty, ErrInvalid
	}
	found := false
	for _, ref := range v.Logs {
		if ref == r.Reference {
			found = true
			break
		}
	}
	var run string
	for _, item := range v.Runs {
		if item.ID == r.Reference.RunID && item.ExecutionID == r.Reference.ExecutionID && (s.RunID == "" || s.RunID == item.ID) {
			run = strconv.FormatUint(item.Number, 10)
			break
		}
	}
	if !found || run == "" {
		return empty, ErrInvalid
	}
	scope := api.Scope{DeploymentID: s.DeploymentID, NamespaceID: s.NamespaceID}
	query := logs.ManifestQuery{Scope: scope, JobID: s.JobID, RunNumber: run, Stream: r.Reference.Stream, TailBytes: int(r.MaxBytes), Limit: 1}
	before, err := p.Source.Manifest(ctx, p.Actor, query)
	if err != nil {
		return empty, err
	}
	if !p.matches(before, r, run) {
		return empty, snapshotChanged()
	}
	start := r.Reference.Bytes - min(r.Reference.Bytes, r.MaxBytes)
	data := make([]byte, 0, r.MaxBytes)
	cursor := ""
	for page := 0; page < 16; page++ {
		remaining := r.MaxBytes - uint64(len(data))
		result, err := p.Logs.Read(ctx, p.Actor, logs.Request{Scope: scope, JobID: s.JobID, RunNumber: run, Stream: r.Reference.Stream, LimitBytes: int(remaining), Cursor: cursor})
		if err != nil {
			return empty, err
		}
		// Dense streams can span several100-chunk broker pages. Each page has
		// fresh authority and exact contiguous offsets, within one64KiB budget.
		if result.State != before.State && result.State != "more" || result.ExecutionID != r.Reference.ExecutionID || result.RunID != r.Reference.RunID || result.RunNumber != run || result.Stream != r.Reference.Stream || len(result.BytesBase64) > base64.StdEncoding.EncodedLen(int(remaining)) {
			return empty, evidenceUnavailable()
		}
		part, err := base64.StdEncoding.Strict().DecodeString(result.BytesBase64)
		end := start + uint64(len(data)) + uint64(len(part))
		if err != nil || uint64(len(part)) > remaining || end > r.Reference.Bytes || result.StartOffset != strconv.FormatUint(start+uint64(len(data)), 10) || result.EndOffset != strconv.FormatUint(end, 10) {
			return empty, ErrInvalid
		}
		data = append(data, part...)
		if end == r.Reference.Bytes {
			if result.State != before.State {
				return empty, snapshotChanged()
			}
			break
		}
		if result.State != "more" || len(part) == 0 || result.NextCursor == "" || result.NextCursor == cursor || len(result.NextCursor) > 4096 {
			return empty, evidenceUnavailable()
		}
		cursor = result.NextCursor
	}
	if uint64(len(data)) != r.Reference.Bytes-start {
		return empty, evidenceUnavailable()
	}
	after, err := p.Source.Manifest(ctx, p.Actor, query)
	if err != nil {
		return empty, err
	}
	if !p.matches(after, r, run) || before.AuthorizationVersion != after.AuthorizationVersion || before.TargetGenerationID != after.TargetGenerationID {
		return empty, snapshotChanged()
	}
	if ctx.Err() != nil {
		return empty, ctx.Err()
	}
	return diagnostic.SharedLogTail{Reference: r.Reference, Data: data, ByteStart: start, ByteEnd: r.Reference.Bytes, CapturedAt: time.Now().UTC()}, nil
}

func evidenceUnavailable() error {
	return &api.Error{Code: "evidence_unavailable", Message: "The selected logs could not be read within the report size or time limits. Try a report without logs, or contact your administrator."}
}

func (p PinnedLogs) matches(m logs.Manifest, r diagnostic.SharedLogRequest, run string) bool {
	s := p.Snapshot.Value
	state := "open"
	if r.Reference.Complete {
		state = "complete"
	}
	return m.Scope.DeploymentID == s.Source.DeploymentID && m.Scope.NamespaceID == s.Source.NamespaceID && m.InstanceID == s.Source.ControlInstanceID && m.RecoveryEpoch == p.Snapshot.RecoveryEpoch && m.JobID == s.Job.ID && m.RunID == r.Reference.RunID && m.RunNumber == run && m.ExecutionID == r.Reference.ExecutionID && m.Stream == r.Reference.Stream && m.State == state && m.Revision == strconv.FormatUint(r.Reference.ManifestRevision, 10) && m.ByteLength >= 0 && uint64(m.ByteLength) == r.Reference.Bytes
}

func snapshotChanged() error {
	return &api.Error{Code: "snapshot_changed", Message: "The job information changed while the report was being prepared. Request a new report."}
}
