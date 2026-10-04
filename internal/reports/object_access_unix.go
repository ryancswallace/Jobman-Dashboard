//go:build unix

package reports

import (
	"os"
	"syscall"
)

func openObjectNoFollow(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func validateObjectAccess(file *os.File, access ObjectAccess, directory, writer bool) error {
	info, err := file.Stat()
	if err != nil || access.Validate() != nil {
		return ErrObject
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return ErrObject
	}
	mode := access.objectMode()
	if directory {
		mode = access.directoryMode()
	}
	if info.Mode().Perm() != mode || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return ErrObject
	}
	if access.Mode == SharedGroup {
		if stat.Uid != access.WorkerUID || stat.Gid != access.ReaderGID || writer && uint32(os.Geteuid()) != access.WorkerUID {
			return ErrObject
		}
		return validateSharedObject(file, directory)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return ErrObject
	}
	return nil
}

func prepareObjectFile(file *os.File, access ObjectAccess) error {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return ErrObject
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return ErrObject
	}
	if access.Mode == SharedGroup {
		if stat.Uid != access.WorkerUID {
			return ErrObject
		}
		return validateSharedObject(file, false)
	}
	return nil
}

func publishObjectPermissions(file *os.File, access ObjectAccess) error {
	if access.Mode == SharedGroup {
		// Only this newly created inode is changed; no existing object/root is
		// chmodded, chowned or recursively repaired by application startup.
		if file.Chown(-1, int(access.ReaderGID)) != nil || file.Chmod(0640) != nil {
			return ErrObject
		}
	}
	return validateObjectAccess(file, access, false, true)
}
