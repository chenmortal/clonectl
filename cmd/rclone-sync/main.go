// rclone-sync: periodic object-storage sync service backed by rclone rcd.
package main

import (
	"fmt"

	"rclone_sync/internal/cli"
)

// Overridden at release build time via -ldflags -X.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	cli.SetVersion(fmt.Sprintf("%s (commit %s)", version, commit))
	cli.Execute()
}
