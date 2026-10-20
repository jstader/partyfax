/* An event's wall: every strip printed there, newest first, new ones
   appearing as they print. Tapping one opens it on its own page (/strip).
   Only for guests who have the event's code. */

import { savedCode, join, strips, stripBlob } from './js/api.js?v=3';

const POLL_MS = 10_000;
const $ = (id) => document.getElementById(id);
const shown = new Set();

async function refresh(code) {
  let list;
  try {
    list = await strips(code);
  } catch (e) {
    $('note').textContent = e.status === 403 ? "This event's wall is turned off." : `Couldn't load the strips: ${e.message}`;
    return false;
  }

  $('note').textContent = list.length ? '' : 'No strips yet. Go take some!';

  // Newest first: walk oldest to newest, putting each new one on top.
  for (const s of list.slice().reverse()) {
    if (shown.has(s.id)) continue;
    shown.add(s.id);
    const li = document.createElement('li');
    const a = Object.assign(document.createElement('a'), { href: `/strip#${encodeURIComponent(s.id)}` });
    const img = document.createElement('img');
    img.alt = `Photo strip, ${new Date(s.time).toLocaleString()}`;
    img.width = 384;
    a.append(img);
    li.append(a);
    $('strips').prepend(li);
    stripBlob(code, s.id).then((b) => { img.src = URL.createObjectURL(b); }).catch(() => li.remove());
  }
  return true;
}

const code = savedCode();
if (!code) {
  location.replace('/');
} else {
  join(code).then(async (theme) => {
    if (!theme) { location.replace('/'); return; }
    $('title').textContent = theme.title;
    document.title = `${theme.title} - photobooth`;
    if (await refresh(code)) setInterval(() => refresh(code), POLL_MS);
  });
}
