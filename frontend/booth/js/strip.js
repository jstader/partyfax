/* A photobooth strip: four photos stacked down the paper inside an event's
   frame, under its title, over the date. Composed here, on the guest's
   phone, so the preview is exactly the dots the printer gets -- the same
   rule as the main site (js/photo.js).

   The geometry, frames and icons are plain data and rectangles, so the tests
   can check them without a browser; only compose() needs a canvas. */

import { dither } from '../../js/photo.js';
import { shrink } from '../../js/scale.js';

export const W = 384;           // dots across the paper
export const MAX_ROWS = 1400;   // the server's limit for a strip (doc.StripMaxRows)
export const SHOTS = 4;

/* What the booth service accepts (backend/cmd/booth/store.go); keep in step. */
export const FRAMES = ['line', 'double', 'dotted', 'film'];
export const FONTS = {
  // Pixel font, the main site's: crisp at one bit.
  vt323: { family: 'VT323', url: '/fonts/VT323.woff2', title: 52, subtitle: 26 },
  // Bold condensed capitals. Bebas Neue, OFL, google/fonts ofl/bebasneue
  // (2019-10-17, 1e42f687f0).
  display: { family: 'Bebas Neue', url: '/fonts/BebasNeue-Regular.ttf', title: 50, subtitle: 24 },
  // Script. Pacifico, OFL, google/fonts ofl/pacifico (2019-08-23, 34ce35e2b6).
  script: { family: 'Pacifico', url: '/fonts/Pacifico-Regular.ttf', title: 40, subtitle: 20 },
};
export const MAX_ICONS = 6;   // repeats allowed: three pumpkins is three
// How far a photo's darkest tones are lifted (photo.js lift). At 0.75 pure
// black prints about 80% of its dots: Atkinson dithering keeps some darkness.
export const PHOTO_INK = 0.75;

/* ---- geometry ----------------------------------------------------------- */

/* How much of each side a frame takes. The film strip's black bands are
   wider, for the sprocket holes. */
const SIDE = { line: 14, double: 16, dotted: 14, film: 32 };

const PAD = 16;          // above the title, below the site name
const ICON = 32;         // an icon as printed: 16 x 16 pixels at 2x
const GAP = 10;          // between photos
const FOOTER = 50;       // date and site name

/* Where everything goes for a theme: the strip's height and every box in
   it, in dots. Nothing is drawn. */
export function layout(theme) {
  const font = FONTS[theme.font] || FONTS.vt323;
  const side = SIDE[theme.frame] ?? SIDE.line;
  const content = { x: side, w: W - 2 * side };

  let y = PAD;
  const title = { y, h: Math.round(font.title * 1.25), size: font.title };
  y += title.h;

  let subtitle = null;
  if ((theme.subtitle || '').trim()) {
    subtitle = { y, h: Math.round(font.subtitle * 1.4), size: font.subtitle };
    y += subtitle.h;
  }

  let icons = null;
  const names = (theme.icons || []).slice(0, MAX_ICONS);
  if (names.length) {
    // Spread evenly across the content width, centred.
    const spacing = ICON + 16, span = names.length * spacing - 16;
    const x0 = content.x + Math.round((content.w - span) / 2);
    icons = { y: y + 4, h: ICON, xs: names.map((_, i) => x0 + i * spacing), names };
    y += ICON + 8;
  }
  y += 8;

  // Four-by-three windows, as wide as the frame allows.
  const wh = Math.round(content.w * 3 / 4);
  const windows = [];
  for (let i = 0; i < SHOTS; i++) {
    windows.push({ x: content.x, y, w: content.w, h: wh });
    y += wh + (i < SHOTS - 1 ? GAP : 0);
  }

  y += 12;
  const footer = { y, h: FOOTER };
  y += FOOTER + PAD;

  return { w: W, h: y, side, content, title, subtitle, icons, windows, footer };
}

/* ---- frames --------------------------------------------------------------- */

/* The frame as rectangles: [x, y, w, h, white]. Black unless white is true;
   white is for punching holes into something already black. */
