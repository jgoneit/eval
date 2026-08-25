package store

import (
	"errors"
	"fmt"
	"os"
)

// Category is a stable machine-readable store failure class.
type Category string

const (
	CategoryUnsafePath  Category = "unsafe-path"
	CategoryPermission  Category = "permission"
	CategoryLockTimeout Category = "lock-timeout"
	CategoryValidation  Category = "validation"
	CategoryIO          Category = "io"
)

var (
	ErrUnsafePath  = errors.New("unsafe state path")
	ErrPermission  = errors.New("private state permission or ownership violation")
	ErrLockTimeout = errors.New("state lock timeout")
	ErrValidation  = errors.New("state validation failed")
	ErrIO          = errors.New("state I/O failed")
)

// Error records detailed diagnostics. Use SafeReason when emitting a
// best-effort result because Error may contain a private filesystem path.
type Error struct {
	Category Category
	Op       string
	Path     string
	Err      error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("store %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("store %s %q: %v", e.Op, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// CategoryOf returns the stable category for a store error.
func CategoryOf(err error) Category {
	var target *Error
	if errors.As(err, &target) {
		return target.Category
	}
	return CategoryIO
}

// SafeReason returns a path-free reason suitable for best-effort JSON output.
func SafeReason(err error) string {
	switch CategoryOf(err) {
	case CategoryUnsafePath:
		return "unsafe-state-path"
	case CategoryPermission:
		return "state-permission-denied"
	case CategoryLockTimeout:
		return "state-lock-timeout"
	case CategoryValidation:
		return "invalid-state-data"
	default:
		return "state-io-error"
	}
}

func storeError(category Category, op, path string, err error) error {
	return &Error{Category: category, Op: op, Path: path, Err: err}
}

func classifyError(op, path string, err error) error {
	if err == nil {
		return nil
	}
	var existing *Error
	if errors.As(err, &existing) {
		return err
	}
	if errors.Is(err, os.ErrPermission) {
		return storeError(CategoryPermission, op, path, errors.Join(ErrPermission, err))
	}
	return storeError(CategoryIO, op, path, errors.Join(ErrIO, err))
}

func classifyLockError(op, path string, err error) error {
	if errors.Is(err, ErrLockTimeout) {
		return storeError(CategoryLockTimeout, op, path, err)
	}
	return classifyError(op, path, err)
}
