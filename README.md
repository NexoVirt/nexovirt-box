# NexoVirt Box

A web UI on top of a normal Linux server, part of the [NexoVirt](https://nexovirt.com) family.
Your server stays a normal server (SSH, your own services and config keep working); Box adds a
dashboard in the browser.

**Status: test version.** It shows the live system load (CPU per thread, memory, swap, load
average, uptime, the last 10 minutes as charts), the mounted drives and the network throughput,
and has:

- an **app store** with 625 Docker apps (install with your own data folder, open, start, stop,
  restart, update notices, logs, uninstall),
- **HTTPS for apps** under their own domain through a reverse proxy (Caddy, Let's Encrypt when the
  domain points to the server),
- a **file explorer** (browse, upload, download, preview, rename, move, delete),
- **firewall** rules with automatic undo when a change locks you out,
- a root **console** in the browser, an **activity log**, two-step sign-in (authenticator app,
  passkeys, recovery codes) and signed self-updates.

## Install

Debian or Ubuntu with systemd, amd64 or arm64:

```sh
sudo sh deploy/install.sh ./nexovirt-box-linux-amd64
```

Open `https://<server-ip>:8443`. The browser warns once about the self-signed certificate.
The first page asks for the **setup code** printed by the installer (also in
`/var/lib/nexovirt-box/web/setup-token`), then you create the admin account. The code is deleted
once the account exists, so nobody else on the network can claim a fresh Box.

## Build

Needs Go 1.25+ and Node 20+.

```sh
make build   # builds the UI, then bin/nexovirt-box-linux-{amd64,arm64}
make test    # go vet + go test
```

Development: `go run . -dev -listen 127.0.0.1:8080 -state ./tmp` (plain HTTP, never use `-dev`
on a reachable server) and `cd web && npm run dev` for the UI with hot reload.

## Security notes

- The web UI runs as the unprivileged user `nexovirt-box`. Root work (console, Docker, firewall,
  files, reverse proxy, updates) goes through `nexovirt-box helper`, a root service on a unix
  socket that only the web user may use and that only knows a fixed list of typed actions.
- The console is a root shell. The helper itself checks the password (and the 2FA code) and gives
  out a 5-minute proof bound to the session; without it the helper refuses to start a shell, so
  even a compromised web process cannot open one. Signing out closes open consoles.
- Two-step sign-in: TOTP with 10 single-use recovery codes, and passkeys. Browsers only allow
  passkeys on a domain name with a trusted certificate (or localhost), not on an IP address.
- Docker is only used through the helper, and it only runs app definitions from the catalog
  signed by NexoVirt. Generated passwords and your answers are in a private `.env` per app; the
  compose file is never rewritten.
- Firewall: Box only changes its own rules (its own nftables table, ufw rules tagged
  `nexovirt-box:`, or the firewalld rich rules it added) and shows the others read only. Each
  change must be confirmed within 60 seconds with a new request, else the helper restores the
  previous rules. Blocking the Box port needs a typed confirmation.
- Files: every path is resolved and must stay inside the allowed folders; /etc, /root, /proc,
  /sys, /dev, /boot and /run are refused (only an edit of `/etc/nexovirt-box/files.json` on the
  server can allow them). Previews are sandboxed; SVG and HTML are always downloads.
- Reverse proxy: Box never takes over a port another program uses; with 80/443 taken you pick
  other ports.
- Self-updates: only releases signed with the NexoVirt release key, applied on click; when the
  new version does not answer, the old binary is restored.
- Failed sign-ins, setup and password confirmations are limited to 5 per minute per IP (counted
  atomically, also across restarts). `X-Forwarded-For` is only trusted from `-trusted-proxies`.
- Passwords are bcrypt hashes; sessions SHA-256 hashes of random tokens; the database is readable
  by the web user only.

## Releases

`go run ./cmd/release-sign -version X -base <public URL> -out dist bin/nexovirt-box-linux-*`
writes `latest.json` and `latest.json.sig` (signed with `~/.config/nexovirt/box-release.key`,
kept outside the repository). Upload the folder to the update address set in Settings.

## App catalog

The built-in catalog is imported from the
[Runtipi App Store](https://github.com/runtipi/runtipi-appstore) (GPL-3.0),
the [CasaOS App Store](https://github.com/IceWhaleTech/CasaOS-AppStore) (Apache-2.0) and
[BigBearCasaOS](https://github.com/bigbeartechworld/big-bear-casaos) (MIT); every app shows
where it came from, and the license texts are in `catalog/LICENSES/`. App names and icons
belong to their projects. See `docs/app-store.md`.

## Tech

Go (standard library HTTP server, SQLite via modernc.org/sqlite, gopsutil, creack/pty,
coder/websocket, go-webauthn, go-qrcode, yaml.v3 in the catalog importer; apps run with
`docker compose`, the reverse proxy is the official Caddy image) with a Svelte 5 UI using its own Apple-inspired design system (plain CSS, no UI framework)
and [Lucide](https://lucide.dev) icons (ISC), built into the binary.

## License

AGPL-3.0. See [LICENSE](LICENSE).
