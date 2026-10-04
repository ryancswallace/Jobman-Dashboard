package auth

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"golang.org/x/oauth2"
)

const sessionCookie = "__Host-jobman-session"
const loginCookie = "__Host-jobman-login"

type OIDCOptions struct {
	Issuer, Audience, WebClientID, WebClientSecret, NativeClientID, NativeRedirectURI string
	DirectoryIDClaim, ClientIDClaim, PublicOrigin                                     string
	Scopes                                                                            []string
	EncryptionKey                                                                     []byte
	EncryptionKeyID                                                                   string
	HTTPClient                                                                        *http.Client
}
type tokenVerifier interface {
	Verify(context.Context, string) (*oidc.IDToken, error)
}
type OIDC struct {
	options     OIDCOptions
	store       IdentityStore
	box         *secretBox
	webVerifier tokenVerifier
	apiVerifier tokenVerifier
	oauth       oauth2.Config
	now         func() time.Time
}

// NewOIDC performs discovery once at startup using private certificate roots.
// API and sign-in audiences must be different: an ID token is never an API credential.
func NewOIDC(ctx context.Context, o OIDCOptions, store IdentityStore) (*OIDC, error) {
	issuer, err := url.Parse(o.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return nil, errors.New("OIDC issuer requires a private trusted HTTPS URL")
	}
	origin, err := url.Parse(o.PublicOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		return nil, errors.New("Dashboard public origin must use HTTPS")
	}
	if store == nil || o.HTTPClient == nil || o.Audience == "" || o.WebClientID == "" || o.WebClientSecret == "" || o.NativeClientID == "" || o.NativeClientID == o.WebClientID || o.Audience == o.WebClientID || o.Audience == o.NativeClientID || o.DirectoryIDClaim == "" || o.ClientIDClaim == "" || o.NativeRedirectURI != "jobman-dashboard-auth://callback" {
		return nil, errors.New("incomplete distinct web/native/API identity configuration")
	}
	o.PublicOrigin = strings.TrimSuffix(o.PublicOrigin, "/")
	box, err := newSecretBox(o.EncryptionKey, o.EncryptionKeyID)
	if err != nil {
		return nil, err
	}
	clientContext := oidc.ClientContext(ctx, o.HTTPClient)
	p, err := oidc.NewProvider(clientContext, o.Issuer)
	if err != nil {
		return nil, errors.New("AD FS discovery failed")
	}
	endpoint := p.Endpoint()
	for _, raw := range []string{endpoint.AuthURL, endpoint.TokenURL} {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "https" || u.Host != issuer.Host || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return nil, errors.New("AD FS endpoints must match the configured issuer origin")
		}
	}
	var metadata struct {
		PKCE []string `json:"code_challenge_methods_supported"`
		JWKS string   `json:"jwks_uri"`
	}
	if err := p.Claims(&metadata); err != nil {
		return nil, errors.New("invalid AD FS metadata")
	}
	keys, e := url.Parse(metadata.JWKS)
	if e != nil || keys.Scheme != "https" || keys.Host != issuer.Host || keys.User != nil || keys.Fragment != "" {
		return nil, errors.New("AD FS key endpoint must match the configured issuer origin")
	}
	if len(metadata.PKCE) > 0 && !slices.Contains(metadata.PKCE, "S256") {
		return nil, errors.New("AD FS must support S256 PKCE")
	}
	scopes := []string{"openid", "profile"}
	for _, s := range o.Scopes {
		if s != "offline_access" && !slices.Contains(scopes, s) {
			scopes = append(scopes, s)
		}
	}
	return &OIDC{options: o, store: store, box: box, webVerifier: newIdentityVerifier(ctx, o, metadata.JWKS, o.WebClientID), apiVerifier: newIdentityVerifier(ctx, o, metadata.JWKS, o.Audience), oauth: oauth2.Config{ClientID: o.WebClientID, ClientSecret: o.WebClientSecret, Endpoint: endpoint, RedirectURL: o.PublicOrigin + "/auth/callback", Scopes: scopes}, now: time.Now}, nil
}

