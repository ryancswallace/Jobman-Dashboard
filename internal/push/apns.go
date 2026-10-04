// Package push performs bounded APNs handoff. Callers own durable attempts and
// must recheck current account, rule, source and device authority before Send.
package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

var ErrConfiguration = errors.New("APNs configuration is invalid")
var ErrRequest = errors.New("APNs handoff request is invalid or expired")
var identifier = regexp.MustCompile(`^[A-Z0-9]{10}$`)
var topicPattern = regexp.MustCompile(`^[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const nilUUID = "00000000-0000-0000-0000-000000000000"

type APNsConfig struct {
	TeamID, KeyID, Topic, Environment string
	PrivateKeyPEM                     []byte
}
type APNs struct {
	topic, environment, team, endpoint string
	signer                             jose.Signer
	client                             *http.Client
	now                                func() time.Time
	slots                              chan struct{}
	mu                                 sync.Mutex
	token                              string
	issued                             time.Time
}
type Request struct {
	// DeliveryID is stable across retries; InboxID is the only application data
	// sent through APNs. Token must come from a currently authorized binding.
	DeliveryID, InboxID, Token, Topic, Environment string
	ExpiresAt                                      time.Time
}
type Result struct {
	Outcome        string // accepted, retry, token_invalid, rejected, provider_error
	ProviderID     string
	Reason         string // bounded known code, never an upstream response body
	RetryAfter     time.Duration
	TokenInvalidAt *time.Time
	Ambiguous      bool
}

func NewAPNs(c APNsConfig) (*APNs, error) {
	if !identifier.MatchString(c.TeamID) || !identifier.MatchString(c.KeyID) || len(c.Topic) > 255 || !topicPattern.MatchString(c.Topic) || c.Environment != "sandbox" && c.Environment != "production" || len(c.PrivateKeyPEM) > 16384 {
		return nil, ErrConfiguration
	}
	block, rest := pem.Decode(c.PrivateKeyPEM)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrConfiguration
	}
	value, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, ErrConfiguration
	}
	key, ok := value.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, ErrConfiguration
	}
	options := (&jose.SignerOptions{}).WithHeader("kid", c.KeyID)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, options)
	if err != nil {
		return nil, ErrConfiguration
	}
	endpoint := "https://api.push.apple.com"
	if c.Environment == "sandbox" {
		endpoint = "https://api.sandbox.push.apple.com"
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 2,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second, MaxResponseHeaderBytes: 8192}
	return &APNs{topic: c.Topic, environment: c.Environment, team: c.TeamID, endpoint: endpoint, signer: signer, client: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: time.Now, slots: make(chan struct{}, 4)}, nil
}
func (p *APNs) Close() { p.client.CloseIdleConnections() }
func (p *APNs) authorization(now time.Time) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != "" && now.Before(p.issued) {
		return "", ErrConfiguration
	}
	if p.token == "" || now.Sub(p.issued) >= 45*time.Minute {
		token, err := jwt.Signed(p.signer).Claims(struct {
			Issuer   string `json:"iss"`
			IssuedAt int64  `json:"iat"`
		}{p.team, now.Unix()}).Serialize()
		if err != nil {
			return "", ErrConfiguration
		}
		p.token, p.issued = token, now
	}
	return p.token, nil
}
func ValidDeviceToken(token string) bool {
	// Apple device tokens are variable length. Bound storage without assuming a
	// permanent fixed32-byte length, and use one canonical lowercase encoding.
	if len(token) < 2 || len(token) > 1024 || len(token)%2 != 0 || strings.ToLower(token) != token {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

type alertText struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}
type alertPayload struct {
	Alert alertText `json:"alert"`
	Sound string    `json:"sound"`
}
type genericPayload struct {
	APS           alertPayload `json:"aps"`
	InboxID       string       `json:"inboxId"`
	SchemaVersion int          `json:"schemaVersion"`
}

func (p *APNs) Send(ctx context.Context, r Request) (Result, error) {
	now := p.now().UTC()
	if !uuidPattern.MatchString(r.DeliveryID) || r.DeliveryID == nilUUID || !uuidPattern.MatchString(r.InboxID) || r.InboxID == nilUUID || !ValidDeviceToken(r.Token) || r.Topic != p.topic || r.Environment != p.environment || !r.ExpiresAt.After(now) || r.ExpiresAt.After(now.Add(24*time.Hour)) {
		return Result{}, ErrRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	case <-ctx.Done():
		return Result{Outcome: "retry", Reason: "transport_unavailable", RetryAfter: time.Minute}, nil
	}
	now = p.now().UTC()
	if ctx.Err() != nil || !r.ExpiresAt.After(now) {
		return Result{}, ErrRequest
	}
	token, err := p.authorization(now)
	if err != nil {
		return Result{}, err
	}
	payload, _ := json.Marshal(genericPayload{APS: alertPayload{Alert: alertText{Title: "Jobman Dashboard", Body: "A monitored job has an update."}, Sound: "default"}, InboxID: r.InboxID, SchemaVersion: 1})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/3/device/"+r.Token, bytes.NewReader(payload))
	if err != nil {
		return Result{}, ErrRequest
	}
	expiration := min(r.ExpiresAt.Unix(), now.Add(5*time.Minute).Unix())
	request.Header.Set("Authorization", "bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("apns-topic", p.topic)
	request.Header.Set("apns-push-type", "alert")
	request.Header.Set("apns-priority", "10")
	request.Header.Set("apns-expiration", strconv.FormatInt(expiration, 10))
	request.Header.Set("apns-id", r.DeliveryID)
	request.Header.Set("apns-collapse-id", r.InboxID)
	response, err := p.client.Do(request)
	if err != nil {
		return Result{Outcome: "retry", Reason: "transport_unavailable", RetryAfter: time.Minute, Ambiguous: true}, nil
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	result := Result{ProviderID: response.Header.Get("apns-id")}
	if err != nil || len(body) > 4096 || response.ProtoMajor != 2 || result.ProviderID != r.DeliveryID {
		return Result{Outcome: "retry", Reason: "invalid_provider_response", RetryAfter: 15 * time.Minute, Ambiguous: true}, nil
	}
	if response.StatusCode == http.StatusOK {
		if len(body) != 0 {
			return Result{Outcome: "retry", Reason: "invalid_provider_response", RetryAfter: 15 * time.Minute, Ambiguous: true}, nil
		}
		result.Outcome = "accepted"
		return result, nil
	}
	var failure struct {
		Reason    string      `json:"reason"`
		Timestamp json.Number `json:"timestamp"`
	}
	if json.Unmarshal(body, &failure) != nil || len(failure.Reason) > 128 {
		return Result{Outcome: "retry", Reason: "invalid_provider_response", RetryAfter: 15 * time.Minute, Ambiguous: true}, nil
	}
	result.Reason = "provider_response"
	switch failure.Reason {
	case "BadDeviceToken", "DeviceTokenNotForTopic", "ExpiredToken", "Unregistered":
		result.Reason = failure.Reason
		if response.StatusCode != 400 && response.StatusCode != 410 {
			result.Outcome = "provider_error"
			break
		}
		result.Outcome = "token_invalid"
		if response.StatusCode == 410 {
			millis, err := strconv.ParseInt(string(failure.Timestamp), 10, 64)
			if err != nil || millis <= 0 || millis > now.Add(5*time.Minute).UnixMilli() {
				result.Outcome = "provider_error"
				break
			}
			invalid := time.UnixMilli(millis).UTC()
			result.TokenInvalidAt = &invalid
		}
	case "IdleTimeout":
		result.Reason = failure.Reason
		if response.StatusCode == 400 {
			result.Outcome = "retry"
			result.RetryAfter = time.Minute
			_ = response.Body.Close()
			p.client.CloseIdleConnections()
		} else {
			result.Outcome = "provider_error"
		}
	case "TooManyRequests":
		result.Outcome = "retry"
		result.Reason = failure.Reason
		result.RetryAfter = time.Minute
	case "TooManyProviderTokenUpdates":
		result.Outcome = "provider_error"
		result.Reason = failure.Reason
		result.RetryAfter = 20 * time.Minute
	case "ExpiredProviderToken", "InvalidProviderToken", "MissingProviderToken", "BadTopic", "TopicDisallowed", "Forbidden", "BadCertificate", "BadCertificateEnvironment":
		result.Outcome = "provider_error"
		result.Reason = failure.Reason
	case "PayloadTooLarge", "BadCollapseId", "BadExpirationDate", "BadMessageId", "BadPriority", "BadPath", "InvalidPushType", "MissingDeviceToken", "MissingTopic", "DuplicateHeaders", "MethodNotAllowed":
		result.Outcome = "rejected"
		result.Reason = failure.Reason
	default:
		if response.StatusCode >= 500 && response.StatusCode <= 599 {
			result.Outcome = "retry"
			result.RetryAfter = 15 * time.Minute
		} else if response.StatusCode == 429 {
			result.Outcome = "retry"
			result.RetryAfter = time.Minute
		} else {
			result.Outcome = "provider_error"
		}
	}
	if result.Outcome == "retry" {
		if seconds, err := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 32); err == nil && seconds > 0 {
			result.RetryAfter = max(result.RetryAfter, min(time.Duration(seconds)*time.Second, time.Hour))
		}
	}
	return result, nil
}
