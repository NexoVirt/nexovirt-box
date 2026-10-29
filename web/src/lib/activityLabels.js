// Readable text, icon and tone per audit action (Activity page and the bell in the top bar).
import LogIn from '@lucide/svelte/icons/log-in';
import LogOut from '@lucide/svelte/icons/log-out';
import ShieldAlert from '@lucide/svelte/icons/shield-alert';
import ShieldCheck from '@lucide/svelte/icons/shield-check';
import SquareTerminal from '@lucide/svelte/icons/square-terminal';
import Package from '@lucide/svelte/icons/package';
import Container from '@lucide/svelte/icons/container';
import FolderOpen from '@lucide/svelte/icons/folder-open';
import Activity from '@lucide/svelte/icons/activity';

const LABELS = {
  'login.ok': ['Signed in', LogIn],
  'login.failed': ['Sign-in failed', ShieldAlert, 'error'],
  'logout': ['Signed out', LogOut],
  'reauth.ok': ['Confirmed password', ShieldCheck],
  'reauth.failed': ['Wrong password at confirmation', ShieldAlert, 'error'],
  'account.password': ['Changed the password', ShieldCheck],
  'account.password_failed': ['Wrong current password', ShieldAlert, 'error'],
  'account.sessions_end': ['Signed out other browsers', LogOut],
  'console.open': ['Opened the console', SquareTerminal],
  'console.close': ['Closed the console', SquareTerminal],
  'docker.install': ['Installed Docker', Container],
  'catalog.sync': ['Synced the app catalog', Package],
  'settings.change': ['Changed a setting', Activity],
  '2fa.totp_on': ['Turned on the authenticator app', ShieldCheck],
  '2fa.totp_off': ['Turned off the authenticator app', ShieldAlert, 'error'],
  '2fa.passkey_add': ['Added a passkey', ShieldCheck],
  '2fa.passkey_remove': ['Removed a passkey', ShieldAlert],
  '2fa.recovery_new': ['Made new recovery codes', ShieldCheck],
  '2fa.password_failed': ['Wrong password in security settings', ShieldAlert, 'error'],
  'firewall.apply': ['Changed firewall rules', ShieldCheck],
  'firewall.apply_failed': ['A firewall change failed', ShieldAlert, 'error'],
  'firewall.confirm': ['Kept the firewall rules', ShieldCheck],
  'files.upload': ['Uploaded a file', FolderOpen],
  'files.download': ['Downloaded', FolderOpen],
  'files.delete': ['Deleted a file or folder', FolderOpen, 'error'],
  'files.move': ['Moved or renamed', FolderOpen],
  'files.copy': ['Copied', FolderOpen],
  'files.mkdir': ['Created a folder', FolderOpen],
  'files.roots': ['Changed the folders in Files', FolderOpen],
  'proxy.setup': ['Set up the reverse proxy', Container],
  'proxy.remove': ['Turned off the reverse proxy', Container],
  'images.prune': ['Cleaned up unused images', Container],
  'box.update': ['Updated NexoVirt Box', Package],
  'backup.run': ['Backed up', Package],
  'backup.failed': ['A backup failed', ShieldAlert, 'error'],
  'backup.restore': ['Restored a backup', Package],
  'backup.restore.failed': ['A restore failed', ShieldAlert, 'error'],
  'backup.verify': ['Checked a backup', ShieldCheck],
  'backup.verify.failed': ['A backup check failed', ShieldAlert, 'error'],
  'backup.delete': ['Deleted a backup', Package, 'error'],
  'backup.prune': ['Removed old backups', Package],
  'backup.key_create': ['Created the backup key', ShieldCheck],
  'backup.key_import': ['Imported a backup key', ShieldAlert],
  'backup.target_save': ['Saved a backup target', Package],
  'backup.target_delete': ['Removed a backup target', Package],
  'backup.schedule_save': ['Saved a backup schedule', Package],
  'backup.schedule_delete': ['Deleted a backup schedule', Package],
  'app.custom_add': ['Added an own app', Package],
  'app.custom_edit': ['Changed an own app', Package],
  'app.custom_view': ['Opened an own app definition', Package],
  'notify.channel_save': ['Saved a notification channel', Activity],
  'notify.channel_delete': ['Deleted a notification channel', Activity],
  'notify.test': ['Sent a test notification', Activity],
  'notify.rules': ['Changed notification events', Activity],
  'desktop.wallpaper': ['Changed the wallpaper', Activity],
};

// Unknown actions show as they are.
export function activityLabel(a) {
  if (LABELS[a]) return LABELS[a];
  if (a.startsWith('app.')) return [a.slice(4).replace(/[._]/g, ' ').replace(/^\w/, (c) => c.toUpperCase()), Package];
  return [a, Activity];
}
