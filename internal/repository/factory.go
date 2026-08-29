// factory.go implements the repository factory and the provider registry.
//
// Backends register themselves (via Register, usually from init()) and the
// factory resolves a Config.Type to the matching Provider, so a new storage
// engine can be added without touching the factory body. New is a pure
// function of Config: tests may build Config values directly.
//
// Public API:
//   - Interface: Provider
//   - Funcs:     Register, New, NopCloser
//
// Private:
//   - registryMu, registry, lookup, nopCloser
package repository

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// provider opens a backend and returns the outbound port plus a closer that
// owns the underlying resources (a *sql.DB, a Mongo client, ...). Pool,
// ping, schema setup, and lifecycle all live here — inside the backend —
// never in the port or the application layer.
type Provider interface {
	Open(ctx context.Context, uri string) (TaskRepository, io.Closer, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[DBType]Provider{}
)

// Register makes a backend available to the factory. Adapter packages call
// this from their init(), so merely importing the package registers it:
//
//	_ "hexarch/internal/repository/sqlite"
//
// Register is also the extension point for bespoke backends.
func Register(t DBType, p Provider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[t] = p
}

// lookup returns the provider registered for t, or ok == false when none is
// registered.
func lookup(t DBType) (Provider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[t]
	return p, ok
}

// New is the repository factory: it resolves cfg.Type against the registry and
// opens the backend, returning the port and the closer for the caller to
// defer. main (the composition root) owns the returned closer.
func New(ctx context.Context, cfg Config) (TaskRepository, io.Closer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	p, ok := lookup(cfg.Type)
	if !ok {
		return nil, nil, fmt.Errorf("repository: backend %q is not registered (supported: %s)",
			cfg.Type, SupportedTypesString())
	}
	return p.Open(ctx, cfg.URI)
}

// NopCloser is returned by backends with no resources to release.
func NopCloser() io.Closer { return nopCloser{} }

type nopCloser struct{}

// Close implements io.Closer with no work; resource-free providers (e.g.
// memory) return nopCloser via NopCloser.
func (nopCloser) Close() error { return nil }
