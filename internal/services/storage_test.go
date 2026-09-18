package services

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"clonectl/internal/database"
)

// s3 remotes default to the classic ListObjects: old OSS builds reject
// ListObjectsV2, which rclone's auto mode otherwise picks (breaks the
// AK/SK verify probe).
func TestBuildRemoteParametersS3DefaultsListVersionV1(t *testing.T) {
	src := database.StorageSource{
		Type: "s3", Endpoint: strPtr("http://oss.internal:9000"),
	}
	ds := database.DataSource{AccessKeyID: strPtr("ak"), SecretAccessKey: strPtr("sk")}

	params := BuildRemoteParameters(&src, &ds)

	assert.Equal(t, "Other", params["provider"])
	assert.Equal(t, "1", params["list_version"])
	assert.Equal(t, "http://oss.internal:9000", params["endpoint"])
	assert.Equal(t, "ak", params["access_key_id"])
}

func TestBuildRemoteParametersS3ExtraListVersionWins(t *testing.T) {
	src := database.StorageSource{
		Type: "s3", Extra: database.JSONObject{"list_version": "2"},
	}

	params := BuildRemoteParameters(&src, &database.DataSource{})

	assert.Equal(t, "2", params["list_version"])
}

// local remotes stay credential-free and carry no list_version.
func TestBuildRemoteParametersLocalUntouched(t *testing.T) {
	src := database.StorageSource{Type: "local", Path: strPtr("/data")}

	params := BuildRemoteParameters(&src, &database.DataSource{})

	assert.Equal(t, database.JSONObject{}, params)
}
