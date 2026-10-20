/* An event's wall: every strip printed there, newest first, new ones
   appearing as they print. Only for guests who have the event's code. */

import { savedCode, join, strips, stripBlob, reprint } from './js/api.js?v=3';

const POLL_MS = 10_000;
const BUSY_RETRY_MS = 3000;
const BUSY_GIVE_UP_MS = 90_000;
// Saved copies are enlarged: 384 dots wide is a thumbnail in a photo album.
// A whole multiple, without smoothing, so the dots stay crisp squares.
const SAVE_SCALE = 3;

const $ = (id) => document.getElementById(id);
const shown = new Set();
const blobs = new Map(); // strip id -> its picture
const urls = new Map();  // strip id -> an object URL of it, for <img>
let code = '';
let title = 'photobooth';
// The open strip, enlarged and ready to share. Prepared when it opens, not
// on the tap: iPhones only open the share sheet straight from a tap, with
// no work in between.
let ready = null;

async function refresh(code) {
  let list;
  try {
    list = await strips(code);
  } catch (e) {
    $('note').textContent = e.status === 403 ? "This event's wall is turned off." : `Couldn't load the strips: ${e.message}`;
    return false;
  }

  $('note').textContent = list.length ? '' : 'No strips yet. Go take some!';

  // Newest first: walk oldest to newest, putting each new one on top.
  for (const s of list.slice().reverse()) {
    if (shown.has(s.id)) continue;
    shown.add(s.id);
    const li = document.createElement('li');
    const a = Object.assign(document.createElement('a'), { href: '#' + s.id });
    const img = document.createElement('img');
    img.alt = `Photo strip, ${new Date(s.time).toLocaleString()}`;
    img.width = 384;
    a.append(img);
    li.append(a);
    $('strips').prepend(li);
    stripBlob(code, s.id).then((b) => {
      blobs.set(s.id, b);
      urls.set(s.id, URL.createObjectURL(b));
      img.src = urls.get(s.id);
      if (location.hash === '#' + s.id) route();
    }).catch(() => li.remove());
  }
  return true;
}

/* ---- one strip ------------------------------------------------------------------ */

/* The page follows the #: a strip id opens it, nothing is the wall. */
function route() {
  const id = location.hash.slice(1);
  const open = id && urls.has(id);
  $('detail').hidden = !open;
  $('strips').hidden = !!open;
  $('note').hidden = !!open;
  if (!open) return;
  $('detail-img').src = urls.get(id);
  $('detail-status').textContent = '';
  $('again').disabled = false;
  $('download').href = '#';
  ready = null;
  enlarged(id).then((blob) => {
    if (location.hash !== '#' + id) return;
    ready = new File([blob], fileName(id), { type: 'image/png' });
    $('download').href = URL.createObjectURL(blob);
    $('download').download = fileName(id);
  }).catch((e) => { $('detail-status').textContent = `Couldn't prepare the picture: ${e.message}`; });
  window.scrollTo(0, 0);
}
window.addEventListener('hashchange', route);
$('back').addEventListener('click', (e) => { e.preventDefault(); location.hash = ''; });

/* The strip enlarged for keeping, as a PNG blob. */
async function enlarged(id) {
  const bmp = await createImageBitmap(blobs.get(id));
  const c = document.createElement('canvas');
  c.width = bmp.width * SAVE_SCALE; c.height = bmp.height * SAVE_SCALE;
  const ctx = c.getContext('2d');
  ctx.imageSmoothingEnabled = false;
  ctx.drawImage(bmp, 0, 0, c.width, c.height);
  bmp.close();
  return new Promise((ok, bad) => c.toBlob((b) => (b ? ok(b) : bad(new Error('no PNG'))), 'image/png'));
}

const fileName = (id) => `${title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'photobooth'}-${id.slice(0, 8)}.png`;

// The share sheet: on a phone it has Save Image (to Photos), Messages and
// the rest. Without one, fall back to downloading the file.
$('save').addEventListener('click', () => {
  if (!ready) { $('detail-status').textContent = 'Still getting it ready, try again in a second.'; return; }
  if (navigator.canShare?.({ files: [ready] })) {
    navigator.share({ files: [ready], title }).catch(() => { /* closed the sheet */ });
  } else {
    $('download').click();
  }
});

$('again').addEventListener('click', async () => {
  const id = location.hash.slice(1);
  const status = $('detail-status');
  $('again').disabled = true;
  const started = Date.now();
  for (;;) {
    status.textContent = 'Printing...';
    try {
      await reprint(code, id);
      status.textContent = 'Printed! Grab it from the printer.';
      $('again').disabled = false;
      return;
    } catch (e) {
      if (e.status === 409 && /busy/i.test(e.message) && Date.now() - started < BUSY_GIVE_UP_MS) {
        status.textContent = 'Someone else is printing. Waiting for the printer...';
        await new Promise((r) => setTimeout(r, BUSY_RETRY_MS));
        continue;
      }
      status.textContent = e.status === 429
        ? "That's a lot of strips! Wait a minute and try again."
        : `Didn't print: ${e.message}`;
      $('again').disabled = false;
      return;
    }
  }
});

/* ---- start -------------------------------------------------------------------------- */

code = savedCode();
if (!code) {
  location.replace('/');
} else {
  join(code).then(async (theme) => {
    if (!theme) { location.replace('/'); return; }
    title = theme.title;
    $('title').textContent = theme.title;
    document.title = `${theme.title} - photobooth`;
    if (await refresh(code)) setInterval(() => refresh(code), POLL_MS);
    route();
  });
}