export function frameRects(frame, L) {
  const r = [];
  const box = (x, y, w, h, t) => {
    r.push([x, y, w, t], [x, y + h - t, w, t], [x, y, t, h], [x + w - t, y, t, h]);
  };

  switch (frame) {
    case 'double':
      box(4, 4, W - 8, L.h - 8, 4);
      box(11, 11, W - 22, L.h - 22, 1);
      break;

    case 'dotted': {
      const d = 4, step = 10, inset = 5;
      for (let x = inset; x + d <= W - inset; x += step) r.push([x, inset, d, d], [x, L.h - inset - d, d, d]);
      for (let y = inset + step; y + d <= L.h - inset - step; y += step) r.push([inset, y, d, d], [W - inset - d, y, d, d]);
      break;
    }

    case 'film': {
      // A film negative down both sides: a line along each edge of the band
      // and black sprocket holes, not a solid black band with white ones --
      // about a third of the black. Solid black down both edges heated part
      // of every line of the strip, and dark strips were knocking the
      // printer off USB.
      const band = L.side - 6, line = 1;
      r.push([0, 0, line, L.h], [band - line, 0, line, L.h], [W - band, 0, line, L.h], [W - line, 0, line, L.h]);
      const hw = 10, hh = 14, step = 26;
      for (let y = 8; y + hh <= L.h - 8; y += step) {
        r.push([(band - hw) >> 1, y, hw, hh], [W - band + ((band - hw) >> 1), y, hw, hh]);
      }
      break;
    }

    default: // line
      box(6, 6, W - 12, L.h - 12, 3);
  }

  // A fine edge around each photo, so a pale one still reads as a frame.
  if (frame !== 'film') for (const w of L.windows) box(w.x - 2, w.y - 2, w.w + 4, w.h + 4, 1);

  return r;
}

/* ---- icons ---------------------------------------------------------------- */

/* Sixteen by sixteen, '#' black. Drawn at 2x, so 32 dots on paper. */
export const ICONS = {
  heart: [
    '................', '..####....####..', '.######..######.', '################',
    '################', '################', '################', '.##############.',
    '..############..', '...##########...', '....########....', '.....######.....',
    '......####......', '.......##.......', '................', '................',
  ],
  star: [
    '.......##.......', '.......##.......', '......####......', '......####......',
    '.....######.....', '################', '.##############.', '..############..',
    '...##########...', '....########....', '....########....', '...####..####...',
    '...###....###...', '..###......###..', '..##........##..', '................',
  ],
  balloon: [
    '....########....', '..############..', '.##############.', '.###.##########.',
    '##.############.', '################', '################', '.##############.',
    '.##############.', '..############..', '....########....', '......####......',
    '.......##.......', '......#.........', '.......#........', '........#.......',
  ],
  cake: [
    '....#...#...#...', '...###.###.###..', '....#...#...#...', '....#...#...#...',
    '..############..', '.##############.', '.#.#.#.#.#.#.#.#', '.##############.',
    '.##############.', '.#............#.', '.##############.', '.##############.',
    '.##############.', '################', '................', '................',
  ],
  confetti: [
    '#.......#.....#.', '..##.......#....', '..##..#........#', '......#..##.....',
    '.#.......##..#..', '....#...........', '...###.....#....', '....#.....###...',
    '..........#.....', '#.....##......#.', '......##..#.....', '..#.........##..',
    '.###...#....##..', '..#...###.......', '.......#....#..#', '#.........#.....',
  ],
  music: [
    '................', '....##########..', '....##########..', '....#........#..',
    '....#........#..', '....#........#..', '....#........#..', '....#........#..',
    '....#........#..', '..###......###..', '.####.....####..', '.####.....####..',
    '..##.......##...', '................', '................', '................',
  ],
  camera: [
    '................', '.....####.......', '....######......', '.##############.',
    '################', '######....######', '#####......#####', '####...##...####',
    '####..####..####', '####..####..####', '####...##...####', '#####......#####',
    '######....######', '################', '.##############.', '................',
  ],
  sparkle: [
    '.......#........', '.......#........', '......###.......', '......###.......',
    '.....#####......', '..###########...', '###############.', '..###########...',
    '.....#####......', '......###.......', '......###.......', '.......#........',
    '.......#........', '................', '................', '................',
  ],
  pumpkin: [
    '.......##.......', '......##........', '..#####..#####..', '.##############.',
    '################', '###..######..###', '##....####....##', '################',
    '#######..#######', '################', '##.##.####.##.##', '###..........###',
    '.###.##..##.###.', '..############..', '....########....', '................',
  ],
  ghost: [
    '................', '.....######.....', '...##########...', '..############..',
    '.##############.', '.###..####..###.', '.###..####..###.', '.##############.',
    '.######..######.', '.######..######.', '.##############.', '.##############.',
    '.##############.', '.##.###..###.##.', '.#...#....#...#.', '................',
  ],
  tree: [
    '.......##.......', '......####......', '.......##.......', '......####......',
    '.....######.....', '....########....', '.....######.....', '....########....',
    '...##########...', '..############..', '....########....', '...##########...',
    '.##############.', '################', '......####......', '......####......',
  ],
  snowflake: [
    '.......##.......', '....#..##..#....', '.....#.##.#.....', '..#...####...#..',
    '...#..####..#...', '....########....', '################', '################',
    '....########....', '...#..####..#...', '..#...####...#..', '.....#.##.#.....',
    '....#..##..#....', '.......##.......', '................', '................',
  ],
  gift: [
    '....##....##....', '.....##..##.....', '......####......', '.######..######.',
    '.######..######.', '................', '..#####..#####..', '..#####..#####..',
    '..#####..#####..', '..#####..#####..', '..#####..#####..', '..#####..#####..',
    '..#####..#####..', '..#####..#####..', '................', '................',
  ],
  bat: [
    '................', '......#..#......', '......####......', '#.....####.....#',
    '##...######...##', '###.########.###', '################', '################',
    '####.######.####', '##....####....##', '#......##......#', '................',
    '................', '................', '................', '................',
  ],
};

