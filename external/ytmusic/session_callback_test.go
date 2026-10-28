package ytmusic

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The sign-in callback hands on a code only with the flow's state, and a
// second callback never blocks it.
func TestOAuthCallbackChecksState(t *testing.T) {
	codes := make(chan string, 1)
	h := oauthCallback("s3cret", codes)
	tests := []struct {
		query    string
		wantCode int
	}{
		{"?code=evil", http.StatusBadRequest},
		{"?code=evil&state=wrong", http.StatusBadRequest},
		{"?code=good&state=s3cret", http.StatusOK},
		{"?code=again&state=s3cret", http.StatusOK}, // a code is already waiting: dropped, not blocked
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/callback"+tt.query, nil))
		if rec.Code != tt.wantCode {
			t.Errorf("%s: status %d, want %d", tt.query, rec.Code, tt.wantCode)
		}
	}
	if got := <-codes; got != "good" {
		t.Errorf("code = %q, want the one with the state", got)
	}
	select {
	case extra := <-codes:
		t.Errorf("extra code %q handed on", extra)
	default:
	}
}
