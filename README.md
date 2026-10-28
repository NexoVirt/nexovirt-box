# NexoVirt Box

A web UI on top of a normal Linux server, part of the [NexoVirt](https://nexovirt.com) family.
Your server stays a normal server (SSH, your own services and config keep working); Box adds a
dashboard in the browser.

**Status: first test version.** It shows the live system load (CPU per thread, memory, swap,
load average, uptime), the mounted drives, the network throughput, and gives you a root console
in the browser. Apps, file explorer and firewall management come later.

## Install

Debian or Ubuntu with systemd, amd64 or arm64:

```sh
sudo sh deploy/install.sh ./nexovirt-box-linux-amd64
```

Open `https://<server-ip>:8443`. The browser warns once about the self-signed certificate.
The first page asks for the **setup code** printed by the installer (also in
`/var/lib/nexovirt-box/setup-token`), then you create the admin account. The code is deleted
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
- Sign-in and setup are limited to 5 attempts per minute per IP.
- **This test version runs as root.** Before a public release Box will be split into an
  unprivileged web process and a small root helper that only accepts a fixed list of actions.
- Passwords are stored as bcrypt hashes; sessions as SHA-256 hashes of random tokens.

## Tech

Go (standard library HTTP server, SQLite via modernc.org/sqlite, gopsutil, creack/pty,
coder/websocket) with a Svelte 5 UI styled with [Tabler](https://tabler.io) (MIT) and Tabler
Icons (MIT), built into the binary.

## License

AGPL-3.0. See [LICENSE](LICENSE).
