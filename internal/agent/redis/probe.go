// Package redis — probe.go: lightweight Redis reachability probe.
//
// Used by the data-sources verify endpoint when the underlying
// backend is a redis (not a rclone FS). We deliberately do NOT shell
// out to redis-cli: the probe must work on hosts without that binary,
// and must give back enough info to populate the
// DataSource.last_verified_* columns.
//
// Implementation: a tiny inline RESP client. Redis's protocol is
// small enough (~10 commands) that pulling in go-redis for a
// ping/info-only client is overkill — and a heavier dependency would
// force a go.sum churn on every go-redis release.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"clonectl/internal/agent"
)

// ProbeResult is what ProbeRedis returns to handlers. It is JSON-
// serializable so the API layer can stream it back to the frontend
// for the verify dialog.
type ProbeResult struct {
	Reachable     bool   `json:"reachable"`
	Authenticated bool   `json:"authenticated"`
	Version       string `json:"version,omitempty"`
	Mode          string `json:"mode"` // echoes the topology we tried
	LatencyMS     int    `json:"latency_ms"`
	Error         string `json:"error,omitempty"`
}

// ProbeRedis opens a TCP connection to ep.Addresses[0], sends an
// inline RESP PING and INFO server, parses the response, and returns
// a ProbeResult. Password, if non-empty, is sent as AUTH before PING.
//
// The probe is best-effort: it does not handle cluster topology
// changes, sentinel failover, or TLS. Callers wanting stronger
// verification should run an actual redis-shake sync_reader or
// redis-fullcheck round.
func ProbeRedis(ctx context.Context, ep agent.Endpoint, password string) ProbeResult {
	res := ProbeResult{Mode: ep.Mode}
	if len(ep.Addresses) == 0 {
		res.Error = "no addresses"
		return res
	}
	addr := ep.Addresses[0]
	start := time.Now()
	conn, err := dialRedis(ctx, addr)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer conn.Close()

	if password != "" {
		if err := respWriteArray(conn, "AUTH", password); err != nil {
			res.Error = "auth write: " + err.Error()
			return res
		}
		auth, err := respReadSimpleString(conn)
		if err != nil {
			res.Error = "auth read: " + err.Error()
			return res
		}
		if !strings.HasPrefix(auth, "+OK") {
			res.Authenticated = false
			res.Error = "auth rejected: " + auth
			return res
		}
		res.Authenticated = true
	}

	if err := respWriteArray(conn, "PING"); err != nil {
		res.Error = "ping write: " + err.Error()
		return res
	}
	if ping, err := respReadSimpleString(conn); err != nil {
		res.Error = "ping read: " + err.Error()
		return res
	} else if !strings.HasPrefix(ping, "+PONG") {
		res.Error = "ping not PONG: " + ping
		return res
	}

	if err := respWriteArray(conn, "INFO", "server"); err != nil {
		res.Error = "info write: " + err.Error()
		return res
	}
	info, err := respReadBulkString(conn)
	if err != nil {
		res.Error = "info read: " + err.Error()
		return res
	}
	res.Version = parseInfoVersion(info)
	res.Reachable = true
	res.LatencyMS = int(time.Since(start) / time.Millisecond)
	return res
}

// dialRedis opens a TCP connection with a context-aware deadline.
func dialRedis(ctx context.Context, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn, nil
}

// parseInfoVersion pulls the "redis_version: x.y.z" line out of an
// INFO server bulk string.
func parseInfoVersion(info string) string {
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "redis_version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "redis_version:"))
		}
	}
	return ""
}

// --- minimal RESP encoder / decoder ---

// respWriteArray emits "*<n>\r\n$<len>\r<arg>\r\n..." for n args.
func respWriteArray(w net.Conn, args ...string) error {
	if _, err := fmt.Fprintf(w, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, a := range args {
		if _, err := fmt.Fprintf(w, "$%d\r\n%s\r\n", len(a), a); err != nil {
			return err
		}
	}
	return nil
}

// respReadSimpleString reads a "+..." line and returns the full raw
// response (including the leading "+" and trailing CRLF stripped).
func respReadSimpleString(r net.Conn) (string, error) {
	line, err := readLine(r)
	if err != nil {
		return "", err
	}
	return line, nil
}

// respReadBulkString reads a "$<len>\r\n<data>\r\n" reply.
func respReadBulkString(r net.Conn) (string, error) {
	header, err := readLine(r)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(header, "$") {
		return "", errors.New("expected bulk string, got " + header)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(header, "$"))
	if err != nil {
		return "", fmt.Errorf("bad bulk length %q: %w", header, err)
	}
	if n < 0 {
		return "", nil
	}
	buf := make([]byte, n+2) // +2 for CRLF
	if _, err := readFull(r, buf); err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}

// readLine reads up to and including CRLF.
func readLine(r net.Conn) (string, error) {
	var b strings.Builder
	one := make([]byte, 1)
	for {
		_, err := readFull(r, one)
		if err != nil {
			return "", err
		}
		b.WriteByte(one[0])
		if one[0] == '\n' {
			line := b.String()
			line = strings.TrimSuffix(line, "\r\n")
			line = strings.TrimSuffix(line, "\n")
			return line, nil
		}
	}
}

// readFull wraps io.ReadFull semantics on a net.Conn.
func readFull(r net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// encodeProbeResult is a tiny helper used by tests and the API layer
// to render ProbeResult as a stable JSON shape.
func encodeProbeResult(r ProbeResult) ([]byte, error) {
	return json.Marshal(r)
}
