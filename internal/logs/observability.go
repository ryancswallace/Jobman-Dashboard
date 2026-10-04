package logs

import (
	"context"
	"errors"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/observability"
)

// SetObserver is startup-only and does not affect reader authority or bounds.
func (b *Broker) SetObserver(r *observability.Registry, mode string) {
	b.observer = r
	b.observationMode = mode
}
func logObservation(state string, err error) string {
	if err != nil {
		var known *api.Error
		if errors.As(err, &known) {
			switch known.Code {
			case "rate_limited":
				return "rate_limited"
			case "invalid_request":
				return "invalid"
			case "forbidden", "not_found_or_inaccessible", "unauthenticated":
				return "denied"
			}
		}
		if errors.Is(err, context.Canceled) {
			return "cancelled"
		}
		if errors.Is(err, monitoring.ErrForbidden) || errors.Is(err, monitoring.ErrNotFound) {
			return "denied"
		}
		return "unavailable"
	}
	switch state {
	case "open", "complete", "more", "not_captured", "chunk_missing", "chunk_corrupt", "sequence_gap", "mapping_inaccessible", "reader_busy", "read_timeout", "reader_unavailable", "invalid_manifest":
		return state
	default:
		return "ok"
	}
}

// ObservedReader records safe fixed file states; paths and content are discarded.
type ObservedReader struct {
	Reader   Reader
	Observer *observability.Registry
}

func (r ObservedReader) Read(ctx context.Context, q FileRequest) FileResult {
	start := time.Now()
	v := r.Reader.Read(ctx, q)
	r.Observer.ObserveBytes("logs", "chunk", "", "", logObservation(v.State, nil), time.Since(start), int64(len(v.Bytes)))
	return v
}
