package auth

import (
	"strings"
	"testing"
)

func TestDelegationModeCompatibilityAndExplicitWorker(t *testing.T) {
	for _, test := range []struct{ input, want DelegationMode }{{"", DelegationInteractive}, {DelegationInteractive, DelegationInteractive}, {DelegationWorker, DelegationWorker}} {
		got, err := test.input.Canonical()
		if err != nil || got != test.want {
			t.Fatal("trusted adapter mode differs", got, err)
		}
	}
}

func TestDelegationModeRejectsUnknownWithoutFallbackOrDisclosure(t *testing.T) {
	for _, input := range []DelegationMode{"Worker", " worker", "interactive\x00", "service", "private-input-canary"} {
		got, err := input.Canonical()
		if err == nil || got != "" || strings.Contains(err.Error(), string(input)) {
			t.Fatal("unknown mode accepted, downgraded or disclosed")
		}
	}
}
