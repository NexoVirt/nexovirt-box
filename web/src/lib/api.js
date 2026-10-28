// Small fetch wrapper: always returns { ok, data, error, status } with a friendly error text.
export async function api(path, { method = 'GET', body } = {}) {
  let res;
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body ? { 'Content-Type': 'application/json' } : {},
      body: body ? JSON.stringify(body) : undefined,
    });
  } catch {
    return { ok: false, status: 0, error: 'NexoVirt Box is not reachable. Check that the service is running.' };
  }
  let data = null;
  try { data = await res.json(); } catch { /* empty body */ }
  if (!res.ok) {
    return { ok: false, status: res.status, error: (data && data.error) || 'Something went wrong. Please try again.' };
  }
  return { ok: true, status: res.status, data };
}
