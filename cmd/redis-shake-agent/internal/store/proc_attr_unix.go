//go:build unix

package store

import "syscall"

// detachedProcAttr puts the child into its own process group so that
// SIGINT delivered to the agent's terminal does not cascade into the
// upstream CLI (which is typically running in the foreground).
//
// On unix variants we use Setpgid; on Windows we fall back to a
// proc_attr_other.go stub.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true,
	}
}
