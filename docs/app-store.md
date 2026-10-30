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
| `first_login` | how to sign in the first time when the app makes its own admin: `username`, `password` (a fixed default the app ships with), `password_from_env` (a variable of the app's `.env`), `password_from_file` (a file the app writes, path relative to `APP_DATA_DIR`) with optional `password_from_file_regex` (one capture group; without it the whole file, one short line, is the password), `password_from_log_regex` (one capture group, the last match in the first 1000 log lines), `note` |
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

First login: Login details (`GET /api/apps/{id}/credentials`, audited; also in the desktop tile menu) returns
`{items, pending, note, notes}`. The first-login user and password come first, then the app's other answers (a value
shows once per kind). Each item may carry `source`: `log`, `file` or `default`, shown as a pill. `pending` is true while
a named password source has nothing yet (the app has not started far enough); the UI then offers "Check again".
The helper reads `password_from_file` through `os.Root` on the app's data folder (no `..`, no absolute path, no symlink
leading outside, regular files up to 64 KiB only; `FirstLogin.FilePath` validates the catalog value). Without
`first_login` the helper scans the first 1000 log lines for common patterns (initial/default/generated password,
admin password, default credentials) and offers up to three values marked "From the logs". Curated `first_login`
entries live in `cmd/catalog-import/firstlogin.go` (`knownFirstLogin`, plus `knownHTTPS` for apps whose UI only answers
on HTTPS); `go run ./cmd/catalog-import -enrich [-casaos <dir> -bigbear <dir>]` refreshes `first_login`, those scheme
fixes and (with the store dirs) `notes` in the existing catalog and re-signs it without a full re-import. A store tip
that names a default password the importer replaced with a generated one is dropped.

Curated today: Crafty (generated password in `data/config/default-creds.txt`), AList and qBittorrent (from the logs),
OpenList, Davis, Dockpeek, Etherpad, Healthchecks, Kopia, MicroBin, MineOS, Nextcloud with SMB, NZBGet, Seafile,
SnapOtter, Stirling-PDF, TaskingAI, Tududi, WebDAV (generated password + fixed user name), and fixed defaults for
AList Sync, BookStack, Brave, Calibre-Web, Chrome, copyparty, Deluge, FileBrowser Quantum, Grafana, Grocy, Kanboard,
Mixpost (and Pro), Peppermint, SolidPing, Speedtest Tracker and Umami.

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

## Updates with rollback (`appupdate.go`, `appautoupdate.go`)

An update (`app_action` op `update`) stops the app and copies its folder, a data folder outside it and its named
volumes into `<state>/rollback/<id>/` (one copy per app, replaced by the next update, removed on uninstall), after
checking free space (copy size + 512 MB; too little = the update is refused with "Not enough free space for the
rollback copy", and the UI offers "Update anyway" = `no_copy`). The images it ran are tagged
`nxbox-rollback/<id>:<n>` so an image prune keeps them. Then `pull`, the install checks, `up -d` and a health check:
every container `running` in two polls 5 s apart and the main port accepting TCP, within 90 s. On failure the old
image ids get their references back (`docker tag`), the copied folders replace the new ones (`putBack`: current folder
aside, copy in, aside removed; a failed copy puts the current folder back) and the app starts again. `app.json`
`last_update` records `{at, result: ok|rolled_back|failed|manual_rollback, rollback}`; with a copy the app menu offers
"Roll back last update" (op `rollback`, same restore, data written since is lost).

Automatic updates: per app `{on, from, to}` (HH:MM, server time, window may wrap midnight) in the web setting
`app_auto_updates` (`GET /api/apps/auto-updates`, `POST /api/apps/{id}/auto-update`, audited `app.auto_update`). Every 5
minutes the web process starts the normal update job for apps with an available update (from the last update check)
inside their window, at most once per 20 hours; audited as user `auto`, result sent as the `app_update` notification.

## Resources per app (`appresources.go`)

`GET /api/apps/stats` (helper `app_stats`) returns per app CPU (percent of one core, summed over its containers),
memory and the smallest container limit, network bytes since start (`docker stats --no-stream`, containers mapped
through the `com.docker.compose.project=nxbox-<id>` label) and disk use of the app folder, an outside data folder and
its volumes (measured in the background, cached 10 minutes, -1 until the first result). Shown in Monitor > Apps.

Limits (app menu "Resource limits", `POST /api/apps/{id}/limits` {cpus, memory_mb}, job `limits`, helper `app_limits`):
0.1 to all cores, 32 MB to the server's memory, the same for every service. Box writes `limits.yaml` (only `cpus` and
`mem_limit` per service) next to `compose.yaml`; `compose()` adds it as a second `-f` file, so the app's compose file
stays untouched; `checkConfig` refuses a `limits.yaml` with any other key (it can come back from a backup), and a
failed recreate puts the previous file back. `app.json` `limits` mirrors it. Every 2 minutes the web process warns
(`app_resources` notification) about apps at 90 % or more of their memory limit.

