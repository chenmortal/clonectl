package rclone

import (
	"fmt"
)

// APIError mirrors Python RcloneApiError: an rclone RC call failure with an
// optional HTTP status code and decoded response body.
type APIError struct {
	Msg        string
	StatusCode int // 0 when the request never reached rclone
	Body       map[string]any
}

func (e *APIError) Error() string { return e.Msg }

// BodyError returns the "error" field of the response body, if any.
func (e *APIError) BodyError() (string, bool) {
	if e == nil || e.Body == nil {
		return "", false
	}
	v, ok := e.Body["error"]
	if !ok || v == nil {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, true
	default:
		return fmt.Sprint(t), true
	}
}
