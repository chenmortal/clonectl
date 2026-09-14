package cli

import (
	"bytes"
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
	for _, cmd := range []string{"serve", "stop", "run-once", "status"} {
		assert.Contains(t, out, cmd)
	}
}

func TestRunOnceRejectsNonNumericID(t *testing.T) {
	rootCmd.SetArgs([]string{"run-once", "abc"})
	err := rootCmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid task id")
	rootCmd.SetArgs([]string{}) // reset for later tests
}
