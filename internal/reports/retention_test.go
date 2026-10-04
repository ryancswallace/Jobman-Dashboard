package reports

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestObjectSweepIsIncrementalAndPreservesReferencesAndRecentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "objects")
	s, err := OpenObjects(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := time.Now().Add(-2 * time.Hour)
	const keep = "90000000-0000-4000-8000-000000000000"
	const recent = "90000000-0000-4000-8000-000000000999"
	write := func(name string, age bool) {
		t.Helper()
		name = filepath.Join(path, name)
		if err := os.WriteFile(name, []byte("synthetic orphan"), 0600); err != nil {
			t.Fatal(err)
		}
		if age {
			if err := os.Chtimes(name, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := range 205 {
		write(fmt.Sprintf("90000000-0000-4000-8000-%012d.json", i), true)
	}
	write(recent+".json", false)
	write(".pending-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true)
	write(".pending-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", false)
	write("operator-file", true)
	lookups := 0
	refs := func(_ context.Context, id string) (bool, error) { lookups++; return id == keep, nil }
	if err = s.Sweep(t.Context(), refs); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) < 109 || lookups > 100 {
		t.Fatal("sweep did not bound its pass")
	}
	for range 5 {
		if err = s.Sweep(t.Context(), refs); err != nil {
			t.Fatal(err)
		}
	}
	entries, err = os.ReadDir(path)
	if err != nil || len(entries) != 4 {
		t.Fatalf("wrong retained objects %d %v", len(entries), err)
	}
	for _, name := range []string{keep + ".json", recent + ".json", ".pending-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "operator-file"} {
		if _, err = os.Stat(filepath.Join(path, name)); err != nil {
			t.Fatal("referenced or recent file deleted")
		}
	}
	write("90000000-0000-4000-8000-000000000888.json", true)
	for range 3 {
		_ = s.Sweep(t.Context(), func(context.Context, string) (bool, error) { return false, ErrInvalid })
	}
	if _, err = os.Stat(filepath.Join(path, "90000000-0000-4000-8000-000000000888.json")); err != nil {
		t.Fatal("database failure deleted object")
	}
}
