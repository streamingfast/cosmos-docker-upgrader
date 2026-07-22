package upgrader

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnapshot_State(t *testing.T) {
	tests := []struct {
		name        string
		composeNext bool
		upgradeInfo bool
		expected    State
	}{
		{"nothing staged, chain running", false, false, StateIdle},
		{"compose staged, chain running", true, false, StateArmed},
		{"chain halted, nothing staged", false, true, StateBlocked},
		{"chain halted, compose staged", true, true, StateUpgradeReady},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := Snapshot{}
			if test.composeNext {
				snapshot.ComposeNext = &FileStat{Path: DockerComposeNext}
			}
			if test.upgradeInfo {
				snapshot.UpgradeInfo = &FileStat{Path: UpgradeInfoFile}
			}

			assert.Equal(t, test.expected, snapshot.State())
		})
	}
}

func TestTakeSnapshot(t *testing.T) {
	t.Run("both folders empty", func(t *testing.T) {
		chainFolder, dataFolder := newFolders(t)

		snapshot := takeSnapshot(chainFolder, dataFolder)

		assert.Nil(t, snapshot.ComposeNext)
		assert.Nil(t, snapshot.UpgradeInfo)
		assert.Nil(t, snapshot.Plan)
		assert.NoError(t, snapshot.PlanParseErr)
		assert.Equal(t, StateIdle, snapshot.State())
	})

	t.Run("parses the upgrade plan", func(t *testing.T) {
		chainFolder, dataFolder := newFolders(t)
		writeUpgradeInfo(t, dataFolder, "v1.2.3", 4567)

		snapshot := takeSnapshot(chainFolder, dataFolder)

		require.NoError(t, snapshot.PlanParseErr)
		require.NotNil(t, snapshot.Plan)
		assert.Equal(t, "v1.2.3", snapshot.Plan.Name)
		assert.Equal(t, int64(4567), snapshot.Plan.Height)
	})

	t.Run("malformed upgrade info is present but unparseable", func(t *testing.T) {
		chainFolder, dataFolder := newFolders(t)
		writeFile(t, filepath.Join(dataFolder, UpgradeInfoFile), "not json at all")

		snapshot := takeSnapshot(chainFolder, dataFolder)

		require.NotNil(t, snapshot.UpgradeInfo, "the file is on disk so the chain is halted, the state machine must still see it")
		assert.Nil(t, snapshot.Plan)
		assert.Error(t, snapshot.PlanParseErr)
		assert.Equal(t, StateBlocked, snapshot.State())
	})

	t.Run("reports compose next size and modification time", func(t *testing.T) {
		chainFolder, dataFolder := newFolders(t)
		writeFile(t, filepath.Join(chainFolder, DockerComposeNext), "services: {}")

		snapshot := takeSnapshot(chainFolder, dataFolder)

		require.NotNil(t, snapshot.ComposeNext)
		assert.Equal(t, int64(len("services: {}")), snapshot.ComposeNext.Size)
		assert.False(t, snapshot.ComposeNext.ModTime.IsZero())
	})
}

func TestState_Message(t *testing.T) {
	for _, state := range []State{StateIdle, StateArmed, StateBlocked, StateUpgradeReady, StateRetryPending} {
		assert.NotEmpty(t, state.Message(), "state %s must have a human readable message", state)
		assert.NotContains(t, state.String(), "unknown")
	}
}
