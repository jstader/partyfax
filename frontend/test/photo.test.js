import { test } from 'node:test';
import assert from 'node:assert/strict';
import { inflateSync } from 'node:zlib';
import { fit, dither, unpack, encodePNG, crc32, tone, unsharp, levels } from '../js/photo.js';

test('crc32 matches the standard check value', () => {
  assert.equal(crc32(new TextEncoder().encode('123456789')), 0xcbf43926);
});

test('fit scales to the width and crops a tall picture to a square', () => {
  assert.deepEqual(fit(768, 384, 384, 384), { sy: 0, sh: 384, h: 192 });
  assert.deepEqual(fit(384, 1000, 384, 384), { sy: 308, sh: 384, h: 384 });
});

// Reads the PNG back with a real inflater, so a wrong length, checksum or
// stored-block header fails here rather than on the server.
test('encodePNG writes a one-bit PNG that inflates to the same rows', async () => {
  const w = 20, h = 3, stride = 3;
  const bits = Uint8Array.from({ length: stride * h }, (_, i) => (i * 37) & 0xff);
  const png = new Uint8Array(await encodePNG(bits, w, h).arrayBuffer());

  assert.deepEqual([...png.subarray(0, 8)], [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

  const view = new DataView(png.buffer);
  const chunks = {};
  for (let o = 8; o < png.length;) {
    const len = view.getUint32(o), type = String.fromCharCode(...png.subarray(o + 4, o + 8));
    const body = png.subarray(o + 4, o + 8 + len);
    assert.equal(view.getUint32(o + 8 + len), crc32(body), `${type} crc`);
    chunks[type] = png.subarray(o + 8, o + 8 + len);
    o += 12 + len;
  }

  const ihdr = new DataView(chunks.IHDR.buffer, chunks.IHDR.byteOffset);
  assert.equal(ihdr.getUint32(0), w);
  assert.equal(ihdr.getUint32(4), h);
  assert.equal(chunks.IHDR[8], 1, 'bit depth');

  const raw = inflateSync(chunks.IDAT);
  for (let y = 0; y < h; y++) {
    assert.equal(raw[y * (stride + 1)], 0, 'filter byte');
    assert.deepEqual([...raw.subarray(y * (stride + 1) + 1, (y + 1) * (stride + 1))],
      [...bits.subarray(y * stride, (y + 1) * stride)]);
  }
});

test('dither keeps a flat picture\'s tone', () => {
  const w = 16, h = 2;
  const flat = (v) => new Uint8ClampedArray(w * h * 4).fill(v);
  assert.ok(dither(flat(255), w, h).every(b => b === 0xff), 'white stays white');
  assert.ok(dither(flat(0), w, h).every(b => b === 0), 'black stays black');
});

test('unpack is the inverse of the packing', () => {
  const rgba = unpack(new Uint8Array([0b10100000]), 3, 1, new Uint8ClampedArray(12));
  assert.deepEqual([...rgba], [255, 255, 255, 255, 0, 0, 0, 255, 255, 255, 255, 255]);
});

const ones = (bits) => bits.reduce((n, b) => { for (; b; b &= b - 1) n++; return n; }, 0);
const grey = (w, h, f) => {
  const px = new Uint8ClampedArray(w * h * 4);
  for (let i = 0; i < w * h; i++) { px.fill(f(i % w, i / w | 0), i * 4, i * 4 + 3); px[i * 4 + 3] = 255; }
  return px;
};

test('every algorithm keeps flat white white and flat black black', () => {
  const w = 16, h = 4;
  for (const opts of [{ algorithm: 'fs', serpentine: true }, { algorithm: 'atkinson' }, { algorithm: 'atkinson', gamma: 2, sharpen: 1 }]) {
    assert.ok(dither(grey(w, h, () => 255), w, h, opts).every(b => b === 0xff), `white, ${JSON.stringify(opts)}`);
    assert.ok(dither(grey(w, h, () => 0), w, h, opts).every(b => b === 0), `black, ${JSON.stringify(opts)}`);
  }
});

// A ramp, so levels has a range and leaves it alone: about half the dots
// white, and a tone curve above 1 makes it lighter, never darker.
test('a ramp dithers to about half white, and gamma lightens it', () => {
  const w = 256, h = 32, px = grey(w, h, (x) => x);
  const plain = ones(dither(px.slice(), w, h, { algorithm: 'fs', gamma: 1, sharpen: 0 })) / (w * h);
  const atk = ones(dither(px.slice(), w, h, { algorithm: 'atkinson', gamma: 1, sharpen: 0 })) / (w * h);
  const light = ones(dither(px.slice(), w, h, { algorithm: 'atkinson', gamma: 1.8, sharpen: 0 })) / (w * h);
  assert.ok(Math.abs(plain - 0.5) < 0.03, `fs ${plain}`);
  assert.ok(Math.abs(atk - 0.5) < 0.06, `atkinson ${atk}`);
  assert.ok(light > atk + 0.05, `gamma 1.8 ${light} vs ${atk}`);
});

test('tone keeps the ends and lifts the middle', () => {
  const lum = Float32Array.from([0, 128, 255]);
  tone(lum, 2);
  assert.equal(lum[0], 0);
  assert.ok(lum[1] > 170 && lum[1] < 190, `middle ${lum[1]}`);
  assert.equal(lum[2], 255);
});

test('unsharp leaves a flat picture alone and steepens an edge', () => {
  const flat = new Float32Array(9).fill(100);
  unsharp(flat, 3, 3, 1);
  assert.ok(flat.every(v => Math.abs(v - 100) < 1e-4));

  const edge = Float32Array.from([50, 50, 200, 200, 50, 50, 200, 200]);
  unsharp(edge, 4, 2, 1);
  assert.ok(edge[1] < 50 && edge[2] > 200, `edge ${[...edge.slice(0, 4)]}`);
});

test('levels stretches the range to black and white', () => {
  const lum = Float32Array.from({ length: 1000 }, (_, i) => 100 + (i % 51));
  levels(lum);
  assert.ok(Math.min(...lum) === 0 && Math.max(...lum) === 255);
});
