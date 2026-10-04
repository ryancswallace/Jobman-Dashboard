package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

func TestNotificationRuntimeLoadsPrivateKeysWithoutProviderTraffic(t *testing.T) {
	c := config.Config{Encryption: config.Encryption{KeyID: "current"}, Events: config.Events{Enabled: true}}
	c.Notifications.DeviceTopics = []config.NotificationTopic{{Topic: "internal.example.JobmanDashboard", Environment: "sandbox"}}
	oldKey := bytes.Repeat([]byte{2}, 32)
	current := bytes.Repeat([]byte{3}, 32)
	oldPath := filepath.Join(t.TempDir(), "old.key")
	if err := os.WriteFile(oldPath, oldKey, 0600); err != nil {
		t.Fatal(err)
	}
	c.Notifications.PreviousTokenKeys = []config.Encryption{{KeyID: "old", KeyFile: oldPath}}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	applePath := filepath.Join(t.TempDir(), "apns.p8")
	if err = os.WriteFile(applePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	c.Notifications.APNs = []config.APNsProvider{{Topic: c.Notifications.DeviceTopics[0].Topic, Environment: "sandbox", TeamID: "ABCDE12345", KeyID: "FGHIJ67890", PrivateKeyFile: applePath}}
	runtime, err := loadNotificationRuntime(c, current)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if len(runtime.providers) != 1 || !runtime.policy.Allows(c.Notifications.DeviceTopics[0].Topic, "sandbox") || runtime.policy.Allows(c.Notifications.DeviceTopics[0].Topic, "production") {
		t.Fatal("provider/topic isolation lost")
	}
	oldCipher, err := notifications.NewDeviceCipher("old", map[string][]byte{"old": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	b := notifications.DeviceTokenBinding{InstallationID: operatorID, BindingID: operatorID, AccountID: operatorID, Topic: c.Notifications.DeviceTopics[0].Topic, Environment: "sandbox", TokenVersion: 1}
	token, err := oldCipher.Seal(b, "aabbccdd")
	if err != nil {
		t.Fatal(err)
	}
	got, err := runtime.cipher.Open(b, token)
	if err != nil || got != "aabbccdd" {
		t.Fatal("previous read key not retained", err)
	}
	if err = os.Chmod(applePath, 0644); err != nil {
		t.Fatal(err)
	}
	if bad, err := loadNotificationRuntime(c, current); err == nil {
		bad.Close()
		t.Fatal("public signing key accepted")
	}
	c.Notifications.APNs = nil
	runtime2, err := loadNotificationRuntime(c, current)
	if err != nil || len(runtime2.providers) != 0 {
		t.Fatal("device registration wrongly requires Apple credentials", err)
	}
	runtime2.Close()
}
func TestNotificationPrivateMaterialCannotResolveInsideStaticRoot(t *testing.T) {
	for _, readKey := range []bool{false, true} {
		c, base := staticFixture(t)
		alias := filepath.Join(base, "secret-alias")
		mustLink(t, c.WebRoot, alias)
		path := filepath.Join(alias, "index.html")
		if readKey {
			c.Notifications.PreviousTokenKeys = []config.Encryption{{KeyID: "old", KeyFile: path}}
		} else {
			c.Notifications.APNs = []config.APNsProvider{{PrivateKeyFile: path}}
		}
		root, err := openStatic(c)
		if root != nil {
			root.Close()
		}
		if err == nil {
			t.Fatal("private notification material could be served as web content")
		}
	}
}
