package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogTailAndDownload(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	token := login(t, r, "root", "pass1234")
	t.Chdir(t.TempDir())

	// Missing file → exists=false, not an error
	w := doJSON(r, http.MethodGet, "/api/logs", token, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out map[string]any
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, false, out["exists"])
	w = doJSON(r, http.MethodGet, "/api/logs/download", token, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Write 1200 lines, ask for the last 500
	var b strings.Builder
	for i := 1; i <= 1200; i++ {
		fmt.Fprintf(&b, "2026-09-15 line %04d\n", i)
	}
	require.NoError(t, os.WriteFile("rcd.log", []byte(b.String()), 0o644))

	w = doJSON(r, http.MethodGet, "/api/logs?lines=500", token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, true, out["exists"])
	assert.Equal(t, false, out["truncated"])
	content := out["content"].(string)
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	assert.Len(t, lines, 500)
	assert.Equal(t, "2026-09-15 line 0701", lines[0])
	assert.Equal(t, "2026-09-15 line 1200", lines[499])

	// lines validation
	for _, q := range []string{"lines=0", "lines=-3", "lines=abc", "lines=99999"} {
		w = doJSON(r, http.MethodGet, "/api/logs?"+q, token, nil)
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code, q)
	}

	// Default lines=500 works too
	w = doJSON(r, http.MethodGet, "/api/logs", token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, unmarshalBody(w, &out))
	assert.Len(t, strings.Split(strings.TrimSuffix(out["content"].(string), "\n"), "\n"), 500)

	// Download streams the whole file as an attachment
	full, err := os.ReadFile("rcd.log")
	require.NoError(t, err)
	w = doJSON(r, http.MethodGet, "/api/logs/download", token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `attachment; filename="rcd.log"`,
		w.Header().Get("Content-Disposition"))
	assert.Equal(t, string(full), w.Body.String())

	// Non-admin denied
	mkUser(t, d, "viewer", "view")
	w = doJSON(r, http.MethodGet, "/api/logs", login(t, r, "viewer", "pass1234"), nil)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestTailFileEdges covers line accounting directly, including the
// unterminated-tail and multi-chunk cases.
func TestTailFileEdges(t *testing.T) {
	t.Chdir(t.TempDir())
	tail := func(t *testing.T, content string, maxLines, maxBytes int) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "log")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		f, err := os.Open(path)
		require.NoError(t, err)
		defer f.Close()
		info, err := f.Stat()
		require.NoError(t, err)
		got, _, err := tailFile(f, info.Size(), maxLines, maxBytes)
		require.NoError(t, err)
		return got
	}

	assert.Empty(t, tail(t, "", 5, 1<<10))
	assert.Equal(t, "l1\nl2\nl3\n", tail(t, "l1\nl2\nl3\n", 5, 1<<10))
	assert.Equal(t, "l2\nl3\n", tail(t, "l1\nl2\nl3\n", 2, 1<<10))
	// Unterminated tail counts as a line
	assert.Equal(t, "l2\nl3", tail(t, "l1\nl2\nl3", 2, 1<<10))
	assert.Equal(t, "l3", tail(t, "l1\nl2\nl3", 1, 1<<10))

	// 3000 × 100-byte lines (~300KB) spans several 64KB scan chunks
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "%04d %098d\n", i, i)
	}
	all := b.String()
	allLines := strings.Split(strings.TrimSuffix(all, "\n"), "\n")
	got := tail(t, all, 1000, 1<<20)
	assert.Equal(t, allLines[2000:],
		strings.Split(strings.TrimSuffix(got, "\n"), "\n"))

	// Byte cap wins over line count, keeping the tail
	got = tail(t, all, 5000, 4096)
	assert.Equal(t, 4096, len(got))
	assert.True(t, strings.HasSuffix(all, got))
}
