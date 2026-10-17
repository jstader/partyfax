#!/usr/bin/env bash
# Nightly, age-encrypted backup of everything that cannot be rebuilt from
# git, to the NAS (pfnas:partyfax/backups, an rclone SMB remote -- see
# /root/.config/rclone/rclone.conf), kept 14 days:
#
#   wall/       every confirmed print, the /prints page
#   photos/     every printed photo
#   passwd      the broker's accounts
#   .env        tunnel token, MQTT and NAS passwords
#
# rclone's SMB backend rather than `mount -t cifs`: unprivileged LXCs cannot
# do kernel cifs/nfs mounts.
#
# To restore:
#   rclone copy pfnas:partyfax/backups/<file> /tmp/ --config /root/.config/rclone/rclone.conf
#   age -d -i /root/.config/age/backup-key.txt /tmp/<file> | tar -xz -C /tmp/restore
# then copy wall/ and photos/ back into the volumes (stack stopped) and
# passwd and .env into the checkout.
set -euo pipefail

REPO=/root/partyfax
VOLUMES=/var/lib/docker/volumes
RCLONE_REMOTE=pfnas:partyfax/backups
RCLONE_CONFIG=/root/.config/rclone/rclone.conf
RETENTION_DAYS=14
AGE_KEY_FILE=/root/.config/age/backup-key.txt
AGE_RECIPIENT=$(age-keygen -y "$AGE_KEY_FILE")

TIMESTAMP=$(date -u +%Y-%m-%dT%H%M%SZ)
ARCHIVE_NAME="partyfax-${TIMESTAMP}.tar.gz.age"
TMP_ARCHIVE="/tmp/${ARCHIVE_NAME}"

# --transform names each piece for what it is rather than its docker path.
tar -cz \
  --transform "s|^${VOLUMES#/}/fax_wall/_data|wall|" \
  --transform "s|^${VOLUMES#/}/fax_photos/_data|photos|" \
  --transform "s|^${REPO#/}/deploy/mosquitto/passwd|passwd|" \
  --transform "s|^${REPO#/}/.env|.env|" \
  -C / \
  "${VOLUMES#/}/fax_wall/_data" \
  "${VOLUMES#/}/fax_photos/_data" \
  "${REPO#/}/deploy/mosquitto/passwd" \
  "${REPO#/}/.env" \
  | age -r "$AGE_RECIPIENT" > "$TMP_ARCHIVE"

if [ ! -s "$TMP_ARCHIVE" ]; then
  echo "tar/age produced an empty file -- aborting, not keeping it" >&2
  rm -f "$TMP_ARCHIVE"
  exit 1
fi

rclone copy "$TMP_ARCHIVE" "$RCLONE_REMOTE/" --config "$RCLONE_CONFIG"
echo "Backup written: $RCLONE_REMOTE/$ARCHIVE_NAME ($(du -h "$TMP_ARCHIVE" | cut -f1))"
rm -f "$TMP_ARCHIVE"

rclone delete "$RCLONE_REMOTE/" --min-age "${RETENTION_DAYS}d" --config "$RCLONE_CONFIG"
