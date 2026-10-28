#!/bin/sh
# Installs NexoVirt Box on a systemd Linux server (Debian/Ubuntu).
# Usage: sudo sh install.sh [path/to/nexovirt-box-binary]
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

STATE=/var/lib/nexovirt-box
install -m 0755 "$BIN_SRC" /usr/local/bin/nexovirt-box
install -d -m 0700 "$STATE"

cat > /etc/systemd/system/nexovirt-box.service <<UNIT
[Unit]
Description=NexoVirt Box web UI
After=network-online.target
Wants=network-online.target

[Service]
# Test version: runs as root. Before a public release this becomes an unprivileged web
# process plus a small root helper with a fixed list of actions.
ExecStart=/usr/local/bin/nexovirt-box -listen :8443 -state $STATE
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
UNIT

SET_UP=0
[ -s "$STATE/box.db" ] && [ ! -s "$STATE/setup-token" ] && SET_UP=1

systemctl daemon-reload
systemctl enable nexovirt-box >/dev/null 2>&1
systemctl restart nexovirt-box

# Wait for the first start to write the setup code.
i=0
while [ "$SET_UP" -eq 0 ] && [ $i -lt 20 ] && [ ! -s "$STATE/setup-token" ]; do sleep 0.5; i=$((i+1)); done

IP=$(hostname -I 2>/dev/null | awk '{print $1}')
echo
echo "NexoVirt Box is running: https://${IP:-<server-ip>}:8443"
echo "Your browser warns about the self-signed certificate once; that is expected."
if [ -s "$STATE/setup-token" ]; then
  echo "Setup code: $(cat "$STATE/setup-token")"
fi
