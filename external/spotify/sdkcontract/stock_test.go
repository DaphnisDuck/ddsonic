//go:build sdkcontract

package sdkcontract

// ddsonic: the stock-product stage (plan v9 §4.1-4.2, M3b2 step 3, the
// runner's step 15): rc.4's external/spotify import closure, pinned without
// Git by an archive whose blobs, trees and commit are recomputed here, staged
// alone into a fresh module, and built against the authenticated v0.9.1 base.
// Every failure is a setup failure, never I11 credit (§4.4). The stock probes
// (StockI2, StockI5) and their adapters are a later slice; so is the wiring
// of this step into run().

import (
	"archive/tar"
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const (
	// stockArchivePath is the archive, relative to the repository.
	stockArchivePath = "external/spotify/sdkcontract/testdata/stock-rc4.tar"
	// stockArchiveSHA256 is the archive's own hash (§4.1, "Archive check").
	stockArchiveSHA256 = "f669bb43d632be467255bc500a869f64e25f8ce06229328bf5d129f136ba171f"
	// rc4Commit and rc4Tree are the commit the archive pins and its tree.
	rc4Commit = "177c1df70dea3b14988188d4ae3b45eea71aca35"
	rc4Tree   = "c97eb2c3287001ece9b1a0f2fbf053bacf33f3bb"
	// The app part of P: the 14 included directories' files (§4.1).
	stockAppFiles = 131
	stockAppBytes = 925504
	// rc4GraphSHA256 is the hash of the sealed derivation output that
	// rc4GraphConst reproduces verbatim (ADDENDUM-6a; derived once, offline,
	// from rc4's go.mod and go.sum; never adjusted to an observation).
	rc4GraphSHA256 = "36bb261c5eccd33a197cf76e40b42cfd2815e234dca5e81a4232b6d4dbc4c87b"
	stockStep      = 15
)

// stockIncluded are the 14 included directories: the in-module import closure
// of external/spotify, plus its testdata (§4.1).
var stockIncluded = []string{
	"applog", "catalog", "catalogsync", "catalogsync/spotifysrc", "external/spotify",
	"internal/appdir", "internal/appmeta", "internal/browser", "internal/fileutil",
	"library", "lyrics", "playlist", "provider", "external/spotify/testdata",
}

// stockAbsent are files that exist after rc.4 and must not be staged (§4.2).
var stockAbsent = []string{
	"external/spotify/recovery_ddsonic.go",
	"external/spotify/ap_lifecycle_ddsonic_test.go",
	"external/spotify/testdata/composed-long.flac.bin",
	"external/spotify/sdkcontract/vehicle_test.go",
	"external/spotify/sdkcontract/composed_test.go",
	"third_party/go-librespot/go.mod",
}

// rc4 seams (§4.3): present in rc.4's external/spotify, and absent (so stock
// probes do not use them).
var (
	stockSeamsPresent = []string{"newLibrespotSession", "initSessionPlayer", "closeLibrespot", "sessionIdentity", "playerNewStream", "sessionNewStream", "sessionReconnect", "buildPlaybackSession", "interactiveTokens", "openHook"}
	stockSeamsAbsent  = []string{"decisionHook", "detachHook", "holdPlayback", "serviceAccessPoint"}
)

// rc4GraphConst is rc.4's module graph, every non-main module as "path
// version", sorted: the sealed derivation output, verbatim.
const rc4GraphConst = `cel.dev/expr v0.25.2
charm.land/bubbletea/v2 v2.0.10
charm.land/lipgloss/v2 v2.0.6
cloud.google.com/go v0.112.2
cloud.google.com/go/auth v0.23.3
cloud.google.com/go/auth/oauth2adapt v0.2.8
cloud.google.com/go/compute/metadata v0.9.1
cloud.google.com/go/longrunning v0.5.6
cloud.google.com/go/translate v1.10.3
github.com/BurntSushi/toml v1.6.0
github.com/GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp v1.34.0
github.com/Masterminds/semver/v3 v3.5.0
github.com/VividCortex/ewma v1.2.0
github.com/acarl005/stripansi v0.0.0-20180116102854-5a71ef0e047d
github.com/aymanbagabas/go-udiff v0.4.1
github.com/bitly/go-simplejson v0.5.1
github.com/bits-and-blooms/bitset v1.24.6
github.com/cenkalti/backoff v2.2.1+incompatible
github.com/cenkalti/backoff/v4 v4.3.0
github.com/cespare/xxhash/v2 v2.3.0
github.com/charmbracelet/colorprofile v0.4.3
github.com/charmbracelet/ultraviolet v0.0.0-20260930135840-270558f35f71
github.com/charmbracelet/x/ansi v0.11.8
github.com/charmbracelet/x/exp/golden v0.0.0-20250806222409-83e3a29d542f
github.com/charmbracelet/x/term v0.2.2
github.com/charmbracelet/x/termios v0.1.1
github.com/charmbracelet/x/windows v0.2.2
github.com/chzyer/logex v1.1.10
github.com/chzyer/readline v1.5.1
github.com/chzyer/test v0.0.0-20180213035817-a1ea475d72b1
github.com/clipperhouse/displaywidth v0.11.0
github.com/clipperhouse/stringish v0.1.1
github.com/clipperhouse/uax29/v2 v2.7.0
github.com/cncf/xds/go v0.0.0-20260202195803-dba9d589def2
github.com/coder/websocket v1.8.15
github.com/d4l3k/messagediff v1.2.2-0.20190829033028-7e0a312ae40b
github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc
github.com/devgianlu/go-librespot v0.9.1
github.com/devgianlu/shannon v0.0.0-20230613115856-82ec90b7fa7e
github.com/dhowden/itl v0.0.0-20170329215456-9fbe21093131
github.com/dhowden/plist v0.0.0-20141002110153-5db6e0d9931a
github.com/dhowden/tag v0.0.0-20240417053706-3d75831295e8
github.com/dlclark/regexp2 v1.11.5
github.com/dlclark/regexp2/v2 v2.7.1
github.com/dop251/goja v0.0.0-20260826204918-8f1c0696a37b
github.com/dop251/goja_nodejs v0.0.0-20211022123610-8dd9abb0616d
github.com/dustin/go-humanize v1.0.1
github.com/ebitengine/oto/v3 v3.4.1
github.com/ebitengine/purego v0.10.2
github.com/envoyproxy/go-control-plane v0.14.0
github.com/envoyproxy/go-control-plane/envoy v1.37.0
github.com/envoyproxy/go-control-plane/ratelimit v0.1.0
github.com/envoyproxy/protoc-gen-validate v1.3.3
github.com/felixge/httpsnoop v1.1.0
github.com/fsnotify/fsnotify v1.9.0
github.com/gdamore/encoding v1.0.0
github.com/gdamore/tcell/v2 v2.7.4
github.com/go-jose/go-jose/v4 v4.1.4
github.com/go-logr/logr v1.4.4
github.com/go-logr/stdr v1.2.2
github.com/go-sourcemap/sourcemap v2.1.4+incompatible
github.com/go-viper/mapstructure/v2 v2.5.0
github.com/goccy/go-yaml v1.19.2
github.com/godbus/dbus/v5 v5.2.2
github.com/gofrs/flock v0.13.0
github.com/golang/glog v1.2.5
github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da
github.com/golang/protobuf v1.5.4
github.com/google/go-cmp v0.7.0
github.com/google/go-pkcs11 v0.3.0
github.com/google/pprof v0.0.0-20260830191439-4932ad3515ea
github.com/google/s2a-go v0.1.10
github.com/google/uuid v1.6.0
github.com/googleapis/enterprise-certificate-proxy v0.3.22
github.com/googleapis/gax-go/v2 v2.24.1
github.com/gopxl/beep/v2 v2.1.1
github.com/grandcat/zeroconf v1.0.0
github.com/hajimehoshi/go-mp3 v0.3.4
github.com/hajimehoshi/oto/v2 v2.3.1
github.com/hashicorp/golang-lru/v2 v2.0.7
github.com/ianlancetaylor/demangle v0.0.0-20250417193237-f615e6bd150b
github.com/icza/bitio v1.1.0
github.com/icza/mighty v0.0.0-20180919140131-cfd07d671de6
github.com/inconshreveable/mousetrap v1.1.0
github.com/jfreymuth/oggvorbis v1.0.5
github.com/jfreymuth/pulse v0.1.3
github.com/jfreymuth/vorbis v1.0.2
github.com/jszwec/csvutil v1.10.0
github.com/kkdai/youtube/v2 v2.10.6
github.com/knadh/koanf/maps v0.1.2
github.com/knadh/koanf/parsers/yaml v1.1.0
github.com/knadh/koanf/providers/confmap v1.0.0
github.com/knadh/koanf/providers/file v1.2.0
github.com/knadh/koanf/providers/posflag v1.0.1
github.com/knadh/koanf/v2 v2.3.0
github.com/kr/pretty v0.3.1
github.com/lucasb-eyer/go-colorful v1.4.1
github.com/mattn/go-isatty v0.0.24
github.com/mattn/go-runewidth v0.0.30
github.com/mewkiz/flac v1.0.14
github.com/mewkiz/pkg v0.0.0-20260703220044-4fb89b18cc87
github.com/mewpkg/term v0.0.0-20241026122259-37a80af23985
github.com/miekg/dns v1.1.69
github.com/mitchellh/copystructure v1.2.0
github.com/mitchellh/go-homedir v1.1.0
github.com/mitchellh/reflectwalk v1.0.2
github.com/muesli/cancelreader v0.2.2
github.com/ncruces/go-strftime v1.0.0
github.com/olekukonko/tablewriter v0.0.5
github.com/orcaman/writerseeker v0.0.0-20200621085525-1d3f536ff85e
github.com/pelletier/go-toml/v2 v2.2.4
github.com/pkg/errors v0.9.1
github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10
github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2
github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec
github.com/rivo/uniseg v0.4.7
github.com/rogpeppe/go-internal v1.14.1
github.com/rs/cors v1.11.1
github.com/sagikazarmark/locafero v0.12.0
github.com/samhocevar/go-meltysynth v0.0.0-20230403180939-aca4a036cb16
github.com/sirupsen/logrus v1.10.2
github.com/spf13/afero v1.15.0
github.com/spf13/cast v1.10.0
github.com/spf13/cobra v1.10.2
github.com/spf13/pflag v1.0.10
github.com/spf13/viper v1.21.0
github.com/spiffe/go-spiffe/v2 v2.8.1
github.com/stretchr/objx v0.5.3
github.com/stretchr/testify v1.12.1
github.com/subosito/gotenv v1.6.0
github.com/urfave/cli/v3 v3.13.0
github.com/vbauerster/mpb/v5 v5.4.0
github.com/xlab/vorbis-go v0.0.0-20210911202351-b5b85f1ec645
github.com/xo/terminfo v1.0.0
github.com/yuin/gopher-lua v1.1.2
go.opencensus.io v0.24.0
go.opentelemetry.io/auto/sdk v1.2.1
go.opentelemetry.io/contrib/detectors/gcp v1.44.0
go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.67.0
go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.71.0
go.opentelemetry.io/otel v1.46.0
go.opentelemetry.io/otel/metric v1.46.0
go.opentelemetry.io/otel/sdk v1.46.0
go.opentelemetry.io/otel/sdk/metric v1.46.0
go.opentelemetry.io/otel/trace v1.46.0
go.uber.org/goleak v1.3.0
go.yaml.in/yaml/v3 v3.0.5
golang.org/x/crypto v0.57.0
golang.org/x/exp v0.0.0-20260824195058-e88cd73687aa
golang.org/x/image v0.41.0
golang.org/x/mod v0.41.0
golang.org/x/net v0.60.0
golang.org/x/oauth2 v0.37.0
golang.org/x/sync v0.23.0
golang.org/x/sys v0.48.0
golang.org/x/term v0.46.0
golang.org/x/text v0.42.0
golang.org/x/time v0.16.0
golang.org/x/tools v0.50.0
golang.org/x/tools/go/expect v0.1.1-deprecated
golang.org/x/tools/go/packages/packagestest v0.1.1-deprecated
gonum.org/v1/gonum v0.17.0
google.golang.org/api v0.299.0
google.golang.org/appengine v1.6.8
google.golang.org/genproto v0.0.0-20260715232425-e75dac1f907d
google.golang.org/genproto/googleapis/api v0.0.0-20260715232425-e75dac1f907d
google.golang.org/genproto/googleapis/bytestream v0.0.0-20260921155816-b14227669459
google.golang.org/genproto/googleapis/rpc v0.0.0-20260921155816-b14227669459
google.golang.org/grpc v1.84.0
google.golang.org/protobuf v1.36.12
gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c
gopkg.in/yaml.v3 v3.0.1
modernc.org/cc/v4 v4.29.7
modernc.org/ccgo/v4 v4.36.1
modernc.org/fileutil v1.4.0
modernc.org/gc/v2 v2.6.5
modernc.org/gc/v3 v3.1.5
modernc.org/goabi0 v0.2.0
modernc.org/libc v1.77.1
modernc.org/mathutil v1.7.1
modernc.org/memory v1.12.1
modernc.org/opt v0.2.0
modernc.org/sortutil v1.2.1
modernc.org/sqlite v1.60.1
modernc.org/strutil v1.2.1
modernc.org/token v1.1.0
`

func stockSetup(what string) error {
	return &stepError{stockStep, "setup failure: " + what, ""}
}

// readStockArchive reads the archive's members, refusing anything that is
// not a regular file with a plain, relative, unique name in the expected
// namespaces, written as the deterministic build writes it.
func readStockArchive(data []byte) (map[string][]byte, []string, error) {
	members := map[string][]byte{}
	var order []string
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, stockSetup("the archive cannot be read: " + err.Error())
		}
		name := hdr.Name
		switch {
		case hdr.Typeflag != tar.TypeReg:
			return nil, nil, stockSetup(fmt.Sprintf("member %q is not a regular file (type %q)", name, hdr.Typeflag))
		case !utf8.ValidString(name) || name == "" || strings.HasPrefix(name, "/") || path.Clean(name) != name || slices.Contains(strings.Split(name, "/"), ".."):
			return nil, nil, stockSetup(fmt.Sprintf("member name %q is not a plain relative name", name))
		case !strings.HasPrefix(name, "payload/") && !strings.HasPrefix(name, "proof/") && name != "MANIFEST.sha256":
			return nil, nil, stockSetup(fmt.Sprintf("member %q is outside payload/, proof/ and MANIFEST.sha256", name))
		case members[name] != nil:
			return nil, nil, stockSetup(fmt.Sprintf("member %q is duplicated", name))
		case hdr.Mode != 0o644 || hdr.Uid != 0 || hdr.Gid != 0 || hdr.ModTime.Unix() != 0:
			return nil, nil, stockSetup(fmt.Sprintf("member %q is not mode 0644, uid/gid 0, mtime 0", name))
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, nil, stockSetup("member " + name + ": " + err.Error())
		}
		members[name] = body
		order = append(order, name)
	}
	if !slices.IsSorted(order) {
		return nil, nil, stockSetup("the archive's members are not in sorted order")
	}
	return members, order, nil
}

