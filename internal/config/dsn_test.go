package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDatabaseURL(t *testing.T) {
	cases := []struct {
		name, in, dialect, dsn string
	}{
		{"sqlite relative", "sqlite:///./x.db", "sqlite", "file:./x.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"},
		{"sqlite bare name", "sqlite:///app.db", "sqlite", "file:app.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"},
		{"sqlite absolute", "sqlite:////var/lib/app.db", "sqlite", "file:/var/lib/app.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"},
		{"sqlite memory", "sqlite:///:memory:", "sqlite", "file::memory:?cache=shared"},
		{"sqlite empty", "sqlite://", "sqlite", "file::memory:?cache=shared"},
		{"bare file", "./local.db", "sqlite", "file:./local.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"},
		{"mysql plain", "mysql://user:pass@dbhost:3307/rclone", "mysql",
			"user:pass@tcp(dbhost:3307)/rclone?parseTime=true&loc=UTC&charset=utf8mb4"},
		{"mysql pymysql driver suffix", "mysql+pymysql://user:pass@dbhost/rclone?kw=1", "mysql",
			"user:pass@tcp(dbhost:3306)/rclone?parseTime=true&loc=UTC&charset=utf8mb4&kw=1"},
		{"mysql default port", "mysql://u@127.0.0.1/db", "mysql",
			"u@tcp(127.0.0.1:3306)/db?parseTime=true&loc=UTC&charset=utf8mb4"},
		{"native go dsn passthrough", "user:pass@tcp(10.0.0.1:3306)/rclone_sync", "mysql",
			"user:pass@tcp(10.0.0.1:3306)/rclone_sync?parseTime=true&loc=UTC&charset=utf8mb4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dialect, dsn, err := ParseDatabaseURL(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.dialect, dialect)
			assert.Equal(t, tc.dsn, dsn)
		})
	}
}

func TestParseDatabaseURLEmptyDefaultsToSQLite(t *testing.T) {
	dialect, dsn, err := ParseDatabaseURL("  ")
	require.NoError(t, err)
	assert.Equal(t, "sqlite", dialect)
	assert.True(t, strings.HasPrefix(dsn, "file:"))
}

func TestParseDatabaseURLUnsupported(t *testing.T) {
	_, _, err := ParseDatabaseURL("postgres://u@h/db")
	assert.Error(t, err)
}
