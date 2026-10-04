package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"time"
)

const HelperArgument = "--isolated-chunk-read"
const maxHelperOutput = 2 * MaxChunkBytes

// ProcessReader bounds live AND killed-but-not-yet-reaped children. A timed-out
// hard NFS mount keeps its slot occupied until the OS releases that process;
// repeated requests therefore cannot spawn unbounded blocked processes.
type ProcessReader struct {
	executable string
	arguments  []string
	gate       chan struct{}
	timeout    time.Duration
}

func NewProcessReader(executable string, concurrency int, timeout time.Duration) (*ProcessReader, error) {
	if !filepath.IsAbs(executable) || concurrency < 1 || concurrency > 16 || timeout <= 0 || timeout > 10*time.Second {
		return nil, errors.New("configure an absolute broker executable, 1–16 readers and at most 10 seconds per read")
	}
	return &ProcessReader{executable: executable, arguments: []string{HelperArgument}, gate: make(chan struct{}, concurrency), timeout: timeout}, nil
}

func (p *ProcessReader) Read(ctx context.Context, request FileRequest) FileResult {
	if !validFileRequest(request) {
		return FileResult{State: "invalid_manifest"}
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	select {
	case <-ctx.Done():
		return FileResult{State: "read_timeout"}
	case p.gate <- struct{}{}:
	default:
		return FileResult{State: "reader_busy"}
	}
	input, _ := json.Marshal(request)
	cmd := exec.Command(p.executable, p.arguments...)
	// The helper requires no credentials, home directory, inherited proxy, or
	// general environment. Its fixed executable and arguments never use a shell.
	cmd.Env = []string{"LANG=C"}
	cmd.Stdin = bytes.NewReader(input)
	output := &limitedBuffer{remaining: maxHelperOutput}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		<-p.gate
		return FileResult{State: "reader_unavailable"}
	}
	done := make(chan error, 1)
	go func() { err := cmd.Wait(); <-p.gate; done <- err }()
	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return FileResult{State: "read_timeout"}
	case err := <-done:
		if err != nil || ctx.Err() != nil {
			return FileResult{State: "reader_unavailable"}
		}
		var result FileResult
		if json.Unmarshal(output.Bytes(), &result) != nil || len(result.Bytes) > MaxChunkBytes {
			return FileResult{State: "reader_unavailable"}
		}
		switch result.State {
		case "ok":
			if int64(len(result.Bytes)) != request.ByteLength {
				return FileResult{State: "chunk_corrupt"}
			}
		case "chunk_missing", "chunk_corrupt", "mapping_inaccessible", "invalid_manifest":
			result.Bytes = nil
		default:
			return FileResult{State: "reader_unavailable"}
		}
		return result
	}
}

// A named buffer prevents io.Copy from bypassing Write through a promoted
// bytes.Buffer.ReadFrom method when exec.Cmd copies the helper output.
type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
}

func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.remaining {
		return 0, errors.New("reader output exceeds bound")
	}
	b.remaining -= len(p)
	return b.buffer.Write(p)
}

// RunHelper handles exactly one bounded private stdin request, writes one result
// and exits. A listening broker must not continue after handling this argument.
func RunHelper(in io.Reader, out io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(in, 8193))
	if err != nil || len(raw) > 8192 {
		return errors.New("invalid reader request")
	}
	var request FileRequest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil {
		return errors.New("invalid reader request")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("invalid reader request")
	}
	return json.NewEncoder(out).Encode(readFile(request))
}
