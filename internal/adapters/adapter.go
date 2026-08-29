// Package adapters defines the inbound side of the hexagon: the Adapter
// interface every driver (cli, httpapi, httpweb) implements, plus AppBase, a
// small embedded base that carries shared state (name and application
// service) so every adapter starts from the same composition.
//
// This file (adapter.go) is the whole package.
//
// Public API:
//   - Interface: Adapter
//   - Type:      AppBase
//   - Func:      NewAppBase
package adapters

import (
	"context"

	"hexarch/internal/application"
)

// Adapter is the shared inbound boundary: anything that can Run is an
// "application" from the composition root's point of view.
type Adapter interface {
	// Run executes the adapter with the given arguments and blocks until it
	// terminates; it returns an error when the adapter cannot complete.
	Run(ctx context.Context, args []string) error
}

// AppBase carries the state shared by every adapter: the adapter name and
// the application service it drives. Embedding AppBase gives each adapter a
// Svc field and a common constructor without duplicating code (composition
// over inheritance: override/extend parts without touching the others).
type AppBase struct {
	name string
	Svc  application.TaskService
}

// NewAppBase builds the shared base for an adapter.
func NewAppBase(name string, svc application.TaskService) *AppBase {
	return &AppBase{name: name, Svc: svc}
}
