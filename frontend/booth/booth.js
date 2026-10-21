/* The photobooth: a code, a countdown, four photos from the front camera,
   one strip on paper. */

import { compose, iconRects, loadFont, sampleShots } from './js/strip.js?v=2';
import { savedCode, saveCode, forgetCode, join, printStrip, whileBusy } from './js/api.js?v=4';
import { unpack, encodePNG } from '../js/photo.js?v=4';

const $ = (id) => document.getElementById(id);

// Seconds before the first photo and between the rest, if the event
// doesn't say (the host sets them per event in the admin page).
const FIRST_COUNT = 5;
const NEXT_COUNT = 3;
const SHOTS = 4;

let code = '';
let theme = null;
let stream = null;
let strip = null; // { bits, h } once composed

/* ---- screens ---------------------------------------------------------------- */

function show(name) {
  for (const s of document.querySelectorAll('.screen')) s.hidden = s.id !== `screen-${name}`;
}

function setTheme(t) {
  theme = t;
  $('event-title').textContent = t.title;
  $('event-subtitle').textContent = t.subtitle || '';
  for (const id of ['wall-link', 'wall-link-done']) $(id).hidden = !t.wall;

  // The event's frame style dresses the welcome card; its font sets the title.
  $('hero').className = `hero frame-${t.frame}`;
  loadFont(t.font).then((f) => { $('event-title').style.fontFamily = `"${f.family}", system-ui, sans-serif`; });
  drawIcons($('hero-icons'), t.icons.length ? t.icons : ['camera', 'sparkle'], 3);

  // What they'll get: the strip with stand-in photos, in this event's theme.
  compose(t, sampleShots(), new Date()).then((s) => draw(s, $('sample')));
}

/* Pixel-art icons in a row on a canvas, light on the dark page. */
function drawIcons(canvas, names, scale) {
  const size = 16 * scale, gap = 6 * scale;
  canvas.width = names.length * (size + gap) - gap;
  canvas.height = size;
  const ctx = canvas.getContext('2d');
  ctx.fillStyle = '#f4f3ef';
  names.forEach((n, i) => {
    for (const [x, y, w, h] of iconRects(n, i * (size + gap), 0, scale)) ctx.fillRect(x, y, w, h);
  });
}

/* ---- the code ------------------------------------------------------------------- */

/* True if the code opens an event, false if it doesn't, null if it
   couldn't be checked (offline, or too many tries): then it may still be
   good. */
async function enterCode(typed) {
  $('code-error').textContent = '';
  let t;
  try {
    t = await join(typed);
  } catch (e) {
    $('code-error').textContent = `Couldn't check that code: ${e.message}`;
    return null;
  }
  if (!t) {
    $('code-error').textContent = "That code doesn't open an event. Check the sign and try again.";
    return false;
  }
  code = typed;
  saveCode(typed);
  setTheme(t);
  show('ready');
  return true;
}

$('code-form').addEventListener('submit', (e) => {
  e.preventDefault();
  const typed = $('code').value.trim();
  if (typed) enterCode(typed);
});

/* ---- the camera ------------------------------------------------------------------ */

$('enter').addEventListener('click', session);
$('retake').addEventListener('click', session);
$('again').addEventListener('click', () => show('ready'));

async function session() {
  strip = null;
  $('thumbs').replaceChildren();
  $('camera-note').textContent = '';
  $('count').textContent = '';
  show('camera');

  try {
    stream = await navigator.mediaDevices.getUserMedia({
      video: { facingMode: 'user', width: { ideal: 1280 }, height: { ideal: 960 } },
      audio: false,
    });
  } catch (e) {
    show('ready');
    alert(e.name === 'NotAllowedError'
      ? 'The photobooth needs your camera. Allow camera access for this site in your browser settings, then try again.'
      : `Couldn't start the camera: ${e.message}`);
    return;
  }

  const video = $('video');
  video.srcObject = stream;
  await video.play();
  await settle(video);

  const shots = [];
  for (let i = 0; i < SHOTS; i++) {
    await countdown(i === 0 ? theme.countFirst || FIRST_COUNT : theme.countNext || NEXT_COUNT, `${i + 1} of ${SHOTS}`);
    shots.push(snap(video));
    flash();
    thumb(shots[i]);
  }

  stop();
  $('count').textContent = '';
  $('camera-note').textContent = 'Making your strip...';

  strip = await compose(theme, shots, new Date());
  draw(strip);
  $('print-status').textContent = '';
  $('print').disabled = false;
  show('review');
}

