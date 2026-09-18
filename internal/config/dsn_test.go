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
		{"go dsn passthrough", "user:pass@tcp(10.0.0.1:3306)/clonectl", "mysql",
			"user:pass@tcp(10.0.0.1:3306)/clonectl?parseTime=true&loc=UTC&charset=utf8mb4"},
		{"go dsn special-char username", "root@tmast#ob:pass@tcp(host:3306)/clonectl", "mysql",
			"root@tmast#ob:pass@tcp(host:3306)/clonectl?parseTime=true&loc=UTC&charset=utf8mb4"},
		{"go dsn with existing params", "user:pass@tcp(10.0.0.1:3306)/clonectl?kw=1", "mysql",
			"user:pass@tcp(10.0.0.1:3306)/clonectl?kw=1&parseTime=true&loc=UTC&charset=utf8mb4"},
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

func TestParseDatabaseURLRejectsMySQLURL(t *testing.T) {
	_, _, err := ParseDatabaseURL("mysql://root@tmast#ob:pass@host:3306/clonectl")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tcp(host:3306)")
}
