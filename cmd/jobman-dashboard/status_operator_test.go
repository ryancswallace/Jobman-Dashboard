package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusOperatorArgumentsAreReadOnlyAndBounded(t *testing.T) {
	for _, args := range [][]string{nil, {"--config", "relative.json"}, {"--config", "/private/config.json", "--format", "xml"}, {"--config", "/private/config.json", "--apply"}, {"--config", "/private/config.json", "extra"}, {"--config", "/private/config.json", "--token", "private-canary"}} {
		var out bytes.Buffer
		if err := runStatusOperator(args, &out); err == nil || out.Len() != 0 || strings.Contains(err.Error(), "private-canary") {
			t.Fatal("invalid command produced output or exposed an argument", err)
		}
	}
	for _, format := range []string{"json", "prometheus"} {
		o, err := parseStatusOperator([]string{"--config", "/private/config.json", "--format", format})
		if err != nil || o.config != "/private/config.json" || o.format != format {
			t.Fatal("valid read-only command rejected", err)
		}
	}
	o, err := parseStatusOperator([]string{"--config", "/private/config.json"})
	if err != nil || o.format != "json" {
		t.Fatal("default format is not JSON", err)
	}
}

func TestStatusOperatorDedicatedConfigExcludesRuntimeAuthority(t *testing.T) {
	for _, args := range [][]string{
		{"--operator-config", "/private/operator.json"},
		{"--operator-config=/private/operator.json", "--format=prometheus"},
	} {
		o, e := parseStatusOperator(args)
		if e != nil || o.config != "" || o.operatorConfig != "/private/operator.json" {
			t.Fatal("dedicated config rejected", e)
		}
	}
	for _, args := range [][]string{
		{"--operator-config", "relative"},
		{"--config", "/private/runtime.json", "--operator-config", "/private/operator.json"},
		{"--config=", "--operator-config=/private/operator.json"},
		{"--operator-config=", "--config=/private/runtime.json"},
		{"--operator-config=/private/operator.json", "--mode=worker"},
	} {
		var out bytes.Buffer
		if e := runStatusOperator(args, &out); e == nil || out.Len() != 0 {
			t.Fatal("ambiguous command accepted", e)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "operator.json")
	data := []byte(`{"schemaVersion":1,"databaseURLFile":"/private/nonexistent-operator-only-database-url","deployments":[{"id":"11111111-1111-4111-8111-111111111111","name":"Read-only source"}]}`)
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	got, e := loadStatusConfiguration(statusOperatorOptions{operatorConfig: path})
	if e != nil || got.databaseURLFile != "/private/nonexistent-operator-only-database-url" || len(got.deployments) != 1 {
		t.Fatalf("public configuration tried to load other material: %+v %v", got, e)
	}
	var out bytes.Buffer
	if e = runStatusOperator([]string{"--operator-config", path}, &out); e == nil || out.Len() != 0 || strings.Contains(e.Error(), "nonexistent") {
		t.Fatal("missing DB material emitted output/path", e)
	}
}
