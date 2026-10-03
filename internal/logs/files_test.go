//go:build linux || darwin

package logs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func fileFixture(t *testing.T, content []byte) FileRequest {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "chunks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "chunks", "one"), content, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	return FileRequest{Root: root, ObjectKey: "chunks/one", ByteLength: int64(len(content)), Checksum: "sha256:" + hex.EncodeToString(sum[:])}
}

func TestExactImmutableFileRead(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("synthetic\x00\xff\n"), bytes.Repeat([]byte("x"), MaxChunkBytes)} {
		r := fileFixture(t, data)
		got := readFile(r)
		if got.State != "ok" || !bytes.Equal(got.Bytes, data) {
			t.Fatalf("read: %s %d", got.State, len(got.Bytes))
		}
	}
}

func TestFileBoundaryRejectsTraversalAndCorruption(t *testing.T) {
	r := fileFixture(t, []byte("synthetic"))
	for _, key := range []string{"../chunks/one", "chunks/../../one", "/chunks/one", "chunks//one", "chunks/./one", "chunks/one/..", "chunks\\one", "https:object", "chunks/one\x00"} {
		bad := r
		bad.ObjectKey = key
		if got := readFile(bad); got.State != "invalid_manifest" || len(got.Bytes) != 0 {
			t.Errorf("accepted key %q", key)
		}
	}
	for _, change := range []func(*FileRequest){func(r *FileRequest) { r.ByteLength++ }, func(r *FileRequest) { r.Checksum = "sha256:" + strings.Repeat("0", 64) }} {
		bad := r
		change(&bad)
		if got := readFile(bad); got.State != "chunk_corrupt" || len(got.Bytes) != 0 {
			t.Fatalf("corrupt: %+v", got)
		}
	}
	bad := r
	bad.ByteLength = MaxChunkBytes + 1
	if readFile(bad).State != "invalid_manifest" {
		t.Fatal("oversized read accepted")
	}
}

func TestNoSymlinkOrNonregularReads(t *testing.T) {
	r := fileFixture(t, []byte("synthetic"))
	for _, c := range []struct{ link, target, key string }{{"file-link", "chunks/one", "file-link"}, {"dir-link", "chunks", "dir-link/one"}} {
		if err := os.Symlink(c.target, filepath.Join(r.Root, c.link)); err != nil {
			t.Fatal(err)
		}
		bad := r
		bad.ObjectKey = c.key
		if got := readFile(bad); got.State != "mapping_inaccessible" || len(got.Bytes) != 0 {
			t.Fatalf("followed symlink: %s", got.State)
		}
	}
	if err := unix.Mkfifo(filepath.Join(r.Root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := r
	bad.ObjectKey = "pipe"
	start := time.Now()
	got := readFile(bad)
	if got.State != "mapping_inaccessible" || time.Since(start) > time.Second {
		t.Fatal("FIFO blocked or was accepted")
	}
	bad.ObjectKey = "chunks"
	if readFile(bad).State != "mapping_inaccessible" {
		t.Fatal("directory accepted")
	}
	bad.ObjectKey = "missing"
	if readFile(bad).State != "chunk_missing" {
		t.Fatal("missing file conflated")
	}
	parent := filepath.Dir(r.Root)
	link := filepath.Join(parent, "linked-root")
	if err := os.Symlink(r.Root, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	bad = r
	bad.Root = link
	if readFile(bad).State != "mapping_inaccessible" {
		t.Fatal("symlink root accepted")
	}
}

func TestReaderHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != HelperArgument {
		return
	}
	if err := RunHelper(os.Stdin, os.Stdout); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestReaderStallHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "--stall-reader" {
		return
	}
	time.Sleep(time.Hour)
	os.Exit(1)
}

func TestProcessReadAndCapacity(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProcessReader(executable, 1, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p.arguments = []string{"-test.run=^TestReaderHelper$", "--", HelperArgument}
	r := fileFixture(t, []byte("synthetic"))
	if got := p.Read(t.Context(), r); got.State != "ok" || string(got.Bytes) != "synthetic" {
		t.Fatalf("helper %s", got.State)
	}
	p.gate <- struct{}{}
	if got := p.Read(t.Context(), r); got.State != "reader_busy" {
		t.Fatalf("capacity %s", got.State)
	}
	<-p.gate
	p.arguments = []string{"-test.run=^TestReaderStallHelper$", "--", "--stall-reader"}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	got := p.Read(ctx, r)
	if got.State != "read_timeout" || time.Since(start) > time.Second {
		t.Fatalf("stalled helper %s", got.State)
	}
	deadline := time.Now().Add(time.Second)
	for len(p.gate) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(p.gate) != 0 {
		t.Fatal("killed child not reaped")
	}
}

func TestHelperRejectsExtraAndOversizedInput(t *testing.T) {
	for _, input := range []string{`{} {}`, `{"root":"/", "override":true}`, strings.Repeat("x", 8193)} {
		var out bytes.Buffer
		if err := RunHelper(strings.NewReader(input), &out); err == nil || out.Len() != 0 {
			t.Fatal("invalid helper request accepted")
		}
	}
}
