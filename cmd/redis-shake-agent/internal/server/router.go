// Package server — router wiring.
//
// Exposes a single New(cfg) entry point that builds a *gin.Engine
// with the full v1 surface and the auth middleware. main.go binds
// it to the configured BindAddr.
package server

import (
	"github.com/gin-gonic/gin"

	"github.com/chenmortal/redis-shake-agent/internal/runner"
	"github.com/chenmortal/redis-shake-agent/internal/store"
)

// New builds a fully-wired *gin.Engine.
//
// In v1 the agent runs as a daemon; the gin engine is configured in
// Release mode to keep stdout clean. The caller is responsible for
// r.Run(cfg.BindAddr) and the surrounding signal handling.
func New(cfg runner.AgentConfig, store *store.TaskStore, iso *runner.Isolation) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(AuthMiddleware(cfg.AuthToken))

	// CORS — clonectl UI may poll from a different origin in dev.
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Headers", "Content-Type, X-Agent-Token")
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	s := &Server{Cfg: cfg, Store: store, Iso: iso}
	s.Register(r)
	return r
}
