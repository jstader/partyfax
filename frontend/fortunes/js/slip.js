/* A fortune cookie slip: a border of cookies and lucky coins, the fortune,
   the lucky numbers, a Chinese lesson, and the border again. Composed here,
   in the browser, so the preview is exactly the dots the printer gets -- the
   same rule as the main site and the photobooth.

   Mostly outlines and text, on purpose: dark slips are what knocked the
   printer off USB (see AGENTS.md), and a slip of thin lines is nowhere near
   that. */

export const W = 384;          // dots across the paper
export const MAX_ROWS = 800;   // the server's limit for a slip (doc.FortuneMaxRows)

/* ---- fonts ------------------------------------------------------------------ */

export const FONTS = {
  // Noto Sans SC, OFL, trimmed to the characters the slips use and the
  // weights 500 to 700 (fonts/README.md). Bundled rather than left to the
  // phone: not every device has a Chinese font, and every phone drawing the
  // same one keeps the preview true.
  text: { family: 'Noto Sans SC Fortunes', url: '/fonts/NotoSansSC-fortunes.woff2', weight: '500 700' },
  // The main site's pixel font, for the small labels.
  pixel: { family: 'VT323', url: '/fonts/VT323.woff2' },
};

const loaded = new Map();

export async function loadFonts() {
  await Promise.all(Object.values(FONTS).map((f) => {
    if (!loaded.has(f.family)) {
      const face = new FontFace(f.family, `url(${f.url})`, f.weight ? { weight: f.weight } : {});
      loaded.set(f.family, face.load().then((ff) => { document.fonts.add(ff); return ff; }));
    }
    return loaded.get(f.family);
  }));
}

/* ---- the border's symbols ------------------------------------------------------ */

/* '#' black. Drawn at 2x. */
export const SYMBOLS = {
  // A folded cookie with its slip of paper hanging out.
  cookie: [
    '......######...........',
    '....##......##.........',
    '...#..........#........',
    '..#.....##.....#.......',
    '.#.....#..#.....#......',
    '.#....#....#....#......',
    '#....#......#....#.....',
    '#...#........#...#.....',
    '#..#..........#..#.....',
    '.##....########.##.....',
    '.......#..............#',
    '.......#.###.####.....#',
    '.......#..............#',
    '.......################',
  ],
  // A lucky coin with its square hole.
  coin: [
    '...####...',
    '.##....##.',
    '.#......#.',
    '#..####..#',
    '#..#..#..#',
    '#..#..#..#',
    '#..####..#',
    '.#......#.',
    '.##....##.',
    '...####...',
  ],
};

const SCALE = 2;
const sizeOf = (name) => ({ w: SYMBOLS[name][0].length * SCALE, h: SYMBOLS[name].length * SCALE });

/* A symbol as rectangles, one per run of black in a row. */
export function symbolRects(name, x, y) {
  const r = [];
  SYMBOLS[name].forEach((row, j) => {
    for (let i = 0; i < row.length;) {
      if (row[i] !== '#') { i++; continue; }
      let k = i;
      while (k < row.length && row[k] === '#') k++;
      r.push([x + i * SCALE, y + j * SCALE, (k - i) * SCALE, SCALE]);
      i = k;
    }
  });
  return r;
}

/* The pattern along the top and bottom: cookies with coins between. */
const PATTERN = ['cookie', 'coin', 'cookie', 'coin', 'cookie', 'coin', 'cookie'];
export const BORDER_H = sizeOf('cookie').h;

/* One border, its top at y, as rectangles: the symbols spread evenly across
   the paper, centred on a common middle line, and a dashed rule along the
   inner side (below the top border, above the bottom one). */
export function borderRects(y, inner) {
  const sizes = PATTERN.map(sizeOf);
  const used = sizes.reduce((s, z) => s + z.w, 0);
  const gap = Math.floor((W - 16 - used) / (PATTERN.length - 1));
  let x = Math.round((W - used - gap * (PATTERN.length - 1)) / 2);

  const r = [];
  PATTERN.forEach((name, i) => {
    r.push(...symbolRects(name, x, y + ((BORDER_H - sizes[i].h) >> 1)));
    x += sizes[i].w + gap;
  });

  const ry = inner === 'below' ? y + BORDER_H + 6 : y - 7;
  for (let dx = 8; dx + 6 <= W - 8; dx += 10) r.push([dx, ry, 6, 1]);
  return r;
}

