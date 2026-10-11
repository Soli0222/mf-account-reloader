// Package failure holds safe diagnostics. Browser errors are never exposed verbatim.
package failure

import (
	"context"
	"errors"
)

type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string           { return e.Message }
func New(code int, message string) error { return &Error{code, message} }
func Code(err error) int {
	if err == nil {
		return 0
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return 5
	}
	return 1
}
func Safe(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Message
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "operation deadline exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "operation canceled"
	}
	return "internal or browser operation failed"
}
