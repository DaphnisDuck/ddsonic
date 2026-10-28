package lyrics

// ddsonic: a lyrics lookup at LRCLIB is made as ddsonic, not as cliamp.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bjarneo/cliamp/internal/appmeta"
)

func TestFetchLRCLIBIdentifiesAsDdsonic(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.UserAgent()
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	installTestClient(t, srv.URL)

	_, _ = fetchLRCLIB("some artist some title")
	if got != appmeta.UserAgent() {
		t.Fatalf("User-Agent = %q, want %q", got, appmeta.UserAgent())
	}
}
