package upgrader

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluate_StagingOrderDoesNotMatter(t *testing.T) {
	t.Run("compose staged first, then the chain halts", func(t *testing.T) {
		fixture := newFixture(t)

		fixture.stageComposeNext("image: chain:v2")
		fixture.evaluate()
		assert.Equal(t, StateArmed, fixture.upgrader.state)
		assert.Empty(t, fixture.runner.commands, "no upgrade until the chain halts")

		fixture.writeUpgradeInfo("v2.0.0", 100)
		fixture.evaluate()

		assert.Equal(t, []string{"docker-compose down", "docker-compose up -d"}, fixture.runner.commands)
		fixture.assertUpgradeApplied("image: chain:v2")
	})

	// This is the case the previous implementation could never recover from: the
	// chain had already written its signal, so no further event ever arrived to
	// re-check for the staged compose file.
	t.Run("chain halts first, then compose is staged", func(t *testing.T) {
		fixture := newFixture(t)

		fixture.writeUpgradeInfo("v2.0.0", 100)
		fixture.evaluate()
		assert.Equal(t, StateBlocked, fixture.upgrader.state)
		assert.Empty(t, fixture.runner.commands)

		fixture.stageComposeNext("image: chain:v2")
		fixture.evaluate()

		assert.Equal(t, []string{"docker-compose down", "docker-compose up -d"}, fixture.runner.commands)
		fixture.assertUpgradeApplied("image: chain:v2")
	})
}

func TestEvaluate_StaleUpgradeSignalDoesNotRetrigger(t *testing.T) {
	fixture := newFixture(t)

	fixture.stageComposeNext("image: chain:v2")
	fixture.writeUpgradeInfo("v2.0.0", 100)
	fixture.evaluate()
	require.Equal(t, 1, fixture.upgrader.upgradesApplied)

	// Months later, the next upgrade is prepared. The signal from the previous
	// upgrade must not fire it.
	fixture.runner.commands = nil
	fixture.stageComposeNext("image: chain:v3")
	fixture.evaluate()

	assert.Equal(t, StateArmed, fixture.upgrader.state)
	assert.Empty(t, fixture.runner.commands, "stale upgrade-info.json-applied must not trigger an upgrade")
	assert.Equal(t, 1, fixture.upgrader.upgradesApplied)
}

func TestEvaluate_ClearsUpgradeSignalOnSuccessOnly(t *testing.T) {
	t.Run("success renames the signal", func(t *testing.T) {
		fixture := newFixture(t)

		fixture.stageComposeNext("image: chain:v2")
		fixture.writeUpgradeInfo("v2.0.0", 100)
		fixture.evaluate()

		assert.NoFileExists(t, filepath.Join(fixture.dataFolder, UpgradeInfoFile))
		assert.FileExists(t, filepath.Join(fixture.dataFolder, UpgradeInfoApplied))
		assert.Equal(t, StateIdle, fixture.upgrader.state)
	})

	t.Run("failure leaves the signal in place", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.runner.failOn("docker-compose down")

		fixture.stageComposeNext("image: chain:v2")
		fixture.writeUpgradeInfo("v2.0.0", 100)
		fixture.evaluate()

		assert.FileExists(t, filepath.Join(fixture.dataFolder, UpgradeInfoFile), "a failed upgrade stays armed so it can be retried")
		assert.NoFileExists(t, filepath.Join(fixture.dataFolder, UpgradeInfoApplied))
		assert.FileExists(t, filepath.Join(fixture.chainFolder, DockerComposeNext))
		assert.Equal(t, 0, fixture.upgrader.upgradesApplied)
		assert.Equal(t, "image: chain:v1", fixture.readCompose(), "compose file must be untouched when the stop step fails")
	})
}

func TestEvaluate_FailedUpgradeDoesNotRetryUntilRetryIntervalElapses(t *testing.T) {
	fixture := newFixture(t)
	fixture.upgrader.config.RetryInterval = time.Hour
	fixture.runner.failOn("docker-compose down")

	fixture.stageComposeNext("image: chain:v2")
	fixture.writeUpgradeInfo("v2.0.0", 100)
	fixture.evaluate()
	require.Equal(t, []string{"docker-compose down"}, fixture.runner.commands)

	fixture.runner.commands = nil
	fixture.evaluate()

	assert.Equal(t, StateRetryPending, fixture.upgrader.state)
	assert.Empty(t, fixture.runner.commands, "must not hammer docker-compose on every poll tick")
}

func TestEvaluate_RestagingComposeNextRetriesImmediately(t *testing.T) {
	fixture := newFixture(t)
	fixture.upgrader.config.RetryInterval = time.Hour
	fixture.runner.failOn("docker-compose down")

	fixture.stageComposeNext("image: chain:v2-broken")
	fixture.writeUpgradeInfo("v2.0.0", 100)
	fixture.evaluate()
	require.Equal(t, StateRetryPending, fixture.upgrader.deriveState(fixture.upgrader.snapshot))

	fixture.runner.commands = nil
	fixture.runner.failures = nil
	fixture.stageComposeNext("image: chain:v2-fixed")
	fixture.evaluate()

	assert.Equal(t, []string{"docker-compose down", "docker-compose up -d"}, fixture.runner.commands)
	fixture.assertUpgradeApplied("image: chain:v2-fixed")
}

