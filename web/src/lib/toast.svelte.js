// Minimal toast queue (status, completion, warning, error feedback).
export const toasts = $state([]);
let seq = 0;

export function toast(message, kind = 'info', ms = 4500) {
  const id = ++seq;
  toasts.push({ id, message, kind });
  setTimeout(() => dismiss(id), ms);
}

export function dismiss(id) {
  const i = toasts.findIndex((t) => t.id === id);
  if (i !== -1) toasts.splice(i, 1);
}
