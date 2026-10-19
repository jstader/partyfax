package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/jstader/partyfax/backend/internal/doc"
	"github.com/jstader/partyfax/backend/internal/httpx"
	"github.com/jstader/partyfax/backend/internal/logging"
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
	log        *slog.Logger
}

func (a *api) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Guests: everything takes the event code.
	mux.HandleFunc("POST /api/booth/join", a.join)
	mux.HandleFunc("POST /api/booth/print", a.print)
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

	e, ok := a.store.ByCode(in.Code)
	if !ok {
		a.fail(w, r, http.StatusNotFound, "no open event with that code")
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

	r.Body = http.MaxBytesReader(w, r.Body, maxStrip)
	png, cfg, err := doc.ReadStrip(r.Body)
	if err != nil {
		a.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}

	a.printStrip(w, r, png, cfg.Height, e.ID, true)
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

// guest is the event the request's code opens, or a 404 already written.
func (a *api) guest(w http.ResponseWriter, r *http.Request) (Event, bool) {
	e, ok := a.store.ByCode(r.Header.Get(codeHeader))
	if !ok {
		a.fail(w, r, http.StatusNotFound, "no open event with that code")
	}
	return e, ok
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
		a.log.InfoContext(r.Context(), "admin", "email", email, "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
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

	r.Body = http.MaxBytesReader(w, r.Body, maxStrip)
	png, cfg, err := doc.ReadStrip(r.Body)
	if err != nil {
		a.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}

	a.printStrip(w, r, png, cfg.Height, e.ID, false)
}

func (a *api) readEvent(w http.ResponseWriter, r *http.Request) (Event, bool) {
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

// printStrip renders, prints and, if keep, stores. The trace id is the job
// id and the strip id, so one grep follows a strip from upload to paper.
func (a *api) printStrip(w http.ResponseWriter, r *http.Request, png []byte, rows int, eventID string, keep bool) {
	payload, err := a.renderer.RenderStrip(r.Context(), png)
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
		if _, err := a.store.AddStrip(eventID, id, png); err != nil {
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

// The statuses the dispatcher and renderer use that are about the request or
// the printer, so the guest's page can say something useful: 400 a bad
// strip, 409 no paper or busy, 503 offline, 504 no confirmation. The same
// set as the public site's gateway.
var passThrough = map[int]bool{
	http.StatusBadRequest:         true,
	http.StatusConflict:           true,
	http.StatusServiceUnavailable: true,
	http.StatusGatewayTimeout:     true,
}

func (a *api) upstreamFailed(w http.ResponseWriter, r *http.Request, service string, err error) {
	var ue *upstream.Error
	if errors.As(err, &ue) && passThrough[ue.Status] {
		a.fail(w, r, ue.Status, ue.Msg)
		return
	}
	a.log.ErrorContext(r.Context(), "upstream failed", "service", service, "err", err)
	a.fail(w, r, http.StatusBadGateway, service+" is unavailable")
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
	level := slog.LevelWarn
	if code >= http.StatusInternalServerError {
		level = slog.LevelError
	}
	a.log.Log(r.Context(), level, "rejected", "reason", msg)
	httpx.WriteJSON(w, code, map[string]string{"error": msg})
}

// orEmpty makes an empty list encode as [] rather than null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
