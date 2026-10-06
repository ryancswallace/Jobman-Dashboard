// Package control adapts the versioned Control API. A client is pinned to one
// configured source, certificate and delegation audience for its lifetime.
package control

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/observability"
)

const contract = "jobman.control/v1alpha1"
const maxBody = 4 << 20

type Config struct {
	Observer       *observability.Registry
	DeploymentID   string
	Name           string
	Endpoint       string
	InstanceID     string
	NamespaceIDs   []string
	Roots          *x509.CertPool
	Certificate    tls.Certificate
	Signer         *auth.DelegationSigner
	ActorMode      auth.DelegationMode
	VerifyIdentity func(context.Context, string, string) error
}

type Client struct {
	config Config
	base   *url.URL
	client *http.Client
	now    func() time.Time
}

func New(config Config) (*Client, error) {
	mode, modeErr := config.ActorMode.Canonical()
	if modeErr != nil {
		return nil, modeErr
	}
	config.ActorMode = mode
	u, err := url.Parse(config.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return nil, errors.New("Control endpoint must be an HTTPS origin without credentials, query, or path")
	}
	if !uuid(config.DeploymentID) || !uuid(config.InstanceID) || strings.TrimSpace(config.Name) == "" || len(config.Name) > 120 || config.Roots == nil || len(config.Certificate.Certificate) == 0 || config.Signer == nil || len(config.NamespaceIDs) == 0 || len(config.NamespaceIDs) > 320 {
		return nil, errors.New("incomplete pinned Control trust configuration")
	}
	config.NamespaceIDs = slices.Clone(config.NamespaceIDs)
	for _, id := range config.NamespaceIDs {
		if !uuid(id) {
			return nil, errors.New("Control namespace allowlist requires UUIDs")
		}
	}
	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: config.Roots.Clone(), Certificates: []tls.Certificate{config.Certificate}},
		ForceAttemptHTTP2: true, MaxIdleConns: 16, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 8,
		IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
	}
	u.Path = ""
	return &Client{config: config, base: u, client: &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: time.Now}, nil
}

func (c *Client) ID() string { return c.config.DeploymentID }
func (c *Client) Close()     { c.client.CloseIdleConnections() }

// get never accepts an absolute caller-controlled URL, method, or header map.
// Source error bodies may contain private diagnostics and are not propagated.
func (c *Client) get(ctx context.Context, actor monitoring.Actor, operation, namespace, path string, query url.Values, dest any) (resultErr error) {
	start := time.Now()
	defer func() {
		c.config.Observer.Observe("source", observationOperation(operation), c.ID(), string(c.config.ActorMode), observationOutcome(resultErr), time.Since(start))
	}()
	if !strings.HasPrefix(path, "/v1/") || strings.ContainsAny(path, "?#\\") {
		return monitoring.ErrSource
	}
	u := *c.base
	u.Path = path
	u.RawQuery = query.Encode()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return monitoring.ErrSource
	}
	r.Header.Set("Accept", "application/json")
	if operation != "" {
		authorization, err := c.config.Signer.Authorize(actor, operation, namespace, string(c.config.ActorMode))
		if err != nil {
			return monitoring.ErrAuthority
		}
		r.Header.Set("Authorization", authorization)
	}
	response, err := c.client.Do(r)
	if err != nil {
		return monitoring.ErrSource
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusNotFound:
		return monitoring.ErrNotFound
	case http.StatusUnauthorized:
		return monitoring.ErrAuthority
	case http.StatusServiceUnavailable:
		return monitoring.ErrAuthority
	case http.StatusConflict:
		if operation == "jobs.read" && strings.HasSuffix(path, "/runs") {
			return monitoring.ErrCursor
		}
		if operation == "targets.read" {
			return monitoring.ErrTargetChanged
		}
		if operation == "evidence.read" {
			return &api.Error{Code: "snapshot_changed", Message: "The job information changed while the report was being prepared. Request a new report."}
		}
		return monitoring.ErrSource
	default:
		return monitoring.ErrSource
	}
	if response.ContentLength > maxBody {
		return monitoring.ErrSource
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return monitoring.ErrSource
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return monitoring.ErrSource
	}
	return nil
}

