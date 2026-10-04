package reports

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestObjectReaderHasNoWriteCapabilityAndNeverCreatesStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "objects")
	if _, err := OpenObjectReader(path, ObjectAccess{}); err == nil {
		t.Fatal("reader created missing storage")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only constructor mutated filesystem")
	}
	writer, err := OpenObjects(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	pair := fixturePair(t)
	object, err := writer.Put(t.Context(), "90000000-0000-4000-8000-000000000031", pair)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenObjectReader(path, ObjectAccess{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, ok := any(reader).(objectWriter); ok {
		t.Fatal("reader exposes writer capability")
	}
	if _, ok := any(reader).(ObjectMaintenance); ok {
		t.Fatal("reader exposes retention capability")
	}
	if got, err := reader.Read(t.Context(), pair.Subject, object); err != nil || got.Report.ReportID != pair.Report.ReportID {
		t.Fatal("reader lost immutable report identity", err)
	}
	// A database object checksum and the full subject remain mandatory for API reads.
	wrong := object
	wrong.SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := reader.Read(t.Context(), pair.Subject, wrong); err == nil {
		t.Fatal("reader ignored object checksum")
	}
	if err := os.Chmod(path, 0750); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(t.Context(), pair.Subject, object); err == nil {
		t.Fatal("reader accepted changed root access")
	}
	if _, err := writer.Put(t.Context(), "90000000-0000-4000-8000-000000000032", pair); err == nil {
		t.Fatal("writer accepted changed root access")
	}
}

type forbiddenMutationQueue struct {
	Queue
	claims, deletes int
}

func (q *forbiddenMutationQueue) ClaimReport(context.Context) (Claim, error) {
	q.claims++
	return Claim{}, ErrLease
}
func (q *forbiddenMutationQueue) DeleteExpiredReports(context.Context) ([]Object, error) {
	q.deletes++
	return nil, nil
}

func TestReadOnlyServiceRejectsWorkerAndRetentionBeforeDatabaseMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "objects")
	writer, err := OpenObjects(path)
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
	reader, err := OpenObjectReader(path, ObjectAccess{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	queue := &forbiddenMutationQueue{}
	service := &Service{queue: queue, objects: reader}
	if !errors.Is(service.RunOne(t.Context()), ErrInvalid) || !errors.Is(service.Prune(t.Context()), ErrInvalid) {
		t.Fatal("API-only service accepted worker operation")
	}
	service.Run(t.Context()) // must return immediately, not start idle worker loops
	if queue.claims != 0 || queue.deletes != 0 {
		t.Fatal("read-only service mutated durable state")
	}
	if !errors.Is(PruneObjects(t.Context(), queue, nil), ErrInvalid) || queue.deletes != 0 {
		t.Fatal("missing writer allowed expiry mutation")
	}
}

type retentionOnlyQueue struct {
	calls   int
	expired []Object
}

func (q *retentionOnlyQueue) DeleteExpiredReports(context.Context) ([]Object, error) {
	q.calls++
	return q.expired, nil
}
func (q *retentionOnlyQueue) ReportObjectReferenced(context.Context, string) (bool, error) {
	return true, nil
}

func TestStandaloneRetentionNeedsNoSourceIdentityOrEngine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "objects")
	writer, err := OpenObjects(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	object, err := writer.Put(t.Context(), "90000000-0000-4000-8000-000000000033", fixturePair(t))
	if err != nil {
		t.Fatal(err)
	}
	queue := &retentionOnlyQueue{expired: []Object{object}}
	if err := PruneObjects(t.Context(), queue, writer); err != nil {
		t.Fatal(err)
	}
	if queue.calls != 1 {
		t.Fatal("expiry not invoked")
	}
	if _, err := os.Stat(filepath.Join(path, object.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("paired object retained after expiry")
	}
}
