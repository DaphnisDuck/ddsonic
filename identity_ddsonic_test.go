// ddsonic: a guard on where the product's name is written down.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// nameLiterals lists the production Go files that may spell the product's
// name in a string, with how often and why. appdir.Name is the one
// definition: folders, the socket and log, the MPRIS and client names, the
// User-Agent, the config section and the config variable all derive from it.
// A string that needs the name takes it from appdir.Name or
// appmeta.ClientName(). When the name must be written out, add the file here
// with its reason; a rename then starts from this list.
var nameLiterals = map[string]struct {
	count int
	why   string
}{
	"internal/appdir/ddsonic.go":  {1, "the definition"},
	"internal/appmeta/appmeta.go": {1, "the project's GitHub address, an external name that happens to match"},
	"cmd/setup_ddsonic.go":        {5, "sentences of setup help, one naming docs/ddsonic/youtube.md"},
	"commands_ddsonic.go":         {1, "the path docs/ddsonic/youtube.md in an error"},
	"catalog/sqlite/store.go":     {1, "an error's text; the catalog packages import no application identity"},
	"acceptance/catalog.go":       {1, "the path docs/ddsonic/acceptance/cases.yaml"},
}

// TestProductNameIsWrittenInOnePlace fails when a string in production Go
// code spells the product's name outside nameLiterals. Comments, such as the
// "// ddsonic:" tags on edits to upstream files, are not strings and are
// not counted; neither are test files.
func TestProductNameIsWrittenInOnePlace(t *testing.T) {
	name := strings.ToLower(appdir.Name)
	got := map[string][]string{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); path != "." && (strings.HasPrefix(n, ".") || n == "vendor" || n == "dist" || n == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || !strings.Contains(strings.ToLower(lit.Value), name) {
				return true
			}
			key := filepath.ToSlash(path)
			got[key] = append(got[key], key+":"+strconv.Itoa(fset.Position(lit.Pos()).Line))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for file, at := range got {
		allowed, ok := nameLiterals[file]
		switch {
		case !ok:
			t.Errorf("%s spells %q in a string; use appdir.Name or appmeta.ClientName(), or list the file in nameLiterals with a reason:\n  %s",
				file, name, strings.Join(at, "\n  "))
		case len(at) != allowed.count:
			t.Errorf("%s spells %q in %d strings, nameLiterals says %d (%s):\n  %s",
				file, name, len(at), allowed.count, allowed.why, strings.Join(at, "\n  "))
		}
	}
	for file := range nameLiterals {
		if len(got[file]) == 0 {
			t.Errorf("nameLiterals lists %s, which no longer spells %q; remove the entry", file, name)
		}
	}
}
