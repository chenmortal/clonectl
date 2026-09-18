package config

import (
	"fmt"
	"strings"
)

// ParseDatabaseURL converts DATABASE_URL into a GORM dialect name +
// connection DSN.
//
// Supported forms:
//
//	sqlite:///./x.db            → sqlite, file:./x.db   (3 slashes = relative)
//	sqlite:////abs/x.db         → sqlite, file:/abs/x.db (4 slashes = absolute)
//	sqlite:// / sqlite:///:memory: → sqlite in-memory (shared cache)
//	user:pass@tcp(host)/db?…    → mysql as-is (native Go DSN)
//	./x.db / x.sqlite3          → sqlite file
//
// MySQL only accepts the native Go DSN — mysql://… URLs are rejected, not
// translated. Go DSNs take credentials literally (userinfo ends at the last
// "@"), so usernames like "root@tmast#ob" need no escaping.
func ParseDatabaseURL(raw string) (dialect, dsn string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "sqlite:///./clonectl.db"
	}

	switch {
	case strings.HasPrefix(raw, "sqlite"):
		return parseSQLite(raw)
	case strings.HasPrefix(raw, "mysql"):
		return "", "", fmt.Errorf(`mysql DATABASE_URL must be a Go DSN like "user:pass@tcp(host:3306)/clonectl", got %q`, raw)
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

// forceMySQLParams pins the params GORM's mysql driver needs to scan
// DATETIME into time.Time in UTC (Python side used naive UTC).
const forceMySQLParams = "parseTime=true&loc=UTC&charset=utf8mb4"
