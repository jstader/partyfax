package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/jstader/partyfax/backend/internal/doc"
	"github.com/jstader/partyfax/backend/internal/httpx"
	"github.com/jstader/partyfax/backend/internal/logging"
	"github.com/jstader/partyfax/backend/internal/render"
	"github.com/jstader/partyfax/backend/internal/timeouts"
	"github.com/jstader/partyfax/backend/internal/upstream"
)

// maxStrip caps an uploaded strip before anything reads it: StripMaxRows of
// 48 bytes is about 66 KB raw, stored as a PNG of uncompressed blocks.
const maxStrip = 256 << 10 // 256 KiB

// maxEvent caps an admin's event body, which is a few hundred bytes.
const maxEvent = 8 << 10

// codeHeader carries the guest's event code. A header, never the URL, so
// codes stay out of access logs and browser history.
const codeHeader = "X-Event-Code"

// StripRenderer is the one renderer call the booth makes.
type StripRenderer interface {
	RenderStrip(ctx context.Context, png []byte) ([]byte, error)
}

type api struct {
	renderer   StripRenderer
	dispatcher upstream.Dispatcher
	store      *Store
	access     *Access // nil: admin is off
	guesses    *guesses
	log        *slog.Logger
}

func (a *api) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Guests: everything takes the event code.
	mux.HandleFunc("POST /api/booth/join", a.join)
	mux.HandleFunc("POST /api/booth/print", a.print)
	mux.HandleFunc("POST /api/booth/reprint", a.reprint)
	mux.HandleFunc("GET /api/booth/strips", a.strips)
	mux.HandleFunc("GET /api/booth/strips/{id}", a.strip)
	mux.HandleFunc("GET /api/booth/health", httpx.Health)

	// The host, through Cloudflare Access.
	admin := http.NewServeMux()
	admin.HandleFunc("GET /api/booth/admin/events", a.listEvents)
	admin.HandleFunc("POST /api/booth/admin/events", a.createEvent)
	admin.HandleFunc("PUT /api/booth/admin/events/{event}", a.updateEvent)
	admin.HandleFunc("DELETE /api/booth/admin/events/{event}", a.deleteEvent)
	admin.HandleFunc("POST /api/booth/admin/events/{event}/code", a.newCode)
	admin.HandleFunc("GET /api/booth/admin/events/{event}/strips", a.adminStrips)
	admin.HandleFunc("GET /api/booth/admin/events/{event}/strips/{id}", a.adminStrip)
	admin.HandleFunc("GET /api/booth/admin/events/{event}/strips.zip", a.stripsZip)
	admin.HandleFunc("DELETE /api/booth/admin/events/{event}/strips/{id}", a.deleteStrip)
	admin.HandleFunc("POST /api/booth/admin/events/{event}/strips/{id}/hide", a.hideStrip(true))
	admin.HandleFunc("POST /api/booth/admin/events/{event}/strips/{id}/show", a.hideStrip(false))
	admin.HandleFunc("POST /api/booth/admin/events/{event}/test", a.testPrint)
	mux.Handle("/api/booth/admin/", a.requireAccess(admin))

	return mux
}

/* ---- guests ------------------------------------------------------------- */

// join is how a guest's page learns which event a code opens, and its theme.
// A wrong code and a closed event look the same from outside.
func (a *api) join(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

	var in struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		a.fail(w, r, http.StatusBadRequest, "send {\"code\": \"...\"}")
		return
	}

	e, ok := a.byCode(w, r, in.Code)
	if !ok {
		return
	}

	httpx.WriteJSON(w, http.StatusOK, e.Theme())
}

// print takes a finished strip from a guest's phone, prints it and keeps it
// for the event's wall. Like the public site it answers only once the
// printer has: the strip is on paper, or the error says why not.
func (a *api) print(w http.ResponseWriter, r *http.Request) {
	e, ok := a.guest(w, r)
	if !ok {
		return
	}

	pic, rows, ok := a.readStrip(w, r)
	if !ok {
		return
	}

	a.printStrip(w, r, pic, rows, e.ID, true)
}

