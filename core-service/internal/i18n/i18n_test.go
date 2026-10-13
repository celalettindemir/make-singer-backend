package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestFromAcceptLanguage(t *testing.T) {
	tests := []struct {
		header string
		want   Lang
	}{
		{"", TR},
		{"en", EN},
		{"tr", TR},
		{"EN-us", EN},
		{"en_GB", EN},
		{"tr-TR,tr;q=0.9,en;q=0.8", TR},
		{"en-US,en;q=0.9,tr;q=0.8", EN},
		{"de-DE,de;q=0.9,en;q=0.5", EN},
		{"fr", TR},
		{"*", TR},
		{"tr;q=0.4, en;q=0.6", EN},
		{"en;q=0.5, tr;q=0.5", EN},
		{"en;q=0, tr;q=0.1", TR},
		{"en;q=0", TR},
		{"en;q=abc, tr;q=0.2", TR},
		{"en;q=1.5", TR},
		{" en ; q = 0.7 ", EN},
		{"de, en-GB;q=0.8", EN},
		{",,;", TR},
	}
	for _, tt := range tests {
		if got := FromAcceptLanguage(tt.header); got != tt.want {
			t.Errorf("FromAcceptLanguage(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestMessage(t *testing.T) {
	if got := Message(TR, KeyJobNotFound); got != "İş bulunamadı." {
		t.Errorf("TR = %q", got)
	}
	if got := Message(EN, KeyJobNotFound); got != "Job not found." {
		t.Errorf("EN = %q", got)
	}
	if got := Message(EN, KeyFieldFileURLInvalid, "masterUrl"); got != "masterUrl: invalid file URL." {
		t.Errorf("args = %q", got)
	}
	if got := Message("de", KeyJobNotFound); got != "İş bulunamadı." {
		t.Errorf("unknown lang = %q, want Turkish", got)
	}
	if got := Message(EN, Key{"no_such_key"}); got != "no_such_key" {
		t.Errorf("unknown key = %q", got)
	}
}

// TestCatalogComplete parses catalog.go: every declared Key variable must
// have non-empty TR and EN texts with the same formatting verbs, and the
// catalog must not hold undeclared keys.
func TestCatalogComplete(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "catalog.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[Key]string{}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.VAR {
			continue
		}
		for _, s := range g.Specs {
			vs := s.(*ast.ValueSpec)
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.CompositeLit)
				if !ok {
					continue
				}
				if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "Key" {
					continue
				}
				v, err := strconv.Unquote(lit.Elts[0].(*ast.BasicLit).Value)
				if err != nil {
					t.Fatal(err)
				}
				if prev, dup := declared[Key{v}]; dup {
					t.Errorf("%s and %s share the id %q", prev, name.Name, v)
				}
				declared[Key{v}] = name.Name
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("no Key constants found")
	}
	for k, name := range declared {
		tx, ok := catalog[k]
		switch {
		case !ok:
			t.Errorf("%s has no catalog entry", name)
		case strings.TrimSpace(tx.tr) == "" || strings.TrimSpace(tx.en) == "":
			t.Errorf("%s is missing a language: tr=%q en=%q", name, tx.tr, tx.en)
		case strings.Count(tx.tr, "%") != strings.Count(tx.en, "%"):
			t.Errorf("%s: TR and EN verbs differ: %q / %q", name, tx.tr, tx.en)
		}
	}
	for k := range catalog {
		if _, ok := declared[k]; !ok {
			t.Errorf("catalog entry %q has no Key variable", k.id)
		}
	}
}
