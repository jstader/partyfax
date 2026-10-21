/* An event's wall: every strip printed there, newest first, new ones
   appearing as they print. On a computer a grid; on a phone a deck, one
   whole strip at a time, swiped through. Tapping one opens it on its own
   page (/strip). Only for guests who have the event's code. */

import { savedCode, join, strips, stripBlob } from './js/api.js?v=4';
import { Deck } from './js/deck.js?v=1';

const POLL_MS = 10_000;
const $ = (id) => document.getElementById(id);
const phone = matchMedia('(max-width: 700px)');
const page = (id) => `/strip#${encodeURIComponent(id)}`;

const items = [];   // { id, url }, newest first
const known = new Set();
const deck = new Deck($('deck'), {
  href: page,
  onPos: (i, n) => { $('pos').textContent = n ? `${i + 1} of ${n}` : ''; },
});
$('prev').addEventListener('click', () => deck.go(-1));
$('next').addEventListener('click', () => deck.go(1));

function layout() {
  $('strips').hidden = phone.matches;
  $('deck-view').hidden = !phone.matches || !items.length;
}
phone.addEventListener('change', layout);

async function refresh(code) {
  let list;
  try {
    list = await strips(code);
  } catch (e) {
    $('note').textContent = e.status === 403 ? "This event's wall is turned off." : `Couldn't load the strips: ${e.message}`;
    return false;
  }

  $('note').textContent = list.length ? '' : 'No strips yet. Go take some!';

  // Pictures for any we haven't seen, all fetched before showing, so the
  // grid and the deck add them in order, newest on top.
  const fresh = list.filter((s) => !known.has(s.id));
  const loaded = await Promise.all(fresh.map((s) => stripBlob(code, s.id)
    .then((b) => ({ id: s.id, time: s.time, url: URL.createObjectURL(b) }))
    .catch(() => null)));

  for (const it of loaded.filter(Boolean).reverse()) {
    known.add(it.id);
    items.unshift(it);

    const li = document.createElement('li');
    const a = Object.assign(document.createElement('a'), { href: page(it.id) });
    const img = Object.assign(document.createElement('img'), {
      src: it.url, width: 384, alt: `Photo strip, ${new Date(it.time).toLocaleString()}`,
    });
    a.append(img);
    li.append(a);
    $('strips').prepend(li);
  }

  if (loaded.length) deck.set(items.slice());
  layout();
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
  }, (e) => {
    $('note').textContent = `Couldn't check the event code: ${e.message}`;
  });
}
