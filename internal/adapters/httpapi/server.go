// Package httpapi implements the REST (JSON) inbound adapter of the hexagon.
// It exposes a small /api resource tree on Gin — tasks CRUD, a PATCH
// endpoint for partial updates, and a stats endpoint — and depends only on
// the application.TaskService port; nothing here knows which storage
// backend is underneath.
//
// This file (server.go) wires the adapter: Api implements adapters.Adapter
// and Run serves the router on a real TCP listener until shutdown. Address
// resolution: CLI argument -> HEXARCH_HTTP_ADDR -> :8080.
//
// Public API:
//   - Types:   Api
//   - Funcs:   NewApi
//   - Methods: Api.Run
package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	"hexarch/internal/adapters"
	"hexarch/internal/application"
)

// Api is the HTTP adapter implementing adapters.Adapter. Run starts a real
// HTTP server and blocks until it is shut down.
type Api struct {
	*adapters.AppBase
}

// NewApi returns an HTTP adapter listening on the given address. Pass an
// empty address to use the default ":8080".
func NewApi(svc application.TaskService) *Api {
	return &Api{AppBase: adapters.NewAppBase("api", svc)}
}

// Run implements adapters.Adapter: it serves the REST API on the configured
// address. It blocks until the server stops; a non-nil error is returned if
// the listener cannot be opened or the server fails.
func (a *Api) Run(ctx context.Context, args []string) error {
	addr := os.Getenv("HEXARCH_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if len(args) == 1 && args[0] != "" {
		addr = args[0]
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", addr, err)
	}

	router := NewRouter(a.Svc)

	srv := &http.Server{
		Handler:     router,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	return srv.Serve(ln)
}
