package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func TestEventDelegationHasIndependentServiceIdentityAndExactScope(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(7), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	ns := "33333333-3333-4333-8333-333333333333"
	other := "44444444-4444-4444-8444-444444444444"
	signer, err := NewDelegationSigner(key, "event-key", "dashboard-feed", "urn:control:feed", der, []string{ns, other})
	if err != nil {
		t.Fatal(err)
	}
	signer.Now = func() time.Time { return now }
	previous := ""
	for range 2 {
		value, err := signer.AuthorizeEvents([]string{ns})
		if err != nil {
			t.Fatal(err)
		}
		token, err := jwt.ParseSigned(strings.TrimPrefix(value, DelegationScheme+" "), []jose.SignatureAlgorithm{jose.EdDSA})
		if err != nil {
			t.Fatal(err)
		}
		var claims ServiceEventClaims
		var raw map[string]any
		if err = token.Claims(pub, &claims, &raw); err != nil {
			t.Fatal(err)
		}
		if _, exists := raw["actor"]; exists {
			t.Fatal("service assertion contains actor field")
		}
		sum := sha256.Sum256(der)
		if claims.ValidateWithLeeway(jwt.Expected{Issuer: "dashboard-feed", Subject: "dashboard-feed", AnyAudience: jwt.Audience{"urn:control:feed"}, Time: now}, 0) != nil || claims.Operation != "events.read" || claims.Mode != "worker" || len(claims.NamespaceIDs) != 1 || claims.NamespaceIDs[0] != ns || claims.Confirmation["x5t#S256"] != base64.RawURLEncoding.EncodeToString(sum[:]) || claims.Expiry.Time().Sub(claims.IssuedAt.Time()) != time.Minute || claims.ID == previous || len(claims.ID) < 22 || token.Headers[0].KeyID != "event-key" {
			t.Fatal("service assertion lost identity, scope, nonce or transport binding")
		}
		previous = claims.ID
	}
	for _, scope := range [][]string{nil, {ns, ns}, {"55555555-5555-4555-8555-555555555555"}} {
		if _, err = signer.AuthorizeEvents(scope); err == nil {
			t.Fatal("invalid event scope signed")
		}
	}
	signer.Now = func() time.Time { return now.Add(59*time.Minute + time.Second) }
	if _, err = signer.AuthorizeEvents([]string{ns}); err == nil {
		t.Fatal("assertion outlived certificate")
	}
}
