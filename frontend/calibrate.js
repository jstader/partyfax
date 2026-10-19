/* Calibration: the same photo printed full size once per way of turning it
   into dots, to choose the default by looking at paper rather than a screen.
   Each receipt is numbered across the top and has a black-to-white ramp
   along the bottom. Not linked from the site.

   The site allows five prints a minute per address, so the queue prints
   what it can and waits out the limit for the rest by itself. */

// Versioned, so a browser holding an older copy cannot pair it with this page.
import { fit, dither, unpack, encodePNG } from './js/photo.js?v=4';
import { shrink, decode } from './js/scale.js?v=4';

export const VARIANTS = [
  { label: 'Floyd-Steinberg (original)', algorithm: 'fs', gamma: 1, sharpen: 0 },
  { label: 'F-S serpentine, gamma 1.5, sharp', algorithm: 'fs', serpentine: true, gamma: 1.5, sharpen: 0.8 },
  { label: 'Atkinson, gamma 1.0, sharp', algorithm: 'atkinson', gamma: 1.0, sharpen: 0.8 },
  { label: 'Atkinson, gamma 1.4, sharp', algorithm: 'atkinson', gamma: 1.4, sharpen: 0.8 },
  { label: 'Atkinson, gamma 1.8, sharp (default)', algorithm: 'atkinson', gamma: 1.8, sharpen: 0.8 },
  { label: 'Atkinson, gamma 2.2, sharp', algorithm: 'atkinson', gamma: 2.2, sharpen: 0.8 },
];

// The server's limit is 384 x 384 dots; the band and ramp come out of it.
const W = 384, MAX_H = 384, BAND = 30, RAMP = 24, GAP = 4;
const PHOTO_MAX = MAX_H - BAND - RAMP - 2 * GAP;

// How long to wait before asking again once the limit says no.
const RETRY_MS = 60_000;

const $ = (id) => document.getElementById(id);
const status = $('status'), send = $('send'), sheets = $('sheets');
let prints = []; // { n, bits, h, row } per variant, once a photo is chosen

$('file').addEventListener('change', async () => {
  const f = $('file').files[0];
  if (!f) return;
  status.textContent = 'working...';
  let bmp;
  try {
    bmp = await decode(f);
  } catch {
    status.textContent = `could not read that picture (${f.type || 'unknown type'}); try a JPEG or PNG`;
    return;
  }
  const { sy, sh, h } = fit(bmp.width, bmp.height, W, PHOTO_MAX);
  const photo = shrink(bmp, 0, sy, bmp.width, sh, W, h);
  if (bmp.close) bmp.close();

  sheets.replaceChildren();
  prints = VARIANTS.map((v, i) => {
    const { bits, h: total } = compose(photo, h, v, i + 1);
    return { n: i + 1, bits, h: total, row: row(i + 1, v.label, bits, total) };
  });
  send.disabled = false;
  status.textContent = 'untick any you don\'t need, then print';
});

send.addEventListener('click', async () => {
  const queue = prints.filter((p) => p.row.querySelector('input').checked);
  if (!queue.length) return;
  send.disabled = true;
  for (const p of queue) {
    const note = p.row.querySelector('.note');
    note.textContent = 'queued';
  }
  for (const p of queue) await printOne(p);
  status.textContent = 'done: pick the number that looks best on paper';
  send.disabled = false;
});

async function printOne(p) {
  const note = p.row.querySelector('.note');
  for (;;) {
    note.textContent = 'printing...';
    status.textContent = `printing ${p.n}`;
    let res, body = {};
    try {
      res = await fetch('/api/print', {
        method: 'POST',
        headers: { 'Content-Type': 'image/png' },
        body: encodePNG(p.bits, W, p.h),
        signal: AbortSignal.timeout(45000),
      });
      body = await res.json().catch(() => ({}));
    } catch (e) {
      note.textContent = `failed: ${e.message}`;
      return;
    }
    if (res.ok) { note.textContent = `printed (${body.id || ''})`; return; }
    if (res.status !== 429) { note.textContent = body.error || `failed (${res.status})`; return; }
    await countdown(note, RETRY_MS);
  }
}

async function countdown(note, ms) {
  for (let left = ms; left > 0; left -= 1000) {
    note.textContent = `rate limit: retrying in ${Math.ceil(left / 1000)}s`;
    status.textContent = 'waiting for the print limit (5 a minute); leave this page open';
    await new Promise((r) => setTimeout(r, 1000));
  }
}

/* One receipt: number band, the photo dithered this way, the ramp. */
function compose(photo, photoH, v, n) {
  const h = BAND + GAP + photoH + GAP + RAMP;
  const c = document.createElement('canvas');
  c.width = W; c.height = h;
  const ctx = c.getContext('2d', { willReadFrequently: true });
  ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, W, h);

  // Black band, white text: reads at a glance on paper.
  ctx.fillStyle = '#000'; ctx.fillRect(0, 0, W, BAND);
  ctx.fillStyle = '#fff'; ctx.font = 'bold 16px monospace'; ctx.textBaseline = 'middle';
  ctx.fillText(`${n}  ${v.label}`, 8, BAND / 2 + 1);

  paste(ctx, dither(photo.slice(), W, photoH, v), W, photoH, 0, BAND + GAP);

  const ramp = new Uint8ClampedArray(W * RAMP * 4);
  for (let y = 0; y < RAMP; y++) {
    for (let x = 0; x < W; x++) {
      const o = (y * W + x) * 4, g = Math.round(x * 255 / (W - 1));
      ramp[o] = ramp[o + 1] = ramp[o + 2] = g; ramp[o + 3] = 255;
    }
  }
  paste(ctx, dither(ramp, W, RAMP, v), W, RAMP, 0, h - RAMP);

  // Back to one bit. All of it is black or white already except the text's
  // antialiased edges, so a threshold, not a dither.
  const px = ctx.getImageData(0, 0, W, h).data, stride = W >> 3, bits = new Uint8Array(stride * h);
  for (let i = 0; i < W * h; i++) {
    if (px[i * 4] >= 128) bits[(i / W | 0) * stride + ((i % W) >> 3)] |= 0x80 >> (i % W & 7);
  }
  return { bits, h };
}

function paste(ctx, bits, w, h, x, y) {
  const img = ctx.createImageData(w, h);
  unpack(bits, w, h, img.data);
  ctx.putImageData(img, x, y);
}

/* The preview for one variant, with its tick box and progress note. */
function row(n, label, bits, h) {
  const li = document.createElement('div');
  const head = document.createElement('label');
  const box = Object.assign(document.createElement('input'), { type: 'checkbox', checked: true });
  head.append(box, ` ${n}. ${label} `, Object.assign(document.createElement('span'), { className: 'note' }));
  const cv = document.createElement('canvas');
  cv.width = W; cv.height = h;
  const ctx = cv.getContext('2d');
  const img = ctx.createImageData(W, h);
  unpack(bits, W, h, img.data);
  ctx.putImageData(img, 0, 0);
  li.append(head, cv);
  sheets.append(li);
  return li;
}
