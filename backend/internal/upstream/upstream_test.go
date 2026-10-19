package upstream

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jstader/partyfax/backend/internal/doc"
	"github.com/jstader/partyfax/backend/internal/httpx"
)

// The gateway's handler tests use fakes, so this covers the real client: an
// upstream's status has to survive the trip, or the gateway's mapping of
// statuses to answers is meaningless.
func TestClientPreservesUpstreamStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusConflict, map[string]string{"error": "printer is out of paper"})
	}))
	defer srv.Close()

	err := NewDispatcherClient(srv.URL, srv.Client()).Print(context.Background(), "abc", []byte("x"))

	var ue *Error
	if !errors.As(err, &ue) {
		t.Fatalf("error = %v, want an *Error", err)
	}

	if ue.Status != http.StatusConflict || ue.Msg != "printer is out of paper" {
		t.Errorf("got %d %q", ue.Status, ue.Msg)
	}
}

// ESC/POS is not valid UTF-8, so the payload has to survive JSON as bytes.
func TestRendererClientPreservesPayloadBytes(t *testing.T) {
	want := []byte{0x1b, '@', 0x00, 0xff, 'h', 'i'}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, renderResponse{Payload: want})
	}))
	defer srv.Close()

	got, err := NewRendererClient(srv.URL, srv.Client()).
		Render(context.Background(), doc.Document{Text: "hi"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("payload = %v, want %v", got, want)
	}
}

// A service that is down is not the same as one reporting on the printer, and
// must not be mistaken for a deliberate status.
func TestDeadUpstreamIsNotAnUpstreamStatus(t *testing.T) {
	srv := httptest.NewServer(nil)
	url := srv.URL
	srv.Close()

	_, err := NewDispatcherClient(url, http.DefaultClient).State(context.Background())
	if err == nil {
		t.Fatal("want an error from a closed upstream")
	}

	var ue *Error
	if errors.As(err, &ue) {
		t.Errorf("a transport failure became %v; it has no upstream status", ue)
	}
}

// RenderStrip goes to the strip route, not the photo one: the renderer keeps
// their size limits apart.
func TestRenderStripUsesTheStripRoute(t *testing.T) {
	var path, ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, ct = r.URL.Path, r.Header.Get("Content-Type")
		httpx.WriteJSON(w, http.StatusOK, renderResponse{Payload: []byte{1}})
	}))
	defer srv.Close()

	if _, err := NewRendererClient(srv.URL, srv.Client()).RenderStrip(context.Background(), []byte("png")); err != nil {
		t.Fatalf("RenderStrip: %v", err)
	}

	if path != "/internal/render/strip" || ct != "image/png" {
		t.Errorf("posted to %s as %s", path, ct)
	}
}
