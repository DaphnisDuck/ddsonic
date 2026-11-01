// Package sdkgate is ddsonic's symbols gate over the patched go-librespot
// (patches/go-librespot/0002-audio-key-requests-across-connection-changes.patch):
// T-S1, the use whitelist of the keylink hook, the announcer slot and
// ErrTaken, and T-S2, the public API delta against the upstream base. It is
// a maintenance and test restriction, not runtime access control. The
// sdkcontract runner runs it against the staged active tree; it is in no
// ddsonic build.
package sdkgate

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	SDK     = "github.com/devgianlu/go-librespot"
	apPath  = SDK + "/ap"
	audio   = SDK + "/audio"
	keylink = SDK + "/internal/keylink"
)

// Load type-checks ap, audio and internal/keylink, with their tests, from
// the module in dir, with overlay applied.
func Load(dir string, overlay map[string][]byte) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Dir:     dir,
		Tests:   true,
		Overlay: overlay,
		Env:     os.Environ(),
	}
	pkgs, err := packages.Load(cfg, apPath, audio, keylink)
	if err != nil {
		return nil, err
	}
	var errs []string
	for _, p := range pkgs {
		for _, e := range p.Errors {
			errs = append(errs, e.Error())
		}
	}
	if len(errs) != 0 {
		return nil, fmt.Errorf("loading the SDK: %s", strings.Join(errs, "; "))
	}
	return pkgs, nil
}

// unit is one source file with the type information of a package it is in.
type unit struct {
	pkg  *packages.Package
	file *ast.File
	name string
	test bool
}

func units(pkgs []*packages.Package) []unit {
	seen := map[string]bool{}
	var out []unit
	for _, p := range pkgs {
		for _, f := range p.Syntax {
			name := p.Fset.Position(f.Pos()).Filename
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, unit{p, f, name, strings.HasSuffix(name, "_test.go")})
		}
	}
	return out
}

// pkgPath is the import path of the package the unit's file belongs to,
// without an external test package's suffix.
func (u unit) pkgPath() string { return strings.TrimSuffix(u.pkg.PkgPath, "_test") }

func (u unit) pos(n ast.Node) string {
	p := u.pkg.Fset.Position(n.Pos())
	return fmt.Sprintf("%s:%d", filepath.Base(p.Filename), p.Line)
}

// visit calls f for every identifier in u's file with its ancestors,
// outermost first, ending with the identifier.
func visit(u unit, f func(id *ast.Ident, stack []ast.Node)) {
	var stack []ast.Node
	ast.Inspect(u.file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		if id, ok := n.(*ast.Ident); ok {
			f(id, stack)
		}
		return true
	})
}

func isAttach(o types.Object) bool {
	v, ok := o.(*types.Var)
	return ok && !v.IsField() && v.Pkg() != nil && v.Pkg().Path() == keylink && v.Name() == "AttachAnnouncer"
}

func isErrTaken(o types.Object) bool {
	v, ok := o.(*types.Var)
	return ok && !v.IsField() && v.Pkg() != nil && v.Pkg().Path() == keylink && v.Name() == "ErrTaken"
}

func isAnnouncer(o types.Object) bool {
	v, ok := o.(*types.Var)
	return ok && v.IsField() && v.Pkg() != nil && v.Pkg().Path() == apPath && v.Name() == "announcer"
}

// expr is the expression an identifier stands for (the selector whose
// selected name it is, or itself, with any parentheses around it), what
// contains that expression, and the ancestors above that.
func expr(stack []ast.Node) (e ast.Expr, parent ast.Node, rest []ast.Node) {
	i := len(stack) - 1
	e = stack[i].(*ast.Ident)
	if i > 0 {
		if sel, ok := stack[i-1].(*ast.SelectorExpr); ok && sel.Sel == stack[i] {
			e, i = sel, i-1
		}
	}
	for i > 0 {
		p, ok := stack[i-1].(*ast.ParenExpr)
		if !ok {
			break
		}
		e, i = p, i-1
	}
	if i == 0 {
		return e, nil, nil
	}
	return e, stack[i-1], stack[:i-1]
}

