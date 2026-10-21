/* One strip, opened from the wall (/strip#<id>): keep it, share it, or print
   it again. Only for guests who have the event's code. */

import { savedCode, join, stripBlob, reprint, whileBusy } from './js/api.js';

// Saved copies are enlarged: 384 dots wide is a thumbnail in a photo album.
// A whole multiple, without smoothing, so the dots stay crisp squares.
const SAVE_SCALE = 3;

const $ = (id) => document.getElementById(id);
const id = decodeURIComponent(location.hash.slice(1));
const code = savedCode();
let title = 'photobooth';
// The strip, enlarged and ready to share. Prepared on arrival, not on the
// tap: iPhones only open the share sheet straight from a tap, with no work
// in between.
let ready = null;

// "Spooky Soirée" -> "spooky-soiree": accents dropped to their letter.
const slug = (t) => t.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase()
  .replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'photobooth';

async function enlarged(blob) {
  const bmp = await createImageBitmap(blob);
  const c = document.createElement('canvas');
  c.width = bmp.width * SAVE_SCALE; c.height = bmp.height * SAVE_SCALE;
  const ctx = c.getContext('2d');
  ctx.imageSmoothingEnabled = false;
  ctx.drawImage(bmp, 0, 0, c.width, c.height);
  bmp.close();
  return new Promise((ok, bad) => c.toBlob((b) => (b ? ok(b) : bad(new Error('no PNG'))), 'image/png'));
}

// The share sheet: on a phone it has Save Image (to Photos), Messages and
// the rest. Where there's no share sheet, the file downloads instead.
$('save').addEventListener('click', () => {
  if (!ready) return;
  if (navigator.canShare?.({ files: [ready] })) {
    navigator.share({ files: [ready], title }).catch(() => { /* closed the sheet */ });
    return;
  }
  const a = Object.assign(document.createElement('a'), { href: URL.createObjectURL(ready), download: ready.name });
  a.click();
});

$('reprint').addEventListener('click', async () => {
  const status = $('status');
  $('reprint').disabled = true;
  try {
    await whileBusy(
      () => { status.textContent = 'Printing...'; return reprint(code, id); },
      () => { status.textContent = 'Someone else is printing. Waiting for the printer...'; },
    );
    status.textContent = 'Printed! Grab it from the printer.';
  } catch (e) {
    status.textContent = `Didn't print: ${e.message}`;
  }
  $('reprint').disabled = false;
});

async function start() {
  if (!code || !id) { location.replace(code ? '/wall' : '/'); return; }
  let theme;
  try {
    theme = await join(code);
  } catch (e) {
    $('status').textContent = `Couldn't check the event code: ${e.message}`;
    $('reprint').disabled = true;
    return;
  }
  if (!theme) { location.replace('/'); return; }
  title = theme.title;
  document.title = `${theme.title} - photo strip`;

  let blob;
  try {
    blob = await stripBlob(code, id);
  } catch (e) {
    $('status').textContent = `Couldn't load this strip: ${e.message}`;
    $('reprint').disabled = true;
    return;
  }
  $('strip').src = URL.createObjectURL(blob);

  try {
    ready = new File([await enlarged(blob)], `${slug(title)}-${id.slice(0, 8)}.png`, { type: 'image/png' });
    $('save').disabled = false;
  } catch (e) {
    $('status').textContent = `Couldn't prepare the picture: ${e.message}`;
  }
}

start();
