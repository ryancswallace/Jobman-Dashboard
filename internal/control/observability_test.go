package control

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/observability"
)

func TestSourceObservationsRequireCompleteAuthorityAndKeepLabelsPrivate(t *testing.T) {
	now := time.Now().UTC()
	var fail atomic.Bool
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			respond(w, capabilities(now))
		case "/v1/me":
			if fail.Load() {
				http.Error(w, "private-directory-error", 503)
				return
			}
			respond(w, grants(now, "2"))
		}
	}))
	r, err := observability.NewRegistry("api", 1, []string{deploymentID}, observability.Build{})
	if err != nil {
		t.Fatal(err)
	}
	c.config.Observer = r
	fail.Store(true)
	if _, err = c.Discover(t.Context(), testActor); err == nil {
		t.Fatal("failed authority accepted")
	}
	raw := string(r.Render())
	if strings.Contains(raw, "authorization_proof_age_seconds{deployment=") {
		t.Fatal("failed discovery presented freshness")
	}
	fail.Store(false)
	if _, err = c.Discover(t.Context(), testActor); err != nil {
		t.Fatal(err)
	}
	raw = string(r.Render())
	if !strings.Contains(raw, `operation="namespace",deployment="`+deploymentID+`",mode="interactive",outcome="unavailable"} 1`) || !strings.Contains(raw, "authorization_proof_age_seconds{deployment=") {
		t.Fatal(raw)
	}
	for _, private := range []string{namespaceID, principalID, "private-directory-error", testActor.Subject} {
		if strings.Contains(raw, private) {
			t.Fatal("private source detail escaped")
		}
	}
}
