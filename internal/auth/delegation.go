// Package auth owns client authentication and source-bound read delegation.
package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

const DelegationScheme = "Jobman-Delegation"

type DelegatedActor struct {
	DirectoryID string `json:"directoryId"`
	Issuer      string `json:"issuer"`
	Subject     string `json:"subject"`
}

type DelegationClaims struct {
	jwt.Claims
	Confirmation map[string]string `json:"cnf"`
	Actor        DelegatedActor    `json:"actor"`
	Operation    string            `json:"operation"`
	NamespaceIDs []string          `json:"namespaceIds"`
	Mode         string            `json:"mode"`
}

type DelegationSigner struct {
	signer               jose.Signer
	serviceID            string
	audience             string
	thumbprint           string
	namespaces           []string
	certificateExpires   time.Time
	certificateValidFrom time.Time
	Now                  func() time.Time
}

// NewDelegationSigner pins one destination audience, leaf client certificate and
// explicit namespace set. It cannot mint execution/admin capabilities or select
// a caller-supplied backend audience/certificate at request time.
func NewDelegationSigner(key ed25519.PrivateKey, keyID, serviceID, audience string, certificateDER []byte, namespaces []string) (*DelegationSigner, error) {
	if len(key) != ed25519.PrivateKeySize || keyID == "" || serviceID == "" || audience == "" || len(namespaces) == 0 || len(namespaces) > 320 {
		return nil, errors.New("incomplete source delegation configuration")
	}
	for _, s := range namespaces {
		if !uuid(s) {
			return nil, errors.New("delegation namespaces require immutable UUIDs")
		}
	}
	cert, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		return nil, errors.New("invalid delegation client certificate")
	}
	options := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, options)
	if err != nil {
		return nil, errors.New("invalid delegation signing key")
	}
	sum := sha256.Sum256(certificateDER)
	ns := slices.Clone(namespaces)
	slices.Sort(ns)
	ns = slices.Compact(ns)
	return &DelegationSigner{signer: signer, serviceID: serviceID, audience: audience, thumbprint: base64.RawURLEncoding.EncodeToString(sum[:]), namespaces: ns, certificateExpires: cert.NotAfter, certificateValidFrom: cert.NotBefore, Now: time.Now}, nil
}

func (s *DelegationSigner) Authorize(actor monitoring.Actor, operation, namespace, mode string) (string, error) {
	if !slices.Contains([]string{"namespace.read", "jobs.read", "groups.read", "targets.read", "logs.read", "artifacts.read", "evidence.read"}, operation) {
		return "", errors.New("delegation operation is not an allowed monitoring read")
	}
	if !uuid(actor.DirectoryID) || actor.Issuer == "" || actor.Subject == "" || len(actor.Issuer) > 512 || len(actor.Subject) > 512 || !slices.Contains([]string{"interactive", "worker"}, mode) {
		return "", errors.New("delegation requires a verified actor and mode")
	}
	ns := s.namespaces
	if operation != "namespace.read" && namespace == "" {
		return "", errors.New("resource delegation requires a namespace")
	}
	if namespace != "" {
		if !slices.Contains(ns, namespace) {
			return "", errors.New("namespace is not allowed by this service")
		}
		ns = []string{namespace}
	}
	now := s.Now().UTC()
	expires := now.Add(60 * time.Second)
	if now.Before(s.certificateValidFrom) || !expires.Before(s.certificateExpires) {
		return "", errors.New("delegation client certificate is not valid for the assertion lifetime")
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("could not create delegation assertion")
	}
	claims := DelegationClaims{Claims: jwt.Claims{Issuer: s.serviceID, Subject: actor.DirectoryID, Audience: jwt.Audience{s.audience}, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(expires), ID: base64.RawURLEncoding.EncodeToString(random[:])}, Confirmation: map[string]string{"x5t#S256": s.thumbprint}, Actor: DelegatedActor{DirectoryID: actor.DirectoryID, Issuer: actor.Issuer, Subject: actor.Subject}, Operation: operation, NamespaceIDs: slices.Clone(ns), Mode: mode}
	compact, err := jwt.Signed(s.signer).Claims(claims).Serialize()
	if err != nil {
		return "", errors.New("could not sign delegation assertion")
	}
	return DelegationScheme + " " + compact, nil
}

func uuid(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
