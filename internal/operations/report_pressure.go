package operations

import (
	"fmt"
	"io"
	"time"
)

// ReportPressure contains only aggregate pending-work pressure for the configured
// source subset. The maximum is global; it is not free filesystem capacity.
type ReportPressure struct {
	ObservedAt      time.Time
	Pending, Leased string
	OldestAt        *time.Time
}

func (p ReportPressure) Validate() error {
	if !statusTime(p.ObservedAt) || !validBacklog(Backlog{Pending: p.Pending, OldestAt: p.OldestAt}) || !statusCount(p.Leased) || !countLE(p.Leased, p.Pending) {
		return ErrStatusUnavailable
	}
	return nil
}
func WriteReportPressure(w io.Writer, p ReportPressure) error {
	if p.Validate() != nil {
		return ErrStatusUnavailable
	}
	if _, e := fmt.Fprintf(w, "# HELP jobman_dashboard_reports_pending Pending report tasks in the configured source subset; not disk bytes.\n# TYPE jobman_dashboard_reports_pending gauge\njobman_dashboard_reports_pending %s\n# TYPE jobman_dashboard_reports_leased gauge\njobman_dashboard_reports_leased %s\n# TYPE jobman_dashboard_reports_global_pending_limit gauge\njobman_dashboard_reports_global_pending_limit 512\n", p.Pending, p.Leased); e != nil {
		return e
	}
	if p.OldestAt != nil {
		_, e := fmt.Fprintf(w, "# TYPE jobman_dashboard_reports_oldest_age_seconds gauge\njobman_dashboard_reports_oldest_age_seconds %g\n", max(0, p.ObservedAt.Sub(*p.OldestAt).Seconds()))
		return e
	}
	return nil
}
