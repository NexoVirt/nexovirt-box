// Actions that change what runs as root (own apps, restores, replacing the backup key) need the
// password from the last few minutes, checked by the helper. withProof runs the action, and on
// 428 asks for the password once (PasswordGate in App.svelte) and runs it again.
export const gate = $state({ open: false, resolve: null });

export function askPassword() {
  return new Promise((resolve) => {
    gate.resolve = resolve;
    gate.open = true;
  });
}

export async function withProof(run) {
  let res = await run();
  if (res?.http === 428 || res?.status === 428) {
    if (!(await askPassword())) return { status: 'failed', ok: false, cancelled: true, error: '' };
    res = await run();
  }
  return res;
}
