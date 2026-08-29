// errors.go implements the typed error contract of the domain layer.
//
// Every error that crosses a hexagon boundary is a *DomainError carrying a
// Kind, so higher layers (application, adapters) can map failures to HTTP
// statuses or user-facing messages without inspecting internals.
//
// Public API:
//   - Types:  Kind, DomainError
//   - Values: KindNotFound, KindInvalid, KindConflict, KindStorage
//   - Funcs:  NotFound, Invalid, Conflict, Storage
//   - Methods: DomainError.Error, DomainError.Kind
package domain

// Kind classifies domain failures so that higher layers can react
// without reaching into implementation details.
type Kind string

const (
	KindNotFound Kind = "NOT_FOUND"
	KindInvalid  Kind = "INVALID_ARGUMENT"
	KindConflict Kind = "CONFLICT"
	KindStorage  Kind = "STORAGE"
)

// DomainError is the shared error type across the hexagon.
type DomainError struct {
	kind Kind
	msg  string
}

func (e *DomainError) Error() string { return e.msg }
func (e *DomainError) Kind() Kind    { return e.kind }

func NotFound(msg string) *DomainError { return &DomainError{KindNotFound, msg} }
func Invalid(msg string) *DomainError  { return &DomainError{KindInvalid, msg} }
func Conflict(msg string) *DomainError { return &DomainError{KindConflict, msg} }
func Storage(msg string) *DomainError  { return &DomainError{KindStorage, msg} }
