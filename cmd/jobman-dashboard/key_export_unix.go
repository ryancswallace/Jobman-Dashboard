//go:build unix

package main

import (
	"os"
	"syscall"
)

func exportOwnedByOperator(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Uid) == uint64(os.Geteuid())
}
