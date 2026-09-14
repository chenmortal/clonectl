// Package rclone talks to an rclone rcd RC API over HTTP with basic auth.
// Port of app/rclone/client.py — error message formats are load-bearing
// (they end up in run.error shown in the UI).
package rclone

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client is an RC API client bound to one rcd endpoint.
type Client struct {
	baseURL  string // no trailing slash
	username string
	password string
	hc       *http.Client
}

// NewClient builds a client; timeout applies to the whole request.
func NewClient(baseURL, username, password string, timeout time.Duration) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		hc:       &http.Client{Timeout: timeout},
	}
}

// Close releases idle connections.
func (c *Client) Close() {
	c.hc.CloseIdleConnections()
}

func (c *Client) post(path string, payload map[string]any, allowErrorField bool) (map[string]any, error) {
	body := payload
	if body == nil {
		body = map[string]any{}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, &APIError{Msg: fmt.Sprintf("request %s failed: %v", path, err)}
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return nil, &APIError{Msg: fmt.Sprintf("request %s failed: %v", path, err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.username, c.password)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, &APIError{Msg: fmt.Sprintf("request %s failed: %v", path, err)}
	}
	defer resp.Body.Close()
	return parse(path, resp, allowErrorField)
}

func parse(path string, resp *http.Response, allowErrorField bool) (map[string]any, error) {
	var raw any
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&raw); err != nil {
		raw = nil
	}
	data := map[string]any{}
	switch t := raw.(type) {
	case map[string]any:
		data = t
	case nil:
		// Non-JSON body → {"raw": text} is impossible to recover text from a
		// consumed stream reliably; keep the status message instead.
		data = map[string]any{}
	default:
		data = map[string]any{"result": raw}
	}

	if resp.StatusCode >= 400 {
		return nil, &APIError{
			Msg:        fmt.Sprintf("rclone rc %s returned %d", path, resp.StatusCode),
			StatusCode: resp.StatusCode,
			Body:       data,
		}
	}
	if !allowErrorField {
		if msg, ok := data["error"]; ok && msg != nil && msg != "" && msg != false {
			return nil, &APIError{
				Msg:        fmt.Sprint(msg),
				StatusCode: resp.StatusCode,
				Body:       data,
			}
		}
	}
	return data, nil
}

// Ping reports whether the rcd answers /core/version with 200.
func (c *Client) Ping() bool {
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/core/version", bytes.NewReader([]byte("{}")))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.username, c.password)
	resp, err := c.hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// CreateRemote pushes one remote config (idempotency handled by callers).
func (c *Client) CreateRemote(name, typ string, parameters map[string]any) (map[string]any, error) {
	return c.post("/config/create", map[string]any{
		"name": name, "type": typ, "parameters": parameters,
	}, false)
}

// DeleteRemote removes one remote config.
func (c *Client) DeleteRemote(name string) (map[string]any, error) {
	return c.post("/config/delete", map[string]any{"name": name}, false)
}

// StartSync submits sync/copy as an async job and returns the jobid.
func (c *Client) StartSync(srcFs, dstFs, mode string, options map[string]any) (int64, error) {
	path := "/sync/copy"
	if mode == "sync" {
		path = "/sync/sync"
	}
	payload := map[string]any{"srcFs": srcFs, "dstFs": dstFs, "_async": true}
	if len(options) > 0 {
		payload["_config"] = options
	}
	data, err := c.post(path, payload, false)
	if err != nil {
		return 0, err
	}
	return jobidFrom(data, path)
}

// JobStatus fetches /job/status. A finished-and-failed job returns a 200
// body carrying "error" — that must NOT raise (allow_error_field parity).
func (c *Client) JobStatus(jobID int64) (map[string]any, error) {
	return c.post("/job/status", map[string]any{"jobid": jobID}, true)
}

// StartCheck submits operations/check as an async job; check options merge
// at the payload top level (unlike sync's _config sub-object).
func (c *Client) StartCheck(srcFs, dstFs string, options map[string]any) (int64, error) {
	payload := map[string]any{"srcFs": srcFs, "dstFs": dstFs, "_async": true}
	for k, v := range options {
		payload[k] = v
	}
	data, err := c.post("/operations/check", payload, false)
	if err != nil {
		return 0, err
	}
	return jobidFrom(data, "/operations/check")
}

// JobStats fetches per-job stats via the group filter.
func (c *Client) JobStats(jobID int64) (map[string]any, error) {
	return c.post("/core/stats", map[string]any{"group": fmt.Sprintf("job/%d", jobID)}, false)
}

func jobidFrom(data map[string]any, path string) (int64, error) {
	v, ok := data["jobid"]
	if !ok || v == nil {
		return 0, &APIError{Msg: fmt.Sprintf("no jobid in response of %s", path), Body: data}
	}
	switch t := v.(type) {
	case float64:
		return int64(t), nil
	case string:
		var n int64
		if _, err := fmt.Sscanf(t, "%d", &n); err == nil {
			return n, nil
		}
	}
	return 0, &APIError{Msg: fmt.Sprintf("no jobid in response of %s", path), Body: data}
}

// --- verification probes (used by /api/data-sources/{id}/verify) ---

// List probes read access via operations/list, normalizing the two
// response shapes rclone produces.
func (c *Client) List(remote string) ([]map[string]any, error) {
	data, err := c.post("/operations/list", map[string]any{"fs": remote, "remote": ""}, false)
	if err != nil {
		return nil, err
	}
	if entries, ok := data["list"].([]any); ok {
		out := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out, nil
	}
	return nil, nil
}

// WriteProbe writes a tiny probe file via operations/copyfile from a local
// temp file, then deletes it via operations/deletefile.
// Returns (writeOK, cleanupWarning). Semantics:
//   - (false, errText)  upload failed
//   - (true, "")        write + cleanup both succeeded
//   - (true, warning)   write succeeded, cleanup failed (stray file left)
func (c *Client) WriteProbe(remote string) (bool, string) {
	name := fmt.Sprintf(".rclone_sync_probe_%d", time.Now().UnixMilli())
	tmp, err := os.CreateTemp("", "rclone-sync-probe-")
	if err != nil {
		return false, err.Error()
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.WriteString("rclone-sync verify probe\n"); err != nil {
		tmp.Close()
		return false, err.Error()
	}
	tmp.Close()

	dstFs := strings.TrimRight(remote, "/")
	_, err = c.post("/operations/copyfile", map[string]any{
		"srcFs":     filepath.Dir(tmpName),
		"srcRemote": filepath.Base(tmpName),
		"dstFs":     dstFs,
		"dstRemote": name,
	}, false)
	if err != nil {
		return false, err.Error()
	}
	if _, err := c.post("/operations/deletefile", map[string]any{
		"fs": dstFs, "remote": name,
	}, false); err != nil {
		return true, fmt.Sprintf("delete failed: %v", err)
	}
	return true, ""
}
