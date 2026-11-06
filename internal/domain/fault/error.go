// Package fault contains transport-independent, safe-to-display operation errors.
package fault

import "fmt"

type Error struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Cause   error  `json:"-"`
}

func (e *Error) Error() string          { return e.Reason + ": " + e.Message }
func (e *Error) Unwrap() error          { return e.Cause }
func New(reason, message string) *Error { return &Error{Reason: reason, Message: message} }
func Invalid(format string, args ...any) error {
	return New("invalid_argument", fmt.Sprintf(format, args...))
}
func Forbidden(message string) error       { return New("forbidden", message) }
func Disabled(message string) error        { return New("feature_disabled", message) }
func Conflict(message string) error        { return New("conflict", message) }
func NotFound(message string) error        { return New("not_found", message) }
func Unauthenticated(message string) error { return New("unauthenticated", message) }
func Unavailable(message string) error     { return New("upstream_unavailable", message) }
func Unknown(message string) error         { return New("execution_unknown", message) }
