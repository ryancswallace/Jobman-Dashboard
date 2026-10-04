package config

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestDedicatedTokenKeysRequireExplicitUnambiguousPrivateRing(t *testing.T) {
	base := func() Config {
		c := notificationConfig(t)
		c.Notifications.PreviousTokenKeys = nil
		c.Notifications.TokenEncryption = &TokenEncryption{Current: Encryption{KeyID: "current", KeyFile: "/private/token-current"}, Previous: []Encryption{{KeyID: "old", KeyFile: "/private/token-old"}}}
		return c
	}
	if _, err := Decode(bytes.NewReader(encoded(t, base()))); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"legacy mixed": func(c *Config) {
			c.Notifications.PreviousTokenKeys = []Encryption{{KeyID: "legacy", KeyFile: "/private/legacy"}}
		},
		"duplicate": func(c *Config) { c.Notifications.TokenEncryption.Previous[0].KeyID = "current" },
		"relative":  func(c *Config) { c.Notifications.TokenEncryption.Current.KeyFile = "token-key" },
		"static current": func(c *Config) {
			c.Notifications.TokenEncryption.Current.KeyFile = filepath.Join(c.WebRoot, "current-key")
		},
		"static previous": func(c *Config) {
			c.Notifications.TokenEncryption.Previous[0].KeyFile = filepath.Join(c.WebRoot, "previous-key")
		},
		"no topics":              func(c *Config) { c.Notifications.DeviceTopics = nil },
		"policy without policy":  func(c *Config) { c.Reports.RedactionFile = ""; c.Reports.PolicyKeyFile = "/private/policy-key" },
		"cursor without brokers": func(c *Config) { c.LogBrokers = nil; c.LogCursorKeyFile = "/private/cursor-key" },
	} {
		t.Run(name, func(t *testing.T) {
			c := base()
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe or ambiguous dedicated key accepted")
			}
		})
	}
}

func TestOptionalPurposeKeyStructureRemainsStrict(t *testing.T) {
	for _, document := range []string{
		`{"ring":null}`,
		`{"ring":"key"}`,
		`{"ring":{"unknown":"value"}}`,
		`{"ring":{"current":{"keyId":"a","keyId":"b"}}}`,
		`{"ring":{"current":null}}`,
	} {
		var target struct {
			Ring *TokenEncryption `json:"ring"`
		}
		if DecodeDocument(bytes.NewBufferString(document), &target) == nil {
			t.Fatal("optional structure weakened strict parsing")
		}
	}
}
