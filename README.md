# Cosmos Docker Upgrader

A Go program that watches for Cosmos blockchain upgrade signals and automatically manages Docker Compose upgrades.

## Overview

This tool monitors a data directory for the appearance of `upgrade-info.json` files (typically created by Cosmos SDK chains during planned upgrades) and automatically performs Docker Compose service upgrades when a new compose file is available.

## Features

- Watches for `upgrade-info.json` files in a specified data directory
- Automatically performs Docker Compose upgrades when `docker-compose.yml-next` is available
- Comprehensive logging of all operations
- Safe upgrade process with backup creation
- Graceful error handling and rollback on failure

## Installation

### Option 1: Download Pre-built Binaries

Download the latest release for your platform from the [releases page](https://github.com/your-username/cosmos-docker-upgrader/releases):

- **Linux AMD64**: `cosmos-docker-upgrader-linux-amd64`
- **macOS ARM64**: `cosmos-docker-upgrader-darwin-arm64`

```bash
# Example for Linux AMD64
curl -L -o cosmos-docker-upgrader https://github.com/your-username/cosmos-docker-upgrader/releases/latest/download/cosmos-docker-upgrader-linux-amd64
chmod +x cosmos-docker-upgrader
```

### Option 2: Build from Source

1. Clone this repository:
   ```bash
   git clone <repository-url>
   cd cosmos-docker-upgrader
   ```

2. Build the program:
   ```bash
   go build -o cosmos-docker-upgrader ./cmd/cosmos-docker-upgrader
   ```

## Usage

```bash
./cosmos-docker-upgrader <ChainFolder> <DataFolder>
```

### Parameters

- **ChainFolder**: Directory containing `docker-compose.yml` and optionally `docker-compose.yml-next` files
- **DataFolder**: Directory to watch for the `upgrade-info.json` file to appear

### Help

```bash
./cosmos-docker-upgrader --help
```

## Example

```bash
./cosmos-docker-upgrader /opt/cosmos-chain /opt/cosmos-chain/data
```

This will:
1. Watch `/opt/cosmos-chain/data` for `upgrade-info.json`
2. When the file appears, check if `/opt/cosmos-chain/docker-compose.yml-next` exists
3. If it exists, perform the upgrade sequence
4. Continue watching for future upgrades

## Upgrade Process

When `upgrade-info.json` appears and `docker-compose.yml-next` exists, the program executes:

1. `docker-compose down` - Stop all containers
2. `mv docker-compose.yml docker-compose.yml-backup` - Backup current config
3. `mv docker-compose.yml-next docker-compose.yml` - Promote new config
4. `docker-compose up -d` - Start containers with new config

## File Structure

Your chain directory should look like this:

```
/opt/cosmos-chain/
├── docker-compose.yml      # Current configuration
├── docker-compose.yml-next # New configuration (created before upgrade)
└── data/
    └── upgrade-info.json   # Appears during planned upgrades
```

## Logging

The program provides detailed logging including:
- Startup and configuration validation
- File watching events
- Upgrade info parsing (name, height, info)
- Each step of the upgrade process
- Error messages and recovery attempts

## Error Handling

- Validates directories and required files on startup
- Creates backups before making changes
- Attempts to restore backups if upgrade fails
- Continues watching even after errors
- Comprehensive error logging

## Requirements

- Go 1.21 or later
- Docker and Docker Compose installed
- Appropriate permissions to manage Docker services
- Read/write access to chain and data directories

## Dependencies

- `github.com/fsnotify/fsnotify` - File system notifications
- `github.com/spf13/cobra` - CLI framework

## Releases

This project uses GitHub Actions to automatically build and release binaries when tags are pushed. The workflow:

- Triggers on version tags (e.g., `v1.0.0`, `v1.2.3`)
- Builds binaries for Linux AMD64 and macOS ARM64
- Creates GitHub releases with binaries and checksums
- Includes auto-generated release notes

### Creating a Release

To create a new release:

1. Tag the commit:
   ```bash
   git tag v1.0.0
   git push origin v1.0.0
   ```

2. The GitHub Actions workflow will automatically:
   - Build the binaries
   - Create checksums
   - Create a GitHub release
   - Upload the artifacts

## License

[Add your license here]