package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

var errKeyExport = errors.New("purpose-key export failed; retain any partial destination for private inspection; no existing file was overwritten")

type keyExportManifest struct {
	Format           string                  `json:"format"`
	TokenEncryption  *config.TokenEncryption `json:"tokenEncryption,omitempty"`
	ReportPolicyFile string                  `json:"reportPolicyKeyFile,omitempty"`
	LogCursorFile    string                  `json:"logCursorKeyFile,omitempty"`
}

// preparePurposeExport never writes. Validate and read all legacy inputs before
// creating the destination, so a missing previous key cannot produce a falsely
// complete export or silently strand an encrypted device token.
func preparePurposeExport(c config.Config, destination string) (map[string][]byte, error) {
	if c.Notifications.TokenEncryption != nil || c.Reports.PolicyKeyFile != "" || c.LogCursorKeyFile != "" {
		return nil, errors.New("purpose export requires a legacy configuration without dedicated key fields")
	}
	master, err := config.ReadSecret(c.Encryption.KeyFile, 32)
	if err != nil || len(master) != 32 {
		return nil, errKeyExport
	}
	files := map[string][]byte{}
	manifest := keyExportManifest{Format: "jobman.dashboard.purpose-keys/v1"}
	if len(c.Notifications.DeviceTopics) > 0 {
		keys := append([]config.Encryption{c.Encryption}, c.Notifications.PreviousTokenKeys...)
		manifest.TokenEncryption = &config.TokenEncryption{}
		for i, key := range keys {
			root := master
			if i > 0 {
				root, err = config.ReadSecret(key.KeyFile, 32)
				if err != nil {
					return nil, errKeyExport
				}
			}
			derived, err := notifications.DeriveDeviceTokenKey(root)
			if err != nil {
				return nil, errKeyExport
			}
			name := "token-" + strconv.Itoa(i) + ".key"
			files[name] = derived
			entry := config.Encryption{KeyID: key.KeyID, KeyFile: filepath.Join(destination, name)}
			if i == 0 {
				manifest.TokenEncryption.Current = entry
			} else {
				manifest.TokenEncryption.Previous = append(manifest.TokenEncryption.Previous, entry)
			}
		}
	}
	if c.Reports.RedactionFile != "" {
		files["report-policy.key"], err = loadPurposeKey("", master, reportPolicyKeyPurpose)
		if err != nil {
			return nil, errKeyExport
		}
		manifest.ReportPolicyFile = filepath.Join(destination, "report-policy.key")
	}
	if len(c.LogBrokers) > 0 {
		files["log-cursor.key"], err = loadLogCursorKey("", master)
		if err != nil {
			return nil, errKeyExport
		}
		manifest.LogCursorFile = filepath.Join(destination, "log-cursor.key")
	}
	if len(files) == 0 {
		return nil, errors.New("legacy configuration has no purpose keys to export")
	}
	files["manifest.json"], err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, errKeyExport
	}
	return files, nil
}

// A fresh child of a private directory is required. Exclusive creation leaves a
// partial directory on interruption; manifest.json is written and synced last.
// No automatic replacement or removal is appropriate for operator key material.
func writePurposeExport(destination string, files map[string][]byte) error {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || destination == string(filepath.Separator) {
		return errKeyExport
	}
	parentPath, name := filepath.Dir(destination), filepath.Base(destination)
	info, err := os.Lstat(parentPath)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || !exportOwnedByOperator(info) {
		return errKeyExport
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return errKeyExport
	}
	defer parent.Close()
	opened, err := parent.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return errKeyExport
	}
	if err = parent.Mkdir(name, 0700); err != nil {
		return errKeyExport
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return errKeyExport
	}
	defer root.Close()
	write := func(name string, data []byte) error {
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errKeyExport
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return errKeyExport
		}
		return nil
	}
	for name, data := range files {
		if name == "manifest.json" {
			continue
		}
		if err = write(name, data); err != nil {
			return err
		}
	}
	if err = write("manifest.json", files["manifest.json"]); err != nil {
		return err
	}
	for _, directory := range []*os.Root{root, parent} {
		f, err := directory.Open(".")
		if err != nil {
			return errKeyExport
		}
		err = f.Sync()
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return errKeyExport
		}
	}
	return nil
}

func runKeyOperator(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "export" {
		return errors.New("keys requires export --config and --output-directory")
	}
	fs := flag.NewFlagSet("keys export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("config", "", "absolute legacy private configuration")
	destination := fs.String("output-directory", "", "new directory inside a private operator directory")
	if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || !filepath.IsAbs(*path) || !filepath.IsAbs(*destination) {
		return errors.New("key export requires an absolute configuration and fresh private output directory")
	}
	c, err := config.Load(*path)
	if err != nil {
		return errors.New("legacy export configuration is unavailable or invalid")
	}
	if err = validateKeyExportDestination(c.WebRoot, *destination); err != nil {
		return err
	}
	files, err := preparePurposeExport(c, *destination)
	if err != nil {
		return err
	}
	if err = writePurposeExport(*destination, files); err != nil {
		return err
	}
	_, err = io.WriteString(output, "Purpose keys exported privately. Use the completed manifest to configure separate process identities; source configuration was not changed.\n")
	return err
}

func validateKeyExportDestination(web, destination string) error {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return errKeyExport
	}
	static, err := filepath.EvalSymlinks(web)
	if err != nil {
		return errKeyExport
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil || config.ContainsPath(static, filepath.Join(parent, filepath.Base(destination))) {
		return errors.New("purpose-key destination must resolve outside the existing static web tree")
	}
	return nil
}
