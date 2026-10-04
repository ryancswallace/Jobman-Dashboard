package reports

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/observability"
)

func TestObjectObservationReportsSealedReadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "objects")
	s, err := OpenObjects(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := observability.NewRegistry("worker", 1, nil, observability.Build{})
	if err != nil {
		t.Fatal(err)
	}
	s.SetObserver(r)
	pair := fixturePair(t)
	id := "90000000-0000-4000-8000-000000000001"
	object, err := s.Put(t.Context(), id, pair)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenObjectReader(path, ObjectAccess{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.SetObserver(r)
	if err = os.WriteFile(filepath.Join(path, id+".json"), []byte("private-corrupt-pair"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Read(t.Context(), pair.Subject, object); err == nil {
		t.Fatal("corrupt object accepted")
	}
	raw := string(r.Render())
	if !strings.Contains(raw, `component="object",operation="read",deployment="",mode="",outcome="failed"} 1`) || strings.Contains(raw, id) || strings.Contains(raw, "private-corrupt-pair") {
		t.Fatal(raw)
	}
}
