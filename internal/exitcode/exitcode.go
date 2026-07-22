// Package exitcode defines the CLI's semantic process exit codes and a typed
// error that carries one. main maps command errors onto these codes via
// errors.As.
package exitcode

import "fmt"

// Code is a semantic process exit code.
type Code int

const (
	// Success means the command completed without error.
	Success Code = 0
	// GenericError covers failures with no more specific classification.
	GenericError Code = 1
	// UsageError marks invalid flags, arguments, or input validation failures.
	UsageError Code = 2
	// AuthError marks authentication or authorization failures (HTTP 401/403,
	// missing credentials).
	AuthError Code = 3
	// NotFound marks missing resources (HTTP 404, unknown project/preview/org).
	NotFound Code = 4
	// Conflict marks conflict or failed-precondition errors (HTTP 409/412/428).
	Conflict Code = 5
	// Timeout marks deadline or wait-timeout failures (HTTP 408/504, --wait-timeout).
	Timeout Code = 6
)

// Error wraps an error with a semantic exit code. It unwraps to the underlying
// error so errors.Is/As keep working through it.
type Error struct {
	Code Code
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit code %d", int(e.Code))
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error {
	return e.Err
}

// New wraps err with the given exit code. A nil err returns nil.
func New(code Code, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Err: err}
}

// Errorf builds a formatted error carrying the given exit code.
func Errorf(code Code, format string, args ...any) error {
	return &Error{Code: code, Err: fmt.Errorf(format, args...)}
}

// FromHTTPStatus maps an HTTP response status onto a semantic exit code.
func FromHTTPStatus(status int) Code {
	switch status {
	case 401, 403:
		return AuthError
	case 404, 410:
		return NotFound
	case 409, 412, 428:
		return Conflict
	case 408, 504:
		return Timeout
	default:
		return GenericError
	}
}
