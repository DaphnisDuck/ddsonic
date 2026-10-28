package qobuz

import "testing"

func TestRegisterAndIsStreamURL(t *testing.T) {
	const u = "https://streaming-qobuz.example/file/abc123?sig=xyz"
	if IsStreamURL(u) {
		t.Fatalf("url should not be registered yet")
	}
	registerStreamURL(u)
	if !IsStreamURL(u) {
		t.Fatalf("url should be registered after registerStreamURL")
	}
	if IsStreamURL("https://other.example/track") {
		t.Fatalf("unrelated url should not match")
	}
}

func TestRegisterStreamURLEmpty(t *testing.T) {
	registerStreamURL("")
	if IsStreamURL("") {
		t.Fatalf("empty url must never be registered")
	}
}