func enclosingFunc(stack []ast.Node) *ast.FuncDecl {
	for i := len(stack) - 1; i >= 0; i-- {
		if f, ok := stack[i].(*ast.FuncDecl); ok {
			return f
		}
	}
	return nil
}

func innermostLit(stack []ast.Node) *ast.FuncLit {
	for i := len(stack) - 1; i >= 0; i-- {
		if f, ok := stack[i].(*ast.FuncLit); ok {
			return f
		}
	}
	return nil
}

func isNil(info *types.Info, e ast.Expr) bool {
	id, ok := ast.Unparen(e).(*ast.Ident)
	if !ok {
		return false
	}
	_, isNil := info.Uses[id].(*types.Nil)
	return isNil
}

// isNilComparison: e is an operand of == or != (of op, unless op is
// ILLEGAL) whose other operand is nil.
func isNilComparison(info *types.Info, e ast.Expr, parent ast.Node, op token.Token) bool {
	b, ok := parent.(*ast.BinaryExpr)
	if !ok || (b.Op != token.EQL && b.Op != token.NEQ) || (op != token.ILLEGAL && b.Op != op) {
		return false
	}
	switch e {
	case b.X:
		return isNil(info, b.Y)
	case b.Y:
		return isNil(info, b.X)
	}
	return false
}

func inLHS(e ast.Expr, parent ast.Node) (*ast.AssignStmt, bool) {
	a, ok := parent.(*ast.AssignStmt)
	return a, ok && slices.Contains(a.Lhs, e)
}

