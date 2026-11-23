package upgrader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// Config holds the tunables of the watcher. Zero durations disable the
// corresponding periodic behaviour.
type Config struct {
	ChainFolder string
	DataFolder  string

	// PollInterval re-checks both folders even when no fsnotify event arrived.
	// fsnotify can silently miss events on some filesystems and gives no way to
	// know, so the poll is the safety net that bounds how long a staged file can
	// go unnoticed.
	PollInterval time.Duration

	// StatusInterval is the heartbeat period. The timer is reset every time a
	// status line is logged for any reason, so a transition never produces a
	// duplicate heartbeat right after it.
	StatusInterval time.Duration

	// RetryInterval is how long to wait before retrying a failed upgrade. A
	// change to docker-compose.yml-next retries immediately regardless.
	RetryInterval time.Duration

	// DebounceDelay coalesces bursts of fsnotify events into a single evaluation.
	DebounceDelay time.Duration

	// HeightSource, when set, is queried whenever upgrade-info.json is present. A
	// node already past the plan height proves the signal is left over from an
	// upgrade applied out of band, so it is cleared instead of firing the next
	// staged compose file. Nil disables the check.
	HeightSource HeightSource

	// PrePull pulls the images of docker-compose.yml-next before stopping
	// anything, so a missing image aborts the upgrade with the chain untouched.
	PrePull bool

	// RestartPreviousOnFailure starts the previous containers again after a
	// failed start was rolled back. Off by default: on a real halt the previous
	// binary must not run, it could be fed blocks past the upgrade height.
	RestartPreviousOnFailure bool
}

// UpgradeResult records the outcome of the most recent upgrade attempt.
type UpgradeResult struct {
	Plan      UpgradePlan
	StartedAt time.Time
	Duration  time.Duration
	Err       error
}

// Upgrader watches the chain and data folders and performs the Docker Compose
// swap when both the staged compose file and the chain upgrade signal are present.
type Upgrader struct {
	config Config
	runner Runner

	mu              sync.Mutex
	state           State
	snapshot        Snapshot
	startedAt       time.Time
	upgradesApplied int
	lastUpgrade     *UpgradeResult
	retryAfter      time.Time
	failedNextMod   time.Time

	// statusTimer is nil when the heartbeat is disabled.
	statusTimer *time.Timer
}

func New(config Config, runner Runner) *Upgrader {
	return &Upgrader{
		config:    config,
		runner:    runner,
		state:     StateIdle,
		startedAt: time.Now(),
	}
}

// Run watches until the context is cancelled. It returns an error only for
// setup failures, runtime problems are logged and watching continues.
func (u *Upgrader) Run(ctx context.Context) error {
	if err := u.validate(); err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer watcher.Close()

	// Both folders are watched: the data folder for the chain's upgrade signal,
	// the chain folder so staging docker-compose.yml-next is visible immediately
	// instead of only being noticed when the chain halts.
	for _, folder := range []string{u.config.ChainFolder, u.config.DataFolder} {
		if err := watcher.Add(folder); err != nil {
			return fmt.Errorf("watch %s: %w", folder, err)
		}
		zlog.Info("watching folder", zap.String("folder", folder))
	}

	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	defer debounce.Stop()

	var pollC <-chan time.Time
	if u.config.PollInterval > 0 {
		poll := time.NewTicker(u.config.PollInterval)
		defer poll.Stop()
		pollC = poll.C
	}

	if u.config.StatusInterval > 0 {
		u.statusTimer = time.NewTimer(u.config.StatusInterval)
		defer u.statusTimer.Stop()
	}

	u.evaluate(ctx, "startup", true)

	for {
		var statusC <-chan time.Time
		if u.statusTimer != nil {
			statusC = u.statusTimer.C
		}

		select {
		case <-ctx.Done():
			zlog.Info("shutting down", zap.Duration("uptime", time.Since(u.startedAt)))
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			if !isRelevantEvent(event) {
				continue
			}

			if tracer.Enabled() {
				zlog.Debug("relevant file event, scheduling evaluation",
					zap.String("file", event.Name),
					zap.String("op", event.Op.String()),
				)
			}

			debounce.Reset(u.config.DebounceDelay)

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			zlog.Error("watcher error, continuing", zap.Error(err))

		case <-debounce.C:
			u.evaluate(ctx, "file_event", false)

		case <-pollC:
			u.evaluate(ctx, "poll", false)

		case <-statusC:
			u.mu.Lock()
			u.logStatus("heartbeat", u.state)
			u.mu.Unlock()
		}
	}
}