// reprint prints a strip from the event's wall again, for a guest who wants
// another copy. Not kept a second time: the wall already has it. Caddy
// limits it with the same allowance as printing.
func (a *api) reprint(w http.ResponseWriter, r *http.Request) {
	e, ok := a.guest(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var in struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !validStripID.MatchString(in.ID) {
		a.fail(w, r, http.StatusBadRequest, "send {\"id\": \"...\"}")
		return
	}

	pic, err := a.store.StripPNG(e.ID, in.ID, false)
	if err != nil {
		a.fail(w, r, http.StatusNotFound, errNoStrip.Error())
		return
	}
	cfg, err := doc.ValidateStrip(bytes.NewReader(pic))
	if err != nil {
		a.log.ErrorContext(r.Context(), "stored strip is unreadable", "event", e.ID, "strip", in.ID, "err", err)
		a.fail(w, r, http.StatusInternalServerError, "that strip can't be printed")
		return
	}

	a.printStrip(w, r, pic, cfg.Height, e.ID, false)
}

// strips is the event's wall: what's been printed, newest first.
func (a *api) strips(w http.ResponseWriter, r *http.Request) {
	e, ok := a.guest(w, r)
	if !ok {
		return
	}
	if !e.Wall {
		a.fail(w, r, http.StatusForbidden, "this event's wall is off")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"strips": orEmpty(a.store.Strips(e.ID, false))})
}

// strip is one strip's picture. Fetched with the code header and shown from
// a blob, since an <img> tag can't send headers.
func (a *api) strip(w http.ResponseWriter, r *http.Request) {
	e, ok := a.guest(w, r)
	if !ok {
		return
	}
	if !e.Wall {
		a.fail(w, r, http.StatusForbidden, "this event's wall is off")
		return
	}
	a.servePNG(w, r, e.ID, r.PathValue("id"), false)
}

// guest is the event the request's code header opens, or the refusal
// already written.
func (a *api) guest(w http.ResponseWriter, r *http.Request) (Event, bool) {
	return a.byCode(w, r, r.Header.Get(codeHeader))
}

// byCode is the open event a typed code opens: a 404 if none does, a 429 if
// this address has guessed too often (see guesses).
func (a *api) byCode(w http.ResponseWriter, r *http.Request, typed string) (Event, bool) {
	addr := logging.ClientIP(r)
	if !a.guesses.allowed(addr) {
		a.fail(w, r, http.StatusTooManyRequests, "too many wrong codes, try again in a few minutes")
		return Event{}, false
	}
	e, ok := a.store.ByCode(typed)
	if !ok {
		a.guesses.wrong(addr)
		a.fail(w, r, http.StatusNotFound, "no open event with that code")
		return Event{}, false
	}
	a.guesses.right(addr)
	return e, true
}

/* ---- the host ----------------------------------------------------------- */

func (a *api) requireAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.access == nil {
			a.fail(w, r, http.StatusForbidden, "admin is not configured")
			return
		}
		email, err := a.access.Verify(r.Context(), r)
		if err != nil {
			a.log.WarnContext(r.Context(), "admin refused", "err", err)
			a.fail(w, r, http.StatusForbidden, "sign in through Cloudflare Access")
			return
		}
		if !sameSite(r) {
			a.log.WarnContext(r.Context(), "admin refused", "err", "request from another site",
				"origin", r.Header.Get("Origin"), "fetch_site", r.Header.Get("Sec-Fetch-Site"))
			a.fail(w, r, http.StatusForbidden, "admin changes come from the admin page only")
			return
		}
		a.log.InfoContext(r.Context(), "admin", "email", email, "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

// sameSite is false for a change another site's page sent. The Access
// token rides on a cookie, which a browser may attach to a form another
// site submits; what that form can't fake is where the browser says it came
// from. Reading is always fine: another site can't see the answer.
func sameSite(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	// Browsers older than Sec-Fetch-Site still send Origin on a POST.
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin == "https://"+r.Host
	}
	return true // not a browser: curl, or a test
}

// wantType refuses a body that isn't the type the handler reads. JSON and
// PNG are not types a plain HTML form can send, so with sameSite this keeps
// the admin API to the admin page.
func (a *api) wantType(w http.ResponseWriter, r *http.Request, want string) bool {
	if ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(ct) != want {
		a.fail(w, r, http.StatusUnsupportedMediaType, "send "+want)
		return false
	}
	return true
}

func (a *api) listEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": orEmpty(a.store.Events())})
}

