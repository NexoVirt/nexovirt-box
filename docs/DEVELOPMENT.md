# Development guide: NexoVirt Box

Open-source (AGPL-3.0) web UI that installs on top of a normal Linux server: live dashboard, Docker app store,
root console in the browser and an activity log. One Go binary with the Svelte UI and the signed app catalog
embedded.

## Layout

- `cmd/nexovirt-box/main.go`: the thin entry point (`box.Run(version)`, `-X main.version` stamps the version).
- `internal/box/`: the whole program (web UI, root helper, apps, backups, files, ...). File names below without a
  directory live here.
- `internal/catalog/`: catalog format, signature check, compose shapes. `catalog/`: the signed catalog data plus
  `embed.go` (package `catalog`, imported as `catalogdata`). `web/`: the Svelte UI plus `embed.go` (`web.Dist`).
- `cmd/catalog-import`, `cmd/catalog-check`, `cmd/catalog-resign`, `cmd/release-sign`: tools.
- `deploy/` (install.sh, get.sh), `docs/`, `keys/` (dev catalog key only), `.github/` (workflows, lint config
  `golangci.yml`, CONTRIBUTING, SECURITY, CODE_OF_CONDUCT, CLA).

## Design rules (hard)

- Own **Apple-inspired design system**. **No shadcn, no Tabler, no Tailwind, no UI component framework.**
  Plain CSS: tokens + base classes in `web/src/app.css` (`.tile`, `.group`/`.row` inset grouped lists, `.btn`,
  `.icon-btn`, `.pill`, `.dot`, `.bar`, `.notice`, `.skeleton`, type scale `.t-large/.t-title/.t-head/.t-caption/
  .t-section`), component styles scoped in the `.svelte` files. Own components in `web/src/lib/` (Ring, Sparkline,
  Segmented, Sheet, Popover, Toaster).
- Feel: system font stack (-apple-system / SF Pro, self-hosted Inter fallback), size-specific tracking, translucent
  materials (backdrop blur sidebar, toolbar, sheet, popover), large rounded surfaces with soft depth, press feedback
  on pointer-down (`scale(0.97)`), springs as CSS `linear()` curves (`--spring` damping 1.0 by default,
  `--spring-bounce` only after a flick), enter/exit along the same path, popovers grow from their trigger,
  44px hit targets, visible keyboard focus, reduced motion / transparency / contrast respected.
- Icons: **Lucide** (`@lucide/svelte/icons/<name>`, one import per icon). **Never put an icon or image on a plate,
  frame, tinted background, border, radius box or shadow** (no `background`/`border`/`box-shadow` on icon wrappers,
  also not "just a subtle tint"). Size icons with the global `.ico` wrapper and the scale tokens in `app.css`:
  `.ico` = `--icon-row` 20 px (rows, tables), `.ico.md` 28 px (section heads, banners, stat tiles), `.ico.lg` 32 px
  (status of a page or dialog), `.ico.xl` 44 px (empty states, `.empty-state`). Colour with `.ico.accent|ok|warn|error`.
  Pass no `size` to a Lucide icon inside `.ico` (the wrapper sizes it). Inline icons in buttons and pills stay 13 to 16.
- Tables: every table is `lib/DataTable.svelte` (card, sticky header, sortable columns, search + toolbar snippet, row
  actions menu, selection, expandable rows, client or server paging, loading/error/empty states, stacked rows on
  phones; `fill` inside `FILL` pages). Never write ad-hoc `<table>` markup in a page.
- Logo: `docs/branding/` (mark `box-mark.svg` in currentColor, app icon `box-icon.svg` = favicon, PNGs, social card).
  In the UI only the mark is used (`lib/Logo.svelte`: sign-in, top bar, Settings > About), never the tile on a plate.
