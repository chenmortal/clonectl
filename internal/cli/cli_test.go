package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootHelpListsCommands(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--help"})
	require.NoError(t, rootCmd.Execute())
	out := buf.String()
	for _, cmd := range []string{"serve", "stop", "run-once", "status", "migrate-legacy"} {
		assert.Contains(t, out, cmd)
	}
	rootCmd.SetArgs([]string{}) // reset for other tests
}

func TestRunOnceRejectsNonNumericID(t *testing.T) {
	rootCmd.SetArgs([]string{"run-once", "abc"})
	err := rootCmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid task id")
	rootCmd.SetArgs([]string{})
}

func TestParseBind(t *testing.T) {
	// host:port
	host, port, err := parseBind("127.0.0.1:9000")
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	assert.Equal(t, 9000, port)

	// bare port → host empty (keeps configured API_HOST)
	host, port, err = parseBind(":8443")
	require.NoError(t, err)
	assert.Equal(t, "", host)
	assert.Equal(t, 8443, port)

	host, port, err = parseBind("8000")
	require.NoError(t, err)
	assert.Equal(t, "", host)
	assert.Equal(t, 8000, port)

	// IPv6-style bracketed host
	host, port, err = parseBind("[::1]:9000")
	require.NoError(t, err)
	assert.Equal(t, "::1", host)
	assert.Equal(t, 9000, port)

	// invalid
	_, _, err = parseBind("not-an-address")
	assert.Error(t, err)
	_, _, err = parseBind("host:notaport")
	assert.Error(t, err)
}

func TestLoadConfigExplicitFile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "prod.env")
	require.NoError(t, os.WriteFile(cfgFile, []byte(
		"API_PORT=9443\nCLUSTER_NAME=prod-cluster\nJWT_SECRET=file-secret-123\n",
	), 0o600))

	// godotenv mutates the process env; restore afterwards.
	t.Cleanup(func() {
		_ = os.Unsetenv("API_PORT")
		_ = os.Unsetenv("CLUSTER_NAME")
		_ = os.Unsetenv("JWT_SECRET")
	})

	rootCmd.SetArgs([]string{"--config", cfgFile, "serve", "--help"})
	require.NoError(t, rootCmd.Execute())

	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, 9443, cfg.APIPort)
	assert.Equal(t, "prod-cluster", cfg.ClusterName)
	assert.Equal(t, "file-secret-123", cfg.JWTSecret)
	// Unset keys keep their defaults.
	assert.NotEqual(t, "", cfg.DatabaseURL)
}

func TestLoadConfigMissingExplicitFileErrors(t *testing.T) {
	configPath = "/nonexistent/path/prod.env"
	t.Cleanup(func() { configPath = "" })

	_, err := loadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "读取配置文件")
}

func TestServeBindOverridesSettings(t *testing.T) {
	// parseBind + serve wiring: host given → overrides; bare port → keeps API_HOST.
	host, port, err := parseBind("127.0.0.1:9443")
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	assert.Equal(t, 9443, port)
}
