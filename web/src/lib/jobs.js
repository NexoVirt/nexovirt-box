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
