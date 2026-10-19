//go:build !darwin

package main

import "errors"

type proxySnapshot struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    string `json:"port"`
}

type systemProxyBackup struct {
	Service string        `json:"service"`
	Web     proxySnapshot `json:"web"`
	Secure  proxySnapshot `json:"secure"`
	Bypass  []string      `json:"bypass,omitempty"`
}

func snapshotProxy() (*systemProxyBackup, error) {
	return nil, errors.New("系统代理仅支持 macOS")
}

func pointProxyAt(int) error { return errors.New("系统代理仅支持 macOS") }

func restoreProxy(*systemProxyBackup) error { return errors.New("系统代理仅支持 macOS") }
