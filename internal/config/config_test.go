package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	s := Load()
	assert.Equal(t, "sqlite:///./clonectl.db", s.DatabaseURL)
	assert.Equal(t, "0.0.0.0:5572", s.RcloneRCAddr)
	assert.Equal(t, "admin", s.RcloneRCUser)
	assert.Equal(t, "6051", s.RcloneRCPass)
	assert.True(t, s.RcloneManaged)
	assert.Equal(t, 10, s.PollInterval)
	assert.Equal(t, 3600, s.CheckTimeout)
	assert.Equal(t, 8000, s.APIPort)
	assert.Equal(t, "INFO", s.LogLevel)
	assert.Equal(t, "", s.NodeID)
	assert.Equal(t, "default", s.ClusterName)
	assert.Equal(t, 480, s.JWTExpiresMin)
	assert.Equal(t, 12, s.BcryptRounds)
	assert.NoError(t, s.Validate())
}

func TestLoadEnvOverride(t *testing.T) {
	t.Setenv("API_PORT", "9001")
	t.Setenv("RCLONE_MANAGED", "false")
	t.Setenv("POLL_INTERVAL_SECONDS", "5")
	t.Setenv("CLUSTER_NAME", "prod")
	s := Load()
	assert.Equal(t, 9001, s.APIPort)
	assert.False(t, s.RcloneManaged)
	assert.Equal(t, 5, s.PollInterval)
	assert.Equal(t, "prod", s.ClusterName)
}

func TestValidateEmptySecret(t *testing.T) {
	// Empty JWT secret can't happen via Load (default applies), so construct
	// directly to pin the guard.
	s := Settings{}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET")
}
