#!/usr/bin/env bash
# Idempotent host firewall for the partyfax container. Run as root. IPv4 only.
#
# The site arrives through a Cloudflare Tunnel that cloudflared dials out to,
# so nothing has to be reachable from the network except SSH:
#
#   INPUT: host-native services. 22 only.
#
#   DOCKER-USER, inbound: published container ports, which are DNAT'd and
#   never touch INPUT. The compose file publishes none; dropping everything
#   new from eth0 here keeps it that way if a port is ever published by
#   mistake.
#
#   DOCKER-USER, outbound: containers reach the internet and nothing else.
#   cloudflared, Caddy and Mosquitto all face the internet, and without this
#   a foothold in any of them could reach every service on the LAN -- the
#   Proxmox UI, the NAS, the router. It also stops a tunnel pointed at a LAN
#   address from the Cloudflare side. DNS keeps working: Docker's resolver
#   asks the upstream from the host's own stack, not the container's.
#
#   This is only the container-level wall. Root on this LXC could remove it,
#   so the same egress block belongs on the Proxmox firewall for the CT too.
#
# INPUT allow rules go AFTER crowdsec-firewall-bouncer's jump, so a banned
# address is refused before it reaches the allow for 22.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root." >&2
  exit 1
fi

EXT_IF=eth0

# --- INPUT ---
iptables -C INPUT -i lo -j ACCEPT 2>/dev/null || iptables -A INPUT -i lo -j ACCEPT
iptables -C INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null \
  || iptables -A INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -C INPUT -p tcp --dport 22 -j ACCEPT 2>/dev/null || iptables -A INPUT -p tcp --dport 22 -j ACCEPT
iptables -P INPUT DROP

# --- DOCKER-USER outbound: no private, link-local or CGNAT destinations ---
for net in 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 100.64.0.0/10; do
  iptables -C DOCKER-USER -o "$EXT_IF" -d "$net" -j DROP 2>/dev/null \
    || iptables -I DOCKER-USER -o "$EXT_IF" -d "$net" -j DROP
done

# --- DOCKER-USER inbound: replies to the containers' own connections only ---
iptables -C DOCKER-USER -i "$EXT_IF" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN 2>/dev/null \
  || iptables -A DOCKER-USER -i "$EXT_IF" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
iptables -C DOCKER-USER -i "$EXT_IF" -j DROP 2>/dev/null || iptables -A DOCKER-USER -i "$EXT_IF" -j DROP