// PinnedIdentityHTTPClient does not follow redirects or send issuer traffic to
// another origin, and bounds even discovery/JWKS bodies read by dependencies.
func PinnedIdentityHTTPClient(issuer string, roots *x509.CertPool) (*http.Client, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || roots == nil {
		return nil, errors.New("invalid identity trust")
	}
	t := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots.Clone()}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 32 << 10, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 8, IdleConnTimeout: time.Minute}
	return &http.Client{Transport: identityTransport{base: t, origin: u.Scheme + "://" + u.Host}, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

type identityTransport struct {
	base   http.RoundTripper
	origin string
}

func (t identityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme+"://"+r.URL.Host != t.origin || r.URL.User != nil {
		return nil, errors.New("identity endpoint outside configured origin")
	}
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	response.Body.Close()
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("identity response exceeds bound")
	}
	response.Body = io.NopCloser(strings.NewReader(string(data)))
	return response, nil
}

func (o *OIDC) identity(token *oidc.IDToken, native bool) (Identity, error) {
	var claims map[string]json.RawMessage
	if token == nil || token.Issuer != o.options.Issuer || token.Subject == "" || len(token.Subject) > 512 || !o.now().Before(token.Expiry) || token.IssuedAt.After(o.now().Add(5*time.Second)) || token.Claims(&claims) != nil {
		return Identity{}, ErrUnauthenticated
	}
	read := func(key string) string { var value string; _ = json.Unmarshal(claims[key], &value); return value }
	if !native {
		// The library verifies audience membership, not the authorized party.
		// A multi-audience ID token must identify this web registration as azp.
		_, hasAuthorizedParty := claims["azp"]
		if token.IssuedAt.IsZero() || (len(token.Audience) > 1 && !hasAuthorizedParty) || (hasAuthorizedParty && read("azp") != o.options.WebClientID) {
			return Identity{}, ErrUnauthenticated
		}
	}
	if raw, present := claims["nbf"]; present {
		var seconds int64
		if json.Unmarshal(raw, &seconds) != nil || time.Unix(seconds, 0).After(o.now().Add(5*time.Second)) {
			return Identity{}, ErrUnauthenticated
		}
	}
	directoryID := strings.ToLower(read(o.options.DirectoryIDClaim))
	if !uuid(directoryID) {
		return Identity{}, ErrUnauthenticated
	}
	if native {
		// ID tokens name an interactive client as audience. Reject that audience
		// even if an issuer mapper also (incorrectly) includes the API resource.
		if read(o.options.ClientIDClaim) != o.options.NativeClientID || slices.Contains(token.Audience, o.options.NativeClientID) || slices.Contains(token.Audience, o.options.WebClientID) {
			return Identity{}, ErrUnauthenticated
		}
	}
	name := read("name")
	if name == "" {
		name = "AD user"
	}
	if len(name) > 256 {
		name = name[:256]
	}
	return Identity{Issuer: token.Issuer, Subject: token.Subject, DirectoryID: directoryID, DisplayName: name}, nil
}