func TestPerformUpgrade_RestoresBackupWhenPromotionFails(t *testing.T) {
	fixture := newFixture(t)

	// Removing the staged file while the containers are stopping makes the
	// promotion step fail after the backup has already been taken.
	fixture.runner.hook("docker-compose down", func() {
		require.NoError(t, os.Remove(filepath.Join(fixture.chainFolder, DockerComposeNext)))
	})

	fixture.stageComposeNext("image: chain:v2")
	fixture.writeUpgradeInfo("v2.0.0", 100)
	fixture.evaluate()

	assert.Equal(t, "image: chain:v1", fixture.readCompose(), "the original compose file must be restored")
	assert.Equal(t, []string{"docker-compose down"}, fixture.runner.commands, "containers must not be started with a half applied upgrade")
	assert.FileExists(t, filepath.Join(fixture.dataFolder, UpgradeInfoFile))
	assert.Equal(t, 0, fixture.upgrader.upgradesApplied)
}

func TestValidate(t *testing.T) {
	t.Run("passes on a well formed layout", func(t *testing.T) {
		fixture := newFixture(t)
		assert.NoError(t, fixture.upgrader.validate())
	})

	t.Run("fails when the chain folder is missing", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.upgrader.config.ChainFolder = filepath.Join(fixture.chainFolder, "nope")

		assert.ErrorContains(t, fixture.upgrader.validate(), "chain folder is not accessible")
	})

	t.Run("fails when docker-compose.yml is missing", func(t *testing.T) {
		fixture := newFixture(t)
		require.NoError(t, os.Remove(filepath.Join(fixture.chainFolder, DockerComposeFile)))

		assert.ErrorContains(t, fixture.upgrader.validate(), DockerComposeFile+" not found in chain folder")
	})
}

// fixture wires an Upgrader over two temporary folders and a fake runner.
type fixture struct {
	t           *testing.T
	chainFolder string
	dataFolder  string
	runner      *fakeRunner
	upgrader    *Upgrader
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	chainFolder, dataFolder := newFolders(t)
	writeFile(t, filepath.Join(chainFolder, DockerComposeFile), "image: chain:v1")

	runner := &fakeRunner{}

	return &fixture{
		t:           t,
		chainFolder: chainFolder,
		dataFolder:  dataFolder,
		runner:      runner,
		upgrader: New(Config{
			ChainFolder:   chainFolder,
			DataFolder:    dataFolder,
			RetryInterval: time.Minute,
		}, runner),
	}
}

func (f *fixture) evaluate() {
	f.t.Helper()
	f.upgrader.evaluate(context.Background(), "test", false)
}

// stageComposeNext writes docker-compose.yml-next with a modification time far
// enough apart that the retry logic can tell two stagings apart on filesystems
// with coarse timestamp granularity.
func (f *fixture) stageComposeNext(content string) {
	f.t.Helper()

	path := filepath.Join(f.chainFolder, DockerComposeNext)
	writeFile(f.t, path, content)
	require.NoError(f.t, os.Chtimes(path, time.Now(), time.Now().Add(time.Duration(len(content))*time.Second)))
}

func (f *fixture) writeUpgradeInfo(name string, height int64) {
	f.t.Helper()
	writeUpgradeInfo(f.t, f.dataFolder, name, height)
}

func (f *fixture) readCompose() string {
	f.t.Helper()

	content, err := os.ReadFile(filepath.Join(f.chainFolder, DockerComposeFile))
	require.NoError(f.t, err)

	return string(content)
}

func (f *fixture) assertUpgradeApplied(expectedCompose string) {
	f.t.Helper()

	assert.Equal(f.t, expectedCompose, f.readCompose(), "the staged compose file must be promoted")
	assert.FileExists(f.t, filepath.Join(f.chainFolder, DockerComposeBackup))
	assert.NoFileExists(f.t, filepath.Join(f.chainFolder, DockerComposeNext))
	assert.NoFileExists(f.t, filepath.Join(f.dataFolder, UpgradeInfoFile))
	assert.Equal(f.t, StateIdle, f.upgrader.state)
}

// fakeRunner records commands instead of running them, and can be told to fail
// or to mutate the filesystem when a given command is reached.
type fakeRunner struct {
	commands []string
	failures map[string]bool
	hooks    map[string]func()
}

func (r *fakeRunner) failOn(command string) {
	if r.failures == nil {
		r.failures = map[string]bool{}
	}
	r.failures[command] = true
}

func (r *fakeRunner) hook(command string, fn func()) {
	if r.hooks == nil {
		r.hooks = map[string]func(){}
	}
	r.hooks[command] = fn
}

func (r *fakeRunner) Run(_ context.Context, _ string, name string, args ...string) error {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	r.commands = append(r.commands, command)

	if hook, found := r.hooks[command]; found {
		hook()
	}

	if r.failures[command] {
		return fmt.Errorf("fake failure running %q", command)
	}

	return nil
}

func newFolders(t *testing.T) (chainFolder string, dataFolder string) {
	t.Helper()

	chainFolder = t.TempDir()
	dataFolder = filepath.Join(chainFolder, "data")
	require.NoError(t, os.MkdirAll(dataFolder, 0755))

	return chainFolder, dataFolder
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

func writeUpgradeInfo(t *testing.T, dataFolder string, name string, height int64) {
	t.Helper()

	content, err := json.Marshal(UpgradePlan{Name: name, Height: height, Info: "https://example.com/" + name})
	require.NoError(t, err)

	writeFile(t, filepath.Join(dataFolder, UpgradeInfoFile), string(content))
}
