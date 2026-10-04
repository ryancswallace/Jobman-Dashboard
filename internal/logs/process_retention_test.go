//go:build linux || darwin

package logs

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The short-lived descendant holds the actual exec output pipe open after its
// parent has been killed. This exercises a pending Cmd.Wait, rather than filling
// the semaphore by hand or pretending a normal process is unkillable NFS I/O.
func TestReaderHeldOutputHelper(t *testing.T) {
	if len(os.Args) < 2 {
		return
	}
	if os.Args[len(os.Args)-2] == "--hold-reader-pipe" {
		marker := os.Args[len(os.Args)-1]
		if !filepath.IsAbs(marker) || filepath.Base(marker) != "reader-pipe-ready" {
			os.Exit(2)
		}
		if err := os.WriteFile(marker, []byte("synthetic pipe holder"), 0600); err != nil {
			os.Exit(3)
		}
		time.Sleep(time.Second)
		os.Exit(0)
	}
	if os.Args[len(os.Args)-1] != "--start-reader-pipe-holder" {
		return
	}
	var request FileRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 8192)).Decode(&request) != nil || !validFileRequest(request) {
		os.Exit(4)
	}
	self, err := os.Executable()
	if err != nil {
		os.Exit(5)
	}
	child := exec.Command(self, "-test.run=^TestReaderHeldOutputHelper$", "--", "--hold-reader-pipe", filepath.Join(request.Root, "reader-pipe-ready"))
	child.Env = []string{"LANG=C"}
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if child.Start() != nil {
		os.Exit(6)
	}
	// The parent test cancels after the descendant's acknowledgement. This
	// fallback is bounded even if its controller fails before cancellation.
	time.Sleep(5 * time.Second)
	os.Exit(7)
}

func TestProcessRetainsCapacityUntilWaitCompletes(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewProcessReader(self, 1, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reader.arguments = []string{"-test.run=^TestReaderHeldOutputHelper$", "--", "--start-reader-pipe-holder"}
	request := fileFixture(t, []byte("synthetic retained capacity"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan FileResult, 1)
	go func() { result <- reader.Read(ctx, request) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(request.Root, "reader-pipe-ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bounded output-pipe holder did not acknowledge startup")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case value := <-result:
		if value.State != "read_timeout" || len(value.Bytes) != 0 {
			t.Fatal("cancelled read exposed bytes or failed to return its timeout")
		}
	case <-time.After(time.Second):
		t.Fatal("read cancellation waited for pipe-holder completion")
	}
	second, stop := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer stop()
	if value := reader.Read(second, request); value.State != "reader_busy" || len(value.Bytes) != 0 {
		t.Fatal("reader capacity was released before actual Cmd.Wait completed")
	}
	// Race-instrumented os.Exit intentionally waits before exit; allow its
	// diagnostic drain as well as the explicit one-second pipe hold.
	deadline = time.Now().Add(4 * time.Second)
	for len(reader.gate) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(reader.gate) != 0 {
		t.Fatal("bounded pipe holder ended without returning capacity")
	}
	reader.arguments = []string{"-test.run=^TestReaderHelper$", "--", HelperArgument}
	if value := reader.Read(t.Context(), request); value.State != "ok" || strings.TrimSpace(string(value.Bytes)) != "synthetic retained capacity" {
		t.Fatal("reaped reader did not admit a subsequent genuine file read")
	}
}
