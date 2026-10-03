//go:build linux || darwin

package logs

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// Open every component relative to the preceding open directory descriptor.
// O_NOFOLLOW rejects both intermediate and final symlinks, including symlinks
// resolving inside the approved root. O_NONBLOCK prevents FIFO/device opens
// from waiting before the regular-file check. Production helpers isolate NFS.
func openRelative(root, key string) (*os.File, error) {
	fd, err := openSearchDirectory(unix.AT_FDCWD, "/")
	if err != nil {
		return nil, err
	}
	components := strings.Split(strings.TrimPrefix(root, "/")+"/"+key, "/")
	for i, component := range components {
		if component == "" {
			continue
		}
		var next int
		var openErr error
		if i < len(components)-1 {
			next, openErr = openSearchDirectory(fd, component)
		} else {
			next, openErr = unix.Openat(fd, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		}
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, openErr
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "log-chunk"), nil
}

// Directory descriptors pin each component without requiring directory listing
// rights. Operators may grant traversal alone on parents of an approved store.
// Never follow a directory symlink, including at or above the configured root.
func openSearchDirectory(parent int, component string) (int, error) {
	fd, err := unix.Openat(parent, component, directorySearchFlags|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	var info unix.Stat_t
	if err = unix.Fstat(fd, &info); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if info.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = unix.Close(fd)
		return -1, unix.ENOTDIR
	}
	return fd, nil
}
