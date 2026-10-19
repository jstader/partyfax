/* One receipt comparing ways of turning a photo into dots, to choose the
   default by looking at paper rather than a screen. Six tiles, each the same
   crop of the chosen photo with a black-to-white ramp under it, numbered so
   the winner can be named. Not linked from the site. */

import { dither, unpack, encodePNG } from './js/photo.js';
import { shrink } from './js/scale.js';

export const VARIANTS = [
  { label: 'Floyd-Steinberg, as before', algorithm: 'fs' },
  { label: 'Floyd-Steinberg serpentine, gamma 1.5, sharpen', algorithm: 'fs', serpentine: true, gamma: 1.5, sharpen: 0.8 },
  { label: 'Atkinson, gamma 1.0, sharpen', algorithm: 'atkinson', gamma: 1.0, sharpen: 0.8 },
  { label: 'Atkinson, gamma 1.4, sharpen', algorithm: 'atkinson', gamma: 1.4, sharpen: 0.8 },
  { label: 'Atkinson, gamma 1.8, sharpen', algorithm: 'atkinson', gamma: 1.8, sharpen: 0.8 },
  { label: 'Atkinson, gamma 2.2, sharpen', algorithm: 'atkinson', gamma: 2.2, sharpen: 0.8 },
];

const W = 384, COLS = 3, TILE_W = 128, PHOTO_H = 160, RAMP_H = 16, GAP = 8;
const H = 2 * (PHOTO_H + RAMP_H + GAP) - GAP;

const $ = (id) => document.getElementById(id);
const out = $('sheet'), status = $('status'), send = $('send');
let sheet = null;

$('legend').append(...VARIANTS.map((v) => Object.assign(document.createElement('li'), { textContent: v.label })));

$('file').addEventListener('change', async () => {
  const f = $('file').files[0];
  if (!f) return;
  status.textContent = 'working...';
  const bmp = await createImageBitmap(f);
  sheet = compose(bmp);
  if (bmp.close) bmp.close();
  show(sheet);
  send.disabled = false;
  status.textContent = 'check the preview, then print it';
});

send.addEventListener('click', async () => {
  send.disabled = true;
  status.textContent = 'printing...';
  try {
    const res = await fetch('/api/print', {
      method: 'POST',
      headers: { 'Content-Type': 'image/png' },
      body: encodePNG(sheet, W, H),
      signal: AbortSignal.timeout(45000),
    });
    const body = await res.json().catch(() => ({}));
    status.textContent = res.ok ? 'printed: pick the number that looks best on paper' : (body.error || `failed (${res.status})`);
  } catch (e) {
    status.textContent = `failed: ${e.message}`;
  }
  send.disabled = false;
});

/* The sheet as packed one-bit rows, ready for encodePNG. */
function compose(bmp) {
  // The middle of the photo at the tile's shape.
  const want = TILE_W / PHOTO_H;
  let sw = bmp.width, sh = bmp.height;
  if (sw / sh > want) sw = sh * want; else sh = sw / want;
  const crop = shrink(bmp, (bmp.width - sw) / 2, (bmp.height - sh) / 2, sw, sh, TILE_W, PHOTO_H);

  const ramp = new Uint8ClampedArray(TILE_W * RAMP_H * 4);
  for (let y = 0; y < RAMP_H; y++) {
    for (let x = 0; x < TILE_W; x++) {
      const o = (y * TILE_W + x) * 4, v = Math.round(x * 255 / (TILE_W - 1));
      ramp[o] = ramp[o + 1] = ramp[o + 2] = v; ramp[o + 3] = 255;
    }
  }

  const c = document.createElement('canvas');
  c.width = W; c.height = H;
  const ctx = c.getContext('2d', { willReadFrequently: true });
  ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, W, H);

  VARIANTS.forEach((v, n) => {
    const x = (n % COLS) * TILE_W, y = Math.floor(n / COLS) * (PHOTO_H + RAMP_H + GAP);
    paste(ctx, dither(crop.slice(), TILE_W, PHOTO_H, v), x, y, PHOTO_H);
    paste(ctx, dither(ramp.slice(), TILE_W, RAMP_H, v), x, y + PHOTO_H, RAMP_H);

    // The number, white on black in the corner, so it reads on any photo.
    ctx.fillStyle = '#000'; ctx.fillRect(x, y, 18, 18);
    ctx.fillStyle = '#fff'; ctx.font = 'bold 16px monospace'; ctx.textBaseline = 'top';
    ctx.fillText(String(n + 1), x + 4, y + 1);
  });

  // Back to one bit. Everything is already black or white except the
  // numbers' antialiased edges, so a threshold, not a dither.
  const px = ctx.getImageData(0, 0, W, H).data, stride = W >> 3, bits = new Uint8Array(stride * H);
  for (let i = 0; i < W * H; i++) {
    if (px[i * 4] >= 128) bits[(i / W | 0) * stride + ((i % W) >> 3)] |= 0x80 >> (i % W & 7);
  }
  return bits;
}

function paste(ctx, bits, x, y, h) {
  const img = ctx.createImageData(TILE_W, h);
  unpack(bits, TILE_W, h, img.data);
  ctx.putImageData(img, x, y);
}

function show(bits) {
  out.width = W; out.height = H;
  const ctx = out.getContext('2d');
  const img = ctx.createImageData(W, H);
  unpack(bits, W, H, img.data);
  ctx.putImageData(img, 0, 0);
}
