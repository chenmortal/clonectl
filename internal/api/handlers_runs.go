package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"rclone_sync/internal/database"
	"rclone_sync/internal/rclone"
)

// listRuns is the shared /api/runs + /api/checks list handler.
// Filters: task_id, status (enum), limit (default 100). id DESC.
func (d *Deps) listRuns(c *gin.Context, model any) {
	q := d.DB.Model(model).Order("id DESC")
	if v := c.Query("task_id"); v != "" {
		q = q.Where("task_id = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	q = q.Limit(limit)
	if err := q.Find(model).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
}

// ListSyncRuns — all roles.
func (d *Deps) ListSyncRuns(c *gin.Context) {
	var rows []database.SyncRun
	d.listRuns(c, &rows)
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, toRunOut(&rows[i]))
	}
	c.JSON(http.StatusOK, out)
}

// ListCheckRuns — all roles.
func (d *Deps) ListCheckRuns(c *gin.Context) {
	var rows []database.CheckRun
	d.listRuns(c, &rows)
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, toCheckOut(&rows[i]))
	}
	c.JSON(http.StatusOK, out)
}

// liveStats stitches job/status + job/stats for a running run. totalBytes
// comes from job_status.stats (core/stats lacks it while running) — the UI
// progress bar depends on this.
func (d *Deps) liveStats(client *rclone.Client, jobID int64) gin.H {
	status, err := client.JobStatus(jobID)
	if err != nil {
		return gin.H{"error": err.Error()}
	}
	stats, statsErr := client.JobStats(jobID)
	if statsErr == nil {
		if statusStats, ok := status["stats"].(map[string]any); ok {
			if total, ok := statusStats["totalBytes"]; ok {
				if _, present := stats["totalBytes"]; !present {
					stats["totalBytes"] = total
				}
			}
		}
		return gin.H{"status": status, "stats": stats}
	}
	return gin.H{"status": status}
}

// GetSyncRun — running runs carry the live payload.
func (d *Deps) GetSyncRun(c *gin.Context) {
	var run database.SyncRun
	if err := d.DB.First(&run, c.Param("run_id")).Error; err != nil {
		AbortDetail(c, http.StatusNotFound, "run not found")
		return
	}
	out := toRunOut(&run)
	out["live"] = nil
	if run.Status == database.RunRunning && run.JobID != nil && d.RC != nil {
		out["live"] = d.liveStats(d.RC, *run.JobID)
	}
	c.JSON(http.StatusOK, out)
}

// GetCheckRun — live carries only job status (no stats merge).
func (d *Deps) GetCheckRun(c *gin.Context) {
	var run database.CheckRun
	if err := d.DB.First(&run, c.Param("check_id")).Error; err != nil {
		AbortDetail(c, http.StatusNotFound, "check not found")
		return
	}
	out := toCheckOut(&run)
	out["live"] = nil
	if run.Status == database.RunRunning && run.JobID != nil && d.RC != nil {
		if status, err := d.RC.JobStatus(*run.JobID); err == nil {
			out["live"] = gin.H{"status": status}
		} else {
			out["live"] = gin.H{"error": err.Error()}
		}
	}
	c.JSON(http.StatusOK, out)
}