func (a *api) createEvent(w http.ResponseWriter, r *http.Request) {
	e, ok := a.readEvent(w, r)
	if !ok {
		return
	}
	created, err := a.store.Create(e)
	if err != nil {
		a.storeFailed(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (a *api) updateEvent(w http.ResponseWriter, r *http.Request) {
	e, ok := a.readEvent(w, r)
	if !ok {
		return
	}
	updated, err := a.store.Update(r.PathValue("event"), e)
	if err != nil {
		a.storeFailed(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, updated)
}

func (a *api) deleteEvent(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Delete(r.PathValue("event")); err != nil {
		a.storeFailed(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) newCode(w http.ResponseWriter, r *http.Request) {
	e, err := a.store.NewCode(r.PathValue("event"))
	if err != nil {
		a.storeFailed(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, e)
}

func (a *api) adminStrips(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("event")
	if _, ok := a.store.Event(id); !ok {
		a.fail(w, r, http.StatusNotFound, errNoEvent.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"strips": orEmpty(a.store.Strips(id, true))})
}

func (a *api) adminStrip(w http.ResponseWriter, r *http.Request) {
	a.servePNG(w, r, r.PathValue("event"), r.PathValue("id"), true)
}

func (a *api) deleteStrip(w http.ResponseWriter, r *http.Request) {
	if !validStripID.MatchString(r.PathValue("id")) {
		a.fail(w, r, http.StatusNotFound, errNoStrip.Error())
		return
	}
	if err := a.store.DeleteStrip(r.PathValue("event"), r.PathValue("id")); err != nil {
		a.storeFailed(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// albumScale enlarges strips for an album: 384 dots wide is a thumbnail on a
// phone. Whole multiples, nearest neighbour, so the dots stay crisp squares.
const albumScale = 3

// stripsZip is every strip the event printed, hidden ones too, enlarged for
// a photo album, oldest first so an album sorts them by time.
func (a *api) stripsZip(w http.ResponseWriter, r *http.Request) {
	e, ok := a.store.Event(r.PathValue("event"))
	if !ok {
		a.fail(w, r, http.StatusNotFound, errNoEvent.Error())
		return
	}

	strips := a.store.Strips(e.ID, true)
	slices.Reverse(strips)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-strips.zip"`, slug(e.Title)))
	w.Header().Set("Cache-Control", "no-store")

	zw := zip.NewWriter(w)
	for _, st := range strips {
		b, err := a.store.StripPNG(e.ID, st.ID, true)
		if err != nil {
			continue
		}
		big, err := enlarge(b, albumScale)
		if err != nil {
			a.log.WarnContext(r.Context(), "skipping unreadable strip", "strip", st.ID, "err", err)
			continue
		}
		name := fmt.Sprintf("%s-%s.png", st.Time.Format("2006-01-02-150405"), st.ID[:8])
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: st.Time})
		if err != nil {
			return // the client went away; the response is already started
		}
		if _, err := f.Write(big); err != nil {
			return
		}
	}
	_ = zw.Close()
}

// enlarge scales a one-bit strip by a whole number, nearest neighbour, and
// re-encodes it. PNG compresses the flat result well, so it stays small.
func enlarge(b []byte, k int) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	out := image.NewGray(image.Rect(0, 0, bounds.Dx()*k, bounds.Dy()*k))
	for y := range bounds.Dy() {
		for x := range bounds.Dx() {
			v := color.GrayModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.Gray)
			for dy := range k {
				row := out.Pix[(y*k+dy)*out.Stride:]
				for dx := range k {
					row[x*k+dx] = v.Y
				}
			}
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// slug makes a title safe as a file name: letters and digits, dashes between.
func slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimSuffix(b.String(), "-")
	if s == "" {
		return "photobooth"
	}
	return s
}

func (a *api) hideStrip(hidden bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.store.SetHidden(r.PathValue("event"), r.PathValue("id"), hidden); err != nil {
			a.storeFailed(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// testPrint prints a strip for an event without keeping it: the host trying
// a theme on paper before the party. Works whether or not the event is open.
func (a *api) testPrint(w http.ResponseWriter, r *http.Request) {
	e, ok := a.store.Event(r.PathValue("event"))
	if !ok {
		a.fail(w, r, http.StatusNotFound, errNoEvent.Error())
		return
	}
	if !a.wantType(w, r, "image/png") {
		return
	}

	pic, rows, ok := a.readStrip(w, r)
	if !ok {
		return
	}

	a.printStrip(w, r, pic, rows, e.ID, false)
}

func (a *api) readEvent(w http.ResponseWriter, r *http.Request) (Event, bool) {
	if !a.wantType(w, r, "application/json") {
		return Event{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxEvent)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var e Event
	if err := dec.Decode(&e); err != nil {
		a.fail(w, r, http.StatusBadRequest, "bad event: "+err.Error())
		return Event{}, false
	}
	if e.Icons == nil {
		e.Icons = []string{}
	}
	return e, true
}

/* ---- shared ------------------------------------------------------------- */

// readStrip reads an uploaded strip and redraws it: the size is checked from
// the header before anything is decoded, then the picture is rebuilt one bit
// a pixel, exactly as it prints. What is printed, kept and shown to other
// guests is that redrawing, never the file the phone sent, so nothing else
// the file carried (metadata, trailing bytes) goes any further.
func (a *api) readStrip(w http.ResponseWriter, r *http.Request) ([]byte, int, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxStrip)
	b, cfg, err := doc.ReadStrip(r.Body)
	if err != nil {
		a.fail(w, r, http.StatusBadRequest, err.Error())
		return nil, 0, false
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		a.fail(w, r, http.StatusBadRequest, "not a PNG: "+err.Error())
		return nil, 0, false
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, render.OneBit(img)); err != nil {
		a.fail(w, r, http.StatusInternalServerError, "could not redraw the strip")
		return nil, 0, false
	}
	return out.Bytes(), cfg.Height, true
}

// printStrip renders, prints and, if keep, stores. The trace id is the job
// id and the strip id, so one grep follows a strip from upload to paper.
func (a *api) printStrip(w http.ResponseWriter, r *http.Request, pic []byte, rows int, eventID string, keep bool) {
	payload, err := a.renderer.RenderStrip(r.Context(), pic)
	if err != nil {
		a.upstreamFailed(w, r, "renderer", err)
		return
	}

	id := logging.Trace(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), timeouts.Print)
	defer cancel()

	if err := a.dispatcher.Print(ctx, id, payload); err != nil {
		a.upstreamFailed(w, r, "dispatcher", err)
		return
	}

	a.log.InfoContext(r.Context(), "printed strip", "event", eventID, "rows", rows, "bytes", len(payload), "kept", keep)

	if keep {
		if _, err := a.store.AddStrip(eventID, id, pic); err != nil {
			// On paper already; the wall missing one strip is the lesser harm.
			a.log.ErrorContext(r.Context(), "cannot keep strip", "event", eventID, "err", err)
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"id": id, "status": "printed"})
}

// Strip ids are trace ids (UUIDs); checked before they become file names.
var validStripID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (a *api) servePNG(w http.ResponseWriter, r *http.Request, eventID, id string, withHidden bool) {
	if !validID.MatchString(eventID) || !validStripID.MatchString(id) {
		a.fail(w, r, http.StatusNotFound, errNoStrip.Error())
		return
	}
	b, err := a.store.StripPNG(eventID, id, withHidden)
	if err != nil {
		a.fail(w, r, http.StatusNotFound, errNoStrip.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(b)
}

// upstreamFailed answers for a failed call to the renderer or dispatcher
// (see upstream.Reply).
func (a *api) upstreamFailed(w http.ResponseWriter, r *http.Request, service string, err error) {
	code, msg, hidden := upstream.Reply(service, err)
	if hidden {
		a.log.ErrorContext(r.Context(), "upstream failed", "service", service, "err", err)
	}
	a.fail(w, r, code, msg)
}

func (a *api) storeFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errNoEvent), errors.Is(err, errNoStrip):
		a.fail(w, r, http.StatusNotFound, err.Error())
	case errors.As(err, new(invalidEvent)):
		a.fail(w, r, http.StatusBadRequest, err.Error())
	default:
		a.log.ErrorContext(r.Context(), "store failed", "err", err)
		a.fail(w, r, http.StatusInternalServerError, "could not save")
	}
}

func (a *api) fail(w http.ResponseWriter, r *http.Request, code int, msg string) {
	httpx.Fail(a.log, w, r, code, msg)
}

// orEmpty makes an empty list encode as [] rather than null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
