package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/streamingfast/cosmos-docker-upgrader/internal/upgrader"

	"github.com/spf13/cobra"
	"github.com/streamingfast/logging"
	"go.uber.org/zap"
)

var (
	// Version information (set during build)
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

var zlog, _ = logging.PackageLogger("upgrader", "github.com/streamingfast/cosmos-docker-upgrader/cmd/cosmos-docker-upgrader")

func main() {
	// INFO by default so the status lines are always visible, DLOG still takes
	// precedence for turning individual loggers up or down.
	logging.InstantiateLoggers(
		logging.WithDefaultLevel(zap.InfoLevel),
		logging.WithConsoleToStdout(),
	)

	config := upgrader.Config{}

	rootCmd := &cobra.Command{
		Use:     "cosmos-docker-upgrader <ChainFolder> <DataFolder>",
		Short:   "Cosmos Docker Upgrader - Watches for upgrade-info.json and manages Docker Compose upgrades",
		Version: fmt.Sprintf("%s (built: %s, commit: %s)", Version, BuildTime, GitCommit),
		Long: `Cosmos Docker Upgrader watches for upgrade-info.json files in a data directory
and automatically manages Docker Compose upgrades for Cosmos chains.

Parameters:
  <ChainFolder>: Directory containing docker-compose.yml and docker-compose.yml-next files
  <DataFolder>:  Directory to watch for upgrade-info.json file appearances

Both folders are watched. The upgrade runs when docker-compose.yml-next and
upgrade-info.json are both present, in whichever order they appear. After a
successful upgrade, upgrade-info.json is renamed to upgrade-info.json-applied so
a later staged compose file does not trigger on a stale signal.

A status line is logged at startup, on every state change and on a heartbeat
(see --status-interval). Set DLOG=upgrader=debug for verbose file event logging.`,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			config.ChainFolder = args[0]
			config.DataFolder = args[1]

			return run(cmd.Context(), config)
		},
	}

	flags := rootCmd.Flags()
	flags.DurationVar(&config.StatusInterval, "status-interval", time.Hour, "How often to log a status line, 0 disables the heartbeat")
	flags.DurationVar(&config.PollInterval, "poll-interval", 30*time.Second, "How often to re-check both folders in case a filesystem event was missed, 0 disables polling")
	flags.DurationVar(&config.RetryInterval, "retry-interval", 5*time.Minute, "How long to wait before retrying a failed upgrade, restaging docker-compose.yml-next retries immediately")
	flags.DurationVar(&config.DebounceDelay, "debounce-delay", 500*time.Millisecond, "How long to wait after a filesystem event before acting, to let the file be fully written")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config upgrader.Config) error {
	zlog.Info("starting cosmos-docker-upgrader",
		zap.String("version", Version),
		zap.String("build_time", BuildTime),
		zap.String("git_commit", GitCommit),
		zap.String("chain_folder", config.ChainFolder),
		zap.String("data_folder", config.DataFolder),
		zap.Duration("status_interval", config.StatusInterval),
		zap.Duration("poll_interval", config.PollInterval),
		zap.Duration("retry_interval", config.RetryInterval),
	)

	return upgrader.New(config, upgrader.ExecRunner{}).Run(ctx)
}
