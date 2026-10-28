// Minimal toast queue (status, completion, warning, error feedback).
export const toasts = $state([]);
let seq = 0;

// actions: [{ label, run }] buttons in the toast.
export function toast(message, kind = 'info', ms = 4500, actions = []) {
  const id = ++seq;
  toasts.push({ id, message, kind, actions });
  setTimeout(() => dismiss(id), ms);
}

export function dismiss(id) {
  const i = toasts.findIndex((t) => t.id === id);
  if (i !== -1) toasts.splice(i, 1);
}
