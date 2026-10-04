//go:build integration

package auth

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestLabBoundedOutputHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--lab-bounded-output" {
		return
	}
	size, err := strconv.Atoi(os.Args[len(os.Args)-1])
	if err != nil || size < 1 || size > 32769 {
		os.Exit(2)
	}
	if _, err := os.Stdout.Write(bytes.Repeat([]byte{'x'}, size)); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

// Exercise exec.Cmd's real pipe-copy path: a promoted bytes.Buffer.ReadFrom
// bypassed these wrappers' Write limits even though direct Write tests passed.
func TestLabSubprocessOutputBounds(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("self-executable unavailable")
	}
	type output interface {
		io.Writer
		Bytes() []byte
		Len() int
	}
	for _, test := range []struct {
		name       string
		limit      int
		makeOutput func() output
	}{
		{"notification", 8192, func() output { return &labBoundedNotificationOutput{} }},
		{"execution", 32768, func() output { return &labExecutionOutput{} }},
		{"rotation", 8192, func() output { return &labRotationOutput{maximum: 8192} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, size := range []int{test.limit, test.limit + 1} {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				command := exec.CommandContext(ctx, executable, "-test.run=^TestLabBoundedOutputHelper$", "--", "--lab-bounded-output", strconv.Itoa(size))
				out := test.makeOutput()
				command.Stdout, command.Stderr = out, io.Discard
				err := command.Run()
				cancel()
				if ctx.Err() == context.DeadlineExceeded {
					t.Fatal("synthetic output child exceeded its deadline")
				}
				if size == test.limit {
					if err != nil || out.Len() != size || !bytes.Equal(out.Bytes(), bytes.Repeat([]byte{'x'}, size)) {
						t.Fatal("bounded subprocess output changed")
					}
				} else {
					if err == nil || out.Len() > test.limit {
						t.Fatal("subprocess copy bypassed the output bound")
					}
					if notification, ok := out.(*labBoundedNotificationOutput); ok {
						var diagnostic labBoundedNotificationOutput
						failure := labMultiNotificationResult(err, notification, &diagnostic, &labNotificationEvent{})
						if !notification.overflow || failure == nil || failure.Error() != "scenario output_limit; retain immutable and pending receipts" {
							t.Fatal("overflow escaped the finite notification diagnostic")
						}
					}
				}
			}
		})
	}
}
