//go:build unix

package main

import (
	"os"
	"syscall"
)

// lockFile keeps the descriptor alive for the process lifetime: if it were
// only a local, the GC finaliser would close it and release the flock.
var lockFile *os.File

// singleInstanceLock holds an flock on path for the process lifetime; a
// second instance would tear down the first one's core, so it must refuse
// to start.
func singleInstanceLock(path string) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return true
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return false
	}
	lockFile = f
	return true
}
