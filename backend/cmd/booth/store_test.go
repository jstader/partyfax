package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func theme() Event {
	return Event{Title: "John's 40th", Subtitle: "18 Oct 2026", Font: "display", Frame: "film",
		Icons: []string{"heart", "star"}, Active: true, Wall: true}
}

func TestStoreKeepsEventsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	e, err := s.Create(theme())
	if err != nil {
		t.Fatal(err)
	}
	if !validID.MatchString(e.ID) || NormalizeCode(e.Code) != e.Code {
		t.Fatalf("created %+v: bad id or code", e)
	}
	if _, err := s.AddStrip(e.ID, "8f0e0d4a-1b2c-4d5e-8f90-a1b2c3d4e5f6", []byte("png")); err != nil {
		t.Fatal(err)
	}

	again, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := again.Event(e.ID)
	if !ok || got.Code != e.Code || got.Title != e.Title {
		t.Fatalf("after reopening: %+v, %v", got, ok)
	}
	if n := len(again.Strips(e.ID, false)); n != 1 {
		t.Errorf("after reopening: %d strips, want 1", n)
	}
}

func TestByCodeIsForgivingAboutTypingButNotAboutClosedEvents(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	e, _ := s.Create(theme())

	typed := strings.ToLower(e.Code[:3]) + " - " + strings.ToLower(e.Code[3:])
	if got, ok := s.ByCode(typed); !ok || got.ID != e.ID {
		t.Errorf("ByCode(%q) = %v, want the event", typed, ok)
	}

	closed := theme()
	closed.Active = false
	if _, err := s.Update(e.ID, closed); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ByCode(e.Code); ok {
		t.Error("a closed event still answers to its code")
	}
}

func TestNormalizeCode(t *testing.T) {
	for typed, want := range map[string]string{
		"abc234":   "ABC234",
		"ABC-234":  "ABC234",
		" ab c234": "ABC234",
		"ABC23":    "", // too short
		"ABC2345":  "", // too long
		"ABC0OO":   "", // 0 and O are not in the alphabet
		"ABÇ234":   "", // nor is anything else
		"":         "",
	} {
		if got := NormalizeCode(typed); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", typed, got, want)
		}
	}
}

func TestCodesAreUniqueAndFromTheAlphabet(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	seen := map[string]bool{}
	for range 200 {
		e, err := s.Create(theme())
		if err != nil {
			t.Fatal(err)
		}
		if seen[e.Code] {
			t.Fatalf("code %s issued twice", e.Code)
		}
		seen[e.Code] = true
		for _, r := range e.Code {
			if !strings.ContainsRune(codeAlphabet, r) {
				t.Fatalf("code %s has %q", e.Code, r)
			}
		}
	}
}

func TestNewCodeRetiresTheOldOne(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	e, _ := s.Create(theme())

	next, err := s.NewCode(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.Code == e.Code {
		t.Fatal("code did not change")
	}
	if _, ok := s.ByCode(e.Code); ok {
		t.Error("the old code still works")
	}
}

func TestUpdateKeepsIdentity(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	e, _ := s.Create(theme())

	edit := theme()
	edit.ID, edit.Code, edit.Title = "ffffffffffffffff", "ZZZZZZ", "New title"
	got, err := s.Update(e.ID, edit)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != e.ID || got.Code != e.Code || !got.Created.Equal(e.Created) || got.Title != "New title" {
		t.Errorf("update = %+v: id, code or created changed, or title didn't", got)
	}
}

func TestValidateRefusesWhatTheComposerCantDraw(t *testing.T) {
	cases := map[string]func(*Event){
		"empty title":   func(e *Event) { e.Title = "  " },
		"long title":    func(e *Event) { e.Title = strings.Repeat("x", maxTitle+1) },
		"long subtitle": func(e *Event) { e.Subtitle = strings.Repeat("x", maxSubtitle+1) },
		"frame":         func(e *Event) { e.Frame = "wavy" },
		"font":          func(e *Event) { e.Font = "comic" },
		"icon":          func(e *Event) { e.Icons = []string{"skull"} },
		"too many":      func(e *Event) { e.Icons = []string{"heart", "star", "cake", "music", "camera", "bat", "gift"} },
		"countdown":     func(e *Event) { e.CountFirst = maxCount + 1 },
		"negative":      func(e *Event) { e.CountNext = -1 },
	}
	s, _ := OpenStore(t.TempDir())
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			e := theme()
			spoil(&e)
			_, err := s.Create(e)
			if !errors.As(err, new(invalidEvent)) {
				t.Errorf("Create = %v, want an invalidEvent", err)
			}
		})
	}
}

