package main

import (
	"bytes"
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