// Check applies T-S1 to the loaded packages, and the import scan to the
// files under root and in overlay. It returns every violation found.
func Check(pkgs []*packages.Package, root string, overlay map[string][]byte) []string {
	var v []string
	bad := func(format string, args ...any) { v = append(v, fmt.Sprintf(format, args...)) }

	v = append(v, importScan(root, overlay)...)
	us := units(pkgs)

	// keylink.AttachAnnouncer: one definition; one assignment, a function
	// literal, directly in ap's init; one production call, in
	// NewAudioKeyProvider, of its access point parameter and p.announce;
	// tests call it and compare it with nil only.
	var (
		attachDefs, attachAssigns, attachCalls int
		installer                              *ast.FuncLit
		installerUnit                          unit
	)
	for _, u := range us {
		info := u.pkg.TypesInfo
		visit(u, func(id *ast.Ident, stack []ast.Node) {
			if o := info.Defs[id]; o != nil && isAttach(o) {
				attachDefs++
				if u.pkgPath() != keylink || u.test {
					bad("%s: AttachAnnouncer defined outside internal/keylink", u.pos(id))
				}
				return
			}
			o := info.Uses[id]
			if o == nil || !isAttach(o) {
				return
			}
			e, parent, _ := expr(stack)
			fn := enclosingFunc(stack)
			if a, ok := inLHS(e, parent); ok {
				attachAssigns++
				var lit *ast.FuncLit
				if len(a.Rhs) == 1 {
					lit, _ = a.Rhs[0].(*ast.FuncLit)
				}
				if u.test || u.pkgPath() != apPath || fn == nil || fn.Recv != nil || fn.Name.Name != "init" ||
					a.Tok != token.ASSIGN || len(a.Lhs) != 1 || lit == nil || !slices.Contains(fn.Body.List, ast.Stmt(a)) {
					bad("%s: an assignment to AttachAnnouncer that is not ap.init's installer", u.pos(id))
					return
				}
				installer, installerUnit = lit, u
				return
			}
			if call, ok := parent.(*ast.CallExpr); ok && call.Fun == e {
				if u.test {
					return
				}
				attachCalls++
				if !supportedCall(info, u, fn, call) {
					bad("%s: a production call of AttachAnnouncer that is not NewAudioKeyProvider's", u.pos(id))
				}
				return
			}
			if u.test && isNilComparison(info, e, parent, token.ILLEGAL) {
				return
			}
			bad("%s: AttachAnnouncer used as a value (address, alias, argument or store)", u.pos(id))
		})
	}
	if attachDefs != 1 {
		bad("AttachAnnouncer has %d definitions, want 1", attachDefs)
	}
	if attachAssigns != 1 {
		bad("AttachAnnouncer is assigned %d times, want 1 (ap.init)", attachAssigns)
	}
	if attachCalls != 1 {
		bad("AttachAnnouncer has %d production calls, want 1 (NewAudioKeyProvider)", attachCalls)
	}

	// ap.Accesspoint.announcer: W0 its declaration; W1 the installer's
	// empty-slot read and one write after it, under connMu.Lock; W2 the
	// publisher's snapshot, guard and call; W3 read-only test uses.
	var (
		decls, w1Reads, w1Writes, w2 int
		w1Check, w1Write             ast.Stmt
		w2Func                       *ast.FuncDecl
		w2Unit                       unit
	)
	for _, u := range us {
		info := u.pkg.TypesInfo
		visit(u, func(id *ast.Ident, stack []ast.Node) {
			if o := info.Defs[id]; o != nil && isAnnouncer(o) {
				decls++
				return
			}
			o := info.Uses[id]
			if o == nil || !isAnnouncer(o) {
				return
			}
			e, parent, rest := expr(stack)
			_, write := inLHS(e, parent)
			_, other := parent.(*ast.UnaryExpr) // an address, or any other operator
			if call, ok := parent.(*ast.CallExpr); ok && call.Fun == e {
				other = true // a call of the slot itself
			}
			if u.test {
				// W3: a test only compares the slot with nil; no write,
				// call, address or alias, so nothing can call it.
				if !isNilComparison(info, e, parent, token.ILLEGAL) {
					bad("%s: a test uses announcer other than comparing it with nil", u.pos(id))
				}
				return
			}
			lit := innermostLit(stack)
			if installer != nil && lit == installer {
				switch {
				case write && !other:
					w1Writes++
					w1Write = stmtOf(rest, parent)
				case isNilComparison(info, e, parent, token.NEQ) && emptySlotCheck(info, rest, parent):
					w1Reads++
					w1Check = stmtOf(rest, parent)
				default:
					bad("%s: a use of announcer in the installer other than its empty-slot check and its write", u.pos(id))
				}
				return
			}
			fn := enclosingFunc(stack)
			if !write && !other && lit == nil && fn != nil && snapshotGuardCall(info, fn, e, parent) {
				w2++
				w2Func, w2Unit = fn, u
				return
			}
			bad("%s: a use of announcer outside W1 and W2", u.pos(id))
		})
	}
	if decls != 1 {
		bad("announcer is declared %d times, want 1", decls)
	}
	if w1Reads != 1 || w1Writes != 1 {
		bad("the installer has %d empty-slot checks and %d writes of announcer, want 1 and 1", w1Reads, w1Writes)
	} else {
		v = append(v, installerShape(installerUnit, installer, w1Check, w1Write)...)
	}
	if w2 != 1 {
		bad("announcer has %d publisher snapshots, want 1", w2)
	} else {
		v = append(v, publicationPath(us, w2Func, w2Unit)...)
	}

	// keylink.ErrTaken: keylink, the installer, ownerFrom and tests only;
	// never assigned and never addressed.
	for _, u := range us {
		info := u.pkg.TypesInfo
		visit(u, func(id *ast.Ident, stack []ast.Node) {
			o := info.Uses[id]
			if o == nil || !isErrTaken(o) {
				return
			}
			e, parent, _ := expr(stack)
			_, write := inLHS(e, parent)
			_, addr := parent.(*ast.UnaryExpr)
			fn := enclosingFunc(stack)
			switch {
			case write || addr:
				bad("%s: ErrTaken assigned or addressed", u.pos(id))
			case u.test, u.pkgPath() == keylink:
			case installer != nil && innermostLit(stack) == installer:
			case u.pkgPath() == audio && fn != nil && fn.Recv == nil && fn.Name.Name == "ownerFrom":
			default:
				bad("%s: ErrTaken referenced outside keylink, the installer, ownerFrom and tests", u.pos(id))
			}
		})
	}
	return v
}

