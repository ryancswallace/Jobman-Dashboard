package control

import (
	"context"
	"errors"
	"strings"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func observationOperation(op string) string {
	if op == "" {
		return "capabilities"
	}
	v, _, _ := strings.Cut(op, ".")
	switch v {
	case "namespace", "jobs", "groups", "targets", "logs", "artifacts", "evidence", "events":
		return v
	}
	return "capabilities"
}
func observationOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, monitoring.ErrNotFound), errors.Is(err, monitoring.ErrForbidden):
		return "denied"
	case errors.Is(err, events.ErrAuthority), errors.Is(err, events.ErrUnavailable), errors.Is(err, monitoring.ErrAuthority), errors.Is(err, monitoring.ErrSource), errors.Is(err, context.DeadlineExceeded):
		return "unavailable"
	case errors.Is(err, events.ErrInvalid):
		return "invalid"
	default:
		return "failed"
	}
}
