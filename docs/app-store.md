# App store design

## Goals

- One click installs of Docker apps on the server Box runs on, with hundreds of apps from day one.
- Never rewrite a user's compose file after install; secrets never inside it.
- Only the root helper talks to Docker, and it only runs compose files from the signed catalog.

## App format

Each app is one JSON file (`catalog/apps/<id>.json`):

| Field | Meaning |
|---|---|
| `id`, `name`, `version`, `category`, `tagline`, `description` | what the store shows |
| `icon`, `screenshots` | upstream image URLs, fetched and cached by Box (the browser never loads third-party URLs) |
| `website`, `source` | links |
| `architectures` | `amd64`, `arm64`; empty = any |
| `main_port`, `scheme` | default host port of the web UI and `http`/`https` (0 = no web UI) |
| `questions` | typed install questions (below) |
| `compose` | the compose file, stored verbatim on install as `compose.yaml` |
| `docker_socket` | true when the app mounts the Docker socket (shown as a warning: that is root access) |
| `attribution` | store name, URL and license of the catalog the app came from |
| `first_login` | how to sign in the first time when the app makes its own admin: `username`, `password_from_log_regex` (one capture group, matched against the first 1000 log lines), `password_from_env` (a variable of the app's `.env`), `note` |
| `notes` | "Getting started" text (plain text, links are made clickable); imported from CasaOS/BigBear `tips.before_install` |

Question types: `text`, `password`, `number`, `email`, `domain`, `boolean`, `select` (`options`), plus `port`.
Fields: `key` (env variable), `label`, `hint`, `placeholder`, `default`, `required`, `min`, `max`,
`generate` (password filled with a random value when left empty, shown as "generated automatically").

Variables Box always provides: `APP_ID`, `APP_DATA_DIR` (`/var/lib/nexovirt-box/apps/<id>/data`), `APP_PORT`,
`APP_DOMAIN`/`APP_HOST` (server IP), `APP_PROTOCOL`, `TZ`, `PUID`, `PGID`, `SHARED_DIR` and `ROOT_FOLDER_HOST`
(both `/var/lib/nexovirt-box/shared`, a media folder apps share; never Box's state dir), `INTERNAL_IP`,
`NETWORK_INTERFACE`.

Passwords: every `password` question is generated (strong random) unless the admin sets one (with confirm);
keys containing `SUDO` (a sudo password inside the container) are always generated. My Apps shows them under
Login details (`GET /api/apps/{id}/credentials`, audited).

First login: Login details (`GET /api/apps/{id}/credentials`, audited) returns `{items, note, notes}`. Without
`first_login` the helper scans the first 1000 log lines for common patterns (initial/default/generated password,
admin password, default credentials) and offers up to three values marked "From the logs". Curated `first_login`
entries live in `cmd/catalog-import/firstlogin.go` (`knownFirstLogin`); `go run ./cmd/catalog-import -enrich -casaos
<dir> -bigbear <dir>` refreshes `first_login` and `notes` in the existing catalog and re-signs it without a full
re-import. A store tip that names a default password the importer replaced with a generated one is dropped.

Logs: `GET /api/apps/{id}/logs?tail=&since=&service=` returns `{services, lines: [{s, t, m}]}` (per service, Docker
timestamps, merged by time, tail up to 10000). The Logs page `/apps/<id>/logs` polls with `since`.

Host folders: before install, retry, start and update the helper renders `docker compose config` and refuses binds
(and local bind volumes) of Box's state outside the app's own folders, `/etc/nexovirt-box`, `/run/nexovirt-box`,
`/` and `/etc`, `/root`, `/proc`, `/sys`, `/boot` (or a folder containing them), secret files (`/etc/shadow`,
`/etc/ssh`, ...) and the Docker socket (only for apps marked `docker_socket` after the admin agreed in the form).
Paths are resolved through symlinks (`appsafety.go`).

Jobs: saved in SQLite (`jobs`, log tail). At web start unfinished jobs become `interrupted`; a helper restart marks
the running job interrupted; a watchdog stops jobs with no output for 15 minutes; the helper cancels long app
actions when the web process disconnects. `.installing` in an app folder marks an unfinished install
(state `interrupted`, "Retry install" resumes with the same files). `GET /api/jobs` lists running jobs so the UI
keeps showing progress after a reload. `app.json` `stopped` is the desired state (no alert for apps stopped on
purpose).

## Install layout (root-owned, 0700)

```
/var/lib/nexovirt-box/apps/<id>/
  compose.yaml   verbatim from the catalog, 0600, never rewritten
  .env           answers, generated secrets and Box variables, 0600
  app.json       name, version, port, catalog source, install time
  data/          APP_DATA_DIR (unless a data folder was chosen at install, recorded in app.json)
```

A chosen data folder must be new or empty and outside system folders. "Delete data" removes it only when Box
created it.

Docker runs `docker compose --project-name nxbox-<id> -f compose.yaml --env-file .env ...`. Before `up`, the helper
renders `docker compose config` and refuses ports that are already in use. Update = `pull` + `up -d` (the compose
file stays). Uninstall = `down`, then either delete the folder or keep `data/` for a later reinstall.

## Catalog

- `catalog/index.json`: `generated_at`, and per app the summary fields plus `file` and `sha256`.
- `catalog/index.sig`: Ed25519 signature (base64) over the exact bytes of `index.json`.
- The public key is compiled into Box (`internal/catalog/key.go`). The built-in catalog is embedded in the binary.
  A synced catalog (the "Online catalog address" in Settings, or `-catalog-url`) is verified by the web process for
  display and again by the helper before every install; an older `generated_at` than the built-in one is refused
  (no rollback).
- **Release key**: the private half lives only at `~/.config/nexovirt/box-catalog.key` (0600) on the machine that
  builds the catalog, never in this repository. `go run ./cmd/catalog-import ...` signs with it by default.
- **Dev key**: `keys/catalog-dev.key` stays in the repo so anyone can rebuild a catalog for testing; only builds with
  `-tags devkey` accept it.

### Hosting an online catalog (owner decides)

The online catalog is plain static files: `index.json`, `index.sig` and `apps/*.json`, exactly as in `catalog/`.
Any HTTPS static host works, because Box trusts the signature, not the host. Options:

- a path on the NexoVirt website (for example `https://nexovirt.com/box/catalog`, the same Plesk host),
- a GitHub Pages site or GitHub release assets of a public catalog repository,
- an object storage bucket (Cloudflare R2, S3) behind HTTPS.

Publishing = run the importer, upload the folder, then raise `generated_at` (the importer does). Boxes pick it up
with "Sync now" in Settings.

## Importer (`cmd/catalog-import`)

Offline tool that builds `catalog/` from local checkouts of:

| Store | License | Notes |
|---|---|---|
| runtipi/runtipi-appstore | GPL-3.0 | typed `form_fields` become questions; Traefik labels and `tipi_main_network` removed |
| IceWhaleTech/CasaOS-AppStore | Apache-2.0 | `x-casaos` metadata removed from the compose; `/DATA/AppData/$AppID` becomes `${APP_DATA_DIR}` |
| bigbeartechworld/big-bear-casaos | MIT | same format as CasaOS |

Umbrel apps are not imported (no license).

Rules: dedupe by normalized name and by main image (registry, `library/` and tag ignored, databases and other
helper images skipped), Runtipi first, then CasaOS, then BigBear; every merge is printed; skip apps with `privileged`, host network or
host PID; literal passwords in the compose (`POSTGRES_PASSWORD: tipi`) become generated secrets, shared across
every variable that used the same literal so linked services still match; the web port becomes `${APP_PORT}`.
Each app keeps its store, URL and license in `attribution`; the full license texts are in `catalog/LICENSES/`.

## Image check (`cmd/catalog-check`)

Run after every import: `go run ./cmd/catalog-check`. It resolves every image of every app anonymously, the way a
Box pulls it (Docker Hub tags through the Hub API, which does not count against the anonymous pull limit; digests on
Docker Hub with a HEAD; other registries through the OCI distribution API: token from `WWW-Authenticate`, then the
manifest, and the config blob for single-arch images). 8 workers, retries with backoff, a 429 pauses that registry
and resumes. Results are cached in `.catalog-check-cache.json` (gitignored, 7 days for definite answers, errors are
always re-checked), so reruns are cheap.

- Compose shapes are repaired and then validated (`internal/catalog/shape.go`): `- KEY: value` environment items
  become `KEY=value`, `network-mode` style typos become the real attribute, depends_on entries naming a missing
  service are dropped, an env_file other than `.env` becomes optional. Any remaining shape error (unknown service
  attribute, environment/labels not a map of values or list of strings, ports/volumes not a list) fails the run, so
  nothing Docker would refuse gets signed. An app that a fix reveals as host network/privileged is dropped (`host`).
  The importer runs the same repair and validation.
- Apps with a missing or private image are dropped.
- `architectures` becomes what every image really ships (`amd64`, `arm64`); Box refuses to install an app on an
  architecture outside that list, and the catalog list API returns `supported` per app for this server.
- Images that could not be checked never drop or narrow an app. Apps following `latest` are kept and listed.
- The report (`docs/catalog-image-report.md`) lists removed apps with the reason, amd64-only apps, `latest` apps and
  unchecked images. The catalog is then re-signed with the release key (`-dry-run` only reports).
