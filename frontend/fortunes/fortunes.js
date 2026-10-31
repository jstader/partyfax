/* The fortunes page: crack a cookie, see its slip, print it. The slip is
   composed here (js/slip.js) and sent as a picture, so what prints is what
   was on the screen. */

import { pick } from './js/fortunes.js';
import { compose, symbolRects, SYMBOLS, W } from './js/slip.js';
import { unpack, encodePNG } from '../js/photo.js';
import { whileBusy } from '../js/busy.js';

const $ = (id) => document.getElementById(id);
const status = $('status');

let current = null;   // { slip, png } on show

/* The closed cookie, from the border's own drawing, large. */
function drawCookie() {
  const c = $('cookie'), rows = SYMBOLS.cookie;
  // symbolRects draws at 2x, so the canvas is twice the pattern.
  c.width = rows[0].length * 2; c.height = rows.length * 2;
  const ctx = c.getContext('2d');
  ctx.fillStyle = getComputedStyle(document.body).color;
  for (const [x, y, w, h] of symbolRects('cookie', 0, 0)) ctx.fillRect(x, y, w, h);
}

function say(text, isError = false) {
  status.textContent = text;
  status.classList.toggle('error', isError);
}

async function crack() {
  $('crack').disabled = $('another').disabled = true;
  say('');
  try {
    const slip = pick();
    const { bits, h } = await compose(slip);

    const c = $('slip');
    c.width = W; c.height = h;
    const ctx = c.getContext('2d');
    const img = ctx.createImageData(W, h);
    unpack(bits, W, h, img.data);
    ctx.putImageData(img, 0, 0);

    current = { slip, png: encodePNG(bits, W, h) };
    $('fortune-text').textContent = `${slip.fortune} Lucky numbers ${slip.numbers.join(', ')}. ` +
      `Learn Chinese: ${slip.lesson.zh}, ${slip.lesson.pinyin}, ${slip.lesson.en}.`;

    $('cookie').hidden = true;
    // Shown again so the unroll animation plays for every cookie.
    c.hidden = true; void c.offsetWidth; c.hidden = false;
    $('crack').hidden = true;
    $('opened').hidden = false;
    $('print').disabled = false;
  } catch (e) {
    say(`Couldn't open that one: ${e.message}`, true);
  } finally {
    $('crack').disabled = $('another').disabled = false;
  }
}

async function print() {
  if (!current) return;
  const btn = $('print');
  btn.disabled = $('another').disabled = true;
  try {
    await whileBusy(
      () => { say('Printing...'); return send(current.png); },
      () => say('Someone else is printing. Waiting for the printer...'),
    );
    say('Printed! Grab your fortune from the printer.');
  } catch (e) {
    say(`Didn't print: ${e.message}`, true);
    btn.disabled = false;
  } finally {
    $('another').disabled = false;
  }
}

/* Resolves once the slip is on paper; throws with the server's reason and
   .status otherwise. The server gives up by 75 seconds at worst, so past 90
   the connection is taken for dead. */
async function send(png) {
  const res = await fetch('/api/fortune', {
    method: 'POST',
    headers: { 'Content-Type': 'image/png' },
    body: png,
    signal: AbortSignal.timeout(90000),
  });
  if (res.ok) return res.json();
  let msg = `error ${res.status}`;
  try { msg = (await res.json()).error || msg; } catch { /* not JSON: keep the status */ }
  const err = new Error(msg);
  err.status = res.status;
  throw err;
}

drawCookie();
$('crack').addEventListener('click', crack);
$('another').addEventListener('click', crack);
$('print').addEventListener('click', print);
