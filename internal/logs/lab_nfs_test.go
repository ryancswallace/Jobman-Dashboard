//go:build integration

package logs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type nfsProbeConfig struct {
	OperationID  string        `json:"operationId"`
	Root         string        `json:"root"`
	BrokerSHA256 string        `json:"brokerSHA256"`
	Chunks       []FileRequest `json:"chunks"`
}
type nfsReadProof struct {
	XID              uint32 `json:"xid"`
	UID              int    `json:"uid"`
	GID              int    `json:"gid"`
	Offset           uint64 `json:"offset"`
	Count            int    `json:"count"`
	FileHandleSHA256 string `json:"fileHandleSHA256"`
	MinorVersion     int    `json:"minorVersion"`
}
type nfsRelayState struct {
	OperationID        string            `json:"operationId"`
	Ready              bool              `json:"ready"`
	State              string            `json:"state"`
	Proof              *nfsReadProof     `json:"proof"`
	ReadCount          int               `json:"readCount"`
	LastRead           *nfsReadProof     `json:"lastRead"`
	SourcePort         int               `json:"sourcePort"`
	Namespaces         map[string]string `json:"namespaces"`
	OriginalNamespaces map[string]string `json:"originalNamespaces"`
	Failure            *string           `json:"failure"`
}

func nfsReadJSON(path string, target any) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("probe configuration unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 8192 {
		return errors.New("probe configuration identity")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink != 1 || st.Uid != 21901 || st.Gid != 21901 || info.Mode().Perm() != 0400 {
		return errors.New("probe configuration permissions")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(raw) > 8192 {
		return errors.New("probe configuration bound")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("probe configuration invalid")
	}
	return nil
}
func nfsRequest(root, command string) (nfsRelayState, error) {
	var result nfsRelayState
	c, err := net.DialTimeout("unix", filepath.Join(root, "control.sock"), time.Second)
	if err != nil {
		return result, errors.New("relay unavailable")
	}
	defer c.Close()
	if c.SetDeadline(time.Now().Add(time.Second)) != nil {
		return result, errors.New("relay deadline")
	}
	if json.NewEncoder(c).Encode(map[string]string{"command": command}) != nil {
		return result, errors.New("relay request")
	}
	if c.(*net.UnixConn).CloseWrite() != nil {
		return result, errors.New("relay request end")
	}
	raw, err := io.ReadAll(io.LimitReader(c, 32769))
	if err != nil || len(raw) > 32768 {
		return result, errors.New("relay response bound")
	}
	var response struct {
		OK     bool          `json:"ok"`
		Result nfsRelayState `json:"result"`
	}
	if json.Unmarshal(raw, &response) != nil || !response.OK {
		return result, errors.New("relay rejected request")
	}
	return response.Result, nil
}
func nfsValidProof(p *nfsReadProof) bool {
	if p == nil || p.UID != 21901 || p.GID != 21901 || p.MinorVersion != 1 || p.Offset != 0 || p.Count < 1 || p.Count > MaxChunkBytes || len(p.FileHandleSHA256) != 64 {
		return false
	}
	_, err := hex.DecodeString(p.FileHandleSHA256)
	return err == nil
}
func nfsCheckBytes(result FileResult, request FileRequest) bool {
	digest := sha256.Sum256(result.Bytes)
	return result.State == "ok" && int64(len(result.Bytes)) == request.ByteLength && "sha256:"+hex.EncodeToString(digest[:]) == request.Checksum
}
func nfsPrivateMount(raw []byte, root string) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[4] != filepath.Join(root, "mount") {
			continue
		}
		joined := " " + line + " "
		if strings.Contains(joined, "shared:") || !strings.Contains(joined, " - nfs4 127.0.0.1:/srv/lab/data ") {
			return false
		}
		options := map[string]bool{}
		for _, field := range fields {
			for _, option := range strings.Split(field, ",") {
				options[option] = true
			}
		}
		return options["ro"] && options["hard"] && options["vers=4.1"] && options["port=20490"] && options["sec=sys"]
	}
	return false
}

