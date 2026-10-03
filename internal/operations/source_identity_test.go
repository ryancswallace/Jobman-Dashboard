//go:build linux || darwin

package operations

import (
	"os"
	"path/filepath"
	"testing"
)

const deployment = "11111111-1111-4111-8111-111111111111"
const instance = "22222222-2222-4222-8222-222222222222"

func TestBrokerIdentityPersistsAndNeverRollsBackward(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSourceIdentities(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSourceIdentities(dir); err == nil {
		t.Fatal("concurrent broker acquired same state")
	}
	if err = s.Verify(t.Context(), deployment, instance, "9", 1); err != nil {
		t.Fatal(err)
	}
	if err = s.Verify(t.Context(), deployment, instance, "10", 2); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenSourceIdentities(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Verify(t.Context(), deployment, instance, "10", 2); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		instance, epoch string
		revision        int64
	}{{instance, "9", 2}, {instance, "10", 1}, {"33333333-3333-4333-8333-333333333333", "10", 2}, {instance, "010", 2}} {
		if err = s.Verify(t.Context(), deployment, bad.instance, bad.epoch, bad.revision); err == nil {
			t.Fatal("identity rollback accepted")
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "source-identities.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("ledger not private")
	}
}

func TestBrokerIdentityRejectsCorruptOrLinkedState(t *testing.T) {
	for _, value := range []string{"null", "[]", "{", `{"invalid":{"instance":"invalid","epoch":1,"revision":1}}`} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			_ = os.Chmod(dir, 0700)
			if err := os.WriteFile(filepath.Join(dir, "source-identities.json"), []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
			if s, err := OpenSourceIdentities(dir); err == nil {
				s.Close()
				t.Fatal("corrupt ledger accepted")
			}
		})
	}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	if err := os.WriteFile(filepath.Join(dir, "another"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("another", filepath.Join(dir, "source-identities.json")); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenSourceIdentities(dir); err == nil {
		s.Close()
		t.Fatal("linked ledger accepted")
	}
}
