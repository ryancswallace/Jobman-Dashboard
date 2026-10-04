//go:build !linux

package reports

import "os"

// The shared-group profile is supported only where its filesystem and ACL
// absence can be verified. In particular, Darwin is not silently treated as
// having Linux POSIX ACL semantics. The owner-only profile remains available.
func validateSharedObject(*os.File, bool) error { return ErrObject }
