package reports

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"regexp/syntax"
	"slices"
	"unicode/utf8"

	"github.com/ryancswallace/jobman/diagnostic"
)

const (
	maximumRedactionValues       = 128
	maximumRedactionPatterns     = 64
	maximumRedactionEntryBytes   = 4096
	maximumRedactionPolicyBytes  = 128 << 10
	maximumRedactionInstructions = 16384
	redactionPolicyVersion       = "jobman.dashboard.redaction/v1"
)

// RedactionConfig contains private operator-supplied literals and Go RE2
// patterns. Patterns match the whole bounded tail; (?s) enables dot/newline
// matching. Neither configuration nor matched bytes appear in errors/notices.
type RedactionConfig struct {
	Values   []string
	Patterns []string
}

// RedactionPolicy is immutable, safe for concurrent collection, and exposes no
// configuration accessor. Matches are unioned before replacement so a prior
// replacement cannot hide an overlapping sensitive value from a later rule.
type RedactionPolicy struct {
	values      [][]byte
	patterns    []*regexp.Regexp
	fingerprint string
}

// NewRedactionPolicy requires a stable, purpose-separated secret HMAC key from
// the operator's persistent key. A keyed fingerprint avoids publishing a
// dictionary-testable digest of potentially low-entropy sensitive values.
func NewRedactionPolicy(config RedactionConfig, fingerprintKey []byte) (*RedactionPolicy, error) {
	if len(fingerprintKey) < 32 || len(fingerprintKey) > 64 || len(config.Values) > maximumRedactionValues || len(config.Patterns) > maximumRedactionPatterns || len(config.Values)+len(config.Patterns) == 0 {
		return nil, ErrInvalid
	}
	total := 0
	for _, entries := range [][]string{config.Values, config.Patterns} {
		for _, value := range entries {
			total += len(value)
			if len(value) == 0 || len(value) > maximumRedactionEntryBytes || !utf8.ValidString(value) || total > maximumRedactionPolicyBytes {
				return nil, ErrInvalid
			}
		}
	}
	values, patterns := canonicalRedactionEntries(config.Values), canonicalRedactionEntries(config.Patterns)
	policy := &RedactionPolicy{}
	for _, value := range values {
		policy.values = append(policy.values, []byte(value))
	}
	instructions := 0
	for _, pattern := range patterns {
		parsed, err := syntax.Parse(pattern, syntax.Perl)
		if err != nil || !consumesRedactionText(parsed) {
			return nil, ErrInvalid
		}
		program, err := syntax.Compile(parsed.Simplify())
		if err != nil {
			return nil, ErrInvalid
		}
		instructions += len(program.Inst)
		if instructions > maximumRedactionInstructions {
			return nil, ErrInvalid
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, ErrInvalid
		}
		policy.patterns = append(policy.patterns, compiled)
	}
	encoded, err := json.Marshal(struct {
		Version          string
		Values, Patterns []string
	}{redactionPolicyVersion, values, patterns})
	if err != nil {
		return nil, ErrInvalid
	}
	mac := hmac.New(sha256.New, fingerprintKey)
	mac.Write(encoded)
	policy.fingerprint = hex.EncodeToString(mac.Sum(nil))
	return policy, nil
}

func canonicalRedactionEntries(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	result := slices.Clone(values)
	slices.Sort(result)
	return slices.Compact(result)
}

// consumesRedactionText rejects all regex branches that can match zero bytes,
// including context-dependent empty matches such as word boundaries.
func consumesRedactionText(expression *syntax.Regexp) bool {
	switch expression.Op {
	case syntax.OpLiteral:
		return len(expression.Rune) > 0
	case syntax.OpCharClass, syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		return true
	case syntax.OpCapture, syntax.OpPlus:
		return consumesRedactionText(expression.Sub[0])
	case syntax.OpRepeat:
		return expression.Min > 0 && consumesRedactionText(expression.Sub[0])
	case syntax.OpConcat:
		for _, child := range expression.Sub {
			if consumesRedactionText(child) {
				return true
			}
		}
	case syntax.OpAlternate:
		for _, child := range expression.Sub {
			if !consumesRedactionText(child) {
				return false
			}
		}
		return len(expression.Sub) > 0
	}
	return false
}

func (policy *RedactionPolicy) ValueRedactionConfigured() bool {
	return policy != nil && policy.fingerprint != "" && len(policy.values)+len(policy.patterns) > 0
}

func (policy *RedactionPolicy) Fingerprint() string {
	if !policy.ValueRedactionConfigured() {
		return ""
	}
	return policy.fingerprint
}

// Sanitize preserves original byte lengths/line breaks outside matched ranges.
// It accepts only shared log fields and bounded data. An invalid direct call
// fails closed; normal collection validates these invariants before invoking it.
func (policy *RedactionPolicy) Sanitize(field string, value []byte) ([]byte, bool) {
	if !policy.ValueRedactionConfigured() || (field != "shared.log.stdout" && field != "shared.log.stderr") || len(value) > diagnostic.SharedMaximumTailBytes {
		return nil, len(value) != 0
	}
	// Difference counters union intervals in O(matches + tail bytes), even for
	// heavily overlapping long literals. Counts stay below 128 * 65536 plus
	// bounded regex matches, safely inside int32.
	masked := make([]int32, len(value)+1)
	mark := func(start, end int) {
		masked[start]++
		masked[end]--
	}
	for _, literal := range policy.values {
		for offset := 0; offset < len(value); {
			index := bytes.Index(value[offset:], literal)
			if index < 0 {
				break
			}
			start := offset + index
			mark(start, start+len(literal))
			offset = start + 1 // Also mask overlapping occurrences.
		}
		// A bounded tail can start/end inside a configured value. Conservatively
		// mask matching boundary fragments instead of exposing a secret suffix.
		window := min(len(literal)-1, len(value))
		mark(0, matchingBoundary(value[:window], literal[1:]))
		length := matchingBoundary(literal, value[len(value)-window:])
		mark(len(value)-length, len(value))
	}
	for _, pattern := range policy.patterns {
		for _, span := range pattern.FindAllIndex(value, -1) {
			mark(span[0], span[1])
		}
	}
	result, changed := bytes.Clone(value), false
	var active int32
	for index := range result {
		active += masked[index]
		if active > 0 {
			changed = changed || result[index] != '*'
			result[index] = '*'
		}
	}
	return result, changed
}

// matchingBoundary returns the longest prefix of prefix that is a suffix of
// suffix using a linear prefix-function scan, bounding repetitive secret values.
func matchingBoundary(prefix, suffix []byte) int {
	if len(prefix) == 0 {
		return 0
	}
	failure := make([]int, len(prefix))
	for index, matched := 1, 0; index < len(prefix); index++ {
		for matched > 0 && prefix[index] != prefix[matched] {
			matched = failure[matched-1]
		}
		if prefix[index] == prefix[matched] {
			matched++
		}
		failure[index] = matched
	}
	matched := 0
	for _, character := range suffix {
		for matched > 0 && (matched == len(prefix) || character != prefix[matched]) {
			matched = failure[matched-1]
		}
		if character == prefix[matched] {
			matched++
		}
	}
	return matched
}

var _ diagnostic.Sanitizer = (*RedactionPolicy)(nil)
var _ diagnostic.ValueRedactionReporter = (*RedactionPolicy)(nil)
