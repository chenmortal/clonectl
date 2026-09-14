// Package rctest provides a programmable fake rclone rcd for tests:
// a real httptest server speaking the small RC API subset the service uses,
// with queued /job/status responses and injectable failures.
package rctest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Record is one observed RC request.
type Record struct {
	Method string
	Path   string
	Body   map[string]any
}

// Server is the fake rcd.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	records  []Record
	jobidSeq int64

	failNext  map[string][]failure       // path → one-shot queued failures
	jobStatus map[int64][]map[string]any // jobid → queued responses (FIFO)
	jobStats  map[int64]map[string]any   // jobid → canned stats
	listFails []failure                  // queued /operations/list failures
	list      []map[string]any           // canned list result
	copyFails []failure                  // queued /operations/copyfile failures
	delFails  []failure                  // queued /operations/deletefile failures
	versionOK bool                       // whether /core/version answers 200
}

type failure struct {
	status int
	body   map[string]any
}

// New starts the fake server. Use srv.URL as the client base URL.
func New() *Server {
	s := &Server{
		failNext:  map[string][]failure{},
		jobStatus: map[int64][]map[string]any{},
		jobStats:  map[int64]map[string]any{},
		versionOK: true,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.route)
	s.Server = httptest.NewServer(mux)
	return s
}

// URL returns the base URL (strip trailing slash for the client).
func (s *Server) URL() string { return strings.TrimRight(s.Server.URL, "/") }

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body = map[string]any{}
	}
	s.mu.Lock()
	s.records = append(s.records, Record{Method: r.Method, Path: r.URL.Path, Body: body})

	path := r.URL.Path
	if fails := s.failNext[path]; len(fails) > 0 {
		f := fails[0]
		s.failNext[path] = fails[1:]
		s.mu.Unlock()
		s.writeJSON(w, f.status, f.body)
		return
	}

	switch path {
	case "/core/version":
		ok := s.versionOK
		s.mu.Unlock()
		if !ok {
			s.writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "down"})
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"version": "v1.70.0"})
	case "/config/create", "/config/delete":
		s.mu.Unlock()
		s.writeJSON(w, http.StatusOK, map[string]any{})
	case "/sync/sync", "/sync/copy", "/operations/check":
		s.jobidSeq++
		id := s.jobidSeq
		s.mu.Unlock()
		s.writeJSON(w, http.StatusOK, map[string]any{"jobid": id})
	case "/job/status":
		id := toInt64(body["jobid"])
		queue := s.jobStatus[id]
		var resp map[string]any
		if len(queue) > 0 {
			resp = queue[0]
			s.jobStatus[id] = queue[1:]
		} else {
			resp = FinishedSuccess(id)
		}
		s.mu.Unlock()
		s.writeJSON(w, http.StatusOK, resp)
	case "/core/stats":
		// body group is "job/{id}"
		group, _ := body["group"].(string)
		id := toInt64(strings.TrimPrefix(group, "job/"))
		stats := s.jobStats[id]
		s.mu.Unlock()
		if stats == nil {
			s.writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		s.writeJSON(w, http.StatusOK, stats)
	case "/operations/list":
		if len(s.listFails) > 0 {
			f := s.listFails[0]
			s.listFails = s.listFails[1:]
			s.mu.Unlock()
			s.writeJSON(w, f.status, f.body)
			return
		}
		list := s.list
		s.mu.Unlock()
		s.writeJSON(w, http.StatusOK, map[string]any{"list": list})
	case "/operations/copyfile":
		if len(s.copyFails) > 0 {
			f := s.copyFails[0]
			s.copyFails = s.copyFails[1:]
			s.mu.Unlock()
			s.writeJSON(w, f.status, f.body)
			return
		}
		s.mu.Unlock()
		s.writeJSON(w, http.StatusOK, map[string]any{})
	case "/operations/deletefile":
		if len(s.delFails) > 0 {
			f := s.delFails[0]
			s.delFails = s.delFails[1:]
			s.mu.Unlock()
			s.writeJSON(w, f.status, f.body)
			return
		}
		s.mu.Unlock()
		s.writeJSON(w, http.StatusOK, map[string]any{})
	default:
		s.mu.Unlock()
		s.writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case string:
		var n int64
		if _, err := fmt.Sscanf(t, "%d", &n); err == nil {
			return n
		}
	}
	return 0
}

// --- scripting API ---

// Records returns a copy of all observed requests.
func (s *Server) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}

// Count returns how many requests hit path.
func (s *Server) Count(path string) int {
	n := 0
	for _, r := range s.Records() {
		if r.Path == path {
			n++
		}
	}
	return n
}

// FailNext makes the NEXT request to path return status/body (one-shot).
func (s *Server) FailNext(path string, status int, body map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext[path] = append(s.failNext[path], failure{status, body})
}

// QueueJobStatus pushes canned /job/status responses for jobid (FIFO).
// When the queue empties, calls default to FinishedSuccess.
func (s *Server) QueueJobStatus(jobid int64, responses ...map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobStatus[jobid] = append(s.jobStatus[jobid], responses...)
}

// SetJobStats pins the /core/stats body for group job/{jobid}.
func (s *Server) SetJobStats(jobid int64, stats map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobStats[jobid] = stats
}

// SetListResult pins the /operations/list entries.
func (s *Server) SetListResult(entries []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = entries
}

// FailListNext queues a one-shot /operations/list failure.
func (s *Server) FailListNext(status int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listFails = append(s.listFails, failure{status, map[string]any{"error": msg}})
}

// FailCopyNext / FailDeleteNext queue one-shot probe-step failures.
func (s *Server) FailCopyNext(status int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.copyFails = append(s.copyFails, failure{status, map[string]any{"error": msg}})
}

func (s *Server) FailDeleteNext(status int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delFails = append(s.delFails, failure{status, map[string]any{"error": msg}})
}

// SetVersionOK controls whether /core/version succeeds (Ping).
func (s *Server) SetVersionOK(ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versionOK = ok
}

// --- canned job/status bodies ---

// Running is a not-yet-finished job.
func Running(jobid int64) map[string]any {
	return map[string]any{"jobid": jobid, "finished": false, "success": true}
}

// FinishedSuccess is a finished, successful job.
func FinishedSuccess(jobid int64) map[string]any {
	return map[string]any{"jobid": jobid, "finished": true, "success": true}
}

// FinishedError is a finished, failed job carrying an error message.
func FinishedError(jobid int64, msg string) map[string]any {
	return map[string]any{"jobid": jobid, "finished": true, "success": false, "error": msg}
}