// supportedCall: keylink.AttachAnnouncer(a, p.announce) inside
// audio.NewAudioKeyProvider, a its *ap.Accesspoint parameter and p.announce
// the method value of a local *KeyProvider.
func supportedCall(info *types.Info, u unit, fn *ast.FuncDecl, call *ast.CallExpr) bool {
	if u.pkgPath() != audio || fn == nil || fn.Recv != nil || fn.Name.Name != "NewAudioKeyProvider" || len(call.Args) != 2 {
		return false
	}
	arg, ok := call.Args[0].(*ast.Ident)
	if !ok || info.Uses[arg] == nil || info.Uses[arg].Type().String() != "*"+apPath+".Accesspoint" {
		return false
	}
	param := false
	for _, f := range fn.Type.Params.List {
		for _, n := range f.Names {
			param = param || info.Defs[n] == info.Uses[arg]
		}
	}
	sel, ok := call.Args[1].(*ast.SelectorExpr)
	if !ok || !param || sel.Sel.Name != "announce" {
		return false
	}
	s := info.Selections[sel]
	recv, ok := ast.Unparen(sel.X).(*ast.Ident)
	if s == nil || s.Kind() != types.MethodVal || !ok || s.Recv().String() != "*"+audio+".KeyProvider" {
		return false
	}
	local := info.Uses[recv]
	return local != nil && local.Parent() != nil && local.Parent() != local.Pkg().Scope()
}

// stmtOf is the innermost statement holding an expression whose parent is
// given.
func stmtOf(rest []ast.Node, parent ast.Node) ast.Stmt {
	if s, ok := parent.(ast.Stmt); ok {
		return s
	}
	for i := len(rest) - 1; i >= 0; i-- {
		if s, ok := rest[i].(ast.Stmt); ok {
			return s
		}
	}
	return nil
}

// emptySlotCheck: the comparison is the whole condition of `if ... { return
// keylink.ErrTaken }`.
func emptySlotCheck(info *types.Info, rest []ast.Node, cmp ast.Node) bool {
	if len(rest) < 1 {
		return false
	}
	ifs, ok := rest[len(rest)-1].(*ast.IfStmt)
	if !ok || ifs.Cond != cmp || ifs.Init != nil || ifs.Else != nil || len(ifs.Body.List) != 1 {
		return false
	}
	ret, ok := ifs.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	sel, ok := ret.Results[0].(*ast.SelectorExpr)
	return ok && isErrTaken(info.Uses[sel.Sel])
}