/* An icon as rectangles, one per run of black in a row. */
export function iconRects(name, x, y, scale = 2) {
  const rows = ICONS[name], r = [];
  if (!rows) return r;
  rows.forEach((row, j) => {
    for (let i = 0; i < row.length;) {
      if (row[i] !== '#') { i++; continue; }
      let k = i;
      while (k < row.length && row[k] === '#') k++;
      r.push([x + i * scale, y + j * scale, (k - i) * scale, scale]);
      i = k;
    }
  });
  return r;
}

/* ---- bits ------------------------------------------------------------------- */

/* Copy a packed picture into a packed strip at (x, y). Both are one bit a
   dot, rows of (w + 7) >> 3 bytes, leftmost dot in the high bit, as
   photo.js packs them. */
export function blit(dst, dstW, src, srcW, srcH, x, y) {
  const ds = (dstW + 7) >> 3, ss = (srcW + 7) >> 3;
  for (let j = 0; j < srcH; j++) {
    for (let i = 0; i < srcW; i++) {
      const on = src[j * ss + (i >> 3)] & (0x80 >> (i & 7));
      const X = x + i, o = (y + j) * ds + (X >> 3), m = 0x80 >> (X & 7);
      if (on) dst[o] |= m; else dst[o] &= ~m;
    }
  }
  return dst;
}

/* Flip RGBA rows left to right, in place: the front camera's preview is a
   mirror, and the strip prints what the guest saw. */
export function mirror(rgba, w, h) {
  for (let y = 0; y < h; y++) {
    for (let a = y * w * 4, b = a + (w - 1) * 4; a < b; a += 4, b -= 4) {
      for (let c = 0; c < 4; c++) { const t = rgba[a + c]; rgba[a + c] = rgba[b + c]; rgba[b + c] = t; }
    }
  }
  return rgba;
}

/* ---- composing (browser only) ---------------------------------------------- */

const loaded = new Map();

/* The theme's font, fetched once. The site allows fonts only from itself. */
export async function loadFont(key) {
  const f = FONTS[key] || FONTS.vt323;
  if (!loaded.has(f.family)) {
    const face = new FontFace(f.family, `url(${f.url})`);
    loaded.set(f.family, face.load().then((ff) => { document.fonts.add(ff); return ff; }));
  }
  await loaded.get(f.family);
  return f;
}

/* The finished strip: { bits, h }, packed for encodePNG(bits, W, h).
   `shots` are four images the canvas can draw (video frames, bitmaps),
   cropped to the window's shape about their middle. */
