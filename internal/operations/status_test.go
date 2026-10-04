package operations

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

const statusDeployment = "10000000-0000-4000-8000-000000000001"

func healthFixture() HealthSnapshot {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	old := now.Add(-90 * time.Second)
	backoff := now.Add(time.Minute)
	return HealthSnapshot{ObservedAt: now, Sources: []SourceHealth{{DeploymentID: statusDeployment, State: "active", Feed: &FeedHealth{RetainedEvents: "4", RetentionSeconds: "2592000", LastSuccessAt: &old, Checkpoint: &SourceObservation{AsOf: old, UnpublishedEvents: "1", OldestUnpublishedAt: &old}}, Events: &Backlog{Pending: "2", OldestAt: &old}, Fanout: &Backlog{Pending: "1", OldestAt: &old}, Evaluation: &QueueHealth{Backlog: Backlog{Pending: "2", OldestAt: &old}, Due: "1", Leased: "1"}, Delivery: &QueueHealth{Backlog: Backlog{Pending: "0"}, Due: "0", Leased: "0"}}}, Activation: QueueHealth{Backlog: Backlog{Pending: "1", OldestAt: &old}, Due: "1", Leased: "0"}, Hold: HoldHealth{Held: true, Generation: "3", UpdatedAt: old, RestoreRecordedThrough: &old}, Provider: ProviderHealth{RecordedFailures: "9007199254740993", BackingOff: "1", LastFailureAt: &old, RetryNotBefore: &backoff}}
}

type statusFake struct {
	snapshot HealthSnapshot
	err      error
	ids      []string
	calls    int
}

func (f *statusFake) OperationalStatus(_ context.Context, ids []string) (HealthSnapshot, error) {
	f.calls++
	f.ids = slices.Clone(ids)
	return f.snapshot, f.err
}

func TestOperatorStatusSafeExactJSONAndBoundedLabels(t *testing.T) {
	r := &statusFake{snapshot: healthFixture()}
	var out bytes.Buffer
	if err := WriteStatus(t.Context(), r, []Deployment{{ID: statusDeployment, Name: `Lab "name" <private>`}}, "json", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"recordedFailures": "9007199254740993"`) || !strings.Contains(out.String(), `Lab \"name\" \u003cprivate\u003e`) {
		t.Fatal("exact JSON/display escaping missing", out.String())
	}
	out.Reset()
	if err := WriteStatus(t.Context(), r, []Deployment{{ID: statusDeployment, Name: "DISPLAY-NAME-ONLY"}}, "prometheus", &out); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`jobman_dashboard_feed_state{deployment="` + statusDeployment + `",state="active"} 1`,
		`jobman_dashboard_notification_pending{component="evaluation",deployment="` + statusDeployment + `"} 2`,
		`jobman_dashboard_notification_oldest_age_seconds{component="evaluation",deployment="` + statusDeployment + `"} 90.000`,
		`jobman_dashboard_notification_provider_recorded_failures_total{component="apns"} 9007199254740993`,
		`jobman_dashboard_notification_provider_backoff_remaining_seconds{component="apns"} 60.000`,
		`jobman_dashboard_notification_delivery_held 1`,
		`jobman_dashboard_notification_activation_rule_update_age_seconds 90.000`,
	} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("missing %q", expected)
		}
	}
	if strings.Contains(out.String(), "DISPLAY-NAME") || strings.Contains(out.String(), "account") || strings.Contains(out.String(), "topic=") {
		t.Fatal("unbounded/private metric labels")
	}
	if strings.Count(out.String(), "# TYPE jobman_dashboard_notification_pending ") != 1 {
		t.Fatal("duplicate metric family metadata")
	}
	first := out.String()
	out.Reset()
	if err := WriteStatus(t.Context(), r, []Deployment{{ID: statusDeployment}}, "prometheus", &out); err != nil || first != out.String() {
		t.Fatal("nondeterministic metrics", err)
	}
}

