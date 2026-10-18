//go:build unix

package main

import (
	"os"
	"syscall"
)

// singleInstanceLock holds an flock on path for the process lifetime; a
// second instance would tear down the first one's core, so it must refuse
// to start.
func singleInstanceLock(path string) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return true
	}
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}
