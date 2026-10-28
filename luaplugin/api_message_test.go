package luaplugin

import (
	"testing"
	"time"
)

func TestMessageAPIDeliversTextAndDuration(t *testing.T) {
	m := newTestManager()
	var gotText string
	var gotDur time.Duration
	m.SetUIProvider(UIProvider{
		ShowMessage: func(text string, duration time.Duration) {
			gotText = text
			gotDur = duration
		},
	})

	loadTestPlugin(t, m, "msg-test", `
		plugin.register({name = "msg-test", type = "hook"})
		cliamp.message("Scrobble Sent", 2)
	`)

	if gotText != "Scrobble Sent" {
		t.Fatalf("text = %q, want %q", gotText, "Scrobble Sent")
	}
	if gotDur != 2*time.Second {
		t.Fatalf("duration = %v, want 2s", gotDur)
	}
}

func TestMessageAPIDefaultsDurationToZero(t *testing.T) {
	m := newTestManager()
	var gotDur time.Duration
	seen := false
	m.SetUIProvider(UIProvider{
		ShowMessage: func(_ string, duration time.Duration) {
			gotDur = duration
			seen = true
		},
	})

	loadTestPlugin(t, m, "msg-default", `
		plugin.register({name = "msg-default", type = "hook"})
		cliamp.message("hello")
	`)

	if !seen {
		t.Fatal("ShowMessage was not called")
	}
	if gotDur != 0 {
		t.Fatalf("duration = %v, want 0 (UI decides default)", gotDur)
	}
}

func TestMessageAPIClampsMaxDuration(t *testing.T) {
	m := newTestManager()
	var gotDur time.Duration
	m.SetUIProvider(UIProvider{
		ShowMessage: func(_ string, duration time.Duration) { gotDur = duration },
	})

	loadTestPlugin(t, m, "msg-clamp", `
		plugin.register({name = "msg-clamp", type = "hook"})
		cliamp.message("long", 9999)
	`)

	if gotDur != messageMaxDuration {
		t.Fatalf("duration = %v, want %v (clamped)", gotDur, messageMaxDuration)
	}
}

func TestMessageAPIWithoutProviderIsNoop(t *testing.T) {
	m := newTestManager()
	// No SetUIProvider call — ShowMessage is nil.
	loadTestPlugin(t, m, "msg-noop", `
		plugin.register({name = "msg-noop", type = "hook"})
		cliamp.message("nobody listening")
	`)
	// Success = no panic / no error from loadPlugin above.
}
