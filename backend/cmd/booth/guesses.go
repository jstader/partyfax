package main

import (
	"sync"
	"time"
)

// Every guest request carries the event code, so every one of them is a
// chance to guess a code, not just /join, which Caddy limits. guesses counts
// wrong codes wherever they arrive:
//
//   - per address: perAddr wrong codes in addrWindow, then that address is
//     refused until its window ends. A guest mistyping the sign never gets
//     near it. An address that has shown a right code lately gets
//     perTrusted instead: a party's guests often share one Wi-Fi address,
//     and one guest's typos shouldn't lock out the rest.
//   - in total: totalMax wrong codes in totalWindow from everyone, then
//     anyone who hasn't shown a right code lately is refused until the
//     window ends. That is what stops many addresses guessing together;
//     guests already at the party carry on.
//
// At those numbers a code is out of reach: 887 million of them against
// 24,000 guesses a day at most.
const (
	perAddr     = 10
	perTrusted  = 50
	addrWindow  = 10 * time.Minute
	totalMax    = 1000
	totalWindow = time.Hour
	// How long a right code vouches for its address during a lockout.
	trustFor = 24 * time.Hour
)

type window struct {
	start time.Time
	n     int
}

type guesses struct {
	now func() time.Time

	mu      sync.Mutex
	addrs   map[string]*window
	total   window
	trusted map[string]time.Time // address -> last right code
}

func newGuesses() *guesses {
	return &guesses{now: time.Now, addrs: map[string]*window{}, trusted: map[string]time.Time{}}
}

// allowed says whether addr may try a code now.
func (g *guesses) allowed(addr string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()

	t, ok := g.trusted[addr]
	trusted := ok && now.Sub(t) < trustFor

	limit := perAddr
	if trusted {
		limit = perTrusted
	}
	if w := g.addrs[addr]; w != nil && now.Sub(w.start) < addrWindow && w.n >= limit {
		return false
	}
	if now.Sub(g.total.start) < totalWindow && g.total.n >= totalMax {
		return trusted
	}
	return true
}

// wrong records a wrong code from addr.
func (g *guesses) wrong(addr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()

	g.prune(now)

	w := g.addrs[addr]
	if w == nil || now.Sub(w.start) >= addrWindow {
		w = &window{start: now}
		g.addrs[addr] = w
	}
	w.n++

	if now.Sub(g.total.start) >= totalWindow {
		g.total = window{start: now}
	}
	g.total.n++
}

// right records a right code from addr.
func (g *guesses) right(addr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.trusted[addr] = g.now()
}

// prune forgets finished windows and old trust, so the maps stay the size
// of the last few minutes' visitors. Called with mu held, on wrong codes
// only: right ones are few.
func (g *guesses) prune(now time.Time) {
	for a, w := range g.addrs {
		if now.Sub(w.start) >= addrWindow {
			delete(g.addrs, a)
		}
	}
	for a, t := range g.trusted {
		if now.Sub(t) >= trustFor {
			delete(g.trusted, a)
		}
	}
}
