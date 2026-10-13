// Command wirecheck prints a deterministic snapshot of core-service's wire
// contract: struct tags, string literals, route registrations and the
// hashes of embedded/config assets. It contains no file paths and no Go
// identifiers, so a pure rename leaves the snapshot unchanged while any
// change to a value that reaches clients, the DB, Redis or the deploy
// shows up as a diff (see wirecheck_test.go and testdata/wire.txt).
//
// Usage (from core-service/):
//
//	go run ./tools/wirecheck            # print the snapshot
//	go test ./tools/wirecheck           # compare with testdata/wire.txt
//	go test ./tools/wirecheck -update   # rewrite testdata/wire.txt
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// assets lists the non-Go files whose bytes are part of the wire contract
// (embedded migrations and templates, deploy configuration). Keys are
// logical names that never change; paths are relative to core-service/
// and ARE edited by the package-rename tasks. Only file contents are
// hashed, so moving a file keeps its line identical.
var assets = map[string]string{
	"kimlik-migration-001":    "internal/identity/migrations/001_kullanici.sql",
	"kimlik-migration-002":    "internal/identity/migrations/002_refresh_indeks.sql",
	"kimlik-migration-003":    "internal/identity/migrations/003_federe_kimlik.sql",
	"kimlik-template-baglama": "internal/identity/sablonlar/baglama.html",
	"kimlik-template-cakisma": "internal/identity/sablonlar/cakisma.html",
	"kimlik-template-giris":   "internal/identity/sablonlar/giris.html",
	"kimlik-template-kayit":   "internal/identity/sablonlar/kayit.html",
	"sosyal-migration-001":    "internal/social/migrations/001_sosyal.sql",
	"config-yaml":             "config.yaml",
	"env-example":             ".env.example",
}

// assetGlobs must be fully covered by the assets table, so a new
// migration or template cannot slip in unguarded. Edited together with
// assets when a directory is renamed.
var assetGlobs = []string{
	"internal/*/migrations/*.sql",
	"internal/*/sablonlar/*",
}

// skipDirs are not scanned (relative to core-service/).
var skipDirs = map[string]bool{
	"docs":  true,
	"tools": true,
}

// routeMethods are Fiber/net/http registration calls whose first argument,
// when it is a string literal starting with "/", is a route path.
var routeMethods = map[string]bool{
	"Get": true, "Post": true, "Put": true, "Patch": true, "Delete": true,
	"Head": true, "Options": true, "All": true, "Group": true, "Use": true,
	"Route": true, "Static": true, "Handle": true, "HandleFunc": true,
}

// logFuncs are log package calls whose arguments are not wire values.
var logFuncs = map[string]bool{
	"Print": true, "Printf": true, "Println": true,
	"Fatal": true, "Fatalf": true, "Fatalln": true,
	"Panic": true, "Panicf": true, "Panicln": true,
}

func main() {
	root := flag.String("root", ".", "core-service directory")
	flag.Parse()
	lines, err := Snapshot(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wirecheck:", err)
		os.Exit(1)
	}
	for _, l := range lines {
		fmt.Println(l)
	}
}

// Snapshot returns the sorted snapshot lines for the core-service tree at
// root. Duplicates are kept: deleting one of two equal literals is a diff.
func Snapshot(root string) ([]string, error) {
	var lines []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if rel != "." && (skipDirs[rel] || name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		lines = append(lines, fileLines(f)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	assetLines, err := hashAssets(root)
	if err != nil {
		return nil, err
	}
	lines = append(lines, assetLines...)
	sort.Strings(lines)
	return lines, nil
}

// collector gathers TAG, STR and ROUTE lines from one file.
type collector struct {
	lines []string
	tags  map[*ast.BasicLit]bool
}

// fileLines collects the lines of one file. Error texts built inside
// function bodies (errors.New, fmt.Errorf) and log/panic arguments are
// skipped; package-level sentinel errors are kept because their text
// reaches clients (err.Error() as an API message or an HTML page).
func fileLines(f *ast.File) []string {
	c := &collector{tags: map[*ast.BasicLit]bool{}}
	for _, decl := range f.Decls {
		_, isFunc := decl.(*ast.FuncDecl)
		c.walk(decl, isFunc)
	}
	return c.lines
}

func (c *collector) walk(root ast.Node, inFunc bool) {
	ast.Inspect(root, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.FuncLit:
			c.walk(n.Body, true)
			return false
		case *ast.Field:
			if n.Tag != nil {
				c.tags[n.Tag] = true
				c.lines = append(c.lines, "TAG\t"+unquote(n.Tag.Value))
			}
		case *ast.BasicLit:
			if n.Kind == token.STRING && !c.tags[n] {
				c.lines = append(c.lines, "STR\t"+strconv.Quote(unquote(n.Value)))
			}
		case *ast.CallExpr:
			return c.call(n, inFunc)
		}
		return true
	})
}

// call records a route registration and reports whether the call's
// arguments are scanned (false for log, panic and in-function error text).
func (c *collector) call(n *ast.CallExpr, inFunc bool) bool {
	pkg, name := callName(n)
	switch {
	case pkg == "log" && logFuncs[name]:
		return false
	case pkg == "" && name == "panic":
		return false
	case inFunc && ((pkg == "fmt" && name == "Errorf") || (pkg == "errors" && name == "New")):
		return false
	}
	if sel, ok := n.Fun.(*ast.SelectorExpr); ok && routeMethods[sel.Sel.Name] && len(n.Args) > 0 {
		if lit, ok := n.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if p := unquote(lit.Value); strings.HasPrefix(p, "/") {
				c.lines = append(c.lines, "ROUTE\t"+strings.ToUpper(sel.Sel.Name)+" "+p)
			}
		}
	}
	return true
}

// callName returns ("pkg", "Func") for pkg.Func(...) and ("", "f") for f(...).
func callName(c *ast.CallExpr) (string, string) {
	switch fn := c.Fun.(type) {
	case *ast.Ident:
		return "", fn.Name
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			return x.Name, fn.Sel.Name
		}
		return "?", fn.Sel.Name
	}
	return "?", ""
}

func unquote(s string) string {
	u, err := strconv.Unquote(s)
	if err != nil {
		return s
	}
	return u
}

// hashAssets emits one FILE line per assets entry and fails when a file
// matched by assetGlobs is missing from the table.
func hashAssets(root string) ([]string, error) {
	listed := map[string]bool{}
	var lines []string
	for name, rel := range assets {
		listed[rel] = true
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("asset %s: %w", name, err)
		}
		sum := sha256.Sum256(b)
		lines = append(lines, "FILE\t"+name+"\t"+hex.EncodeToString(sum[:]))
	}
	for _, g := range assetGlobs {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(g)))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return nil, err
			}
			if rel = filepath.ToSlash(rel); !listed[rel] {
				return nil, fmt.Errorf("%s matches %s but is not in the assets table", rel, g)
			}
		}
	}
	return lines, nil
}
