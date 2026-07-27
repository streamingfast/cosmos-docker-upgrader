# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## Unreleased

### Added

- Publish a `.deb` package for `amd64` and `arm64` on every release. It installs the binary to `/usr/bin` along with a `cosmos-docker-upgrader@.service` systemd template unit, so one host can run one instance per chain, configured from `/etc/cosmos-docker-upgrader/<instance>.env`. Installable directly from the release URL with Ansible's `apt` module.
- Publish a `linux/arm64` binary in addition to `linux/amd64` and `darwin/arm64`.
- Build every release artifact inside a container from a `Dockerfile`, so binaries can be reproduced locally with the same command the workflow runs.
- Add `LOG_FORMAT=json` for structured JSON output. The format is now pinned rather than auto-detected, so it no longer silently switches to JSON when running inside a container.
- Log a clear status line whenever `docker-compose.yml-next` is staged, so it is immediately visible in the log that the upgrade was seen and is armed.
- Add a status heartbeat, logged at startup, on every state change and every hour by default. Configure with `--status-interval`, `0` disables it. The heartbeat timer resets on every status line, so an event never produces a duplicate heartbeat right after it.
- Watch the chain folder in addition to the data folder, so staging `docker-compose.yml-next` is noticed right away instead of only when the chain halts.
- Poll both folders every 30 seconds as a safety net for filesystem events that are silently missed. Configure with `--poll-interval`, `0` disables it.
- Retry a failed upgrade after 5 minutes instead of never. Restaging `docker-compose.yml-next` retries immediately. Configure with `--retry-interval`.
- Add `--debounce-delay` to control how long to wait after a filesystem event before acting.
- Rename `upgrade-info.json` to `upgrade-info.json-applied` after a successful upgrade, so the applied plan stays on disk for inspection.

### Changed

- Change the module path to `github.com/streamingfast/cosmos-docker-upgrader` so it matches the repository. `go install github.com/streamingfast/cosmos-docker-upgrader/cmd/cosmos-docker-upgrader@latest` now works.
- Replace the standard library logger with structured `zap` logging through `github.com/streamingfast/logging`. Output stays human readable on a terminal and drops colors when redirected to a file. Set `DLOG=upgrader=debug` for verbose filesystem event logging.
- Every upgrade step now logs its start, its duration and its outcome, including the exact commands run.

### Fixed

- Run the upgrade when `docker-compose.yml-next` and `upgrade-info.json` are both present regardless of arrival order. Previously the staged compose file was only checked at the moment `upgrade-info.json` appeared, so staging it after the chain had already halted left the node down indefinitely.
- Do not trigger an upgrade from a stale `upgrade-info.json` left over from a previous upgrade. The signal is now cleared once applied.
- Stop retrying a failing upgrade on every filesystem event.