export async function compose(theme, shots, date = new Date()) {
  const L = layout(theme);
  const font = await loadFont(theme.font);
  await loadFont('vt323');

  const c = document.createElement('canvas');
  c.width = W; c.height = L.h;
  const ctx = c.getContext('2d', { willReadFrequently: true });
  ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, W, L.h);

  const paint = (rects) => {
    for (const [x, y, w, h, white] of rects) {
      ctx.fillStyle = white ? '#fff' : '#000';
      ctx.fillRect(x, y, w, h);
    }
  };
  paint(frameRects(theme.frame, L));

  ctx.fillStyle = '#000';
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  const mid = L.content.x + L.content.w / 2;
  fitText(ctx, theme.title, font.family, L.title.size, L.content.w - 8, mid, L.title.y + L.title.h / 2);
  if (L.subtitle) fitText(ctx, theme.subtitle, font.family, L.subtitle.size, L.content.w - 8, mid, L.subtitle.y + L.subtitle.h / 2);
  if (L.icons) L.icons.names.forEach((n, i) => paint(iconRects(n, L.icons.xs[i], L.icons.y)));

  const when = date.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' }) +
    '  ' + date.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  ctx.font = `22px "${FONTS.vt323.family}"`;
  ctx.fillText(when, mid, L.footer.y + 12);
  ctx.font = `18px "${FONTS.vt323.family}"`;
  ctx.fillText('photobooth.nilanet.live', mid, L.footer.y + 36);

  // Everything so far is black or white bar antialiased text edges, so a
  // threshold, not a dither.
  const px = ctx.getImageData(0, 0, W, L.h).data, stride = W >> 3;
  const bits = new Uint8Array(stride * L.h);
  for (let i = 0; i < W * L.h; i++) {
    if (px[i * 4] >= 128) bits[(i / W | 0) * stride + ((i % W) >> 3)] |= 0x80 >> (i % W & 7);
  }

  // Then the photos, dithered with the calibrated defaults, straight in, with
  // the darkest tones lifted to PHOTO_INK (see lift in photo.js).
  L.windows.forEach((win, i) => {
    const shot = shots[i];
    if (!shot) return;
    const sw0 = shot.videoWidth || shot.width, sh0 = shot.videoHeight || shot.height;
    const want = win.w / win.h;
    let sw = sw0, sh = sh0;
    if (sw / sh > want) sw = sh * want; else sh = sw / want;
    const rgba = mirror(shrink(shot, (sw0 - sw) / 2, (sh0 - sh) / 2, sw, sh, win.w, win.h), win.w, win.h);
    blit(bits, W, dither(rgba, win.w, win.h, { maxInk: PHOTO_INK }), win.w, win.h, win.x, win.y);
  });

  return { bits, h: L.h };
}

/* Text at `size`, shrunk until it fits `maxW`. */
function fitText(ctx, text, family, size, maxW, x, y) {
  let s = size;
  do {
    ctx.font = `${s}px "${family}"`;
    if (ctx.measureText(text).width <= maxW) break;
    s -= 2;
  } while (s > 12);
  ctx.fillText(text, x, y);
}

/* Four stand-in photos for previews where there's no camera: soft shapes
   with a face-ish blob, so tone and frame can be judged. */
export function sampleShots() {
  return [0, 1, 2, 3].map((i) => {
    const c = document.createElement('canvas');
    c.width = 640; c.height = 480;
    const x = c.getContext('2d');
    const g = x.createLinearGradient(0, 0, 640, 480);
    g.addColorStop(0, ['#6b8fb5', '#b56b8f', '#8fb56b', '#b5a36b'][i]);
    g.addColorStop(1, '#1d1d24');
    x.fillStyle = g; x.fillRect(0, 0, 640, 480);
    x.fillStyle = '#e8c4a0';
    x.beginPath(); x.ellipse(320 + (i - 1.5) * 40, 230, 110, 140, 0, 0, Math.PI * 2); x.fill();
    x.fillStyle = '#2a1d16';
    x.beginPath(); x.ellipse(320 + (i - 1.5) * 40, 130, 120, 60, 0, Math.PI, Math.PI * 2); x.fill();
    return c;
  });
}
