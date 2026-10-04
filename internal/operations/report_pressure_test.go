package operations

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestReportPressureRenderingAndBounds(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-time.Minute)
	p := ReportPressure{ObservedAt: now, Pending: "2", Leased: "1", OldestAt: &old}
	var out bytes.Buffer
	if err := WriteReportPressure(&out, p); err != nil || !strings.Contains(out.String(), "jobman_dashboard_reports_oldest_age_seconds 60") {
		t.Fatal(out.String(), err)
	}
	for _, bad := range []ReportPressure{{ObservedAt: now, Pending: "1", Leased: "2", OldestAt: &old}, {ObservedAt: now, Pending: "0", Leased: "0", OldestAt: &old}, {ObservedAt: now, Pending: "1", Leased: "0"}, {ObservedAt: now, Pending: "-1", Leased: "0"}} {
		out.Reset()
		if WriteReportPressure(&out, bad) == nil || out.Len() != 0 {
			t.Fatal("invalid pressure published", bad)
		}
	}
}
