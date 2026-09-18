package api

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"clonectl/internal/rclone"
)

// Tail and download of the managed rcd log (rclone.RcdLogName). The file
// lives in the serve process working directory, so handlers address it
// relatively — the exact path the rclone manager appends to.

const (
	logTailDefaultLines = 500
	logTailMaxLines     = 5000
	logTailMaxBytes     = 1 << 20 // hard cap on returned content
	logScanChunk        = 64 << 10
)

// LogTail (admin) — last ?lines= (default 500, max 5000) lines of the rcd
// log. When the file is missing (external rcd, never started yet) the
// response reports exists=false instead of erroring.
func (d *Deps) LogTail(c *gin.Context) {
	lines := logTailDefaultLines
	if s := c.Query("lines"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > logTailMaxLines {
			AbortDetail(c, http.StatusUnprocessableEntity,
				"lines must be an integer between 1 and "+strconv.Itoa(logTailMaxLines))
			return
		}
		lines = n
	}

	f, err := os.Open(rclone.RcdLogName)
	if os.IsNotExist(err) {
		c.JSON(http.StatusOK, gin.H{
			"file": rclone.RcdLogName, "exists": false, "size": 0,
			"mod_time": nil, "content": "", "truncated": false,
		})
		return
	}
	if err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	content, truncated, err := tailFile(f, info.Size(), lines, logTailMaxBytes)
	if err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"file":      rclone.RcdLogName,
		"exists":    true,
		"size":      info.Size(),
		"mod_time":  NaiveUTC(info.ModTime()),
		"content":   content,
		"truncated": truncated,
	})
}

// LogDownload (admin) — streams the full rcd log as an attachment.
func (d *Deps) LogDownload(c *gin.Context) {
	f, err := os.Open(rclone.RcdLogName)
	if os.IsNotExist(err) {
		AbortDetail(c, http.StatusNotFound,
			"log file not found; it appears once a managed rcd has started")
		return
	}
	if err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+rclone.RcdLogName+`"`)
	c.DataFromReader(http.StatusOK, info.Size(), "text/plain; charset=utf-8", f, nil)
}

// tailFile returns the last maxLines lines (at most maxBytes) reading
// backwards in chunks, so a multi-GB log costs no more than ~1MB of memory.
// A final unterminated segment counts as one line.
func tailFile(f *os.File, size int64, maxLines, maxBytes int) (string, bool, error) {
	if size == 0 {
		return "", false, nil
	}
	var buf []byte
	newlines, truncated := 0, false
	end := size
	for end > 0 {
		start := end - logScanChunk
		if start < 0 {
			start = 0
		}
		chunk := make([]byte, end-start)
		if _, err := f.ReadAt(chunk, start); err != nil && err != io.EOF {
			return "", false, err
		}
		buf = append(chunk, buf...)
		newlines += bytes.Count(chunk, []byte{'\n'})
		end = start
		// buf always ends at EOF, so its last byte classifies the tail.
		total := newlines
		if buf[len(buf)-1] != '\n' {
			total++
		}
		if total > maxLines {
			break
		}
		if len(buf) >= maxBytes {
			truncated = true
			break
		}
	}

	// Drop whole lines from the front until at most maxLines remain.
	if partial := buf[len(buf)-1] != '\n'; partial {
		newlines++ // the unterminated tail is a line too
	}
	if extra := newlines - maxLines; extra > 0 {
		for i := 0; i < len(buf); i++ {
			if buf[i] == '\n' {
				if extra--; extra == 0 {
					buf = buf[i+1:]
					break
				}
			}
		}
	}
	if len(buf) > maxBytes {
		buf, truncated = buf[len(buf)-maxBytes:], true
	}
	return string(buf), truncated, nil
}
