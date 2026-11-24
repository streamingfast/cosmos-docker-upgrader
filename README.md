# Cosmos Docker Upgrader

A Go program that watches for Cosmos blockchain upgrade signals and automatically manages Docker Compose upgrades.

## Overview

This tool monitors a data directory for the appearance of `upgrade-info.json` files (typically created by Cosmos SDK chains during planned upgrades) and automatically performs Docker Compose service upgrades when a new compose file is available.

## Features

- Watches both the chain folder and the data folder, so staging `docker-compose.yml-next` shows up in the log right away
- Runs the upgrade when `docker-compose.yml-next` and `upgrade-info.json` are both present, in whichever order they appear
- Keeps `upgrade-info.json` after the swap (the new binary needs it at boot) and remembers the applied plan, so it never triggers a later staged compose file
- Structured logging with a status line at startup, on every state change and on an hourly heartbeat
- Polls as a safety net for filesystem events that are silently missed
- Clears `upgrade-info.json` once the node has committed past the plan height (with `--rpc-url`), including one left behind by an upgrade applied by hand or by an older upgrader
- Pulls the staged images before stopping anything, so a missing image never takes the chain down
- Safe upgrade process with backup creation, and rollback of the compose files when the new containers fail to start
- Retries a failed upgrade instead of giving up

## Installation

### Option 1: Debian Package (recommended on Ubuntu)

Each release publishes a `.deb` for `amd64` and `arm64`. It installs the binary to
`/usr/bin/cosmos-docker-upgrader` and a systemd template unit, so one host can run
one instance per chain.

```bash
version=1.1.1
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

- **Linux AMD64**: `cosmos-docker-upgrader_linux_x86_64.tar.gz`
- **Linux ARM64**: `cosmos-docker-upgrader_linux_arm64.tar.gz`
- **macOS ARM64**: `cosmos-docker-upgrader_darwin_arm64.tar.gz`

```bash
# Example for Linux AMD64
curl -L https://github.com/streamingfast/cosmos-docker-upgrader/releases/latest/download/cosmos-docker-upgrader_linux_x86_64.tar.gz | tar -xz cosmos-docker-upgrader
```

### Option 3: Build from Source

```bash
go install github.com/streamingfast/cosmos-docker-upgrader/cmd/cosmos-docker-upgrader@latest
```

Or build a binary in a container from `Dockerfile`, which writes it to `dist/`:

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
| `--rpc-url` | | CometBFT RPC of the node, e.g. `http://localhost:26657`, enables clearing the signal once the node is past the plan height (see below) |
| `--pre-pull` | `true` | Pull the images of `docker-compose.yml-next` before stopping the chain |
| `--restart-previous-on-failure` | `false` | Start the previous containers again when the upgraded ones fail to start |

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

1. `docker-compose -f docker-compose.yml-next pull` - Pull the new images, skipped with `--pre-pull=false`
2. `docker-compose down` - Stop all containers
3. `mv docker-compose.yml docker-compose.yml-backup` - Backup current config
4. `mv docker-compose.yml-next docker-compose.yml` - Promote new config
5. `docker-compose up -d` - Start containers with new config
6. Record the applied plan (name and height) in `upgrade-info.json-applied`. `upgrade-info.json` is **not** touched

Order does not matter. Staging `docker-compose.yml-next` after the chain has already
halted works and upgrades immediately.

Step 6 only runs on success, so a failed upgrade stays armed and is retried after
`--retry-interval`, or immediately if you restage `docker-compose.yml-next`.

`upgrade-info.json` must stay in place after the swap. Cosmos SDK apps read it at every
boot, and use it to install the store upgrade loader that adds the new module stores at the
upgrade height. Without it, an upgrade that adds a store panics at load (`version of store ... mismatch root store's version`), and any restart before the upgrade block is committed
(crash, restart policy, OOM) needs it again. Cosmovisor never deletes it either.

Instead, the applied plan is remembered by name and height, in memory and in
`upgrade-info.json-applied` so it survives an upgrader restart. While `upgrade-info.json`
holds that plan it is not a signal: nothing fires, even if `docker-compose.yml-next` is
staged again. The next halt writes a different plan, which fires normally.

A pull failure in step 1 aborts with the chain untouched. A start failure in step 5 is
rolled back: the containers are stopped, `docker-compose.yml` goes back to
`docker-compose.yml-next` and the backup goes back to `docker-compose.yml`, so the
upgrade stays staged for the retry. The containers are left stopped unless
`--restart-previous-on-failure` is set. It is off by default because on a real halt the
previous binary must not run, it could end up following the wrong fork past the upgrade
height.

### Clearing the Signal

Cosmos SDK never deletes `upgrade-info.json`. A leftover one would fire the next staged
`docker-compose.yml-next` on the spot, so it is moved out of the way once it is safe.

With `--rpc-url` set, every time `upgrade-info.json` is seen the node's latest height
is read from `/status`. When it is **strictly greater** than the plan height, the upgrade
block was committed (a halted node can have the upgrade block in its block store, height
equal to the plan height, without the app having committed it), and the file is renamed:

- to `upgrade-info.json-applied` if this program did the swap
- to `upgrade-info.json-stale` if the upgrade was applied by anything else (by hand, by an
  older version of this program)

An unreachable node proves nothing (a halted chain usually has its RPC down), so the file is
then kept as is.

Without `--rpc-url`, there is no height source. A plan this program applied is still
ignored forever by name and height and the file is left in place (harmless, the SDK ignores
it once past the height, and the next halt overwrites it). But a leftover signal from an
upgrade applied by other means cannot be detected: check for one before staging
`docker-compose.yml-next`. Setting `--rpc-url` is recommended.

## File Structure

Your chain directory should look like this:

```
/opt/cosmos-chain/
├── docker-compose.yml         # Current configuration
├── docker-compose.yml-next    # New configuration (created before upgrade)
├── docker-compose.yml-backup  # Previous configuration (written during upgrade)
└── data/
    ├── upgrade-info.json          # Appears during planned upgrades
    ├── upgrade-info.json-applied  # Last plan this program applied, also its memory across restarts
    └── upgrade-info.json-stale    # Last stale signal cleared, kept for inspection
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
- Pulls staged images before stopping anything
- Restores the previous compose file if the new containers fail to start, optionally restarting the previous ones
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

Releases go through [sfreleaser](https://github.com/streamingfast/sfreleaser) and two
GitHub Actions workflows, `draft-release.yml` and `publish-release.yml`.

### Creating a Release

1. Open a pull request against `main` that moves the `Unreleased` section of
   `CHANGELOG.md` under the new version header, e.g. `## v1.1.0`.
2. `draft-release.yml` runs on that pull request and prepares the release.
3. Merge the pull request. `publish-release.yml` tags the release, publishes it on
   GitHub and starts `deb.yml` on the new tag.

`publish-release.yml` can be re-run from the Actions tab with the `tag` input, e.g.
`v1.1.0`, to redo the publish steps.

### Debian Packages

sfreleaser cannot build `.deb` packages yet, so `deb.yml` does it. It is started
automatically by `publish-release.yml`, and can be run by hand with the release `tag`
as input (`gh workflow run deb.yml -f tag=v1.1.0`). On the existing release, for
`amd64` and `arm64` it:

- downloads the Linux binary sfreleaser attached to the release, on a native runner;
- builds the package with `nfpm.yaml`;
- installs the package as a smoke test;
- attaches the `.deb` to the release.

## License

[Add your license here]