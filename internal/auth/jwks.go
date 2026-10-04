package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
)

var errKeyAuthorityUnavailable = errors.New("identity key authority unavailable")

// go-oidc's IDTokenVerifier formats KeySet errors with %v. Preserve the original
// dependency classification per verification, before that formatting erases it.
// This state is never shared by concurrent token verifications.
type verificationAttempt struct{ unavailable bool }
type verificationAttemptKey struct{}

type classifiedKeySet struct{ oidc.KeySet }

func (k classifiedKeySet) VerifySignature(ctx context.Context, token string) ([]byte, error) {
	payload, err := k.KeySet.VerifySignature(ctx, token)
	if attempt, ok := ctx.Value(verificationAttemptKey{}).(*verificationAttempt); ok {
		attempt.unavailable = errors.Is(err, errKeyAuthorityUnavailable)
	}
	return payload, err
}

type classifiedVerifier struct{ verifier *oidc.IDTokenVerifier }

func (v classifiedVerifier) Verify(ctx context.Context, token string) (*oidc.IDToken, error) {
	attempt := new(verificationAttempt)
	verified, err := v.verifier.Verify(context.WithValue(ctx, verificationAttemptKey{}, attempt), token)
	if err != nil && attempt.unavailable {
		return nil, errKeyAuthorityUnavailable
	}
	return verified, err
}

// Only the dedicated JWKS client uses this wrapper. Discovery and token-exchange
// error handling remain separate. RemoteKeySet retains its normal cache and
// concurrent-fetch coalescing, including successful verification from cache.
type keyAuthorityTransport struct{ base http.RoundTripper }

func (t keyAuthorityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, errKeyAuthorityUnavailable
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, errKeyAuthorityUnavailable
	}
	// Syntax/envelope failures are provider failures too. Cryptographic key
	// parsing, algorithm selection and signature verification stay in go-oidc.
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	_ = response.Body.Close()
	var envelope struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &envelope) != nil || envelope.Keys == nil {
		return nil, errKeyAuthorityUnavailable
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}

func newIdentityVerifier(ctx context.Context, options OIDCOptions, keysURL, audience string) tokenVerifier {
	client := *options.HTTPClient
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = keyAuthorityTransport{base: base}
	verificationContext := oidc.ClientContext(context.WithoutCancel(ctx), &client)
	keys := classifiedKeySet{oidc.NewRemoteKeySet(verificationContext, keysURL)}
	return classifiedVerifier{oidc.NewVerifier(options.Issuer, keys, &oidc.Config{ClientID: audience, SupportedSigningAlgs: []string{"RS256"}})}
}