func (o *OIDC) Authenticate(r *http.Request) (monitoring.Actor, error) {
	if r.Host != strings.TrimPrefix(o.options.PublicOrigin, "https://") {
		return monitoring.Actor{}, ErrUnauthenticated
	}
	header := r.Header.Values("Authorization")
	cookies := r.CookiesNamed(sessionCookie)
	if len(header) > 0 {
		if len(header) != 1 || len(cookies) != 0 {
			return monitoring.Actor{}, ErrUnauthenticated
		}
		scheme, token, ok := strings.Cut(header[0], " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || len(token) > 16384 || strings.ContainsAny(token, " \t\r\n") {
			return monitoring.Actor{}, ErrUnauthenticated
		}
		verified, err := o.apiVerifier.Verify(r.Context(), token)
		if err != nil {
			if verifierUnavailable(r.Context(), err) {
				return monitoring.Actor{}, monitoring.ErrSource
			}
			return monitoring.Actor{}, ErrUnauthenticated
		}
		identity, err := o.identity(verified, true)
		if err != nil {
			return monitoring.Actor{}, err
		}
		return o.store.ResolveIdentity(r.Context(), identity)
	}
	if len(cookies) != 1 || len(cookies[0].Value) != 43 {
		return monitoring.Actor{}, ErrUnauthenticated
	}
	cookie := cookies[0]
	session, err := o.store.Session(r.Context(), tokenHash(cookie.Value))
	if err != nil {
		return monitoring.Actor{}, err
	}
	csrf := o.box.csrf(cookie.Value)
	if subtle.ConstantTimeCompare(session.CSRFHash, tokenHash(csrf)) != 1 {
		return monitoring.Actor{}, ErrUnauthenticated
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
		if r.Header.Get("Origin") != o.options.PublicOrigin || len(r.Header.Values("X-CSRF-Token")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrf)) != 1 {
			return monitoring.Actor{}, ErrUnauthenticated
		}
	}
	session.Actor.CSRFToken = csrf
	return session.Actor, nil
}

func (o *OIDC) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", o.login)
	mux.HandleFunc("GET /auth/callback", o.callback)
	mux.HandleFunc("POST /auth/logout", o.logout)
	mux.HandleFunc("GET /auth/native/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		scopes := []string{"openid", "profile", "offline_access"}
		for _, scope := range o.options.Scopes {
			if !slices.Contains(scopes, scope) {
				scopes = append(scopes, scope)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": o.options.Issuer, "clientId": o.options.NativeClientID, "audience": o.options.Audience, "redirectURI": o.options.NativeRedirectURI, "scopes": scopes})
	})
}

type loginState struct{ Verifier, Nonce, ReturnTo string }

