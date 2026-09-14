package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseDatabaseURL converts the Python-style DATABASE_URL into a GORM
// dialect name + connection DSN.
//
// Supported forms:
//
//	sqlite:///./x.db            → sqlite, file:./x.db   (3 slashes = relative)
//	sqlite:////abs/x.db         → sqlite, file:/abs/x.db (4 slashes = absolute)
//	sqlite:// / sqlite:///:memory: → sqlite in-memory (shared cache)
//	mysql://… / mysql+pymysql://… / mysql+aiomysql://… → mysql (driver suffix stripped)
//	user:pass@tcp(host)/db?…    → mysql as-is (native Go DSN)
//	./x.db / x.sqlite3          → sqlite file
func ParseDatabaseURL(raw string) (dialect, dsn string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "sqlite:///./rclone_sync.db"
	}

	switch {
	case strings.HasPrefix(raw, "sqlite"):
		return parseSQLite(raw)
	case strings.HasPrefix(raw, "mysql+"):
		// mysql+pymysql://u:p@host/db → mysql://u:p@host/db
		i := strings.Index(raw, "://")
		if i < 0 {
			return "", "", fmt.Errorf("invalid mysql DATABASE_URL: %q", raw)
		}
		return parseMySQLURL("mysql://" + raw[i+3:])
	case strings.HasPrefix(raw, "mysql"):
		return parseMySQLURL(raw)
	case strings.Contains(raw, "@tcp("):
		sep := "?"
		if strings.Contains(raw, "?") {
			sep = "&"
		}
		return "mysql", raw + sep + forceMySQLParams, nil
	case strings.HasSuffix(raw, ".db") || strings.HasSuffix(raw, ".sqlite") || strings.HasSuffix(raw, ".sqlite3"):
		return parseSQLite("sqlite://" + raw)
	default:
		return "", "", fmt.Errorf("unsupported DATABASE_URL: %q", raw)
	}
}

func parseSQLite(raw string) (string, string, error) {
	rest := strings.TrimPrefix(raw, "sqlite:")
	rest = strings.TrimPrefix(rest, "//") // "" or "/./x.db" or "/:memory:"

	path := rest
	switch {
	case path == "" || path == "/" || path == "/:memory:" || path == ":memory:":
		// Shared-cache in-memory so GORM's pool can see one DB.
		return "sqlite", "file::memory:?cache=shared", nil
	default:
		// "/./x.db" → "./x.db"; "/x.db" stays absolute "/x.db".
		path = strings.TrimPrefix(path, "/")
		if path == "" {
			return "sqlite", "file::memory:?cache=shared", nil
		}
		pragmas := "_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
		return "sqlite", "file:" + path + "?" + pragmas, nil
	}
}

func parseMySQLURL(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid mysql DATABASE_URL: %w", err)
	}
	host := u.Host
	if host == "" {
		host = "127.0.0.1:3306"
	} else if !strings.Contains(host, ":") {
		host += ":3306"
	}
	dsn := ""
	if u.User != nil {
		dsn = u.User.String() + "@"
	}
	dsn += "tcp(" + host + ")" + u.Path

	params := forceMySQLParams
	for k, vs := range u.Query() {
		if len(vs) == 0 {
			continue
		}
		switch k {
		case "parseTime", "loc", "charset": // forced below
			continue
		}
		params += "&" + k + "=" + url.QueryEscape(vs[0])
	}
	return "mysql", dsn + "?" + params, nil
}

// forceMySQLParams pins the params GORM's mysql driver needs to scan
// DATETIME into time.Time in UTC (Python side used naive UTC).
const forceMySQLParams = "parseTime=true&loc=UTC&charset=utf8mb4"
