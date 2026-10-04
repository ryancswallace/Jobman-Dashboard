package notifications

import (
	"errors"
	"testing"
	"time"
)

func TestEvaluationSourceAndAuthorizationFenceBounds(t *testing.T) {
	now := fixtureTime()
	f := SourceFence{Checkpoint: fixtureCheckpoint(), FeedGeneration: 3, ConfigurationRevision: 1, VerifiedAt: now}
	if f.Validate(now) != nil {
		t.Fatal("valid source rejected")
	}
	advanced := f
	advanced.FeedGeneration++
	advanced.Checkpoint.HeadCursor = "later"
	if !f.SameSource(advanced) {
		t.Fatal("normal ingestion changed identity")
	}
	advanced.Checkpoint.RecoveryEpoch = "9007199254740994"
	if f.SameSource(advanced) {
		t.Fatal("epoch was not fenced")
	}
	for _, delta := range []time.Duration{-31 * time.Second, 6 * time.Second} {
		bad := f
		bad.VerifiedAt = now.Add(delta)
		if !errors.Is(bad.Validate(now), ErrEvaluationSource) {
			t.Fatal("stale/future proof accepted")
		}
	}
	p := AuthorizationProof{AccountID: fixtureID(9, 1), Namespace: fixtureRef(1, 1), ControlInstanceID: fixtureID(3, 1), RecoveryEpoch: fixtureCheckpoint().RecoveryEpoch, PrincipalID: fixtureID(7, 1), Version: "directory-proof-1", CheckedAt: now, ExpiresAt: now.Add(120 * time.Second)}
	if p.Validate(now) != nil {
		t.Fatal("valid authorization rejected")
	}
	for _, change := range []func(*AuthorizationProof){func(p *AuthorizationProof) { p.ExpiresAt = now }, func(p *AuthorizationProof) { p.ExpiresAt = now.Add(121 * time.Second) }, func(p *AuthorizationProof) { p.PrincipalID = "" }, func(p *AuthorizationProof) { p.AccountID = "" }, func(p *AuthorizationProof) { p.Namespace = NamespaceRef{} }} {
		bad := p
		change(&bad)
		if bad.Validate(now) == nil {
			t.Fatal("unbounded or unbound authorization proof accepted")
		}
	}
}
func TestEvaluationBackoffDoesNotAbandonUnavailableAccounts(t *testing.T) {
	for _, attempt := range []int64{-1, 0, 1, 2, 7, 100, 1 << 62} {
		delay := EvaluationRetryDelay(attempt)
		if delay < 5*time.Second || delay > 5*time.Minute {
			t.Fatal("retry outside bounds", attempt, delay)
		}
	}
	if EvaluationRetryDelay(100) != 5*time.Minute {
		t.Fatal("large retry count overflowed")
	}
}
