// Command booth is the photobooth's back end: events and their themes, the
// strips guests print, and each event's private wall. It prints through the
// same renderer and dispatcher as the public site, and is reached only at
// photobooth.nilanet.live.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/jstader/partyfax/backend/internal/httpx"
	"github.com/jstader/partyfax/backend/internal/logging"
	"github.com/jstader/partyfax/backend/internal/timeouts"
	"github.com/jstader/partyfax/backend/internal/upstream"
)

// Rendering is quick, so its client has a flat timeout. Printing waits on
// the printer and gets a per-request deadline instead (see timeouts.Print).
const renderTimeout = 5 * time.Second

func main() {
	addr := flag.String("addr", ":8083", "listen address")
	rendererURL := flag.String("renderer", "http://localhost:8081", "renderer service base URL")
	dispatcherURL := flag.String("dispatcher", "http://localhost:8082", "dispatcher service base URL")
	dataDir := flag.String("data-dir", "/var/lib/booth", "events and printed strips live here")
	logFlags := logging.RegisterFlags()
	flag.Parse()

	log, err := logFlags.Logger()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	store, err := OpenStore(*dataDir)
	if err != nil {
		log.Error("cannot open the store", "path", *dataDir, "err", err)
		os.Exit(1)
	}

	// From the environment, like every secret-adjacent setting. Both unset
	// leaves admin off, which is the safe way to be misconfigured.
	access := NewAccess(os.Getenv("CF_ACCESS_TEAM_DOMAIN"), os.Getenv("CF_ACCESS_AUD"),
		&http.Client{Timeout: 10 * time.Second})
	if access == nil {
		log.Warn("CF_ACCESS_TEAM_DOMAIN or CF_ACCESS_AUD is unset; the admin API refuses everything")
	}

	a := &api{
		renderer:   upstream.NewRendererClient(*rendererURL, &http.Client{Timeout: renderTimeout}),
		dispatcher: upstream.NewDispatcherClient(*dispatcherURL, &http.Client{}),
		store:      store,
		access:     access,
		log:        log,
	}

	srv := &http.Server{
		Addr: *addr,
		// Edge, not Requests: a caller must never choose the job id.
		Handler:           logging.Edge(log)(a.routes()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// Must exceed the longest print, as for the gateway.
		WriteTimeout: timeouts.Write,
		IdleTimeout:  60 * time.Second,
	}

	if err := httpx.Serve(srv, log, "data", *dataDir, "admin", access != nil); err != nil {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}
