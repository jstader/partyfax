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
# On the card now, not in a few seconds: a Pi that resets before then is
# left with an empty file where the agent was.
sync

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

say "Cloudflare Access token"
# Optional. Access in front of /mqtt keeps everyone but this Pi away from
# the broker. Asked once: a blank answer is remembered, so later runs (and
# updates) do not ask again. To add it later, edit $ENV_FILE.
if grep -q 'CF_ACCESS_CLIENT_ID' "$ENV_FILE"; then
	echo "Already decided in $ENV_FILE; keeping it."
else
	id=${PARTYFAX_CF_ACCESS_CLIENT_ID:-}
	secret=${PARTYFAX_CF_ACCESS_CLIENT_SECRET:-}
	if [ -z "$id" ]; then
		read -r -p "Access service token Client ID (Enter to skip): " id </dev/tty
	fi
	if [ -n "$id" ] && [ -z "$secret" ]; then
		while [ -z "$secret" ]; do
			read -r -s -p "Access service token Client Secret: " secret </dev/tty
			echo
		done
	fi
	if [ -n "$id" ]; then
		printf 'PARTYFAX_CF_ACCESS_CLIENT_ID=%s\nPARTYFAX_CF_ACCESS_CLIENT_SECRET=%s\n' "$id" "$secret" >>"$ENV_FILE"
		echo "Saved to $ENV_FILE."
	else
		printf '# PARTYFAX_CF_ACCESS_CLIENT_ID=\n# PARTYFAX_CF_ACCESS_CLIENT_SECRET=\n' >>"$ENV_FILE"
		echo "Skipped."
	fi
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

say "Keeping the Pi on the network"
# On Oct 23 2026 the Pi's Wi-Fi setting vanished and it sat offline until
# someone came with a screen and keyboard. The cause wasn't found: the logs
# went with the reboots. So, from here on:
#
#   - cloud-init, which wrote the Wi-Fi setting from Imager on first boot,
#     may not write network settings again
#   - a copy of the Wi-Fi setting is kept, refreshed whenever this runs
#     with Wi-Fi saved (never overwritten with nothing)
#   - partyfax-netcheck puts that copy back if the setting disappears,
#     restarts Wi-Fi after 10 minutes offline, and reboots after 30
#   - the system log survives reboots (Raspberry Pi OS keeps it in memory
#     by default, to spare the card), capped at 50 MB
if [ -d /etc/cloud/cloud.cfg.d ]; then
	echo 'network: {config: disabled}' >/etc/cloud/cloud.cfg.d/99-partyfax-no-network.cfg
fi

BACKUP=/etc/partyfax/network-backup
if nmcli -t -f TYPE connection show 2>/dev/null | grep -qx '802-11-wireless'; then
	install -d -m 700 "$BACKUP"
	rm -f "$BACKUP"/*
	cp -p /etc/NetworkManager/system-connections/*.nmconnection /etc/netplan/*.yaml "$BACKUP"/ 2>/dev/null || true
	chmod 600 "$BACKUP"/* 2>/dev/null || true
	echo "Wi-Fi setting backed up ($(ls "$BACKUP" | wc -l) files)."
else
	echo "No Wi-Fi connection saved right now, so the backup is left as it was."
fi

for f in partyfax-netcheck.service partyfax-netcheck.timer; do
	curl -fsSL "https://raw.githubusercontent.com/$REPO/main/pi/$f" -o "$tmp"
	install -m 0644 "$tmp" "/etc/systemd/system/$f"
done
curl -fsSL "https://raw.githubusercontent.com/$REPO/main/pi/partyfax-netcheck" -o "$tmp"
install -m 0755 "$tmp" /usr/local/sbin/partyfax-netcheck

# Raspberry Pi OS already runs the hardware watchdog (a minute, which
# reboots a frozen Pi). An earlier version of this script shortened it to
# 14 seconds and reloaded systemd, which took longer than that on a Zero:
# the watchdog reset the Pi mid-install. Undo it.
rm -f /etc/systemd/system.conf.d/partyfax-watchdog.conf

# 50- sorts after Raspberry Pi OS's 40-rpi-volatile-storage.conf, so it wins.
install -d /etc/systemd/journald.conf.d
printf '[Journal]\nStorage=persistent\nSystemMaxUse=50M\n' >/etc/systemd/journald.conf.d/50-partyfax-persistent.conf

sync
systemctl daemon-reload
systemctl restart systemd-journald
journalctl --flush 2>/dev/null || true
systemctl enable --now partyfax-netcheck.timer
echo "Done. Log of what it does: journalctl -t partyfax-netcheck"

say "Writing less to the card"
# A power cut while the card is being written can leave a file empty (the
# Pi lost its Wi-Fi setting once, and an installer run its agent). The
# fewer writes, the smaller that window. What's left on purpose: the system
# log (capped, above), updates, and NetworkManager's own settings. The root
# filesystem is already mounted noatime, /tmp is in memory, and a damaged
# filesystem is repaired at boot (fsck.repair=yes).
#
#   - swap stays in compressed memory (zram) and no longer spills idle
#     pages to /var/swap on the card; the agent needs a fraction of the
#     RAM. A drop-in, not an edit of /etc/rpi/swap.conf, so updates never
#     stop to ask which version to keep. Raspberry Pi OS deletes the
#     446 MB /var/swap itself at the next boot.
#   - man-db's daily rebuild of the manual pages index is switched off
if [ -e /etc/rpi/swap.conf ]; then
	install -d /etc/rpi/swap.conf.d
	printf '[Main]\nMechanism=zram\n' >/etc/rpi/swap.conf.d/50-partyfax.conf
fi
systemctl disable --now man-db.timer 2>/dev/null || true
sync
echo "Done. /var/swap goes at the next reboot."

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

say "USB: the mainline driver"
# Raspberry Pi OS drives the Zero's USB port with its own dwc_otg driver.
# Partway through dark strips the printer drops off USB ("disabled by hub
# (EMI?)"), and with dwc_otg that sometimes left the port taking no data at
# all until the Pi rebooted, however often the printer was reset. Linux's own
# dwc2 driver for the same controller rides the drop out: the next print
# works. Takes effect at the next boot.
CONFIG=/boot/firmware/config.txt
if [ -f "$CONFIG" ] && ! grep -qx 'dtoverlay=dwc2,dr_mode=host' "$CONFIG"; then
	printf '\n[all]\ndtoverlay=dwc2,dr_mode=host\n' >>"$CONFIG"
	sync
	echo "Done. Reboot to use it: sudo reboot"
else
	echo "Already set."
fi

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
sync
systemctl enable --now partyfax-agent
sleep 5
journalctl -u partyfax-agent -n 5 --no-pager -o cat

cat <<'EOF'

Done. Look above for "connected, subscribed, announced online".

  Live log:   journalctl -u partyfax-agent -f
  Restart:    sudo systemctl restart partyfax-agent
  Update:     run the same curl | sudo bash line again
EOF
