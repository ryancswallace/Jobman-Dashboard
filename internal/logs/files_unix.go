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
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	components := strings.Split(strings.TrimPrefix(root, "/")+"/"+key, "/")
	for i, component := range components {
		if component == "" {
			continue
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(components)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, openErr := unix.Openat(fd, component, flags, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, openErr
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "log-chunk"), nil
}
