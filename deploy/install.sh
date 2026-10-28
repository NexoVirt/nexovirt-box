#!/bin/sh
# Installs or upgrades NexoVirt Box on a systemd Linux server (Debian/Ubuntu).
# Usage: sudo sh install.sh [path/to/nexovirt-box-binary]
#
# Two services:
#   nexovirt-box-helper  root, listens on /run/nexovirt-box/helper.sock, fixed list of actions
#   nexovirt-box         the web UI, runs as the unprivileged user nexovirt-box
set -eu

BIN_SRC="${1:-}"
if [ -z "$BIN_SRC" ]; then
  case "$(uname -m)" in
    x86_64|amd64) BIN_SRC="./nexovirt-box-linux-amd64" ;;
    aarch64|arm64) BIN_SRC="./nexovirt-box-linux-arm64" ;;
    *) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
  esac
fi
[ "$(id -u)" -eq 0 ] || { echo "Run this as root (sudo sh install.sh)." >&2; exit 1; }
[ -f "$BIN_SRC" ] || { echo "Binary not found: $BIN_SRC" >&2; exit 1; }
command -v systemctl >/dev/null || { echo "systemd is required." >&2; exit 1; }

USER_NAME=nexovirt-box
BASE=/var/lib/nexovirt-box
WEB=$BASE/web
APPS=$BASE/apps

if ! id "$USER_NAME" >/dev/null 2>&1; then
  useradd --system --home-dir "$WEB" --no-create-home --shell /usr/sbin/nologin "$USER_NAME"
fi

systemctl stop nexovirt-box 2>/dev/null || true
install -m 0755 "$BIN_SRC" /usr/local/bin/nexovirt-box
install -d -m 0755 -o root -g root "$BASE"
install -d -m 0700 -o "$USER_NAME" -g "$USER_NAME" "$WEB"
install -d -m 0700 -o root -g root "$APPS"

# Upgrade from the 0.1 test layout (everything in $BASE, running as root).
for f in box.db box.db-wal box.db-shm cert.pem key.pem setup-token; do
  if [ -e "$BASE/$f" ]; then mv -f "$BASE/$f" "$WEB/$f"; fi
done
chown -R "$USER_NAME:$USER_NAME" "$WEB"
find "$WEB" -maxdepth 1 -type f -name 'box.db*' -exec chmod 0600 {} +

cat > /etc/systemd/system/nexovirt-box-helper.service <<UNIT
[Unit]
Description=NexoVirt Box root helper (fixed list of actions for the web UI)
After=network-online.target docker.service
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/nexovirt-box helper -socket /run/nexovirt-box/helper.sock -user $USER_NAME -apps $APPS -web-state $WEB
RuntimeDirectory=nexovirt-box
RuntimeDirectoryMode=0755
RuntimeDirectoryPreserve=yes
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
UNIT

cat > /etc/systemd/system/nexovirt-box.service <<UNIT
[Unit]
Description=NexoVirt Box web UI
After=network-online.target nexovirt-box-helper.service
Wants=network-online.target nexovirt-box-helper.service

[Service]
User=$USER_NAME
Group=$USER_NAME
ExecStart=/usr/local/bin/nexovirt-box -listen :8443 -state $WEB
Restart=on-failure
RestartSec=3
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=read-only
PrivateTmp=yes
ReadWritePaths=$WEB /run/nexovirt-box
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
UNIT

SET_UP=0
[ -s "$WEB/box.db" ] && [ ! -s "$WEB/setup-token" ] && SET_UP=1

systemctl daemon-reload
systemctl enable nexovirt-box-helper nexovirt-box >/dev/null 2>&1
systemctl restart nexovirt-box-helper
systemctl restart nexovirt-box

i=0
while [ "$SET_UP" -eq 0 ] && [ $i -lt 20 ] && [ ! -s "$WEB/setup-token" ]; do sleep 0.5; i=$((i+1)); done

IP=$(hostname -I 2>/dev/null | awk '{print $1}')
echo
echo "NexoVirt Box is running: https://${IP:-<server-ip>}:8443"
echo "Your browser warns about the self-signed certificate once; that is expected."
if [ -s "$WEB/setup-token" ]; then
  echo "Setup code: $(cat "$WEB/setup-token")"
fi
