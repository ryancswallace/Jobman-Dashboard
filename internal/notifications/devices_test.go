package notifications

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const testDeviceInstallation = "10000000-0000-4000-8000-000000000001"

func deviceTestSecret() string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{31}, 32))
}
func deviceTestPins() DeviceTokenBinding {
	return DeviceTokenBinding{InstallationID: testDeviceInstallation, BindingID: "20000000-0000-4000-8000-000000000002", AccountID: "30000000-0000-4000-8000-000000000003", Topic: "test.jobman.dashboard", Environment: "sandbox", TokenVersion: 1}
}
func TestDeviceSecretsAndInputBounds(t *testing.T) {
	p, err := NewDevicePolicy([]DeviceTopic{{"test.jobman.dashboard", "sandbox"}})
	if err != nil {
		t.Fatal(err)
	}
	good := DeviceRegistration{RevocationID: "88000000-0000-4000-8000-000000000001", RevocationCredential: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32)), InstallationID: testDeviceInstallation, InstallationSecret: deviceTestSecret(), Label: "Synthetic phone", Topic: "test.jobman.dashboard", Environment: "sandbox", Token: "ab", Permission: "authorized", Enabled: true}
	if err = good.Validate(p); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*DeviceRegistration){
		"token case": func(r *DeviceRegistration) { r.Token = "AB" }, "odd token": func(r *DeviceRegistration) { r.Token = "abc" }, "oversize token": func(r *DeviceRegistration) { r.Token = strings.Repeat("ab", 513) }, "unknown environment": func(r *DeviceRegistration) { r.Environment = "production" }, "topic": func(r *DeviceRegistration) { r.Topic = "other.app" }, "secret padding": func(r *DeviceRegistration) { r.InstallationSecret += "=" }, "secret short": func(r *DeviceRegistration) { r.InstallationSecret = "abc" }, "secret long": func(r *DeviceRegistration) { r.InstallationSecret = strings.Repeat("a", 100000) }, "label control": func(r *DeviceRegistration) { r.Label = "phone\n" }, "label bytes": func(r *DeviceRegistration) { r.Label = strings.Repeat("é", 61) }, "permission": func(r *DeviceRegistration) { r.Permission = "enabled" }, "id": func(r *DeviceRegistration) { r.InstallationID = "00000000-0000-0000-0000-000000000000" },
	} {
		t.Run(name, func(t *testing.T) {
			r := good
			change(&r)
			if !errors.Is(r.Validate(p), ErrDeviceInvalid) {
				t.Fatal("unbounded or untrusted input accepted")
			}
		})
	}
	long := good
	long.Token = strings.Repeat("ab", 512)
	if long.Validate(p) != nil {
		t.Fatal("maximum variable-length token rejected")
	}
	for _, permission := range []string{"not_determined", "denied", "authorized", "provisional", "ephemeral"} {
		r := good
		r.Permission = permission
		if r.Validate(p) != nil {
			t.Fatal(permission)
		}
	}
	if DevicePermissionAllowsDelivery("denied") || DevicePermissionAllowsDelivery("not_determined") || !DevicePermissionAllowsDelivery("ephemeral") {
		t.Fatal("permission delivery policy")
	}
	first, _ := InstallationSecretHash(testDeviceInstallation, deviceTestSecret())
	second, _ := InstallationSecretHash("10000000-0000-4000-8000-000000000002", deviceTestSecret())
	if bytes.Equal(first, second) || len(first) != 32 {
		t.Fatal("secret proof not bound to installation")
	}
	encoded, _ := json.Marshal(good)
	if bytes.Contains(encoded, []byte(deviceTestSecret())) || bytes.Contains(encoded, []byte(`"Token"`)) {
		t.Fatal("private request material serialized")
	}
	for _, topics := range [][]DeviceTopic{nil, {{"test.jobman.dashboard", "sandbox"}, {"test.jobman.dashboard", "sandbox"}}, {{"invalid topic", "sandbox"}}, {{"test.jobman.dashboard", "unknown"}}} {
		if _, err := NewDevicePolicy(topics); err == nil {
			t.Fatal("bad operator allowlist accepted")
		}
	}
}
func TestDeviceCipherAuthenticatedPinsAndRotation(t *testing.T) {
	oldKey, newKey := bytes.Repeat([]byte{17}, 32), bytes.Repeat([]byte{29}, 32)
	old, err := NewDeviceCipher("old", map[string][]byte{"old": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	pins, token := deviceTestPins(), strings.Repeat("deadc0de", 40)
	sealed, err := old.Seal(pins, token)
	if err != nil {
		t.Fatal(err)
	}
	again, err := old.Seal(pins, token)
	if err != nil || bytes.Equal(sealed.Ciphertext, again.Ciphertext) || bytes.Contains(sealed.Ciphertext, []byte(token)) {
		t.Fatal("nonce reuse/plaintext", err)
	}
	current, err := NewDeviceCipher("new", map[string][]byte{"old": oldKey, "new": newKey})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := current.Open(pins, sealed); err != nil || got != token {
		t.Fatal("previous read key unavailable", err)
	}
	latest, err := current.Seal(pins, token)
	if err != nil || latest.KeyID != "new" {
		t.Fatal("write key not rotated", err)
	}
	for name, change := range map[string]func(*DeviceTokenBinding){"installation": func(p *DeviceTokenBinding) { p.InstallationID = "10000000-0000-4000-8000-000000000002" }, "binding": func(p *DeviceTokenBinding) { p.BindingID = "20000000-0000-4000-8000-000000000003" }, "account": func(p *DeviceTokenBinding) { p.AccountID = "30000000-0000-4000-8000-000000000004" }, "topic": func(p *DeviceTokenBinding) { p.Topic = "other.jobman.dashboard" }, "environment": func(p *DeviceTokenBinding) { p.Environment = "production" }, "version": func(p *DeviceTokenBinding) { p.TokenVersion++ }} {
		t.Run(name, func(t *testing.T) {
			wrong := pins
			change(&wrong)
			if _, err := current.Open(wrong, sealed); !errors.Is(err, ErrDeviceUnavailable) {
				t.Fatal("AAD substitution accepted", err)
			}
		})
	}
	modified := EncryptedDeviceToken{KeyID: sealed.KeyID, Ciphertext: bytes.Clone(sealed.Ciphertext)}
	modified.Ciphertext[len(modified.Ciphertext)-1] ^= 1
	if _, err := current.Open(pins, modified); !errors.Is(err, ErrDeviceUnavailable) {
		t.Fatal("modified ciphertext accepted")
	}
	modified = sealed
	modified.KeyID = "new"
	if _, err := current.Open(pins, modified); !errors.Is(err, ErrDeviceUnavailable) {
		t.Fatal("key substitution accepted")
	}
	retired, _ := NewDeviceCipher("new", map[string][]byte{"new": newKey})
	if _, err := retired.Open(pins, sealed); !errors.Is(err, ErrDeviceUnavailable) {
		t.Fatal("retired key opened")
	}
	for _, bad := range []EncryptedDeviceToken{{KeyID: "old"}, {KeyID: "old", Ciphertext: make([]byte, 1101)}} {
		if _, err := current.Open(pins, bad); !errors.Is(err, ErrDeviceUnavailable) {
			t.Fatal("invalid envelope accepted")
		}
	}
	if _, err := (&DeviceCipher{}).Seal(pins, token); !errors.Is(err, ErrDeviceInvalid) {
		t.Fatal("zero cipher accepted")
	}
	if _, err := NewDeviceCipher("missing", map[string][]byte{"old": oldKey}); err == nil {
		t.Fatal("missing active key accepted")
	}
	if _, err := NewDeviceCipher("old", map[string][]byte{"old": oldKey[:31]}); err == nil {
		t.Fatal("short root key accepted")
	}
	oldKey[0] ^= 1
	if got, err := old.Open(pins, sealed); err != nil || got != token {
		t.Fatal("retained caller key buffer", err)
	}
	a := DeviceTokenDigest(pins.Topic, pins.Environment, token)
	b := DeviceTokenDigest(pins.Topic, "production", token)
	if len(a) != 32 || bytes.Equal(a, b) {
		t.Fatal("token digest scope not bound")
	}
	handoff := DeviceHandoff{Token: token}
	encoded, _ := json.Marshal(handoff)
	if bytes.Contains(encoded, []byte(token)) {
		t.Fatal("handoff token serialized")
	}
}

func TestDeviceRevocationHashPurposeAndGenerationBinding(t *testing.T) {
	id := "90000000-0000-4000-8000-000000000001"
	installation := testDeviceInstallation
	binding := deviceTestPins().BindingID
	baseline, err := RevocationSecretHash(id, installation, binding, deviceTestSecret())
	if err != nil || len(baseline) != 32 {
		t.Fatal(err)
	}
	for _, parts := range [][3]string{{testDeviceInstallation, installation, binding}, {id, id, binding}, {id, installation, id}} {
		other, err := RevocationSecretHash(parts[0], parts[1], parts[2], deviceTestSecret())
		if err != nil || bytes.Equal(baseline, other) {
			t.Fatal("authority crossed credential/install/generation", err)
		}
	}
	proof, _ := InstallationSecretHash(id, deviceTestSecret())
	if bytes.Equal(proof, baseline) {
		t.Fatal("installation proof reused as revocation authority")
	}
	for _, secret := range []string{"", deviceTestSecret() + "=", strings.Repeat("x", 44), strings.Repeat("/", 43)} {
		if ValidateDeviceRevocation(id, secret) == nil {
			t.Fatal("noncanonical secret accepted")
		}
	}
	encoded, _ := json.Marshal(DeviceRevocationInput{InstallationSecret: deviceTestSecret(), RevocationID: id, RevocationCredential: deviceTestSecret()})
	if bytes.Contains(encoded, []byte(deviceTestSecret())) {
		t.Fatal("request proofs serialized")
	}
}
