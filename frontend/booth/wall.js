/* An event's wall: every strip printed there, newest first, new ones
   appearing as they print. Only for guests who have the event's code. */

import { savedCode, join, strips, stripURL } from './js/api.js?v=1';

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
    const img = document.createElement('img');
    img.alt = `Photo strip, ${new Date(s.time).toLocaleString()}`;
    img.width = 384;
    li.append(img);
    $('strips').prepend(li);
    stripURL(code, s.id).then((u) => { img.src = u; }).catch(() => li.remove());
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
