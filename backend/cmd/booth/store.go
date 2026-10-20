package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The theme choices the strip composer knows how to draw
// (frontend/booth/js/strip.js). An event naming anything else is refused, so
// a typo in the admin page is a 400, not a strip that silently draws nothing.
var (
	frames = []string{"line", "double", "dotted", "film"}
	fonts  = []string{"vt323", "display", "script"}
	icons  = []string{"heart", "star", "balloon", "cake", "confetti", "music", "camera", "sparkle",
		"pumpkin", "ghost", "tree", "snowflake", "gift", "bat"}
)

const (
	maxTitle    = 40
	maxSubtitle = 60
	maxIcons    = 6 // an icon may repeat

	// Seconds of countdown before the first photo and between the others.
	// Zero in a stored event means it predates the setting: the defaults.
	defaultCountFirst = 5
	defaultCountNext  = 3
	maxCount          = 15
)

// codeAlphabet leaves out 0/O and 1/I/L, which read the same off a sign
// across a room. Six of these is about 887 million codes; with join limited
// to ten tries a minute per address, guessing one is out of reach.
const (
	codeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	codeLen      = 6
)

// Event is one party: its code, its look, and whether it is open. The theme
// fields are exactly what the strip composer reads.
type Event struct {
	ID       string   `json:"id"`
	Code     string   `json:"code"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Font     string   `json:"font"`
	Frame    string   `json:"frame"`
	Icons    []string `json:"icons"`
	// Countdown seconds: before the first photo, and between the rest.
	CountFirst int       `json:"countFirst"`
	CountNext  int       `json:"countNext"`
	Active     bool      `json:"active"` // guests can join and print
	Wall       bool      `json:"wall"`   // guests can see the event's strips
	Created    time.Time `json:"created"`
}

// Theme is the part of an event a guest's page needs, and nothing more: no
// id, no code back, no switches.
type Theme struct {
	Title      string   `json:"title"`
	Subtitle   string   `json:"subtitle"`
	Font       string   `json:"font"`
	Frame      string   `json:"frame"`
	Icons      []string `json:"icons"`
	CountFirst int      `json:"countFirst"`
	CountNext  int      `json:"countNext"`
	Wall       bool     `json:"wall"`
}

func (e Event) Theme() Theme {
	return Theme{Title: e.Title, Subtitle: e.Subtitle, Font: e.Font, Frame: e.Frame, Icons: e.Icons,
		CountFirst: e.CountFirst, CountNext: e.CountNext, Wall: e.Wall}
}

// withDefaults fills the countdowns an older event, or an admin who left
// them blank, didn't set.
func (e Event) withDefaults() Event {
	if e.CountFirst == 0 {
		e.CountFirst = defaultCountFirst
	}
	if e.CountNext == 0 {
		e.CountNext = defaultCountNext
	}
	return e
}

// Validate is the admin's input checked against what the composer can draw.
func (e Event) Validate() error {
	if strings.TrimSpace(e.Title) == "" {
		return errors.New("title is empty")
	}
	if n := utf8.RuneCountInString(e.Title); n > maxTitle {
		return fmt.Errorf("title is %d characters, the limit is %d", n, maxTitle)
	}
	if n := utf8.RuneCountInString(e.Subtitle); n > maxSubtitle {
		return fmt.Errorf("subtitle is %d characters, the limit is %d", n, maxSubtitle)
	}
	if !slices.Contains(frames, e.Frame) {
		return fmt.Errorf("frame %q is not one of %v", e.Frame, frames)
	}
	if !slices.Contains(fonts, e.Font) {
		return fmt.Errorf("font %q is not one of %v", e.Font, fonts)
	}
	if len(e.Icons) > maxIcons {
		return fmt.Errorf("%d icons, the limit is %d", len(e.Icons), maxIcons)
	}
	for _, i := range e.Icons {
		if !slices.Contains(icons, i) {
			return fmt.Errorf("icon %q is not one of %v", i, icons)
		}
	}
	for _, c := range []int{e.CountFirst, e.CountNext} {
		if c < 1 || c > maxCount {
			return fmt.Errorf("countdowns are 1 to %d seconds", maxCount)
		}
	}
	return nil
}

// Strip is one printed strip. The picture is next to it as <id>.png.
type Strip struct {
	ID     string    `json:"id"`
	Time   time.Time `json:"time"`
	Hidden bool      `json:"hidden,omitempty"`
}

// invalidEvent is the admin's input failing Validate: a 400, unlike the
// store's own failures.
type invalidEvent struct{ error }

func (e invalidEvent) Unwrap() error { return e.error }

var (
	errNoEvent = errors.New("no such event")
	errNoStrip = errors.New("no such strip")
)

// Store keeps events and their strips on disk:
//
//	events.json                  every event, rewritten whole on a change
//	strips/<event>/<strip>.png   each printed strip
//	strips/<event>/strips.jsonl  their list, one line each, appended
//
// Events are few and change rarely, so rewriting the file is fine; strips
// are many and only ever added, so they append, like the public wall.
type Store struct {
	dir string

	mu     sync.Mutex
	events []Event
	strips map[string][]Strip // by event id, oldest first
}

func OpenStore(dir string) (*Store, error) {
	s := &Store{dir: dir, strips: map[string][]Strip{}}

	if err := os.MkdirAll(filepath.Join(dir, "strips"), 0o755); err != nil {
		return nil, err
	}

	b, err := os.ReadFile(s.eventsPath())
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.events); err != nil {
			return nil, fmt.Errorf("%s: %w", s.eventsPath(), err)
		}
		for i := range s.events {
			s.events[i] = s.events[i].withDefaults()
		}
	}

	for _, e := range s.events {
		list, err := readStrips(s.stripsList(e.ID))
		if err != nil {
			return nil, err
		}
		s.strips[e.ID] = list
	}

	return s, nil
}

func (s *Store) eventsPath() string        { return filepath.Join(s.dir, "events.json") }
func (s *Store) eventDir(id string) string { return filepath.Join(s.dir, "strips", id) }
func (s *Store) stripsList(id string) string {
	return filepath.Join(s.eventDir(id), "strips.jsonl")
}

// Events is every event, newest first.
func (s *Store) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := slices.Clone(s.events)
	slices.Reverse(out)
	return out
}

func (s *Store) Event(id string) (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := s.index(id)
	if i < 0 {
		return Event{}, false
	}
	return s.events[i], true
}

// ByCode finds an active event from what a guest typed: case, spaces and
// dashes don't matter, since people copy a code off a sign however they like.
func (s *Store) ByCode(typed string) (Event, bool) {
	code := NormalizeCode(typed)
	if code == "" {
		return Event{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, e := range s.events {
		if e.Code == code && e.Active {
			return e, true
		}
	}
	return Event{}, false
}

// Create adds an event with a fresh id and code. The caller's id, code and
// created time are ignored.
func (s *Store) Create(e Event) (Event, error) {
	e = e.withDefaults()
	if err := e.Validate(); err != nil {
		return Event{}, invalidEvent{err}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	e.ID = randomID()
	e.Code = s.newCode()
	e.Created = time.Now().UTC()

	if err := os.MkdirAll(s.eventDir(e.ID), 0o755); err != nil {
		return Event{}, err
	}

	s.events = append(s.events, e)
	if err := s.saveEvents(); err != nil {
		s.events = s.events[:len(s.events)-1]
		return Event{}, err
	}
	s.strips[e.ID] = nil
	return e, nil
}

// Update replaces an event's theme and switches; its id, code and created
// time stay as they were.
func (s *Store) Update(id string, e Event) (Event, error) {
	e = e.withDefaults()
	if err := e.Validate(); err != nil {
		return Event{}, invalidEvent{err}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	i := s.index(id)
	if i < 0 {
		return Event{}, errNoEvent
	}

	old := s.events[i]
	e.ID, e.Code, e.Created = old.ID, old.Code, old.Created
	s.events[i] = e
	if err := s.saveEvents(); err != nil {
		s.events[i] = old
		return Event{}, err
	}
	return e, nil
}

// NewCode replaces an event's code, for when the old one got out.
func (s *Store) NewCode(id string) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := s.index(id)
	if i < 0 {
		return Event{}, errNoEvent
	}

	old := s.events[i].Code
	s.events[i].Code = s.newCode()
	if err := s.saveEvents(); err != nil {
		s.events[i].Code = old
		return Event{}, err
	}
	return s.events[i], nil
}

// Delete removes an event and every strip it printed.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := s.index(id)
	if i < 0 {
		return errNoEvent
	}

	kept := slices.Delete(slices.Clone(s.events), i, i+1)
	old := s.events
	s.events = kept
	if err := s.saveEvents(); err != nil {
		s.events = old
		return err
	}

	delete(s.strips, id)
	return os.RemoveAll(s.eventDir(id))
}

// AddStrip keeps a printed strip: the picture first, then its line, so a
// listed strip always has a picture.
func (s *Store) AddStrip(eventID, id string, png []byte) (Strip, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.index(eventID) < 0 {
		return Strip{}, errNoEvent
	}

	st := Strip{ID: id, Time: time.Now().UTC()}
	if err := os.WriteFile(filepath.Join(s.eventDir(eventID), id+".png"), png, 0o644); err != nil {
		return Strip{}, err
	}

	line, err := json.Marshal(st)
	if err != nil {
		return Strip{}, err
	}
	f, err := os.OpenFile(s.stripsList(eventID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Strip{}, err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return Strip{}, err
	}

	s.strips[eventID] = append(s.strips[eventID], st)
	return st, nil
}

// Strips is an event's strips, newest first, hidden ones only if asked.
func (s *Store) Strips(eventID string, withHidden bool) []Strip {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Strip
	for _, st := range slices.Backward(s.strips[eventID]) {
		if withHidden || !st.Hidden {
			out = append(out, st)
		}
	}
	return out
}

// StripPNG is a strip's picture, if the event has that strip.
func (s *Store) StripPNG(eventID, id string, withHidden bool) ([]byte, error) {
	s.mu.Lock()
	ok := slices.ContainsFunc(s.strips[eventID], func(st Strip) bool {
		return st.ID == id && (withHidden || !st.Hidden)
	})
	s.mu.Unlock()

	if !ok {
		return nil, errNoStrip
	}
	return os.ReadFile(filepath.Join(s.eventDir(eventID), id+".png"))
}

// DeleteStrip removes a strip for good: its line and its picture.
func (s *Store) DeleteStrip(eventID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := s.strips[eventID]
	i := slices.IndexFunc(list, func(st Strip) bool { return st.ID == id })
	if i < 0 {
		return errNoStrip
	}

	next := slices.Delete(slices.Clone(list), i, i+1)
	if err := writeStrips(s.stripsList(eventID), next); err != nil {
		return err
	}
	s.strips[eventID] = next

	err := os.Remove(filepath.Join(s.eventDir(eventID), id+".png"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// SetHidden takes a strip off the event's wall, or puts it back. The list is
// rewritten whole: rare, and small.
func (s *Store) SetHidden(eventID, id string, hidden bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := s.strips[eventID]
	i := slices.IndexFunc(list, func(st Strip) bool { return st.ID == id })
	if i < 0 {
		return errNoStrip
	}

	next := slices.Clone(list)
	next[i].Hidden = hidden
	if err := writeStrips(s.stripsList(eventID), next); err != nil {
		return err
	}
	s.strips[eventID] = next
	return nil
}

func (s *Store) index(id string) int {
	return slices.IndexFunc(s.events, func(e Event) bool { return e.ID == id })
}

// newCode is a code no other event has. Called with mu held.
func (s *Store) newCode() string {
	for {
		c := randomCode()
		if !slices.ContainsFunc(s.events, func(e Event) bool { return e.Code == c }) {
			return c
		}
	}
}

// saveEvents writes events.json through a temporary file, so a crash
// mid-write leaves the old file rather than half a new one. Called with mu
// held.
func (s *Store) saveEvents() error {
	b, err := json.MarshalIndent(s.events, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.eventsPath(), b)
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readStrips(path string) ([]Strip, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Strip
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var st Strip
		// A torn last line from a crash mid-append is skipped, not fatal.
		if json.Unmarshal(sc.Bytes(), &st) == nil && st.ID != "" {
			out = append(out, st)
		}
	}
	return out, sc.Err()
}

func writeStrips(path string, list []Strip) error {
	var b strings.Builder
	for _, st := range list {
		line, err := json.Marshal(st)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return writeAtomic(path, []byte(b.String()))
}

// NormalizeCode turns what a guest typed into a stored code's form, or ""
// if it can't be one.
func NormalizeCode(typed string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(typed) {
		switch {
		case r == ' ' || r == '-':
			continue
		case strings.ContainsRune(codeAlphabet, r):
			b.WriteRune(r)
		default:
			return ""
		}
	}
	if b.Len() != codeLen {
		return ""
	}
	return b.String()
}

var validID = regexp.MustCompile(`^[0-9a-f]{16}$`)

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomCode() string {
	// crypto/rand.Text is base32; this alphabet is ours. Rejection sampling
	// keeps every character equally likely.
	out := make([]byte, 0, codeLen)
	buf := make([]byte, 1)
	for len(out) < codeLen {
		_, _ = rand.Read(buf)
		if int(buf[0]) < 256-256%len(codeAlphabet) {
			out = append(out, codeAlphabet[int(buf[0])%len(codeAlphabet)])
		}
	}
	return string(out)
}
