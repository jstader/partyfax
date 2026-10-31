/* Printing when someone else may be: the printer takes one job at a time,
   and the dispatcher answers 409 "busy" while it has one. Used by the
   photobooth and the fortunes site. */

// The printer takes one job at a time; at a party someone else's is often
// on it. A print waits this long between tries, and gives up after this.
const BUSY_RETRY_MS = 3000;
const BUSY_GIVE_UP_MS = 90_000;

/* Runs send(), a print, again while the printer is busy with someone
   else's, calling onWait() before each wait. Resolves or throws as send()
   does in the end. */
export async function whileBusy(send, onWait) {
  const started = Date.now();
  for (;;) {
    try {
      return await send();
    } catch (e) {
      if (e.status !== 409 || !/busy/i.test(e.message) || Date.now() - started >= BUSY_GIVE_UP_MS) throw e;
      onWait();
      await new Promise((r) => setTimeout(r, BUSY_RETRY_MS));
    }
  }
}
