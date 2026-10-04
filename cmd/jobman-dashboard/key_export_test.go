package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

func TestKeyExportPreservesEveryKeyIDAndProducesPrivateCompletionManifest(t *testing.T) {
	master := bytes.Repeat([]byte{11}, 32)
	previous := bytes.Repeat([]byte{22}, 32)
	c := config.Config{Encryption: config.Encryption{KeyID: "current", KeyFile: privatePurposeFile(t, "master.key", master)}, Notifications: config.Notifications{DeviceTopics: []config.NotificationTopic{{Topic: "org.example.dashboard", Environment: "sandbox"}}, PreviousTokenKeys: []config.Encryption{{KeyID: "prior", KeyFile: privatePurposeFile(t, "old-master.key", previous)}}}, Reports: config.Reports{RedactionFile: "/private/policy"}, LogBrokers: []config.RemoteBroker{{}}}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(parent, "export")
	files, err := preparePurposeExport(c, destination)
	if err != nil || len(files) != 5 {
		t.Fatal("export did not retain all purposes/read keys", err)
	}
	want, _ := notifications.DeriveDeviceTokenKey(previous)
	if !bytes.Equal(files["token-1.key"], want) || bytes.Equal(files["token-0.key"], master) {
		t.Fatal("raw master was exported or prior key lost")
	}
	if err = writePurposeExport(destination, files); err != nil {
		t.Fatal(err)
	}
	for name, expected := range files {
		path := filepath.Join(destination, name)
		actual, err := os.ReadFile(path)
		info, statErr := os.Lstat(path)
		if err != nil || statErr != nil || !bytes.Equal(actual, expected) || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			t.Fatal("exported private file differs", name, err, statErr)
		}
	}
	var manifest keyExportManifest
	if json.Unmarshal(files["manifest.json"], &manifest) != nil || manifest.TokenEncryption.Current.KeyID != "current" || manifest.TokenEncryption.Previous[0].KeyID != "prior" || manifest.ReportPolicyFile == "" || manifest.LogCursorFile == "" {
		t.Fatal("completion manifest lost configured identities")
	}
	if err = writePurposeExport(destination, files); err == nil {
		t.Fatal("existing secret directory reused")
	}
	c.Notifications.PreviousTokenKeys[0].KeyFile = filepath.Join(parent, "missing")
	if data, err := preparePurposeExport(c, filepath.Join(parent, "must-not-exist")); err == nil || data != nil {
		t.Fatal("missing old read key silently omitted")
	}
}

func TestKeyExportRejectsPublicLinkedOrStaticDestinations(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writePurposeExport(filepath.Join(parent, "keys"), map[string][]byte{"manifest.json": []byte("{}")}); err == nil {
		t.Fatal("public parent accepted")
	}
	web := t.TempDir()
	if err := validateKeyExportDestination(web, filepath.Join(web, "private")); err == nil {
		t.Fatal("static destination accepted")
	}
	alias := filepath.Join(parent, "static-alias")
	if err := os.Symlink(web, alias); err != nil {
		t.Fatal(err)
	}
	if err := validateKeyExportDestination(web, filepath.Join(alias, "private")); err == nil {
		t.Fatal("aliased static destination accepted")
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writePurposeExport(filepath.Join(alias, "keys"), map[string][]byte{"manifest.json": []byte("{}")}); err == nil {
		t.Fatal("linked export parent accepted")
	}
}
