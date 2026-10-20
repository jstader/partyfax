/* A swipeable deck of strips, for phones: one strip at a time, whole, and a
   drag throws it off to show the next, like flicking through profiles.
   Left is the next (older) strip, right the previous; a tap opens it. */

const THROW = 90;        // px of drag that commits a swipe
const FLICK = 0.5;       // or this fast, px/ms, however short
const TAP = 8;           // less movement than this is a tap
const OUT_MS = 260;      // the card flying off
const BACK_MS = 220;     // the card springing back

export class Deck {
  /* `root` holds the cards; `onPos(i, n)` hears where we are; `href(id)`
     is where a tap goes. */
  constructor(root, { onPos, href }) {
    this.root = root;
    this.onPos = onPos;
    this.href = href;
    this.items = []; // { id, url }, newest first
    this.i = 0;
    this.busy = false;

    this.top = this.card();
    this.under = this.card('under');
    root.append(this.under, this.top);

    this.top.addEventListener('pointerdown', (e) => this.down(e));
    this.top.addEventListener('pointermove', (e) => this.move(e));
    this.top.addEventListener('pointerup', (e) => this.up(e));
    this.top.addEventListener('pointercancel', () => this.snapBack());
    window.addEventListener('keydown', (e) => {
      if (this.root.offsetParent === null) return; // the deck isn't showing
      if (e.key === 'ArrowLeft') this.go(1);
      if (e.key === 'ArrowRight') this.go(-1);
    });
  }

  card(extra = '') {
    const c = document.createElement('div');
    c.className = `card ${extra}`.trim();
    const img = document.createElement('img');
    img.draggable = false;
    img.alt = 'Photo strip';
    c.append(img);
    return c;
  }

  /* New strips arrive newest first. Ones added in front of where the guest
     is don't move them off the strip they're looking at. */
  set(items) {
    const current = this.items[this.i]?.id;
    this.items = items;
    const at = current ? items.findIndex((x) => x.id === current) : 0;
    this.i = Math.max(0, at);
    this.show();
  }

  show() {
    const it = this.items[this.i];
    this.top.hidden = !it;
    if (it) this.top.firstChild.src = it.url;
    this.top.style.transition = 'none';
    this.top.style.transform = '';
    this.under.hidden = true;
    this.onPos(this.i, this.items.length);
  }

  /* The card underneath while dragging: the one the swipe would bring. */
  peek(dir) {
    const it = this.items[this.i + dir];
    this.under.hidden = !it;
    if (it) this.under.firstChild.src = it.url;
    return !!it;
  }

  down(e) {
    if (this.busy || !this.items.length) return;
    this.top.setPointerCapture(e.pointerId);
    this.drag = { x: e.clientX, y: e.clientY, t: performance.now(), dx: 0 };
    this.top.style.transition = 'none';
  }

  move(e) {
    if (!this.drag) return;
    const dx = e.clientX - this.drag.x;
    this.drag.dx = dx;
    const dir = dx < 0 ? 1 : -1;
    const can = this.peek(dir);
    // At either end the card still moves, but grudgingly.
    const x = can ? dx : dx / 4;
    this.top.style.transform = `translateX(${x}px) rotate(${x / 18}deg)`;
    // The card underneath rises to meet the swipe.
    const k = Math.min(1, Math.abs(dx) / THROW);
    this.under.style.transform = `scale(${0.92 + 0.08 * k})`;
    this.under.style.opacity = String(0.5 + 0.5 * k);
  }

  up(e) {
    if (!this.drag) return;
    const { dx, t, x, y } = this.drag;
    this.drag = null;
    const moved = Math.hypot(e.clientX - x, e.clientY - y);
    if (moved < TAP) {
      const it = this.items[this.i];
      if (it) location.href = this.href(it.id);
      return;
    }
    const v = Math.abs(dx) / (performance.now() - t);
    const dir = dx < 0 ? 1 : -1;
    if ((Math.abs(dx) > THROW || v > FLICK) && this.items[this.i + dir]) this.throw(dir);
    else this.snapBack();
  }

  /* Off it goes, and the next one is on top. */
  throw(dir) {
    this.busy = true;
    const off = (dir > 0 ? -1 : 1) * window.innerWidth * 1.3;
    this.top.style.transition = `transform ${OUT_MS}ms ease-out`;
    this.top.style.transform = `translateX(${off}px) rotate(${off / 18}deg)`;
    this.under.style.transition = `transform ${OUT_MS}ms ease-out, opacity ${OUT_MS}ms`;
    this.under.style.transform = 'scale(1)';
    this.under.style.opacity = '1';
    setTimeout(() => {
      this.i += dir;
      this.under.style.transition = 'none';
      this.show();
      this.busy = false;
    }, OUT_MS);
  }

  snapBack() {
    this.drag = null;
    this.top.style.transition = `transform ${BACK_MS}ms cubic-bezier(.3, 1.4, .5, 1)`;
    this.top.style.transform = '';
    this.under.style.transition = `transform ${BACK_MS}ms, opacity ${BACK_MS}ms`;
    this.under.style.transform = 'scale(0.92)';
    this.under.style.opacity = '0.5';
  }

  /* The arrow buttons and keys: the same throw, without a finger. */
  go(dir) {
    if (this.busy || !this.items[this.i + dir]) return;
    this.peek(dir);
    this.under.style.transform = 'scale(0.92)';
    this.under.style.opacity = '0.5';
    requestAnimationFrame(() => this.throw(dir));
  }
}
