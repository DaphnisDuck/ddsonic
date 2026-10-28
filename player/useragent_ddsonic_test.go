package player

// ddsonic: an HTTP stream is opened as ddsonic, not as cliamp.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bjarneo/cliamp/internal/appmeta"
)

func TestOpenSourceIdentifiesAsDdsonic(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.UserAgent()
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("x"))
	}))
	t.Cleanup(srv.Close)

	res, err := openSource(srv.URL+"/stream.mp3", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.body.Close()
	if got != appmeta.UserAgent() {
		t.Fatalf("User-Agent = %q, want %q", got, appmeta.UserAgent())
	}
}
