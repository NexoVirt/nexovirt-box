// App installs that keep running when their sheet closes. The sheet, My Apps and the toasts all
// read this one state; after a reload resumeInstalls() picks running installs up again.
import { api } from './api.js';
import { waitJob, appURL } from './jobs.js';
import { toast } from './toast.svelte.js';
import { withProof } from './proof.svelte.js';
import { router } from './nav.js';

export const installs = $state({}); // app id -> { name, job, status, lines, result, error, http }
export const sheet = $state({ id: '' }); // app whose sheet is open (no toast for it when it ends)

const details = (id) => ({ label: 'View details', run: () => router.go(`/apps?app=${id}`) });

function track(id, name, jobId) {
  if (installs[id]?.job === jobId) return;
  installs[id] = { name, job: jobId, status: 'running', lines: [], result: null, error: '' };
  waitJob(jobId, (j) => {
    if (installs[id]?.job === jobId) installs[id].lines = j.lines || [];
  }).then((j) => {
    const it = installs[id];
    if (!it || it.job !== jobId) return;
    Object.assign(it, { status: j.status, result: j.result || null, error: j.error || '', http: j.http, lines: j.lines?.length ? j.lines : it.lines });
    if (sheet.id === id || j.http === 401) return;
    if (j.status === 'done') {
      const url = appURL(j.result);
      toast(`${name} is installed.`, 'success', 12000, [
        ...(url ? [{ label: 'Open', run: () => window.open(url, '_blank', 'noopener,noreferrer') }] : []), details(id)]);
    } else {
      toast(`${name} could not be installed.`, 'error', 12000, [details(id)]);
    }
  });
}

// Starts an install; the result says only whether it started.
export async function startInstall(id, name, body) {
  // Docker access needs the password (the server answers 428 without a fresh one).
  const res = await (body.allow_docker_socket ? withProof : (f) => f())(() => api(`/api/apps/${id}/install`, { method: 'POST', body }));
  if (res.ok) track(id, name, res.data.job);
  return res;
}

// Attaches to installs that run on the server (after a reload or from another tab).
export async function resumeInstalls() {
  const res = await api('/api/jobs');
  if (!res.ok) return;
  const mine = res.data.filter((j) => j.kind === 'install' || j.kind === 'retry');
  if (!mine.length) return;
  const apps = await api('/api/apps');
  const nameOf = (id) => installs[id]?.name || (apps.ok && apps.data.find((a) => a.id === id)?.name) || id;
  for (const j of mine) track(j.app, nameOf(j.app), j.id);
}

// The sheet of a running install was closed: say it goes on.
export function backgrounded(id, name) {
  const it = installs[id];
  if (!it || it.status !== 'running') return;
  if (name) it.name = name;
  toast(`Installing ${it.name} in the background.`, 'info', 6000, [{ label: 'Show progress', run: () => router.go(`/apps?app=${id}`) }]);
}
