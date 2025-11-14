#!/bin/bash

# Cosmos Docker Upgrader Build Script
# This script builds the application for multiple platforms

set -e

BINARY_NAME="cosmos-docker-upgrader"
VERSION="${VERSION:-dev}"
BUILD_TIME=$(date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Build flags
LDFLAGS="-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}"

echo "Building ${BINARY_NAME}..."
echo "Version: ${VERSION}"
echo "Build Time: ${BUILD_TIME}"
echo "Git Commit: ${GIT_COMMIT}"
echo

# Clean previous builds
echo "Cleaning previous builds..."
rm -f ${BINARY_NAME}-*
rm -f checksums.txt

# Build for Linux AMD64
echo "Building for Linux AMD64..."
GOOS=linux GOARCH=amd64 go build -ldflags "${LDFLAGS}" -o ${BINARY_NAME}-linux-amd64 ./cmd/cosmos-docker-upgrader

# Build for macOS ARM64
echo "Building for macOS ARM64..."
GOOS=darwin GOARCH=arm64 go build -ldflags "${LDFLAGS}" -o ${BINARY_NAME}-darwin-arm64 ./cmd/cosmos-docker-upgrader

# Build for current platform
echo "Building for current platform..."
go build -ldflags "${LDFLAGS}" -o ${BINARY_NAME} ./cmd/cosmos-docker-upgrader

# Make binaries executable
chmod +x ${BINARY_NAME}-*
chmod +x ${BINARY_NAME}

# Create checksums
echo "Creating checksums..."
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum ${BINARY_NAME}-linux-amd64 > checksums.txt
    sha256sum ${BINARY_NAME}-darwin-arm64 >> checksums.txt
    sha256sum ${BINARY_NAME} >> checksums.txt
elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 ${BINARY_NAME}-linux-amd64 > checksums.txt
    shasum -a 256 ${BINARY_NAME}-darwin-arm64 >> checksums.txt
    shasum -a 256 ${BINARY_NAME} >> checksums.txt
else
    echo "Warning: Neither sha256sum nor shasum found, skipping checksum generation"
fi

echo
echo "Build complete! Generated files:"
ls -la ${BINARY_NAME}*
if [ -f checksums.txt ]; then
    echo
    echo "Checksums:"
    cat checksums.txt
fi

echo
echo "Test the binary:"
echo "./${BINARY_NAME} --version"
