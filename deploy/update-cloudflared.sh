#!/usr/bin/env bash
#
# Move cloudflared to the newest release that is at least MIN_AGE_DAYS old, and
# put the old one back if the tunnel doesn't come up healthy. Run weekly by
# partyfax-cloudflared-update.timer; safe to run by hand.
#
# The version stays pinned, in .env rather than docker-compose.yml, so a restart
# never changes it and an update doesn't dirty the checkout and block a deploy.
# The wait skips releases Cloudflare replaces within days: it shipped five in the
# ten days before this was written.

set -euo pipefail

cd "$(dirname "$0")/.."

MIN_AGE_DAYS=3
# Seconds. The healthcheck runs every 30 with a 20 second start period.
HEALTH_WAIT=${HEALTH_WAIT:-120}
IMAGE=cloudflare/cloudflared

current=$(docker compose config --format json |
	python3 -c 'import json, sys; print(json.load(sys.stdin)["services"]["cloudflared"]["image"].rsplit(":", 1)[1])')

target=$(curl -fsS --max-time 30 "https://api.github.com/repos/cloudflare/cloudflared/releases?per_page=30" |
	MIN_AGE_DAYS=$MIN_AGE_DAYS python3 -c '
import datetime, json, os, re, sys
cutoff = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=int(os.environ["MIN_AGE_DAYS"]))
for r in json.load(sys.stdin):
    published = datetime.datetime.fromisoformat(r["published_at"].replace("Z", "+00:00"))
    if not r["draft"] and not r["prerelease"] and published <= cutoff and re.fullmatch(r"\d+\.\d+\.\d+", r["tag_name"]):
        print(r["tag_name"])
        break
')

if [[ -z "$target" ]]; then
	echo "no release old enough to take; staying on $current" >&2
	exit 1
fi
if [[ "$(printf '%s\n%s\n' "$current" "$target" | sort -V | tail -1)" == "$current" ]]; then
	echo "cloudflared is up to date: $current (newest eligible: $target)"
	exit 0
fi

set_version() {
	if grep -q '^CLOUDFLARED_VERSION=' .env; then
		sed -i "s/^CLOUDFLARED_VERSION=.*/CLOUDFLARED_VERSION=$1/" .env
	else
		echo "CLOUDFLARED_VERSION=$1" >>.env
	fi
}

wait_healthy() {
	local status
	for ((waited = 0; waited < HEALTH_WAIT; waited += 5)); do
		status=$(docker inspect --format '{{.State.Health.Status}}' "$(docker compose ps -q cloudflared)" 2>/dev/null || true)
		[[ "$status" == healthy ]] && return 0
		sleep 5
	done
	return 1
}

echo "updating cloudflared: $current -> $target"
# Pulled first, so a failed download leaves the running tunnel alone.
docker pull --quiet "$IMAGE:$target"
set_version "$target"
docker compose up -d --no-deps cloudflared

if wait_healthy; then
	docker image rm "$IMAGE:$current" >/dev/null 2>&1 || true
	echo "cloudflared updated to $target"
	exit 0
fi

echo "cloudflared $target did not become healthy; going back to $current" >&2
set_version "$current"
docker compose up -d --no-deps cloudflared
HEALTH_WAIT=120
if wait_healthy; then
	echo "rolled back to $current" >&2
else
	echo "rolled back to $current, but it is not healthy either" >&2
fi
exit 1
