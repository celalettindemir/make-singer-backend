package main

import (
	"flag"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/wire.txt from the current code")

const snapshotFile = "testdata/wire.txt"

// TestWireSnapshot fails when anything on the wire changed. A rename-only
// change must leave it green; an intended wire change is recorded with
// "go test ./tools/wirecheck -update" and reviewed as a diff of wire.txt.
func TestWireSnapshot(t *testing.T) {
	got, err := Snapshot(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	body := strings.Join(got, "\n") + "\n"
	if *update {
		if err := os.MkdirAll(filepath.Dir(snapshotFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(snapshotFile, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", snapshotFile, err)
		}
		t.Logf("wrote %s (%d lines)", snapshotFile, len(got))
		return
	}
	raw, err := os.ReadFile(snapshotFile)
	if err != nil {
		t.Fatalf("read %s: %v (run with -update to create it)", snapshotFile, err)
	}
	want := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	removed, added := diff(want, got)
	if len(removed) == 0 && len(added) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("wire contract changed (--- " + snapshotFile + " +++ current code)\n")
	for _, l := range removed {
		b.WriteString("-" + l + "\n")
	}
	for _, l := range added {
		b.WriteString("+" + l + "\n")
	}
	b.WriteString("If this change is intended, run: go test ./tools/wirecheck -update\n")
	t.Error(b.String())
}

// diff compares two line multisets and returns the sorted lines only in
// want (removed) and only in got (added), counting duplicates.
func diff(want, got []string) (removed, added []string) {
	count := map[string]int{}
	for _, l := range want {
		count[l]++
	}
	for _, l := range got {
		count[l]--
	}
	for l, n := range count {
		for ; n > 0; n-- {
			removed = append(removed, l)
		}
		for ; n < 0; n++ {
			added = append(added, l)
		}
	}
	sort.Strings(removed)
	sort.Strings(added)
	return removed, added
}

func TestDiffCountsDuplicates(t *testing.T) {
	removed, added := diff([]string{"STR\t\"a\"", "STR\t\"a\"", "TAG\tx"}, []string{"STR\t\"a\"", "TAG\tx", "TAG\ty"})
	if len(removed) != 1 || removed[0] != "STR\t\"a\"" || len(added) != 1 || added[0] != "TAG\ty" {
		t.Fatalf("removed=%q added=%q", removed, added)
	}
}

func TestFileLinesRules(t *testing.T) {
	const src = `package p

import "errors"

var ErrSentinel = errors.New("sentinel text")

type T struct {
	A string ` + "`json:\"a\"`" + `
}

func f(r router) error {
	log.Printf("log text %s", "log arg")
	r.Get("/yol", nil)
	c.Get("X-Header")
	go func() { panic("panic text") }()
	_ = fmt.Sprintf("key:%s", "v")
	return fmt.Errorf("wrap text: %w", errors.New("inner text"))
}
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := fileLines(f)
	sort.Strings(got)
	want := []string{
		"ROUTE\tGET /yol",
		"STR\t\"/yol\"",
		"STR\t\"X-Header\"",
		"STR\t\"key:%s\"",
		"STR\t\"sentinel text\"",
		"STR\t\"v\"",
		"TAG\tjson:\"a\"",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
