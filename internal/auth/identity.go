package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

var ErrUnauthenticated = &api.Error{Code: "unauthenticated", Message: "Sign in to continue."}
var ErrIdentityConflict = errors.New("verified identity conflicts with an existing account")

// Identity is accepted only after verifying an IdP-signed token against the
// configured issuer, audience, client and immutable directory-claim policy.
type Identity struct{ Issuer, Subject, DirectoryID, DisplayName string }
type Session struct {
	TokenHash []byte
	CSRFHash  []byte
	Actor     monitoring.Actor
	CreatedAt time.Time
	ExpiresAt time.Time
}

type IdentityStore interface {
	ResolveIdentity(context.Context, Identity) (monitoring.Actor, error)
	PutLogin(context.Context, []byte, []byte, time.Time) error
	ConsumeLogin(context.Context, []byte) ([]byte, error)
	CreateSession(context.Context, Session) error
	Session(context.Context, []byte) (Session, error)
	RevokeSession(context.Context, []byte) error
}

type secretBox struct {
	aead    cipher.AEAD
	keyID   string
	csrfKey []byte
}

func newSecretBox(key []byte, keyID string) (*secretBox, error) {
	if len(key) != 32 || keyID == "" || len(keyID) > 64 {
		return nil, errors.New("authentication encryption requires a 32-byte key and key ID")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("jobman-dashboard/csrf/v1"))
	return &secretBox{aead: aead, keyID: keyID, csrfKey: mac.Sum(nil)}, nil
}
func (b *secretBox) seal(data []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, data, []byte("login:"+b.keyID)), nil
}
func (b *secretBox) open(data []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("invalid encrypted login")
	}
	return b.aead.Open(nil, data[:n], data[n:], []byte("login:"+b.keyID))
}
func (b *secretBox) csrf(token string) string {
	mac := hmac.New(sha256.New, b.csrfKey)
	mac.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func tokenHash(token string) []byte { hash := sha256.Sum256([]byte(token)); return hash[:] }
func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
