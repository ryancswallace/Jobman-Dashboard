package notifications

import (
	"strings"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/observability"
)

func TestSenderObservationReportsActualProviderAcceptanceOnly(t *testing.T) {
	s, f := newSenderFixture(t)
	r, err := observability.NewRegistry("worker", 1, nil, observability.Build{})
	if err != nil {
		t.Fatal(err)
	}
	s.SetObserver(r)
	f.verifyErr = ErrEvaluationSource
	if _, err = s.Step(t.Context(), f.fence.Checkpoint.DeploymentID); err == nil {
		t.Fatal("failed authority accepted")
	}
	if strings.Contains(string(r.Render()), `component="delivery"`) {
		t.Fatal("no provider attempt should be recorded")
	}
	f.verifyErr = nil
	if _, err = s.Step(t.Context(), f.fence.Checkpoint.DeploymentID); err != nil {
		t.Fatal(err)
	}
	raw := string(r.Render())
	if !strings.Contains(raw, `operation="attempt",deployment="",mode="",outcome="accepted"} 1`) || !strings.Contains(raw, `operation="recorded_to_acceptance"`) {
		t.Fatal(raw)
	}
	for _, private := range []string{f.claim.ID, f.handoff.Device.Token, f.handoff.Device.Topic} {
		if strings.Contains(raw, private) {
			t.Fatal("private provider detail escaped")
		}
	}
}
