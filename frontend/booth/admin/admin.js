/* The host's page: events, their look, their code and sign, and their
   strips. Cloudflare Access signs the host in before this page loads; the
   booth service checks that sign-in again on every call. */

import { FRAMES, FONTS, ICONS, MAX_ICONS, compose, sampleShots } from '../js/strip.js?v=1';
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
  history.replaceState(null, '', location.pathname);
  loadList();
}

/* ---- the editor ------------------------------------------------------------------- */

function fillChoices() {
  $('f-font').replaceChildren(...Object.entries(FONTS).map(([k, f]) => new Option(f.family, k)));
  $('f-frame').replaceChildren(...FRAMES.map((f) => new Option(f[0].toUpperCase() + f.slice(1), f)));
  $('f-icons').replaceChildren(...Object.keys(ICONS).map((name) => {
    const label = document.createElement('label');
    const box = Object.assign(document.createElement('input'), { type: 'checkbox', value: name });
    label.append(box, ' ' + name);
    return label;
  }));
}

function edit(e) {
  current = e;
  $('list-view').hidden = true;
  $('edit-view').hidden = false;
  $('sign').hidden = true;
  $('saved-only').hidden = !e.id;

  $('f-title').value = e.title;
  $('f-subtitle').value = e.subtitle;
  $('f-font').value = e.font;
  $('f-frame').value = e.frame;
  for (const box of $('f-icons').querySelectorAll('input')) box.checked = e.icons.includes(box.value);
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
    icons: [...$('f-icons').querySelectorAll('input:checked')].map((b) => b.value),
    active: $('f-active').checked,
    wall: $('f-wall').checked,
  };
}

// No more than MAX_ICONS ticked: untick the oldest choice otherwise.
$('f-icons').addEventListener('change', (ev) => {
  const ticked = [...$('f-icons').querySelectorAll('input:checked')];
  if (ticked.length > MAX_ICONS) ev.target.checked = false;
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
  id: null, title: '', subtitle: '', font: 'display', frame: 'film', icons: [], active: true, wall: true,
}));
$('back').addEventListener('click', showList);

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
    showList();
  } catch (e) { note(e.message); }
});

$('show-sign').addEventListener('click', () => {
  $('sign-title').textContent = current.title;
  $('sign-code').textContent = current.code.slice(0, 3) + ' ' + current.code.slice(3);
  $('sign').hidden = false;
  $('sign').scrollIntoView({ behavior: 'smooth' });
  setTimeout(() => window.print(), 300);
});

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
    li.append(img, btn);
    list.append(li);
    call('GET', `${API}/${current.id}/strips/${s.id}`).then((blob) => { img.src = URL.createObjectURL(blob); });
  }
}

/* ---- start ------------------------------------------------------------------------------ */

fillChoices();
await loadList();
const fromHash = events.find((e) => '#' + e.id === location.hash);
if (fromHash) edit(fromHash);
