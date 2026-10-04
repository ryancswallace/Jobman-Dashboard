//go:build !linux && unix

package reports

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSharedObjectsRejectUnsupportedPlatformWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "objects")
	if err := os.Mkdir(path, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0750); err != nil {
		t.Fatal(err)
	}
	access := ObjectAccess{Mode: SharedGroup, WorkerUID: uint32(os.Geteuid()), ReaderGID: uint32(os.Getegid())}
	if _, err := OpenObjectsWithAccess(path, access); err == nil {
		t.Fatal("unsupported shared writer accepted")
	}
	if _, err := OpenObjectReader(path, access); err == nil {
		t.Fatal("unsupported shared reader accepted")
	}
	entries, _ := os.ReadDir(path)
	info, _ := os.Stat(path)
	if len(entries) != 0 || info.Mode().Perm() != 0750 {
		t.Fatal("rejected profile modified root")
	}
}