// checkStockSets checks that the members are exactly P ∪ M ∪ F, and that F
// lists every member of P ∪ M, and nothing else, with its hash (§4.1).
func checkStockSets(members map[string][]byte) error {
	manifest, ok := members["MANIFEST.sha256"]
	if !ok {
		return stockSetup("MANIFEST.sha256 is missing")
	}
	listed := map[string]bool{}
	for _, line := range strings.SplitAfter(string(manifest), "\n") {
		if line == "" {
			continue
		}
		sum, name, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "  ")
		if !ok || len(sum) != 64 || !strings.HasSuffix(line, "\n") || name == "MANIFEST.sha256" || listed[name] {
			return stockSetup(fmt.Sprintf("MANIFEST.sha256 has a malformed or repeated line %q", line))
		}
		body, ok := members[name]
		if !ok {
			return stockSetup("MANIFEST.sha256 lists " + name + ", which is not a member")
		}
		if got := sha256.Sum256(body); hex.EncodeToString(got[:]) != sum {
			return stockSetup("MANIFEST.sha256: the hash of " + name + " differs")
		}
		listed[name] = true
	}
	var proof, payload int
	for name := range members {
		switch {
		case name == "MANIFEST.sha256":
		case !listed[name]:
			return stockSetup(name + " is a member that MANIFEST.sha256 does not list")
		case strings.HasPrefix(name, "proof/"):
			if name != "proof/MERKLE.txt" && name != "proof/COMMIT.txt" {
				return stockSetup(name + " is not one of M's two members")
			}
			proof++
		default:
			payload++
		}
	}
	if proof != 2 || payload != stockAppFiles+2 {
		return stockSetup(fmt.Sprintf("the sets are M %d and P %d; v9 §4.1 says 2 and %d", proof, payload, stockAppFiles+2))
	}
	return nil
}

