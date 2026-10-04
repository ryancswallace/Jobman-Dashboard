//go:build linux || darwin

package observability

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Listener struct {
	Server   *Server
	server   *http.Server
	listener *net.UnixListener
	root     *os.Root
	name     string
	identity os.FileInfo
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
}

func Listen(parent context.Context, o Options) (*Listener, error) {
	if !filepath.IsAbs(o.SocketPath) || filepath.Clean(o.SocketPath) != o.SocketPath || len(o.SocketPath) > 100 {
		return nil, ErrUnavailable
	}
	path, name := filepath.Dir(o.SocketPath), filepath.Base(o.SocketPath)
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !owned(info) {
		return nil, ErrUnavailable
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, ErrUnavailable
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	fail := func() (*Listener, error) { root.Close(); return nil, ErrUnavailable }
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return fail()
	}
	if _, err = root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return fail()
	}
	s, err := NewServer(o)
	if err != nil {
		return fail()
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: o.SocketPath, Net: "unix"})
	if err != nil {
		return fail()
	}
	ln.SetUnlinkOnClose(false)
	created, err := root.Lstat(name)
	current, check := os.Lstat(path)
	if err != nil || check != nil || !os.SameFile(opened, current) || created.Mode()&os.ModeSocket == 0 || !owned(created) {
		ln.Close()
		return fail()
	}
	if err = root.Chmod(name, 0600); err != nil {
		ln.Close()
		if current, e := root.Lstat(name); e == nil && os.SameFile(current, created) {
			_ = root.Remove(name)
		}
		return fail()
	}
	socket, err := root.Lstat(name)
	if err != nil || !os.SameFile(created, socket) || socket.Mode().Perm() != 0600 {
		ln.Close()
		return fail()
	}
	ctx, cancel := context.WithCancel(parent)
	result := &Listener{Server: s, listener: ln, root: root, name: name, identity: socket, cancel: cancel, done: make(chan struct{})}
	result.server = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	var group sync.WaitGroup
	group.Go(func() { _ = result.server.Serve(ln) })
	group.Go(func() { s.RunSampler(ctx) })
	go func() { group.Wait(); close(result.done) }()
	return result, nil
}
func owned(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid())
}
func (l *Listener) Close() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.Server.Draining()
		l.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = l.server.Shutdown(ctx)
		cancel()
		_ = l.listener.Close()
		<-l.done
		if info, err := l.root.Lstat(l.name); err == nil && os.SameFile(info, l.identity) {
			_ = l.root.Remove(l.name)
		}
		_ = l.root.Close()
	})
}
