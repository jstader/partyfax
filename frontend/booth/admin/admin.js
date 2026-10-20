/* The host's page: events, their look, their code and sign, and their
   strips. Cloudflare Access signs the host in before this page loads; the
   booth service checks that sign-in again on every call. */

import { FRAMES, FONTS, ICONS, MAX_ICONS, compose, sampleShots } from '../js/strip.js?v=2';
import { unpack, encodePNG } from '../../js/photo.js?v=4';

const $ = (id) => document.getElementById(id);
const API = '/api/booth/admin/events';

let events = [];
let current = null;  // the event being edited; null id for a new one
let samples = null;
let previewTimer = 0;
let lastStrip = null;

/* ---- api ------------------------------------------------------------------------- */

async function call(method, path, body, type = 'application/json') {
  const res = await fetch(path, {
    method,
    headers: body ? { 'Content-Type': type } : {},
    body: body && type === 'application/json' ? JSON.stringify(body) : body,
    cache: 'no-store',
  });
  if (res.status === 403) throw new Error('Not signed in. Reload the page to sign in through Cloudflare Access.');
  if (!res.ok) {
    let msg = `error ${res.status}`;
    try { msg = (await res.json()).error || msg; } catch { /* not JSON */ }
    throw new Error(msg);
  }
  return res.status === 204 ? null : res.headers.get('Content-Type')?.includes('json') ? res.json() : res.blob();
}

function note(text) { $('note').textContent = text; }

/* ---- the list --------------------------------------------------------------------- */

async function loadList() {
  try {
    events = (await call('GET', API)).events;
  } catch (e) {
    note(e.message);
    return;
  }
  note(events.length ? '' : 'No events yet.');
  $('events').replaceChildren(...events.map((e) => {
    const li = document.createElement('li');
    const a = document.createElement('a');
    a.href = '#' + e.id;
    a.textContent = e.title;
    const meta = document.createElement('span');
    const code = Object.assign(document.createElement('span'), { className: 'code', textContent: e.code });
    meta.append(code, ` · ${e.active ? 'open' : 'closed'}${e.wall ? ' · wall on' : ''}`);
    li.append(a, meta);
    return li;
  }));
}

function showList() {
  current = null;
  $('edit-view').hidden = true;
  $('list-view').hidden = false;
  loadList();
}

/* The page follows the #: #<event id> edits it, nothing is the list. Links,
   the back button and a bookmarked URL all go through here. */
async function route() {
  const id = location.hash.slice(1);
  if (!id) { showList(); return; }
  if (!events.some((e) => e.id === id)) await loadList();
  const e = events.find((e) => e.id === id);
  if (e) edit(e); else { note('No event with that link.'); showList(); }
}
window.addEventListener('hashchange', route);

/* ---- the editor ------------------------------------------------------------------- */

function fillChoices() {
  $('f-font').replaceChildren(...Object.entries(FONTS).map(([k, f]) => new Option(f.family, k)));
  $('f-frame').replaceChildren(...FRAMES.map((f) => new Option(f[0].toUpperCase() + f.slice(1), f)));
  $('f-icons').replaceChildren(...Object.keys(ICONS).map((name) => {
    const label = document.createElement('label');
    const count = Object.assign(document.createElement('input'), {
      type: 'number', min: 0, max: MAX_ICONS, value: 0, className: 'icon-count',
    });
    count.dataset.icon = name;
    label.append(count, ' ' + name);
    return label;
  }));
}

function edit(e) {
  current = e;
  $('list-view').hidden = true;
  $('edit-view').hidden = false;
  $('sign').hidden = true;
  $('saved-only').hidden = !e.id;
  if (e.id) showShare(e);

  $('f-title').value = e.title;
  $('f-subtitle').value = e.subtitle;
  $('f-font').value = e.font;
  $('f-frame').value = e.frame;
  for (const n of $('f-icons').querySelectorAll('input')) {
    n.value = e.icons.filter((i) => i === n.dataset.icon).length;
  }
  $('f-count-first').value = e.countFirst || 5;
  $('f-count-next').value = e.countNext || 3;
  $('f-active').checked = e.active;
  $('f-wall').checked = e.wall;

  schedulePreview();
  loadStrips();
}

