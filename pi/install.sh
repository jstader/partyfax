#!/usr/bin/env bash
#
# Install or update the partyfax printer agent on a Raspberry Pi. Run on the
# Pi, as one line:
#
#   curl -fsSL https://raw.githubusercontent.com/jstader/partyfax/main/pi/install.sh | sudo bash
#
# Safe to re-run: that is also how to update. The broker password is asked
# for once and kept in /etc/partyfax/agent.env; later runs leave it alone.

set -euo pipefail

REPO=jstader/partyfax
BIN_URL="https://github.com/$REPO/releases/download/agent-latest/partyfax-agent"
UNIT_URL="https://raw.githubusercontent.com/$REPO/main/pi/partyfax-agent.service"
ENV_FILE=/etc/partyfax/agent.env

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

# Piped into bash, stdin is the script itself, so questions go to the terminal.
ask() { local reply; read -r -p "$1" reply </dev/tty; printf '%s' "$reply"; }

if [ "$(id -u)" -ne 0 ]; then
	echo "Run with sudo: curl -fsSL ... | sudo bash" >&2
	exit 1
fi

say "Downloading the agent"
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
curl -fsSL "$BIN_URL" -o "$tmp"
install -m 0755 "$tmp" /usr/local/bin/partyfax-agent

say "Creating the partyfax user"
# lp owns /dev/usb/lp0, so membership is what lets the agent use the printer.
if ! id partyfax >/dev/null 2>&1; then
	useradd --system --no-create-home --shell /usr/sbin/nologin --groups lp partyfax
fi

say "Broker password"
install -d -m 0750 -g partyfax /etc/partyfax
if grep -qE '^PARTYFAX_MQTT_PASS=.+' "$ENV_FILE" 2>/dev/null; then
	echo "Already set in $ENV_FILE; keeping it."
else
	pass=${PARTYFAX_MQTT_PASS:-}
	while [ -z "$pass" ]; do
		read -r -s -p "Paste PRINTER_MQTT_PASSWORD from the server's .env: " pass </dev/tty
		echo
	done
	umask 027
	printf 'PARTYFAX_MQTT_PASS=%s\n' "$pass" >"$ENV_FILE"
	chown root:partyfax "$ENV_FILE"
	chmod 0640 "$ENV_FILE"
	echo "Saved to $ENV_FILE."
fi

say "Turning off Wi-Fi power saving"
# The Zero W's power saving drops idle connections, and the agent's is idle
# between prints. Applies from the next Wi-Fi connect (or reboot).
if [ -d /etc/NetworkManager/conf.d ]; then
	printf '[connection]\nwifi.powersave = 2\n' >/etc/NetworkManager/conf.d/partyfax-wifi-powersave.conf
	echo "Done."
else
	echo "NetworkManager not found; skipping."
fi

say "Installing the service"
curl -fsSL "$UNIT_URL" -o /etc/systemd/system/partyfax-agent.service
systemctl daemon-reload
# Stopped for the test print: two processes cannot share the printer.
systemctl stop partyfax-agent 2>/dev/null || true

say "Printer check"
if [ -e /dev/usb/lp0 ]; then
	if [ "$(ask "Printer found. Print a test receipt? [Y/n] ")" != n ]; then
		sudo -u partyfax /usr/local/bin/partyfax-agent -selftest ||
			echo "The test print failed; see the message above. Continuing anyway."
	fi
else
	echo "No printer at /dev/usb/lp0. Plug it into the Pi's USB port (not PWR) and"
	echo "switch it on; the agent waits for it, so it can be done later."
fi

say "Starting the agent"
systemctl enable --now partyfax-agent
sleep 5
journalctl -u partyfax-agent -n 5 --no-pager -o cat

cat <<'EOF'

Done. Look above for "connected, subscribed, announced online".

  Live log:   journalctl -u partyfax-agent -f
  Restart:    sudo systemctl restart partyfax-agent
  Update:     run the same curl | sudo bash line again
EOF
