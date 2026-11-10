package lyrics

// ddsonic: tell "LRCLIB was busy or unreachable" apart from "LRCLIB has no
// lyrics for this track" (#82), so the lyrics view does not claim a track
// has no lyrics when the service could not be asked.

import (
	"errors"
	"fmt"
	"net"
	"net/http"
)

// ErrUnavailable is returned by Fetch when LRCLIB could not answer (no
// connection, a timeout, HTTP 429 or a 5xx) and no other source supplied
// lyrics. A retry may find them.
//
// It is also ErrNotFound: errors.Is(err, ErrNotFound) holds, and its text is
// ErrNotFound's, so callers that only know ErrNotFound, such as the IPC
// lyrics reply, behave and read exactly as before.
var ErrUnavailable error = unavailableError{}

type unavailableError struct{}

func (unavailableError) Error() string { return ErrNotFound.Error() }

func (unavailableError) Is(target error) bool { return target == ErrNotFound }

// lrclibStatusError is a non-200 answer from LRCLIB. Its text is upstream's.
type lrclibStatusError struct {
	code   int
	status string
}

func (e lrclibStatusError) Error() string { return fmt.Sprintf("lrclib: %s", e.status) }

func newLRCLIBStatusError(resp *http.Response) error {
	return lrclibStatusError{code: resp.StatusCode, status: resp.Status}
}

// lrclibTransient reports whether an LRCLIB lookup failed because the
// service could not answer, rather than because it has no lyrics: a network
// error or timeout, or HTTP 429 or 5xx. A 404, an empty result, other
// statuses and unreadable answers are not.
func lrclibTransient(err error) bool {
	var status lrclibStatusError
	if errors.As(err, &status) {
		return status.code == http.StatusTooManyRequests || status.code >= 500
	}
	var netErr net.Error // *url.Error from the client, or a timeout reading the body
	return errors.As(err, &netErr)
}

// notFoundAfter is the error Fetch returns when no source has lyrics, given
// the LRCLIB lookup's error.
func notFoundAfter(lrclibErr error) error {
	if lrclibTransient(lrclibErr) {
		return ErrUnavailable
	}
	return ErrNotFound
}
