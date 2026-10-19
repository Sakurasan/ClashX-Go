//go:build !unix

package main

// singleInstanceLock is unimplemented outside unix; allow the start.
func singleInstanceLock(path string) bool { return true }
