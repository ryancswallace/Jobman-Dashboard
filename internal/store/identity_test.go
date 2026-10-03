package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/auth"
)

func TestVerifiedAliasesSessionsAndSourcePins(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	i := auth.Identity{Issuer: "https://synthetic.example/adfs", Subject: "browser", DirectoryID: "44444444-4444-4444-8444-444444444444", DisplayName: "Alice"}
	a, err := s.ResolveIdentity(ctx, i)
	if err != nil {
		t.Fatal(err)
	}
	i.Subject = "native"
	b, err := s.ResolveIdentity(ctx, i)
	if err != nil || b.Account.ID != a.Account.ID {
		t.Fatalf("verified aliases split account: %+v %v", b, err)
	}
	i.DirectoryID = "55555555-5555-4555-8555-555555555555"
	if _, err := s.ResolveIdentity(ctx, i); !errors.Is(err, auth.ErrIdentityConflict) {
		t.Fatalf("alias remapped: %v", err)
	}
	var now time.Time
	if err := s.Pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	token := sha256.Sum256([]byte("synthetic-session"))
	csrf := sha256.Sum256([]byte("synthetic-csrf"))
	session := auth.Session{TokenHash: token[:], CSRFHash: csrf[:], Actor: a, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Session(ctx, token[:]); err != nil || got.Actor.Subject != "browser" || got.Actor.DirectoryID != a.DirectoryID {
		t.Fatalf("session identity: %+v %v", got, err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_sessions SET touched_at=clock_timestamp()-interval '31 minutes'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, token[:]); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("idle session survived: %v", err)
	}
	token2 := sha256.Sum256([]byte("synthetic-session-two"))
	session.TokenHash = token2[:]
	if err := s.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, token2[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, token2[:]); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("revoked session survived")
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_accounts SET disabled_at=clock_timestamp() WHERE id=$1::uuid`, a.Account.ID); err != nil {
		t.Fatal(err)
	}
	i.DirectoryID = a.DirectoryID
	if _, err := s.ResolveIdentity(ctx, i); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("disabled account signed in")
	}
	const deployment = "11111111-1111-4111-8111-111111111111"
	const instance = "22222222-2222-4222-8222-222222222222"
	if err := s.VerifySourceIdentity(ctx, deployment, instance, "1", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifySourceIdentity(ctx, deployment, instance, "2", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifySourceIdentity(ctx, deployment, a.DirectoryID, "1", 3); err == nil {
		t.Fatal("replacement Control reused deployment ID")
	}
	if err := s.VerifySourceIdentity(ctx, deployment, instance, "1", 1); err == nil {
		t.Fatal("registry revision rolled backward")
	}
	for _, revision := range []int64{2, 3} {
		if err := s.VerifySourceIdentity(ctx, deployment, instance, "1", revision); err == nil {
			t.Fatal("source recovery epoch rolled backward")
		}
	}
	if err := s.VerifySourceIdentity(ctx, deployment, instance, "10", 3); err != nil {
		t.Fatalf("increasing epoch was compared lexicographically: %v", err)
	}
	if err := s.VerifySourceIdentity(ctx, deployment, instance, "9", 4); err == nil {
		t.Fatal("lexicographically greater but numerically lower epoch accepted")
	}
	if err := s.VerifySourceIdentity(ctx, deployment, instance, "10", 3); err != nil {
		t.Fatalf("same-epoch revalidation rejected: %v", err)
	}
}

func TestSourceEpochRequiresCanonicalPositiveInt64BeforeStorage(t *testing.T) {
	s := &Store{} // No pool: invalid input must never reach a database call.
	for _, epoch := range []string{"", " ", "0", "-1", "+1", "01", "1.0", "1e1", "9223372036854775808"} {
		if err := s.VerifySourceIdentity(context.Background(), "unused", "unused", epoch, 1); err == nil {
			t.Fatalf("invalid recovery epoch %q accepted", epoch)
		}
	}
}
func TestLoginStateConsumedOnceAndExpiredStateRemoved(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	var now time.Time
	if err := s.Pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("synthetic-state"))
	if err := s.PutLogin(ctx, hash[:], []byte("encrypted-test-payload"), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ConsumeLogin(ctx, hash[:]); err != nil || string(got) != "encrypted-test-payload" {
		t.Fatalf("consume: %q %v", got, err)
	}
	if _, err := s.ConsumeLogin(ctx, hash[:]); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("login state replayed")
	}
	if err := s.PutLogin(ctx, hash[:], []byte("encrypted-test-payload"), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_login_attempts SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeLogin(ctx, hash[:]); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("expired state consumed")
	}
}