type stockEntry struct {
	mode, typ, oid, name string
}

// stockModeType are the Git tree-entry mode/type pairs a listing may hold
// (R54-1): a regular file, an executable, a symlink, a tree, a submodule.
// Any other pair, or a label that disagrees with its mode, is refused.
var stockModeType = map[string]bool{
	"100644 blob": true, "100755 blob": true, "120000 blob": true,
	"040000 tree": true, "160000 commit": true,
}

var stockOIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// parseMerkle reads proof/MERKLE.txt: for each listed directory, a line
// "path <dir>" (the root is "."), its `git ls-tree` lines, then a blank line.
func parseMerkle(data []byte) (map[string][]stockEntry, error) {
	trees := map[string][]stockEntry{}
	var cur string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case !in && strings.HasPrefix(line, "path "):
			cur, in = strings.TrimPrefix(line, "path "), true
			if cur == "." {
				cur = ""
			}
			if _, dup := trees[cur]; dup {
				return nil, stockSetup("MERKLE.txt lists " + cur + " twice")
			}
			trees[cur] = nil
		case in && line == "":
			in = false
		case in:
			meta, name, ok := strings.Cut(line, "\t")
			f := strings.Fields(meta)
			if !ok || len(f) != 3 || len(f[2]) != 40 {
				return nil, stockSetup(fmt.Sprintf("MERKLE.txt: a malformed entry %q", line))
			}
			if !stockModeType[f[0]+" "+f[1]] || !stockOIDPattern.MatchString(f[2]) || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
				return nil, stockSetup(fmt.Sprintf("MERKLE.txt: an unsupported mode/type pair or malformed entry %q", line))
			}
			trees[cur] = append(trees[cur], stockEntry{f[0], f[1], f[2], name})
		case line == "":
		default:
			return nil, stockSetup(fmt.Sprintf("MERKLE.txt: an unexpected line %q", line))
		}
	}
	return trees, nil
}

func gitOID(kind string, body []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d\x00", kind, len(body))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// gitOrderKey is Git's tree order: bytewise by name, a tree compared as
// "name/".
func gitOrderKey(e stockEntry) string {
	if e.typ == "tree" {
		return e.name + "/"
	}
	return e.name
}

// checkStockMerkle recomputes, without Git, each P blob's oid, each listed
// tree bottom-up in Git's encoding, the root, and the framed commit id
// (§4.1, "The runner recomputes").
func checkStockMerkle(members map[string][]byte) (string, error) {
	trees, err := parseMerkle(members["proof/MERKLE.txt"])
	if err != nil {
		return "", err
	}
	// The listed directories: the root, each directory on a path to an
	// included one, and each included one.
	want := map[string]bool{"": true}
	for _, d := range stockIncluded {
		parts := strings.Split(d, "/")
		for i := 1; i <= len(parts); i++ {
			want[strings.Join(parts[:i], "/")] = true
		}
	}
	for d := range trees {
		if !want[d] {
			return "", stockSetup("MERKLE.txt lists " + d + ", which is not on a path to an included directory")
		}
	}
	for d := range want {
		if _, ok := trees[d]; !ok {
			return "", stockSetup("MERKLE.txt does not list " + d)
		}
	}
	// 1. Blob oids: every file entry of an included directory is in P with
	// that oid, and P holds nothing else but go.mod and go.sum.
	accounted := map[string]bool{}
	var appFiles, appBytes int
	for _, d := range stockIncluded {
		for _, e := range trees[d] {
			if e.typ != "blob" {
				continue
			}
			name := "payload/" + d + "/" + e.name
			body, ok := members[name]
			if !ok {
				return "", stockSetup(name + " is in rc.4's listing but missing from P")
			}
			if got := gitOID("blob", body); got != e.oid {
				return "", stockSetup(fmt.Sprintf("%s: blob oid %s, the listing says %s", name, got, e.oid))
			}
			accounted[name] = true
			appFiles++
			appBytes += len(body)
		}
	}
	for _, e := range trees[""] {
		if e.typ == "blob" && (e.name == "go.mod" || e.name == "go.sum") {
			name := "payload/" + e.name
			body, ok := members[name]
			if !ok || gitOID("blob", body) != e.oid {
				return "", stockSetup(name + " is missing or differs from rc.4's root listing")
			}
			accounted[name] = true
		}
	}
	for name := range members {
		if strings.HasPrefix(name, "payload/") && !accounted[name] {
			return "", stockSetup(name + " is in P but in no included directory's listing")
		}
	}
	if appFiles != stockAppFiles || appBytes != stockAppBytes {
		return "", stockSetup(fmt.Sprintf("the app part is %d files, %d bytes; v9 §4.1 says %d and %d", appFiles, appBytes, stockAppFiles, stockAppBytes))
	}
	// 2. Trees, bottom-up: the deepest first, a listed child's computed id
	// substituted for (and checked against) its parent's entry; an opaque
	// subtree keeps its recorded oid.
	dirs := slices.SortedFunc(func(yield func(string) bool) {
		for d := range trees {
			if !yield(d) {
				return
			}
		}
	}, func(a, b string) int {
		da, db := strings.Count(a, "/")+1, strings.Count(b, "/")+1
		if a == "" {
			da = 0
		}
		if b == "" {
			db = 0
		}
		if da != db {
			return db - da
		}
		return strings.Compare(a, b)
	})
	computed := map[string]string{}
	linked := map[string]bool{}
	for _, d := range dirs {
		var body bytes.Buffer
		entries := trees[d]
		names := map[string]bool{}
		for i, e := range entries {
			if i > 0 && gitOrderKey(entries[i-1]) >= gitOrderKey(e) {
				return "", stockSetup("MERKLE.txt: " + d + "'s entries are not in Git order")
			}
			if names[e.name] {
				return "", stockSetup("MERKLE.txt: " + d + " lists " + e.name + " twice")
			}
			names[e.name] = true
			oid := e.oid
			child := e.name
			if d != "" {
				child = d + "/" + e.name
			}
			// R54-1: a listed directory is linked from its listed parent by
			// path, whatever the entry's type label says, and only through a
			// tree-mode, tree-typed entry carrying its exact computed id.
			if c, ok := computed[child]; ok {
				if e.mode != "040000" || e.typ != "tree" {
					return "", stockSetup(fmt.Sprintf("listed directory %s is linked by a non-tree entry (%s %s)", child, e.mode, e.typ))
				}
				if c != e.oid {
					return "", stockSetup(fmt.Sprintf("tree %s recomputes to %s, its parent lists %s", child, c, e.oid))
				}
				linked[child] = true
				oid = c
			}
			raw, err := hex.DecodeString(oid)
			if err != nil {
				return "", stockSetup("MERKLE.txt: a malformed oid " + oid)
			}
			fmt.Fprintf(&body, "%s %s\x00", strings.TrimLeft(e.mode, "0"), e.name)
			body.Write(raw)
		}
		computed[d] = gitOID("tree", body.Bytes())
	}
	// R54-1: every listed directory but the root is linked from its listed
	// parent; a disconnected listing proves nothing about the root.
	for d := range trees {
		if d != "" && !linked[d] {
			return "", stockSetup("listed directory " + d + " is not linked from its parent's listing")
		}
	}
	// 3. The root and the commit.
	if computed[""] != rc4Tree {
		return "", stockSetup(fmt.Sprintf("the root recomputes to %s, not %s", computed[""], rc4Tree))
	}
	commit := members["proof/COMMIT.txt"]
	if !bytes.HasPrefix(commit, []byte("tree "+rc4Tree+"\n")) {
		return "", stockSetup("COMMIT.txt does not name the tree " + rc4Tree)
	}
	if got := gitOID("commit", commit); got != rc4Commit {
		return "", stockSetup(fmt.Sprintf("COMMIT.txt's framed id is %s, not %s", got, rc4Commit))
	}
	return computed["external/spotify/testdata"], nil
}

// stageStock writes P's members, payload/ stripped, into a fresh dir, and
// checks that the staged set is exactly P's and that no post-rc.4 file is
// there (§4.2). M and F are never staged.
func stageStock(members map[string][]byte, dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return stockSetup(dir + " already exists; a stage is never reused")
	}
	for name, body := range members {
		rel, ok := strings.CutPrefix(name, "payload/")
		if !ok {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return stockSetup(err.Error())
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return stockSetup(err.Error())
		}
	}
	return checkStaged(members, dir)
}

