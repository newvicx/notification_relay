package api

import (
	"errors"
	"net/http"
)

// coreError carries an HTTP status alongside a message, so logic shared
// between JSON API handlers and UI handlers can report failures without
// either caller having to re-derive a status code from scratch.
type coreError struct {
	status int
	msg    string
}

func (e *coreError) Error() string { return e.msg }

func newCoreError(status int, msg string) error {
	return &coreError{status: status, msg: msg}
}

// writeCoreError translates an error from a *Core method into an HTTP
// response for the JSON API. UI handlers inspect the error directly instead
// (e.g. to render a flash message or a 404 page) rather than calling this.
func writeCoreError(w http.ResponseWriter, err error) {
	var ce *coreError
	if errors.As(err, &ce) {
		http.Error(w, ce.msg, ce.status)
		return
	}
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
