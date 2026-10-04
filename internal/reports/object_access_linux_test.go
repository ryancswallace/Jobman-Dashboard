//go:build linux

package reports

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func sharedTestRoot(t *testing.T) (string, ObjectAccess) {
	t.Helper()
	if os.Geteuid() == 0 || os.Getegid() == 0 {
		t.Skip("shared profile requires a non-root worker and reader group; use the explicit role-isolation test as root")
	}
	base := os.Getenv("JOBMAN_REPORT_OS_SHM_ROOT")
	if base == "" {
		base = "/dev/shm"
	}
	parent, err := os.MkdirTemp(base, "jobman-report-access-")
	if err != nil {
		t.Skip("a verified local tmpfs test directory is unavailable")
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	path := filepath.Join(parent, "objects")
	if err := os.Mkdir(path, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0750); err != nil {
		t.Fatal(err)
	}
	return path, ObjectAccess{Mode: SharedGroup, WorkerUID: uint32(os.Geteuid()), ReaderGID: uint32(os.Getegid())}
}

func TestSharedObjectsPinOwnerGroupModesAndPairIdentity(t *testing.T) {
	path, access := sharedTestRoot(t)
	writer, err := OpenObjectsWithAccess(path, access)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	pair := fixturePair(t)
	object, err := writer.Put(t.Context(), "90000000-0000-4000-8000-000000000041", pair)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenObjectReader(path, access)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Read(t.Context(), pair.Subject, object); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenObjectReader(path, ObjectAccess{}); err == nil {
		t.Fatal("shared profile was inferred rather than explicitly selected")
	}
	wrong := access
	wrong.WorkerUID++
	if _, err := OpenObjectReader(path, wrong); err == nil {
		t.Fatal("wrong worker accepted")
	}
	wrong = access
	wrong.ReaderGID++
	if _, err := OpenObjectReader(path, wrong); err == nil {
		t.Fatal("wrong reader group accepted")
	}
	name := filepath.Join(path, object.ID+".json")
	if err := os.Chmod(name, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(t.Context(), pair.Subject, object); err == nil {
		t.Fatal("unconverted owner-only object accepted")
	}
	if err := os.Chmod(name, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(t.Context(), pair.Subject, object); err == nil {
		t.Fatal("public object accepted")
	}
	missing := filepath.Join(filepath.Dir(path), "missing")
	if _, err := OpenObjectsWithAccess(missing, access); err == nil {
		t.Fatal("shared writer automatically provisioned a root")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("shared startup mutated missing root")
	}
}

// A named user can receive extra access while stat still reports exactly 0750.
func extendedACL(owner, group uint16, uid uint32) []byte {
	data := make([]byte, 4+5*8)
	binary.LittleEndian.PutUint32(data, 2)
	for i, e := range []struct {
		tag, perm uint16
		id        uint32
	}{{1, owner, ^uint32(0)}, {2, 4, uid}, {4, group, ^uint32(0)}, {16, group, ^uint32(0)}, {32, 0, ^uint32(0)}} {
		entry := data[4+i*8:]
		binary.LittleEndian.PutUint16(entry, e.tag)
		binary.LittleEndian.PutUint16(entry[2:], e.perm)
		binary.LittleEndian.PutUint32(entry[4:], e.id)
	}
	return data
}

func TestSharedObjectsRejectAccessDefaultAndInheritedACLs(t *testing.T) {
	for _, attribute := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		t.Run(attribute, func(t *testing.T) {
			path, access := sharedTestRoot(t)
			writer, err := OpenObjectsWithAccess(path, access)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := unix.Fsetxattr(int(file.Fd()), attribute, extendedACL(7, 5, access.WorkerUID+1), 0); err != nil {
				t.Fatal(err)
			}
			if info, err := file.Stat(); err != nil || info.Mode().Perm() != 0750 {
				t.Fatal("ACL fixture must preserve ordinary root mode", err)
			}
			if _, err := OpenObjectReader(path, access); err == nil {
				t.Fatal("extra root ACL accepted")
			}
			if _, err := writer.Put(t.Context(), "90000000-0000-4000-8000-000000000042", fixturePair(t)); err == nil {
				t.Fatal("writer ignored ACL change after open")
			}
			if attribute == "system.posix_acl_default" {
				pending, err := os.OpenFile(filepath.Join(path, ".pending-inherited-test"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer pending.Close()
				if info, err := pending.Stat(); err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("inherited ACL fixture must preserve private temporary mode", err)
				}
				if prepareObjectFile(pending, access) == nil {
					t.Fatal("temporary inode accepted an inherited ACL before publication")
				}
			}
		})
	}
	path, access := sharedTestRoot(t)
	writer, err := OpenObjectsWithAccess(path, access)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	pair := fixturePair(t)
	object, err := writer.Put(t.Context(), "90000000-0000-4000-8000-000000000043", pair)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(path, object.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := unix.Fsetxattr(int(file.Fd()), "system.posix_acl_access", extendedACL(6, 4, access.WorkerUID+1), 0); err != nil {
		t.Fatal(err)
	}
	if info, err := file.Stat(); err != nil || info.Mode().Perm() != 0640 {
		t.Fatal("ACL fixture must preserve immutable object mode", err)
	}
	reader, err := OpenObjectReader(path, access)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Read(t.Context(), pair.Subject, object); err == nil {
		t.Fatal("extra object ACL accepted")
	}
}

func TestSharedObjectsRejectUnverifiableFilesystemKinds(t *testing.T) {
	for _, kind := range []int64{unix.NFS_SUPER_MAGIC, unix.OVERLAYFS_SUPER_MAGIC, unix.FUSE_SUPER_MAGIC, 0} {
		if supportedSharedFilesystem(kind) {
			t.Fatal("unsupported filesystem accepted", kind)
		}
	}
	for _, kind := range []int64{unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.TMPFS_MAGIC, unix.BTRFS_SUPER_MAGIC} {
		if !supportedSharedFilesystem(kind) {
			t.Fatal("verified local filesystem rejected", kind)
		}
	}
}