// checkStaged compares the staged tree with P: the same path set and every
// file byte for byte; and the absent list.
func checkStaged(members map[string][]byte, dir string) error {
	seen := map[string]bool{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		name := "payload/" + filepath.ToSlash(rel)
		body, ok := members[name]
		if !ok {
			return fmt.Errorf("%s is staged but is not in P", rel)
		}
		got, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, body) {
			return fmt.Errorf("%s differs from P", rel)
		}
		seen[name] = true
		return nil
	})
	if err != nil {
		return stockSetup("staging: " + err.Error())
	}
	for name := range members {
		if strings.HasPrefix(name, "payload/") && !seen[name] {
			return stockSetup("staging: " + name + " was not staged")
		}
	}
	for _, rel := range stockAbsent {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			return stockSetup("staging: the post-rc.4 file " + rel + " is present")
		}
	}
	return nil
}

// stockGraph parses rc4GraphConst after checking it is the sealed derivation.
func stockGraph() (map[string]string, error) {
	if got := sha256.Sum256([]byte(rc4GraphConst)); hex.EncodeToString(got[:]) != rc4GraphSHA256 {
		return nil, stockSetup("the embedded rc.4 graph is not the sealed derivation")
	}
	g := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(rc4GraphConst, "\n"), "\n") {
		p, v, ok := strings.Cut(line, " ")
		if !ok || g[p] != "" {
			return nil, stockSetup(fmt.Sprintf("the embedded rc.4 graph has a malformed line %q", line))
		}
		g[p] = v
	}
	return g, nil
}

