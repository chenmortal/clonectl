package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Every Python HTTPException body is serialized by FastAPI as
// {"detail": <as-passed>} — string or object. These helpers reproduce that
// exactly; the React errMessage() helper depends on the shapes.

// AbortDetail emits status + {"detail": detail} and aborts the chain.
func AbortDetail(c *gin.Context, status int, detail any) {
	c.AbortWithStatusJSON(status, gin.H{"detail": detail})
}

// AbortErr emits status + {"detail": {"error": ...}} (error-object detail).
func AbortErr(c *gin.Context, status int, errObj gin.H) {
	AbortDetail(c, status, errObj)
}

// AbortUnauthenticated emits FastAPI's 401 shape with the bearer challenge.
func AbortUnauthenticated(c *gin.Context, reason string) {
	c.Header("WWW-Authenticate", "Bearer")
	AbortDetail(c, http.StatusUnauthorized, gin.H{"error": "unauthenticated", "reason": reason})
}

// AbortForbidden emits the RBAC 403 shape (required roles sorted).
func AbortForbidden(c *gin.Context, required []string, actual string) {
	AbortDetail(c, http.StatusForbidden, gin.H{
		"error":    "forbidden",
		"required": sortedStrings(required),
		"actual":   actual,
	})
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
