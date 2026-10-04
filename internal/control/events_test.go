package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

func eventCaps(now time.Time) map[string]any {
	c := capabilities(now)
	c["capabilities"].(map[string]any)["features"] = []string{"durable-monitoring-events"}
	return c
}
func eventCheckpoint(now time.Time, kind string) map[string]any {
	return map[string]any{"apiVersion": contract, "kind": kind, "controlInstanceId": instanceID, "recoveryEpoch": "1", "asOf": now, "headCursor": "authenticated.head", "oldestCursor": "authenticated.oldest", "retentionSeconds": "2592000", "backlogCount": "0"}
}
func eventPage(now time.Time) map[string]any {
	v := eventCheckpoint(now, "MonitoringEventList")
	v["items"] = []map[string]any{{"eventId": "66666666-6666-4666-8666-666666666666", "position": "9007199254740993", "namespaceId": namespaceID, "jobId": jobID, "runId": jobID, "runNumber": "3", "ownerPrincipalId": principalID, "oldPhase": "running", "newPhase": "terminal", "outcome": "future-outcome", "jobRevision": "9223372036854775807", "observedCompletedAt": now.Add(-time.Minute), "recordedAt": now, "imported": false, "reconciliation": true}}
	v["nextCursor"], v["hasMore"] = "authenticated.head", false
	return v
}
func newTestEventSource(t *testing.T, handler http.Handler) *EventSource {
	t.Helper()
	interactive := testClient(t, handler)
	source, err := NewEventSource(interactive.config)
	if err != nil {
		t.Fatal(err)
	}
	if source.client.client.Transport == interactive.client.Transport {
		t.Fatal("worker borrowed interactive transport")
	}
	t.Cleanup(source.Close)
	return source
}
func verifyEventAssertion(t *testing.T, r *http.Request) string {
	t.Helper()
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) != 1 {
		t.Fatal("missing actual mTLS")
	}
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, auth.DelegationScheme+" ") {
		t.Fatal("missing service assertion")
	}
	token, err := jwt.ParseSigned(strings.TrimPrefix(value, auth.DelegationScheme+" "), []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		t.Fatal("malformed assertion")
	}
	var claims auth.ServiceEventClaims
	var raw map[string]any
	if err = token.Claims(r.TLS.PeerCertificates[0].PublicKey, &claims, &raw); err != nil {
		t.Fatal("assertion signature invalid")
	}
	if _, exists := raw["actor"]; exists {
		t.Fatal("service assertion leaked represented actor")
	}
	sum := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	if claims.ValidateWithLeeway(jwt.Expected{Issuer: "dashboard-synthetic", Subject: "dashboard-synthetic", AnyAudience: jwt.Audience{"control-synthetic"}, Time: time.Now()}, 0) != nil || claims.Operation != "events.read" || claims.Mode != "worker" || len(claims.NamespaceIDs) != 1 || claims.NamespaceIDs[0] != namespaceID || claims.Confirmation["x5t#S256"] != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatal("service or certificate binding changed")
	}
	return claims.ID
}

func TestEventAdapterUsesServiceAuthorityAndPreservesOriginalFacts(t *testing.T) {
	now := time.Now().UTC()
	seen := map[string]bool{}
	source := newTestEventSource(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			respond(w, eventCaps(now))
			return
		}
		id := verifyEventAssertion(t, r)
		if seen[id] {
			t.Error("assertion replay")
		}
		seen[id] = true
		switch r.URL.Path {
		case "/v1/monitoring-events/checkpoint":
			if r.URL.RawQuery != "" {
				t.Error("checkpoint changed scope through query")
			}
			respond(w, eventCheckpoint(now, "MonitoringCheckpoint"))
		case "/v1/monitoring-events":
			if r.URL.Query().Get("cursor") != "opaque.original" || r.URL.Query().Get("limit") != "200" {
				t.Error("caller cursor or limit changed")
			}
			respond(w, eventPage(now))
		default:
			t.Error("service source called user route")
			http.NotFound(w, r)
		}
	}))
	ids := source.NamespaceIDs()
	ids[0] = jobID
	checkpoint, err := source.Checkpoint(t.Context())
	if err != nil || checkpoint.Validate() != nil || checkpoint.DeploymentID != deploymentID || checkpoint.NamespaceIDs[0] != namespaceID || source.SourceID() != deploymentID {
		t.Fatalf("checkpoint: %v", err)
	}
	checkpoint.NamespaceIDs[0] = jobID
	page, err := source.Read(t.Context(), "opaque.original", 200)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page: %v", err)
	}
	e := page.Items[0]
	if e.EventID != "66666666-6666-4666-8666-666666666666" || e.DeploymentID != deploymentID || e.ControlInstanceID != instanceID || e.Position != "9007199254740993" || e.JobRevision != "9223372036854775807" || e.RunNumber != "3" || e.OwnerPrincipalID != principalID || e.Outcome != "future-outcome" || e.Imported || !e.Reconciliation || e.ObservedCompletedAt == nil || !e.RecordedAt.Equal(now) {
		t.Fatal("event facts changed")
	}
}