// stockStage is the runner's step 15 without its probes: the archive, its
// proofs, the stage, the module context, the graph, a compile of the stock
// package and the seam scan. baseDir is the authenticated base (step 4).
func (r *runner) stockStage(baseDir string, prov *provenance) ([]string, error) {
	var notes []string
	data, err := os.ReadFile(filepath.Join(r.repo, filepath.FromSlash(stockArchivePath)))
	if err != nil {
		return notes, stockSetup("the archive: " + err.Error())
	}
	if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != stockArchiveSHA256 {
		return notes, stockSetup("the archive's own hash differs from the runner's constant")
	}
	members, order, err := readStockArchive(data)
	if err != nil {
		return notes, err
	}
	if err := checkStockSets(members); err != nil {
		return notes, err
	}
	notes = append(notes, fmt.Sprintf("STOCK-ARCHIVE-OK members=%d sha256=%s", len(order), stockArchiveSHA256))
	testdataTree, err := checkStockMerkle(members)
	if err != nil {
		return notes, err
	}
	notes = append(notes, fmt.Sprintf("STOCK-MERKLE-OK root=%s commit=%s external/spotify/testdata=%s app=%d files %d bytes", rc4Tree, rc4Commit, testdataTree, stockAppFiles, stockAppBytes))
	stock := filepath.Join(r.dir, "stock")
	if err := stageStock(members, stock); err != nil {
		return notes, err
	}
	notes = append(notes, fmt.Sprintf("STOCK-STAGED-OK files=%d absent=%d", stockAppFiles+2, len(stockAbsent)))
	// The SDK: the authenticated base, copied out of the cache, at base-h1
	// before and after the stage.
	base := filepath.Join(r.dir, "stockbase")
	if err := copyTree(baseDir, base); err != nil {
		return notes, stockSetup("the base copy: " + err.Error())
	}
	if got, err := hash1(base, sdkModule+"@"+sdkPin); err != nil || got != prov.baseH1 {
		return notes, stockSetup(fmt.Sprintf("the base copy is %s (%v), not base-h1 %s", got, err, prov.baseH1))
	}
	notes = append(notes, "STOCK-BASE-H1-OK before "+prov.baseH1)
	// The module context: stock.mod = stock/go.mod plus exactly the one
	// replace; stock.sum = stock/go.sum.
	modDir := filepath.Join(r.dir, "stockmod")
	gomod, gosum := members["payload/go.mod"], members["payload/go.sum"]
	if err := errors.Join(
		os.MkdirAll(modDir, 0o755),
		os.WriteFile(filepath.Join(modDir, "stock.mod"), append(append([]byte{}, gomod...), []byte(fmt.Sprintf("\nreplace %s %s => %s\n", sdkModule, sdkPin, base))...), 0o644),
		os.WriteFile(filepath.Join(modDir, "stock.sum"), gosum, 0o644),
	); err != nil {
		return notes, stockSetup("the module context: " + err.Error())
	}
	modfile := "-modfile=" + filepath.Join(modDir, "stock.mod")
	if _, err := r.tool(stockStep, "go mod download (stock)", stock, 10*time.Minute, "mod", "download", modfile); err != nil {
		return notes, stockSetup(err.Error())
	}
	out, err := r.tool(stockStep, "go mod verify (stock)", stock, 10*time.Minute, "mod", "verify", modfile)
	if err != nil || !strings.Contains(string(out), "all modules verified") {
		return notes, stockSetup(fmt.Sprintf("go mod verify: %v %s", err, out))
	}
	notes = append(notes, "STOCK-VERIFY-OK "+strings.TrimSpace(string(out)))
	// The graph: rc.4's, plus the one replace.
	want, err := stockGraph()
	if err != nil {
		return notes, err
	}
	out, err = r.tool(stockStep, "go list -m all (stock)", stock, 5*time.Minute, "list", modfile, "-m", "-json", "all")
	if err != nil {
		return notes, stockSetup(err.Error())
	}
	got := map[string]moduleInfo{}
	var mainPath string
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var m moduleInfo
		if err := dec.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			return notes, stockSetup("go list -m all: " + err.Error())
		}
		if m.Main {
			mainPath = m.Path
			continue
		}
		got[m.Path] = m
	}
	var diffs []string
	if mainPath != "github.com/bjarneo/cliamp" {
		diffs = append(diffs, "the main module is "+mainPath)
	}
	for p, v := range want {
		m, ok := got[p]
		switch {
		case !ok:
			diffs = append(diffs, p+" is missing")
		case m.Version != v:
			diffs = append(diffs, fmt.Sprintf("%s is %s, not %s", p, m.Version, v))
		case p == sdkModule && (m.Replace == nil || m.Replace.Dir != base):
			diffs = append(diffs, p+" is not replaced by the base copy")
		case p != sdkModule && m.Replace != nil:
			diffs = append(diffs, p+" is replaced")
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			diffs = append(diffs, p+" is added")
		}
	}
	if len(diffs) != 0 {
		slices.Sort(diffs)
		return notes, stockSetup("the stock graph is not rc.4's plus the one replace: " + strings.Join(diffs, "; "))
	}
	notes = append(notes, fmt.Sprintf("STOCK-GRAPH-OK modules=%d (rc.4's, plus the one replace)", len(want)))
	// A compile of the stock package, rc.4's own tests included; no test
	// executes.
	if _, err := r.tool(stockStep, "compile the stock package", stock, 15*time.Minute, "test", modfile, "-count=1", "-run", "^$", "./external/spotify"); err != nil {
		return notes, stockSetup(err.Error())
	}
	notes = append(notes, "STOCK-COMPILE-OK ./external/spotify")
	// The seams at rc.4 (§4.3).
	var src strings.Builder
	files, _ := filepath.Glob(filepath.Join(stock, "external", "spotify", "*.go"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return notes, stockSetup(err.Error())
		}
		src.Write(b)
	}
	for _, s := range stockSeamsPresent {
		if !regexp.MustCompile(`\b` + s + `\b`).MatchString(src.String()) {
			return notes, stockSetup("the rc.4 seam " + s + " is not present")
		}
	}
	for _, s := range stockSeamsAbsent {
		if regexp.MustCompile(`\b` + s + `\b`).MatchString(src.String()) {
			return notes, stockSetup(s + " is present at rc.4; the plan says it is absent")
		}
	}
	notes = append(notes, fmt.Sprintf("STOCK-SEAMS-OK present=%d absent=%d", len(stockSeamsPresent), len(stockSeamsAbsent)))
	// After the stage: the base copy and the staged tree unchanged.
	if got, err := hash1(base, sdkModule+"@"+sdkPin); err != nil || got != prov.baseH1 {
		return notes, stockSetup(fmt.Sprintf("the base copy changed during the stage: %s (%v)", got, err))
	}
	if err := checkStaged(members, stock); err != nil {
		return notes, err
	}
	notes = append(notes, "STOCK-BASE-H1-OK after; STOCK-STAGE-UNCHANGED")
	return notes, nil
}

// TestSDKContractStockStage runs the stock stage once, standalone (plan v9
// §4.1-4.3 without the probes). Its failures are setup failures.
func TestSDKContractStockStage(t *testing.T) {
	r := newRunner(t)
	if err := r.environment(); err != nil {
		t.Fatal(err)
	}
	prov, err := readProvenance(r.repo)
	if err != nil {
		t.Fatal(err)
	}
	baseDir, err := r.downloadBase(prov)
	if err != nil {
		t.Fatal(err)
	}
	notes, err := r.stockStage(baseDir, prov)
	for _, n := range notes {
		t.Log(n)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Log("STOCK-STAGE-OK")
}

// stockOverlay is the stock stage's O (plan v9 §4.1 "O (overlay
// additions)", §4.3): each source in testdata, the bytes it is bound to, and
// where it is compiled: "app:" into the staged rc.4 tree's external/spotify,
// "sdk:" into the authenticated base copy. The shared core is the SAME H'
// the active stage compiles; the ACTIVE controls, cases and key probe are
// never in it. O is never written into P: it is overlaid, and only O.
var stockOverlay = []struct{ src, sha, dst string }{
	{"composed_harness_test.go.txt", "5ae07dbd0c8479bc72a4f53cfdc4d5548f7b30e2743d5b6317dec5d7d43614a8", "app:zz_ddsonic_composed_harness_test.go"},
	{"stock_adapter_test.go.txt", "fe33332fb487a6a278141683819a21f457f0759bb68740d0b357069f455638e9", "app:zz_ddsonic_composed_stock_adapter_test.go"},
	{"composed_bypass_test.go.txt", bypassSHA256, "app:composed_bypass_ddsonic_test.go"},
	{"stock_probes_test.go.txt", "42274cda0a5794823464e700ceba7a988af63f2af5a590c5b5898aebf1af062f", "app:zz_ddsonic_composed_stock_probes_test.go"},
	{"ap_fake_helper.go.txt", "40f7baedee3a3ff9ff27ce98190b13e699d86edb74bb488057de8eeb1565da33", "sdk:ap/zz_ddsonic_contract_fake.go"},
	{"ap_composed_observer.go.txt", "8c3af1062670a9276ca7e677a378b7efa96d6056e894a4afde42bde676e33920", "sdk:ap/zz_ddsonic_composed_observer.go"},
}

// stockMarkers is one stock probe's row name and the live preconditions it
// must log, in this order ("I11-PRECONDITION-OK: <row> <what>"), before its
// one red ("I11-STOCK-RED: <row> "; plan v9 §4.4).
type stockMarkers struct {
	row string
	pre []string
}

// stockProbeMarkers names the stock probes: exactly these run, and pass.
var stockProbeMarkers = map[string]stockMarkers{
	"TestDDSonicStockI2": {"StockI2", []string{"P0 SC ", "W ", "R "}},
	"TestDDSonicStockI5": {"StockI5", []string{"P0 SC ", "D9 installed ", "W ", "C ", "R ", "N ", "L-late source and consumption control"}},
}

// stockPackage is what go list -json says about one package (§4.3).
type stockPackage struct {
	ImportPath, Dir, ForTest                           string
	Standard                                           bool
	GoFiles, TestGoFiles, XTestGoFiles, IgnoredGoFiles []string
	CgoFiles                                           []string
	Module                                             *moduleInfo
}

func under(dir, root string) bool {
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
}

// checkStockIdentity is §4.3's identity of the composed stock probe, from
// go list -modfile=stock.mod -overlay -deps -test -json ./external/spotify:
// external/spotify's GoFiles are exactly P's non-test files there, and its
// TestGoFiles exactly P's test files there plus the app O, with no other
// file list; every main-module package resolves under stock/; every SDK
// package resolves under the base copy, the ap package with exactly the SDK
// O added to the base's own files; nothing resolves under the repository.
func checkStockIdentity(out []byte, repo, stock, base string, appO, sdkO map[string][]string) error {
	pkgs := map[string]stockPackage{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p stockPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return stockSetup("go list -deps -test: " + err.Error())
		}
		if p.Standard {
			continue
		}
		switch {
		case under(p.Dir, repo):
			return stockSetup(p.ImportPath + " resolves under the repository: " + p.Dir)
		case p.Module != nil && p.Module.Main && !under(p.Dir, stock):
			return stockSetup("the main-module package " + p.ImportPath + " resolves outside stock/: " + p.Dir)
		case p.Module != nil && p.Module.Path == sdkModule && !under(p.Dir, base):
			return stockSetup("the SDK package " + p.ImportPath + " resolves outside the base copy: " + p.Dir)
		}
		if p.ForTest == "" && !strings.Contains(p.ImportPath, " [") {
			pkgs[p.ImportPath] = p
		}
	}
	listGo := func(dir string, test bool) []string {
		var names []string
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") == test {
				names = append(names, filepath.Base(f))
			}
		}
		return names
	}
	same := func(what string, got, want []string) error {
		got, want = slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))
		if !slices.Equal(got, want) {
			return stockSetup(fmt.Sprintf("%s is %v, not %v", what, got, want))
		}
		return nil
	}
	const app = "github.com/bjarneo/cliamp/external/spotify"
	sp, ok := pkgs[app]
	if !ok {
		return stockSetup("go list -deps -test did not list " + app)
	}
	dir := filepath.Join(stock, "external", "spotify")
	if sp.Dir != dir {
		return stockSetup(app + " resolves to " + sp.Dir + ", not the staged " + dir)
	}
	if err := errors.Join(
		same(app+" GoFiles", sp.GoFiles, listGo(dir, false)),
		same(app+" TestGoFiles", sp.TestGoFiles, append(listGo(dir, true), appO[app]...)),
		same(app+" XTestGoFiles", sp.XTestGoFiles, nil),
		same(app+" IgnoredGoFiles", sp.IgnoredGoFiles, nil),
		same(app+" CgoFiles", sp.CgoFiles, nil),
	); err != nil {
		return err
	}
	for path, files := range sdkO {
		p, ok := pkgs[path]
		if !ok {
			return stockSetup("go list -deps -test did not list " + path)
		}
		if err := same(path+" GoFiles", p.GoFiles, append(listGo(p.Dir, false), files...)); err != nil {
			return err
		}
	}
	return nil
}

