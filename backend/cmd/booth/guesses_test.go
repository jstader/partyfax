package main

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func fakeClock(g *guesses) *time.Time {
	now := time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	return &now
}

func TestGuessesPerAddress(t *testing.T) {
	g := newGuesses()
	now := fakeClock(g)

	for range perAddr {
		if !g.allowed("a") {
			t.Fatal("refused before the limit")
		}
		g.wrong("a")
	}
	if g.allowed("a") {
		t.Error("allowed past the limit")
	}
	if !g.allowed("b") {
		t.Error("another address is refused too")
	}

	*now = now.Add(addrWindow)
	if !g.allowed("a") {
		t.Error("still refused after the window")
	}
}

// A party shares one address: one guest's typos don't lock out the rest,
// though there is still a limit.
func TestGuessesTrustedAddress(t *testing.T) {
	g := newGuesses()
	fakeClock(g)

	g.right("party")
	for range perAddr {
		g.wrong("party")
	}
	if !g.allowed("party") {
		t.Fatal("a trusted address is refused at the ordinary limit")
	}
	for range perTrusted - perAddr {
		g.wrong("party")
	}
	if g.allowed("party") {
		t.Error("a trusted address is never refused")
	}
}

func TestGuessesInTotal(t *testing.T) {
	g := newGuesses()
	now := fakeClock(g)

	g.right("guest")
	// Many addresses, each under its own limit.
	for i := range totalMax {
		g.wrong(fmt.Sprintf("bot-%d", i))
	}

	if g.allowed("newcomer") {
		t.Error("a new address is allowed during a lockout")
	}
	if !g.allowed("guest") {
		t.Error("a guest who had the right code is locked out")
	}

	*now = now.Add(totalWindow)
	if !g.allowed("newcomer") {
		t.Error("still locked out after the window")
	}
}

func TestGuessesForget(t *testing.T) {
	g := newGuesses()
	now := fakeClock(g)

	g.wrong("a")
	g.right("b")
	*now = now.Add(trustFor)
	g.wrong("c")

	if len(g.addrs) != 1 || len(g.trusted) != 0 {
		t.Errorf("kept %d addresses and %d trusted, want 1 and 0", len(g.addrs), len(g.trusted))
	}
}

// Every request that takes a code counts, not only joining.
func TestWrongCodesAnywhereAreLimited(t *testing.T) {
	g := newRig(t, nil)

	for range perAddr {
		if rec := g.do("GET", "/api/booth/strips", "ZZZZZZ", nil); rec.Code != http.StatusNotFound {
			t.Fatalf("wrong code = %d, want 404", rec.Code)
		}
	}
	// Now even the right code is refused from this address.
	if rec := g.do("GET", "/api/booth/strips", g.ev.Code, nil); rec.Code != http.StatusTooManyRequests {
		t.Errorf("after %d wrong codes = %d, want 429", perAddr, rec.Code)
	}
	if rec := g.do("GET", "/api/booth/strips", g.ev.Code, nil, "X-Forwarded-For", "203.0.113.9"); rec.Code != http.StatusOK {
		t.Errorf("another address = %d, want 200", rec.Code)
	}
}
