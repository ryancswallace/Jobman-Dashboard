//go:build unix

package config

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestSecretFIFOIsRejectedWithoutWaitingForAWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret(path, 4096); err == nil {
		t.Fatal("named pipe was accepted as a secret file")
	}
}
