// Package httpweb implements the server-rendered web UI inbound adapter of
// the hexagon: Gin + Go html/template pages styled with DaisyUI/Tailwind and
// driven by HTMX fragments. Like httpapi it depends only on the
// application.TaskService port.
//
// This file (server.go) wires the adapter: Web implements adapters.Adapter
// and Run serves the router on a real TCP listener until shutdown. Address
// resolution: CLI argument -> HEXARCH_HTTP_ADDR -> :8081.
//
// Public API:
//   - Types:   Web
//   - Funcs:   NewWeb
//   - Methods: Web.Run
package httpweb

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	"hexarch/internal/adapters"
	"hexarch/internal/application"
)

// Web is the web UI adapter implementing adapters.Adapter. Run starts a real
// HTTP server and blocks until it is shut down.
type Web struct {
	*adapters.AppBase
}

// NewWeb returns an HTTP adapter listening on the given address. Pass an
// empty address to use the default ":8081".
func NewWeb(svc application.TaskService) *Web {
	return &Web{AppBase: adapters.NewAppBase("web", svc)}
}

// Run implements adapters.Adapter: it serves the Web App on the configured
// address. It blocks until the server stops; a non-nil error is returned if
// the listener cannot be opened or the server fails.
func (a *Web) Run(ctx context.Context, args []string) error {
	addr := os.Getenv("HEXARCH_HTTP_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	if len(args) == 1 && args[0] != "" {
		addr = args[0]
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", addr, err)
	}

	router, err := NewRouter(a.Svc)
	if err != nil {
		return fmt.Errorf("cannot create router: %w", err)
	}

	srv := &http.Server{
		Handler:     router,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	return srv.Serve(ln)
}
