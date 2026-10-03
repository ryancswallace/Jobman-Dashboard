package config

import (
	"bytes"
	"encoding/json"
	"testing"
)

func configuredLogs(t *testing.T) Config {
	t.Helper()
	c := example(t)
	source := c.Controls[0]
	c.LogBrokers = []RemoteBroker{{ID: "logs", DeploymentID: source.ID, Origin: "https://broker.example.internal", NamespaceIDs: source.NamespaceIDs, TrustRootsFile: "/etc/trust.pem", ClientCertificateFile: "/etc/client.pem", ClientKeyFile: "/run/secrets/client-key", DelegationKeyFile: "/run/secrets/delegation-key", DelegationKeyID: "broker-key", ServiceID: "dashboard", Audience: "urn:broker"}}
	c.LogMappings = []RemoteLogMapping{{BrokerID: "logs", DeploymentID: source.ID, StoreName: "lab-nfs", StoreVersion: "1"}}
	return c
}
func TestLogMappingRejectsImplicitTrustAndCrossSourceRouting(t *testing.T) {
	c := configuredLogs(t)
	if _, err := Decode(bytes.NewReader(encoded(t, c))); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.LogBrokers[0].Origin = "http://broker" },
		func(c *Config) { c.LogBrokers[0].NamespaceIDs = []string{"11111111-1111-4111-8111-111111111111"} },
		func(c *Config) { c.LogBrokers[0].ClientKeyFile = "relative" },
		func(c *Config) { c.LogMappings[0].DeploymentID = "44444444-4444-4444-8444-444444444444" },
		func(c *Config) { c.LogMappings[0].BrokerID = "unknown" },
		func(c *Config) { c.LogMappings[0].TargetGenerationID = "arbitrary" },
		func(c *Config) { c.LogMappings[0].StoreName = "../logs" },
		func(c *Config) { c.LogMappings[0].StoreVersion = "01" },
		func(c *Config) { c.LogMappings = append(c.LogMappings, c.LogMappings[0]) },
	} {
		c := configuredLogs(t)
		mutate(&c)
		if _, err := Decode(bytes.NewReader(encoded(t, c))); err == nil {
			t.Fatal("invalid mapping accepted")
		}
	}
}
func TestBrokerConfigStrictDecodeAndLimits(t *testing.T) {
	c := example(t)
	b := BrokerConfig{ConfigurationRevision: 1, PublicOrigin: "https://broker.example.internal", Listen: "127.0.0.1:9443", ServerTLS: c.ServerTLS, ClientTrustRootsFile: "/etc/roots.pem", StateDirectory: "/var/lib/jobman-log-broker", Controls: c.Controls, Services: []BrokerRegistration{{KeyID: "key", ServiceID: "dashboard", Audience: "urn:broker", DeploymentID: c.Controls[0].ID, ClientCertificateFile: "/etc/client.pem", PublicKeyFile: "/etc/delegation.pub", NamespaceIDs: c.Controls[0].NamespaceIDs}}, LogRoots: []LogRoot{{DeploymentID: c.Controls[0].ID, StoreName: "logs", StoreVersion: "1", Root: "/srv/logs"}}, ReaderConcurrency: 4, ReaderTimeoutMilliseconds: 3000}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var decoded BrokerConfig
	if err = DecodeDocument(bytes.NewReader(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	if err = decoded.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*BrokerConfig){func(b *BrokerConfig) { b.ReaderConcurrency = 17 }, func(b *BrokerConfig) { b.ReaderTimeoutMilliseconds = 10001 }, func(b *BrokerConfig) { b.StateDirectory = "relative" }, func(b *BrokerConfig) { b.LogRoots[0].Root = "/../etc" }, func(b *BrokerConfig) { b.Services[0].PublicKeyFile = "" }, func(b *BrokerConfig) { b.Services[0].NamespaceIDs = []string{"unmapped"} }} {
		var copy BrokerConfig
		if err = json.Unmarshal(raw, &copy); err != nil {
			t.Fatal(err)
		}
		mutate(&copy)
		if copy.Validate() == nil {
			t.Fatal("invalid broker deployment accepted")
		}
	}
}
