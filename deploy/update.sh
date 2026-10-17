#!/usr/bin/env bash
#
# Deploy what is on main. The images are built here, on the container: they
# are small Go builds and Caddy, and building locally means no registry
# login to keep.
#
#   ./deploy/update.sh

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

# --pull refreshes the base images; unchanged layers come from the cache.
docker compose build --pull
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

docker image prune -f >/dev/null

docker compose ps --format "table {{.Service}}\t{{.Status}}\t{{.Image}}"
