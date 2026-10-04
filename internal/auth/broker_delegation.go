package auth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type BrokerService struct {
	KeyID, ServiceID, Audience, DeploymentID, CertificateSHA256 string
	PublicKey                                                   ed25519.PublicKey
	NamespaceIDs                                                []string
}

// BrokerVerifier trusts only explicitly registered Dashboard identities over
// verified mTLS. It authorizes the delegation boundary, not the resource read;
// the broker must still ask Control for current represented-user authority.
type BrokerVerifier struct {
	services    map[string]BrokerService
	now         func() time.Time
	issuedAfter time.Time
	mu          sync.Mutex
	used        map[string]time.Time
}

func NewBrokerVerifier(services []BrokerService) (*BrokerVerifier, error) {
	if len(services) < 1 || len(services) > 64 {
		return nil, errors.New("configure 1–64 broker service registrations")
	}
	v := &BrokerVerifier{services: make(map[string]BrokerService), used: make(map[string]time.Time), now: time.Now, issuedAfter: time.Now().UTC().Add(5 * time.Second)}
	for _, s := range services {
		fingerprint, err := base64.RawURLEncoding.DecodeString(s.CertificateSHA256)
		if s.KeyID == "" || len(s.KeyID) > 128 || s.ServiceID == "" || len(s.ServiceID) > 256 || s.Audience == "" || len(s.Audience) > 256 || !uuid(s.DeploymentID) || len(s.PublicKey) != ed25519.PublicKeySize || err != nil || len(fingerprint) != 32 || len(s.NamespaceIDs) < 1 || len(s.NamespaceIDs) > 320 {
			return nil, errors.New("invalid broker service registration")
		}
		if _, ok := v.services[s.KeyID]; ok {
			return nil, errors.New("broker signing key IDs must be unique")
		}
		for _, ns := range s.NamespaceIDs {
			if !uuid(ns) {
				return nil, errors.New("broker service namespace must be a UUID")
			}
		}
		s.PublicKey = slices.Clone(s.PublicKey)
		s.NamespaceIDs = slices.Clone(s.NamespaceIDs)
		v.services[s.KeyID] = s
	}
	return v, nil
}

// VerifiedDelegation is emitted only after signature, certificate, audience,
// operation, namespace, freshness and replay checks. Mode is provenance, not a
// permission grant; downstream reads still require current Control authority.
type VerifiedDelegation struct {
	Actor monitoring.Actor
	Mode  DelegationMode
}

func (v *BrokerVerifier) Authenticate(r *http.Request, scope api.Scope) (monitoring.Actor, error) {
	verified, err := v.AuthenticateDelegation(r, scope)
	return verified.Actor, err
}

func (v *BrokerVerifier) AuthenticateDelegation(r *http.Request, scope api.Scope) (VerifiedDelegation, error) {
	deny := func() (VerifiedDelegation, error) { return VerifiedDelegation{}, monitoring.ErrAuthority }
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 || len(r.Header.Values("Authorization")) != 1 {
		return deny()
	}
	value := r.Header.Get("Authorization")
	if len(value) > 8192 || !strings.HasPrefix(value, DelegationScheme+" ") {
		return deny()
	}
	compact := strings.TrimPrefix(value, DelegationScheme+" ")
	if strings.Count(compact, ".") != 2 {
		return deny()
	}
	token, err := jwt.ParseSigned(compact, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil || len(token.Headers) != 1 {
		return deny()
	}
	h := token.Headers[0]
	s, ok := v.services[h.KeyID]
	if !ok || h.Algorithm != string(jose.EdDSA) || h.ExtraHeaders[jose.HeaderType] != "JWT" || s.DeploymentID != scope.DeploymentID || !slices.Contains(s.NamespaceIDs, scope.NamespaceID) {
		return deny()
	}
	now := v.now().UTC()
	cert := r.TLS.PeerCertificates[0]
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return deny()
	}
	thumbprint := sha256.Sum256(cert.Raw)
	if base64.RawURLEncoding.EncodeToString(thumbprint[:]) != s.CertificateSHA256 {
		return deny()
	}
	var claims DelegationClaims
	if token.Claims(s.PublicKey, &claims) != nil || claims.ValidateWithLeeway(jwt.Expected{Issuer: s.ServiceID, AnyAudience: jwt.Audience{s.Audience}, Time: now}, 5*time.Second) != nil {
		return deny()
	}
	if claims.IssuedAt == nil || claims.NotBefore == nil || claims.Expiry == nil || len(claims.Audience) != 1 || claims.IssuedAt.Time().Before(v.issuedAfter) || claims.IssuedAt.Time().After(now.Add(5*time.Second)) || claims.NotBefore.Time() != claims.IssuedAt.Time() || !claims.Expiry.Time().After(claims.IssuedAt.Time()) || claims.Expiry.Time().Sub(claims.IssuedAt.Time()) > time.Minute {
		return deny()
	}
	// Keep token acceptance and replay retention on the same exclusive end
	// boundary; jose's leeway validator alone accepts the exact expiry instant.
	if !now.Before(claims.Expiry.Time().Add(5 * time.Second)) {
		return deny()
	}
	if claims.Operation != "logs.read" || !slices.Contains([]string{"interactive", "worker"}, claims.Mode) || len(claims.NamespaceIDs) != 1 || claims.NamespaceIDs[0] != scope.NamespaceID || len(claims.Confirmation) != 1 || claims.Confirmation["x5t#S256"] != s.CertificateSHA256 || !uuid(claims.Actor.DirectoryID) || claims.Subject != claims.Actor.DirectoryID || claims.Actor.Issuer == "" || len(claims.Actor.Issuer) > 512 || claims.Actor.Subject == "" || len(claims.Actor.Subject) > 512 {
		return deny()
	}
	id, err := base64.RawURLEncoding.DecodeString(claims.ID)
	if err != nil || len(id) != 24 {
		return deny()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for id, expires := range v.used {
		if !now.Before(expires) {
			delete(v.used, id)
		}
	}
	replayKey := s.ServiceID + "/" + claims.ID
	if _, replay := v.used[replayKey]; replay || len(v.used) >= 10000 {
		return deny()
	}
	v.used[replayKey] = claims.Expiry.Time().Add(5 * time.Second)
	// No Dashboard account ID is accepted from the request body. The signed
	// immutable directory identity is the broker's stable cursor/account scope.
	return VerifiedDelegation{Actor: monitoring.Actor{Account: api.Account{ID: claims.Actor.DirectoryID}, DirectoryID: claims.Actor.DirectoryID, Issuer: claims.Actor.Issuer, Subject: claims.Actor.Subject}, Mode: DelegationMode(claims.Mode)}, nil
}
