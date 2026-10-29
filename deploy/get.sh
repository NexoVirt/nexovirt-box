#!/bin/sh
# Installs or upgrades NexoVirt Box from the latest GitHub release:
#
#   curl -fsSL https://raw.githubusercontent.com/NexoVirt/nexovirt-box/main/deploy/get.sh | sudo sh
#
# It downloads the signed release manifest (latest.json + latest.json.sig), checks the Ed25519
# signature with openssl against the release key below, checks the sha256 of the binary and of
# install.sh against the manifest, and then runs install.sh. Running it again upgrades, or does
# nothing when the newest version is already installed.
#
# Environment:
#   NEXOVIRT_BOX_VERSION=0.2.0  install exactly this release instead of the latest one
#   NEXOVIRT_BOX_FORCE=1        reinstall even when that version is already installed
#   GITHUB_TOKEN=...            read release assets through the GitHub API (needed while the
#                               repository is private)
set -eu

REPO=NexoVirt/nexovirt-box

# Public half of the Box release key (same key as internal/catalog/key.go, checked by a test).
RELEASE_PUB='-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAovGXLAbEZ+DXv7ZpNQcrdKguIbLJ1nn8eaTI7CBf+L8=
-----END PUBLIC KEY-----'

die() { echo "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "Run this as root (curl -fsSL ... | sudo sh)."
for c in curl openssl sha256sum base64 awk; do
  command -v "$c" >/dev/null 2>&1 || die "Missing tool: $c (apt-get install -y curl openssl coreutils gawk)."
done

# Supported systems only: Debian 12/13, Ubuntu 22.04/24.04.
[ -r /etc/os-release ] || die "Unsupported system: /etc/os-release is missing."
. /etc/os-release
case "${ID:-}:${VERSION_ID:-}" in
  debian:12|debian:13|ubuntu:22.04|ubuntu:24.04) ;;
  *) die "Unsupported system: ${PRETTY_NAME:-unknown}. NexoVirt Box supports Debian 12/13 and Ubuntu 22.04/24.04." ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "Unsupported CPU architecture: $(uname -m) (amd64 and arm64 are supported)." ;;
esac

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

if [ -n "${NEXOVIRT_BOX_VERSION:-}" ]; then
  TAG="v${NEXOVIRT_BOX_VERSION#v}"
  BASE="https://github.com/$REPO/releases/download/$TAG"
  API="https://api.github.com/repos/$REPO/releases/tags/$TAG"
else
  BASE="https://github.com/$REPO/releases/latest/download"
  API="https://api.github.com/repos/$REPO/releases/latest"
fi

# fetch <asset name> <destination>
if [ -n "${GITHUB_TOKEN:-}" ]; then
  curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/vnd.github+json" "$API" -o "$TMP/release.json" \
    || die "Could not read the release from GitHub (check GITHUB_TOKEN and the version)."
  fetch() {
    # Asset objects list their API "url" before their "name"; map name -> url.
    u=$(grep -o -E '"url": *"[^"]*/releases/assets/[0-9]+"|"name": *"[^"]*"' "$TMP/release.json" | awk -v want="$1" '
      /releases\/assets\// { sub(/^"url": *"/, ""); sub(/"$/, ""); url = $0; next }
      { sub(/^"name": *"/, ""); sub(/"$/, ""); if (url != "" && $0 == want) { print url; exit } url = "" }')
    [ -n "$u" ] || die "The release has no file named $1."
    curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/octet-stream" "$u" -o "$2"
  }
else
  fetch() { curl -fsSL "$BASE/$1" -o "$2"; }
fi

echo "Downloading the release manifest ..."
fetch latest.json "$TMP/latest.json" || die "Could not download latest.json. While the repository is private, set GITHUB_TOKEN."
fetch latest.json.sig "$TMP/latest.json.sig" || die "Could not download latest.json.sig."

printf '%s\n' "$RELEASE_PUB" > "$TMP/release.pem"
base64 -d < "$TMP/latest.json.sig" > "$TMP/sig.bin" 2>/dev/null || die "The release signature is damaged."
openssl pkeyutl -verify -pubin -inkey "$TMP/release.pem" -rawin -in "$TMP/latest.json" -sigfile "$TMP/sig.bin" >/dev/null 2>&1 \
  || die "The release manifest is not signed by NexoVirt. Nothing was installed."

# field <files key> <sha256|url>: read one value from the (indented, signed) manifest.
field() {
  awk -v key="\"$1\": {" -v f="\"$2\":" '
    index($0, key) { inside = 1; next }
    inside && /}/ { exit }
    inside && index($0, f) { sub(/^[^:]*: *"/, ""); sub(/".*$/, ""); print; exit }' "$TMP/latest.json"
}
VERSION=$(awk -F'"' '/^  "version":/ { print $4; exit }' "$TMP/latest.json")
BIN_URL=$(field "$ARCH" url); BIN_SUM=$(field "$ARCH" sha256)
INS_URL=$(field install.sh url); INS_SUM=$(field install.sh sha256)
[ -n "$VERSION" ] && [ -n "$BIN_URL" ] && [ ${#BIN_SUM} -eq 64 ] || die "This release has no build for $ARCH."
[ -n "$INS_URL" ] && [ ${#INS_SUM} -eq 64 ] || die "This release has no signed install.sh."

if [ -x /usr/local/bin/nexovirt-box ] && [ -z "${NEXOVIRT_BOX_FORCE:-}" ] \
  && [ "$(/usr/local/bin/nexovirt-box version 2>/dev/null || true)" = "$VERSION" ]; then
  echo "NexoVirt Box $VERSION is already installed. Nothing to do (NEXOVIRT_BOX_FORCE=1 reinstalls)."
  exit 0
fi

echo "Downloading NexoVirt Box $VERSION for $ARCH ..."
fetch "${BIN_URL##*/}" "$TMP/nexovirt-box" || die "Could not download the binary."
fetch "${INS_URL##*/}" "$TMP/install.sh" || die "Could not download install.sh."
[ "$(sha256sum "$TMP/nexovirt-box" | awk '{print $1}')" = "$BIN_SUM" ] || die "The binary does not match the signed manifest. Nothing was installed."
[ "$(sha256sum "$TMP/install.sh" | awk '{print $1}')" = "$INS_SUM" ] || die "install.sh does not match the signed manifest. Nothing was installed."

chmod 0755 "$TMP/nexovirt-box"
sh "$TMP/install.sh" "$TMP/nexovirt-box"