/* The form as an event body, in the shape the booth service validates. */
function formEvent() {
  return {
    title: $('f-title').value.trim(),
    subtitle: $('f-subtitle').value.trim(),
    font: $('f-font').value,
    frame: $('f-frame').value,
    // Each icon as many times as its count, in the picker's order.
    icons: [...$('f-icons').querySelectorAll('input')]
      .flatMap((n) => Array(Math.max(0, Math.min(MAX_ICONS, n.valueAsNumber || 0))).fill(n.dataset.icon)),
    countFirst: $('f-count-first').valueAsNumber || 5,
    countNext: $('f-count-next').valueAsNumber || 3,
    active: $('f-active').checked,
    wall: $('f-wall').checked,
  };
}

// No more than MAX_ICONS in all: the count just raised gives way.
$('f-icons').addEventListener('input', (ev) => {
  const total = [...$('f-icons').querySelectorAll('input')].reduce((a, n) => a + (n.valueAsNumber || 0), 0);
  if (total > MAX_ICONS) ev.target.value = Math.max(0, ev.target.valueAsNumber - (total - MAX_ICONS));
});

$('form').addEventListener('input', schedulePreview);
$('form').addEventListener('change', schedulePreview);

$('form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  try {
    const saved = current.id
      ? await call('PUT', `${API}/${current.id}`, formEvent())
      : await call('POST', API, formEvent());
    note(`Saved. Code: ${saved.code}`);
    history.replaceState(null, '', '#' + saved.id);
    edit(saved);
  } catch (e) {
    note(`Not saved: ${e.message}`);
  }
});

$('new').addEventListener('click', () => edit({
  id: null, title: '', subtitle: '', font: 'display', frame: 'film', icons: [],
  countFirst: 5, countNext: 3, active: true, wall: true,
}));
$('back').addEventListener('click', () => { location.hash = ''; });

$('new-code').addEventListener('click', async () => {
  if (!confirm('Make a new code? The old one stops working, including on guests\' phones.')) return;
  try {
    const e = await call('POST', `${API}/${current.id}/code`);
    note(`New code: ${e.code}`);
    edit(e);
  } catch (e) { note(e.message); }
});

$('delete').addEventListener('click', async () => {
  if (!confirm(`Delete "${current.title}" and every strip it printed? This can't be undone.`)) return;
  try {
    await call('DELETE', `${API}/${current.id}`);
    note('Deleted.');
    events = events.filter((x) => x.id !== current.id);
    location.hash = '';
  } catch (e) { note(e.message); }
});

/* ---- the guest link, QR code and sign ------------------------------------------ */

/* The code rides after the #, which browsers never send to a server, so
   texted links and scanned QR codes don't put it in anyone's logs. */
const guestLink = (e) => `${location.origin}/#${e.code}`;
const spaced = (code) => code.slice(0, 3) + ' ' + code.slice(3);

/* A QR code drawn square-edged onto a canvas, with the standard quiet zone
   of four modules around it. qrcode is the vendored qrcode-generator. */
function drawQR(canvas, text) {
  const qr = window.qrcode(0, 'M');
  qr.addData(text);
  qr.make();
  const n = qr.getModuleCount(), quiet = 4;
  const scale = Math.floor(canvas.width / (n + 2 * quiet));
  const size = scale * (n + 2 * quiet);
  canvas.width = canvas.height = size;
  const ctx = canvas.getContext('2d');
  ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, size, size);
  ctx.fillStyle = '#000';
  for (let r = 0; r < n; r++) {
    for (let c = 0; c < n; c++) {
      if (qr.isDark(r, c)) ctx.fillRect((c + quiet) * scale, (r + quiet) * scale, scale, scale);
    }
  }
}

function showShare(e) {
  $('link').value = guestLink(e);
  $('code-text').textContent = spaced(e.code);
  $('qr').width = 200;
  drawQR($('qr'), guestLink(e));
  $('share').hidden = !navigator.share;
}

$('copy').addEventListener('click', async () => {
  try {
    await navigator.clipboard.writeText(guestLink(current));
    note('Link copied. Paste it into a text to your guests.');
  } catch {
    $('link').select();
    note('Copy the selected link.');
  }
});