func uuid(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
func decimal(s string) bool {
	if s == "" || len(s) > 19 || (len(s) > 1 && s[0] == '0') {
		return false
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil && s[0] != '-' && s[0] != '+'
}
func selector(s string) bool {
	if len(s) == 0 || len(s) > 253 || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.", r) {
			return false
		}
	}
	return true
}

type principalPage struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Principal  struct {
		ID          string `json:"id"`
		DirectoryID string `json:"directoryId"`
		Issuer      string `json:"issuer"`
		Subject     string `json:"subject"`
	} `json:"principal"`
	Namespaces    []namespaceAccess `json:"namespaces"`
	NextPageToken string            `json:"nextPageToken"`
}
type namespaceAccess struct {
	api.Namespace
	LastDirectoryVerifiedAt time.Time `json:"lastDirectoryVerifiedAt"`
	AuthorizationStatus     string    `json:"authorizationStatus"`
}
type discovery struct {
	monitoring.Discovery
	principalID string
	features    []string
}

func (c *Client) Discover(ctx context.Context, actor monitoring.Actor) (monitoring.Discovery, error) {
	d, err := c.discover(ctx, actor)
	// A missing/forbidden discovery endpoint is not an authoritative namespace
	// removal. Only a successfully verified /me response describes current grants.
	// Preserve that distinction for rule revocation and recovery callers.
	if errors.Is(err, monitoring.ErrNotFound) || errors.Is(err, monitoring.ErrForbidden) {
		return monitoring.Discovery{}, monitoring.ErrAuthority
	}
	return d.Discovery, err
}
func (c *Client) discover(ctx context.Context, actor monitoring.Actor) (discovery, error) {
	var caps struct {
		APIVersion   string `json:"apiVersion"`
		Kind         string `json:"kind"`
		Capabilities struct {
			InstanceID       string    `json:"instanceId"`
			RecoveryEpoch    string    `json:"recoveryEpoch"`
			ServiceTime      time.Time `json:"serviceTime"`
			ContractVersions []string  `json:"contractVersions"`
			Features         []string  `json:"features"`
			MaximumPageSize  int       `json:"maximumPageSize"`
		} `json:"capabilities"`
	}
	if err := c.get(ctx, actor, "", "", "/v1/capabilities", nil, &caps); err != nil {
		return discovery{}, err
	}
	v := caps.Capabilities
	if v.InstanceID != c.config.InstanceID {
		c.config.Observer.Mismatch("source_identity")
	}
	if caps.APIVersion != contract || !slices.Contains(v.ContractVersions, contract) {
		c.config.Observer.Mismatch("source_contract")
	}
	if caps.APIVersion != contract || caps.Kind != "ControlCapabilities" || v.InstanceID != c.config.InstanceID || !decimal(v.RecoveryEpoch) || v.ServiceTime.IsZero() || !slices.Contains(v.ContractVersions, contract) || v.MaximumPageSize < 200 {
		return discovery{}, monitoring.ErrSource
	}
	if c.config.VerifyIdentity != nil {
		if err := c.config.VerifyIdentity(ctx, v.InstanceID, v.RecoveryEpoch); err != nil {
			return discovery{}, monitoring.ErrSource
		}
	}
	for _, feature := range []string{"namespace-discovery", "job-monitoring", "namespace-summary", "directory-authorization", "read-delegation"} {
		if !slices.Contains(v.Features, feature) {
			return discovery{}, monitoring.ErrAuthority
		}
	}
	d := discovery{features: slices.Clone(v.Features), Discovery: monitoring.Discovery{InstanceID: v.InstanceID, RecoveryEpoch: v.RecoveryEpoch, ServiceTime: v.ServiceTime, Deployment: api.Deployment{ID: c.ID(), Name: c.config.Name, Status: "available", Namespaces: []api.Namespace{}}}}
	cursor := ""
	var oldestProof, earliestExpiry time.Time
	seen := map[string]bool{}
	for page := 0; page < 3; page++ {
		q := url.Values{"limit": {"200"}}
		if cursor != "" {
			q.Set("pageToken", cursor)
		}
		var p principalPage
		if err := c.get(ctx, actor, "namespace.read", "", "/v1/me", q, &p); err != nil {
			return discovery{}, err
		}
		if p.APIVersion != contract || p.Kind != "CurrentPrincipal" || len(p.Namespaces) > 200 || (p.Principal.ID != "" && !uuid(p.Principal.ID)) || (page > 0 && p.Principal.ID != d.principalID) {
			return discovery{}, monitoring.ErrSource
		}
		// Canonical Control aliases may differ from the token issuer/subject. The
		// verified directory GUID binds that canonical principal to this actor.
		if !uuid(p.Principal.DirectoryID) || !strings.EqualFold(p.Principal.DirectoryID, actor.DirectoryID) || (len(p.Namespaces) > 0 && p.Principal.ID == "") {
			return discovery{}, monitoring.ErrAuthority
		}
		d.principalID = p.Principal.ID
		d.PrincipalID = p.Principal.ID
		for _, ns := range p.Namespaces {
			if !uuid(ns.ID) || !selector(ns.Name) || seen[ns.ID] || !slices.Contains(c.config.NamespaceIDs, ns.ID) {
				return discovery{}, monitoring.ErrSource
			}
			seen[ns.ID] = true
			if len(seen) > 320 {
				return discovery{}, monitoring.ErrSource
			}
			now := c.now().UTC()
			if ns.AuthorizationStatus != "verified" || !decimal(ns.AuthorizationVersion) || ns.AuthorizationVersion == "0" || ns.LastDirectoryVerifiedAt.IsZero() || ns.LastDirectoryVerifiedAt.After(now.Add(5*time.Second)) || ns.AuthorizationCheckedAt.IsZero() || ns.AuthorizationCheckedAt.After(now.Add(5*time.Second)) || ns.AuthorizationCheckedAt.Before(ns.LastDirectoryVerifiedAt) || !now.Before(ns.AuthorizationExpiresAt) || ns.AuthorizationExpiresAt.After(ns.LastDirectoryVerifiedAt.Add(120*time.Second)) || ns.AuthorizationExpiresAt.Before(ns.AuthorizationCheckedAt) {
				return discovery{}, monitoring.ErrAuthority
			}
			if !slices.Contains(ns.Capabilities, "namespace.read") {
				return discovery{}, monitoring.ErrAuthority
			}
			if oldestProof.IsZero() || ns.LastDirectoryVerifiedAt.Before(oldestProof) {
				oldestProof = ns.LastDirectoryVerifiedAt
			}
			if earliestExpiry.IsZero() || ns.AuthorizationExpiresAt.Before(earliestExpiry) {
				earliestExpiry = ns.AuthorizationExpiresAt
			}
			d.Deployment.Namespaces = append(d.Deployment.Namespaces, ns.Namespace)
		}
		if p.NextPageToken == "" {
			c.config.Observer.ObserveAuthority(c.ID(), oldestProof, earliestExpiry)
			return d, nil
		}
		if p.NextPageToken == cursor || len(p.NextPageToken) > 8192 {
			return discovery{}, monitoring.ErrSource
		}
		cursor = p.NextPageToken
	}
	return discovery{}, monitoring.ErrSource
}

