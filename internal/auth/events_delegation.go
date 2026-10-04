package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"slices"
	"time"

	"github.com/go-jose/go-jose/v4/jwt"
)

// ServiceEventClaims is deliberately disjoint from represented-user claims.
// It has no actor field, including no null/empty actor, and cannot be used on
// user detail routes. Control independently checks the registered service grant.
type ServiceEventClaims struct {
	jwt.Claims
	Confirmation map[string]string `json:"cnf"`
	Operation    string            `json:"operation"`
	NamespaceIDs []string          `json:"namespaceIds"`
	Mode         string            `json:"mode"`
}

// AuthorizeEvents signs only the explicit configured subset for service-only
// background ingestion. It never accepts a user, arbitrary operation or mode.
func (s *DelegationSigner) AuthorizeEvents(namespaces []string) (string, error) {
	if len(namespaces) == 0 || len(namespaces) > 320 {
		return "", errors.New("event delegation requires an explicit namespace set")
	}
	ns := slices.Clone(namespaces)
	slices.Sort(ns)
	for i, id := range ns {
		if !slices.Contains(s.namespaces, id) || i > 0 && ns[i-1] == id {
			return "", errors.New("event namespace is not allowed by this service")
		}
	}
	now := s.Now().UTC()
	expires := now.Add(60 * time.Second)
	if now.Before(s.certificateValidFrom) || !expires.Before(s.certificateExpires) {
		return "", errors.New("delegation client certificate is not valid for the assertion lifetime")
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", errors.New("could not create event delegation assertion")
	}
	claims := ServiceEventClaims{Claims: jwt.Claims{Issuer: s.serviceID, Subject: s.serviceID, Audience: jwt.Audience{s.audience}, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(expires), ID: base64.RawURLEncoding.EncodeToString(nonce[:])}, Confirmation: map[string]string{"x5t#S256": s.thumbprint}, Operation: "events.read", NamespaceIDs: ns, Mode: "worker"}
	compact, err := jwt.Signed(s.signer).Claims(claims).Serialize()
	if err != nil {
		return "", errors.New("could not sign event delegation assertion")
	}
	return DelegationScheme + " " + compact, nil
}