## Stacks (`internal/catalog/stacks.go`, `internal/box/stacks.go`)

A stack installs several catalog apps together (media, smart home, development, personal cloud). Stacks are written by
hand in `catalog/stacks.json` (id, name, description, notes on how the apps work together, apps with optional fixed
answers, never secrets) and copied into the signed `index.json` by `catalog.WriteDir` (so the importer, the image
check and `cmd/catalog-resign` all keep them; apps the catalog no longer has are left out, a stack with fewer than two
apps is dropped). Box only reads stacks from the verified index. After editing `stacks.json`, re-sign:
`go run ./cmd/catalog-resign` (key `~/.config/nexovirt/box-catalog.key`, never committed).

The App Store shows them above the apps ("Stacks"); "Install all" (`POST /api/stacks/{id}/install`, one job) runs the
normal helper install for each app that is not installed yet, in order, with the stack's answers; it stops at the first
failure (installed apps stay) and then shows the notes. The apps are wired through Box's shared media folder (the
Runtipi apps mount `${ROOT_FOLDER_HOST}/media`); connections inside the apps (API keys, download clients) are explained
in the notes, Box does not change app settings.

## Start and stop times (`appschedule.go`)

App menu "Start and stop times": per weekday an optional start and stop time (server time), stored in the web setting
`app_schedules` (`GET /api/apps/schedules`, `POST /api/apps/{id}/schedule`, audited `app.schedule`). Every 30 s the web
process runs the normal start/stop job for times due within the last 10 minutes, once per app, kind and day; audited as
user `schedule`. A scheduled stop sets the desired state, so it raises no "app down" alert.

## Clone (`appclone.go`)

App menu "Clone" (`POST /api/apps/{id}/clone` {name, port}, job `clone`, helper `app_clone`): a new id (slug of the
name, not a store id), the compose file copied verbatim, `.env` copied with `APP_ID`, `APP_DATA_DIR` (and `APP_PORT`
when a port was given) repointed; data starts empty and named volumes are new through the new project name. Box writes
`clone.yaml` (extra compose file, validated by `checkCloneFile`: only `container_name: nxbox-<id>-<service>` and
`ports: !override` lines) that renames fixed container names and moves published ports that another app (running or
not) or the server uses to the next free port; the given port becomes the clone's main port. Refused: binds outside the
clone's own folder and the shared folder (it would share files with the original), port ranges. Domain, sign-in,
limits and update history are not taken over; `app.json` `clone_of` names the original. A source behind the Box sign-in
gives a clone published on 127.0.0.1 only, unless the admin turns on "Reachable without sign-in" (`expose`, audited
`app.clone_expose`). Volumes and networks with a fixed name get a name in the clone's project
(`nxbox-<id>_<key>`, written to `clone.yaml`); an external volume refuses the clone (external networks stay shared,
they hold no data). A failed clone is cleaned up with `down` (never `--volumes`) plus `docker volume rm` of volumes
that carry its project label and its name prefix only, so the original's volumes are never touched. Update roll back
copies and restores only volumes labelled with the app's own project.

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
