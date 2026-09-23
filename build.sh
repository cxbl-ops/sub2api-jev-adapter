#!/usr/bin/env bash
set -euo pipefail

plugin_root=$(cd "$(dirname "$0")" && pwd)
cd "$plugin_root"

mkdir -p build dist
if [[ ! -f build/keys/publisher.private ]]; then
  go run ./tools/keygen -dir build/keys
fi

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath \
  -ldflags '-s -w -X main.version=0.1.0' \
  -o build/jev-adapter \
  ./cmd/jev-adapter

go run ./tools/packager \
  -root . \
  -binary build/jev-adapter \
  -ui ui/index.html \
  -private-key build/keys/publisher.private \
  -key-id cxbl-jev-adapter-v1 \
  -output dist/sub2api-jev-adapter-0.1.0.s2plugin

cp docs/INSTALL.md dist/INSTALL.md
cp docs/JEV-用户使用指南.md dist/JEV-用户使用指南.md
cp docs/JEV-开发者接入文档.md dist/JEV-开发者接入文档.md
