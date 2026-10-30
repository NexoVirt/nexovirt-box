# Security model

- The web UI runs as the unprivileged user `nexovirt-box`. Root work (console, Docker, firewall,
  files, reverse proxy, updates) goes through `nexovirt-box helper`, a root service on a unix
  socket that only the web user may use and that only knows a fixed list of typed actions.
- The console is a root shell. The helper itself checks the password (and the 2FA code) and gives
  out a 5-minute proof bound to the session; without it the helper refuses to start a shell and
  the other actions that reach root (own apps, restores, Docker access for an app, VPN, SSH
  hardening, the folders Files may show, formatting a drive). So a stolen session cookie, a
  script injected into the page or a paired box cannot get root. The helper reads the password
  hash from the web database, so a compromised web process (which can change that database and
  sees the password when it is typed) counts as the admin. Signing out closes open consoles.
- Two-step sign-in: TOTP with 10 single-use recovery codes, and passkeys. Browsers only allow
  passkeys on a domain name with a trusted certificate (or localhost), not on an IP address.
- Docker is only used through the helper, and it only runs app definitions from the catalog
  signed by NexoVirt. Generated passwords and your answers are in a private `.env` per app; the
  compose file is never rewritten.
- No nested mounts: an app never mounts a folder inside another folder it mounts (an app may mount
  the media folder or a subfolder of it, never both), and outside the shared folder never one
  inside another app's folders. Otherwise a container that can write the outer folder could put a
  symlink where the inner one is, and Docker would follow it on its next start. Apps installed
  with an older nested catalog layout are moved to the side-by-side layout before they start
  (data moved without following links, previous files kept as `*.layout-prev`); apps that cannot
  be moved, and own apps with nested mounts, stay stopped and show why.
- Firewall: Box only changes its own rules (its own nftables table, ufw rules tagged
  `nexovirt-box:`, or the firewalld rich rules it added) and shows the others read only. Each
  change must be confirmed within 60 seconds with a new request, else the helper restores the
  previous rules. Blocking the Box port needs a typed confirmation.
- Files: every path is resolved and must stay inside the allowed folders. System folders (/etc,
  /root, /proc, /sys, /dev, /boot, /run, /usr, /bin, /sbin, /lib*, cron, package and Docker
  state) are refused (only an edit of `/etc/nexovirt-box/files.json` on the server can allow
  them), and of Box's own folder only the app data and the shared folder are shown, never an
  app's compose file, `.env` or settings. Changing the shown folders needs the password.
  Previews are sandboxed; SVG and HTML are always downloads.
- Reverse proxy: Box never takes over a port another program uses; with 80/443 taken you pick
  other ports.
- Backups are encrypted with age and signed with an HMAC keyed from the backup key, so someone who
  can write to a backup target cannot plant an archive that restores as a privileged app or a
  different admin account.
- Self-updates: only releases signed with the NexoVirt release key, applied on click; when the
  new version does not answer, the old binary is restored.
- Failed sign-ins, setup and password confirmations are limited to 5 per minute per IP (counted
  atomically, also across restarts). `X-Forwarded-For` is only trusted from `-trusted-proxies`.
- Passwords are bcrypt hashes; sessions SHA-256 hashes of random tokens; the database is readable
  by the web user only.

## Known limits

- Apps that share Box's media folder can change each other's files there; that is what the
  shared folder is for. Outside it, apps never mount folders inside each other.
- A paired box with full access can do everything the app and system API allows, except the
  actions listed above that need the password.

Report security problems privately, see [SECURITY.md](../.github/SECURITY.md).
