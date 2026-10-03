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
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func TestDelegationIsBoundToActorAudienceCertificateNamespaceAndLifetime(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	ns := "33333333-3333-4333-8333-333333333333"
	signer, err := NewDelegationSigner(key, "test-key", "dashboard-api", "urn:control:test", der, []string{ns})
	if err != nil {
		t.Fatal(err)
	}
	signer.Now = func() time.Time { return now }
	actor := monitoring.Actor{Account: api.Account{ID: "not-forwarded"}, DirectoryID: "44444444-4444-4444-8444-444444444444", Issuer: "https://idp.invalid", Subject: "immutable-subject"}
	value, err := signer.Authorize(actor, "jobs.read", ns, "interactive")
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.ParseSigned(strings.TrimPrefix(value, DelegationScheme+" "), []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		t.Fatal(err)
	}
	var c DelegationClaims
	if err = token.Claims(pub, &c); err != nil {
		t.Fatal(err)
	}
	if err = c.ValidateWithLeeway(jwt.Expected{Issuer: "dashboard-api", Subject: actor.DirectoryID, AnyAudience: jwt.Audience{"urn:control:test"}, Time: now}, 0); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	if c.Confirmation["x5t#S256"] != base64.RawURLEncoding.EncodeToString(sum[:]) || c.Actor.Subject != actor.Subject || len(c.NamespaceIDs) != 1 || c.NamespaceIDs[0] != ns || c.Expiry.Time().Sub(c.IssuedAt.Time()) != time.Minute {
		t.Fatal("delegation trust binding changed")
	}
	if err = c.ValidateWithLeeway(jwt.Expected{AnyAudience: jwt.Audience{"another-control"}, Time: now}, 0); err == nil {
		t.Fatal("wrong audience accepted")
	}
	if err = c.ValidateWithLeeway(jwt.Expected{Time: now.Add(61 * time.Second)}, 0); err == nil {
		t.Fatal("expired assertion accepted")
	}
	for _, op := range []string{"jobs.submit", "jobs.cancel.any", "memberships.manage", "events.read"} {
		if _, err = signer.Authorize(actor, op, ns, "interactive"); err == nil {
			t.Fatalf("unsafe delegation %s allowed", op)
		}
	}
	if _, err = signer.Authorize(actor, "logs.read", "55555555-5555-4555-8555-555555555555", "interactive"); err == nil {
		t.Fatal("other namespace permitted")
	}
	actor.DirectoryID = ""
	if _, err = signer.Authorize(actor, "jobs.read", ns, "interactive"); err == nil {
		t.Fatal("unverified identity permitted")
	}
}
