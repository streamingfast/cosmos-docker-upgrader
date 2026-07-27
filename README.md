# Cosmos Docker Upgrader

A Go program that watches for Cosmos blockchain upgrade signals and automatically manages Docker Compose upgrades.

## Overview

This tool monitors a data directory for the appearance of `upgrade-info.json` files (typically created by Cosmos SDK chains during planned upgrades) and automatically performs Docker Compose service upgrades when a new compose file is available.

## Features

- Watches both the chain folder and the data folder, so staging `docker-compose.yml-next` shows up in the log right away
- Runs the upgrade when `docker-compose.yml-next` and `upgrade-info.json` are both present, in whichever order they appear
- Clears the upgrade signal once applied, so a stale `upgrade-info.json` never triggers a later staged compose file
- Structured logging with a status line at startup, on every state change and on an hourly heartbeat
- Polls as a safety net for filesystem events that are silently missed
- Safe upgrade process with backup creation and rollback on failure
- Retries a failed upgrade instead of giving up

## Installation

### Option 1: Debian Package (recommended on Ubuntu)

Each release publishes a `.deb` for `amd64` and `arm64`. It installs the binary to
`/usr/bin/cosmos-docker-upgrader` and a systemd template unit, so one host can run
one instance per chain.

```bash
version=1.0.0
curl -L -O https://github.com/streamingfast/cosmos-docker-upgrader/releases/download/v${version}/cosmos-docker-upgrader_${version}_amd64.deb
sudo apt install ./cosmos-docker-upgrader_${version}_amd64.deb
```

Then configure an instance and start it. The instance name is whatever you put after
the `@`, and it selects the matching env file:

```bash
sudo cp /usr/share/cosmos-docker-upgrader/example.env \
        /etc/cosmos-docker-upgrader/injective-evm-testnet.env
sudo editor /etc/cosmos-docker-upgrader/injective-evm-testnet.env

sudo systemctl enable --now cosmos-docker-upgrader@injective-evm-testnet
```

The unit pipes output through `rotatelogs` to the `LOG_FILE` set in the env file.
Install `apache2-utils` if it is not already present, it provides `rotatelogs`.

#### With Ansible

```yaml
- name: Install cosmos-docker-upgrader
  ansible.builtin.apt:
    deb: "https://github.com/streamingfast/cosmos-docker-upgrader/releases/download/v{{ cdu_version }}/cosmos-docker-upgrader_{{ cdu_version }}_{{ ansible_architecture | replace('x86_64', 'amd64') | replace('aarch64', 'arm64') }}.deb"

- name: Configure the instance
  ansible.builtin.template:
    src: cosmos-docker-upgrader.env.j2
    dest: "/etc/cosmos-docker-upgrader/{{ chain_name }}.env"
  notify: Restart cosmos-docker-upgrader

- name: Enable the instance
  ansible.builtin.systemd_service:
    name: "cosmos-docker-upgrader@{{ chain_name }}"
    enabled: true
    state: started
    daemon_reload: true
```

### Option 2: Download Pre-built Binaries

