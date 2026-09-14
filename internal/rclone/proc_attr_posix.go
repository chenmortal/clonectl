//go:build !windows

package rclone

import "syscall"

// detachedProcAttr puts rcd in its own process group so a service SIGTERM
// never lands on it directly (we manage its lifetime explicitly).
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