// installerShape checks W1's whole form. The installer's statements are
// `a := x.(*Accesspoint)`, then `a.connMu.Lock()`, then only `defer
// a.connMu.Unlock()` and the empty-slot check until the write, which is
// `a.announcer = f`, f being the installer's callback parameter; and nothing
// in it unlocks a.connMu before returning. So the check and the write happen
// with that access point's lock held, and the slot is set to the callback.
func installerShape(u unit, lit *ast.FuncLit, check, write ast.Stmt) []string {
	info := u.pkg.TypesInfo
	fail := func(what string) []string { return []string{fmt.Sprintf("%s: %s", u.pos(lit), what)} }
	var params []types.Object
	for _, f := range lit.Type.Params.List {
		for _, n := range f.Names {
			params = append(params, info.Defs[n])
		}
	}
	list := lit.Body.List
	if len(params) != 2 || len(list) == 0 {
		return fail("the installer is not func(x, f) with a body")
	}
	def, ok := list[0].(*ast.AssignStmt)
	if !ok || def.Tok != token.DEFINE || len(def.Lhs) != 1 || len(def.Rhs) != 1 {
		return fail("the installer does not begin with a := x.(*Accesspoint)")
	}
	ta, ok := def.Rhs[0].(*ast.TypeAssertExpr)
	if !ok || types.ExprString(ta.Type) != "*Accesspoint" {
		return fail("the installer does not begin with a := x.(*Accesspoint)")
	}
	if x, ok := ta.X.(*ast.Ident); !ok || info.Uses[x] != params[0] {
		return fail("the installer does not assert its own parameter")
	}
	aID, _ := def.Lhs[0].(*ast.Ident)
	a := info.Defs[aID]
	// connMuCall is `a.connMu.<method>()` on the asserted access point.
	connMuCall := func(e ast.Expr, method string) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok || len(call.Args) != 0 {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != method {
			return false
		}
		inner, ok := sel.X.(*ast.SelectorExpr)
		if !ok || inner.Sel.Name != "connMu" {
			return false
		}
		id, ok := inner.X.(*ast.Ident)
		return ok && info.Uses[id] == a
	}
	onA := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && a != nil && info.Uses[id] == a
	}
	lock, ci, wi := -1, slices.Index(list, check), slices.Index(list, write)
	for i, st := range list {
		if es, ok := st.(*ast.ExprStmt); ok && connMuCall(es.X, "Lock") && lock < 0 {
			lock = i
		}
	}
	if lock < 0 || ci < 0 || wi < 0 || !(lock < ci && ci < wi) {
		return fail("the installer's check and write are not under that access point's connMu.Lock, in that order")
	}
	for _, st := range list[lock+1 : wi] {
		if st == check {
			continue
		}
		if d, ok := st.(*ast.DeferStmt); ok && connMuCall(d.Call, "Unlock") {
			continue
		}
		return fail("the installer's check and write are not under that access point's connMu.Lock: another statement between them")
	}
	unlocked := false
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.DeferStmt); ok {
			return false
		}
		if e, ok := n.(ast.Expr); ok && connMuCall(e, "Unlock") {
			unlocked = true
		}
		return true
	})
	if unlocked {
		return fail("the installer's check and write are not under that access point's connMu.Lock: it unlocks before returning")
	}
	ifs := check.(*ast.IfStmt)
	if b, ok := ifs.Cond.(*ast.BinaryExpr); !ok || !onA(b.X) {
		return fail("the installer's empty-slot check is not of the asserted access point's slot")
	}
	as, ok := write.(*ast.AssignStmt)
	if !ok || as.Tok != token.ASSIGN || len(as.Rhs) != 1 || !onA(as.Lhs[0]) {
		return fail("the installer's write is not to the asserted access point's slot")
	}
	if f, ok := as.Rhs[0].(*ast.Ident); !ok || info.Uses[f] != params[1] {
		return fail("the installer's write does not store its callback parameter")
	}
	return nil
}

// snapshotGuardCall: `obs := x.announcer` directly followed by `if obs !=
// nil { obs(...) }`, in a method of *Accesspoint, with obs used nowhere
// else.
func snapshotGuardCall(info *types.Info, fn *ast.FuncDecl, e ast.Expr, parent ast.Node) bool {
	a, ok := parent.(*ast.AssignStmt)
	if !ok || a.Tok != token.DEFINE || len(a.Lhs) != 1 || len(a.Rhs) != 1 || a.Rhs[0] != e {
		return false
	}
	if fn.Recv == nil || len(fn.Recv.List) != 1 || types.ExprString(fn.Recv.List[0].Type) != "*Accesspoint" {
		return false
	}
	obsID, ok := a.Lhs[0].(*ast.Ident)
	if !ok {
		return false
	}
	obs := info.Defs[obsID]
	i := slices.Index(fn.Body.List, ast.Stmt(a))
	if obs == nil || i < 0 || i+1 >= len(fn.Body.List) {
		return false
	}
	ifs, ok := fn.Body.List[i+1].(*ast.IfStmt)
	if !ok || ifs.Init != nil || ifs.Else != nil || len(ifs.Body.List) != 1 {
		return false
	}
	cond, ok := ifs.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.NEQ || !isNil(info, cond.Y) {
		return false
	}
	if x, ok := cond.X.(*ast.Ident); !ok || info.Uses[x] != obs {
		return false
	}
	es, ok := ifs.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	if f, ok := call.Fun.(*ast.Ident); !ok || info.Uses[f] != obs {
		return false
	}
	uses := 0
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && info.Uses[id] == obs {
			uses++
		}
		return true
	})
	return uses == 2
}

