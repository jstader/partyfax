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
	(umask 027 && printf 'PARTYFAX_MQTT_PASS=%s\n' "$pass" >"$ENV_FILE")
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

say "SSH: keys only"
# Raspberry Pi Imager's "public-key only" option already does this; this
# keeps it so after a re-flash that chose passwords. Only once the person
# running this has a key, or it would lock them out. 10- sorts before
# cloud-init's 50-, and sshd takes the first value it reads.
home=$(getent passwd "${SUDO_USER:-root}" | cut -d: -f6)
if [ -s "$home/.ssh/authorized_keys" ]; then
	printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin no\n' \
		>/etc/ssh/sshd_config.d/10-partyfax.conf
	sshd -t && systemctl reload ssh
	echo "Done."
else
	echo "No SSH key for ${SUDO_USER:-root} yet, so passwords stay on. Add one"
	echo "(ssh-copy-id from your computer) and run this again."
fi

say "Automatic security updates"
# Raspberry Pi OS packages come from the "Raspbian" and "Raspberry Pi
# Foundation" origins; unattended-upgrades' stock list only names Debian's,
# and would quietly install nothing. A kernel update needs a reboot, so it
# reboots itself at 04:00 when one is waiting: the printer is gone a minute.
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq unattended-upgrades >/dev/null
cat >/etc/apt/apt.conf.d/51partyfax-unattended <<'CONF'
Unattended-Upgrade::Origins-Pattern {
	"origin=Raspbian,codename=${distro_codename},label=Raspbian";
	"origin=Raspberry Pi Foundation,codename=${distro_codename},label=Raspberry Pi Foundation";
};
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-Time "04:00";
CONF
printf 'APT::Periodic::Update-Package-Lists "1";\nAPT::Periodic::Unattended-Upgrade "1";\n' \
	>/etc/apt/apt.conf.d/20auto-upgrades
echo "Done. Daily, rebooting at 04:00 only when an update needs it."

say "Bluetooth off"
# Nothing here uses it, and a radio stack that is listening is something to
# attack from the street.
systemctl disable --now bluetooth.service hciuart.service 2>/dev/null || true
systemctl mask bluetooth.service >/dev/null 2>&1 || true
echo "Done."

say "Installing the service"
curl -fsSL "$UNIT_URL" -o "$tmp"
install -m 0644 "$tmp" /etc/systemd/system/partyfax-agent.service
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
