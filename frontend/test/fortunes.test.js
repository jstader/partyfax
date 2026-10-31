import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { FORTUNES, LESSONS, MAX_FORTUNE, luckyNumbers, pick } from '../fortunes/js/fortunes.js';
import { W, SYMBOLS, BORDER_H, borderRects, symbolRects } from '../fortunes/js/slip.js';

const fontChars = new Set(readFileSync(new URL('../fonts/NotoSansSC-fortunes.txt', import.meta.url), 'utf8').replace(/\n$/, ''));

test('every fortune is short enough for the slip', () => {
  for (const f of FORTUNES) assert.ok(f.length <= MAX_FORTUNE, `${f.length} characters: ${f}`);
  assert.equal(new Set(FORTUNES).size, FORTUNES.length, 'a fortune is in twice');
});

test('every lesson is complete, and Chinese where it should be', () => {
  for (const l of LESSONS) {
    assert.ok(l.zh && l.pinyin && l.en, JSON.stringify(l));
    assert.match(l.zh, /^\p{Script=Han}+$/u, `${l.zh} is not all Chinese characters`);
    assert.ok(l.zh.length <= 6, `${l.zh} is too long for one line`);
  }
  assert.equal(new Set(LESSONS.map((l) => l.zh)).size, LESSONS.length, 'a lesson is in twice');
});

test('the trimmed font has every character the slips print', () => {
  const texts = [...FORTUNES, ...LESSONS.flatMap((l) => [l.zh, l.pinyin, l.en, l.note || ''])];
  for (const s of texts) {
    for (const c of s) assert.ok(fontChars.has(c), `"${c}" in "${s}" is not in the font (fonts/README.md)`);
  }
  for (let n = 0; n <= 9; n++) assert.ok(fontChars.has(String(n)));
});

test('lucky numbers are six different ones from 1 to 49, smallest first', () => {
  let seed = 1;
  const random = () => ((seed = (seed * 16807) % 2147483647) - 1) / 2147483646;
  for (let i = 0; i < 500; i++) {
    const n = luckyNumbers(random);
    assert.equal(n.length, 6);
    assert.equal(new Set(n).size, 6);
    assert.ok(n.every((x) => Number.isInteger(x) && x >= 1 && x <= 49), n.join());
    assert.deepEqual(n, [...n].sort((a, b) => a - b));
  }
  // The extremes are reachable.
  const ends = [0, 0.99999, 0.1, 0.2, 0.3, 0.4];
  let i = 0;
  assert.deepEqual(luckyNumbers(() => ends[i++ % ends.length]), [1, 5, 10, 15, 20, 49]);
});

test('pick takes from the lists', () => {
  const s = pick();
  assert.ok(FORTUNES.includes(s.fortune));
  assert.ok(LESSONS.includes(s.lesson));
  assert.equal(s.numbers.length, 6);
});

test('the borders are on the paper and light on ink', () => {
  for (const [y, inner] of [[14, 'below'], [500, 'above']]) {
    const rects = borderRects(y, inner);
    let ink = 0;
    for (const [x, ry, w, h] of rects) {
      assert.ok(x >= 0 && x + w <= W, `x ${x}..${x + w} is off the paper`);
      assert.ok(ry >= y - 7 && ry + h <= y + BORDER_H + 7, `y ${ry} is outside the border`);
      ink += w * h;
    }
    // Long stretches of dark rows knock the printer off USB (AGENTS.md).
    // A one-dot dashed rule is fine; a border of mostly black is not.
    const share = ink / (W * BORDER_H);
    assert.ok(share < 0.25, `the border is ${(100 * share).toFixed(0)}% ink`);
  }
});

test('every symbol is a clean grid', () => {
  for (const [name, rows] of Object.entries(SYMBOLS)) {
    assert.ok(rows.every((r) => r.length === rows[0].length), `${name} rows differ in width`);
    assert.ok(rows.every((r) => /^[.#]+$/.test(r)), `${name} has a stray character`);
    assert.ok(symbolRects(name, 0, 0).length > 0);
  }
});
