// Package logs delivers only immutable chunks named by an authorized Control
// manifest. Storage permission alone never establishes namespace permission.
package logs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"
)

const MaxChunkBytes = 256 << 10

type FileRequest struct {
	Root       string `json:"root"`
	ObjectKey  string `json:"objectKey"`
	ByteLength int64  `json:"byteLength"`
	Checksum   string `json:"checksum"`
}

// FileResult contains safe state codes only; filesystem paths and OS errors
// must not cross the broker boundary or appear in operational logs.
type FileResult struct {
	Bytes []byte `json:"bytes,omitempty"`
	State string `json:"state"`
}

type Reader interface {
	Read(context.Context, FileRequest) FileResult
}

func validFileRequest(r FileRequest) bool {
	if r.Root == "" || !strings.HasPrefix(r.Root, "/") || path.Clean(r.Root) != r.Root || len(r.Root) > 4096 || strings.ContainsAny(r.Root, "\x00\r\n") {
		return false
	}
	if r.ObjectKey == "" || len(r.ObjectKey) > 1024 || strings.HasPrefix(r.ObjectKey, "/") || path.Clean(r.ObjectKey) != r.ObjectKey || strings.ContainsAny(r.ObjectKey, "\\:\x00\r\n") {
		return false
	}
	for _, component := range strings.Split(r.ObjectKey, "/") {
		if component == ".." || component == "." || component == "" {
			return false
		}
	}
	if r.ByteLength < 0 || r.ByteLength > MaxChunkBytes || len(r.Checksum) != 71 || !strings.HasPrefix(r.Checksum, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(r.Checksum[7:])
	return err == nil && strings.ToLower(r.Checksum) == r.Checksum
}

// readFile is called ONLY inside an isolated child process in production. Even
// opening/statting an NFS object may enter an uninterruptible kernel wait.
func readFile(r FileRequest) FileResult {
	if !validFileRequest(r) {
		return FileResult{State: "invalid_manifest"}
	}
	f, err := openRelative(r.Root, r.ObjectKey)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileResult{State: "chunk_missing"}
		}
		return FileResult{State: "mapping_inaccessible"}
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return FileResult{State: "mapping_inaccessible"}
	}
	if before.Size() != r.ByteLength {
		return FileResult{State: "chunk_corrupt"}
	}
	data, err := io.ReadAll(io.LimitReader(f, r.ByteLength+1))
	if err != nil || int64(len(data)) != r.ByteLength {
		return FileResult{State: "chunk_corrupt"}
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return FileResult{State: "chunk_corrupt"}
	}
	sum := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(sum[:]) != r.Checksum {
		return FileResult{State: "chunk_corrupt"}
	}
	return FileResult{Bytes: data, State: "ok"}
}