func (c *Client) authorize(ctx context.Context, actor monitoring.Actor, scope api.Scope, capability string) (discovery, api.Namespace, error) {
	if scope.DeploymentID != c.ID() || !uuid(scope.NamespaceID) {
		return discovery{}, api.Namespace{}, monitoring.ErrForbidden
	}
	d, err := c.discover(ctx, actor)
	if err != nil {
		return d, api.Namespace{}, err
	}
	for _, ns := range d.Deployment.Namespaces {
		if ns.ID == scope.NamespaceID && slices.Contains(ns.Capabilities, capability) {
			return d, ns, nil
		}
	}
	return d, api.Namespace{}, monitoring.ErrForbidden
}
func (c *Client) recheck(ctx context.Context, actor monitoring.Actor, before discovery, ns api.Namespace, capability string) error {
	after, current, err := c.authorize(ctx, actor, api.Scope{DeploymentID: c.ID(), NamespaceID: ns.ID}, capability)
	if err != nil {
		return err
	}
	if before.InstanceID != after.InstanceID || before.RecoveryEpoch != after.RecoveryEpoch || before.principalID != after.principalID || ns.AuthorizationVersion != current.AuthorizationVersion || ns.Name != current.Name {
		return monitoring.ErrAuthority
	}
	return nil
}
