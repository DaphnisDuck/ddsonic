package sdkgate

// The gate's tests run only under the sdkcontract runner, which sets where
// the staged SDK, its scratch module, its overlay and the upstream base are.
// Without them they fail; they never skip.

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type gateEnv struct {
	scratch, staged, base string
	overlay               map[string][]byte
}

func environment(t *testing.T) gateEnv {
	t.Helper()
	e := gateEnv{
		scratch: os.Getenv("SDKGATE_SCRATCH"),
		staged:  os.Getenv("SDKGATE_STAGED"),
		base:    os.Getenv("SDKGATE_BASE"),
	}
	path := os.Getenv("SDKGATE_OVERLAY")
	if e.scratch == "" || e.staged == "" || e.base == "" || path == "" {
		t.Fatal("SDKGATE_SCRATCH, SDKGATE_STAGED, SDKGATE_BASE and SDKGATE_OVERLAY are not all set: the gate is run by the sdkcontract runner")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var o struct{ Replace map[string]string }
	if err := json.Unmarshal(data, &o); err != nil {
		t.Fatal(err)
	}
	e.overlay = map[string][]byte{}
	for target, from := range o.Replace {
		src, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		e.overlay[target] = src
	}
	return e
}

// C0: the whitelist holds for the staged active tree and its contract tests.
func TestSymbolsWhitelist(t *testing.T) {
	e := environment(t)
	pkgs, err := Load(e.scratch, e.overlay)
	if err != nil {
		t.Fatal(err)
	}
	if v := Check(pkgs, e.staged, e.overlay); len(v) != 0 {
		t.Fatalf("C0: the symbols gate rejects the active tree:\n  %s", strings.Join(v, "\n  "))
	}
}

// T-S1F: each source mutation of the whitelisted shape makes the gate fail,
// with the violation it is for. Every mutated tree must load and type-check:
// a loader failure is never taken as the gate's rejection.
func TestSymbolsControls(t *testing.T) {
	e := environment(t)
	for _, m := range []struct{ name, file, old, new, want string }{
		{"C1 a second write, in Close", "ap/ap.go",
			"\t\tclose(ap.done)\n", "\t\tclose(ap.done)\n\t\tap.announcer = nil\n", "outside W1 and W2"},
		{"C2 an extra read", "ap/ap.go",
			"\tp := ap.gen.Load()\n\treturn p.gen, p.changed\n", "\t_ = ap.announcer\n\tp := ap.gen.Load()\n\treturn p.gen, p.changed\n", "outside W1 and W2"},
		{"C3 an extra call, in reconnect", "ap/ap.go",
			"\tap.resetPongAckDeadline()\n\n\t// if we are here", "\tap.announcer(0)\n\tap.resetPongAckDeadline()\n\n\t// if we are here", "outside W1 and W2"},
		{"C4 an alias", "ap/ap.go",
			"\tp := ap.gen.Load()\n\treturn p.gen, p.changed\n", "\tx := ap.announcer\n\t_ = x\n\tp := ap.gen.Load()\n\treturn p.gen, p.changed\n", "outside W1 and W2"},
		{"C4 an address", "ap/ap.go",
			"\tp := ap.gen.Load()\n\treturn p.gen, p.changed\n", "\t_ = &ap.announcer\n\tp := ap.gen.Load()\n\treturn p.gen, p.changed\n", "outside W1 and W2"},
		{"C5 the empty-slot check removed", "ap/ap.go",
			"\t\tif a.announcer != nil {\n\t\t\treturn keylink.ErrTaken\n\t\t}\n", "", "0 empty-slot checks"},
		{"W1 the installer stores nil, not its callback", "ap/ap.go",
			"\t\ta.announcer = f\n", "\t\ta.announcer = nil\n", "does not store its callback parameter"},
		{"W1 the lock released before the check and write", "ap/ap.go",
			"\t\tdefer a.connMu.Unlock()\n", "\t\ta.connMu.Unlock()\n", "not under that access point's connMu.Lock"},
		{"W3 a test calls an alias of the slot", "ap/zz_ddsonic_keylink_contract_test.go",
			"\tg, changed := a.Generation()\n", "\tif f := a.announcer; f != nil {\n\t\tf(0)\n\t}\n\tg, changed := a.Generation()\n", "other than comparing it with nil"},
		{"K1 a second assignment of the hook", "ap/ap.go",
			"\nfunc NewAccesspoint(", "\nfunc init() { keylink.AttachAnnouncer = nil }\n\nfunc NewAccesspoint(", "assigned 2 times"},
		{"K2 an alias of the hook", "ap/ap.go",
			"\nfunc NewAccesspoint(", "\nvar ddsonicAlias = keylink.AttachAnnouncer\n\nfunc NewAccesspoint(", "used as a value"},
		{"K3 the hook's address", "ap/ap.go",
			"\nfunc NewAccesspoint(", "\nvar ddsonicAddress = &keylink.AttachAnnouncer\n\nfunc NewAccesspoint(", "used as a value"},
		{"K4 a second production call", "audio/provider.go",
			"\tp.owner = ownerFrom(keylink.AttachAnnouncer(a, p.announce))\n",
			"\tp.owner = ownerFrom(keylink.AttachAnnouncer(a, p.announce))\n\t_ = keylink.AttachAnnouncer(a, p.announce)\n", "2 production calls"},
		{"K5 another package imports keylink", "player/zz_sdkgate_import.go",
			"", "package player\n\nimport _ \"github.com/devgianlu/go-librespot/internal/keylink\"\n", "imports internal/keylink"},
	} {
		t.Run(m.name, func(t *testing.T) {
			path := filepath.Join(e.staged, filepath.FromSlash(m.file))
			mutated := m.new
			if m.old != "" {
				src, ok := e.overlay[path]
				if !ok {
					var err error
					if src, err = os.ReadFile(path); err != nil {
						t.Fatal(err)
					}
				}
				if n := strings.Count(string(src), m.old); n != 1 {
					t.Fatalf("the mutation's anchor occurs %d times in %s, want once", n, m.file)
				}
				mutated = strings.Replace(string(src), m.old, m.new, 1)
			}
			overlay := maps.Clone(e.overlay)
			overlay[path] = []byte(mutated)
			pkgs, err := Load(e.scratch, overlay)
			if err != nil {
				t.Fatalf("the mutated tree does not load, so the control shows nothing: %v", err)
			}
			v := Check(pkgs, e.staged, overlay)
			if !slices.ContainsFunc(v, func(s string) bool { return strings.Contains(s, m.want) }) {
				t.Fatalf("the gate did not reject the mutation with %q; it reported:\n  %s", m.want, strings.Join(v, "\n  "))
			}
		})
	}
}

// allowedAdditions is T-S2's list: the public API patch 0002 adds, and
// nothing else.
var allowedAdditions = map[string][]string{
	"ap": {
		"method Accesspoint.Generation",
		"method Accesspoint.SendStamped",
	},
	"audio": {
		"method ConnectionReplacedError.Error",
		"method KeySendError.Error",
		"method KeySendError.Unwrap",
		"type ConnectionReplacedError",
		"type KeySendError",
		"var ErrKeyProviderNotOwner",
		"var ErrKeySequenceExhausted",
	},
}

// T-S2: against the upstream base, ap and audio add exactly the allowed
// names, remove nothing and change nothing; Send and Request keep their
// signatures.
func TestPublicAPIDelta(t *testing.T) {
	e := environment(t)
	for _, pkg := range []string{"ap", "audio"} {
		base, err := API(filepath.Join(e.base, pkg))
		if err != nil {
			t.Fatal(err)
		}
		active, err := API(filepath.Join(e.staged, pkg))
		if err != nil {
			t.Fatal(err)
		}
		added, removed, changed := APIDelta(base, active)
		if !slices.Equal(added, allowedAdditions[pkg]) || len(removed) != 0 || len(changed) != 0 {
			t.Errorf("%s: added %q (allowed %q), removed %q, changed %q", pkg, added, allowedAdditions[pkg], removed, changed)
		}
	}
	for _, k := range []struct{ pkg, key string }{{"ap", "method Accesspoint.Send"}, {"audio", "method KeyProvider.Request"}} {
		base, _ := API(filepath.Join(e.base, k.pkg))
		active, _ := API(filepath.Join(e.staged, k.pkg))
		if base[k.key] == "" || base[k.key] != active[k.key] {
			t.Errorf("%s %s: %q in the base, %q in the active tree", k.pkg, k.key, base[k.key], active[k.key])
		}
	}
}

// T-S2's control: an extra exported name, a removed one and a changed
// signature are each reported.
func TestPublicAPIDeltaControl(t *testing.T) {
	dir := t.TempDir()
	write := func(sub, src string) string {
		d := filepath.Join(dir, sub)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "x.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}
	base := write("base", "package x\n\ntype T struct{ A int; b int }\n\nfunc (t *T) Send(n int) error { return nil }\n\nfunc Gone() {}\n")
	active := write("active", "package x\n\ntype T struct{ A int; c string }\n\nfunc (t *T) Send(m int, extra bool) error { return nil }\n\nvar Extra int\n")
	b, err := API(base)
	if err != nil {
		t.Fatal(err)
	}
	a, err := API(active)
	if err != nil {
		t.Fatal(err)
	}
	added, removed, changed := APIDelta(b, a)
	if !slices.Equal(added, []string{"var Extra"}) || !slices.Equal(removed, []string{"func Gone"}) || len(changed) != 1 || !strings.HasPrefix(changed[0], "method T.Send") {
		t.Fatalf("added %q, removed %q, changed %q", added, removed, changed)
	}
}
