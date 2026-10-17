#!/usr/bin/env bash
#
# Cross-compile the agent for the Pi Zero W (ARMv6). Uses a local Go if there
# is one, otherwise Docker.
#
#   ./pi/build.sh        writes pi/partyfax-agent

set -euo pipefail

cd "$(dirname "$0")"

export CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6

if command -v go >/dev/null; then
	go build -trimpath -ldflags='-s -w' -o partyfax-agent .
else
	docker run --rm -v "$PWD":/src -w /src \
		-e CGO_ENABLED -e GOOS -e GOARCH -e GOARM \
		golang:1.26-alpine go build -buildvcs=false -trimpath -ldflags='-s -w' -o partyfax-agent .
fi

ls -l partyfax-agent
