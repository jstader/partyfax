package main

import (
	"net/http"

	"github.com/jstader/partyfax/backend/internal/upstream"
)

// The clients live in internal/upstream, shared with the booth. These names
// keep the gateway's handlers and tests as they were.
type (
	Renderer      = upstream.Renderer
	Dispatcher    = upstream.Dispatcher
	State         = upstream.State
	upstreamError = upstream.Error
)

func NewRendererClient(base string, hc *http.Client) Renderer {
	return upstream.NewRendererClient(base, hc)
}

var NewDispatcherClient = upstream.NewDispatcherClient
