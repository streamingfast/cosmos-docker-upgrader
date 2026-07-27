package upgrader

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	UpgradeInfoFile     = "upgrade-info.json"
	UpgradeInfoApplied  = "upgrade-info.json-applied"
	DockerComposeFile   = "docker-compose.yml"
	DockerComposeNext   = "docker-compose.yml-next"
	DockerComposeBackup = "docker-compose.yml-backup"
)

// State is the observable condition of the watched folders, derived solely from
// what is present on disk. It exists to make the log output unambiguous: every
// status line names the state, and every state has a single meaning.
type State int

const (
	// StateIdle means nothing is staged and the chain is running normally.
	StateIdle State = iota

	// StateArmed means docker-compose.yml-next is staged and we are waiting for
	// the chain to write upgrade-info.json.
	StateArmed

	// StateBlocked means the chain wrote upgrade-info.json (it is halted) but no
	// docker-compose.yml-next is staged, so the upgrade cannot proceed.
	StateBlocked

	// StateUpgradeReady means both files are present and an upgrade will run.
	StateUpgradeReady

	// StateRetryPending means an upgrade was attempted and failed, and we are
	// waiting for the retry delay to elapse or for docker-compose.yml-next to change.
	StateRetryPending
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateArmed:
		return "armed"
	case StateBlocked:
		return "blocked"
	case StateUpgradeReady:
		return "upgrade_ready"
	case StateRetryPending:
		return "retry_pending"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Message returns the human sentence logged alongside the structured fields. It
// spells out what the state means so the log needs no interpretation.
func (s State) Message() string {
	switch s {
	case StateIdle:
		return "status: IDLE - no " + DockerComposeNext + " staged, chain running normally"
	case StateArmed:
		return "status: ARMED - " + DockerComposeNext + " staged, waiting for chain to write " + UpgradeInfoFile
	case StateBlocked:
		return "status: BLOCKED - chain wrote " + UpgradeInfoFile + " but no " + DockerComposeNext + " is staged, upgrade cannot proceed"
	case StateUpgradeReady:
		return "status: UPGRADE READY - both " + DockerComposeNext + " and " + UpgradeInfoFile + " present, starting upgrade"
	case StateRetryPending:
		return "status: RETRY PENDING - previous upgrade failed, waiting before retrying"
	default:
		return "status: unknown"
	}
}

// FileStat is the subset of file metadata we report on. A nil *FileStat means
// the file is absent.
type FileStat struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// UpgradePlan is the payload Cosmos SDK writes to upgrade-info.json when a
// planned upgrade height is reached.
type UpgradePlan struct {
	Name   string `json:"name"`
	Height int64  `json:"height"`
	Info   string `json:"info"`
	Time   string `json:"time,omitempty"`
}

// Snapshot is a point-in-time view of the two files that drive the state machine.
type Snapshot struct {
	ComposeNext  *FileStat
	UpgradeInfo  *FileStat
	Plan         *UpgradePlan
	PlanParseErr error
}

// State derives the state from the snapshot alone. Retry bookkeeping is layered
// on top by the Upgrader, this only reports what the disk says.
func (s Snapshot) State() State {
	switch {
	case s.ComposeNext != nil && s.UpgradeInfo != nil:
		return StateUpgradeReady
	case s.ComposeNext != nil:
		return StateArmed
	case s.UpgradeInfo != nil:
		return StateBlocked
	default:
		return StateIdle
	}
}

// takeSnapshot stats both files and parses upgrade-info.json when present. A
// missing file is not an error, an unreadable or malformed one is recorded in
// PlanParseErr so it can be logged without derailing the state machine.
func takeSnapshot(chainFolder, dataFolder string) Snapshot {
	snapshot := Snapshot{
		ComposeNext: statFile(filepath.Join(chainFolder, DockerComposeNext)),
		UpgradeInfo: statFile(filepath.Join(dataFolder, UpgradeInfoFile)),
	}

	if snapshot.UpgradeInfo != nil {
		plan, err := readUpgradePlan(snapshot.UpgradeInfo.Path)
		snapshot.Plan, snapshot.PlanParseErr = plan, err
	}

	return snapshot
}

func statFile(path string) *FileStat {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}

	return &FileStat{Path: path, Size: info.Size(), ModTime: info.ModTime()}
}

func readUpgradePlan(path string) (*UpgradePlan, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	plan := &UpgradePlan{}
	if err := json.Unmarshal(content, plan); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}

	return plan, nil
}
