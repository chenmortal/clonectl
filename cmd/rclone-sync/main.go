// rclone-sync: periodic object-storage sync service backed by rclone rcd.
package main

import (
	"rclone_sync/internal/cli"
)

func main() {
	cli.Execute()
}
