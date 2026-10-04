//go:build linux

package reports

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func validateSharedObject(file *os.File, directory bool) error {
	var fs unix.Statfs_t
	if unix.Fstatfs(int(file.Fd()), &fs) != nil || !supportedSharedFilesystem(int64(fs.Type)) {
		return ErrObject
	}
	attributes := []string{"system.posix_acl_access"}
	if directory {
		attributes = append(attributes, "system.posix_acl_default")
	}
	for _, attribute := range attributes {
		// A zero-length query allocates no attribute-controlled buffer. Only an
		// affirmative ENODATA absence is accepted, not an unsupported/denied
		// probe or an ACL whose mask happens to resemble ordinary mode bits.
		if _, err := unix.Fgetxattr(int(file.Fd()), attribute, nil); !errors.Is(err, unix.ENODATA) {
			return ErrObject
		}
	}
	return nil
}

func supportedSharedFilesystem(kind int64) bool {
	switch kind {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.TMPFS_MAGIC, unix.BTRFS_SUPER_MAGIC:
		return true
	default:
		return false
	}
}
