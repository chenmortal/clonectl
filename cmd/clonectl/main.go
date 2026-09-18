// clonectl: multi-tool sync & verify control plane (rclone rcd, redis-shake-agent, ...).
package main

import (
	"fmt"

	"clonectl/internal/cli"
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
