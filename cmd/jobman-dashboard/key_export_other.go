//go:build !unix

package main

import "os"

func exportOwnedByOperator(os.FileInfo) bool { return false }
