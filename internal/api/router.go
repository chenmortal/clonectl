package api

import (
	"github.com/gin-gonic/gin"

	"rclone_sync/internal/database"
)

// NewRouter assembles the full route table. Route registration order matters:
// API routes first, then static/SPA (added in later slices via NoRoute).
func NewRouter(d *Deps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.CustomRecovery(func(c *gin.Context, _ any) {
		if !c.Writer.Written() {
			AbortDetail(c, 500, "internal server error")
		}
	}))

	registerAuth(r, d)
	registerUsers(r, d)
	return r
}

func registerAuth(r *gin.Engine, d *Deps) {
	g := r.Group("/api/auth")
	g.POST("/login", d.Login)
	g.GET("/me", d.RequireAuth(), d.Me)
	g.POST("/change-password", d.RequireAuth(), d.ChangePassword)
}

func registerUsers(r *gin.Engine, d *Deps) {
	g := r.Group("/api/users", d.RequireAuth(), d.RequireRoles(database.RoleAdmin))
	g.GET("", d.ListUsers)
	g.POST("", d.CreateUser)
	g.PUT("/:user_id", d.UpdateUser)
	g.DELETE("/:user_id", d.DeleteUser)
	g.POST("/:user_id/reset-password", d.ResetPassword)
}
