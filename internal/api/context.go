// Package api is the gin HTTP layer: routes, middleware and handlers.
// JSON field names and error shapes reproduce the FastAPI contract the
// existing React frontend depends on.
package api

import (
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"rclone_sync/internal/config"
	"rclone_sync/internal/database"
	"rclone_sync/internal/rclone"
)

// ElectorInfo is the HA leadership view the API needs. Implemented by
// cluster.LeaderElector; nil (or a nil Deps.Elector func) = HA off.
type ElectorInfo interface {
	IsLeader() bool
	Identity() (leaderID, clusterName string)
}

// Deps carries everything handlers need; fields stay nil on single-node
// deployments or before later subsystems start (nil elector = HA off,
// nil RC = verify/trigger endpoints 503).
type Deps struct {
	DB      *gorm.DB
	Cfg     config.Settings
	RC      *rclone.Client
	Elector func() ElectorInfo
}

// GetElector returns nil when HA is off.
func (d *Deps) GetElector() ElectorInfo {
	if d.Elector == nil {
		return nil
	}
	return d.Elector()
}

// contextUserKey stores the authenticated *database.User.
const contextUserKey = "authUser"

// CurrentUser returns the authenticated user set by RequireAuth.
func CurrentUser(c *gin.Context) *database.User {
	v, ok := c.Get(contextUserKey)
	if !ok {
		return nil
	}
	u, _ := v.(*database.User)
	return u
}

// NaiveUTC renders a time the way Python's pydantic serialized naive UTC
// datetimes: "2026-09-04T17:43:00" — no timezone suffix, no fraction.
func NaiveUTC(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05") }

// NaiveUTCPtr renders an optional time (nil → nil).
func NaiveUTCPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := NaiveUTC(*t)
	return &s
}
