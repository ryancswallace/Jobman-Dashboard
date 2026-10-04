package reports

import (
	"context"
	"os"
)

// ObjectAccess is deliberately opt-in. The zero value retains the owner-only
// 0700/0600 profile. SharedGroup requires a provisioned local Linux directory;
// it never converts an existing tree or changes process credentials/umask.
type ObjectAccess struct {
	Mode      string
	WorkerUID uint32
	ReaderGID uint32
}

const SharedGroup = "shared_group"

func (a ObjectAccess) Validate() error {
	if a.Mode == "" && a.WorkerUID == 0 && a.ReaderGID == 0 {
		return nil
	}
	if a.Mode != SharedGroup || a.WorkerUID == 0 || a.WorkerUID > 1<<31-1 || a.ReaderGID == 0 || a.ReaderGID > 1<<31-1 {
		return ErrObject
	}
	return nil
}

func (a ObjectAccess) directoryMode() os.FileMode {
	if a.Mode == SharedGroup {
		return 0750
	}
	return 0700
}

func (a ObjectAccess) objectMode() os.FileMode {
	if a.Mode == SharedGroup {
		return 0640
	}
	return 0600
}

// ObjectReader gives API code no publication, recovery or retention operation.
type ObjectReader interface {
	Read(context.Context, Subject, Object) (Pair, error)
}

type objectWriter interface {
	ObjectReader
	ObjectMaintenance
	Put(context.Context, string, Pair) (Object, error)
	Recover(context.Context, Subject, string) (Pair, Object, error)
}

type ObjectMaintenance interface {
	Remove(string) error
	Sweep(context.Context, func(context.Context, string) (bool, error)) error
}

// ReadOnlyObjects owns a read-only handle to an existing tree. No writable
// implementation is embedded, so an interface assertion cannot recover one.
type ReadOnlyObjects struct{ directory *objectDirectory }

func OpenObjectReader(path string, access ObjectAccess) (*ReadOnlyObjects, error) {
	directory, err := openObjectDirectory(path, access, false)
	if err != nil {
		return nil, err
	}
	return &ReadOnlyObjects{directory: directory}, nil
}

func (r *ReadOnlyObjects) Read(ctx context.Context, subject Subject, object Object) (Pair, error) {
	return r.directory.read(ctx, subject, object)
}

func (r *ReadOnlyObjects) Close() error { return r.directory.root.Close() }
