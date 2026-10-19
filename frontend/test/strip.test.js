import { test } from 'node:test';
import assert from 'node:assert/strict';
import { W, MAX_ROWS, SHOTS, FRAMES, FONTS, MAX_ICONS, ICONS, layout, frameRects, iconRects, blit, mirror } from '../booth/js/strip.js';

const ICON_NAMES = Object.keys(ICONS);
const themes = function* () {
  for (const frame of FRAMES) {
    for (const font of Object.keys(FONTS)) {
      for (let n = 0; n <= MAX_ICONS; n++) {
        for (const subtitle of ['', 'Saturday 18 October']) {
          yield { title: 'A party', subtitle, font, frame, icons: ICON_NAMES.slice(0, n) };
        }
      }
    }
  }
};

test('every theme fits the paper and the server limit', () => {
  for (const t of themes()) {
    const L = layout(t);
    assert.equal(L.w, W);
    assert.ok(L.h <= MAX_ROWS, `${JSON.stringify(t)} is ${L.h} rows`);
    assert.equal(L.windows.length, SHOTS);
  }
});

test('windows are 4:3, inside the frame, and overlap nothing', () => {
  for (const t of themes()) {
    const L = layout(t);
    const head = (L.icons ? L.icons.y + L.icons.h : L.subtitle ? L.subtitle.y + L.subtitle.h : L.title.y + L.title.h);
    let prev = head;
    for (const w of L.windows) {
      assert.ok(Math.abs(w.w / w.h - 4 / 3) < 0.02, `window ${w.w}x${w.h}`);
      assert.ok(w.x >= L.side && w.x + w.w <= W - L.side, 'window inside the frame');
      assert.ok(w.y >= prev, `window at ${w.y} overlaps what's above (${prev})`);
      prev = w.y + w.h;
    }
    assert.ok(L.footer.y >= prev, 'footer below the last photo');
    assert.ok(L.footer.y + L.footer.h <= L.h, 'footer on the paper');
  }
});

test('icons sit inside the content width', () => {
  const t = { title: 'x', font: 'vt323', frame: 'film', icons: ICON_NAMES.slice(0, MAX_ICONS) };
  const L = layout(t);
  for (const x of L.icons.xs) assert.ok(x >= L.content.x && x + 32 <= L.content.x + L.content.w, `icon at ${x}`);
});

test('every icon is 16 x 16 and not blank', () => {
  for (const [name, rows] of Object.entries(ICONS)) {
    assert.equal(rows.length, 16, `${name} rows`);
    for (const r of rows) assert.equal(r.length, 16, `${name} row "${r}"`);
    assert.ok(rows.some((r) => r.includes('#')), `${name} is blank`);
  }
});

test('icon rectangles cover exactly the black pixels, at scale', () => {
  for (const name of ICON_NAMES) {
    const area = iconRects(name, 0, 0, 2).reduce((a, [, , w, h]) => a + w * h, 0);
    const dots = ICONS[name].join('').split('').filter((c) => c === '#').length;
    assert.equal(area, dots * 4, name);
  }
});

test('every frame draws only on the paper and never over a photo', () => {
  for (const frame of FRAMES) {
    const L = layout({ title: 'x', font: 'vt323', frame, icons: [] });
    for (const [x, y, w, h] of frameRects(frame, L)) {
      assert.ok(x >= 0 && y >= 0 && x + w <= W && y + h <= L.h, `${frame} rect off the paper`);
      for (const win of L.windows) {
        const overlaps = x < win.x + win.w && x + w > win.x && y < win.y + win.h && y + h > win.y;
        assert.ok(!overlaps, `${frame} draws over a photo`);
      }
    }
  }
});

test('the line frame borders all four sides', () => {
  const L = layout({ title: 'x', font: 'vt323', frame: 'line', icons: [] });
  const rects = frameRects('line', L);
  const covers = (px, py) => rects.some(([x, y, w, h]) => px >= x && px < x + w && py >= y && py < y + h);
  for (const [px, py] of [[W / 2, 7], [W / 2, L.h - 8], [7, L.h / 2], [W - 8, L.h / 2]]) {
    assert.ok(covers(px, py), `no border at ${px},${py}`);
  }
});

test('blit copies bits, at any alignment, and clears where the source is white', () => {
  const dstW = 24, dst = new Uint8Array(3 * 4).fill(0xff);
  const src = new Uint8Array([0b10100000, 0b01000000]); // 3 x 2
  blit(dst, dstW, src, 3, 2, 9, 1);
  const at = (x, y) => (dst[y * 3 + (x >> 3)] & (0x80 >> (x & 7))) ? 1 : 0;
  assert.deepEqual([at(9, 1), at(10, 1), at(11, 1)], [1, 0, 1]);
  assert.deepEqual([at(9, 2), at(10, 2), at(11, 2)], [0, 1, 0]);
  assert.equal(at(8, 1), 1, 'neighbours untouched');
  assert.equal(at(12, 1), 1, 'neighbours untouched');
});

test('mirror flips each row and twice is the original', () => {
  const px = Uint8ClampedArray.from({ length: 3 * 2 * 4 }, (_, i) => i);
  const once = mirror(px.slice(), 3, 2);
  assert.deepEqual([...once.slice(0, 4)], [8, 9, 10, 11]);
  assert.deepEqual([...mirror(once, 3, 2)], [...px]);
});
