package config

import (
	"testing"
)

func TestEveryConfiguredSecretIsOutsideStaticTree(t *testing.T) {
	c := example(t)
	c.Reports = Reports{ObjectRoot: "/private/reports", RedactionFile: "/private/redaction.json"}
	c.LogBrokers = []RemoteBroker{{ClientKeyFile: "/private/broker-key", DelegationKeyFile: "/private/broker-signing"}}
	mutations := map[string]func(*Config, string){
		"server key":         func(c *Config, p string) { c.ServerTLS.KeyFile = p },
		"database":           func(c *Config, p string) { c.DatabaseURLFile = p },
		"OIDC client":        func(c *Config, p string) { c.OIDC.WebClientSecretFile = p },
		"encryption":         func(c *Config, p string) { c.Encryption.KeyFile = p },
		"redaction":          func(c *Config, p string) { c.Reports.RedactionFile = p },
		"Control TLS":        func(c *Config, p string) { c.Controls[0].ClientKeyFile = p },
		"Control delegation": func(c *Config, p string) { c.Controls[0].DelegationKeyFile = p },
		"broker TLS":         func(c *Config, p string) { c.LogBrokers[0].ClientKeyFile = p },
		"broker delegation":  func(c *Config, p string) { c.LogBrokers[0].DelegationKeyFile = p },
	}
	if len(c.PrivateFilePaths()) != len(mutations) {
		t.Fatal("secret enumeration and tested fields differ")
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			copy := c
			copy.Controls = append([]Control(nil), c.Controls...)
			copy.LogBrokers = append([]RemoteBroker(nil), c.LogBrokers...)
			mutate(&copy, c.WebRoot+"/private-material")
			if copy.validatePrivatePaths() == nil {
				t.Fatal("secret accepted in static directory")
			}
			mutate(&copy, c.WebRoot+"-private/private-material")
			if err := copy.validatePrivatePaths(); err != nil {
				t.Fatalf("sibling path incorrectly rejected: %v", err)
			}
		})
	}
	valid := example(t)
	valid.Reports.RedactionFile = valid.WebRoot + "/policy.json"
	valid.Reports.ObjectRoot = "/private/reports"
	if valid.Validate() == nil {
		t.Fatal("full configuration validation accepted a public redaction policy")
	}
}
