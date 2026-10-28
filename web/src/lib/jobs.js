import { api } from './api.js';

// Polls a background job until it is done or failed. onUpdate gets every snapshot.
export async function waitJob(id, onUpdate = () => {}) {
  for (;;) {
    const res = await api(`/api/jobs/${id}`);
    if (!res.ok) return { status: 'failed', error: res.error, lines: [], http: res.status };
    onUpdate(res.data);
    if (res.data.status !== 'running') return res.data;
    await new Promise((r) => setTimeout(r, 900));
  }
}

// Starts an action that answers with {job} and waits for it.
export async function runJob(path, body, onUpdate) {
  const res = await api(path, { method: 'POST', body: body || {} });
  if (!res.ok) return { status: 'failed', error: res.error, lines: [], http: res.status };
  return waitJob(res.data.job, onUpdate);
}

// What a running action is called in a status line.
export const BUSY_LABEL = {
  install: 'Installing', retry: 'Installing', start: 'Starting', stop: 'Stopping', restart: 'Restarting',
  update: 'Updating', uninstall: 'Removing',
};

// Picks up actions that run in the background after a reload (or in another tab). sync()
// returns { appId: kind } and calls onDone(job, app) once when each of them ends. Jobs of apps
// in `own` (started on this page, which waits for them itself) are skipped.
export function jobFollower(onDone, own = () => ({})) {
  const seen = new Set();
  return async function sync() {
    const res = await api('/api/jobs');
    if (!res.ok) return {};
    const out = {};
    for (const j of res.data) {
      out[j.app] = j.kind;
      if (seen.has(j.id) || own()[j.app]) continue;
      seen.add(j.id);
      waitJob(j.id).then((done) => onDone(done, j.app));
    }
    return out;
  };
}

// A strong random password that fits the field's limits.
export function generatePassword(q = {}) {
  const chars = 'abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789';
  const n = Math.min(q.max || Infinity, Math.max(q.min || 0, 32));
  const out = [];
  const buf = new Uint8Array(1);
  while (out.length < n) {
    crypto.getRandomValues(buf);
    if (buf[0] < 256 - (256 % chars.length)) out.push(chars[buf[0] % chars.length]); // no modulo bias
  }
  return out.join('');
}

// Readable markdown-ish text: drops heading marks, emphasis and images, keeps paragraphs.
export function plainText(s = '') {
  return s
    .replace(/!\[[^\]]*]\([^)]*\)/g, '')
    .replace(/\[([^\]]+)]\([^)]*\)/g, '$1')
    .replace(/^#{1,6}\s*/gm, '')
    .replace(/[*_`]{1,3}([^*_`]+)[*_`]{1,3}/g, '$1')
    .replace(/<[^>]+>/g, '')
    .replace(/[ \t]+\n/g, '\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

// "version-12.2" or "v1.2" read as "12.2" / "1.2".
export function versionLabel(v = '') {
  return v.replace(/^(version-|v(?=\d))/i, '');
}

export function appURL(app) {
  if (!app?.main_port) return '';
  return `${app.scheme === 'https' ? 'https' : 'http'}://${location.hostname}:${app.main_port}`;
}
