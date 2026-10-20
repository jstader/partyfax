#!/usr/bin/env bash
#
# Deploy what is on main. The images are built here, on the container: they
# are small Go builds and Caddy, and building locally means no registry
# login to keep.
#
#   ./deploy/update.sh
#   FULL_BUILD=1 ./deploy/update.sh   rebuild every image on fresh bases

set -euo pipefail

cd "$(dirname "$0")/.."

# The checkout matters too: the frontend, the Caddyfile and the Mosquitto
# config are mounted from it, not baked into images.
before=$(git rev-parse HEAD)
git pull --ff-only
after=$(git rev-parse HEAD)

# Default build attestations carry a timestamp, so every build would be a new
# image even with nothing changed, and every deploy would restart everything.
export BUILDX_NO_DEFAULT_ATTESTATIONS=1

# Rebuild an image only when its source changed in this pull, or it doesn't
# exist yet. The build cache is dropped after every deploy (below), so a
# rebuild starts from scratch -- Caddy's is seven minutes and hundreds of
# downloads -- and doing it for nothing is slow and one more thing to fail.
# FULL_BUILD=1 rebuilds everything, with --pull, for base-image updates.
#
# One image at a time: the container has 1 GB, and Caddy's and the Go
# services' compiles side by side run it out ("resource temporarily
# unavailable").
docker compose config --format json | python3 -c '
import json, os, sys
root = os.getcwd()
for name, svc in json.load(sys.stdin)["services"].items():
    b = svc.get("build")
    if b:
        print(name, svc["image"], os.path.relpath(b["context"], root))
' | while read -r svc image context; do
	if [[ "${FULL_BUILD:-0}" == 1 ]]; then
		docker compose build --pull "$svc" </dev/null
	elif ! docker image inspect "$image" >/dev/null 2>&1; then
		echo "building $svc: no image yet"
		docker compose build "$svc" </dev/null
	elif ! git diff --quiet "$before" "$after" -- "$context"; then
		echo "building $svc: $context changed"
		docker compose build "$svc" </dev/null
	fi
done
docker compose pull --quiet --ignore-buildable
docker compose up -d --remove-orphans

# Neither of these reads its config while running. Compose does not restart a
# container whose only change is a mounted file, so it has to be done here,
# and only when the file actually changed: a Mosquitto restart drops every
# client for a few seconds.
if ! git diff --quiet "$before" "$after" -- deploy/mosquitto/; then
	echo "mosquitto config changed; restarting the broker"
	docker compose restart mosquitto
fi

# A restart, not `caddy reload`: git replaces the file rather than editing
# it, and a single-file bind mount keeps showing the old one until the
# container restarts.
if ! git diff --quiet "$before" "$after" -- deploy/Caddyfile; then
	echo "Caddyfile changed; restarting caddy"
	docker compose restart caddy
fi

# Build cache is about 2 GB, a quarter of the disk, and all of it would be
# in every nightly vzdump. Deploys are rare, so the next one builds from
# scratch instead (a few minutes).
docker image prune -f >/dev/null
docker builder prune -af >/dev/null

docker compose ps --format "table {{.Service}}\t{{.Status}}\t{{.Image}}"
