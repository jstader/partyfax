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
#   - unattended-upgrades
#
# No backups here, on purpose: Proxmox's nightly job backs up the whole
# container, so this box holds no NAS credentials and needs no route to the
# NAS. See deploy/README.md.
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

echo "== Docker DNS =="
# Public resolvers, not the router. firewall/apply-rules.sh blocks every
# container from the LAN, and build steps (unlike the app's own networks)
# ask the configured DNS server directly from inside the container: pointed
# at the router, every `apk add` in a Dockerfile fails.
if ! cmp -s "$SCRIPT_DIR/docker/daemon.json" /etc/docker/daemon.json; then
  install -m 0644 "$SCRIPT_DIR/docker/daemon.json" /etc/docker/daemon.json
  systemctl restart docker
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

echo "== unattended-upgrades =="
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq unattended-upgrades

echo "== Status =="
systemctl is-active crowdsec crowdsec-firewall-bouncer partyfax-firewall unattended-upgrades
iptables -S INPUT
iptables -S DOCKER-USER
