package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jstader/partyfax/backend/internal/doc"
	"github.com/jstader/partyfax/backend/internal/logging"
	"github.com/jstader/partyfax/backend/internal/upstream"
)

type fakeRenderer struct {
	got []byte
	err error
}

func (f *fakeRenderer) RenderStrip(_ context.Context, png []byte) ([]byte, error) {
	f.got = png
	return []byte("\x1b@raster"), f.err
}

type fakeDispatcher struct {
	ids []string
	err error
}

func (f *fakeDispatcher) Print(_ context.Context, id string, _ []byte) error {
	f.ids = append(f.ids, id)
	return f.err
}

func (f *fakeDispatcher) State(context.Context) (upstream.State, error) {
	return upstream.State{Online: true}, nil
}

type rig struct {
	api *api
	h   http.Handler
	r   *fakeRenderer
	d   *fakeDispatcher
	ev  Event
}

func newRig(t *testing.T, edit func(*Event)) *rig {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := theme()
	if edit != nil {
		edit(&e)
	}
	e, err = s.Create(e)
	if err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r, d := &fakeRenderer{}, &fakeDispatcher{}
	a := &api{renderer: r, dispatcher: d, store: s, log: log}
	return &rig{api: a, h: logging.Edge(log)(a.routes()), r: r, d: d, ev: e}
}