// On a phone this opens the share sheet: Messages, WhatsApp, email.
$('share').addEventListener('click', () => {
  navigator.share({ title: current.title, text: `Photobooth for ${current.title}`, url: guestLink(current) })
    .catch(() => { /* closed the sheet */ });
});

$('show-sign').addEventListener('click', () => {
  $('sign-title').textContent = current.title;
  $('sign-code').textContent = spaced(current.code);
  $('sign-qr').width = 400;
  drawQR($('sign-qr'), guestLink(current));
  $('sign').hidden = false;
  window.print();
});

// The sign only exists on paper; hide it again once the print dialog closes.
window.addEventListener('afterprint', () => { $('sign').hidden = true; });

/* ---- preview and test print ------------------------------------------------------- */

function schedulePreview() {
  clearTimeout(previewTimer);
  previewTimer = setTimeout(preview, 250);
}

async function preview() {
  const t = formEvent();
  if (!t.title) t.title = 'Your title';
  samples ??= sampleShots();
  lastStrip = await compose(t, samples, new Date());
  const c = $('preview');
  c.width = 384; c.height = lastStrip.h;
  const ctx = c.getContext('2d');
  const img = ctx.createImageData(384, lastStrip.h);
  unpack(lastStrip.bits, 384, lastStrip.h, img.data);
  ctx.putImageData(img, 0, 0);
}

$('test').addEventListener('click', async () => {
  if (!current.id) { note('Save the event first, then print a test strip.'); return; }
  await preview();
  note('Printing a test strip...');
  try {
    await call('POST', `${API}/${current.id}/test`, encodePNG(lastStrip.bits, 384, lastStrip.h), 'image/png');
    note('Test strip printed. It isn\'t kept or shown on the wall.');
  } catch (e) { note(`Test print failed: ${e.message}`); }
});

/* ---- strips --------------------------------------------------------------------------- */

// Every strip in one zip, enlarged for a photo album. Fetched rather than
// linked, so it waits for the zip and can say if something went wrong.
$('download-all').addEventListener('click', async () => {
  note('Making the zip...');
  try {
    const blob = await call('GET', `${API}/${current.id}/strips.zip`);
    const a = Object.assign(document.createElement('a'), {
      href: URL.createObjectURL(blob),
      download: `${current.title.normalize('NFD').replace(/[\u0300-\u036f]/g, '').toLowerCase()
        .replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'photobooth'}-strips.zip`,
    });
    a.click();
    note('Downloaded.');
  } catch (e) { note(`Download failed: ${e.message}`); }
});

async function loadStrips() {
  const list = $('strips');
  list.replaceChildren();
  if (!current.id) return;
  let strips;
  try {
    strips = (await call('GET', `${API}/${current.id}/strips`)).strips;
  } catch (e) { note(e.message); return; }

  for (const s of strips) {
    const li = document.createElement('li');
    li.classList.toggle('hidden', !!s.hidden);
    const img = Object.assign(document.createElement('img'), { alt: new Date(s.time).toLocaleString(), className: 'strip' });
    const btn = document.createElement('button');
    btn.textContent = s.hidden ? 'Show on wall' : 'Hide from wall';
    btn.addEventListener('click', async () => {
      try {
        await call('POST', `${API}/${current.id}/strips/${s.id}/${s.hidden ? 'show' : 'hide'}`);
        loadStrips();
      } catch (e) { note(e.message); }
    });
    const del = Object.assign(document.createElement('button'), { textContent: 'Delete', className: 'quiet' });
    del.addEventListener('click', async () => {
      if (!confirm('Delete this strip for good? It can\'t be undone.')) return;
      try {
        await call('DELETE', `${API}/${current.id}/strips/${s.id}`);
        loadStrips();
      } catch (e) { note(e.message); }
    });
    li.append(img, btn, del);
    list.append(li);
    call('GET', `${API}/${current.id}/strips/${s.id}`).then((blob) => { img.src = URL.createObjectURL(blob); });
  }
}

/* ---- start ------------------------------------------------------------------------------ */

fillChoices();
await loadList();
route();
