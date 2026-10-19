/* Shrinking a phone photo to the paper. One drawImage from 4000 pixels to
   384 at the browser's default smoothing samples a fraction of the source and
   leaves jagged edges and moire for the dither to amplify. Halving in steps,
   each at high quality, averages every source pixel in. */

/* The rectangle sx, sy, sw, sh of `source`, scaled to `w` x `h`, as RGBA. */
export function shrink(source, sx, sy, sw, sh, w, h) {
  let src = source, cy = sy, ch = sh;

  while (sw / 2 >= w * 1.5) {
    const half = canvas(Math.round(sw / 2), Math.round(ch / 2));
    half.getContext('2d').drawImage(src, sx, cy, sw, ch, 0, 0, half.width, half.height);
    src = half; sx = 0; cy = 0; sw = half.width; ch = half.height;
  }

  const out = canvas(w, h);
  const ctx = out.getContext('2d', { willReadFrequently: true });
  ctx.drawImage(src, sx, cy, sw, ch, 0, 0, w, h);
  return ctx.getImageData(0, 0, w, h).data;
}

function canvas(w, h) {
  const c = document.createElement('canvas');
  c.width = w; c.height = h;
  const ctx = c.getContext('2d');
  ctx.imageSmoothingEnabled = true;
  ctx.imageSmoothingQuality = 'high';
  return c;
}

/* A picture file as something drawImage takes. createImageBitmap honours the
   camera's orientation tag and is fast, but some browsers that have it
   cannot decode every format they show -- an iPhone's HEIC -- so a failure
   falls back to an <img>, which can. */
export async function decode(blob) {
  if (window.createImageBitmap) {
    try { return await createImageBitmap(blob); } catch { /* try an <img> */ }
  }
  return new Promise((ok, bad) => {
    const url = URL.createObjectURL(blob), img = new Image();
    img.onload = () => { URL.revokeObjectURL(url); ok(img); };
    img.onerror = () => { URL.revokeObjectURL(url); bad(new Error('decode')); };
    img.src = url;
  });
}
