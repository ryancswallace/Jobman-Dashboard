package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

func privatePurposeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDedicatedPurposeKeysPreservePolicyAndCiphertextWithoutMaster(t *testing.T) {
	master := bytes.Repeat([]byte{0x19}, 32)
	policyKey, err := loadPurposeKey("", master, reportPolicyKeyPurpose)
	if err != nil {
		t.Fatal(err)
	}
	logKey, err := loadLogCursorKey("", master)
	if err != nil || bytes.Equal(logKey, policyKey) {
		t.Fatal("purposes are not separated", err)
	}
	tokenKey, err := notifications.DeriveDeviceTokenKey(master)
	if err != nil || bytes.Equal(tokenKey, policyKey) || bytes.Equal(tokenKey, logKey) || bytes.Equal(tokenKey, master) {
		t.Fatal("token purpose is not separated", err)
	}
	reportConfig := config.Reports{RedactionFile: privatePurposeFile(t, "redaction.json", []byte(`{"values":["synthetic-canary"]}`))}
	legacyPolicy, err := loadReportPolicy(reportConfig, master)
	if err != nil {
		t.Fatal(err)
	}
	reportConfig.PolicyKeyFile = privatePurposeFile(t, "policy.key", policyKey)
	isolatedPolicy, err := loadReportPolicy(reportConfig, nil)
	if err != nil || isolatedPolicy.Fingerprint() != legacyPolicy.Fingerprint() {
		t.Fatal("report identity changed during purpose export", err)
	}
	dedicatedLog, err := loadLogCursorKey(privatePurposeFile(t, "cursor.key", logKey), nil)
	if err != nil || !bytes.Equal(dedicatedLog, logKey) {
		t.Fatal("exported log key changed", err)
	}
	topic := config.NotificationTopic{Topic: "org.example.dashboard", Environment: "sandbox"}
	c := config.Config{Notifications: config.Notifications{DeviceTopics: []config.NotificationTopic{topic}, TokenEncryption: &config.TokenEncryption{Current: config.Encryption{KeyID: "original", KeyFile: privatePurposeFile(t, "token.key", tokenKey)}}}}
	isolated, err := loadNotificationRuntime(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer isolated.Close()
	legacy, err := notifications.NewDeviceCipher("original", map[string][]byte{"original": master})
	if err != nil {
		t.Fatal(err)
	}
	binding := notifications.DeviceTokenBinding{InstallationID: operatorID, BindingID: operatorID, AccountID: operatorID, Topic: topic.Topic, Environment: topic.Environment, TokenVersion: 1}
	for _, pair := range [][2]*notifications.DeviceCipher{{legacy, isolated.cipher}, {isolated.cipher, legacy}} {
		sealed, err := pair[0].Seal(binding, "aabbccdd")
		if err != nil {
			t.Fatal(err)
		}
		if token, err := pair[1].Open(binding, sealed); err != nil || token != "aabbccdd" {
			t.Fatal("existing ciphertext compatibility lost", err)
		}
		binding.TokenVersion++
		if _, err := pair[1].Open(binding, sealed); err == nil {
			t.Fatal("export lost token-version authentication")
		}
		binding.TokenVersion--
	}
}

func TestDedicatedPurposeKeysFailClosedWithoutPrivateMaterial(t *testing.T) {
	if _, err := loadLogCursorKey("", nil); err == nil {
		t.Fatal("missing isolated key accepted")
	}
	if _, err := loadPurposeKey("", bytes.Repeat([]byte{1}, 32), "unreviewed-purpose"); err == nil {
		t.Fatal("arbitrary key purpose accepted")
	}
	for _, key := range [][]byte{nil, bytes.Repeat([]byte{1}, 31), bytes.Repeat([]byte{1}, 33)} {
		if _, err := loadLogCursorKey(privatePurposeFile(t, "bad.key", key), nil); err == nil {
			t.Fatal("wrong-size dedicated key accepted")
		}
	}
	path := privatePurposeFile(t, "public.key", bytes.Repeat([]byte{1}, 32))
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLogCursorKey(path, bytes.Repeat([]byte{2}, 32)); err == nil {
		t.Fatal("unsafe dedicated key fell back to the master")
	}
}