func TestEventAdapterReturnsExplicitRecoveryWithoutCheckpointReset(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
		reason events.RecoveryReason
	}{
		{409, "event_cursor_expired", events.CursorExpired}, {409, "source_recovery_changed", events.SourceChanged}, {409, "event_cursor_scope_changed", events.ScopeChanged}, {400, "invalid_request", events.CursorInvalid},
	} {
		t.Run(test.code, func(t *testing.T) {
			reads := 0
			source := newTestEventSource(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/capabilities" {
					respond(w, eventCaps(time.Now().UTC()))
					return
				}
				if r.URL.Path != "/v1/monitoring-events" {
					t.Error("automatic checkpoint reset")
				}
				reads++
				w.WriteHeader(test.status)
				respond(w, map[string]any{"error": map[string]any{"code": test.code, "message": "private upstream diagnostic"}})
			}))
			_, err := source.Read(t.Context(), "original.cursor", 1)
			var recovery *events.RecoveryError
			if !errors.Is(err, events.ErrRecoveryRequired) || !errors.As(err, &recovery) || recovery.Reason != test.reason || reads != 1 || strings.Contains(err.Error(), "private") {
				t.Fatalf("wrong recovery: %v", err)
			}
		})
	}
}

func TestEventAdapterRejectsMalformedOrMisboundPages(t *testing.T) {
	for _, mode := range []string{"instance", "epoch", "scope", "missing-flag", "null-flag", "wrong-type", "duplicate", "case-alias", "unknown", "missing-items", "too-many", "order", "repeat-id", "more-count", "nonprogress", "body-limit", "depth", "missing-feature", "callback", "authority", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			calls := 0
			source := newTestEventSource(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/capabilities" {
					c := eventCaps(now)
					if mode == "missing-feature" {
						c["capabilities"].(map[string]any)["features"] = []string{"read-delegation"}
					}
					respond(w, c)
					return
				}
				calls++
				body := eventPage(now)
				item := body["items"].([]map[string]any)[0]
				switch mode {
				case "instance":
					body["controlInstanceId"] = jobID
				case "epoch":
					body["recoveryEpoch"] = "2"
				case "scope":
					item["namespaceId"] = jobID
				case "missing-flag":
					delete(item, "imported")
				case "null-flag":
					item["reconciliation"] = nil
				case "wrong-type":
					item["position"] = 1
				case "duplicate":
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, `{"kind":"MonitoringEventList","kind":"MonitoringEventList"}`)
					return
				case "case-alias":
					item["Position"] = item["position"]
					delete(item, "position")
				case "unknown":
					item["jobName"] = "private"
				case "missing-items":
					delete(body, "items")
				case "too-many":
					body["items"] = []map[string]any{item, item}
				case "order":
					next := make(map[string]any)
					for k, v := range item {
						next[k] = v
					}
					next["eventId"] = jobID
					next["position"] = "1"
					body["items"] = []map[string]any{item, next}
				case "repeat-id":
					next := make(map[string]any)
					for k, v := range item {
						next[k] = v
					}
					next["position"] = "9007199254740994"
					body["items"] = []map[string]any{item, next}
				case "more-count":
					body["hasMore"] = true
					body["nextCursor"] = "more"
				case "nonprogress":
					body["nextCursor"] = "input"
					body["headCursor"] = "input"
				case "body-limit":
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, strings.Repeat(" ", (2<<20)+1))
					return
				case "depth":
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, strings.Repeat("[", 18)+"0"+strings.Repeat("]", 18))
					return
				case "authority":
					w.WriteHeader(403)
					return
				case "redirect":
					w.Header().Set("Location", "/private-redirect")
					w.WriteHeader(307)
					return
				}
				respond(w, body)
			}))
			if mode == "callback" {
				source.client.config.VerifyIdentity = func(context.Context, string, string) error {
					return &api.Error{Code: "source_recovery_changed", Message: "private"}
				}
			}
			limit := 2
			if mode == "too-many" {
				limit = 1
			}
			page, err := source.Read(t.Context(), "input", limit)
			if err == nil || len(page.Items) != 0 {
				t.Fatal("invalid source page escaped validation")
			}
			if (mode == "instance" || mode == "epoch" || mode == "callback") && !errors.Is(err, events.ErrRecoveryRequired) {
				t.Fatal("source replacement lost recovery classification")
			}
			if (mode == "callback" || mode == "missing-feature") && calls != 0 {
				t.Fatal("incompatible source received service assertion")
			}
			if calls > 1 {
				t.Fatal("redirect or retry escaped fixed route")
			}
		})
	}
}

func TestEventSourceCapacityCancellationAndScopeOwnership(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var active, maximum atomic.Int32
	source := newTestEventSource(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			n := active.Add(1)
			for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
			}
			entered <- struct{}{}
			<-release
			active.Add(-1)
			respond(w, eventCaps(time.Now().UTC()))
			return
		}
		respond(w, eventCheckpoint(time.Now().UTC(), "MonitoringCheckpoint"))
	}))
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			if _, err := source.Checkpoint(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	<-entered
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := source.Checkpoint(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("worker queue ignored cancellation")
	}
	close(release)
	workers.Wait()
	if maximum.Load() != 2 {
		t.Fatal("worker capacity was not bounded")
	}
	config := source.client.config
	config.NamespaceIDs = []string{namespaceID, namespaceID}
	if _, err := NewEventSource(config); err == nil {
		t.Fatal("duplicate configured namespace set accepted")
	}
	for _, limit := range []int{0, 201} {
		if _, err := source.Read(t.Context(), "opaque", limit); !errors.Is(err, events.ErrInvalid) {
			t.Fatal("invalid limit accepted")
		}
	}
	if _, err := source.Read(t.Context(), "", 1); !errors.Is(err, events.ErrRecoveryRequired) {
		t.Fatal("lost checkpoint silently reset")
	}
}
