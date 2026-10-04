//go:build unix

package reports

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadOnlyObjectRejectsNonregularAndReplacedFilesWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "objects")
	writer, err := OpenObjects(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	pair := fixturePair(t)
	object, err := writer.Put(t.Context(), "90000000-0000-4000-8000-000000000034", pair)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenObjectReader(path, ObjectAccess{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	name := filepath.Join(path, object.ID+".json")
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(name, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := reader.Read(t.Context(), pair.Subject, object); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO blocked reader")
	}
	// Even if Lstat raced with replacement, the final open itself is nonblocking.
	file, err := openObjectNoFollow(writer.root, object.ID+".json")
	if err == nil {
		defer file.Close()
		if validateObjectAccess(file, ObjectAccess{}, false, false) == nil {
			t.Fatal("opened FIFO accepted")
		}
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", name); err != nil {
		t.Fatal(err)
	}
	if _, err := openObjectNoFollow(writer.root, object.ID+".json"); err == nil {
		t.Fatal("final symlink followed")
	}
}
