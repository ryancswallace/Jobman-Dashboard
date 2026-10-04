package logs

import "golang.org/x/sys/unix"

// O_PATH pins a directory for subsequent openat without requiring read/list
// permission; each child open still checks search permission on that directory.
const directorySearchFlags = unix.O_PATH
