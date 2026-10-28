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

Question types: `text`, `password`, `number`, `email`, `domain`, `boolean`, `select` (`options`), plus `port`.
Fields: `key` (env variable), `label`, `hint`, `placeholder`, `default`, `required`, `min`, `max`,
`generate` (password filled with a random value when left empty, shown as "generated automatically").

Variables Box always provides: `APP_ID`, `APP_DATA_DIR` (`/var/lib/nexovirt-box/apps/<id>/data`), `APP_PORT`,
`APP_DOMAIN`/`APP_HOST` (server IP), `APP_PROTOCOL`, `TZ`, `PUID`, `PGID`, `SHARED_DIR` and `ROOT_FOLDER_HOST`
(`/var/lib/nexovirt-box`, media lives in `media/`), `INTERNAL_IP`, `NETWORK_INTERFACE`.

## Install layout (root-owned, 0700)

```
/var/lib/nexovirt-box/apps/<id>/
  compose.yaml   verbatim from the catalog, 0600, never rewritten
  .env           answers, generated secrets and Box variables, 0600
  app.json       name, version, port, catalog source, install time
  data/          APP_DATA_DIR
```

Docker runs `docker compose --project-name nxbox-<id> -f compose.yaml --env-file .env ...`. Before `up`, the helper
renders `docker compose config` and refuses ports that are already in use. Update = `pull` + `up -d` (the compose
file stays). Uninstall = `down`, then either delete the folder or keep `data/` for a later reinstall.

## Catalog

- `catalog/index.json`: `generated_at`, and per app the summary fields plus `file` and `sha256`.
- `catalog/index.sig`: Ed25519 signature (base64) over the exact bytes of `index.json`.
- The public key is compiled into Box (`internal/catalog/key.go`). The built-in catalog is embedded in the binary.
  A synced catalog (`-catalog-url`) is verified by the web process for display and again by the helper before
  every install; an older `generated_at` than the built-in one is refused (no rollback).
- **Dev key**: `keys/catalog-dev.key` (in the repo, for development only). Replace it with an offline key before
  a public release and update the public key.

## Importer (`cmd/catalog-import`)

Offline tool that builds `catalog/` from local checkouts of:

| Store | License | Notes |
|---|---|---|
| runtipi/runtipi-appstore | GPL-3.0 | typed `form_fields` become questions; Traefik labels and `tipi_main_network` removed |
| IceWhaleTech/CasaOS-AppStore | Apache-2.0 | `x-casaos` metadata removed from the compose; `/DATA/AppData/$AppID` becomes `${APP_DATA_DIR}` |
| bigbeartechworld/big-bear-casaos | MIT | same format as CasaOS |

Umbrel apps are not imported (no license).

Rules: dedupe by name (Runtipi first, then CasaOS, then BigBear); skip apps with `privileged`, host network or
host PID; literal passwords in the compose (`POSTGRES_PASSWORD: tipi`) become generated secrets, shared across
every variable that used the same literal so linked services still match; the web port becomes `${APP_PORT}`.
Each app keeps its store, URL and license in `attribution`; the full license texts are in `catalog/LICENSES/`.