- Colours: zinc neutrals with ONE blue accent `--accent` (#2563eb light, #60a5fa dark, same as the NexoVirt panel).
  Status colours `--ok`/`--warn`/`--error` only for gauges, bars and dots (gauges use the accent until 75 %).
- No gradients. No coloured card status strips or borders. No em dashes anywhere (code, UI, docs, commits).
- Shell (desktop, since 2026-10-29): NO sidebar. `App.svelte` = slim translucent top bar (`.glass.blur`, the only
  blurred desktop surface; left: account popover with appearance + sign out, Settings, Terminal; right: activity bell
  (solid popover: disk warnings + last 6 audit entries + "All activity"), theme toggle) over `Desktop.svelte`
  (wallpaper layer, left widgets `desktop/Widgets.svelte` + `desktop/ClockWidget.svelte`, right search + "System"
  dock (Apps, Files, Monitor, Terminal, Backups, Settings; Apps opens the Apps window with Installed and App Store tabs plus "Add your own app" and "Check for updates") + "Apps" grid of installed apps, each shown once). Tiles: click opens
  (apps in a new tab, Box tools as windows), right-click/long-press menu (open, start/stop/restart, logs, backups,
  settings, move, uninstall; system tiles: open, move, hide), drag to reorder inside its group (touch: long press, then
  move). Layout per user on the server: `GET/POST /api/desktop` (`desktop.go`, `users.desktop` JSON: wallpaper, dim,
  blur, widgets order/on, tile order, hidden system tiles, clock options, theme; POST merges, unknown fields refused),
  wallpaper upload `POST/GET /api/desktop/wallpaper` (JPEG/PNG/WebP only, 8 MB). Built-in wallpapers are flat colours
  and two flat-shape SVGs made for Box (`public/wallpapers/`), never gradients or third-party photos.
- Windows (`lib/AppWindow.svelte`, `lib/windows.svelte.js`, geometry in `lib/winmath.js`): every Box window floats:
  drag the title bar, resize from any edge or corner (min 420 x 300, never under the top bar; the window body is its own
  stacking context so page toolbars never cover the handles; a smaller browser window shrinks it), maximise (button or double-click the title), minimise to the dock row at the bottom,
  click to bring to front; several at once, new ones centred with a cascade offset; place, size and maximise in
  localStorage `nxbox-win-<key>`; under 700 px full screen. Esc closes only the window in front (unless a dialog, menu,
  input or the terminal has the key). Dialogs (`Dialog`, `Sheet`, z 50+) are modal above all windows (z 30 + rank), the
  dock row is z 49. Routes: `/apps/installed` and `/apps` (one Apps window, tabs), `?app=<id>&do=backups|uninstall`,
  `/apps/<id>/logs`, `/files`, `/monitor`, `/console`, `/backups`. Inside a window the page's own `.page-head h1` is
  hidden (the title bar names it); pages put no background, border or max-width of their own on the window surface.
  Apps, Files, Terminal and Settings are `flush` (edge to edge); the terminal refits with a ResizeObserver.
- Settings window (`SettingsView.svelte`, macOS System Settings style, list left, section right; phones drill down):
  `/settings/<section>` for general, security, apps, network, firewall, shares, health, disks, system-updates, box-updates,
  backups, notifications, activity, about. `Settings.svelte` renders one section (`section` prop). Old paths redirect in
  `App.svelte` `canonical()` (/firewall, /shares, /disks, /updates, /activity, /settings#tab).
- Performance rules: pages are separate chunks (`lib/Lazy.svelte` + dynamic import in App/SettingsView; initial JS
  ~170 KB, xterm only with the Terminal); no backdrop-filter on many elements at once (widgets/tiles are near-solid
  `.glass`, blur only on the top bar and window title bars); menus, popovers, toasts, sheets are SOLID (`--material`);
  the stats stream and app polling pause behind full windows and in background tabs; unchanged polls do not touch the
  grid; search is debounced.
- Theme (Settings > General > Theme, `ThemeSettings.svelte`, `lib/themes.js`, server `theme.go`): presets (NexoVirt,
  Graphite, Midnight, Forest, Sand, Rose, High contrast) with light + dark, accent (auto contrast: on-accent text black
  or white, accent nudged until 4.5:1), neutral tone, solid/subtle glass, corners, density, text size; live preview,
  Save/Revert/Reset, own named themes (max 20), JSON export/import (validated). Applied as inline CSS tokens on <html>;
  cached in localStorage `nxbox-theme-vars` so `public/theme.js` paints it before the app loads.
- Light + dark mode: follows the system until the user picks Light/Dark/Auto (account menu); the pick is
  remembered (`nxbox-theme` in localStorage; `public/theme.js` sets the `dark` class before first paint,
  `src/lib/theme.svelte.js` holds the state). Every page keeps the shared `.page-head`; never add page-local overrides.
  Pages listed as fill (Files, Activity, Backups, Logs) fill their window and scroll inside their own card.
- Backups (`Backups.svelte`) has top tabs (Segmented, `#overview|archives|schedules|targets|activity` in the URL).
- Horizontal strips (`lib/HScroll.svelte` with previous/next buttons while it overflows, or the bare `use:hscroll`
  action from `lib/hscroll.js`): a normal vertical wheel scrolls them sideways while hovered (the page keeps the wheel at
  the strip's ends), arrow/Home/End move between items, touch and trackpads stay native. Used by the App Store
  categories, screenshots, the Files path bar and the minimised-window row; never a bare `overflow-x: auto` strip.
- Selects use the global `select.input` style (own chevron with room); never the cramped native arrow.
- Every async thing has a loading state (skeletons, spinners). Errors are friendly text (toasts via
  `src/lib/toast.svelte.js` or a `.notice`), never raw HTTP errors or stack traces.
- CSP: no inline scripts (`script-src 'self'`). Inline styles are allowed (xterm.js, transforms).
- Open source: never copy premium/commercial assets (e.g. Tabler premium) into this repo.

## Security model

- Two processes from the same binary: the web UI runs as the unprivileged user `nexovirt-box`
  (`/var/lib/nexovirt-box/web`, systemd hardening), and `nexovirt-box helper` runs as root on
  `/run/nexovirt-box/helper.sock` (0660 root:nexovirt-box, peer uid checked with SO_PEERCRED / LOCAL_PEERCRED).
  The helper only knows the allowlist in `helper.go` `actions()` plus `console`; there is no generic exec. Add new
  root work as a new typed action there, never as a command string from the web process.
- Docker is reachable ONLY through the helper (the web user is never in the docker group: the socket is root).
  The helper only runs compose files from the signed catalog (verified again in the helper, `loadCatalog`), stored
  verbatim; answers and secrets go to a 0600 `.env` written as `KEY='value'` (answers may not contain `'` or
  line breaks).
- Console: the helper starts a root login shell and hands the pty master to the web process (SCM_RIGHTS). Opening
  it needs a password proof from the last 5 minutes (`sessions.reauth_at`, `POST /api/auth/reauth`); sign-out
  closes open consoles (`consoleRegistry`); open/close are audited.
- Routes live in `routeTable()` and are private unless marked public; `TestEveryPrivateRouteNeedsAuth` walks the
  table unauthenticated. `X-Forwarded-For` is only read from `-trusted-proxies`; X-Real-IP never.
- Rate limits are stored in SQLite (`attempts`), 5 per minute per IP for setup, failed sign-ins and re-auth.
  The database is 0600, the process umask 077.
- Console proof is enforced IN THE HELPER (`proof.go`): `console_proof` checks the bcrypt hash (and TOTP code)
  straight from the web DB (opened read only) and returns an HMAC proof (helper-only random key, 5 min, bound to
  the session hash); `console` refuses without a valid proof for a session that still exists.
- Rate limits reserve a try BEFORE the check (`limiter.reserve`, one mutex) and give it back on success, so
  parallel requests cannot bypass them and successful sign-ins do not count. Same pattern for the helper's own
  proof limit and the 2FA pending sign-in (`takeTry`).
- 2FA (`twofactor.go`, `totp.go`): TOTP + 10 single-use recovery codes (sha256), passkeys via go-webauthn (RP ID =
  request host; refused on IP hosts). Password sign-in with 2FA on only creates a pending sign-in (memory, 5 min,
  5 tries).
- Account: `POST /api/account/password` and `POST /api/sessions/end-others` (current password) sign out every other
  session, close their consoles, drop their proofs and void pending two-step sign-ins (`twoFactor.forgetUser`); a
  pending sign-in also fails when the password hash changed since it started (`pendingLogin.pwHash`).
- Files: copy keeps symlinks as links, zip downloads skip symlinks, every new endpoint (`files_copy`, `files_size`,
  `files_zip`, `mkdir` with `op: exist_ok`) resolves paths inside the roots (`TestFilesCopyZipSizeStayInside`).
- Firewall (`firewall.go`): only Box's own rules; `firewall.pending.json` + 60 s timer roll back unless confirmed;
  rules restored at helper start (nftables does not persist). Files (`files.go`): resolve + roots + blocked dirs,
  fd passing for read/upload. Proxy (`proxy.go`): Caddy container `nxbox-proxy` in /etc/nexovirt-box/caddy. Domains per app (`POST /api/apps/{id}/domain`
  {domain, port, protect}): the port must be one the app publishes (0 = main port), DNS is checked against the
  server's addresses, Let's Encrypt when it points here and Caddy holds 80/443, else `tls internal`; Settings > Network
  lists each route's certificate (issuer, expiry, read from Caddy's data folder). "Box sign-in in front"
  (`proxyauth.go`): Caddy `forward_auth` to Box (`https://127.0.0.1:<box port>`, self-signed so verify is skipped on
  localhost) at `/api/proxy-auth/check`; without the domain cookie it redirects to Box `/api/proxy-auth/start` (Box
  address remembered from the admin's browser when protection was turned on), which for a signed-in user sends a
  one-time token (1 min, table `proxy_tokens`) to `https://<domain>/.nxbox-auth`, rewritten to
  `/api/proxy-auth/finish`, which sets `nxbox_proxy` for that domain only (12 h, HttpOnly, Secure). Tokens are bound
  to the domain and to the Box session they came from (sign-out ends them) and are never a Box session, so an app
  that reads its own cookies gains nothing on Box. Redirect targets stay same-site (`safeLocalPath`: absolute path, no
  `//`, no backslash or control character anywhere, `url.Parse` gives no scheme or host); the token only goes to
  `https://<protected domain>` on the proxy's HTTPS port (`signinTarget`: no user info, no other port, rebuilt from
  the checked parts). While protected, the app's port behind the domain is published on 127.0.0.1 only
  (`proxybind.go`: Box-written `bind.yaml`, `ports: !override` for the services publishing it, every other port
  unchanged, added as an extra `-f` file and checked by `checkBindFile` + `checkConfig` before the recreate; removed
  again when the sign-in is turned off). An app that publishes the port as part of a range cannot be protected. The protected port is pinned in `app.json`
  (`domain_port`); `checkConfig`, which every compose path runs before Docker uses the files (start, update, own app
  edit, catalog update, restore, limits, roll back), rewrites `bind.yaml` from the current compose files (or removes a
  stale one), so the binding cannot drift open. When the port can no longer be bound (an edit dropped it) the change is
  refused and the old file stays (fail closed). A roll back keeps today's domain, sign-in and pinned port.
  Updates (`updates.go`): registry digest HEAD for apps; signed `latest.json` for Box (release key in
  `internal/catalog/key.go`, private half `~/.config/nexovirt/box-release.key`), rollback on failed health check.

- Backups (`backup*.go`), own apps (`customapp*.go`) and notifications (`notify*.go`): see `docs/backups.md`.
  Restores, own app add/edit, backup target changes, backup deletes, key replacement and notification channel
  save/test need a fresh password proof that the helper checks itself (`consoleAllowed`).
## System updates, disks and shares

- Updates (`sysupdates.go`, page `Updates.svelte`): helper actions `apt_status`, `apt_refresh` (apt-get update, 150 s,
  errors verbatim + reason code; missing Translation files ignored), `apt_simulate` (dependencies reported as `added`),
  `apt_upgrade_start` (re-simulates, refuses removals, runs DETACHED as unit `nexovirt-box-apt` via systemd-run, log +
  exit code in /var/log/nexovirt-box/apt-upgrade.{log,exit}), `apt_upgrade_status` (log by byte offset),
  `system_reboot` (confirm "REBOOT", 3 s delay), ctx actions `auto_updates` (only Box's drop-in
  /etc/apt/apt.conf.d/52nexovirt-box-auto-updates; the server's own unattended-upgrades config is read only) and
  `pkg_install` (fixed map `installable`: smartmontools, samba, nfs-kernel-server, unattended-upgrades). Package
  names: `^[a-z0-9][a-z0-9+.-]{0,127}$`, max 500, passed as separate argv after `--`.
- Disks (`disks.go`): `disks_list` = lsblk -J -d + `smartctl --json=c -a -n standby` per disk, `parseSmart` verdict
  good/warning/failing/unknown; virtual disks (QEMU, virtio, ...) say "No SMART data (virtual disk)". `smart_selftest`
  only for a device lsblk lists right now. The web side re-checks every 6 h (`watchDisks`), Home shows a warning tile
  (`GET /api/disks/summary`, cached only), and worse verdicts raise a `diskEvent` to `diskEventSink`s
  (TODO: the notifications feature registers one with `app.subscribeDiskEvents`).
- Health (`health.go`, Settings > Health, `GET /api/health`): SMART verdicts and disk temperatures from the cached
  `watchDisks` result, sensor temperatures (gopsutil hwmon, read by the web user), UPS through NUT (helper `ups_status`:
  `upsc -l` then `upsc <name>`, names `^[A-Za-z0-9_.-]{1,64}$`; without upsc it says so, Box never installs NUT), and
  the last successful backup per app (`backup_history`) plus whether it has a schedule. `healthProblems` (pure) flags
  failing/warning disks, disks at 60 °C, sensors within 5 °C of their critical/high limit, UPS on battery/low
  battery/replace battery, and scheduled apps without a successful backup for 8 days. `watchSystemHealth` (every 10
  min) sends them as the `health` notification (backup problems once a day; SMART changes keep `disk_health`).
  smartmontools is installed only from the Disks banner (the admin's click, `pkg_install`).
- Shares (`shares.go`, state /etc/nexovirt-box/shares.json): Samba writes ONLY /etc/samba/nexovirt-box.conf plus one
  include line appended to smb.conf once; testparm failure restores the previous file. Share users are Linux system users
  Box created (nologin, no home; a name that already exists is refused) with smbpasswd. Shares use force user/group = the
  folder owner, wide links = no, create mask 0664 / directory mask 0775; guest only for read-only shares. A READ-WRITE
  share needs a folder owned by one of its users, else the save fails with `needs_handover:` and the UI offers
  `samba_handover` (op check = owner + item count, apply = lchown -R to the user and its group; WalkDir never follows
  symlinks). Allowlist: strictly inside an allowed root (never the root itself), then system dirs and Box's state
  refused; also refused when the folder is, lies under or contains any /etc/passwd home, or when ANY item inside belongs
  to an account other than root or a Box share user (login accounts, uid >= 1000 with a shell, named first). Max 200000 items. NFS writes ONLY
  /etc/exports.d/nexovirt-box.exports, explicit client CIDRs (no /0 and nothing wider than /8, or /16 for IPv6), always
  root_squash, `exportfs -ra` failure restores.
- Share links (`sharelinks.go`, table `share_links`, token stored as sha256, shown once): expiry 1 h to 90 days, optional
  bcrypt password, optional download limit, revoke. Public `GET|POST /s/{token}` (in the public route list) serves only
  the stored path via `files_open_read` / `files_zip`, so the roots are checked again on every download; 60 hits/min/IP
  (`shareLimit`), wrong passwords use the 5/min limiter. The page sets Referrer-Policy same-origin so its password form
  sends a real Origin (sameOrigin would refuse "null").

## App store

- Design: `docs/app-store.md`. Catalog in `catalog/` (index.json + index.sig + apps/*.json + LICENSES/), embedded
  and verified with the Ed25519 release key in `internal/catalog/key.go` (private half only at
  `~/.config/nexovirt/box-catalog.key`). `keys/catalog-dev.key` is the DEV key, accepted only with `-tags devkey`.
- Rebuild the catalog from local checkouts of runtipi/runtipi-appstore, IceWhaleTech/CasaOS-AppStore and
  bigbeartechworld/big-bear-casaos: `go run ./cmd/catalog-import -runtipi <dir> -casaos <dir> -bigbear <dir>`.
  Umbrel apps are never imported (no license). Then run `go run ./cmd/catalog-check` (drops apps whose images are
  missing or private, records real architectures, re-signs; report in `docs/catalog-image-report.md`).
- Installed apps: `/var/lib/nexovirt-box/apps/<id>/{compose.yaml,.env,app.json,data/}` (root, 0700), compose
  project `nxbox-<id>`. Long actions run as web-side jobs (`/api/jobs/{id}`, polled by the UI).

## Build

- `make build`: UI (`web`, `npm ci && npm run build`) then Go for linux/amd64 + linux/arm64 into `bin/`.
- `make test`: `go vet` + `go test` (includes the importer and the catalog signature checks).
- Web unit tests: `cd web && npm test` (`node --test`, plain modules only: `lib/winmath.js`, `lib/hscroll.js`).
- Dev: `go run ./cmd/nexovirt-box -dev -listen 127.0.0.1:8080` and `cd web && npm run dev` (Vite proxies `/api`).
- Install or upgrade on a server: copy `bin/nexovirt-box-linux-<arch>` + `deploy/install.sh`, run the script as
  root. It creates the `nexovirt-box` user and both units and moves a 0.1 root-only install into the new layout.
- Dev with the helper on a Mac: `go run ./cmd/nexovirt-box helper -socket /tmp/nxb.sock -user $USER -apps /tmp/nxb-apps` and
  `go run ./cmd/nexovirt-box -dev -listen 127.0.0.1:8080 -helper /tmp/nxb.sock -state /tmp/nxb-web`.

## GitHub, CI and releases

- Repository: `github.com/NexoVirt/nexovirt-box` (private until the owner makes it public). Only `main`
  is pushed; work on `feat/*` / `fix/*` branches and merge through pull requests once CI is green.
- CI (`.github/workflows/ci.yml`, every push to main and every PR): `go vet` + `go test ./...` (race
  detector on `internal/` and `cmd/` except `internal/box`, whose tests swap package-level paths in cleanups),
  golangci-lint (`.github/golangci.yml`, locally `golangci-lint run -c .github/golangci.yml`), catalog and key checks (built-in catalog verifies with the release
  catalog key, release builds do not trust `keys/catalog-dev.key`, `deploy/get.sh` carries the release
  key), `svelte-check` + `npm test` + Vite build, linux/amd64 + linux/arm64 binaries as artifacts.
- Release (`.github/workflows/release.yml`): push an annotated tag `vX.Y.Z` (a `-suffix` makes it a
  pre-release; GitHub's `latest` ignores pre-releases). The tag message becomes the release notes and
  the notes shown in Box. The workflow tests, builds the UI and both binaries with `main.version`
  stamped, runs `cmd/release-sign` with the secret `BOX_RELEASE_KEY` (base64 Ed25519 seed, the same
  content as `~/.config/nexovirt/box-release.key`; release-sign refuses any other key), checks the
  signature with openssl like get.sh, writes `SHA256SUMS` and publishes the GitHub release with
  `nexovirt-box-linux-{amd64,arm64}-<ver>`, `install.sh`, `get.sh`, `latest.json`, `latest.json.sig`,
  `SHA256SUMS`. `latest.json` lists both binaries and `install.sh` (url + sha256) under their tag URLs.
- Update address for Box (Settings > Box updates, built-in default `defaultReleaseURL` in `settings.go`; a saved
  value wins, an empty one means the default): `https://github.com/NexoVirt/nexovirt-box/releases/latest/download`
  (Box fetches `latest.json` + `latest.json.sig` there; GitHub redirects to the newest non-pre-release).
- Catalog URL for Box (Settings > Apps and catalog > Online catalog address, built-in default `defaultCatalogURL`;
  saved value > `-catalog-url` > default): `https://raw.githubusercontent.com/NexoVirt/nexovirt-box/main/catalog`
  (index.json, index.sig and apps/ straight from `main`, signed with the catalog key; works once the
  repository is public). Re-sign the catalog locally with `go run ./cmd/catalog-check` (key in
  `~/.config/nexovirt/box-catalog.key`) and push.
- One-line install: `curl -fsSL https://raw.githubusercontent.com/NexoVirt/nexovirt-box/main/deploy/get.sh | sudo sh`
  (Debian 12/13, Ubuntu 22.04/24.04, amd64/arm64; verifies the manifest signature with openssl and the
  sha256 of binary and install.sh, then runs install.sh; idempotent; `NEXOVIRT_BOX_VERSION` pins,
  `GITHUB_TOKEN` reads assets through the API while the repository is private).
- Contributions need the CLA (`.github/CLA.md`, bot in `.github/workflows/cla.yml`, signatures in
  `signatures/cla.json` on the `cla-signatures` branch). Dependabot watches gomod, npm and actions.
- Keep the repository clean for the public: no private keys (only public keys and the dev key),
  no internal hosts or IPs, no build output (`bin/`, `web/dist/*`, the `box` dev binary), and no
  assistant/AI attribution in commits, PRs or docs.