// isRelevantEvent filters out unrelated churn in the watched folders. The data
// folder in particular is a busy chain data directory.
func isRelevantEvent(event fsnotify.Event) bool {
	switch filepath.Base(event.Name) {
	case UpgradeInfoFile, DockerComposeNext, DockerComposeFile:
		return true
	default:
		return false
	}
}

func (u *Upgrader) validate() error {
	for name, folder := range map[string]string{"chain folder": u.config.ChainFolder, "data folder": u.config.DataFolder} {
		info, err := os.Stat(folder)
		if err != nil {
			return fmt.Errorf("%s is not accessible: %s: %w", name, folder, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory: %s", name, folder)
		}
	}

	composePath := filepath.Join(u.config.ChainFolder, DockerComposeFile)
	if _, err := os.Stat(composePath); err != nil {
		return fmt.Errorf("%s not found in chain folder: %s: %w", DockerComposeFile, composePath, err)
	}

	return nil
}

// evaluate re-reads the disk and reacts. Every trigger (startup, fsnotify, poll)
// funnels through here so a missed event costs at most one poll interval.
func (u *Upgrader) evaluate(ctx context.Context, trigger string, force bool) {
	u.mu.Lock()
	defer u.mu.Unlock()

	snapshot := u.clearStaleSignal(ctx, takeSnapshot(u.config.ChainFolder, u.config.DataFolder))

	if snapshot.PlanParseErr != nil {
		zlog.Warn("could not parse "+UpgradeInfoFile+", treating it as present but unreadable",
			zap.Error(snapshot.PlanParseErr),
		)
	}

	state := u.deriveState(snapshot)
	previous := u.state
	previousSnapshot := u.snapshot

	u.state = state
	u.snapshot = snapshot

	switch {
	case force || state != previous:
		u.logStatus(trigger, previous)
	default:
		u.logFileChanges(previousSnapshot, snapshot)
	}

	if state == StateUpgradeReady {
		u.runUpgrade(ctx, snapshot)
	}
}

// clearStaleSignal renames upgrade-info.json out of the way when the node reports
// a height past the plan, which means the upgrade was already applied by some
// other means (by hand, by a previous upgrader). Left in place, such a signal
// fires the next staged compose file the moment it appears. It returns the
// snapshot to act on.
//
// An unreachable node proves nothing, a halted chain usually has its RPC down,
// so the signal is then trusted as before. Once proven stale the file is moved
// rather than ignored, so a later RPC outage cannot bring it back to life.
func (u *Upgrader) clearStaleSignal(ctx context.Context, snapshot Snapshot) Snapshot {
	if u.config.HeightSource == nil || snapshot.UpgradeInfo == nil || snapshot.Plan == nil {
		return snapshot
	}

	height, err := u.config.HeightSource.LatestHeight(ctx)
	if err != nil {
		if tracer.Enabled() {
			zlog.Debug("node height unavailable, trusting "+UpgradeInfoFile, zap.Error(err))
		}
		return snapshot
	}

	// A halted node has committed at most plan height - 1, the upgrade block is
	// only ever committed by the upgraded binary.
	if height <= snapshot.Plan.Height {
		return snapshot
	}

	stalePath := filepath.Join(u.config.DataFolder, UpgradeInfoStale)
	zlog.Warn("stale "+UpgradeInfoFile+" detected, node is already past the upgrade height, clearing it",
		zap.String("upgrade_name", snapshot.Plan.Name),
		zap.Int64("upgrade_height", snapshot.Plan.Height),
		zap.Int64("node_height", height),
		zap.Time("upgrade_info_modified", snapshot.UpgradeInfo.ModTime),
		zap.String("from", snapshot.UpgradeInfo.Path),
		zap.String("to", stalePath),
	)

	if err := os.Rename(snapshot.UpgradeInfo.Path, stalePath); err != nil {
		// Still known stale, so it is ignored for this evaluation and the rename
		// is retried on the next one.
		zlog.Error("could not clear stale "+UpgradeInfoFile+", ignoring it, rename it by hand", zap.Error(err))
		snapshot.UpgradeInfo, snapshot.Plan, snapshot.PlanParseErr = nil, nil, nil
		return snapshot
	}

	return takeSnapshot(u.config.ChainFolder, u.config.DataFolder)
}

// deriveState layers retry bookkeeping on top of the disk-derived state so a
// failed upgrade does not re-run on every poll tick.
func (u *Upgrader) deriveState(snapshot Snapshot) State {
	state := snapshot.State()
	if state == StateUpgradeReady && u.retryBlocked(snapshot) {
		return StateRetryPending
	}

	return state
}

func (u *Upgrader) retryBlocked(snapshot Snapshot) bool {
	if u.retryAfter.IsZero() {
		return false
	}

	// A restaged compose file means the operator acted on the failure, retry now.
	if snapshot.ComposeNext != nil && !snapshot.ComposeNext.ModTime.Equal(u.failedNextMod) {
		return false
	}

	return time.Now().Before(u.retryAfter)
}

// logFileChanges reports meaningful changes that do not move the state machine,
// such as restaging docker-compose.yml-next with new content.
func (u *Upgrader) logFileChanges(previous, current Snapshot) {
	if previous.ComposeNext == nil || current.ComposeNext == nil {
		return
	}

	if previous.ComposeNext.ModTime.Equal(current.ComposeNext.ModTime) && previous.ComposeNext.Size == current.ComposeNext.Size {
		return
	}

	zlog.Info(DockerComposeNext+" was updated while staged",
		zap.Time("modified_at", current.ComposeNext.ModTime),
		zap.Int64("size", current.ComposeNext.Size),
	)
}

// runUpgrade performs the swap and then re-derives the state so the log shows
// where things landed. The caller must hold the lock.
func (u *Upgrader) runUpgrade(ctx context.Context, snapshot Snapshot) {
	plan := UpgradePlan{}
	if snapshot.Plan != nil {
		plan = *snapshot.Plan
	}

	startedAt := time.Now()
	zlog.Info("starting upgrade sequence",
		zap.String("upgrade_name", plan.Name),
		zap.Int64("upgrade_height", plan.Height),
		zap.String("chain_folder", u.config.ChainFolder),
	)

	err := u.performUpgrade(ctx, snapshot)
	result := &UpgradeResult{Plan: plan, StartedAt: startedAt, Duration: time.Since(startedAt), Err: err}
	u.lastUpgrade = result

	if err != nil {
		u.retryAfter = time.Now().Add(u.config.RetryInterval)
		if snapshot.ComposeNext != nil {
			u.failedNextMod = snapshot.ComposeNext.ModTime
		}

		zlog.Error("upgrade failed, "+UpgradeInfoFile+" left in place so it can be retried",
			zap.String("upgrade_name", plan.Name),
			zap.Int64("upgrade_height", plan.Height),
			zap.Duration("duration", result.Duration),
			zap.Time("retry_after", u.retryAfter),
			zap.Error(err),
		)
	} else {
		u.upgradesApplied++
		u.retryAfter = time.Time{}
		u.failedNextMod = time.Time{}

		zlog.Info("upgrade completed successfully",
			zap.String("upgrade_name", plan.Name),
			zap.Int64("upgrade_height", plan.Height),
			zap.Duration("duration", result.Duration),
			zap.Int("upgrades_applied", u.upgradesApplied),
		)
	}

	previous := u.state
	u.snapshot = takeSnapshot(u.config.ChainFolder, u.config.DataFolder)
	u.state = u.deriveState(u.snapshot)
	u.logStatus("upgrade_finished", previous)
}

// performUpgrade runs the swap sequence. Any failure past the backup puts the
// compose files back where they were, so the failed upgrade stays staged and
// can be retried.
func (u *Upgrader) performUpgrade(ctx context.Context, snapshot Snapshot) error {
	chainFolder := u.config.ChainFolder
	currentPath := filepath.Join(chainFolder, DockerComposeFile)
	backupPath := filepath.Join(chainFolder, DockerComposeBackup)
	nextPath := filepath.Join(chainFolder, DockerComposeNext)

	// Pulling first means a missing or mistyped image fails here, while the chain
	// is still running, instead of after it was stopped.
	if u.config.PrePull {
		zlog.Info("upgrade step 1/6: pulling staged images", zap.String("command", "docker-compose -f "+DockerComposeNext+" pull"))
		if err := u.runner.Run(ctx, chainFolder, "docker-compose", "-f", DockerComposeNext, "pull"); err != nil {
			return fmt.Errorf("step 1/6 pull staged images: %w (nothing was stopped)", err)
		}
	} else {
		zlog.Info("upgrade step 1/6: pulling staged images skipped, pre-pull disabled")
	}

	zlog.Info("upgrade step 2/6: stopping containers", zap.String("command", "docker-compose down"))
	if err := u.runner.Run(ctx, chainFolder, "docker-compose", "down"); err != nil {
		return fmt.Errorf("step 2/6 stop containers: %w", err)
	}

	zlog.Info("upgrade step 3/6: backing up current compose file",
		zap.String("from", currentPath),
		zap.String("to", backupPath),
	)
	if err := os.Rename(currentPath, backupPath); err != nil {
		return fmt.Errorf("step 3/6 backup %s: %w", DockerComposeFile, err)
	}

	zlog.Info("upgrade step 4/6: promoting staged compose file",
		zap.String("from", nextPath),
		zap.String("to", currentPath),
	)
	if err := os.Rename(nextPath, currentPath); err != nil {
		zlog.Error("promotion failed, restoring backup", zap.Error(err))
		if restoreErr := os.Rename(backupPath, currentPath); restoreErr != nil {
			return fmt.Errorf("step 4/6 promote %s: %w (restoring backup also failed: %v)", DockerComposeNext, err, restoreErr)
		}
		return fmt.Errorf("step 4/6 promote %s: %w (backup restored)", DockerComposeNext, err)
	}

	zlog.Info("upgrade step 5/6: starting containers", zap.String("command", "docker-compose up -d"))
	if err := u.runner.Run(ctx, chainFolder, "docker-compose", "up", "-d"); err != nil {
		return fmt.Errorf("step 5/6 start containers: %w (%s)", err, u.rollback(ctx, currentPath, backupPath, nextPath))
	}

	// Clearing the signal is what prevents the now-stale upgrade-info.json from
	// firing the next staged compose file the moment it is created. It is renamed
	// rather than deleted so the applied plan stays on disk for inspection.
	if snapshot.UpgradeInfo != nil {
		appliedPath := filepath.Join(u.config.DataFolder, UpgradeInfoApplied)
		zlog.Info("upgrade step 6/6: clearing upgrade signal",
			zap.String("from", snapshot.UpgradeInfo.Path),
			zap.String("to", appliedPath),
		)
		if err := os.Rename(snapshot.UpgradeInfo.Path, appliedPath); err != nil {
			return fmt.Errorf("step 6/6 clear %s: %w (containers are up, clear it manually or the next staged compose file will upgrade immediately)", UpgradeInfoFile, err)
		}
	}

	return nil
}

// rollback undoes a promotion whose containers failed to start: the promoted
// file goes back to docker-compose.yml-next and the backup back to
// docker-compose.yml, so the upgrade stays staged and the retry can run. The
// previous containers are only started again with RestartPreviousOnFailure. It
// returns a summary of what was done for the upgrade error.
func (u *Upgrader) rollback(ctx context.Context, currentPath, backupPath, nextPath string) string {
	zlog.Warn("start failed, rolling back to the previous compose file")

	// Whatever did start from the promoted file must go before it is swapped
	// out, docker-compose can only stop what its current file describes.
	if err := u.runner.Run(ctx, u.config.ChainFolder, "docker-compose", "down"); err != nil {
		zlog.Error("could not stop partially started containers, continuing rollback", zap.Error(err))
	}

	if err := os.Rename(currentPath, nextPath); err != nil {
		zlog.Error("rollback failed, could not move promoted compose file back", zap.Error(err))
		return fmt.Sprintf("rollback failed: unstage %s: %v", DockerComposeFile, err)
	}

	if err := os.Rename(backupPath, currentPath); err != nil {
		zlog.Error("rollback failed, could not restore backup", zap.Error(err))
		return fmt.Sprintf("rollback failed: restore %s: %v", DockerComposeBackup, err)
	}

	if !u.config.RestartPreviousOnFailure {
		zlog.Info("rolled back compose files, containers left stopped, upgrade stays staged for retry")
		return "compose files rolled back, containers left stopped"
	}

	if err := u.runner.Run(ctx, u.config.ChainFolder, "docker-compose", "up", "-d"); err != nil {
		zlog.Error("compose files rolled back but previous containers failed to start", zap.Error(err))
		return fmt.Sprintf("compose files rolled back, restarting previous containers failed: %v", err)
	}

	zlog.Info("rolled back, previous containers started again, upgrade stays staged for retry")
	return "rolled back, previous containers started"
}

// logStatus emits the single status line for this occasion. Every status log
// goes through here and resets the heartbeat timer, so an event-driven status
// and the hourly heartbeat never double up.
func (u *Upgrader) logStatus(reason string, previous State) {
	defer u.resetStatusTimer()

	fields := []zap.Field{
		zap.String("reason", reason),
		zap.String("state", u.state.String()),
		zap.Duration("uptime", time.Since(u.startedAt).Truncate(time.Second)),
		zap.String("chain_folder", u.config.ChainFolder),
		zap.String("data_folder", u.config.DataFolder),
		zap.Int("upgrades_applied", u.upgradesApplied),
	}

	if previous != u.state {
		fields = append(fields, zap.String("previous_state", previous.String()))
	}

	if next := u.snapshot.ComposeNext; next != nil {
		fields = append(fields,
			zap.Bool("compose_next_present", true),
			zap.Int64("compose_next_size", next.Size),
			zap.Time("compose_next_modified", next.ModTime),
			zap.Duration("compose_next_age", time.Since(next.ModTime).Truncate(time.Second)),
		)
	} else {
		fields = append(fields, zap.Bool("compose_next_present", false))
	}

	if info := u.snapshot.UpgradeInfo; info != nil {
		fields = append(fields,
			zap.Bool("upgrade_info_present", true),
			zap.Time("upgrade_info_modified", info.ModTime),
		)
		if plan := u.snapshot.Plan; plan != nil {
			fields = append(fields,
				zap.String("upgrade_name", plan.Name),
				zap.Int64("upgrade_height", plan.Height),
			)
		}
	} else {
		fields = append(fields, zap.Bool("upgrade_info_present", false))
	}

	if last := u.lastUpgrade; last != nil {
		outcome := "success"
		if last.Err != nil {
			outcome = "failure"
		}
		fields = append(fields,
			zap.String("last_upgrade_outcome", outcome),
			zap.String("last_upgrade_name", last.Plan.Name),
			zap.Time("last_upgrade_at", last.StartedAt),
		)
	}

	if !u.retryAfter.IsZero() {
		fields = append(fields, zap.Time("retry_after", u.retryAfter))
	}

	if u.state == StateBlocked || u.state == StateRetryPending {
		zlog.Warn(u.state.Message(), fields...)
		return
	}

	zlog.Info(u.state.Message(), fields...)
}

func (u *Upgrader) resetStatusTimer() {
	if u.statusTimer != nil {
		u.statusTimer.Reset(u.config.StatusInterval)
	}
}
