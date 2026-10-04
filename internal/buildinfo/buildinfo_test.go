package buildinfo

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("unavailable") }

func TestBuildInformationIsStandaloneJSON(t *testing.T) {
	var out bytes.Buffer
	if err := Write(&out); err != nil {
		t.Fatal(err)
	}
	var info Info
	if err := json.Unmarshal(out.Bytes(), &info); err != nil || info.FormatVersion != 1 || info.Version == "" || info.GoVersion == "" || info.OS == "" || info.Architecture == "" || info.Dependencies == nil {
		t.Fatalf("invalid build metadata: %v", err)
	}
	if Write(failedWriter{}) == nil {
		t.Fatal("lost output error")
	}
}
