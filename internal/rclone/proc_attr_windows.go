//go:build windows

package rclone

import "syscall"

// Windows has no Setpgid; the process tree is managed via Job handles we
// don't use — plain attributes suffice.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
