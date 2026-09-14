package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Healthz is public. Cluster fields default to empty/false with no elector
// (HealthOut defaults in Python).
func (d *Deps) Healthz(c *gin.Context) {
	rcloneOK := false
	if d.RC != nil {
		rcloneOK = d.RC.Ping()
	}

	out := gin.H{
		"status":           "ok",
		"rclone_reachable": rcloneOK,
		"is_leader":        false,
		"node_id":          "",
		"cluster_name":     "",
		"leader_id":        nil,
		"peers":            []string{},
	}
	if e := d.GetElector(); e != nil {
		leaderID, clusterName := e.Identity()
		out["is_leader"] = e.IsLeader()
		out["node_id"] = e.GetNodeID()
		out["cluster_name"] = clusterName
		if leaderID != "" {
			out["leader_id"] = leaderID
		}
		peers := e.Peers()
		if peers == nil {
			peers = []string{}
		}
		out["peers"] = peers
	}
	c.JSON(http.StatusOK, out)
}
