package observability

import (
	"fmt"
	"time"
)

func formatSampleAge(t time.Time) string {
	return fmt.Sprintf("# TYPE jobman_dashboard_operator_snapshot_age_seconds gauge\njobman_dashboard_operator_snapshot_age_seconds %g\n", max(0, time.Since(t).Seconds()))
}
