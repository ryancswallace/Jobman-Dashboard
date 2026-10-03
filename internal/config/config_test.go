package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func example(t *testing.T) Config {
	t.Helper()
	path, err := filepath.Abs("../../deploy/config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func encoded(t *testing.T, config Config) []byte {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestExampleIsStructuralOnlyAndRoundTripsWithoutReadingCredentials(t *testing.T) {
	c := example(t)
	if c.ConfigurationRevision != 1 || c.PublicOrigin != "https://dashboard.example.internal" || c.OIDC.NativeRedirectURI != "jobman-dashboard-auth://callback" || len(c.Controls) != 1 {
		t.Fatalf("example changed unexpectedly: %#v", c)
	}
	c.ConfigurationRevision = 9223372036854775807
	got, err := Decode(bytes.NewReader(encoded(t, c)))
	if err != nil || got.ConfigurationRevision != c.ConfigurationRevision {
		t.Fatalf("large exact revision lost: %d %v", got.ConfigurationRevision, err)
	}
}

func TestDecodeRejectsAmbiguousOrUnboundedDocumentsWithoutCredentialEcho(t *testing.T) {
	base := string(encoded(t, example(t)))
	const secret = "THIS_MUST_NEVER_APPEAR_IN_ERRORS"
	for name, document := range map[string]string{
		"empty": "", "null": "null", "array": "[]", "trailing": base + "{}",
		"unknown":             strings.Replace(base, "{", `{"inlineSecret":"`+secret+`",`, 1),
		"duplicate":           strings.Replace(base, "{", `{"publicOrigin":"`+secret+`",`, 1),
		"nested unknown":      strings.Replace(base, `"oidc":{`, `"oidc":{"clientSecret":"`+secret+`",`, 1),
		"nested duplicate":    strings.Replace(base, `"oidc":{`, `"oidc":{"issuer":"`+secret+`",`, 1),
		"case alias":          strings.Replace(base, `"publicOrigin"`, `"PublicOrigin"`, 1),
		"wrong scalar":        strings.Replace(base, `"configurationRevision":1`, `"configurationRevision":"1"`, 1),
		"fractional revision": strings.Replace(base, `"configurationRevision":1`, `"configurationRevision":1.5`, 1),
		"overflow revision":   strings.Replace(base, `"configurationRevision":1`, `"configurationRevision":9223372036854775808`, 1),
		"null scopes":         strings.Replace(base, `["openid","profile","offline_access"]`, `null`, 1),
		"null object":         strings.Replace(base, `"serverTLS":{`, `"serverTLS":null,"serverTLS":{`, 1),
		"oversized":           strings.Repeat(" ", int(MaxConfigBytes)) + base,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(document)); err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("invalid document accepted or credential echoed: %v", err)
			}
		})
	}
}

func TestRequiredTrustAndIdentityConfiguration(t *testing.T) {
	cases := map[string]func(*Config){
		"missing revision":    func(c *Config) { c.ConfigurationRevision = 0 },
		"negative revision":   func(c *Config) { c.ConfigurationRevision = -1 },
		"missing secret ref":  func(c *Config) { c.OIDC.WebClientSecretFile = "" },
		"relative secret ref": func(c *Config) { c.Encryption.KeyFile = "./secret" },
		"unclean secret ref":  func(c *Config) { c.DatabaseURLFile = "/tmp/../secret" },
		"missing trust":       func(c *Config) { c.Controls[0].TrustRootsFile = "" },
		"missing web root":    func(c *Config) { c.WebRoot = "" },
		"missing key id":      func(c *Config) { c.Encryption.KeyID = "" },
		"long key id":         func(c *Config) { c.Encryption.KeyID = strings.Repeat("a", 65) },
		"missing audience":    func(c *Config) { c.Controls[0].Audience = "" },
		"missing claim":       func(c *Config) { c.OIDC.DirectoryIDClaim = "" },
		"same clients":        func(c *Config) { c.OIDC.NativeClientID = c.OIDC.WebClientID },
		"web audience":        func(c *Config) { c.OIDC.APIAudience = c.OIDC.WebClientID },
		"native audience":     func(c *Config) { c.OIDC.APIAudience = c.OIDC.NativeClientID },
		"different callback":  func(c *Config) { c.OIDC.NativeRedirectURI = "jobman-dashboard://callback" },
		"no openid":           func(c *Config) { c.OIDC.Scopes = []string{"profile"} },
		"duplicate scopes":    func(c *Config) { c.OIDC.Scopes = []string{"openid", "openid"} },
		"compound scope":      func(c *Config) { c.OIDC.Scopes = []string{"openid", "profile email"} },
		"no sources":          func(c *Config) { c.Controls = nil },
		"too many sources": func(c *Config) {
			for len(c.Controls) <= 32 {
				c.Controls = append(c.Controls, c.Controls[0])
			}
		},
		"noncanonical id": func(c *Config) { c.Controls[0].ID = "abc" },
		"no namespaces":   func(c *Config) { c.Controls[0].NamespaceIDs = nil },
		"duplicate namespace": func(c *Config) {
			c.Controls[0].NamespaceIDs = append(c.Controls[0].NamespaceIDs, c.Controls[0].NamespaceIDs[0])
		},
		"too many namespaces": func(c *Config) {
			for len(c.Controls[0].NamespaceIDs) <= 320 {
				c.Controls[0].NamespaceIDs = append(c.Controls[0].NamespaceIDs, c.Controls[0].NamespaceIDs[0])
			}
		},
		"named port":     func(c *Config) { c.Listen = ":https" },
		"ephemeral port": func(c *Config) { c.Listen = ":0" },
		"large port":     func(c *Config) { c.Listen = ":65536" },
		"listen URL":     func(c *Config) { c.Listen = "https://127.0.0.1:443" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := example(t)
			mutate(&c)
			if _, err := Decode(bytes.NewReader(encoded(t, c))); err == nil {
				t.Fatal("invalid trust configuration accepted")
			}
		})
	}
}

