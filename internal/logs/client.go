package logs

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/observability"
)

type ClientConfig struct {
	Observer     *observability.Registry
	DeploymentID string
	Origin       string
	Roots        *x509.CertPool
	Certificate  tls.Certificate
	Signer       *auth.DelegationSigner
	ActorMode    auth.DelegationMode
}
type Client struct {
	config   ClientConfig
	endpoint string
	client   *http.Client
}

func NewClient(c ClientConfig) (*Client, error) {
	mode, modeErr := c.ActorMode.Canonical()
	if modeErr != nil {
		return nil, modeErr
	}
	c.ActorMode = mode
	u, err := url.Parse(c.Origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || !uuid(c.DeploymentID) || c.Roots == nil || len(c.Certificate.Certificate) == 0 || c.Signer == nil {
		return nil, errors.New("incomplete pinned log broker trust")
	}
	u.Path = "/v1/chunks/read"
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: c.Roots.Clone(), Certificates: []tls.Certificate{c.Certificate}}, ForceAttemptHTTP2: true, MaxIdleConns: 8, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 8, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 6 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	return &Client{config: c, endpoint: u.String(), client: &http.Client{Transport: transport, Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Close() { c.client.CloseIdleConnections() }

func (c *Client) ReadChunk(ctx context.Context, a monitoring.Actor, m Manifest, chunk Chunk) (result FileResult, resultErr error) {
	start := time.Now()
	defer func() {
		c.config.Observer.ObserveBytes("logs", "chunk", c.config.DeploymentID, string(c.config.ActorMode), logObservation(result.State, resultErr), time.Since(start), int64(len(result.Bytes)))
	}()
	if m.Scope.DeploymentID != c.config.DeploymentID {
		return FileResult{}, monitoring.ErrForbidden
	}
	request := ChunkRequest{Scope: m.Scope, JobID: m.JobID, RunNumber: m.RunNumber, ExecutionID: m.ExecutionID, Stream: m.Stream, Sequence: strconv.FormatInt(chunk.Sequence, 10)}
	data, _ := json.Marshal(request)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return FileResult{}, monitoring.ErrSource
	}
	header, err := c.config.Signer.Authorize(a, "logs.read", m.Scope.NamespaceID, string(c.config.ActorMode))
	if err != nil {
		return FileResult{}, monitoring.ErrAuthority
	}
	r.Header.Set("Authorization", header)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	response, err := c.client.Do(r)
	if err != nil {
		return FileResult{}, monitoring.ErrSource
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return FileResult{}, monitoring.ErrAuthority
	case http.StatusNotFound:
		return FileResult{}, monitoring.ErrNotFound
	case http.StatusConflict:
		return FileResult{}, monitoring.ErrCursor
	default:
		return FileResult{}, monitoring.ErrSource
	}
	if response.ContentLength > maxHelperOutput {
		return FileResult{}, monitoring.ErrSource
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxHelperOutput+1))
	if err != nil || len(raw) > maxHelperOutput {
		return FileResult{}, monitoring.ErrSource
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Bytes) > MaxChunkBytes {
		return FileResult{}, monitoring.ErrSource
	}
	switch result.State {
	case "ok":
		if int64(len(result.Bytes)) != chunk.ByteLength {
			return FileResult{}, monitoring.ErrSource
		}
	case "chunk_missing", "chunk_corrupt", "mapping_inaccessible", "invalid_manifest", "reader_busy", "read_timeout", "reader_unavailable":
		result.Bytes = nil
	default:
		return FileResult{}, monitoring.ErrSource
	}
	return result, nil
}

type RemoteMapping struct {
	DeploymentID, TargetGenerationID, StoreName, StoreVersion string
	Client                                                    *Client
}
type RemoteChunks struct{ mappings map[mappingKey]*Client }

func NewRemoteChunks(mappings []RemoteMapping) (*RemoteChunks, error) {
	if len(mappings) < 1 || len(mappings) > 1024 {
		return nil, errors.New("configure 1–1024 remote log mappings")
	}
	r := &RemoteChunks{mappings: make(map[mappingKey]*Client)}
	for _, m := range mappings {
		key := mappingKey{m.DeploymentID, m.TargetGenerationID, m.StoreName, m.StoreVersion}
		if m.Client == nil || m.DeploymentID != m.Client.config.DeploymentID || (!uuid(m.TargetGenerationID) && m.TargetGenerationID != "") || !name(m.StoreName) || !positive(m.StoreVersion) || r.mappings[key] != nil {
			return nil, errors.New("invalid or duplicate remote log mapping")
		}
		r.mappings[key] = m.Client
	}
	return r, nil
}
func (r *RemoteChunks) ReadChunk(ctx context.Context, a monitoring.Actor, m Manifest, c Chunk) (FileResult, error) {
	client := r.mappings[mappingKey{m.Scope.DeploymentID, m.TargetGenerationID, c.StoreName, c.StoreVersion}]
	if client == nil {
		client = r.mappings[mappingKey{m.Scope.DeploymentID, "", c.StoreName, c.StoreVersion}]
	}
	if client == nil {
		return FileResult{State: "mapping_inaccessible"}, nil
	}
	return client.ReadChunk(ctx, a, m, c)
}