// TestLabHardNFSRead is run only by the reviewed isolated namespace keeper, as
// the existing broker UID. No live endpoint, credential or mutable source path
// is accepted here. It tests the production ProcessReader/helper with a real
// NFS READ held by the private relay; it does not claim HTTP authorization tests.
func TestLabHardNFSRead(t *testing.T) {
	path := os.Getenv("JOBMAN_DASHBOARD_LAB_NFS_PROBE")
	if path == "" {
		t.Skip("explicit isolated NFS probe only")
	}
	if runtime.GOOS != "linux" || os.Getuid() != 21901 || os.Getgid() != 21901 {
		t.Fatal("isolated Linux broker identity required")
	}
	root := filepath.Dir(path)
	if filepath.Dir(root) != "/var/lib/jobman-dashboard-nfs-stall" || filepath.Base(path) != "probe.json" || len(filepath.Base(root)) != 36 {
		t.Fatal("probe path boundary")
	}
	var config nfsProbeConfig
	if err := nfsReadJSON(path, &config); err != nil {
		t.Fatal(err)
	}
	if config.Root != root || config.OperationID != filepath.Base(root) || len(config.Chunks) != 2 || config.Chunks[0].ObjectKey == config.Chunks[1].ObjectKey {
		t.Fatal("probe identity")
	}
	for _, r := range config.Chunks {
		if !validFileRequest(r) || r.Root != filepath.Join(root, "mount") || r.ByteLength < 1 {
			t.Fatal("chunk boundary")
		}
	}
	broker := filepath.Join(root, "jobman-log-broker")
	f, err := os.OpenFile(broker, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal("broker binary unavailable")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0555 || info.Size() > 128<<20 {
		f.Close()
		t.Fatal("broker binary identity")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || st.Gid != 0 || st.Nlink != 1 {
		f.Close()
		t.Fatal("broker binary ownership")
	}
	digest := sha256.New()
	_, err = io.Copy(digest, io.LimitReader(f, 128<<20+1))
	f.Close()
	if err != nil || hex.EncodeToString(digest.Sum(nil)) != config.BrokerSHA256 {
		t.Fatal("broker checksum")
	}
	mountFile, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		t.Fatal("mount information unavailable")
	}
	mountInfo, err := io.ReadAll(io.LimitReader(mountFile, (1<<20)+1))
	mountFile.Close()
	if err != nil || len(mountInfo) > 1<<20 || !nfsPrivateMount(mountInfo, root) {
		t.Fatal("private hard NFS mount required")
	}
	state, err := nfsRequest(root, "state")
	if err != nil || !state.Ready || state.Failure != nil || state.SourcePort < 900 || state.SourcePort > 915 || state.State != "unarmed" || state.OperationID != config.OperationID {
		t.Fatal("fresh relay required")
	}
	for _, name := range []string{"mnt", "net", "uts"} {
		own, err := os.Readlink("/proc/self/ns/" + name)
		if err != nil || own != state.Namespaces[name] || own == state.OriginalNamespaces[name] {
			t.Fatal("private namespace proof")
		}
	}
	reader, err := NewProcessReader(broker, 1, 2*time.Second)
	if err != nil {
		t.Fatal("reader configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	t.Cleanup(func() {
		if _, err := nfsRequest(root, "resume"); err != nil {
			t.Error("explicit forwarding recovery failed; independent watchdog remains armed")
		}
	})
	if !nfsCheckBytes(reader.Read(ctx, config.Chunks[0]), config.Chunks[0]) {
		t.Fatal("baseline chunk checksum")
	}
	baseline, err := nfsRequest(root, "state")
	if err != nil || !nfsValidProof(baseline.LastRead) || baseline.ReadCount < 1 {
		t.Fatal("baseline actual NFS READ proof")
	}
	if _, err = nfsRequest(root, "hold"); err != nil {
		t.Fatal("acknowledged watchdog required before hold")
	}
	began := time.Now()
	done := make(chan FileResult, 1)
	go func() { done <- reader.Read(ctx, config.Chunks[1]) }()
	var held nfsRelayState
	for time.Since(began) < 1500*time.Millisecond {
		held, err = nfsRequest(root, "state")
		if err != nil {
			t.Fatal("held READ observation unavailable")
		}
		if held.State == "held" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if held.State != "held" || !nfsValidProof(held.Proof) || held.Proof.FileHandleSHA256 == baseline.LastRead.FileHandleSHA256 || held.ReadCount != baseline.ReadCount+1 {
		t.Fatal("cold distinct chunk did not reach held kernel NFS READ")
	}
	if result := reader.Read(ctx, config.Chunks[0]); result.State != "reader_busy" || len(result.Bytes) != 0 {
		t.Fatal("live held helper did not retain one slot")
	}
	var result FileResult
	select {
	case result = <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("reader deadline not bounded")
	}
	elapsed := time.Since(began)
	if result.State != "read_timeout" || len(result.Bytes) != 0 || elapsed < 1800*time.Millisecond || elapsed > 3500*time.Millisecond {
		t.Fatal("held NFS reader deadline result")
	}
	// SIGKILL may promptly reap this kernel's NFS read. Report the observation
	// honestly; the separate real Cmd.Wait retention test covers delayed reaping.
	retainedAtDeadline := len(reader.gate) == 1

	if _, err = nfsRequest(root, "resume"); err != nil {
		t.Fatal("forwarding recovery unavailable")
	}
	reaped := time.Now().Add(15 * time.Second)
	for len(reader.gate) != 0 && time.Now().Before(reaped) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(reader.gate) != 0 {
		t.Fatal("helper remains unreaped; keeper must remain for recovery")
	}
	for _, request := range config.Chunks {
		if !nfsCheckBytes(reader.Read(ctx, request), request) {
			t.Fatal("original chunk bytes not restored")
		}
	}
	final, err := nfsRequest(root, "state")
	if err != nil || final.State != "resumed" || final.Failure != nil || final.Proof == nil || *final.Proof != *held.Proof {
		t.Fatal("final relay proof changed")
	}
	t.Log("NFS_ACCEPTANCE " + strconv.FormatBool(retainedAtDeadline) + " " + strconv.FormatInt(elapsed.Milliseconds(), 10) + " " + held.Proof.FileHandleSHA256)
}

func TestNFSProofAndMountGuards(t *testing.T) {
	valid := &nfsReadProof{UID: 21901, GID: 21901, Count: 4096, MinorVersion: 1, FileHandleSHA256: strings.Repeat("a", 64)}
	if !nfsValidProof(valid) {
		t.Fatal("valid proof rejected")
	}
	invalid := *valid
	invalid.UID++
	if nfsValidProof(&invalid) {
		t.Fatal("wrong reader accepted")
	}
	root := "/var/lib/jobman-dashboard-nfs-stall/fixture"
	row := "10 9 0:40 / " + root + "/mount ro - nfs4 127.0.0.1:/srv/lab/data ro,hard,vers=4.1,port=20490,sec=sys\n"
	if !nfsPrivateMount([]byte(row), root) {
		t.Fatal("private mount rejected")
	}
	for _, bad := range []string{strings.Replace(row, "hard", "soft", 1), strings.Replace(row, " ro - ", " ro shared:1 - ", 1), strings.Replace(row, "127.0.0.1", "10.77.0.10", 1)} {
		if nfsPrivateMount([]byte(bad), root) {
			t.Fatal("unsafe mount accepted")
		}
	}
}
