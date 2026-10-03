// Package operations contains private process-local operational state. Broker
// identity pins live on local disk, separately from potentially stalled mounts.
package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
)

type sourcePin struct {
	Instance string `json:"instance"`
	Epoch    int64  `json:"epoch"`
	Revision int64  `json:"revision"`
}
type SourceIdentities struct {
	mu     sync.Mutex
	root   *os.Root
	lock   *os.File
	pins   map[string]sourcePin
	failed bool
}

func OpenSourceIdentities(directory string) (*SourceIdentities, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("broker state directory must already exist with private permissions")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.New("cannot open broker state directory")
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, errors.New("broker state directory changed while opening")
	}
	lock, err := openStateFile(root, ".identity.lock", os.O_RDWR|os.O_CREATE)
	if err != nil {
		root.Close()
		return nil, errors.New("cannot open broker state lock")
	}
	if err = lockState(lock); err != nil {
		lock.Close()
		root.Close()
		return nil, errors.New("broker state is already in use or cannot be locked")
	}
	s := &SourceIdentities{root: root, lock: lock, pins: make(map[string]sourcePin)}
	f, err := openStateFile(root, "source-identities.json", os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		s.Close()
		return nil, errors.New("cannot open broker source identity ledger")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &s.pins) != nil || s.pins == nil || len(s.pins) > 128 {
		s.Close()
		return nil, errors.New("broker source identity ledger is invalid")
	}
	for deployment, pin := range s.pins {
		if !identityUUID(deployment) || !identityUUID(pin.Instance) || pin.Epoch < 1 || pin.Revision < 1 {
			s.Close()
			return nil, errors.New("broker source identity ledger contains invalid pins")
		}
	}
	return s, nil
}
func (s *SourceIdentities) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = true
	_ = s.lock.Close()
	_ = s.root.Close()
}

func (s *SourceIdentities) Verify(ctx context.Context, deployment, instance, epoch string, revision int64) error {
	number, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil || number < 1 || strconv.FormatInt(number, 10) != epoch || revision < 1 || !identityUUID(deployment) || !identityUUID(instance) {
		return errors.New("invalid source identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || ctx.Err() != nil {
		return errors.New("source identity state unavailable")
	}
	old, exists := s.pins[deployment]
	if exists && (old.Instance != instance || old.Epoch > number || old.Revision > revision) {
		return errors.New("source identity or configuration cannot roll backward")
	}
	if !exists && len(s.pins) >= 128 {
		return errors.New("source identity ledger capacity reached")
	}
	pin := sourcePin{Instance: instance, Epoch: number, Revision: revision}
	if old == pin {
		return nil
	}
	s.pins[deployment] = pin
	if err = s.persist(); err != nil {
		s.failed = true
		return errors.New("cannot persist source identity pin")
	}
	return nil
}
func (s *SourceIdentities) persist() error {
	raw, err := json.Marshal(s.pins)
	if err != nil || len(raw) > 65536 {
		return errors.New("identity ledger exceeds bound")
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := ".identity-" + hex.EncodeToString(nonce[:])
	f, err := openStateFile(s.root, tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	defer s.root.Remove(tmp)
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = s.root.Rename(tmp, "source-identities.json"); err != nil {
		return err
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func identityUUID(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
