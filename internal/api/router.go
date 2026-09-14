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
	registerStorageSources(r, d)
	registerDataSources(r, d)
	registerStorages(r, d)
	registerSystemSettings(r, d)
	registerTasks(r, d)
	registerRuns(r, d)
	registerScheduler(r, d)
	registerProxy(r, d)

	r.GET("/healthz", d.Healthz)

	// Static/SPA last (dev disk mode; release builds pass an embed.FS).
	MountStatic(r, d.Static)
	return r
}

func registerProxy(r *gin.Engine, d *Deps) {
	if d.RCProxy == nil {
		return
	}
	r.Any("/rclone", append([]gin.HandlerFunc{d.RequireAuth(), d.ProxyMethodGate()}, ProxyTo(d.RCProxy))...)
	r.Any("/rclone/*path", append([]gin.HandlerFunc{d.RequireAuth(), d.ProxyMethodGate()}, ProxyTo(d.RCProxy))...)
}

func registerTasks(r *gin.Engine, d *Deps) {
	write := func(h gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{d.RequireLeader(), d.RequireRoles(database.RoleEdit, database.RoleAdmin), h}
	}
	tasks := r.Group("/api/tasks", d.RequireAuth(), d.RequireRoles(allRoles()...))
	tasks.GET("", d.ListTasks)
	tasks.POST("", write(d.CreateTask)...)
	tasks.GET("/:task_id", d.GetTask)
	tasks.PUT("/:task_id", write(d.UpdateTask)...)
	tasks.DELETE("/:task_id", write(d.DeleteTask)...)
	tasks.POST("/:task_id/trigger", d.RequireRoles(database.RoleEdit, database.RoleAdmin), d.TriggerTask)

	checks := r.Group("/api/check-tasks", d.RequireAuth(), d.RequireRoles(allRoles()...))
	checks.GET("", d.ListCheckTasks)
	checks.POST("", write(d.CreateCheckTask)...)
	checks.GET("/:check_task_id", d.GetCheckTask)
	checks.PUT("/:check_task_id", write(d.UpdateCheckTask)...)
	checks.DELETE("/:check_task_id", write(d.DeleteCheckTask)...)
	checks.POST("/:check_task_id/trigger", d.RequireRoles(database.RoleEdit, database.RoleAdmin), d.TriggerCheckTask)
}

func registerRuns(r *gin.Engine, d *Deps) {
	g := r.Group("", d.RequireAuth(), d.RequireRoles(allRoles()...))
	g.GET("/api/runs", d.ListSyncRuns)
	g.GET("/api/runs/:run_id", d.GetSyncRun)
	g.GET("/api/checks", d.ListCheckRuns)
	g.GET("/api/checks/:check_id", d.GetCheckRun)
}

func registerScheduler(r *gin.Engine, d *Deps) {
	g := r.Group("/api/scheduler", d.RequireAuth(), d.RequireRoles(allRoles()...))
	g.GET("/jobs", d.ListSchedulerJobs)
	g.GET("/jobs/:job_id", d.GetSchedulerJob)
	g.GET("/overview", d.SchedulerOverview)
	g.POST("/jobs/:job_id/run", d.RequireLeader(), d.RequireRoles(database.RoleEdit, database.RoleAdmin), d.RunSchedulerJob)
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

func allRoles() []string { return []string{database.RoleAdmin, database.RoleEdit, database.RoleView} }

func registerStorageSources(r *gin.Engine, d *Deps) {
	g := r.Group("/api/storage-sources", d.RequireAuth(), d.RequireRoles(allRoles()...))
	g.GET("", d.ListStorageSources)
	g.POST("", d.RequireLeader(), d.RequireRoles(database.RoleAdmin), d.CreateStorageSource)
	g.GET("/:source_id", d.GetStorageSource)
	g.PUT("/:source_id", d.RequireLeader(), d.RequireRoles(database.RoleAdmin), d.UpdateStorageSource)
	g.DELETE("/:source_id", d.RequireLeader(), d.RequireRoles(database.RoleAdmin), d.DeleteStorageSource)
}

func registerDataSources(r *gin.Engine, d *Deps) {
	g := r.Group("/api/data-sources", d.RequireAuth(), d.RequireRoles(allRoles()...))
	g.GET("", d.ListDataSources)
	g.POST("", d.RequireLeader(), d.RequireRoles(database.RoleEdit, database.RoleAdmin), d.CreateDataSource)
	g.GET("/:data_source_id", d.GetDataSource)
	g.PUT("/:data_source_id", d.LoadDSForAccess(database.PermissionWrite), d.UpdateDataSource)
	g.DELETE("/:data_source_id", d.LoadDSForAccess(database.PermissionAdmin), d.DeleteDataSource)
	g.POST("/:data_source_id/verify", d.LoadDSForAccess(database.PermissionWrite), d.VerifyDataSource)
	g.GET("/:data_source_id/bindings", d.LoadDSForAccess(database.PermissionRead), d.ListBindings)
	g.POST("/:data_source_id/bindings", d.LoadDSForAccess(database.PermissionAdmin), d.CreateBinding)
	g.PUT("/:data_source_id/bindings/:binding_id", d.LoadDSForAccess(database.PermissionAdmin), d.UpdateBinding)
	g.DELETE("/:data_source_id/bindings/:binding_id", d.LoadDSForAccess(database.PermissionAdmin), d.DeleteBinding)
}

func registerStorages(r *gin.Engine, d *Deps) {
	g := r.Group("/api/storages", d.RequireAuth(), d.RequireRoles(allRoles()...))
	g.GET("", d.ListStorages)
	g.POST("", d.CreateStorageDeprecated)
	g.GET("/:storage_id", d.GetStorage)
	g.PUT("/:storage_id", d.UpdateStorageDeprecated)
	g.DELETE("/:storage_id", d.DeleteStorageDeprecated)
}

func registerSystemSettings(r *gin.Engine, d *Deps) {
	g := r.Group("/api/system-settings", d.RequireAuth(), d.RequireRoles(database.RoleAdmin))
	g.GET("", d.ListSettings)
	g.GET("/:key", d.GetSetting)
	g.PUT("/:key", d.UpsertSetting)
	g.POST("/internal/alertmanager-test", d.AlertmanagerTest)
}
