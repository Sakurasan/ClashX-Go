//go:build !darwin

package main

import "errors"

// setSystemProxy is only implemented on macOS.
func setSystemProxy(bool, int) error {
	return errors.New("系统代理仅支持 macOS")
}
