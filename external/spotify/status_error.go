package spotify

// ddsonic: a typed Web API status error, so callers match the status
// code instead of the message text.

import "fmt"

// StatusError is a Web API response with an unexpected HTTP status. Its
// message is the one upstream formats ("http status 404 Not Found: …").
type StatusError struct {
	Code    int
	Status  string // e.g. "404 Not Found"
	Body    []byte // up to 512 bytes of the response body
	ReadErr error  // why the body could not be read, if it could not
}

func (e *StatusError) Error() string {
	if e.ReadErr != nil {
		return fmt.Sprintf("http status %s (failed to read body: %v)", e.Status, e.ReadErr)
	}
	return fmt.Sprintf("http status %s: %s", e.Status, e.Body)
}

func statusError(code int, status string, body []byte, readErr error) *StatusError {
	return &StatusError{Code: code, Status: status, Body: body, ReadErr: readErr}
}
