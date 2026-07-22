package upgrader

import "github.com/streamingfast/logging"

// Tests live in the same package, so they log through the package's own zlog.
// Set DLOG=upgrader=debug to see it while running them.
func init() {
	logging.InstantiateLoggers()
}
