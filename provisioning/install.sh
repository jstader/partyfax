#!/usr/bin/env bash
# Idempotent host-level setup for the partyfax LXC. Run as root on Ubuntu.
# Safe to re-run.
#
#   - Docker CE from the official apt repo
#   - SSH: pubkey only (ssh/hardening.conf)
#   - CrowdSec + firewall bouncer, watching sshd. Not Caddy: every visitor
#     arrives through the tunnel from cloudflared's address, so an iptables
#     ban could only ever ban the tunnel. FAX_BANNED in .env bans visitors.
#   - Host firewall (firewall/apply-rules.sh), reapplied at boot
#   - age key for backups at /root/.config/age/backup-key.txt, generated once
#     and never regenerated: that would orphan every existing backup. Keep a
#     copy off this host; without it the backups are unrecoverable.
#   - rclone (SMB) and a nightly encrypted backup to the NAS, 14 days kept
#   - unattended-upgrades
#
# Touches nothing in docker-compose.yml or the running stack.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root." >&2
  exit 1
fi

echo "== Docker =="
if ! command -v docker >/dev/null 2>&1; then
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq ca-certificates curl
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  . /etc/os-release
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $VERSION_CODENAME stable" > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
else
  echo "Docker already installed, skipping."
fi

echo "== SSH hardening =="
# Refuse to disable password auth with no key to log in with: that would
# leave only the Proxmox console.
AUTHORIZED_KEYS=/root/.ssh/authorized_keys
if [ ! -s "$AUTHORIZED_KEYS" ] || ! grep -qE '^(ssh-(rsa|ed25519)|ecdsa-sha2-)' "$AUTHORIZED_KEYS"; then
  echo "ERROR: $AUTHORIZED_KEYS has no public key. Refusing to disable" >&2
  echo "password auth. Add a working key and re-run." >&2
  exit 1
fi
install -m 0644 "$SCRIPT_DIR/ssh/hardening.conf" /etc/ssh/sshd_config.d/hardening.conf
sshd -t
systemctl reload ssh 2>/dev/null || systemctl reload sshd

echo "== CrowdSec apt repo =="
CS_LIST=/etc/apt/sources.list.d/crowdsec_crowdsec.list
if [ ! -f "$CS_LIST" ]; then
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq apt-transport-https ca-certificates curl gnupg
  install -d -m 0755 /etc/apt/keyrings
  curl -fsSL "https://packagecloud.io/crowdsec/crowdsec/gpgkey" \
    | gpg --dearmor > /etc/apt/keyrings/crowdsec_crowdsec-archive-keyring.gpg
  chmod 0644 /etc/apt/keyrings/crowdsec_crowdsec-archive-keyring.gpg
  cat > "$CS_LIST" <<'EOF'
deb [signed-by=/etc/apt/keyrings/crowdsec_crowdsec-archive-keyring.gpg] https://packagecloud.io/crowdsec/crowdsec/any/ any main
EOF
fi
apt-get update -qq

echo "== Install CrowdSec + firewall bouncer =="
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq crowdsec crowdsec-firewall-bouncer-iptables

echo "== Acquisition config (replaces any auto-detected setup.*.yaml) =="
mkdir -p /etc/crowdsec/acquis.d
rm -f /etc/crowdsec/acquis.d/setup.*.yaml
install -m 0644 "$SCRIPT_DIR/crowdsec/sshd.yaml" /etc/crowdsec/acquis.d/sshd.yaml
install -m 0644 "$SCRIPT_DIR/crowdsec/linux.yaml" /etc/crowdsec/acquis.d/linux.yaml

echo "== Collections =="
cscli hub update
cscli collections install crowdsecurity/sshd
cscli collections install crowdsecurity/linux

echo "== Enable + restart services =="
systemctl enable --now crowdsec
systemctl restart crowdsec
systemctl enable --now crowdsec-firewall-bouncer
systemctl restart crowdsec-firewall-bouncer

echo "== Host firewall =="
install -m 0755 "$SCRIPT_DIR/firewall/apply-rules.sh" /usr/local/sbin/partyfax-apply-firewall.sh
install -m 0644 "$SCRIPT_DIR/firewall/partyfax-firewall.service" /etc/systemd/system/partyfax-firewall.service
systemctl daemon-reload
systemctl enable --now partyfax-firewall.service

echo "== Backup encryption key =="
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq age
mkdir -p /root/.config/age
if [ ! -f /root/.config/age/backup-key.txt ]; then
  age-keygen -o /root/.config/age/backup-key.txt
  chmod 600 /root/.config/age/backup-key.txt
  echo "New backup key generated. Save /root/.config/age/backup-key.txt in"
  echo "the password manager: without it every backup is unrecoverable."
fi

echo "== rclone (SMB backend for NAS backups) =="
# Regenerated every run, so rotating the NAS password in .env takes effect
# on the next install.sh.
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq rclone
ENV_FILE="$SCRIPT_DIR/../.env"
mkdir -p /root/.config/rclone
if [ -f "$ENV_FILE" ]; then
  CIFS_HOST=$(grep -E '^CIFS_HOST=' "$ENV_FILE" | cut -d= -f2-)
  CIFS_USERNAME=$(grep -E '^CIFS_USERNAME=' "$ENV_FILE" | cut -d= -f2-)
  CIFS_PASSWORD=$(grep -E '^CIFS_PASSWORD=' "$ENV_FILE" | cut -d= -f2-)
  if [ -n "$CIFS_USERNAME" ] && [ -n "$CIFS_PASSWORD" ]; then
    OBSCURED_PASS=$(rclone obscure "$CIFS_PASSWORD")
    cat > /root/.config/rclone/rclone.conf <<EOF
[pfnas]
type = smb
host = $CIFS_HOST
user = $CIFS_USERNAME
pass = $OBSCURED_PASS
port = 445
EOF
    chmod 600 /root/.config/rclone/rclone.conf
  else
    echo "CIFS_USERNAME/CIFS_PASSWORD not set in .env yet -- skipping rclone.conf."
    echo "Backups fail until they are filled in and install.sh is re-run."
  fi
fi

echo "== Nightly backup =="
install -m 0755 "$SCRIPT_DIR/backup/backup.sh" /usr/local/sbin/partyfax-backup.sh
install -m 0644 "$SCRIPT_DIR/backup/partyfax-backup.service" /etc/systemd/system/partyfax-backup.service
install -m 0644 "$SCRIPT_DIR/backup/partyfax-backup.timer" /etc/systemd/system/partyfax-backup.timer
systemctl daemon-reload
systemctl enable --now partyfax-backup.timer

echo "== unattended-upgrades =="
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq unattended-upgrades

echo "== Status =="
systemctl is-active crowdsec crowdsec-firewall-bouncer partyfax-firewall unattended-upgrades
systemctl list-timers partyfax-backup.timer --no-pager
iptables -S INPUT
iptables -S DOCKER-USER
