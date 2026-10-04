//go:build linux || darwin

package observability

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateSocketRejectsExistingPathsAndCleansOnlyOwnInode(t *testing.T) {
	temporary, e := os.MkdirTemp("/tmp", "jd-obs-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(temporary) })
	base, e := filepath.EvalSymlinks(temporary)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(base, "operator.sock")
	options := Options{SocketPath: path, Registry: testRegistry(t), Ready: func(context.Context) error { return nil }}
	listener, e := Listen(t.Context(), options)
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Lstat(path)
	if e != nil || info.Mode().Perm() != 0600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatal("socket access mode", e)
	}
	listener.Server.Started()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
	defer client.CloseIdleConnections()
	response, e := client.Get("http://operator/readyz")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("bound process not ready")
	}
	if _, e = Listen(t.Context(), options); e == nil {
		t.Fatal("second process replaced live socket")
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("replacement"), 0600); e != nil {
		t.Fatal(e)
	}
	listener.Close()
	data, e := os.ReadFile(path)
	if e != nil || string(data) != "replacement" {
		t.Fatal("cleanup removed replacement", e)
	}
	if _, e = Listen(t.Context(), options); e == nil {
		t.Fatal("existing regular path replaced")
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(base, 0750); e != nil {
		t.Fatal(e)
	}
	if _, e = Listen(t.Context(), options); e == nil {
		t.Fatal("shared operator directory admitted")
	}
}
