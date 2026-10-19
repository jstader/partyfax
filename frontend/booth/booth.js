/* The photobooth: a code, a countdown, four photos from the front camera,
   one strip on paper. */

import { compose } from './js/strip.js?v=1';
import { savedCode, saveCode, forgetCode, join, printStrip } from './js/api.js?v=1';
import { unpack, encodePNG } from '../js/photo.js?v=4';

const $ = (id) => document.getElementById(id);

const FIRST_COUNT = 5;   // seconds before the first photo
const NEXT_COUNT = 3;    // and between the rest
const SHOTS = 4;
const BUSY_RETRY_MS = 3000;
const BUSY_GIVE_UP_MS = 90_000;

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
}

/* ---- the code ------------------------------------------------------------------- */

async function enterCode(typed) {
  $('code-error').textContent = '';
  let t;
  try {
    t = await join(typed);
  } catch (e) {
    $('code-error').textContent = `Couldn't check that code: ${e.message}`;
    return false;
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

$('leave').addEventListener('click', () => {
  forgetCode();
  code = ''; theme = null;
  $('code').value = '';
  show('code');
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
    await countdown(i === 0 ? FIRST_COUNT : NEXT_COUNT, `${i + 1} of ${SHOTS}`);
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

function thumb(shot) {
  const c = document.createElement('canvas');
  c.width = 120; c.height = 90;
  const ctx = c.getContext('2d');
  // Mirrored, as the strip will be.
  ctx.translate(120, 0); ctx.scale(-1, 1);
  ctx.drawImage(shot, 0, 0, 120, 90);
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

function draw({ bits, h }) {
  const c = $('preview');
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
  const started = Date.now();

  for (;;) {
    status.textContent = 'Printing...';
    try {
      await printStrip(code, encodePNG(strip.bits, 384, strip.h));
      show('done');
      return;
    } catch (e) {
      if (e.status === 409 && /busy/i.test(e.message) && Date.now() - started < BUSY_GIVE_UP_MS) {
        status.textContent = 'Someone else is printing. Waiting for the printer...';
        await sleep(BUSY_RETRY_MS);
        continue;
      }
      if (e.status === 404) {
        status.textContent = 'This event has closed.';
        forgetCode();
        return;
      }
      if (e.status === 429) {
        status.textContent = "That's a lot of strips! Wait a minute and try again.";
      } else {
        status.textContent = `Didn't print: ${e.message}`;
      }
      $('print').disabled = false;
      return;
    }
  }
});

/* ---- start ------------------------------------------------------------------------- */

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const remembered = savedCode();
if (remembered) {
  enterCode(remembered).then((ok) => { if (!ok) { forgetCode(); $('code-error').textContent = ''; show('code'); } });
} else {
  show('code');
}