func (g *rig) do(method, path, code string, body io.Reader, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if code != "" {
		req.Header.Set(codeHeader, code)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

func stripPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestJoin(t *testing.T) {
	g := newRig(t, nil)

	rec := g.do("POST", "/api/booth/join", "", strings.NewReader(`{"code":"`+strings.ToLower(g.ev.Code)+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("join = %d %s", rec.Code, rec.Body)
	}
	var th Theme
	json.Unmarshal(rec.Body.Bytes(), &th)
	if th.Title != g.ev.Title || th.Frame != g.ev.Frame {
		t.Errorf("theme = %+v", th)
	}
	if strings.Contains(rec.Body.String(), g.ev.ID) || strings.Contains(rec.Body.String(), g.ev.Code) {
		t.Error("join leaks the event id or code")
	}

	if rec := g.do("POST", "/api/booth/join", "", strings.NewReader(`{"code":"ZZZZZZ"}`)); rec.Code != http.StatusNotFound {
		t.Errorf("wrong code = %d, want 404", rec.Code)
	}
}

func TestPrintKeepsTheStripForTheWall(t *testing.T) {
	g := newRig(t, nil)
	body := stripPNG(t, doc.PhotoWidth, doc.StripMaxRows)

	rec := g.do("POST", "/api/booth/print", g.ev.Code, bytes.NewReader(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("print = %d %s", rec.Code, rec.Body)
	}
	if !bytes.Equal(g.r.got, body) || len(g.d.ids) != 1 {
		t.Fatal("strip didn't go through the renderer and dispatcher")
	}

	strips := g.api.store.Strips(g.ev.ID, false)
	if len(strips) != 1 || strips[0].ID != g.d.ids[0] {
		t.Fatalf("stored %+v, want the printed job %s", strips, g.d.ids[0])
	}

	rec = g.do("GET", "/api/booth/strips/"+strips[0].ID, g.ev.Code, nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), body) || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("fetching the strip = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestPrintRefuses(t *testing.T) {
	g := newRig(t, nil)
	cases := map[string]struct {
		code string
		body []byte
		want int
	}{
		"no code":     {"", stripPNG(t, doc.PhotoWidth, 10), http.StatusNotFound},
		"wrong code":  {"ZZZZZZ", stripPNG(t, doc.PhotoWidth, 10), http.StatusNotFound},
		"too tall":    {g.ev.Code, stripPNG(t, doc.PhotoWidth, doc.StripMaxRows+1), http.StatusBadRequest},
		"wrong width": {g.ev.Code, stripPNG(t, doc.PhotoWidth+1, 10), http.StatusBadRequest},
		"not a png":   {g.ev.Code, []byte("hello"), http.StatusBadRequest},
		"too big":     {g.ev.Code, bytes.Repeat([]byte("x"), maxStrip+1), http.StatusBadRequest},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if rec := g.do("POST", "/api/booth/print", c.code, bytes.NewReader(c.body)); rec.Code != c.want {
				t.Errorf("= %d %s, want %d", rec.Code, rec.Body, c.want)
			}
		})
	}
	if len(g.d.ids) != 0 {
		t.Errorf("%d refused strips reached the printer", len(g.d.ids))
	}
}

func TestClosedEventCannotPrint(t *testing.T) {
	g := newRig(t, func(e *Event) { e.Active = false })
	if rec := g.do("POST", "/api/booth/print", g.ev.Code, bytes.NewReader(stripPNG(t, doc.PhotoWidth, 10))); rec.Code != http.StatusNotFound {
		t.Errorf("print to a closed event = %d, want 404", rec.Code)
	}
}

// The printer's answers reach the guest's page as they are, so it can say
// "busy, trying again" rather than "something went wrong".
func TestPrinterAnswersPassThrough(t *testing.T) {
	g := newRig(t, nil)
	g.d.err = &upstream.Error{Service: "dispatcher", Status: http.StatusConflict, Msg: "printer is busy"}

	rec := g.do("POST", "/api/booth/print", g.ev.Code, bytes.NewReader(stripPNG(t, doc.PhotoWidth, 10)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "busy") {
		t.Errorf("= %d %s, want 409 busy", rec.Code, rec.Body)
	}
	if n := len(g.api.store.Strips(g.ev.ID, true)); n != 0 {
		t.Errorf("a strip that didn't print was kept (%d)", n)
	}
}

func TestWallOffIsForbidden(t *testing.T) {
	g := newRig(t, func(e *Event) { e.Wall = false })
	if rec := g.do("GET", "/api/booth/strips", g.ev.Code, nil); rec.Code != http.StatusForbidden {
		t.Errorf("strips with the wall off = %d, want 403", rec.Code)
	}
}

func TestStripIDsAreCheckedBeforeTheyBecomePaths(t *testing.T) {
	g := newRig(t, nil)
	for _, id := range []string{"..%2Fevents.json", "x", strings.Repeat("a", 36)} {
		if rec := g.do("GET", "/api/booth/strips/"+id, g.ev.Code, nil); rec.Code != http.StatusNotFound {
			t.Errorf("strip %q = %d, want 404", id, rec.Code)
		}
	}
}

func TestAdminIsClosedWithoutAccess(t *testing.T) {
	g := newRig(t, nil)
	if rec := g.do("GET", "/api/booth/admin/events", "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("unconfigured admin = %d, want 403", rec.Code)
	}

	g.api.access = newFakeAccess(t).access(now)
	if rec := g.do("GET", "/api/booth/admin/events", "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("admin without a token = %d, want 403", rec.Code)
	}
	// An event code is not an admin credential.
	if rec := g.do("GET", "/api/booth/admin/events", g.ev.Code, nil); rec.Code != http.StatusForbidden {
		t.Errorf("admin with an event code = %d, want 403", rec.Code)
	}
}

func TestAdminCreatesAndValidatesEvents(t *testing.T) {
	g := newRig(t, nil)
	f := newFakeAccess(t)
	g.api.access = f.access(now)
	tok := f.token(t, map[string]any{"alg": "RS256", "kid": "k1"}, goodClaims())

	good := `{"title":"Wedding","subtitle":"","font":"script","frame":"double","icons":["heart"],"active":true,"wall":false}`
	rec := g.do("POST", "/api/booth/admin/events", "", strings.NewReader(good), accessHeader, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var e Event
	json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Code == "" || e.Title != "Wedding" {
		t.Errorf("created %+v", e)
	}

	bad := strings.Replace(good, `"double"`, `"wavy"`, 1)
	if rec := g.do("POST", "/api/booth/admin/events", "", strings.NewReader(bad), accessHeader, tok); rec.Code != http.StatusBadRequest {
		t.Errorf("bad frame = %d, want 400", rec.Code)
	}
	unknown := strings.Replace(good, `"wall":false`, `"wall":false,"id":"x","extra":1`, 1)
	if rec := g.do("POST", "/api/booth/admin/events", "", strings.NewReader(unknown), accessHeader, tok); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field = %d, want 400", rec.Code)
	}
}

func TestTestPrintIsNotKept(t *testing.T) {
	g := newRig(t, func(e *Event) { e.Active = false })
	f := newFakeAccess(t)
	g.api.access = f.access(now)
	tok := f.token(t, map[string]any{"alg": "RS256", "kid": "k1"}, goodClaims())

	rec := g.do("POST", "/api/booth/admin/events/"+g.ev.ID+"/test", "", bytes.NewReader(stripPNG(t, doc.PhotoWidth, 100)), accessHeader, tok)
	if rec.Code != http.StatusOK || len(g.d.ids) != 1 {
		t.Fatalf("test print = %d %s", rec.Code, rec.Body)
	}
	if n := len(g.api.store.Strips(g.ev.ID, true)); n != 0 {
		t.Errorf("test print was kept (%d strips)", n)
	}
}

func TestReprintPrintsTheStoredStripWithoutKeepingItAgain(t *testing.T) {
	g := newRig(t, nil)
	body := stripPNG(t, doc.PhotoWidth, 50)
	g.do("POST", "/api/booth/print", g.ev.Code, bytes.NewReader(body))
	id := g.d.ids[0]

	rec := g.do("POST", "/api/booth/reprint", g.ev.Code, strings.NewReader(`{"id":"`+id+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("reprint = %d %s", rec.Code, rec.Body)
	}
	if len(g.d.ids) != 2 || !bytes.Equal(g.r.got, body) {
		t.Error("reprint didn't send the stored strip to the printer")
	}
	if n := len(g.api.store.Strips(g.ev.ID, true)); n != 1 {
		t.Errorf("%d strips kept, want 1", n)
	}

	// Not someone else's strip, not a hidden one, not without the code.
	if rec := g.do("POST", "/api/booth/reprint", g.ev.Code, strings.NewReader(`{"id":"8f0e0d4a-1b2c-4d5e-8f90-a1b2c3d4e5f6"}`)); rec.Code != http.StatusNotFound {
		t.Errorf("unknown strip = %d, want 404", rec.Code)
	}
	if rec := g.do("POST", "/api/booth/reprint", "", strings.NewReader(`{"id":"`+id+`"}`)); rec.Code != http.StatusNotFound {
		t.Errorf("no code = %d, want 404", rec.Code)
	}
	g.api.store.SetHidden(g.ev.ID, id, true)
	if rec := g.do("POST", "/api/booth/reprint", g.ev.Code, strings.NewReader(`{"id":"`+id+`"}`)); rec.Code != http.StatusNotFound {
		t.Errorf("hidden strip = %d, want 404", rec.Code)
	}
}

func adminRig(t *testing.T) (*rig, string) {
	t.Helper()
	g := newRig(t, nil)
	f := newFakeAccess(t)
	g.api.access = f.access(now)
	return g, f.token(t, map[string]any{"alg": "RS256", "kid": "k1"}, goodClaims())
}

func TestAdminDeletesAStrip(t *testing.T) {
	g, tok := adminRig(t)
	g.do("POST", "/api/booth/print", g.ev.Code, bytes.NewReader(stripPNG(t, doc.PhotoWidth, 10)))
	id := g.d.ids[0]

	rec := g.do("DELETE", "/api/booth/admin/events/"+g.ev.ID+"/strips/"+id, "", nil, accessHeader, tok)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if n := len(g.api.store.Strips(g.ev.ID, true)); n != 0 {
		t.Errorf("%d strips left", n)
	}
	if _, err := g.api.store.StripPNG(g.ev.ID, id, true); err == nil {
		t.Error("picture still served")
	}
	if rec := g.do("DELETE", "/api/booth/admin/events/"+g.ev.ID+"/strips/"+id, "", nil, accessHeader, tok); rec.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", rec.Code)
	}
}

func TestAdminZipHasEveryStripEnlarged(t *testing.T) {
	g, tok := adminRig(t)
	for range 2 {
		g.do("POST", "/api/booth/print", g.ev.Code, bytes.NewReader(stripPNG(t, doc.PhotoWidth, 20)))
	}
	g.api.store.SetHidden(g.ev.ID, g.d.ids[0], true)

	rec := g.do("GET", "/api/booth/admin/events/"+g.ev.ID+"/strips.zip", "", nil, accessHeader, tok)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("zip = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `"john-s-40th-strips.zip"`) {
		t.Errorf("Content-Disposition = %s", cd)
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("%d files, want both strips (hidden included)", len(zr.File))
	}
	f, _ := zr.File[0].Open()
	cfg, err := png.DecodeConfig(f)
	if err != nil || cfg.Width != doc.PhotoWidth*albumScale || cfg.Height != 20*albumScale {
		t.Errorf("enlarged strip is %dx%d (%v), want %dx%d", cfg.Width, cfg.Height, err, doc.PhotoWidth*albumScale, 20*albumScale)
	}
}

func TestJoinCarriesTheCountdowns(t *testing.T) {
	g := newRig(t, func(e *Event) { e.CountFirst, e.CountNext = 7, 4 })
	rec := g.do("POST", "/api/booth/join", "", strings.NewReader(`{"code":"`+g.ev.Code+`"}`))
	var th Theme
	json.Unmarshal(rec.Body.Bytes(), &th)
	if th.CountFirst != 7 || th.CountNext != 4 {
		t.Errorf("theme countdowns = %d, %d; want 7, 4", th.CountFirst, th.CountNext)
	}
}