// checkStockProbes is §4.4 on the probe run's go test -json record: no
// forbidden output; exactly the named probes ran, each PASS; in each, every
// precondition marker in order, then exactly one red marker after them. A
// probe that fails is classified from its own output: a live precondition
// that did not hold, a red not shown, or else the harness (cleanup, poison,
// quiescence, T9) or the toolchain. None of these is I11 credit.
func checkStockProbes(rec *testRecord) ([]string, error) {
	var notes []string
	for _, f := range composedForbidden {
		if strings.Contains(rec.all.String(), f) {
			return nil, stockSetup("the stock probes' output holds " + f)
		}
	}
	var names []string
	for name, res := range rec.results {
		if strings.Contains(name, "/") {
			continue
		}
		if _, ok := stockProbeMarkers[name]; !ok {
			return nil, stockSetup("an unnamed test ran in the stock stage: " + name + " " + res)
		}
	}
	for name := range stockProbeMarkers {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		m := stockProbeMarkers[name]
		out := ""
		if b := rec.output[name]; b != nil {
			out = b.String()
		}
		switch res := rec.results[name]; {
		case res == "pass":
		case strings.Contains(out, "I11-PRECONDITION-FAIL"):
			return nil, stockSetup(name + ": a live precondition did not hold (never I11 credit)")
		case strings.Contains(out, "I11-NOT-SHOWN"):
			return nil, stockSetup(name + ": its I11 red was not shown (never I11 credit)")
		default:
			return nil, stockSetup(fmt.Sprintf("%s did not pass (%q): the harness or the toolchain (never I11 credit)", name, res))
		}
		at := 0
		for _, p := range m.pre {
			marker := "I11-PRECONDITION-OK: " + m.row + " " + p
			i := strings.Index(out[at:], marker)
			if i < 0 {
				return nil, stockSetup(fmt.Sprintf("%s: the precondition marker %q is missing or out of order", name, marker))
			}
			at += i + len(marker)
		}
		red := "I11-STOCK-RED: " + m.row + " "
		if strings.Count(out, red) != 1 || !strings.Contains(out[at:], red) {
			return nil, stockSetup(fmt.Sprintf("%s: not exactly one red marker after its preconditions", name))
		}
		line := out[at+strings.Index(out[at:], red):]
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		notes = append(notes, fmt.Sprintf("STOCK-I11-OK %s: %d preconditions; %s", name, len(m.pre), line))
	}
	return notes, nil
}