func (o *OIDC) login(w http.ResponseWriter, r *http.Request) {
	if r.Host != strings.TrimPrefix(o.options.PublicOrigin, "https://") {
		authFailure(w, r)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 || len(query["returnTo"]) > 1 {
		authFailure(w, r)
		return
	}
	for key := range query {
		if key != "returnTo" {
			authFailure(w, r)
			return
		}
	}
	returnTo := query.Get("returnTo")
	if returnTo == "" {
		returnTo = "/"
	}
	if !safeReturn(returnTo) {
		authFailure(w, r)
		return
	}
	state, err := randomToken()
	if err != nil {
		authFailure(w, r)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		authFailure(w, r)
		return
	}
	payload, _ := json.Marshal(loginState{Verifier: oauth2.GenerateVerifier(), Nonce: nonce, ReturnTo: returnTo})
	encrypted, err := o.box.seal(payload)
	if err != nil {
		authFailure(w, r)
		return
	}
	if err := o.store.PutLogin(r.Context(), tokenHash(state), encrypted, o.now().Add(5*time.Minute)); err != nil {
		authStoreFailure(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Value: state, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	var flow loginState
	_ = json.Unmarshal(payload, &flow)
	http.Redirect(w, r, o.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(flow.Verifier), oauth2.SetAuthURLParam("resource", o.options.Audience)), http.StatusFound)
}
func safeReturn(value string) bool {
	if len(value) > 2048 || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\\r\n") {
		return false
	}
	u, err := url.Parse(value)
	return err == nil && !u.IsAbs() && u.Host == "" && u.User == nil && !strings.HasPrefix(u.Path, "/auth/") && !strings.HasPrefix(u.Path, "//") && !strings.ContainsAny(u.Path, "\\\r\n")
}
func (o *OIDC) callback(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, loginCookie)
	if r.Host != strings.TrimPrefix(o.options.PublicOrigin, "https://") {
		authFailure(w, r)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q["state"]) != 1 || len(q["code"]) != 1 || len(q.Get("code")) > 4096 {
		authFailure(w, r)
		return
	}
	cookies := r.CookiesNamed(loginCookie)
	state := q.Get("state")
	if len(cookies) != 1 || len(r.CookiesNamed(sessionCookie)) > 1 || len(state) != 43 || subtle.ConstantTimeCompare([]byte(cookies[0].Value), []byte(state)) != 1 {
		authFailure(w, r)
		return
	}
	encrypted, err := o.store.ConsumeLogin(r.Context(), tokenHash(state))
	if err != nil {
		authStoreFailure(w, r, err)
		return
	}
	payload, err := o.box.open(encrypted)
	if err != nil {
		authFailure(w, r)
		return
	}
	var flow loginState
	if json.Unmarshal(payload, &flow) != nil || !safeReturn(flow.ReturnTo) {
		authFailure(w, r)
		return
	}
	ctx := oidc.ClientContext(r.Context(), o.options.HTTPClient)
	tokens, err := o.oauth.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(flow.Verifier), oauth2.SetAuthURLParam("resource", o.options.Audience))
	if err != nil {
		var rejected *oauth2.RetrieveError
		if errors.As(err, &rejected) && rejected.Response != nil && slices.Contains([]int{400, 401, 403}, rejected.Response.StatusCode) {
			authFailure(w, r)
		} else {
			authUnavailable(w)
		}
		return
	}
	raw, ok := tokens.Extra("id_token").(string)
	if !ok || len(raw) > 16384 {
		authFailure(w, r)
		return
	}
	verified, err := o.webVerifier.Verify(r.Context(), raw)
	if err != nil {
		if verifierUnavailable(r.Context(), err) {
			authUnavailable(w)
		} else {
			authFailure(w, r)
		}
		return
	}
	if subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(flow.Nonce)) != 1 {
		authFailure(w, r)
		return
	}
	identity, err := o.identity(verified, false)
	if err != nil {
		authFailure(w, r)
		return
	}
	actor, err := o.store.ResolveIdentity(r.Context(), identity)
	if err != nil {
		authStoreFailure(w, r, err)
		return
	}
	token, err := randomToken()
	if err != nil {
		authFailure(w, r)
		return
	}
	expires := o.now().Add(8 * time.Hour)
	if verified.Expiry.Before(expires) {
		expires = verified.Expiry
	}
	if !tokens.Expiry.IsZero() && tokens.Expiry.Before(expires) {
		expires = tokens.Expiry
	}
	if !expires.After(o.now()) {
		authFailure(w, r)
		return
	}
	session := Session{TokenHash: tokenHash(token), CSRFHash: tokenHash(o.box.csrf(token)), Actor: actor, CreatedAt: o.now(), ExpiresAt: expires}
	if old, err := r.Cookie(sessionCookie); err == nil {
		if err := o.store.RevokeSession(r.Context(), tokenHash(old.Value)); err != nil {
			authStoreFailure(w, r, err)
			return
		}
	}
	if err := o.store.CreateSession(r.Context(), session); err != nil {
		authStoreFailure(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expires})
	http.Redirect(w, r, flow.ReturnTo, http.StatusSeeOther)
}
func (o *OIDC) logout(w http.ResponseWriter, r *http.Request) {
	if _, err := o.Authenticate(r); err != nil {
		authStoreFailure(w, r, err)
		return
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := o.store.RevokeSession(r.Context(), tokenHash(cookie.Value)); err != nil {
			authStoreFailure(w, r, err)
			return
		}
	}
	clearCookie(w, sessionCookie)
	w.WriteHeader(http.StatusNoContent)
}
func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
func authFailure(w http.ResponseWriter, r *http.Request) {
	if r.Context().Err() != nil {
		authUnavailable(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(ErrUnauthenticated)
}

// Invalid credentials/state remain an authentication failure. A missing
// database response or unavailable identity provider does not invalidate them.
func authStoreFailure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrUnauthenticated) || errors.Is(err, ErrIdentityConflict) {
		authFailure(w, r)
		return
	}
	authUnavailable(w)
}

func verifierUnavailable(ctx context.Context, err error) bool {
	var network net.Error
	return ctx.Err() != nil || errors.Is(err, errKeyAuthorityUnavailable) || errors.As(err, &network)
}

func authUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(monitoring.ErrSource)
}
