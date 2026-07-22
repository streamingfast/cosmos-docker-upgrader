package upgrader

import (
	"context"
	"os"
	"os/exec"
	"time"

	"go.uber.org/zap"
)

// Runner executes external commands. It is an interface so tests can exercise
// the upgrade sequence without Docker installed.
type Runner interface {
	Run(ctx context.Context, workDir string, name string, args ...string) error
}

// ExecRunner runs commands through os/exec, streaming their output to the
// process stdout/stderr so Docker Compose progress ends up in the same log.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, workDir string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	zlog.Info("running command",
		zap.String("work_dir", workDir),
		zap.String("command", name),
		zap.Strings("args", args),
	)

	startedAt := time.Now()
	err := cmd.Run()
	duration := time.Since(startedAt)

	if err != nil {
		zlog.Error("command failed",
			zap.String("command", name),
			zap.Strings("args", args),
			zap.Duration("duration", duration),
			zap.Error(err),
		)
		return err
	}

	zlog.Info("command succeeded",
		zap.String("command", name),
		zap.Strings("args", args),
		zap.Duration("duration", duration),
	)

	return nil
}