func TestOperatorStatusMissingFeedHasNoHealthyZeros(t *testing.T) {
	s := healthFixture()
	s.Sources = []SourceHealth{{DeploymentID: statusDeployment, State: "uninitialized"}}
	r := &statusFake{snapshot: s}
	var out bytes.Buffer
	if err := WriteStatus(t.Context(), r, []Deployment{{ID: statusDeployment}}, "prometheus", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `state="uninitialized"} 1`) || strings.Contains(out.String(), "feed_retained_events") || strings.Contains(out.String(), `component="evaluation"`) {
		t.Fatal("missing feed represented as healthy/empty", out.String())
	}
	r.snapshot.Sources[0].Events = &Backlog{Pending: "0"}
	out.Reset()
	if err := WriteStatus(t.Context(), r, []Deployment{{ID: statusDeployment}}, "json", &out); !errors.Is(err, ErrStatusUnavailable) || out.Len() != 0 {
		t.Fatal("inconsistent snapshot published")
	}
}

func TestOperatorStatusInvalidAndUnavailableNeverPublishPartial(t *testing.T) {
	for name, mutate := range map[string]func(*HealthSnapshot){
		"label":              func(s *HealthSnapshot) { s.Sources[0].DeploymentID = `bad"label` },
		"error":              func(s *HealthSnapshot) { s.Sources[0].Feed.LastError = "private arbitrary error" },
		"count":              func(s *HealthSnapshot) { s.Provider.RecordedFailures = "1\nsecret" },
		"negative":           func(s *HealthSnapshot) { s.Sources[0].Events.Pending = "-1" },
		"lease-overlap":      func(s *HealthSnapshot) { s.Sources[0].Evaluation.Due = "2" },
		"missing-age":        func(s *HealthSnapshot) { s.Sources[0].Events.OldestAt = nil },
		"missing-checkpoint": func(s *HealthSnapshot) { s.Sources[0].Feed.Checkpoint = nil },
		"other-source":       func(s *HealthSnapshot) { s.Sources[0].DeploymentID = "10000000-0000-4000-8000-000000000002" },
	} {
		t.Run(name, func(t *testing.T) {
			s := healthFixture()
			mutate(&s)
			var out bytes.Buffer
			if err := WriteStatus(t.Context(), &statusFake{snapshot: s}, []Deployment{{ID: statusDeployment}}, "json", &out); !errors.Is(err, ErrStatusUnavailable) || out.Len() != 0 {
				t.Fatal("invalid data published", err)
			}
		})
	}
	var out bytes.Buffer
	if err := WriteStatus(t.Context(), &statusFake{err: errors.New("postgres://private-credential")}, nil, "json", &out); err != ErrStatusUnavailable || out.Len() != 0 {
		t.Fatal("unsafe error or partial output", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := WriteStatus(ctx, &statusFake{snapshot: healthFixture()}, []Deployment{{ID: statusDeployment}}, "json", &out); err != ErrStatusUnavailable || out.Len() != 0 {
		t.Fatal("cancelled output", err)
	}
}

func TestOperatorStatusRequestBoundsAndWriterFailure(t *testing.T) {
	r := &statusFake{snapshot: healthFixture()}
	for _, cfg := range [][]Deployment{{{ID: "bad"}}, {{ID: statusDeployment}, {ID: statusDeployment}}, {{ID: statusDeployment, Name: "bad\nname"}}, make([]Deployment, 33)} {
		if err := WriteStatus(t.Context(), r, cfg, "json", io.Discard); err != ErrStatusInvalid {
			t.Fatal(err)
		}
	}
	if r.calls != 0 {
		t.Fatal("invalid configuration reached database")
	}
	if err := WriteStatus(t.Context(), r, nil, "xml", io.Discard); err != ErrStatusInvalid {
		t.Fatal(err)
	}
	want := errors.New("closed pipe")
	if err := WriteStatus(t.Context(), r, []Deployment{{ID: statusDeployment}}, "json", statusErrorWriter{want}); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if quoteMetric("a\\b\"c\nd") != `"a\\b\"c\nd"` {
		t.Fatal("unsafe metric escaping")
	}
}

type statusErrorWriter struct{ err error }

func (w statusErrorWriter) Write([]byte) (int, error) { return 0, w.err }
