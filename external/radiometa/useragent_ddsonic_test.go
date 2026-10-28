package radiometa

// ddsonic: a now-playing lookup is made as ddsonic, not as cliamp.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bjarneo/cliamp/internal/appmeta"
)

func TestGetJSONIdentifiesAsDdsonic(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.UserAgent()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	var v map[string]any
	if err := getJSON(context.Background(), srv.URL, &v); err != nil {
		t.Fatal(err)
	}
	if got != appmeta.UserAgent() {
		t.Fatalf("User-Agent = %q, want %q", got, appmeta.UserAgent())
	}
}
