package api

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"clonectl/internal/database"
)

// authTransport injects the rclone rcd basic-auth credentials on every
// forwarded request (parity: httpx.Client(auth=...) on the Python side).
type authTransport struct {
	base http.RoundTripper
	user string
	pass string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.SetBasicAuth(t.user, t.pass)
	return t.base.RoundTrip(out)
}

// NewRcloneProxy builds the /rclone/* reverse proxy. Header strip lists and
// the 502 error body mirror app/api/proxy.py exactly.
func NewRcloneProxy(upstream *url.URL, rcUser, rcPass string) *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{FlushInterval: 100}

	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		pr.SetURL(upstream)
		pr.Out.Host = upstream.Host
		// /rclone/<path> → <path> (httpx base_url semantics).
		pr.Out.URL.Path = strings.TrimPrefix(pr.Out.URL.Path, "/rclone")
		if pr.Out.URL.Path == "" {
			pr.Out.URL.Path = "/"
		}
		// Stripped request headers (Python): host, authorization, content-length.
		pr.Out.Header.Del("Authorization")
		pr.Out.Header.Del("Content-Length")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Stripped response headers (Python): content-encoding, content-length,
		// transfer-encoding, connection, keep-alive.
		for _, h := range []string{"Content-Encoding", "Content-Length",
			"Transfer-Encoding", "Connection", "Keep-Alive"} {
			resp.Header.Del(h)
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"detail":"rclone rcd unreachable: ` + jsonEscape(err.Error()) + `"}`))
	}
	proxy.Transport = &authTransport{base: http.DefaultTransport, user: rcUser, pass: rcPass}
	return proxy
}

func jsonEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// ProxyMethodGate enforces the method-based ACL: reads for every role,
// writes (POST/PUT/DELETE/PATCH) admin-only.
func (d *Deps) ProxyMethodGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		default:
			user := CurrentUser(c)
			if user == nil {
				AbortUnauthenticated(c, "missing bearer token")
				return
			}
			if user.Role != database.RoleAdmin {
				AbortForbidden(c, []string{"admin"}, user.Role)
				return
			}
			c.Next()
		}
	}
}

// bareWriter exposes only the http.ResponseWriter core so httputil.ReverseProxy
// skips the deprecated CloseNotifier path (gin's writer panics on it under
// httptest; behavior is identical without it).
type bareWriter struct{ w gin.ResponseWriter }

func (b bareWriter) Header() http.Header         { return b.w.Header() }
func (b bareWriter) Write(p []byte) (int, error) { return b.w.Write(p) }
func (b bareWriter) WriteHeader(code int)        { b.w.WriteHeader(code) }
func (b bareWriter) Flush()                      { b.w.Flush() }

// ProxyTo returns a handler forwarding to the pre-built proxy.
func ProxyTo(p *httputil.ReverseProxy) gin.HandlerFunc {
	return func(c *gin.Context) {
		p.ServeHTTP(bareWriter{w: c.Writer}, c.Request)
	}
}
