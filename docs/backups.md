# Backups, own apps and notifications

## Backups

- **What**: per app the app folder (`compose.yaml`, `.env`, `app.json`, `data/`), a data folder chosen at install
  that lies outside the app folder, and the app's named Docker volumes (label `com.docker.compose.project=nxbox-<id>`,
  local volumes in `/var/lib/docker/volumes` only; external volumes are not included). "Box settings" (`box`) is a
  consistent copy of the web database (`VACUUM INTO`) plus `notify.key`; `/etc/nexovirt-box` (firewall, Caddy) is not
  restored.
- **Consistency**: the app is stopped (`docker compose stop`) while its files are read and started again
  (`docker compose start`) right after; apps stopped on purpose stay stopped. Downtime is the time to pack the files.
  We chose stop over pause or volume snapshots: pause leaves dirty database pages, and snapshots need LVM/ZFS/btrfs.
- **Format**: `<app>-<UTC yyyymmddThhmmssZ>.nxb`, a gzip tar encrypted with age (X25519). The archive is written to
  `/var/lib/nexovirt-box/backup-tmp` first (needs free space for one archive), then uploaded.
- **Key**: the age identity lives in `/etc/nexovirt-box/backup/key.txt` (root, 0600) so restores are one click. It is
  shown once when created ("Save your backup key"); import it on a new server to restore there. Replacing it needs a
  password proof; old backups then need the old key.
- **Targets** (`/etc/nexovirt-box/backup/targets.json`, root, 0600, secrets never returned to the browser):
  local folder (not system folders or Box's own state, default `/var/lib/nexovirt-box/backups`), S3-compatible
  (minio-go; HTTPS, plain HTTP only to a private address), Proxmox Backup Server through `proxmox-backup-client` (host
  backup `nxbox-<app>`, one `archive.img`; without the client the target explains how to install it).
- **Schedules** (web DB): daily or weekly at a time, for one app, Box settings or everything; retention keep last N,
  one per day for N days, one per week for N weeks (`pruneList`), applied after each scheduled backup.
- **Restore**: needs a fresh password proof checked by the helper. Unpacks into `apps/.restore-*` through `os.Root`
  (no path leaves it, setuid bits dropped), stops the app, moves the current folders and volume folders aside
  (`*.pre-restore-<stamp>`), moves the backup in, runs the install checks (mounts, privileges, ports for new apps),
  starts it. Any failure moves the previous files back. An installed app keeps its current `compose.yaml` and
  `app.json`; data and `.env` come from the backup. "As a new app" restores under another id (Box variables in `.env`
  are repointed, domains are not taken over). Box settings: Box stops, the database is replaced, Box starts, everyone
  signs in again.
- **Overview problems** (`backup_status.go` `backupProblems`): only the latest backup per app and target counts; a
  later successful backup of the same app to the same target clears it (a success to another target does not hide it); failures of deleted apps or targets are not shown. Each one can
  be dismissed (`POST /api/backups/history/{id}/dismiss`, stored in `backup_history.dismissed`). "Clear history" in
  Activity (`POST /api/backups/history/clear`, `older_than_days` 0 = all) needs a fresh password proof and is audited.
- **Verify** downloads, decrypts and unpacks an archive and checks its contents list.

## Own apps

A compose file (pasted or uploaded) plus an optional `.env`, stored verbatim in the app folder and managed like a
catalog app (`app.json` `custom: true`). Adding or changing one needs a fresh password proof checked by the helper.
Before Docker reads it, the raw YAML is refused when it has `include`, `extends.file`, `build`, `label_file`,
secrets or configs with `file`, or an `env_file` other than `.env` (Docker would read those host files as root while
rendering). The rendered config then gets the mount checks of every app (Box's own files, system folders and secrets
stay refused, no confirmation possible). Before saving, the dialog calls `POST /api/apps/custom/check` (helper
`custom_app_check`: renders the file in a scratch folder `apps/.check-*` without `app.json`, runs nothing) and lists
risk findings: privileged mode, devices, GPUs, host network/PID/IPC/UTS/userns/cgroup, dangerous capabilities
(SYS_ADMIN, ALL, ...), unconfined security profiles, and binds outside the app folder, the shared folder and the
file roots. Each one is allowed per item (key like `web:privileged`, `bind:/opt/x`); the save request carries the
confirmed keys (audited as `app.custom_allow`), the helper refuses every unconfirmed finding and stores the confirmed
ones in `app.json` (`allowed`, `reviewed`) so later starts, updates and restores pass the same check. Own apps saved
before the review (no `reviewed`) keep host network and outside binds, never privileges. `.env` may
not set `DOCKER_*` or `COMPOSE_*`. Editing shows a line diff, checks the new file (only new ports must be free),
redeploys, and goes back to the old file when it does not start.

## Notifications

Channels: email (SMTP, STARTTLS/TLS), Discord webhook, ntfy (server, topic, token) and a generic JSON webhook. Secrets
are sealed with AES-256-GCM under `<state>/notify.key` and never returned; a stored secret is dropped when the
destination (URL, ntfy server and topic, mail server and user) changes. Saving and testing a channel need a fresh
proof. Events: app down (two polls in a row, 30 s apart) and recovered, disk above a threshold, updates available,
backup failed or succeeded, sign-in from a new IP, 5 failed sign-ins in 10 minutes, firewall change rolled back.
The same event key is sent once per 30 minutes, at most 20 messages per 10 minutes, the last 500 are kept.
