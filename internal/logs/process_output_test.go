//go:build linux || darwin

package logs

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestProcessOversizeOutputHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "--oversize-reader-output" {
		return
	}
	// Trailing JSON whitespace used to bypass the byte cap, yet still decode
	// into a valid one-byte successful read after the entire payload was buffered.
	prefix := []byte(`{"state":"ok","bytes":"eA=="}`)
	value := append(prefix, bytes.Repeat([]byte{' '}, maxHelperOutput+1-len(prefix))...)
	if _, err := os.Stdout.Write(value); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestProcessOutputCapUsesActualPipeCopy(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("self-executable unavailable")
	}
	arguments := []string{"-test.run=^TestProcessOversizeOutputHelper$", "--", "--oversize-reader-output"}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, arguments...)
	output := &limitedBuffer{remaining: maxHelperOutput}
	command.Stdout, command.Stderr = output, io.Discard
	if err := command.Run(); err == nil || len(output.Bytes()) > maxHelperOutput || output.remaining < 0 {
		t.Fatal("actual subprocess copy bypassed the helper output cap")
	}
	if ctx.Err() != nil {
		t.Fatal("synthetic output child exceeded its deadline")
	}
	reader, err := NewProcessReader(executable, 1, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reader.arguments = arguments
	request := fileFixture(t, []byte("x"))
	if result := reader.Read(t.Context(), request); result.State != "reader_unavailable" || len(result.Bytes) != 0 {
		t.Fatal("oversized helper response was accepted or exposed")
	}
	reader.arguments = []string{"-test.run=^TestReaderHelper$", "--", HelperArgument}
	if result := reader.Read(t.Context(), request); result.State != "ok" || string(result.Bytes) != "x" {
		t.Fatal("failed output copy did not release reader capacity")
	}
}
