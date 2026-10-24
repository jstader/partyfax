/* The guest's side of the booth API, and the event code it rides on. The
   code goes in a header on every request, never in a URL, and is remembered
   on this device so a guest types it once. */

const KEY = 'booth.code';

// The printer takes one job at a time; at a party someone else's is often
// on it. A print waits this long between tries, and gives up after this.
const BUSY_RETRY_MS = 3000;
const BUSY_GIVE_UP_MS = 90_000;

/* Runs send(), a print, again while the printer is busy with someone
   else's, calling onWait() before each wait. Resolves or throws as send()
   does in the end. */
export async function whileBusy(send, onWait) {
  const started = Date.now();
  for (;;) {
    try {
      return await send();
    } catch (e) {
      if (e.status !== 409 || !/busy/i.test(e.message) || Date.now() - started >= BUSY_GIVE_UP_MS) throw e;
      onWait();
      await new Promise((r) => setTimeout(r, BUSY_RETRY_MS));
    }
  }
}

export function savedCode() {
  try { return localStorage.getItem(KEY) || ''; } catch { return ''; }
}

export function saveCode(code) {
  try { localStorage.setItem(KEY, code); } catch { /* private mode: ask again next time */ }
}

export function forgetCode() {
  try { localStorage.removeItem(KEY); } catch { /* nothing to forget */ }
}

/* The event's theme for a code, or null if no open event has it. */
export async function join(code) {
  const res = await fetch('/api/booth/join', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code }),
  });
  if (res.status === 404) return null;
  if (!res.ok) {
    const err = new Error(await reason(res));
    err.status = res.status;
    throw err;
  }
  return res.json();
}

/* Print a finished strip. Resolves once it is on paper; throws with the
   server's reason otherwise, and .status for the caller to act on. */
export async function printStrip(code, png) {
  const res = await fetch('/api/booth/print', {
    method: 'POST',
    headers: { 'Content-Type': 'image/png', 'X-Event-Code': code },
    body: png,
    signal: AbortSignal.timeout(45000),
  });
  if (res.ok) return res.json();
  const err = new Error(await reason(res));
  err.status = res.status;
  throw err;
}

/* Print a strip from the wall again. Same answers as printStrip. */
export async function reprint(code, id) {
  const res = await fetch('/api/booth/reprint', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Event-Code': code },
    body: JSON.stringify({ id }),
    signal: AbortSignal.timeout(45000),
  });
  if (res.ok) return res.json();
  const err = new Error(await reason(res));
  err.status = res.status;
  throw err;
}

export async function strips(code) {
  const res = await fetch('/api/booth/strips', { headers: { 'X-Event-Code': code }, cache: 'no-store' });
  if (!res.ok) {
    const err = new Error(await reason(res));
    err.status = res.status;
    throw err;
  }
  return (await res.json()).strips;
}

/* One strip's picture, as a blob: an <img> can't send the code header. */
export async function stripBlob(code, id) {
  const res = await fetch(`/api/booth/strips/${encodeURIComponent(id)}`, { headers: { 'X-Event-Code': code } });
  if (!res.ok) throw new Error(await reason(res));
  return res.blob();
}

async function reason(res) {
  try { return (await res.json()).error || `error ${res.status}`; } catch { return `error ${res.status}`; }
}
