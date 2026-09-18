// Package server — gin router and handlers for the agent's HTTP API.
//
// Endpoints exposed (all under /v1, all return the standard envelope):
//
//	GET    /v1/ping           liveness probe
//	GET    /v1/noop           keepalive (for NAT/load balancer)
//	POST   /v1/tasks/submit   start a new task
//	GET    /v1/tasks/list     lightweight task summaries
//	GET    /v1/tasks/status   full task status
//	DELETE /v1/tasks/stop     stop a task
//	GET    /v1/tasks/logs     tail/range stdout
//	GET    /v1/tasks/metrics  latest NormalizedProgress + raw
//	GET    /v1/version        agent + upstream binary versions
//	GET    /v1/binary-info    available modes / upstream binary info
//
// Auth: X-Agent-Token header. Missing/empty cfg.AuthToken disables
// auth entirely (dev / LAN). The middleware always sets
// CORS=Access-Control-Allow-Origin=* so the clonectl UI can poll from
// a different origin (proxy or direct in dev).
package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware enforces the shared-secret header when cfg.AuthToken
// is non-empty. Disabled otherwise.
func AuthMiddleware(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" {
			c.Next()
			return
		}
		got := c.GetHeader("X-Agent-Token")
		if got != token {
			c.Header("Content-Type", "application/json")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"ok": false,
				"error": gin.H{
					"code":    "auth_failed",
					"message": "missing or wrong X-Agent-Token",
				},
			})
			return
		}
		c.Next()
	}
}
