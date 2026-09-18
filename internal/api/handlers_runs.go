package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"clonectl/internal/database"
	"clonectl/internal/rclone"
)

// runListPage is the paginated response shape used by /api/runs and /api/checks.
type runListPage struct {
	Items    []gin.H `json:"items"`
	Total    int64   `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}

// parsePage reads ?page=&page_size= (1-based). Missing or bad → defaults.
func parsePage(c *gin.Context) (page, size int) {
	page, size = 1, 20
	if v := c.Query("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			size = n
		}
	}
	return
}

// listRuns is the shared /api/runs + /api/checks list handler.
// Filters: task_id, status (enum). Pagination: page, page_size.
// Non-admin users only see runs for tasks they have a read binding on.
func (d *Deps) listRuns(c *gin.Context, model any, kind string) (runListPage, bool) {
	user := CurrentUser(c)
	q := d.DB.Model(model).Order("id DESC")

	// Filter by visible tasks for non-admin callers.
	if user.Role != database.RoleAdmin {
		var ids []int64
		if kind == "sync" {
			ids, _ = d.visibleSyncTaskIDs(user)
		} else {
			ids, _ = d.visibleCheckTaskIDs(user)
		}
		if len(ids) == 0 {
			return runListPage{Items: []gin.H{}, Page: 1, PageSize: 20}, true
		}
		q = q.Where("task_id IN ?", ids)
	}

	if v := c.Query("task_id"); v != "" {
		q = q.Where("task_id = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return runListPage{}, false
	}

	page, size := parsePage(c)
	offset := (page - 1) * size
	q = q.Limit(size).Offset(offset)

	if err := q.Find(model).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return runListPage{}, false
	}
	return runListPage{Total: total, Page: page, PageSize: size}, true
}

// ListSyncRuns — paginated.
func (d *Deps) ListSyncRuns(c *gin.Context) {
	var rows []database.SyncRun
	page, ok := d.listRuns(c, &rows, "sync")
	if !ok {
		return
	}
	page.Items = make([]gin.H, 0, len(rows))
	for i := range rows {
		page.Items = append(page.Items, toRunOut(&rows[i]))
	}
	c.JSON(http.StatusOK, page)
}

// ListCheckRuns — paginated.
func (d *Deps) ListCheckRuns(c *gin.Context) {
	var rows []database.CheckRun
	page, ok := d.listRuns(c, &rows, "check")
	if !ok {
		return
	}
	page.Items = make([]gin.H, 0, len(rows))
	for i := range rows {
		page.Items = append(page.Items, toCheckOut(&rows[i]))
	}
	c.JSON(http.StatusOK, page)
}

// --- CSV export -----------------------------------------------------------

// csvTime renders *time.Time as a UTC RFC3339 string (empty for nil).
func csvTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// runRowsForExport applies the same visibility + filter rules as listRuns
// but returns the full set (no limit/offset) for CSV export.
func (d *Deps) runRowsForExport(c *gin.Context, kind string) (any, string, bool) {
	user := CurrentUser(c)
	var model any
	var q *gorm.DB
	if kind == "sync" {
		m := []database.SyncRun{}
		model = &m
		q = d.DB.Model(&database.SyncRun{}).Order("id DESC")
	} else {
		m := []database.CheckRun{}
		model = &m
		q = d.DB.Model(&database.CheckRun{}).Order("id DESC")
	}
	if user.Role != database.RoleAdmin {
		var ids []int64
		if kind == "sync" {
			ids, _ = d.visibleSyncTaskIDs(user)
		} else {
			ids, _ = d.visibleCheckTaskIDs(user)
		}
		if len(ids) == 0 {
			return nil, "", true
		}
		q = q.Where("task_id IN ?", ids)
	}
	if v := c.Query("task_id"); v != "" {
		q = q.Where("task_id = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if err := q.Find(model).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return nil, "", false
	}
	return model, kind, true
}

// ExportSyncRunsCSV — streams all sync runs (filtered) as a CSV download.
func (d *Deps) ExportSyncRunsCSV(c *gin.Context) {
	d.exportRunsCSV(c, "sync")
}

// ExportCheckRunsCSV — streams all check runs (filtered) as a CSV download.
func (d *Deps) ExportCheckRunsCSV(c *gin.Context) {
	d.exportRunsCSV(c, "check")
}

func (d *Deps) exportRunsCSV(c *gin.Context, kind string) {
	raw, _, ok := d.runRowsForExport(c, kind)
	if !ok {
		return
	}
	filename := fmt.Sprintf("%s-runs-%s.csv", kind, time.Now().UTC().Format("20060102-150405"))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	w := csv.NewWriter(c.Writer)
	if kind == "sync" {
		rows := *(raw.(*[]database.SyncRun))
		_ = w.Write([]string{"id", "task_id", "status", "trigger", "started_at", "finished_at", "error", "bytes"})
		for _, r := range rows {
			bytes := ""
			if r.Stats != nil {
				if v, ok := (*r.Stats)["bytes"]; ok {
					bytes = fmt.Sprintf("%v", v)
				}
			}
			_ = w.Write([]string{
				strconv.FormatInt(r.ID, 10),
				strconv.FormatInt(r.TaskID, 10),
				r.Status,
				r.Trigger,
				csvTime(r.StartedAt),
				csvTime(r.FinishedAt),
				strPtr(r.Error),
				bytes,
			})
		}
	} else {
		rows := *(raw.(*[]database.CheckRun))
		_ = w.Write([]string{"id", "task_id", "status", "trigger", "started_at", "finished_at", "error"})
		for _, r := range rows {
			_ = w.Write([]string{
				strconv.FormatInt(r.ID, 10),
				strconv.FormatInt(r.TaskID, 10),
				r.Status,
				r.Trigger,
				csvTime(r.StartedAt),
				csvTime(r.FinishedAt),
				strPtr(r.Error),
			})
		}
	}
	w.Flush()
}

func strPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
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