/* ---- composing (browser only) ------------------------------------------------- */

const PAD = 14;             // above the top border, below the site name
const TEXT_W = W - 40;      // fortune and lesson lines wrap to this

/* The finished slip for `slip` ({ fortune, lesson, numbers }, from
   fortunes.js pick()): { bits, h }, packed for encodePNG(bits, W, h). */
export async function compose(slip) {
  await loadFonts();
  const c = document.createElement('canvas');
  const ctx = c.getContext('2d', { willReadFrequently: true });
  const text = (size, weight = 500) => `${weight} ${size}px "${FONTS.text.family}"`;
  const pixel = (size) => `${size}px "${FONTS.pixel.family}"`;

  // Lines are measured before the canvas is sized, so the slip is as long
  // as its fortune and no longer.
  ctx.font = text(25);
  const fortune = wrap(ctx, slip.fortune, TEXT_W);
  ctx.font = text(19);
  const note = slip.lesson.note ? wrap(ctx, slip.lesson.note, TEXT_W) : [];
  ctx.font = text(64);
  const zhSize = Math.min(64, Math.floor(64 * TEXT_W / ctx.measureText(slip.lesson.zh).width));

  // Top to bottom: each entry is a line of text, or a gap.
  const items = [
    { gap: PAD + BORDER_H + 22 },
    ...fortune.map((t) => ({ t, font: text(25), h: 34 })),
    { gap: 18 }, { rule: true }, { gap: 14 },
    { t: 'LUCKY NUMBERS', font: pixel(22), h: 22 },
    { gap: 4 },
    { t: slip.numbers.join('   '), font: text(26, 700), h: 34 },
    { gap: 12 }, { rule: true }, { gap: 14 },
    { t: 'LEARN CHINESE', font: pixel(22), h: 22 },
    { gap: 4 },
    { t: slip.lesson.zh, font: text(zhSize), h: Math.round(zhSize * 1.25) },
    { t: slip.lesson.pinyin, font: text(24), h: 32 },
    { t: slip.lesson.en, font: text(22, 700), h: 30 },
    ...note.map((t) => ({ t, font: text(19), h: 25 })),
    { gap: 22 + BORDER_H + 8 },
    { t: 'fortunes.nilanet.live', font: pixel(18), h: 18 },
    { gap: PAD },
  ];
  const h = items.reduce((s, it) => s + (it.gap ?? it.h ?? 1), 0);

  c.width = W; c.height = h;
  ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, W, h);
  ctx.fillStyle = '#000';
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';

  const rects = [...borderRects(PAD, 'below')];
  let y = 0;
  for (const it of items) {
    if (it.gap) { y += it.gap; continue; }
    if (it.rule) {
      for (let dx = 40; dx + 6 <= W - 40; dx += 12) rects.push([dx, y, 6, 1]);
      y += 1;
      continue;
    }
    ctx.font = it.font;
    ctx.fillText(it.t, W / 2, y + it.h / 2);
    y += it.h;
  }
  rects.push(...borderRects(h - PAD - 18 - 8 - BORDER_H, 'above'));
  for (const [x, ry, w, rh] of rects) ctx.fillRect(x, ry, w, rh);

  // Black on white, bar the antialiased edges of the letters: a threshold.
  // A set bit is white, as photo.js packs them.
  const px = ctx.getImageData(0, 0, W, h).data, stride = W >> 3;
  const bits = new Uint8Array(stride * h);
  for (let i = 0; i < W * h; i++) {
    if (px[i * 4] >= 128) bits[(i / W | 0) * stride + ((i % W) >> 3)] |= 0x80 >> (i % W & 7);
  }
  return { bits, h };
}

/* Words into lines no wider than maxW at the context's font. */
export function wrap(ctx, s, maxW) {
  const lines = [];
  let line = '';
  for (const word of s.split(/\s+/)) {
    const next = line ? line + ' ' + word : word;
    if (line && ctx.measureText(next).width > maxW) {
      lines.push(line);
      line = word;
    } else {
      line = next;
    }
  }
  if (line) lines.push(line);
  return lines;
}
