package control

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
)

func TestControlConstructorBindsVerifiedActorMode(t *testing.T) {
	var calls atomic.Int64
	modes := make(chan string, 2)
	base := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		token, err := jwt.ParseSigned(strings.TrimPrefix(r.Header.Get("Authorization"), auth.DelegationScheme+" "), []jose.SignatureAlgorithm{jose.EdDSA})
		var claims auth.DelegationClaims
		if err != nil || len(r.TLS.VerifiedChains) == 0 || token.Claims(r.TLS.PeerCertificates[0].PublicKey, &claims) != nil {
			t.Error("assertion was not certificate-authenticated")
			http.Error(w, "invalid", http.StatusUnauthorized)
			return
		}
		modes <- claims.Mode
		respond(w, map[string]bool{"ok": true})
	}))
	for _, mode := range []auth.DelegationMode{"", auth.DelegationWorker} {
		cfg := base.config
		cfg.ActorMode = mode
		client, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]bool
		err = client.get(t.Context(), testActor, "jobs.read", namespaceID, "/v1/probe", nil, &value)
		client.Close()
		if err != nil || !value["ok"] {
			t.Fatal(err)
		}
		want, _ := mode.Canonical()
		if got := <-modes; got != string(want) {
			t.Fatal("signed mode differs", got, want)
		}
	}
	cfg := base.config
	cfg.ActorMode = "arbitrary-caller-mode"
	if _, err := New(cfg); err == nil || calls.Load() != 2 {
		t.Fatal("invalid constructor mode reached network", err)
	}
}
