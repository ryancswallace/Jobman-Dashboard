package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/store"
)

func TestWorkerCheckUsesOnlySelectedLocalCredentials(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "worker-db")
	// Deliberately unreachable: check-config must validate syntax without dialing.
	if e := os.WriteFile(dsn, []byte("postgres://worker:synthetic@127.0.0.1:1/dashboard?sslmode=verify-full\n"), 0600); e != nil {
		t.Fatal(e)
	}
	c := config.WorkerConfig{ConfigurationRevision: 1, DatabaseURLFile: dsn, Components: []config.WorkerComponent{config.WorkerRetention}, Controls: []config.Control{}}
	data, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "worker.json")
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	if e = runConfiguredMode(path, "check-config", "", "worker"); e != nil {
		t.Fatalf("retention worker needs unrelated material or network: %v", e)
	}
	if e = runConfiguredMode(path, "worker", "/private/ddl-secret", "serve"); e == nil {
		t.Fatal("worker accepted DDL credential")
	}
	if e = runConfiguredMode(path, "api", "", "worker"); e == nil {
		t.Fatal("non-check accepted role flag")
	}
	if e = runConfiguredMode(path, "check-config", "", "unknown"); e == nil {
		t.Fatal("unknown check role accepted")
	}
	if e = runConfiguredMode(path, "check-config", "", "api"); e == nil {
		t.Fatal("worker shape accepted as interactive configuration")
	}
}
func TestAPIModeRejectsProviderSigningAuthority(t *testing.T) {
	c := config.Config{}
	if e := validateAPIMode(c); e != nil {
		t.Fatal(e)
	}
	c.Notifications.APNs = []config.APNsProvider{{PrivateKeyFile: "/private/SECRET-CANARY"}}
	if e := validateAPIMode(c); e == nil || strings.Contains(e.Error(), "SECRET-CANARY") {
		t.Fatalf("API admitted signing credential or leaked path: %v", e)
	}
}
func TestCombinedModeSelectsOnlyConfiguredWorkers(t *testing.T) {
	c := config.Config{ConfigurationRevision: 7, OIDC: config.OIDC{Issuer: "https://identity.example.test"}}
	got := combinedWorkerConfig(c)
	if len(got.Components) != 1 || !got.Has(config.WorkerRetention) || got.ConfigurationRevision != 7 {
		t.Fatalf("minimal combined: %+v", got.Components)
	}
	c.Events.Enabled = true
	c.Reports.ObjectRoot = "/private/reports"
	c.Notifications.APNs = []config.APNsProvider{{Topic: "org.example.dashboard"}}
	got = combinedWorkerConfig(c)
	for _, want := range []config.WorkerComponent{config.WorkerIngestion, config.WorkerNotifications, config.WorkerDelivery, config.WorkerReports, config.WorkerRetention} {
		if !got.Has(want) {
			t.Fatalf("missing %s", want)
		}
	}
}
func TestBackgroundCloseJoinsBeforeClosingResources(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	var cleanup atomic.Int32
	b := &backgroundRuntime{runners: []func(context.Context){func(ctx context.Context) { close(started); <-ctx.Done(); close(finished) }}, cleanup: []func(){func() {
		select {
		case <-finished:
			cleanup.Add(1)
		default:
			t.Error("resources closed before runner finished")
		}
	}}}
	b.Start(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	b.Close()
	b.Close()
	if cleanup.Load() != 1 {
		t.Fatalf("cleanup ran %d times", cleanup.Load())
	}
}
func TestRetentionPreparationDoesNotOpenSourceTransports(t *testing.T) {
	// Source configuration is deliberately unusable. Retention may enumerate IDs,
	// but must never construct source clients or require an authentication key.
	c := config.WorkerConfig{Components: []config.WorkerComponent{config.WorkerRetention}, Controls: []config.Control{{ID: "11111111-1111-4111-8111-111111111111"}}}
	background, e := prepareBackground(c, workerSecrets{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer background.Close()
	if len(background.runners) != 1 || len(background.cleanup) != 0 {
		t.Fatal("retention constructed network resources or extra workers")
	}
}

type fakeDeliveryHoldReader struct {
	state   store.DeliveryControl
	err     error
	calls   int
	bounded bool
}

func (f *fakeDeliveryHoldReader) NotificationDeliveryControl(ctx context.Context) (store.DeliveryControl, error) {
	f.calls++
	deadline, ok := ctx.Deadline()
	f.bounded = ok && time.Until(deadline) <= 5*time.Second
	return f.state, f.err
}
func TestSeparateRuntimeRequiresExistingOperatorHoldWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name      string
		held      bool
		failure   bool
		wantError bool
	}{
		{"held", true, false, false}, {"not held", false, false, true}, {"unavailable", false, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &fakeDeliveryHoldReader{state: store.DeliveryControl{Generation: 7, Held: test.held}}
			if test.failure {
				reader.err = errors.New("PRIVATE-DB-ERROR")
			}
			err := requirePersistedDeliveryHold(t.Context(), reader)
			if (err != nil) != test.wantError || reader.calls != 1 || !reader.bounded {
				t.Fatalf("hold result=%v calls=%d bounded=%v", err, reader.calls, reader.bounded)
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE-DB-ERROR") {
				t.Fatal("private database error escaped")
			}
			if reader.state.Generation != 7 || reader.state.Held != test.held {
				t.Fatal("hold state was changed")
			}
		})
	}
}
