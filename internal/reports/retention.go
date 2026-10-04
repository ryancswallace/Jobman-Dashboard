package reports

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// Sweep examines at most100 names per pass, retaining its directory position
// between passes. Active/pending task IDs are references too, so publication
// between filesystem and database commits is never removed. A one-hour grace
// exceeds task deadlines/leases and protects recent temporary writes.
func (s *ObjectStore) Sweep(ctx context.Context, referenced func(context.Context, string) (bool, error)) (resultErr error) {
	start := time.Now()
	defer func() { s.observer.Observe("object", "sweep", "", "", reportObservation(resultErr), time.Since(start)) }()
	if referenced == nil || s.validateRoot(true) != nil {
		return ErrInvalid
	}
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	if s.sweepDir == nil {
		var err error
		s.sweepDir, err = s.root.Open(".")
		if err != nil {
			return ErrObject
		}
	}
	names, err := s.sweepDir.Readdirnames(100)
	if err != nil {
		_ = s.sweepDir.Close()
		s.sweepDir = nil
		if !errors.Is(err, io.EOF) {
			return ErrObject
		}
	}
	for _, name := range names {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		id := strings.TrimSuffix(name, ".json")
		isObject := strings.HasSuffix(name, ".json") && uuidPattern.MatchString(id)
		isPending := strings.HasPrefix(name, ".pending-") && len(name) == len(".pending-")+32 && hexName(name[len(".pending-"):])
		if !isObject && !isPending {
			continue
		}
		info, err := s.root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return ErrObject
		}
		if time.Since(info.ModTime()) < time.Hour {
			continue
		}
		if isObject {
			active, err := referenced(ctx, id)
			if err != nil {
				return err
			}
			if active {
				continue
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = s.root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrObject
		}
	}
	return s.sync()
}
func hexName(value string) bool {
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// RetentionQueue deliberately excludes source reads, interactive identity,
// analysis/lease claims and redaction policy. Cleanup uses durable references.
type RetentionQueue interface {
	DeleteExpiredReports(context.Context) ([]Object, error)
	ReportObjectReferenced(context.Context, string) (bool, error)
}

func PruneObjects(ctx context.Context, queue RetentionQueue, objects ObjectMaintenance) error {
	if queue == nil || objects == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	expired, err := queue.DeleteExpiredReports(ctx)
	if err != nil {
		return err
	}
	for _, object := range expired {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := objects.Remove(object.ID); err != nil {
			return err
		}
	}
	return objects.Sweep(ctx, queue.ReportObjectReferenced)
}
