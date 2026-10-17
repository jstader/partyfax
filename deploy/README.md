# Deploying

The stack runs in an unprivileged LXC on a home Proxmox host, checked out at
`/root/partyfax`. The images are built
there. Nothing is published to the LAN or the internet: a `cloudflared`
container dials out to Cloudflare, and `partyfax.nilanet.live` reaches Caddy
through that tunnel.

```
visitor --HTTPS--> Cloudflare --tunnel--> cloudflared --HTTP--> caddy --> gateway / mosquitto
Pi agent --WSS /mqtt--> Cloudflare --tunnel--> ...
```

## One-time setup

Host provisioning (Docker, SSH hardening, CrowdSec, firewall) is
`provisioning/install.sh`. It's idempotent and safe to re-run.

Secrets live in `.env` and the broker's password file. Neither is committed.

```sh
cp .env.example .env && chmod 600 .env
# Fill in MQTT_PASSWORD and PRINTER_MQTT_PASSWORD (openssl rand -hex 24).

# -c creates the file, so only on the first account.
docker run --rm -v ./deploy/mosquitto:/m eclipse-mosquitto:2 \
  mosquitto_passwd -c -b /m/passwd backend '<MQTT_PASSWORD>'
docker run --rm -v ./deploy/mosquitto:/m eclipse-mosquitto:2 \
  mosquitto_passwd -b /m/passwd printer '<PRINTER_MQTT_PASSWORD>'
# The broker drops to its own user (uid 1883) before reading this.
chown 1883:1883 deploy/mosquitto/passwd
```

The printer password goes on the Pi (`pi/README.md`). Keep it different from
the backend's. The ACL lets `printer` receive jobs but never send them, and
that only helps if the two accounts differ.

### The tunnel

In Cloudflare Zero Trust, go to Networks → Tunnels → Create a tunnel
(Cloudflared). Copy its token into `.env` as `CLOUDFLARE_TUNNEL_TOKEN`, then
add a public hostname:

- `partyfax.nilanet.live` → service `HTTP`, URL `caddy:80`

Cloudflare creates the proxied DNS record itself. Then
`docker compose up -d cloudflared`.

Caddy trusts `Cf-Connecting-IP` from private addresses only, which here means
cloudflared. So rate limits, `FAX_BANNED` and the gateway's logs all see the
visitor, not the tunnel.

## Every deploy

```sh
cd /root/partyfax && ./deploy/update.sh
```

It pulls the checkout (the frontend, Caddyfile and Mosquitto config are
mounted from it), rebuilds what changed, restarts it, and restarts Mosquitto
or Caddy only when their config changed.

## Rate limit

Caddy allows each address three `POST /api/print` per ten minutes and
answers the rest with `429` and a message the page shows. The page, the
wall and the rest of the API are never limited. Change it in the
`rate_limit` block of the Caddyfile; `update.sh` restarts Caddy when that
file changes. The counts live in Caddy's memory and reset on a restart.

Stock Caddy has no rate limiting, so the stack builds its own Caddy image
(`deploy/caddy/Dockerfile`) with the plugin.

## The wall at /prints

Every print the printer confirms is appended to `prints.jsonl` in the `wall`
volume and pushed live to anyone watching `/prints`. Refused or unconfirmed
prints never appear.

Take one down (the id is in its link, `/prints#<id>`):

```sh
./deploy/hide.sh <id>
```

Backfilling old prints reads the gateway's logs. Docker keeps logs per
container, and a deploy that rebuilds the gateway replaces the
container, so save them first:

```sh
docker compose logs --no-color gateway > ~/gateway-logs.txt   # before update.sh
./deploy/update.sh
docker compose stop gateway
docker compose run --rm -T --no-deps gateway \
  -wall-dir=/var/lib/fax/wall -photos-dir=/var/lib/fax/photos -backfill < ~/gateway-logs.txt
docker compose start gateway
```

The gateway is stopped because backfill rewrites the file it appends to.
Logs carry no styles, so backfilled text comes back plain. A photo is
included only if its log line survived. Running it twice adds nothing twice.

## Backups

Proxmox's nightly backup job on the host includes this container (CT 102),
so everything is in it: the wall and photos in the Docker volumes, `.env`,
and the broker's `passwd`. The container itself holds no NAS credentials and
has no route to the NAS. That's deliberate: it's the box strangers can reach,
so a compromise of it shouldn't lead anywhere near the backups.

To restore, use the Proxmox UI (the container's Backup tab, then Restore).
For a single file, open the archive on the host:
`tar -I zstd -xf vzdump-lxc-102-*.tar.zst ./var/lib/docker/volumes/fax_wall`.

## The printer

The Pi Zero W next to the printer runs `pi/`; see `pi/README.md`. Update it
and the server together when a change touches both: the two ends share the
MQTT message shapes and the timeouts.
