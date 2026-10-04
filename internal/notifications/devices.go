package notifications

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ryancswallace/jobman-dashboard/internal/push"
)

const (
	MaximumBoundDevices                 = 50
	MaximumCreatedInstallations         = 100
	MaximumInstallations                = 10000
	MaximumInstallationBindings         = 100
	MaximumInstallationCreatesPerMinute = 5
	MaximumBindingChangesPerMinute      = 10
	MaximumDeviceMutationsPerMinute     = 30
)

var (
	ErrDeviceInvalid     = errors.New("invalid notification device request")
	ErrDeviceNotFound    = errors.New("notification installation not found or inaccessible")
	ErrDeviceConflict    = errors.New("notification device binding or revision changed")
	ErrDeviceCapacity    = errors.New("notification installation capacity reached")
	ErrDeviceRateLimited = errors.New("notification device mutation rate limited")
	ErrDeviceUnavailable = errors.New("notification device secret unavailable")
)

type DeviceTopic struct{ Topic, Environment string }
type DevicePolicy struct{ allowed map[DeviceTopic]bool }

var deviceTopicPattern = regexp.MustCompile(`^[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+$`)

func NewDevicePolicy(topics []DeviceTopic) (*DevicePolicy, error) {
	if len(topics) < 1 || len(topics) > 16 {
		return nil, ErrDeviceInvalid
	}
	p := &DevicePolicy{allowed: map[DeviceTopic]bool{}}
	for _, topic := range topics {
		if len(topic.Topic) > 255 || !deviceTopicPattern.MatchString(topic.Topic) || topic.Environment != "sandbox" && topic.Environment != "production" || p.allowed[topic] {
			return nil, ErrDeviceInvalid
		}
		p.allowed[topic] = true
	}
	return p, nil
}
func (p *DevicePolicy) Allows(topic, environment string) bool {
	return p != nil && p.allowed[DeviceTopic{topic, environment}]
}

// Requests are private inputs. Proofs and tokens are never part of a public
// response and must not be logged or stored as plaintext.
type DeviceRegistration struct {
	InstallationID            string
	InstallationSecret        string `json:"-"`
	Label, Topic, Environment string
	Token                     string `json:"-"`
	Permission                string
	Enabled, Muted            bool
}
type DeviceRefresh struct {
	InstallationSecret string `json:"-"`
	Token              string `json:"-"`
	Permission         string
}
type DeviceSettings struct {
	Label          string
	Enabled, Muted bool
}

func DevicePermission(permission string) bool {
	switch permission {
	case "not_determined", "denied", "authorized", "provisional", "ephemeral":
		return true
	}
	return false
}
func DevicePermissionAllowsDelivery(permission string) bool {
	return permission == "authorized" || permission == "provisional" || permission == "ephemeral"
}
func deviceLabel(label string) bool {
	return utf8.ValidString(label) && label == strings.TrimSpace(label) && len(label) > 0 && len(label) <= 120 && !strings.ContainsFunc(label, unicode.IsControl)
}
func (s DeviceSettings) Validate() error {
	if !deviceLabel(s.Label) {
		return ErrDeviceInvalid
	}
	return nil
}
func (r DeviceRegistration) Validate(policy *DevicePolicy) error {
	if !uuid(r.InstallationID) || !deviceLabel(r.Label) || !policy.Allows(r.Topic, r.Environment) || !push.ValidDeviceToken(r.Token) || !DevicePermission(r.Permission) {
		return ErrDeviceInvalid
	}
	_, err := InstallationSecretHash(r.InstallationID, r.InstallationSecret)
	return err
}
func (r DeviceRefresh) Validate(id string) error {
	if !push.ValidDeviceToken(r.Token) || !DevicePermission(r.Permission) {
		return ErrDeviceInvalid
	}
	_, err := InstallationSecretHash(id, r.InstallationSecret)
	return err
}

