# Notes for coding agents

What partyfax is and how it's deployed are in `README.md`, `deploy/README.md`
and `pi/README.md`. This file holds what those don't: the limits the code is
built around, the dead ends already tried, and how changes are made here.

## Layout

- `backend/`: one Go module, four services in `cmd/`:
  - `gateway`: the public site's API, and the fortunes site's one print
    route.
  - `booth`: photobooth events, strips and admin.
  - `renderer`: turns documents into printer bytes.
  - `dispatcher`: sends jobs to the Pi over MQTT.
  - Shared code is in `internal/`.
- `frontend/`: plain HTML/JS, no build step.
  - The public site is at the top level; the photobooth is in `booth/`, the
    fortune cookie site in `fortunes/`.
  - Pictures are composed in the browser (`js/photo.js` dithers), so a
    preview is exactly what prints.
  - The fortunes' Chinese font is trimmed to the characters the slips use;
    new characters mean rebuilding it (`fonts/README.md`).
- `pi/`: the agent on the Raspberry Pi Zero W (ARMv6) that writes jobs to the
  USB printer (`/dev/usb/lp0`), plus its installer and systemd units.
- `deploy/`, `provisioning/`, `docker-compose.yml`: the Docker Compose stack
  and the host it runs on.

## Testing

CI (`.github/workflows/ci.yml`) is the reference:

- `backend/` and `pi/`: `gofmt -l .` must print nothing, then `go vet ./...`
  and `go test ./...`.
- `pi/` tests run on the build machine's own architecture
  (`GOARCH=amd64 go test ./...`), not ARM.
- `frontend/`: `npm test` (node's built-in runner; there are no dependencies).

The maintainer's Mac has neither Go nor Node; run the tests in Docker
(`golang`, `node` images) or rely on CI.

## Limits the code is built around

- **The printer is 1-bit and 384 dots wide.**
  - Photos are at most 384 rows (`doc.PhotoMaxRows`).
  - Photobooth strips are at most 1400 rows (`doc.StripMaxRows`).
  - Fortune slips are at most 800 rows (`doc.FortuneMaxRows`).
  - A job is at most 128 KB, enforced by the broker and by the Pi agent.
- **Cloudflare cuts every request off at 100 s.**
  - The timeouts in `backend/internal/timeouts` are chained to stay under it,
    and a test enforces this.
  - Pages abort at 90 s.
  - Raise none of them without checking the whole chain.
- **Uploaded strips are redrawn server-side** (`render.OneBit`) before they're
  printed or stored. Don't store or print client bytes as they are.

## The printer: dead ends already tried

The printer reports `58POS-USB-BT` / `POS_58` (USB id `1d81:5721`).

- **Commands it ignores:**
  - ESC 7 (heating)
  - GS ( K (speed and density)
  - DC2 # (density)
  - Don't add them again; they were tested on paper and changed nothing.
- **Queries:** it answers GS r 1 (paper sensor) but not DLE EOT or most GS I
  queries.
- **Very dark images make it drop off USB partway through a print**: the
  kernel logs "disabled by hub (EMI?)". The cause is electrical, at the
  printer. Three things keep it working:
  - Strips are kept light: `PHOTO_INK` in `frontend/booth/js/strip.js`, and
    the film frame is drawn as a negative.
  - The Pi uses the mainline `dwc2` USB driver, set by `pi/install.sh`, which
    recovers on the next print.
  - The agent (`pi/pieces.go`) sends pictures in 128-row pieces and waits for
    the printer to answer GS r 1 after each one before sending the next.
    Keep this. Rows that come out garbled or shifted mean the printer lost
    data, not that the waiting is wrong.

## Making changes

- **Pi changes reach the Pi only when the installer is re-run on it by hand.**
  CI publishes the agent binary to the `agent-latest` release, and
  `pi/install.sh` downloads it from there. Deploying the server doesn't touch
  the Pi.
- **Deploys are manual:** `deploy/update.sh` on the server. The server takes
  no inbound connections, so CI can't deploy.
- **This repo is public.** LAN addresses, hostnames, tokens and Cloudflare
  Access values live in `.env` (see `.env.example`), never in the repo.
- **Containers:** never privileged. Keep the hardening the existing services
  have (`cap_drop: ALL`, `no-new-privileges`, read-only root, memory limits).
- **Shared host:** don't touch services outside this compose project.
- **Backups:** don't schedule work or restart things between 03:15 and 03:35
  Pacific, the nightly backup window.
- **Commits** go straight to `main`. Use conventional prefixes (`fix(pi):`,
  `feat(booth):`, `docs:`), each summary one plain sentence.
- **Code style:** match the existing code. Comments explain why, in full
  sentences, and say what was measured when a number came from testing.