func TestHiddenStripsLeaveTheWallButNotTheAdmin(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	e, _ := s.Create(theme())
	a, b := "8f0e0d4a-1b2c-4d5e-8f90-a1b2c3d4e5f6", "9f0e0d4a-1b2c-4d5e-8f90-a1b2c3d4e5f6"
	s.AddStrip(e.ID, a, []byte("a"))
	s.AddStrip(e.ID, b, []byte("b"))

	if err := s.SetHidden(e.ID, a, true); err != nil {
		t.Fatal(err)
	}

	if got := s.Strips(e.ID, false); len(got) != 1 || got[0].ID != b {
		t.Errorf("wall = %+v, want only %s", got, b)
	}
	if got := s.Strips(e.ID, true); len(got) != 2 || got[0].ID != b {
		t.Errorf("admin list = %+v, want both, newest first", got)
	}
	if _, err := s.StripPNG(e.ID, a, false); !errors.Is(err, errNoStrip) {
		t.Errorf("hidden strip's picture served to guests: %v", err)
	}
	if png, err := s.StripPNG(e.ID, a, true); err != nil || string(png) != "a" {
		t.Errorf("admin can't see the hidden strip: %q, %v", png, err)
	}

	// And it stays hidden after a restart.
	again, _ := OpenStore(s.dir)
	if n := len(again.Strips(e.ID, false)); n != 1 {
		t.Errorf("after reopening: %d visible strips, want 1", n)
	}
}

func TestDeleteRemovesTheEventsStrips(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	e, _ := s.Create(theme())
	s.AddStrip(e.ID, "8f0e0d4a-1b2c-4d5e-8f90-a1b2c3d4e5f6", []byte("png"))

	if err := s.Delete(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Event(e.ID); ok {
		t.Error("event still there")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "strips", e.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("strips directory still there: %v", err)
	}
}

func TestATornLastLineIsSkipped(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir)
	e, _ := s.Create(theme())
	s.AddStrip(e.ID, "8f0e0d4a-1b2c-4d5e-8f90-a1b2c3d4e5f6", []byte("png"))

	f, _ := os.OpenFile(s.stripsList(e.ID), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"id":"half`)
	f.Close()

	again, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("a torn line broke the store: %v", err)
	}
	if n := len(again.Strips(e.ID, true)); n != 1 {
		t.Errorf("%d strips, want 1", n)
	}
}

// Events from before the countdown settings, and an admin leaving them
// blank, get today's timings rather than a zero-second countdown.
func TestCountdownsDefault(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	e, err := s.Create(theme())
	if err != nil {
		t.Fatal(err)
	}
	if e.CountFirst != defaultCountFirst || e.CountNext != defaultCountNext {
		t.Errorf("new event countdowns %d, %d", e.CountFirst, e.CountNext)
	}

	old := `[{"id":"0123456789abcdef","code":"ABC234","title":"Old","font":"vt323","frame":"line","icons":[],"active":true}]`
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "events.json"), []byte(old), 0o644)
	s2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s2.Event("0123456789abcdef")
	if got.CountFirst != defaultCountFirst || got.CountNext != defaultCountNext {
		t.Errorf("old event countdowns %d, %d", got.CountFirst, got.CountNext)
	}
}
