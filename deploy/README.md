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

### Cloudflare Access on /mqtt

The site is public, but the broker shouldn't be: only the Pi has any business
on `/mqtt`. A Cloudflare Access application covering just that path makes
Cloudflare refuse every connection that doesn't carry the Pi's service token,
before it reaches the tunnel. That keeps strangers away from Mosquitto
entirely, and they can't fill its connection limit to knock the printer
offline.

In Cloudflare Zero Trust:

1. **Access → Service Auth → Service Tokens → Create Service Token.** Name it
   `partyfax-pi`. A duration of a year or more is fine; set a reminder,
   because when it expires the printer goes offline. Copy the **Client ID**
   and **Client Secret**. The secret is shown only once.
2. **Give the token to the Pi first,** before Access is turned on, so the Pi
   never loses its connection. Run the installer on the Pi again and paste
   both when it asks (`pi/README.md`, "Updating"). If you skipped that
   question before, add the two `PARTYFAX_CF_ACCESS_*` lines to
   `/etc/partyfax/agent.env` instead and `sudo systemctl restart partyfax-agent`.
3. **Access → Applications → Add an application → Self-hosted.** Name it
   `partyfax mqtt`. Domain `partyfax.nilanet.live`, path `mqtt`. Only that
   path: an application on the bare domain would lock the public site too.
4. Add one policy: action **Service Auth**, include **Service Token** →
   `partyfax-pi`. No other policies.
5. Save. Check that `https://partyfax.nilanet.live/` still loads in a private
   window and the printer still shows online.

### The photobooth host and its admin

The photobooth is a second hostname on the same tunnel, served by the same
Caddy, which sends it to the `booth` service. Two one-time steps in
Cloudflare Zero Trust:

1. **Public hostname.** Networks → Tunnels → your tunnel → Public Hostname
   → Add: `photobooth.nilanet.live` → service `HTTP`, URL `caddy:80`.
2. **Access for the admin page.** Access → Applications → Add →
   Self-hosted, named `photobooth admin`, domain `photobooth.nilanet.live`
   with **two** paths: `admin` and `api/booth/admin`. Add one policy:
   action **Allow**, include **Emails**: yours. (Allow here, not Service
   Auth: a person logs in with a one-time code.) Then copy the app's
   **Audience (AUD) tag** and your team domain (Settings → Custom Pages,
   `<team>.cloudflareaccess.com`) into `.env` as `CF_ACCESS_AUD` and
   `CF_ACCESS_TEAM_DOMAIN`, and run `docker compose up -d booth`.

The booth checks the Access token itself as well, so if the Access app is
ever deleted, admin stays closed rather than open. It also refuses admin
changes another site's page sends (the Access login is a cookie a browser
might attach to them). In the Access app's settings, **Cookie settings →
SameSite attribute: Strict** stops a browser sending it in the first place.

Guests need no login, only the event code. Codes are 6 characters (887
million of them), and every request carries one, so the booth counts wrong
codes wherever they arrive: an address gets 10 wrong in 10 minutes (50 once
it has shown a right code, since a party shares one Wi-Fi address), and
everyone together 1,000 an hour, after which only addresses that have shown
a right code lately get in until the hour is up. Caddy also limits joining
to 10 a minute per address. Turn an event off, or give it a new code, once
the party is over: a code works for as long as the event is open.

Booth limits per address: 3 strips a minute, 30 an hour, 60 a day. Strips
are kept in the `booth` volume, `/var/lib/docker/volumes/fax_booth`, and are
in the nightly Proxmox backup like everything else.

## Browser caching

Caddy tells browsers to check every page, script and stylesheet with the
server each time (`no-cache`; unchanged files come back as a quick 304),
so a deploy reaches everyone at once. Cloudflare overrides that with four
hours on scripts and stylesheets unless **Caching → Configuration →
Browser Cache TTL** is **Respect Existing Headers**. Keep it that way: the
pages load their scripts by plain name, with no version number to force a
fresh copy.

## Every deploy

```sh
cd /root/partyfax && ./deploy/update.sh
```

It pulls the checkout (the frontend, Caddyfile and Mosquitto config are
mounted from it), rebuilds what changed, restarts it, and restarts Mosquitto
or Caddy only when their config changed.

Once a month, `partyfax-rebuild.timer` (installed by
`provisioning/install.sh`) runs it with `FULL_BUILD=1 NO_PULL=1`: every
image rebuilt on fresh base images, nothing new from main. That is how
security fixes in Alpine, Caddy and Go's standard library arrive.
`systemctl list-timers partyfax-rebuild` shows when it runs next, and
`journalctl -u partyfax-rebuild` how the last one went.

## Networks

`cloudflared` is alone on `edge` with Caddy. Everything else is on `app`,
which is internal: no route to the internet. Only the booth also joins
`egress`, to fetch Cloudflare Access's signing keys. The host firewall
(`provisioning/firewall/`) keeps every container off the LAN either way.

## Rate limit

Caddy allows each address 5 `POST /api/print` a minute, 50 an hour and 100
a day (three zones; a print has to fit all of them), and answers the rest
with `429` and a message the page shows. The page, the
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
