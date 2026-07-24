#!/bin/sh
set -eu

GO_BIN="${GO:-go}"
DIST_DIR="${DIST_DIR:-dist}"
VERSION="${VERSION:-dev}"
LDFLAGS="${LDFLAGS:--s -w -X main.version=${VERSION}}"

mkdir -p \
  "${DIST_DIR}/linux-amd64" \
  "${DIST_DIR}/linux-arm64" \
  "${DIST_DIR}/windows-amd64"

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${GO_BIN}" build -ldflags "${LDFLAGS}" -o "${DIST_DIR}/linux-amd64/save-sync" ./cmd/save-sync
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${GO_BIN}" build -ldflags "${LDFLAGS}" -o "${DIST_DIR}/linux-amd64/save-sync-ui" ./cmd/save-sync-ui

GOOS=linux GOARCH=arm64 CGO_ENABLED=0 "${GO_BIN}" build -ldflags "${LDFLAGS}" -o "${DIST_DIR}/linux-arm64/save-sync" ./cmd/save-sync
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 "${GO_BIN}" build -ldflags "${LDFLAGS}" -o "${DIST_DIR}/linux-arm64/save-sync-ui" ./cmd/save-sync-ui

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "${GO_BIN}" build -ldflags "${LDFLAGS}" -o "${DIST_DIR}/windows-amd64/save-sync.exe" ./cmd/save-sync
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "${GO_BIN}" build -ldflags "${LDFLAGS}" -o "${DIST_DIR}/windows-amd64/save-sync-ui.exe" ./cmd/save-sync-ui
