package control

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

// The unavailable peer exercises the same partial path as a stopped Control,
// without replacing the healthy source's real HTTP adapter or authorization.
type unavailableOverviewPeer struct{ monitoring.Source }

func (unavailableOverviewPeer) ID() string { return "66666666-6666-4666-8666-666666666666" }
func (unavailableOverviewPeer) Discover(context.Context, monitoring.Actor) (monitoring.Discovery, error) {
	return monitoring.Discovery{}, monitoring.ErrSource
}

func TestOverviewCanonicalWindowSurvivesDatabasePrecisionAndPeerOutage(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name      string
		from, to  time.Time
		drift     time.Duration
		wantCode  string
		wantReads int32
	}{
		{name: "fractional default", from: base.Add(-24*time.Hour + 987654321), to: base.Add(987654321), wantReads: 1},
		{name: "explicit different fractions", from: base.Add(-time.Hour + 123456789), to: base.Add(999999999), wantReads: 1},
		{name: "offset timezone", from: base.Add(-time.Hour + 1234).In(time.FixedZone("offset", 19800)), to: base.Add(5678).In(time.FixedZone("offset", 19800)), wantReads: 1},
		{name: "one microsecond", from: base.Add(123), to: base.Add(1123), wantReads: 1},
		{name: "collapsed interval", from: base.Add(123), to: base.Add(999), wantCode: "invalid_request"},
		{name: "source changed canonical bound", from: base.Add(-time.Hour + 1234), to: base.Add(5678), drift: time.Microsecond, wantCode: "source_unavailable", wantReads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads atomic.Int32
			var discoveries atomic.Int32
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/capabilities":
					discoveries.Add(1)
					respond(w, capabilities(base))
				case "/v1/me":
					respond(w, grants(base, "1"))
				case "/v1/namespaces/lab/summary":
					reads.Add(1)
					from, errFrom := time.Parse(time.RFC3339Nano, r.URL.Query().Get("completedFrom"))
					to, errTo := time.Parse(time.RFC3339Nano, r.URL.Query().Get("completedBefore"))
					if errFrom != nil || errTo != nil {
						t.Error("summary bounds are not RFC3339 timestamps")
						http.Error(w, "invalid bounds", http.StatusBadRequest)
						return
					}
					// Model the real Control SQL timestamptz round trip: any
					// sub-microsecond precision cannot survive the source read.
					from = from.Truncate(time.Microsecond)
					to = to.Truncate(time.Microsecond).Add(tc.drift)
					respond(w, map[string]any{"apiVersion": contract, "kind": "NamespaceSummary", "summary": map[string]any{
						"namespaceId": namespaceID, "namespace": "lab", "asOf": base,
						"completedFrom": from, "completedBefore": to, "total": "7", "active": "5", "awaitingExecution": "2", "evidenceAttention": "1", "missingCompletionTime": "0",
						"byPhase": map[string]string{"running": "3"}, "byOutcome": map[string]string{"success": "2"},
					}})
				default:
					t.Errorf("unexpected summary endpoint %q", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			peer := unavailableOverviewPeer{}
			e, err := monitoring.New([]monitoring.Source{c, peer}, monitoring.NewMemoryCursors())
			if err != nil {
				t.Fatal(err)
			}
			e.Now = func() time.Time { return base }
			q := monitoring.Query{Scopes: []api.Scope{{DeploymentID: c.ID(), NamespaceID: namespaceID}, {DeploymentID: peer.ID(), NamespaceID: namespaceID}}}
			o, err := e.Overview(t.Context(), testActor, q, api.Window{From: tc.from, To: tc.to})
			if tc.wantCode != "" {
				var failure *api.Error
				if !errors.As(err, &failure) || failure.Code != tc.wantCode || o.Active != nil {
					t.Fatalf("unexpected invalid-window/summary result: error=%v active=%v", err, o.Active)
				}
			} else {
				if err != nil || o.Completeness != "partial" || len(o.Sources) != 2 || o.Active == nil || *o.Active != 5 || o.Terminal["success"] == nil || *o.Terminal["success"] != 2 {
					t.Fatalf("healthy contribution lost after precision round trip: error=%v overview=%+v", err, o)
				}
				if !o.Window.From.Equal(tc.from.Truncate(time.Microsecond)) || !o.Window.To.Equal(tc.to.Truncate(time.Microsecond)) || o.Window.From.Location() != time.UTC || o.Window.To.Location() != time.UTC {
					t.Fatal("published window differs from the exact canonical source query")
				}
				if o.Sources[0].Status != "available" || o.Sources[1].Status != "authorization_unavailable" {
					t.Fatal("source outage provenance changed")
				}
			}
			if reads.Load() != tc.wantReads || tc.wantReads == 0 && discoveries.Load() != 0 {
				t.Fatalf("unexpected source work: reads=%d discoveries=%d", reads.Load(), discoveries.Load())
			}
		})
	}
}
