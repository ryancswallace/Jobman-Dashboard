//go:build linux

package reports

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

const isolatedObjectID = "90000000-0000-4000-8000-000000000051"

// This opt-in test changes credentials only in child processes. It creates no
// users/groups and touches only fresh directories and the public test fixture.
// Run as root with JOBMAN_REPORT_OS_ISOLATION=1 on a disposable Linux test host.
func TestSharedObjectOSRoleIsolation(t *testing.T) {
	if os.Getenv("JOBMAN_REPORT_OS_ISOLATION") != "1" || os.Geteuid() != 0 {
		t.Skip("requires explicit root-only disposable Linux role-isolation test")
	}
	temporaryBase, sharedBase := os.Getenv("JOBMAN_REPORT_OS_TEST_ROOT"), os.Getenv("JOBMAN_REPORT_OS_SHM_ROOT")
	if temporaryBase == "" {
		temporaryBase = "/tmp"
	}
	if sharedBase == "" {
		sharedBase = "/dev/shm"
	}
	work, err := os.MkdirTemp(temporaryBase, "jobman-report-role-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	if err := os.Chmod(work, 0755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(work, "reports.test")
	if err := os.WriteFile(executable, binary, 0555); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/shared-control-failure-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(work, "testdata"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "testdata", "shared-control-failure-v2.json"), fixture, 0444); err != nil {
		t.Fatal(err)
	}
	objects, err := os.MkdirTemp(sharedBase, "jobman-report-role-objects-")
	if err != nil {
		t.Fatal("verified tmpfs test storage unavailable", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(objects) })
	if err := os.Chown(objects, 21910, 21911); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(objects, 0750); err != nil {
		t.Fatal(err)
	}
	workerTests, err := os.MkdirTemp(sharedBase, "jobman-report-worker-tests-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workerTests) })
	if err := os.Chown(workerTests, 21910, 21911); err != nil {
		t.Fatal(err)
	}
	run := func(role, pattern string, uid, gid uint32) {
		t.Helper()
		command := exec.CommandContext(t.Context(), executable, "-test.run="+pattern, "-test.v", "-test.timeout=45s")
		command.Dir = work
		command.Env = []string{"JOBMAN_REPORT_ROLE=" + role, "JOBMAN_REPORT_ROLE_ROOT=" + objects, "JOBMAN_REPORT_OS_SHM_ROOT=" + workerTests}
		command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{gid}}}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", role, err, output)
		}
		t.Logf("%s: %s", role, output)
	}
	run("worker", "^TestSharedObjectRoleHelper$", 21910, 21911)
	run("reader", "^TestSharedObjectRoleHelper$", 21912, 21911)
	run("unrelated", "^TestSharedObjectRoleHelper$", 21913, 21913)
	// Run the ACL and immutable-pair suite as an ordinary non-root worker too.
	run("worker", "^TestSharedObjects", 21910, 21911)
}

func TestSharedObjectRoleHelper(t *testing.T) {
	role, root := os.Getenv("JOBMAN_REPORT_ROLE"), os.Getenv("JOBMAN_REPORT_ROLE_ROOT")
	if role == "" || root == "" {
		t.Skip("launched only by the isolated Linux parent test")
	}
	access := ObjectAccess{Mode: SharedGroup, WorkerUID: 21910, ReaderGID: 21911}
	path := filepath.Join(root, isolatedObjectID+".json")
	if role == "unrelated" {
		if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
			t.Fatal("unrelated identity was not denied by the OS", err)
		}
		if reader, err := OpenObjectReader(root, access); err == nil {
			reader.Close()
			t.Fatal("unrelated identity opened report storage")
		}
		return
	}
	pair := fixturePair(t)
	if role == "worker" {
		writer, err := OpenObjectsWithAccess(root, access)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		if _, err := writer.Put(t.Context(), isolatedObjectID, pair); err != nil {
			t.Fatal(err)
		}
		return
	}
	if role != "reader" {
		t.Fatal("unexpected test role")
	}
	reader, err := OpenObjectReader(root, access)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := encodePair(pair)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(t.Context(), pair.Subject, objectFor(isolatedObjectID, data, pair)); err != nil {
		t.Fatal("API identity could not verify original pair", err)
	}
	if writer, err := OpenObjectsWithAccess(root, access); err == nil {
		writer.Close()
		t.Fatal("API identity accepted as worker")
	}
	if file, err := os.OpenFile(path, os.O_WRONLY, 0); !errors.Is(err, os.ErrPermission) {
		if file != nil {
			file.Close()
		}
		t.Fatal("API identity file write was not denied by OS", err)
	}
	if err := os.Remove(path); !errors.Is(err, os.ErrPermission) {
		t.Fatal("API identity deletion was not denied by OS", err)
	}
	if err := os.Mkdir(filepath.Join(root, "forbidden"), 0700); !errors.Is(err, os.ErrPermission) {
		t.Fatal("API identity directory mutation was not denied by OS", err)
	}
}
