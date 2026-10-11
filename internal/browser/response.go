package browser

import (
	"fmt"
	"github.com/Soli0222/mf-account-reloader/internal/failure"
)

// AccessDeniedError distinguishes a denied document from expired cookies or
// a changed account DOM. Its diagnostic never includes a response body or URL.
type AccessDeniedError struct{ Status int }

func (e *AccessDeniedError) Error() string {
	return fmt.Sprintf("site access denied (HTTP %d); check authentication with --headless=false", e.Status)
}
func (e *AccessDeniedError) Unwrap() error { return failure.New(3, e.Error()) }
func ResponseError(status int) error {
	switch status {
	case 401, 403:
		return &AccessDeniedError{Status: status}
	case 429:
		return failure.New(1, "site request was rate limited (HTTP 429)")
	}
	if status >= 500 {
		return failure.New(1, fmt.Sprintf("site request failed (HTTP %d)", status))
	}
	return nil
}
