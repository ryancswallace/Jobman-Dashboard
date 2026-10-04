package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/reports"
)

const (
	reportPolicyKeyPurpose = "jobman-dashboard/diagnosis-policy-key/v1"
	logCursorKeyPurpose    = "jobman-dashboard/log-cursor-key/v1"
)

// Legacy derivation is retained only for the combined/API compatibility loader
// and explicit offline export. Workers must receive dedicated purpose keys.
func loadPurposeKey(path string, legacyRoot []byte, purpose string) ([]byte, error) {
	if purpose != reportPolicyKeyPurpose && purpose != logCursorKeyPurpose {
		return nil, errors.New("unsupported key purpose")
	}
	if path != "" {
		key, err := config.ReadSecret(path, 32)
		if err != nil || len(key) != 32 {
			return nil, errors.New("purpose key must contain exactly32 private raw bytes")
		}
		return key, nil
	}
	if len(legacyRoot) != 32 {
		return nil, errors.New("dedicated purpose key is required")
	}
	mac := hmac.New(sha256.New, legacyRoot)
	mac.Write([]byte(purpose))
	return mac.Sum(nil), nil
}

func loadLogCursorKey(path string, legacyRoot []byte) ([]byte, error) {
	return loadPurposeKey(path, legacyRoot, logCursorKeyPurpose)
}

func loadReportPolicy(c config.Reports, legacyRoot []byte) (*reports.RedactionPolicy, error) {
	if c.RedactionFile == "" {
		if c.PolicyKeyFile != "" {
			return nil, errors.New("diagnosis policy key requires a redaction policy")
		}
		return nil, nil
	}
	encoded, err := config.ReadSecret(c.RedactionFile, config.MaxConfigBytes)
	if err != nil {
		return nil, errors.New("diagnosis redaction file is unavailable or not private")
	}
	var policy struct {
		Values   []string `json:"values"`
		Patterns []string `json:"patterns"`
	}
	if config.DecodeDocument(bytes.NewReader(encoded), &policy) != nil {
		return nil, errors.New("diagnosis redaction file is invalid")
	}
	key, err := loadPurposeKey(c.PolicyKeyFile, legacyRoot, reportPolicyKeyPurpose)
	if err != nil {
		return nil, err
	}
	result, err := reports.NewRedactionPolicy(reports.RedactionConfig{Values: policy.Values, Patterns: policy.Patterns}, key)
	if err != nil {
		return nil, errors.New("diagnosis redaction policy is invalid or exceeds its bounds")
	}
	return result, nil
}
