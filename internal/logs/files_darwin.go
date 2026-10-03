package logs

// Darwin sys/fcntl.h defines O_SEARCH as O_EXEC (0x40000000) | O_DIRECTORY.
// x/sys/unix does not expose O_SEARCH/O_EXEC. The common walker adds O_DIRECTORY
// and validates the opened descriptor with fstat before using it as a parent.
const directorySearchFlags = 0x40000000
