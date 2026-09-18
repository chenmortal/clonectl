package cli

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"clonectl/internal/rclone"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "停止主服务与被管的 rclone rcd",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		stopPIDFile(cfg.PIDFile, "clonectl service", 15*time.Second)
		stopPIDFile("rcd.pid", "rclone rcd", 10*time.Second)
		return nil
	},
}

// stopPIDFile: alive pid → SIGTERM, wait up to timeout polling 0.3s, then
// SIGKILL; stale file → unlinked; missing → reported.
func stopPIDFile(path, label string, timeout time.Duration) {
	pid := rclone.ReadPIDFile(path)
	if pid == 0 {
		fmt.Printf("%s: not running (no pid file)\n", label)
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil || proc.Signal(syscall.Signal(0)) != nil {
		fmt.Printf("%s: stale pid file (pid %d not alive); removed\n", label, pid)
		_ = os.Remove(path)
		return
	}
	fmt.Printf("stopping %s (pid %d)...\n", label, pid)
	_ = proc.Signal(syscall.SIGTERM)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if proc.Signal(syscall.Signal(0)) != nil {
			fmt.Printf("%s: stopped\n", label)
			_ = os.Remove(path)
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	_ = proc.Kill()
	fmt.Printf("%s: killed after %s\n", label, timeout)
	_ = os.Remove(path)
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
