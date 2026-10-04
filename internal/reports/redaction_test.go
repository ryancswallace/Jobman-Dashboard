package reports

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ryancswallace/jobman/diagnostic"
)

func testRedactionPolicy(t *testing.T, config RedactionConfig) *RedactionPolicy {
	t.Helper()
	policy, err := NewRedactionPolicy(config, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestRedactionMasksWholeMultilineAndOverlappingValuesWithoutChangingOffsets(t *testing.T) {
	policy := testRedactionPolicy(t, RedactionConfig{Values: []string{"secret\nvalue", "ababa", "babab"}, Patterns: []string{`(?s)BEGIN\n.*?\nEND`, `token=[a-z]+`}})
	raw := []byte("prefix secret\nvalue ababab BEGIN\none\ntwo\nEND token=private suffix")
	original := bytes.Clone(raw)
	got, changed := policy.Sanitize("shared.log.stderr", raw)
	want := "prefix ************ ****** ***************** ************* suffix"
	if !changed || string(got) != want || len(got) != len(raw) || !bytes.Equal(raw, original) {
		t.Fatalf("redacted shape=%q, changed=%v", got, changed)
	}
	for _, sensitive := range []string{"secret", "value", "ababa", "one", "two", "private"} {
		if bytes.Contains(got, []byte(sensitive)) {
			t.Fatal("configured sensitive bytes remain")
		}
	}
	// One rule must still see original text masked by another rule.
	overlap := testRedactionPolicy(t, RedactionConfig{Patterns: []string{"abc", "bcd"}})
	if got, _ = overlap.Sanitize("shared.log.stdout", []byte("abcd")); string(got) != "****" {
		t.Fatal("sequential replacements hid overlapping matches")
	}
}

func TestRedactionMasksLiteralFragmentsAtBoundedTailEdges(t *testing.T) {
	policy := testRedactionPolicy(t, RedactionConfig{Values: []string{"private-secret", "秘密", "x"}})
	for _, sample := range []struct{ input, want string }{
		{"secret safe priv", "****** safe ****"}, {"safe private-secret", "safe **************"},
		{"safe\nx", "safe\n*"}, {"秘密 safe", "****** safe"}, {"unchanged", "unchanged"}, {"", ""},
	} {
		got, changed := policy.Sanitize("shared.log.stdout", []byte(sample.input))
		if string(got) != sample.want || changed != (sample.want != sample.input) {
			t.Fatalf("boundary shape input=%q output=%q", sample.input, got)
		}
	}
	// Repetitive long literals use a linear boundary scan, not quadratic suffix
	// comparisons; every possible cropped tail still loses the sensitive bytes.
	repeated := testRedactionPolicy(t, RedactionConfig{Values: []string{strings.Repeat("a", 4096)}})
	got, changed := repeated.Sanitize("shared.log.stdout", []byte(strings.Repeat("a", 4095)))
	if !changed || string(got) != strings.Repeat("*", 4095) {
		t.Fatal("cropped repeated literal was exposed")
	}
}

func TestRedactionPolicyFingerprintIsCanonicalKeyedAndPrivate(t *testing.T) {
	config := RedactionConfig{Values: []string{"second", "first", "first"}, Patterns: []string{"[0-9]+", "password=[a-z]+"}}
	policy := testRedactionPolicy(t, config)
	canonical := testRedactionPolicy(t, RedactionConfig{Values: []string{"first", "second"}, Patterns: []string{"password=[a-z]+", "[0-9]+"}})
	if policy.Fingerprint() != canonical.Fingerprint() || !digestPattern.MatchString(policy.Fingerprint()) {
		t.Fatal("equivalent configurations changed policy identity")
	}
	nilValues := testRedactionPolicy(t, RedactionConfig{Patterns: []string{"secret"}})
	emptyValues := testRedactionPolicy(t, RedactionConfig{Values: []string{}, Patterns: []string{"secret"}})
	if nilValues.Fingerprint() != emptyValues.Fingerprint() {
		t.Fatal("equivalent empty collections changed policy identity")
	}
	otherKey, err := NewRedactionPolicy(config, bytes.Repeat([]byte{0x43}, 32))
	if err != nil || otherKey.Fingerprint() == policy.Fingerprint() {
		t.Fatal("policy fingerprint is not keyed")
	}
	changed := testRedactionPolicy(t, RedactionConfig{Values: []string{"first", "different"}, Patterns: config.Patterns})
	if changed.Fingerprint() == policy.Fingerprint() {
		t.Fatal("policy changes did not invalidate fingerprint")
	}
	config.Values[0], config.Patterns[0] = "mutation", "mutation"
	got, _ := policy.Sanitize("shared.log.stdout", []byte("first second 123"))
	if string(got) != "***** ****** ***" {
		t.Fatal("caller mutated compiled policy")
	}
}

func TestRedactionRejectsUnboundedUnsupportedAndEmptyMatchConfiguration(t *testing.T) {
	invalid := []RedactionConfig{
		{}, {Values: []string{""}}, {Values: []string{string([]byte{0xff})}},
		{Values: []string{strings.Repeat("v", maximumRedactionEntryBytes+1)}},
		{Values: make([]string, maximumRedactionValues+1)}, {Patterns: make([]string, maximumRedactionPatterns+1)},
		{Patterns: []string{"("}}, {Patterns: []string{`a(?=b)`}}, {Patterns: []string{`(a)\1`}},
		{Patterns: []string{`a*`}}, {Patterns: []string{`\b`}}, {Patterns: []string{`a|\b`}}, {Patterns: []string{`(?:abc)?`}},
		{Patterns: []string{strings.Repeat("a{1000}", 20)}},
	}
	oversized := RedactionConfig{}
	for range 33 {
		oversized.Values = append(oversized.Values, strings.Repeat("v", 4096))
	}
	invalid = append(invalid, oversized)
	for index, config := range invalid {
		if _, err := NewRedactionPolicy(config, bytes.Repeat([]byte{1}, 32)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid policy %d accepted", index)
		}
	}
	if _, err := NewRedactionPolicy(RedactionConfig{Values: []string{"sensitive-canary"}}, []byte("short")); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "sensitive-canary") {
		t.Fatal("short key accepted or private policy disclosed")
	}
	var missing *RedactionPolicy
	if missing.Fingerprint() != "" || missing.ValueRedactionConfigured() {
		t.Fatal("missing policy advertised as configured")
	}
	policy := testRedactionPolicy(t, RedactionConfig{Values: []string{"canary"}})
	for _, sample := range []struct {
		field string
		data  []byte
	}{{"source.path", []byte("canary")}, {"shared.log.stdout", make([]byte, diagnostic.SharedMaximumTailBytes+1)}} {
		got, changed := policy.Sanitize(sample.field, sample.data)
		if len(got) != 0 || !changed {
			t.Fatal("invalid sanitizer use returned unredacted content")
		}
	}
}

func TestRedactionPolicySupportsConcurrentUse(t *testing.T) {
	policy := testRedactionPolicy(t, RedactionConfig{Values: []string{"canary"}, Patterns: []string{`(?s)secret\nvalue`}})
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 25 {
				got, changed := policy.Sanitize("shared.log.stdout", []byte("canary secret\nvalue"))
				if !changed || string(got) != "****** ************" {
					t.Error("concurrent redaction changed")
				}
			}
		})
	}
	workers.Wait()
}
