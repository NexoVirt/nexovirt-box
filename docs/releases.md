# Releases

Pushing a tag `v*` runs `.github/workflows/release.yml`: it builds both binaries with the version
stamped, writes `SHA256SUMS`, signs `latest.json` with the release key (a repository secret) and
publishes a GitHub release with all files. Box's self-updater and `get.sh` read
`latest.json` + `latest.json.sig` from `https://github.com/NexoVirt/nexovirt-box/releases/latest/download`.

## Tech stack

Go (standard library HTTP server, SQLite via modernc.org/sqlite, gopsutil, creack/pty,
coder/websocket, go-webauthn, go-qrcode, yaml.v3 in the catalog importer; apps run with
`docker compose`, the reverse proxy is the official Caddy image) with a Svelte 5 UI using its own Apple-inspired design system (plain CSS, no UI framework)
and [Lucide](https://lucide.dev) icons (ISC), built into the binary.
