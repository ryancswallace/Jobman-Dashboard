package reports

import (
	"context"
	"errors"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/observability"
)

// SetObserver is startup-only; immutable object ownership/seal checks remain in
// the same read and write paths with or without an observer.
func (s *objectDirectory) SetObserver(r *observability.Registry) { s.observer = r }
func reportObservation(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, monitoring.ErrForbidden), errors.Is(err, monitoring.ErrNotFound):
		return "denied"
	case errors.Is(err, monitoring.ErrSource), errors.Is(err, monitoring.ErrAuthority), errors.Is(err, context.DeadlineExceeded):
		return "unavailable"
	default:
		return "failed"
	}
}

func (r *ReadOnlyObjects) SetObserver(observer *observability.Registry) {
	r.directory.SetObserver(observer)
}
