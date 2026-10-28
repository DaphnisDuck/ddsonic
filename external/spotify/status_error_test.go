package spotify

// ddsonic: StatusError keeps upstream's message and drives unreadable.

import (
	"errors"
	"fmt"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
)

func TestStatusError(t *testing.T) {
	tests := []struct {
		err       *StatusError
		msg       string
		forbidden bool
	}{
		{statusError(403, "403 Forbidden", []byte(`{"error":1}`), nil), `http status 403 Forbidden: {"error":1}`, true},
		{statusError(404, "404 Not Found", nil, nil), "http status 404 Not Found: ", true},
		{statusError(500, "500 Internal Server Error", nil, errors.New("reset")), "http status 500 Internal Server Error (failed to read body: reset)", false},
		// The code decides, not the text.
		{statusError(502, "502 Bad Gateway", []byte("upstream said http status 404"), nil), "http status 502 Bad Gateway: upstream said http status 404", false},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.msg {
			t.Errorf("Error() = %q, want %q", got, tt.msg)
		}
		wrapped := fmt.Errorf("playlist x: %w", tt.err)
		if got := errors.Is(unreadable(wrapped), catalog.ErrForbidden); got != tt.forbidden {
			t.Errorf("unreadable(%d) forbidden = %v, want %v", tt.err.Code, got, tt.forbidden)
		}
	}
}