// stockProbes is step 15's probe part (plan v9 §4.3-4.4, §9 step 15), on the
// stage stockStage left in r.dir: the bound O overlaid onto the staged rc.4
// tree and the base copy, the compiled set's exact identity, then either a
// compile of the overlaid package (no test executes) or the stock probes,
// once, with -run '^TestDDSonicStock'. The base copy is base-h1 before and
// after, and the staged tree and O are unchanged after.
func (r *runner) stockProbes(prov *provenance, run bool) ([]string, error) {
	var notes []string
	stock := filepath.Join(r.dir, "stock")
	base := filepath.Join(r.dir, "stockbase")
	modfile := "-modfile=" + filepath.Join(r.dir, "stockmod", "stock.mod")
	td := filepath.Join(r.repo, "external", "spotify", "sdkcontract", "testdata")
	if got, err := hash1(base, sdkModule+"@"+sdkPin); err != nil || got != prov.baseH1 {
		return notes, stockSetup(fmt.Sprintf("before the probes, the base copy is %s (%v), not base-h1 %s", got, err, prov.baseH1))
	}
	notes = append(notes, "STOCK-BASE-H1-OK before the probes")
	// O, bound, copied out of the repository: the overlay never points into
	// it, and no O file may stand in for a staged or base file.
	const app = "github.com/bjarneo/cliamp/external/spotify"
	odir := filepath.Join(r.dir, "stockO")
	if err := os.MkdirAll(odir, 0o755); err != nil {
		return notes, stockSetup(err.Error())
	}
	replace := map[string]string{}
	appO, sdkO := map[string][]string{}, map[string][]string{}
	for _, o := range stockOverlay {
		data, err := os.ReadFile(filepath.Join(td, o.src))
		if err != nil {
			return notes, stockSetup("O: " + err.Error())
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != o.sha {
			return notes, stockSetup(fmt.Sprintf("O: %s is %x, not the bound %s", o.src, sum, o.sha))
		}
		local := filepath.Join(odir, o.src)
		if err := os.WriteFile(local, data, 0o444); err != nil {
			return notes, stockSetup(err.Error())
		}
		kind, dst, _ := strings.Cut(o.dst, ":")
		var target string
		switch kind {
		case "app":
			target = filepath.Join(stock, "external", "spotify", dst)
			appO[app] = append(appO[app], dst)
		case "sdk":
			target = filepath.Join(base, filepath.FromSlash(dst))
			pkg := sdkModule + "/" + path.Dir(dst)
			sdkO[pkg] = append(sdkO[pkg], path.Base(dst))
		}
		if _, err := os.Stat(target); err == nil {
			return notes, stockSetup("O: " + target + " exists; an O file adds, it never replaces")
		}
		replace[target] = local
	}
	ov, err := json.Marshal(map[string]any{"Replace": replace})
	if err == nil {
		err = os.WriteFile(filepath.Join(r.dir, "stock-overlay.json"), ov, 0o644)
	}
	if err != nil {
		return notes, stockSetup(err.Error())
	}
	overlay := "-overlay=" + filepath.Join(r.dir, "stock-overlay.json")
	notes = append(notes, fmt.Sprintf("STOCK-O-BOUND files=%d (app %d, sdk %d)", len(stockOverlay), len(appO[app]), len(stockOverlay)-len(appO[app])))
	// The identity (§4.3).
	out, err := r.tool(stockStep, "go list -deps -test (stock)", stock, 10*time.Minute, "list", modfile, overlay, "-deps", "-test", "-json", "./external/spotify")
	if err != nil {
		return notes, stockSetup(err.Error())
	}
	if err := checkStockIdentity(out, r.repo, stock, base, appO, sdkO); err != nil {
		return notes, err
	}
	notes = append(notes, "STOCK-IDENTITY-OK external/spotify = P + app O; the main module under stock/; the SDK under the base copy, ap with the SDK O")
	if !run {
		// A compile of the overlaid stock package; no test executes.
		if _, err := r.tool(stockStep, "compile the overlaid stock package", stock, 15*time.Minute, "test", modfile, overlay, "-race", "-count=1", "-run", "^$", "./external/spotify"); err != nil {
			return notes, stockSetup(err.Error())
		}
		notes = append(notes, "STOCK-O-COMPILE-OK")
	} else {
		// The probes, once (§9 step 15: -run '^TestDDSonicStock', 30 min).
		out, err = r.tool(stockStep, "the stock probes", stock, 30*time.Minute, "test", modfile, overlay, "-race", "-count=1", "-json", "-timeout", "29m", "-run", "^TestDDSonicStock", "./external/spotify")
		got, perr := checkStockProbes(readTestJSON(out))
		if perr != nil {
			if err != nil {
				return notes, errors.Join(perr, err)
			}
			return notes, perr
		}
		if err != nil {
			return notes, stockSetup("the probes passed their checks but go test failed: " + err.Error())
		}
		notes = append(notes, got...)
	}
	// After: the base copy, the stage and O unchanged.
	if got, err := hash1(base, sdkModule+"@"+sdkPin); err != nil || got != prov.baseH1 {
		return notes, stockSetup(fmt.Sprintf("the base copy changed during the probes: %s (%v)", got, err))
	}
	data, err := os.ReadFile(filepath.Join(r.repo, filepath.FromSlash(stockArchivePath)))
	if err != nil {
		return notes, stockSetup(err.Error())
	}
	members, _, err := readStockArchive(data)
	if err != nil {
		return notes, err
	}
	if err := checkStaged(members, stock); err != nil {
		return notes, err
	}
	for _, o := range stockOverlay {
		data, err := os.ReadFile(filepath.Join(odir, o.src))
		if sum := sha256.Sum256(data); err != nil || hex.EncodeToString(sum[:]) != o.sha {
			return notes, stockSetup("O changed during the probes: " + o.src)
		}
	}
	notes = append(notes, "STOCK-BASE-H1-OK after the probes; STOCK-STAGE-UNCHANGED; STOCK-O-UNCHANGED")
	return notes, nil
}

// stockProbeTest runs the stock stage, then stockProbes, standalone: the
// same two calls run() makes as its step 15, in its own runner.
func stockProbeTest(t *testing.T, run bool) {
	r := newRunner(t)
	if err := r.environment(); err != nil {
		t.Fatal(err)
	}
	prov, err := readProvenance(r.repo)
	if err != nil {
		t.Fatal(err)
	}
	baseDir, err := r.downloadBase(prov)
	if err != nil {
		t.Fatal(err)
	}
	notes, err := r.stockStage(baseDir, prov)
	for _, n := range notes {
		t.Log(n)
	}
	if err != nil {
		t.Fatal(err)
	}
	notes, err = r.stockProbes(prov, run)
	for _, n := range notes {
		t.Log(n)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// TestSDKContractStockProbeCompile is the declared compile-only diagnostic:
// the stage, O, the identity and a compile of the overlaid package. No probe
// executes, and it earns no credit.
func TestSDKContractStockProbeCompile(t *testing.T) {
	stockProbeTest(t, false)
	t.Log("STOCK-PROBE-COMPILE-OK")
}

// TestSDKContractStockProbes runs the stock probes once (plan v9 §4.4, §8
// I11). Its failures are setup failures or an I11 not shown.
func TestSDKContractStockProbes(t *testing.T) {
	stockProbeTest(t, true)
	t.Log("STOCK-PROBES-OK")
}

// stockMembers reads the repository's archive for the controls below.
func stockMembers(t *testing.T) map[string][]byte {
	t.Helper()
	repo, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(stockArchivePath)))
	if err != nil {
		t.Fatal(err)
	}
	members, _, err := readStockArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	return members
}

// writeStockTar writes members as the deterministic build does, plus extra
// raw headers, for the negative controls.
func writeStockTar(t *testing.T, members map[string][]byte, extra ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for n := range members {
			if !yield(n) {
				return
			}
		}
	}) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(members[name])), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
		tw.Write(members[name])
	}
	for _, h := range extra {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	return buf.Bytes()
}

// relist rewrites F for members (a consistent but wrong archive).
func relist(members map[string][]byte) {
	var b strings.Builder
	for _, n := range slices.Sorted(func(yield func(string) bool) {
		for n := range members {
			if !yield(n) {
				return
			}
		}
	}) {
		if n == "MANIFEST.sha256" {
			continue
		}
		s := sha256.Sum256(members[n])
		fmt.Fprintf(&b, "%x  %s\n", s, n)
	}
	members["MANIFEST.sha256"] = []byte(b.String())
}

// wantStockSetupFailure runs the archive checks on data and requires a step-15
// setup failure that says want.
func wantStockSetupFailure(t *testing.T, data []byte, want string) {
	t.Helper()
	members, _, err := readStockArchive(data)
	if err == nil {
		err = checkStockSets(members)
	}
	if err == nil {
		_, err = checkStockMerkle(members)
	}
	var se *stepError
	if !errors.As(err, &se) || se.step != stockStep || !strings.Contains(err.Error(), "setup failure") || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v; want a step-%d setup failure saying %q", err, stockStep, want)
	}
}

// The repository's archive passes every check: the POSITIVE control (the
// others below are negative controls, each refusing a changed archive with
// its named step-15 setup failure); the controls start from this archive.
func TestStockArchiveIsValid(t *testing.T) {
	members := stockMembers(t)
	if err := checkStockSets(members); err != nil {
		t.Fatal(err)
	}
	if _, err := checkStockMerkle(members); err != nil {
		t.Fatal(err)
	}
}

func TestStockArchiveTamperedPayloadFails(t *testing.T) {
	m := stockMembers(t)
	m["payload/external/spotify/session.go"] = append(m["payload/external/spotify/session.go"], '\n')
	wantStockSetupFailure(t, writeStockTar(t, m), "the hash of payload/external/spotify/session.go differs")
}

func TestStockArchiveConsistentTamperFails(t *testing.T) {
	m := stockMembers(t)
	m["payload/external/spotify/session.go"] = append(m["payload/external/spotify/session.go"], '\n')
	relist(m)
	wantStockSetupFailure(t, writeStockTar(t, m), "payload/external/spotify/session.go: blob oid")
}

