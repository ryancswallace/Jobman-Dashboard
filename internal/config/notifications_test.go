package config

import (
	"bytes"
	"path/filepath"
	"testing"
)

func notificationConfig(t *testing.T) Config {
	c := example(t)
	c.Events.Enabled = true
	c.Notifications = Notifications{DeviceTopics: []NotificationTopic{{"internal.example.JobmanDashboard", "sandbox"}, {"internal.example.JobmanDashboard", "production"}}, PreviousTokenKeys: []Encryption{{KeyID: "older-key", KeyFile: "/run/secrets/older-token-key"}}, APNs: []APNsProvider{{Topic: "internal.example.JobmanDashboard", Environment: "sandbox", TeamID: "ABCDE12345", KeyID: "FGHIJ67890", PrivateKeyFile: "/run/secrets/apns-key.p8"}}}
	return c
}
func TestNotificationConfigurationSeparatesTopicEnvironmentAndPrivateMaterial(t *testing.T) {
	c := notificationConfig(t)
	if _, err := Decode(bytes.NewReader(encoded(t, c))); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Config){
		"unknown topic": func(c *Config) { c.Notifications.APNs[0].Topic = "internal.example.Other" },
		"duplicate topic": func(c *Config) {
			c.Notifications.DeviceTopics = append(c.Notifications.DeviceTopics, c.Notifications.DeviceTopics[0])
		},
		"unknown environment":     func(c *Config) { c.Notifications.DeviceTopics[0].Environment = "testing" },
		"duplicate provider":      func(c *Config) { c.Notifications.APNs = append(c.Notifications.APNs, c.Notifications.APNs[0]) },
		"duplicate key":           func(c *Config) { c.Notifications.PreviousTokenKeys[0].KeyID = c.Encryption.KeyID },
		"invalid Apple ID":        func(c *Config) { c.Notifications.APNs[0].TeamID = "invalid" },
		"relative key":            func(c *Config) { c.Notifications.APNs[0].PrivateKeyFile = "./token-key" },
		"provider without events": func(c *Config) { c.Events.Enabled = false },
		"public signing key":      func(c *Config) { c.Notifications.APNs[0].PrivateKeyFile = filepath.Join(c.WebRoot, "key.p8") },
		"public read key":         func(c *Config) { c.Notifications.PreviousTokenKeys[0].KeyFile = filepath.Join(c.WebRoot, "older-key") },
	} {
		t.Run(name, func(t *testing.T) {
			c := notificationConfig(t)
			change(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe notification configuration accepted")
			}
		})
	}
}