/* Wait until the video has real frames: the first few on some phones are
   black while the sensor adjusts. */
function settle(video) {
  return new Promise((ok) => {
    const start = performance.now();
    const check = () => (video.videoWidth && performance.now() - start > 600 ? ok() : requestAnimationFrame(check));
    check();
  });
}

async function countdown(seconds, label) {
  $('camera-note').textContent = label;
  for (let s = seconds; s > 0; s--) {
    $('count').textContent = s;
    await sleep(1000);
  }
  $('count').textContent = '';
}

/* A full-resolution copy of the current frame. compose() crops and shrinks. */
function snap(video) {
  const c = document.createElement('canvas');
  c.width = video.videoWidth; c.height = video.videoHeight;
  c.getContext('2d').drawImage(video, 0, 0);
  return c;
}

function flash() {
  const f = $('flash');
  f.classList.remove('on');
  void f.offsetWidth; // restart the animation
  f.classList.add('on');
}

/* The shot as the strip will show it: the middle of it at 4:3, mirrored.
   Drawing the whole frame into 4:3 squashed a phone camera's taller or
   wider picture; compose() crops, and so must this. */
function thumb(shot) {
  const c = document.createElement('canvas');
  c.width = 120; c.height = 90;
  const ctx = c.getContext('2d');
  let sw = shot.width, sh = shot.height;
  if (sw / sh > 4 / 3) sw = sh * 4 / 3; else sh = sw * 3 / 4;
  ctx.translate(120, 0); ctx.scale(-1, 1);
  ctx.drawImage(shot, (shot.width - sw) / 2, (shot.height - sh) / 2, sw, sh, 0, 0, 120, 90);
  const li = document.createElement('li');
  li.append(c);
  $('thumbs').append(li);
}

function stop() {
  if (stream) for (const t of stream.getTracks()) t.stop();
  stream = null;
}

// Leaving the page or switching apps mid-session releases the camera.
document.addEventListener('visibilitychange', () => {
  if (document.hidden && stream) { stop(); show('ready'); }
});

/* ---- review and print ------------------------------------------------------------ */

function draw({ bits, h }, c = $('preview')) {
  c.width = 384; c.height = h;
  const ctx = c.getContext('2d');
  const img = ctx.createImageData(384, h);
  unpack(bits, 384, h, img.data);
  ctx.putImageData(img, 0, 0);
}

$('print').addEventListener('click', async () => {
  if (!strip) return;
  $('print').disabled = true;
  const status = $('print-status');
  const png = encodePNG(strip.bits, 384, strip.h);

  try {
    await whileBusy(
      () => { status.textContent = 'Printing...'; return printStrip(code, png); },
      () => { status.textContent = 'Someone else is printing. Waiting for the printer...'; },
    );
    show('done');
  } catch (e) {
    if (e.status === 404) {
      status.textContent = 'This event has closed.';
      forgetCode();
      return;
    }
    status.textContent = e.status === 429
      ? "That's a lot of strips! Wait a minute and try again."
      : `Didn't print: ${e.message}`;
    $('print').disabled = false;
  }
});

/* ---- start ------------------------------------------------------------------------- */

drawIcons($('code-art'), ['camera'], 6);

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/* A link or QR code from the host carries the code after the #, which the
   browser never sends to the server, so codes stay out of logs. It wins
   over one remembered from an earlier event. Once used it's taken out of
   the address bar, so a screenshot of the page doesn't show it. */
const linked = decodeURIComponent(location.hash.slice(1)).trim();
if (linked) history.replaceState(null, '', location.pathname);

const start = linked || savedCode();
if (start) {
  enterCode(start).then((ok) => {
    if (ok) return;
    if (ok === null) { show('code'); return; } // keep it for next time
    forgetCode();
    // A bad link says why; a stale remembered code just asks for a new one.
    if (!linked) $('code-error').textContent = '';
    show('code');
  });
} else {
  show('code');
}