func TestStockArchiveMissingMemberFails(t *testing.T) {
	m := stockMembers(t)
	delete(m, "payload/external/spotify/session.go")
	relist(m)
	wantStockSetupFailure(t, writeStockTar(t, m), "the sets are M 2 and P 132")
}

func TestStockArchiveExtraMemberFails(t *testing.T) {
	m := stockMembers(t)
	delete(m, "payload/applog/applog.go")
	m["payload/external/spotify/extra_ddsonic.go"] = []byte("package spotify\n")
	relist(m)
	wantStockSetupFailure(t, writeStockTar(t, m), "is in rc.4's listing but missing from P")
}

func TestStockArchiveBadCommitFails(t *testing.T) {
	m := stockMembers(t)
	c := bytes.Clone(m["proof/COMMIT.txt"])
	c[len(c)-2] ^= 1
	m["proof/COMMIT.txt"] = c
	relist(m)
	wantStockSetupFailure(t, writeStockTar(t, m), "COMMIT.txt's framed id")
}

func TestStockArchiveBadTreeFails(t *testing.T) {
	m := stockMembers(t)
	mk := string(m["proof/MERKLE.txt"])
	i := strings.Index(mk, "\tsdkcontract\n")
	mk = mk[:i-1] + string("0123456789abcdef"[(strings.IndexByte("0123456789abcdef", mk[i-1])+1)%16]) + mk[i:]
	m["proof/MERKLE.txt"] = []byte(mk)
	relist(m)
	wantStockSetupFailure(t, writeStockTar(t, m), "recomputes to")
}

// stockReplaceLine replaces, in proof/MERKLE.txt, the one line equal to old
// with new (an empty new removes it), and relists F.
func stockReplaceLine(t *testing.T, m map[string][]byte, old, new string) {
	t.Helper()
	mk := string(m["proof/MERKLE.txt"])
	if strings.Count(mk, old+"\n") != 1 {
		t.Fatalf("MERKLE.txt holds %q %d times", old, strings.Count(mk, old+"\n"))
	}
	if new == "" {
		mk = strings.Replace(mk, old+"\n", "", 1)
	} else {
		mk = strings.Replace(mk, old+"\n", new+"\n", 1)
	}
	m["proof/MERKLE.txt"] = []byte(mk)
	relist(m)
}

// stockTreeID is Git's id of a listing as written (no substitution): the
// controls' own tree computation, to build internally consistent subtrees.
func stockTreeID(t *testing.T, entries []stockEntry) string {
	t.Helper()
	var body bytes.Buffer
	for _, e := range entries {
		raw, err := hex.DecodeString(e.oid)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&body, "%s %s\x00", strings.TrimLeft(e.mode, "0"), e.name)
		body.Write(raw)
	}
	return gitOID("tree", body.Bytes())
}

// stockDeepTamper changes payload/external/spotify/session.go by one byte,
// same length, and makes everything below the root's "external" entry
// internally consistent: session.go's blob oid in external/spotify's
// listing, external/spotify's tree oid in external's listing, and F. The
// root's "external" entry still holds the authentic oid. A non-empty label
// also replaces that entry's type label, keeping its mode 040000 and oid, so
// the root's bytes are unchanged: review 54's R54-1 counterexample.
func stockDeepTamper(t *testing.T, label string) []byte {
	t.Helper()
	m := stockMembers(t)
	src := bytes.Clone(m["payload/external/spotify/session.go"])
	src[100] ^= 0x20
	m["payload/external/spotify/session.go"] = src
	trees, err := parseMerkle(m["proof/MERKLE.txt"])
	if err != nil {
		t.Fatal(err)
	}
	var oldBlob, oldSpotify string
	spotify := slices.Clone(trees["external/spotify"])
	for i, e := range spotify {
		if e.name == "session.go" {
			oldBlob = e.oid
			spotify[i].oid = gitOID("blob", src)
		}
	}
	for _, e := range trees["external"] {
		if e.name == "spotify" {
			oldSpotify = e.oid
		}
	}
	newSpotify := stockTreeID(t, spotify)
	stockReplaceLine(t, m, "100644 blob "+oldBlob+"\tsession.go", "100644 blob "+gitOID("blob", src)+"\tsession.go")
	stockReplaceLine(t, m, "040000 tree "+oldSpotify+"\tspotify", "040000 tree "+newSpotify+"\tspotify")
	if label != "" {
		stockReplaceLine(t, m, "040000 tree 5072528c0ff4eb69cee816265a1c4cbb0df7a97b\texternal", "040000 "+label+" 5072528c0ff4eb69cee816265a1c4cbb0df7a97b\texternal")
	}
	return writeStockTar(t, m)
}

// R54-1: the relabelled edge with an internally consistent deep tamper is
// refused (its mode/type pair is not Git's), for each non-tree label.
func TestStockArchiveTypeLabelDeepTamperFails(t *testing.T) {
	for _, label := range []string{"blob", "commit"} {
		t.Run(label, func(t *testing.T) {
			wantStockSetupFailure(t, stockDeepTamper(t, label), "an unsupported mode/type pair")
		})
	}
}

// The same consistent deep tamper with the authentic label: the parent edge
// check finds the recomputed subtree differs.
func TestStockArchiveDeepTamperFails(t *testing.T) {
	wantStockSetupFailure(t, stockDeepTamper(t, ""), "tree external recomputes to")
}

// A listed directory linked through a valid but non-tree pair is refused.
func TestStockArchiveNonTreeEdgeFails(t *testing.T) {
	m := stockMembers(t)
	stockReplaceLine(t, m, "040000 tree 5072528c0ff4eb69cee816265a1c4cbb0df7a97b\texternal", "100644 blob 5072528c0ff4eb69cee816265a1c4cbb0df7a97b\texternal")
	wantStockSetupFailure(t, writeStockTar(t, m), "listed directory external is linked by a non-tree entry")
}

// A listed directory its parent does not list at all is disconnected.
func TestStockArchiveDisconnectedChildFails(t *testing.T) {
	m := stockMembers(t)
	stockReplaceLine(t, m, "040000 tree 5072528c0ff4eb69cee816265a1c4cbb0df7a97b\texternal", "")
	wantStockSetupFailure(t, writeStockTar(t, m), "listed directory external is not linked from its parent's listing")
}

func TestStockArchiveEscapingNameFails(t *testing.T) {
	wantStockSetupFailure(t, writeStockTar(t, stockMembers(t), &tar.Header{Name: "payload/../escape.go", Mode: 0o644, ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}), "is not a plain relative name")
}

func TestStockArchiveSymlinkFails(t *testing.T) {
	wantStockSetupFailure(t, writeStockTar(t, stockMembers(t), &tar.Header{Name: "payload/zz-link", Linkname: "/etc/passwd", Mode: 0o644, ModTime: time.Unix(0, 0), Typeflag: tar.TypeSymlink, Format: tar.FormatUSTAR}), "is not a regular file")
}

func TestStockStagingExtraFileFails(t *testing.T) {
	m := stockMembers(t)
	dir := filepath.Join(t.TempDir(), "stock")
	if err := stageStock(m, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "external", "spotify", "recovery_ddsonic.go"), []byte("package spotify\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var se *stepError
	if err := checkStaged(m, dir); !errors.As(err, &se) || se.step != stockStep || !strings.Contains(err.Error(), "is staged but is not in P") {
		t.Fatalf("an extra staged file gave %v; want a step-%d setup failure", err, stockStep)
	}
}
