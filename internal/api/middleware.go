package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"clonectl/internal/auth"
	"clonectl/internal/database"
)

// RequireAuth resolves the bearer token to a live, non-disabled user and
// stores it in the context. Missing/invalid → uniform 401.
func (d *Deps) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			AbortUnauthenticated(c, "missing bearer token")
			return
		}
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			AbortUnauthenticated(c, "missing bearer token")
			return
		}
		claims, err := auth.ParseToken(d.Cfg.JWTSecret, d.Cfg.JWTAlgorithm, token)
		if err != nil {
			AbortUnauthenticated(c, "invalid token: "+err.Error())
			return
		}
		var user database.User
		if err := d.DB.First(&user, claims.UserID).Error; err != nil || user.DisabledAt != nil {
			AbortUnauthenticated(c, "user not found or disabled")
			return
		}
		c.Set(contextUserKey, &user)
		c.Next()
	}
}

// RequireRoles 403s unless the current user's role is in the allowed set.
func (d *Deps) RequireRoles(allowed ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := CurrentUser(c)
		if user == nil {
			// Route lacks RequireAuth; treat as unauthenticated.
			AbortUnauthenticated(c, "missing bearer token")
			return
		}
		for _, a := range allowed {
			if user.Role == a {
				c.Next()
				return
			}
		}
		AbortForbidden(c, allowed, user.Role)
	}
}

// RequireLeader rejects writes on the standby node. Nil elector = HA off =
// pass-through (parity with app/api/deps.py).
func (d *Deps) RequireLeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		e := d.GetElector()
		if e == nil || e.IsLeader() {
			c.Next()
			return
		}
		leaderID, clusterName := e.Identity()
		c.Header("Retry-After", "5")
		AbortDetail(c, http.StatusServiceUnavailable, gin.H{
			"error":        "not_leader",
			"leader_id":    leaderID,
			"cluster_name": clusterName,
			"retry_after":  5,
		})
	}
}
