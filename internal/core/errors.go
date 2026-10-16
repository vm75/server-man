// Package core defines the shared management boundary used by HTTP and MCP.
package core

import "fmt"

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string { return e.Message }

func Invalid(format string, args ...any) error {
	return &Error{Code: "invalid_arguments", Message: fmt.Sprintf(format, args...), Status: 400}
}

func Unavailable(message string) error {
	return &Error{Code: "unavailable", Message: message, Status: 503}
}