Download the latest release from the [releases page](https://github.com/streamingfast/cosmos-docker-upgrader/releases):

- **Linux AMD64**: `cosmos-docker-upgrader-linux-amd64`
- **Linux ARM64**: `cosmos-docker-upgrader-linux-arm64`
- **macOS ARM64**: `cosmos-docker-upgrader-darwin-arm64`

```bash
# Example for Linux AMD64
curl -L -o cosmos-docker-upgrader https://github.com/streamingfast/cosmos-docker-upgrader/releases/latest/download/cosmos-docker-upgrader-linux-amd64
chmod +x cosmos-docker-upgrader
```

### Option 3: Build from Source

```bash
go install github.com/streamingfast/cosmos-docker-upgrader/cmd/cosmos-docker-upgrader@latest
```

Or reproduce a release binary exactly, which builds it in a container and writes it
to `dist/`:

```bash
docker buildx build --target binary --output type=local,dest=dist \
  --build-arg GOOS=linux --build-arg GOARCH=amd64 .
```

## Usage

```bash
./cosmos-docker-upgrader <ChainFolder> <DataFolder>
```

### Parameters

- **ChainFolder**: Directory containing `docker-compose.yml` and optionally `docker-compose.yml-next` files
- **DataFolder**: Directory to watch for the `upgrade-info.json` file to appear

### Flags

| Flag | Default | Description |
| --- | --- | --- |
| `--status-interval` | `1h` | How often to log a status line, `0` disables the heartbeat |
| `--poll-interval` | `30s` | How often to re-check both folders in case a filesystem event was missed, `0` disables polling |
| `--retry-interval` | `5m` | How long to wait before retrying a failed upgrade, restaging `docker-compose.yml-next` retries immediately |
| `--debounce-delay` | `500ms` | How long to wait after a filesystem event before acting, to let the file be fully written |

### Help

```bash
./cosmos-docker-upgrader --help
```

## Example

```bash
./cosmos-docker-upgrader /opt/cosmos-chain /opt/cosmos-chain/data
```

This will:
1. Watch `/opt/cosmos-chain` for `docker-compose.yml-next` and `/opt/cosmos-chain/data` for `upgrade-info.json`
2. Log a status line as soon as either appears
3. Perform the upgrade sequence once both are present
4. Continue watching for future upgrades

## States

The program reports one of these states in every status line:

| State | Meaning |
| --- | --- |
| `idle` | Nothing staged, chain running normally |
| `armed` | `docker-compose.yml-next` staged, waiting for the chain to write `upgrade-info.json` |
| `blocked` | Chain wrote `upgrade-info.json` but nothing is staged, the upgrade cannot proceed (logged as a warning) |
| `upgrade_ready` | Both files present, the upgrade is starting |
| `retry_pending` | An upgrade failed, waiting for the retry delay (logged as a warning) |

## Upgrade Process

When `docker-compose.yml-next` and `upgrade-info.json` are both present, the program executes:

1. `docker-compose down` - Stop all containers
2. `mv docker-compose.yml docker-compose.yml-backup` - Backup current config
3. `mv docker-compose.yml-next docker-compose.yml` - Promote new config
4. `docker-compose up -d` - Start containers with new config
5. `mv upgrade-info.json upgrade-info.json-applied` - Clear the upgrade signal

Order does not matter. Staging `docker-compose.yml-next` after the chain has already
halted works and upgrades immediately.

Step 5 is what keeps a stale signal from firing the next upgrade prematurely. It only
runs on success, so a failed upgrade stays armed and is retried after `--retry-interval`,
or immediately if you restage `docker-compose.yml-next`.

## File Structure

Your chain directory should look like this:

```
/opt/cosmos-chain/
├── docker-compose.yml         # Current configuration
├── docker-compose.yml-next    # New configuration (created before upgrade)
├── docker-compose.yml-backup  # Previous configuration (written during upgrade)
└── data/
    ├── upgrade-info.json          # Appears during planned upgrades
    └── upgrade-info.json-applied  # Last applied plan, kept for inspection
```

## Logging

Logs are structured and written to stdout. On a terminal they are colored, when
redirected to a file the colors are dropped.

Every status line names the state and spells out what it means:

```
INFO (upgrader) status: ARMED - docker-compose.yml-next staged, waiting for chain to write upgrade-info.json {"reason": "file_event", "state": "armed", "previous_state": "idle", "compose_next_size": 1247, "compose_next_age": "0s", ...}
```

A status line is logged:
- at startup
- on every state change
- on the heartbeat (`--status-interval`, default hourly)

The heartbeat timer resets on every status line, so events never produce a duplicate
heartbeat right after them. In steady state that is exactly one line per hour.

Also logged: each upgrade step with its duration, every command run, updates to an
already staged `docker-compose.yml-next`, and all errors with the recovery taken.

Set `DLOG=upgrader=debug` for verbose filesystem event logging.

Set `LOG_FORMAT=json` for structured JSON output instead, if you ship logs to an
aggregator. The format is pinned rather than auto-detected, so it does not change
when the tool runs inside a container.

## Error Handling

- Validates directories and required files on startup
- Creates backups before making changes
- Attempts to restore backups if upgrade fails
- Leaves `upgrade-info.json` in place on failure so the upgrade can be retried
- Continues watching even after errors
- Comprehensive error logging

## Requirements

- Go 1.24 or later
- Docker and Docker Compose installed
- Appropriate permissions to manage Docker services
- Read/write access to chain and data directories

## Dependencies

- `github.com/fsnotify/fsnotify` - File system notifications
- `github.com/spf13/cobra` - CLI framework
- `github.com/streamingfast/logging` - Structured logging on top of `zap`

## Releases

Releases are built by GitHub Actions when a `v*` tag is pushed. Every binary is
built inside a container from `Dockerfile`, so a release artifact can be reproduced
locally with the same command the workflow runs.

Three build jobs run in parallel, each on a native runner:

| Job | Runner | Produces |
| --- | --- | --- |
| `linux/amd64` | `ubuntu-24.04` | binary + `.deb` |
| `linux/arm64` | `ubuntu-24.04-arm` | binary + `.deb` |
| `darwin/arm64` | `ubuntu-24.04` | binary |

The builder stage always runs on the native platform and cross compiles, so no
emulation is involved. Each Linux job installs the `.deb` it just built as a smoke
test. A final job collects every artifact, writes `checksums.txt` and creates the
GitHub release.

The workflow also runs on pull requests, without publishing, so packaging breakage
is caught before tagging.

### Creating a Release

1. Update `CHANGELOG.md`, moving the `Unreleased` section under the new version.
2. Tag and push:
   ```bash
   git tag v1.0.0
   git push origin v1.0.0
   ```

## License

[Add your license here]