# NexoVirt Box

A web UI on top of a normal Linux server, part of the [NexoVirt](https://nexovirt.com) family.
Your server stays a normal server (SSH, your own services and config keep working); Box adds a
dashboard in the browser.

**Status: test version.** It shows the live system load (CPU per thread, memory, swap, load
average, uptime, the last 10 minutes as charts), the mounted drives and the network throughput,
has an **app store** with 677 Docker apps (install, open, start, stop, restart, update, logs,
uninstall), a root console in the browser and an activity log. File explorer and firewall
management come later.

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

- The console is a root shell. It is only reachable after sign-in; the WebSocket checks the
  Origin, and every write request must come from the Box's own origin (plus SameSite=Strict
  session cookies).
- The web UI runs as the unprivileged user `nexovirt-box`. Root work (the console, Docker) goes
  through `nexovirt-box helper`, a root service on a unix socket that only the web user may use
  and that only knows a fixed list of actions.
- Docker is only used through that helper, and it only runs app definitions from the catalog
  signed by NexoVirt. Generated passwords and your answers are stored in a private `.env` per
  app, never in the compose file.
- Opening the console asks for your password again when the last confirmation is older than
  5 minutes. Signing out closes open consoles. Sign-ins and console sessions are in the
  activity log.
- Sign-in, setup and password confirmation are limited to 5 attempts per minute per IP, also
  across restarts. `X-Forwarded-For` is only trusted from proxies listed in `-trusted-proxies`.
- Passwords are stored as bcrypt hashes; sessions as SHA-256 hashes of random tokens; the
  database is readable by the web user only.

## App catalog

The built-in catalog is imported from the
[Runtipi App Store](https://github.com/runtipi/runtipi-appstore) (GPL-3.0),
the [CasaOS App Store](https://github.com/IceWhaleTech/CasaOS-AppStore) (Apache-2.0) and
[BigBearCasaOS](https://github.com/bigbeartechworld/big-bear-casaos) (MIT); every app shows
where it came from, and the license texts are in `catalog/LICENSES/`. App names and icons
belong to their projects. See `docs/app-store.md`.

## Tech

Go (standard library HTTP server, SQLite via modernc.org/sqlite, gopsutil, creack/pty,
coder/websocket, yaml.v3 in the catalog importer; apps run with `docker compose`) with a Svelte 5 UI using its own Apple-inspired design system (plain CSS, no UI framework)
and [Lucide](https://lucide.dev) icons (ISC), built into the binary.

## License

AGPL-3.0. See [LICENSE](LICENSE).