// publicationPath: the publisher step is called once in production, from a
// function that is itself called once in production, from
// (*Accesspoint).connect.
func publicationPath(us []unit, w2 *ast.FuncDecl, w2u unit) []string {
	callers := func(name string) (from []*ast.FuncDecl) {
		for _, u := range us {
			if u.test || u.pkgPath() != apPath {
				continue
			}
			for _, d := range u.file.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
							from = append(from, fd)
						}
					}
					return true
				})
			}
		}
		return from
	}
	one := callers(w2.Name.Name)
	if len(one) != 1 {
		return []string{fmt.Sprintf("%s: the publisher step %s has %d production callers, want 1", w2u.pos(w2), w2.Name.Name, len(one))}
	}
	two := callers(one[0].Name.Name)
	if len(two) != 1 || two[0].Name.Name != "connect" || two[0].Recv == nil {
		return []string{fmt.Sprintf("the publication %s is not called once, from (*Accesspoint).connect", one[0].Name.Name)}
	}
	return nil
}

// importScan: only files in ap and audio import internal/keylink.
func importScan(root string, overlay map[string][]byte) []string {
	var v []string
	check := func(path string, src []byte) {
		f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ImportsOnly)
		if err != nil {
			v = append(v, fmt.Sprintf("%s: %v", path, err))
			return
		}
		for _, im := range f.Imports {
			if p, _ := strconv.Unquote(im.Path.Value); p != keylink {
				continue
			}
			if rel, _ := filepath.Rel(root, filepath.Dir(path)); rel != "ap" && rel != "audio" {
				v = append(v, fmt.Sprintf("%s imports internal/keylink", path))
			}
		}
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if _, ok := overlay[path]; ok {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		check(path, src)
		return nil
	})
	if err != nil {
		v = append(v, err.Error())
	}
	for path, src := range overlay {
		if strings.HasSuffix(path, ".go") {
			check(path, src)
		}
	}
	return v
}

// API is a package's exported surface: each exported top-level name and
// each method on an exported type, with its signature or type; parameter
// names and unexported struct fields are left out.
func API(dir string) (map[string]string, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	str := func(e ast.Expr) string {
		var b bytes.Buffer
		_ = printer.Fprint(&b, fset, e)
		return b.String()
	}
	types := func(fl *ast.FieldList) string {
		if fl == nil {
			return ""
		}
		var parts []string
		for _, f := range fl.List {
			for range max(len(f.Names), 1) {
				parts = append(parts, str(f.Type))
			}
		}
		return strings.Join(parts, ", ")
	}
	api := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				key := "func " + d.Name.Name
				if d.Recv != nil {
					recv := strings.TrimPrefix(str(d.Recv.List[0].Type), "*")
					if !ast.IsExported(recv) {
						continue
					}
					key = "method " + recv + "." + d.Name.Name
				}
				api[key] = "(" + types(d.Type.Params) + ") (" + types(d.Type.Results) + ")"
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if !s.Name.IsExported() {
							continue
						}
						st, ok := s.Type.(*ast.StructType)
						if !ok {
							api["type "+s.Name.Name] = str(s.Type)
							continue
						}
						var fs []string
						for _, fl := range st.Fields.List {
							if len(fl.Names) == 0 {
								fs = append(fs, "embedded "+str(fl.Type))
							}
							for _, n := range fl.Names {
								if n.IsExported() {
									fs = append(fs, n.Name+" "+str(fl.Type))
								}
							}
						}
						api["type "+s.Name.Name] = "struct{" + strings.Join(fs, "; ") + "}"
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if !n.IsExported() {
								continue
							}
							typ := ""
							if s.Type != nil {
								typ = str(s.Type)
							}
							api[d.Tok.String()+" "+n.Name] = typ
						}
					}
				}
			}
		}
	}
	return api, nil
}

// APIDelta compares a package's exported surface in the base and the active
// tree: what was added, removed or changed.
func APIDelta(base, active map[string]string) (added, removed, changed []string) {
	for k, v := range active {
		b, ok := base[k]
		switch {
		case !ok:
			added = append(added, k)
		case b != v:
			changed = append(changed, k+": "+b+" -> "+v)
		}
	}
	for k := range base {
		if _, ok := active[k]; !ok {
			removed = append(removed, k)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	slices.Sort(changed)
	return
}
