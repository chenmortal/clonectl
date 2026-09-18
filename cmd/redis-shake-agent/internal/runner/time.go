// Package runner — small time helpers shared across runner files.
package runner

import "time"

// nowMillis returns the current Unix epoch in milliseconds.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}
