/* The fortunes page: crack a cookie, see its slip, print it. The server
   picks the fortune and draws the slip (backend/internal/fortune), and the
   page sends that same picture back to print, so what prints is what was on
   the screen. */

import { whileBusy } from '../js/busy.js';

/* The closed cookie, from the same drawing as the slip's border: '#' is
   black. */
const COOKIE = [
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
];

const $ = (id) => document.getElementById(id);
const status = $('status');

let current = null;   // the PNG on show

/* The closed cookie, large. */
function drawCookie() {
  const c = $('cookie');
  c.width = COOKIE[0].length; c.height = COOKIE.length;
  const ctx = c.getContext('2d');
  ctx.fillStyle = getComputedStyle(document.body).color;
  COOKIE.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) if (row[x] === '#') ctx.fillRect(x, y, 1, 1);
  });
}

function say(text, isError = false) {
  status.textContent = text;
  status.classList.toggle('error', isError);
}

async function crack() {
  $('crack').disabled = $('another').disabled = true;
  say('');
  try {
    const slip = await request('/api/fortune/slip', { signal: AbortSignal.timeout(20000) });
    const png = Uint8Array.from(atob(slip.png), (ch) => ch.charCodeAt(0));
    const bitmap = await createImageBitmap(new Blob([png], { type: 'image/png' }));

    const c = $('slip');
    c.width = bitmap.width; c.height = bitmap.height;
    c.getContext('2d').drawImage(bitmap, 0, 0);

    current = png;
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
      () => { say('Printing...'); return send(current); },
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
function send(png) {
  return request('/api/fortune', {
    method: 'POST',
    headers: { 'Content-Type': 'image/png' },
    body: png,
    signal: AbortSignal.timeout(90000),
  });
}

/* The reply's JSON, or throws with the server's reason and .status. */
async function request(url, options) {
  const res = await fetch(url, options);
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
