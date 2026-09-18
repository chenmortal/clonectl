//go:build !unix

package store

import "syscall"

// detachedProcAttr returns nil on non-unix platforms; cmd.Cancel +
// WaitDelay still works for graceful shutdown.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