// The client creates 32 random bytes and retains the canonical base64url secret
// in Keychain. The hash is bound to its installation UUID and is not reusable
// for another installation. Server endpoints never return the secret.
func InstallationSecretHash(id, secret string) ([]byte, error) {
	if len(secret) != 43 {
		return nil, ErrDeviceInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	if !uuid(id) || err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != secret {
		return nil, ErrDeviceInvalid
	}
	h := sha256.New()
	h.Write([]byte("jobman-dashboard/installation-proof/v1\x00"))
	h.Write([]byte(id))
	h.Write([]byte{0})
	h.Write(decoded)
	return h.Sum(nil), nil
}

// DeviceView is safe for the authenticated owner. Binding IDs, token versions,
// token ciphertext/hashes and installation proofs are deliberately absent.
type DeviceView struct {
	InstallationID string    `json:"installationId"`
	Revision       int64     `json:"revision,string"`
	Label          string    `json:"label"`
	Topic          string    `json:"topic"`
	Environment    string    `json:"environment"`
	State          string    `json:"state"`
	Enabled        bool      `json:"enabled"`
	Muted          bool      `json:"muted"`
	Permission     string    `json:"permission"`
	TokenStatus    string    `json:"tokenStatus"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	LastSeenAt     time.Time `json:"lastSeenAt"`
}
type InstallationState struct {
	InstallationID        string `json:"installationId"`
	Revision              int64  `json:"revision,string"`
	HasBinding            bool   `json:"hasBinding"`
	BoundToCurrentAccount bool   `json:"boundToCurrentAccount"`
}

// DeviceCandidate contains fences for durable delivery attempts, never a token.
type DeviceCandidate struct {
	InstallationID, BindingID, AccountID string
	TokenVersion                         int64
}
type DeviceHandoff struct {
	DeviceCandidate
	Topic, Environment string
	Token              string `json:"-"`
	RegisteredAt       time.Time
}

type DeviceTokenBinding struct {
	InstallationID, BindingID, AccountID, Topic, Environment string
	TokenVersion                                             int64
}

func (b DeviceTokenBinding) aad(keyID string) ([]byte, error) {
	if !uuid(b.InstallationID) || !uuid(b.BindingID) || !uuid(b.AccountID) || b.TokenVersion <= 0 || len(b.Topic) > 255 || !deviceTopicPattern.MatchString(b.Topic) || b.Environment != "sandbox" && b.Environment != "production" {
		return nil, ErrDeviceInvalid
	}
	return json.Marshal([]any{"jobman-dashboard/apns-token/v1", keyID, b.InstallationID, b.BindingID, b.AccountID, b.Topic, b.Environment, b.TokenVersion})
}

type EncryptedDeviceToken struct {
	KeyID      string
	Ciphertext []byte
}
type DeviceCipher struct {
	current string
	keys    map[string]cipher.AEAD
}

// Retain previous read keys during rotation. The active key follows the existing
// 32-byte secret-file/key-ID convention; purpose derivation prevents reuse of an
// authentication ciphertext as a device token, even when the root key is shared.
func NewDeviceCipher(current string, keys map[string][]byte) (*DeviceCipher, error) {
	if len(keys) < 1 || len(keys) > 8 {
		return nil, ErrDeviceInvalid
	}
	c := &DeviceCipher{current: current, keys: map[string]cipher.AEAD{}}
	for id, key := range keys {
		if id == "" || len(id) > 64 || !utf8.ValidString(id) || strings.ContainsFunc(id, unicode.IsControl) || len(key) != 32 {
			return nil, ErrDeviceInvalid
		}
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte("jobman-dashboard/apns-encryption-key/v1"))
		block, err := aes.NewCipher(mac.Sum(nil))
		if err != nil {
			return nil, ErrDeviceInvalid
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, ErrDeviceInvalid
		}
		c.keys[id] = aead
	}
	if c.keys[current] == nil {
		return nil, ErrDeviceInvalid
	}
	return c, nil
}
func (c *DeviceCipher) Seal(binding DeviceTokenBinding, token string) (EncryptedDeviceToken, error) {
	if c == nil || c.keys[c.current] == nil || !push.ValidDeviceToken(token) {
		return EncryptedDeviceToken{}, ErrDeviceInvalid
	}
	aad, err := binding.aad(c.current)
	if err != nil {
		return EncryptedDeviceToken{}, err
	}
	aead := c.keys[c.current]
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return EncryptedDeviceToken{}, err
	}
	return EncryptedDeviceToken{KeyID: c.current, Ciphertext: aead.Seal(nonce, nonce, []byte(token), aad)}, nil
}
func (c *DeviceCipher) Open(binding DeviceTokenBinding, encrypted EncryptedDeviceToken) (string, error) {
	if c == nil {
		return "", ErrDeviceUnavailable
	}
	aead := c.keys[encrypted.KeyID]
	if aead == nil || len(encrypted.Ciphertext) < aead.NonceSize()+aead.Overhead() || len(encrypted.Ciphertext) > 1100 {
		return "", ErrDeviceUnavailable
	}
	aad, err := binding.aad(encrypted.KeyID)
	if err != nil {
		return "", ErrDeviceUnavailable
	}
	n := aead.NonceSize()
	raw, err := aead.Open(nil, encrypted.Ciphertext[:n], encrypted.Ciphertext[n:], aad)
	if err != nil || !push.ValidDeviceToken(string(raw)) {
		return "", ErrDeviceUnavailable
	}
	return string(raw), nil
}

// This stable digest prevents duplicate active bindings for a topic/environment
// token across encryption-key rotation. High-entropy APNs tokens remain encrypted;
// the digest is private lookup metadata, never an API field or a credential.
func DeviceTokenDigest(topic, environment, token string) []byte {
	raw, _ := json.Marshal([]string{"jobman-dashboard/apns-token-index/v1", topic, environment, token})
	sum := sha256.Sum256(raw)
	return sum[:]
}