func TestHTTPSOriginsAndCanonicalDuplicateIdentities(t *testing.T) {
	for _, invalid := range []string{"http://host", "https://user:password@host", "https://host?secret=value", "https://host?", "https://host#fragment", "https://host#", "https://host/path", "https://host:", "https://host:0", "https://host:65536", "https://host\\else", "https://"} {
		t.Run(invalid, func(t *testing.T) {
			c := example(t)
			c.Controls[0].Origin = invalid
			if err := c.Validate(); err == nil {
				t.Fatal("invalid origin accepted")
			}
		})
	}
	for _, mode := range []string{"id", "instance", "origin", "equivalent-origin"} {
		t.Run(mode, func(t *testing.T) {
			c := example(t)
			other := c.Controls[0]
			other.ID, other.ExpectedInstanceID = "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"
			other.Origin = "https://other.example.internal"
			switch mode {
			case "id":
				other.ID = c.Controls[0].ID
			case "instance":
				other.ExpectedInstanceID = c.Controls[0].ExpectedInstanceID
			case "origin":
				other.Origin = c.Controls[0].Origin
			case "equivalent-origin":
				other.Origin = "https://CONTROL.EXAMPLE.INTERNAL.:443/"
			}
			c.Controls = append(c.Controls, other)
			if err := c.Validate(); err == nil {
				t.Fatal("duplicate registered identity accepted")
			}
		})
	}
	c := example(t)
	c.PublicOrigin, c.Listen = "https://[::1]:8443/", "[::1]:8443"
	if err := c.Validate(); err != nil {
		t.Fatalf("HTTPS IPv6 private deployment rejected: %v", err)
	}
}

func TestSecretFilesArePrivateBoundedAndUnmodified(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("server file loading requires Unix")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "credential")
	want := []byte(" synthetic-secret\n\x00")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSecret(path, int64(len(want)))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("secret transformed or rejected: %v", err)
	}
	if _, err := ReadSecret(path, int64(len(want)-1)); err == nil {
		t.Fatal("oversized credential accepted")
	}
	for _, mode := range []os.FileMode{0640, 0604, 0666} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSecret(path, 4096); err == nil {
			t.Fatalf("nonprivate mode %o accepted", mode)
		}
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret(path, 4096); err != nil {
		t.Fatalf("read-only credential rejected: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{link, dir, filepath.Join(dir, "absent"), "relative"} {
		if _, err := ReadSecret(invalid, 4096); err == nil {
			t.Fatalf("nonregular path accepted: %s", invalid)
		}
	}
	for _, limit := range []int64{0, -1, MaxSecretBytes + 1} {
		if _, err := ReadSecret(path, limit); err == nil {
			t.Fatalf("unbounded limit accepted: %d", limit)
		}
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret(empty, 4096); err == nil {
		t.Fatal("empty secret accepted")
	}
}

func TestConfigSupportsDistinctSourcesWithSourceQualifiedNamespaces(t *testing.T) {
	c := example(t)
	for i := 2; i <= 32; i++ {
		other := c.Controls[0]
		other.ID = fmt.Sprintf("%08d-1111-4111-8111-111111111111", i)
		other.ExpectedInstanceID = fmt.Sprintf("%08d-2222-4222-8222-222222222222", i)
		other.Origin = fmt.Sprintf("https://control-%d.example.internal", i)
		c.Controls = append(c.Controls, other)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("32 distinct sources rejected: %v", err)
	}
}

func TestPublicCertificatesRetainFileAndByteBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "public.pem")
	if err := os.WriteFile(path, []byte("synthetic certificate"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret(path, 4096); err == nil {
		t.Fatal("public file accepted as private credential")
	}
	if _, err := ReadPublicFile(path, 4096); err != nil {
		t.Fatalf("public certificate rejected: %v", err)
	}
	if _, err := ReadPublicFile(path, 4); err == nil {
		t.Fatal("oversized public certificate accepted")
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPublicFile(link, 4096); err == nil {
		t.Fatal("public certificate symlink accepted")
	}
}
