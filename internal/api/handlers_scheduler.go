package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"rclone_sync/internal/scheduler"
)

// Scheduler monitoring API — inspired by go-co-op/gocron-ui, implemented on
// gocron's own Job API + the in-memory Monitor. Read for every role;
// RunNow needs edit|admin AND leadership. History resets on restart (in-memory).

// ListSchedulerJobs → {node_id,is_leader,scheduler_running,jobs:[...]}.
func (d *Deps) ListSchedulerJobs(c *gin.Context) {
	if d.Sched == nil {
		AbortDetail(c, http.StatusServiceUnavailable, "scheduler not available")
		return
	}
	jobs := d.Sched.Snapshot()
	nodeID := ""
	if e := d.GetElector(); e != nil {
		nodeID = e.GetNodeID()
	}
	out := make([]gin.H, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, schedJobJSON(j))
	}
	c.JSON(http.StatusOK, gin.H{
		"node_id":           nodeID,
		"is_leader":         d.Sched.IsLeaderPublic(),
		"scheduler_running": true,
		"jobs":              out,
		"note":              "execution history is in-memory and resets on restart",
	})
}

// GetSchedulerJob → single job with upcoming-runs preview + execution ring.
func (d *Deps) GetSchedulerJob(c *gin.Context) {
	if d.Sched == nil {
		AbortDetail(c, http.StatusServiceUnavailable, "scheduler not available")
		return
	}
	j, ok := d.Sched.JobByID(c.Param("job_id"))
	if !ok {
		AbortDetail(c, http.StatusNotFound, "job not found")
		return
	}
	body := schedJobJSON(j)
	body["next_runs"] = rfc3339List(j.NextRuns)
	body["executions"] = j.Executions
	c.JSON(http.StatusOK, body)
}

// RunSchedulerJob manually fires a user job through the service layer
// (same path as POST /api/tasks/{id}/trigger: run row + concurrency guard).
func (d *Deps) RunSchedulerJob(c *gin.Context) {
	if d.Sched == nil {
		AbortDetail(c, http.StatusServiceUnavailable, "scheduler not available")
		return
	}
	if kind, found := d.Sched.KindByID(c.Param("job_id")); found && kind.Kind == "internal" {
		AbortDetail(c, http.StatusConflict, "internal jobs cannot be run manually")
		return
	}
	kind, resultID, ok := d.Sched.RunUserJobNow(c.Param("job_id"))
	if !ok {
		AbortDetail(c, http.StatusNotFound, "job not found")
		return
	}
	body := gin.H{"job_id": c.Param("job_id"), "kind": kind.Kind, "task_id": nil}
	if kind.TaskID != nil {
		body["task_id"] = *kind.TaskID
	}
	if resultID != nil {
		if kind.Kind == "task" {
			body["run_id"] = *resultID
		} else {
			body["check_id"] = *resultID
		}
	}
	c.JSON(http.StatusAccepted, body)
}

// SchedulerOverview powers the page header.
func (d *Deps) SchedulerOverview(c *gin.Context) {
	if d.Sched == nil {
		AbortDetail(c, http.StatusServiceUnavailable, "scheduler not available")
		return
	}
	jobs := d.Sched.Snapshot()
	userJobs, runningNow := 0, 0
	for _, j := range jobs {
		if j.Kind != "internal" {
			userJobs++
		}
		if j.IsRunning {
			runningNow++
		}
	}
	isLeader := d.Sched.IsLeaderPublic()
	leaderID := any(nil)
	clusterName := ""
	nodeID := ""
	if e := d.GetElector(); e != nil {
		nodeID = e.GetNodeID()
		id, cn := e.Identity()
		clusterName = cn
		if id != "" {
			leaderID = id
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"is_leader":    isLeader,
		"leader_id":    leaderID,
		"node_id":      nodeID,
		"cluster_name": clusterName,
		"jobs_total":   len(jobs),
		"user_jobs":    userJobs,
		"running_now":  runningNow,
	})
}

func schedJobJSON(j scheduler.JobView) gin.H {
	var taskID any
	if j.TaskID != nil {
		taskID = *j.TaskID
	}
	return gin.H{
		"id": j.ID, "name": j.Name, "kind": j.Kind, "task_id": taskID,
		"tags":                  j.Tags,
		"schedule":              j.Schedule,
		"next_run":              rfc3339Ptr(j.NextRun),
		"last_run_started_at":   rfc3339Ptr(j.LastRunStartedAt),
		"last_run_completed_at": rfc3339Ptr(j.LastRunCompletedAt),
		"is_running":            j.IsRunning,
		"last_error":            j.LastError,
		"run_count":             j.RunCount,
		"fail_count":            j.FailCount,
		"consecutive_failures":  j.ConsecutiveFails,
	}
}

func rfc3339Ptr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

func rfc3339List(in []time.Time) []string {
	out := make([]string, 0, len(in))
	for _, t := range in {
		out = append(out, t.UTC().Format("2006-01-02T15:04:05Z"))
	}
	return out
}
