# Backend English Naming and Localized Error Messages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename every Go identifier, file name, comment and test name in `core-service` to English (Go conventions), without changing one byte of the wire contract. Then make API error `message`s TR/EN by `Accept-Language`, replace string-compared errors with sentinels, and translate error, log and test-message strings to English.

**Architecture:**
- A committed guard (`tools/wirecheck`, Task 1) snapshots everything that can reach the wire: struct tags, string literals outside logs and in-function error texts, route registrations, and SHA-256 hashes of migrations, templates and deploy config. Every rename task leaves that snapshot byte-identical.
- Renames run leaves first: `model` → `config` → `deletionmark`/`auth` → `client` → `middleware` → `identity` → `social` → `service` → `worker` → `handler` → `cmd/server`/`e2e`. Each task owns one package or one file group of a package.
- Every rename task carries its own declaration table. A helper (`apply_renames.py`, see Tooling) reads that table from this file, resolves each old name to a byte offset (`declpos`) and runs `gopls rename -w`, which updates every caller in other packages in the same task.
- Five tasks change the snapshot on purpose, each with an exact expected diff and a reviewer gate: Redis JSON tags (Task 7), template actions (Task 10), localized messages (Task 22), job-state sentinels (Task 23) and string translation (Tasks 24-26).

**Tech Stack:** Go 1.25, Fiber v2, pgx v5, go-redis v9, asynq, zitadel/oidc v3, `html/template`, `embed`. Tools: gopls v0.20.0 (`~/go/bin/gopls`), golangci-lint 2.14.0 (`~/go/bin/golangci-lint`, CI version), swag v1.16.4 (`~/go/bin/swag`), Python 3 (helper script only). No new dependencies.

**Spec:** the section "Design (approved 2026-10-12)" at the end of this plan. The plan merges four scope glossaries (infrastructure, app layer, social, identity) and two verified bracketing tasks (wire guard, localized errors). Where those sources disagreed, the "Reconciliation rulings" section decides, and its decisions are already applied to every table below.

---

## Global Constraints

**Order and state**
- Tasks run strictly in numeric order on one branch. Each task starts from a clean tree (`git status --short` prints nothing) and ends with one commit. After each commit the build, the full test suite and the guard are green.
- Run Go commands in `core-service/`. Paths in tables are relative to `core-service/`. Two verbatim commit commands use repository-root paths and say so: Task 1 Step 8 and Task 22 Step 10. Run those from the repository root.
- **No `v*` tag** may be cut from the first rename task (Task 2) to the end of the plan (Task 28). A deploy in the middle would ship the A7 blind spot described in the rulings. Do not push tags. Pushing branches is up to the person running the plan.

**Exact wire rule**
- In every rename task (2-6, 8, 9, 11-21), `go test -count=1 ./tools/wirecheck` must pass **unchanged**. `testdata/wire.txt` must not be regenerated in those tasks.
- The only edit a rename task may make to `tools/wirecheck` is in the package-move tasks (8, 13): the paths in its `assets` table. Logical asset names are never edited.
- Five tasks change the snapshot on purpose: 7 (Redis JSON tags), 10 (template actions), 22 (localized messages), 23 (sentinel errors) and 24-26 (string translation). Each lists its exact expected diff. Its reviewer must accept that diff line by line before `-update`.

**Renames vs translation (ruling A7)**
- Rename tasks change identifiers, file names, comments and test function names only.
- String literals stay byte-identical. That includes `errors.New` and `fmt.Errorf` texts, log messages, panic texts, test failure and skip messages, `t.Run` subtest labels, test data, SQL, and every wire string.
- The only exception is a string that a glossary explicitly calls a pure Go-name reference, such as a template file name. This plan uses that exception nowhere: ruling R4 keeps the template file names, so the snapshot stays byte-identical.
- Translating error, log and test-message strings into English is Tasks 24-26. Those tasks run after Task 22 (localized messages) has stopped service error texts from reaching API responses. They also run after Task 23 has replaced the `err.Error() == "job not completed"` and `"job already completed"` comparisons with sentinels and `errors.Is`.

**Redis structs without tags**
- Before any of their fields is renamed, Task 7 adds `json:"<OldGoName>"` tags to the Turkish-named fields of the identity structs stored in Redis: `AuthIstek.IpucuSubject`, `OturumBaglama.Kayitlar`, `FedereDurum.{Saglayici,Cerez,KodDogrulayici}` and `BaglamaBekleme.{Saglayici,Eposta}`. This is identity glossary risk R1.
- Task 7 also adds `TestRedisJSON_KeysUnchanged`, which pins the JSON keys of all six Redis-stored structs.
- No other package has an untagged serialized struct (checked by the infra, app and social glossaries).

**Templates**
- The identity templates keep their file names and directory (`internal/identity/sablonlar/{giris,kayit,baglama,cakisma}.html`) and their content.
- The only edit is in Task 10: the `{{.X}}` field references follow the renamed template data fields. Text, attributes and markup stay byte-identical.
- A golden render of all four pages before and after must be identical.

**Validation error details (app glossary risk R4)**
- `handler.formatValidationErrors` uses `e.Field()`, so the keys in `error.details` for `VALIDATION_ERROR` are the Go field names of the `model` request structs. Decision: **every field with a `validate` tag keeps its exact Go name.** No model field is renamed by this plan. Task 2 renames two methods (`Job.SahibiMi`, `RenderStartRequest.StemAyarlari`), one sentinel and test names only.
- The social package has no `validate` tags. It validates by hand and returns codes, so it is unaffected.
- A later change that wants English-only `details` keys must register a tag-name function that reads the `json` tag. That is a wire change and is out of scope.

**Tooling**
- Identifier renames use `~/go/bin/gopls rename -w <file>:#<byteoffset> <NewName>`, driven by the task's table through `apply_renames.py`. Files and directories move with `git mv`.
- **gopls updates every caller, in every package, in the same task.** That includes callers in packages owned by later tasks. Nothing is renamed by hand that gopls can rename.
- Three gopls behaviours were verified on a copy of the tree at `de6c92b`. The tasks are built around them:
  1. Renaming an **interface method** also renames the methods of implementers in the same package and in packages that import the interface's package, for example test fakes. It does **not** rename an implementer in a package that never imports the interface's package. There are three such cases:
     - `service.KimlikSilici` ↔ `kimlik.Sunucu`;
     - `service.SosyalTemizleyici` ↔ `sosyal.PostgresDepo`;
     - `handler.SosyalServisi` ↔ `sosyal.Servis`.
     In Tasks 9, 13 and 14, `apply_renames.py` renames each interface method with gopls. Right after each one, it renames the concrete method textually (rows of kind `manual: textual`). The textual rename is limited to the implementer's package, and the script checks it with a one-declaration precondition and `go build ./...`.
  2. Renaming a **concrete method** first is refused ("would make ... no longer assignable to interface ..."). While any package has type errors, every rename that touches it is refused ("not possible because main.go ... has errors"). So the textual rename must follow each interface method at once, before the next gopls call. The script does exactly that, one method at a time.
  3. Renaming a **package clause** (`gopls rename -w <file>:#<offset of the name> <newpkg>`) rewrites the package clause of every file and every import path, then prints `getFile: ... is a directory`. That error is expected. `git mv` of the directory finishes the move.
- gopls does not realign columns (struct fields, composite literals, `const` blocks). Every apply step therefore runs `gofmt -w` on the files `gofmt -l .` lists. At `de6c92b` that list is empty, so only touched files are reformatted.
- Local variables and parameters are **not** listed in the tables. The implementer translates them in the files a task owns, following the naming rules in the Design section. Use mixedCaps and initialisms (`userID`, `pubID`); prefer short names in short scopes; never shadow an imported package name (`identity`, `social`, `service`, `client`) or a builtin. gopls refuses a rename that would collide, which is the check.
- **Comments** are translated in the task that owns the file (see each task's file list). They use the final English names from this plan, even for identifiers that a later task renames.
- Swag annotation lines (`// @...`) keep `@Router` paths, `@Param` names, `@Tags`, `@Security` and type references. Their `@Summary` and `@Description` texts are already English.

**Tooling setup (once per machine; nothing goes into the repository)**

```bash
export T="${TMPDIR:-/tmp}/en-rename"
mkdir -p "$T/src/declpos" "$T/src/identdump"
```

Create `$T/src/declpos/main.go`:

```go
// Command declpos prints the gopls position (<file>:#<byteoffset>) of the
// declaring identifier of a Go declaration in one package directory, test
// files included. Usage:
//
//	declpos <pkgdir> <Name>            top-level func, type, var or const
//	declpos <pkgdir> <Type>.<Member>   method, struct field or interface method
//
// It exits 1 when nothing matches and prints every match when several do.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: declpos <pkgdir> <Name|Type.Member>")
		os.Exit(2)
	}
	dir, want := os.Args[1], os.Args[2]
	typ, member, isMember := strings.Cut(want, ".")
	if !isMember {
		member = typ
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	sort.Strings(files)
	fset := token.NewFileSet()
	var hits []string
	hit := func(id *ast.Ident) {
		p := fset.Position(id.Pos())
		hits = append(hits, fmt.Sprintf("%s:#%d", p.Filename, p.Offset))
	}
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		for _, d := range af.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					if !isMember && d.Name.Name == member {
						hit(d.Name)
					}
					continue
				}
				if isMember && d.Name.Name == member && recvName(d.Recv.List[0].Type) == typ {
					hit(d.Name)
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if !isMember {
							if s.Name.Name == member {
								hit(s.Name)
							}
							continue
						}
						if s.Name.Name != typ {
							continue
						}
						var fl *ast.FieldList
						switch t := s.Type.(type) {
						case *ast.StructType:
							fl = t.Fields
						case *ast.InterfaceType:
							fl = t.Methods
						}
						if fl == nil {
							continue
						}
						for _, fld := range fl.List {
							for _, n := range fld.Names {
								if n.Name == member {
									hit(n)
								}
							}
						}
					case *ast.ValueSpec:
						if isMember {
							continue
						}
						for _, n := range s.Names {
							if n.Name == member {
								hit(n)
							}
						}
					}
				}
			}
		}
	}
	if len(hits) == 0 {
		fmt.Fprintf(os.Stderr, "declpos: %s not found in %s\n", want, dir)
		os.Exit(1)
	}
	for _, h := range hits {
		fmt.Println(h)
	}
}

// recvName returns the receiver's type name without pointer or type parameters.
func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}
```

Create `$T/src/declpos/go.mod`:

```
module declpos

go 1.25
```

Create `$T/apply_renames.py`:

```python
#!/usr/bin/env python3
"""Apply the declaration renames of one task of the English-naming plan.

Usage, from core-service/:
    python3 "$T/apply_renames.py" <plan.md> <marker> [--dry-run]

<marker> is the id in the plan's "<!-- renames:<marker> -->" comment
(for example 9 or 9-first). Every table row whose kind does not start with
"manual" is resolved to a byte offset with declpos and renamed with
"gopls rename -w". Order: interface methods, then other members
(Type.Member), then plain names, so a type is renamed after its members
and an implementer's method follows its interface.

Rows of kind "manual: textual ..." name a concrete method in a package that
does not import the interface (gopls cannot reach it). Right after the
interface method with the same old name is renamed, the script renames that
concrete method textually in the row's directory: declarations
") Old(" and calls ".Old(". It first checks that exactly one declaration
matches, then runs "go build ./...". This keeps the build green between
gopls calls; gopls refuses every rename while a package has type errors.

The script is re-runnable: a row whose old name is gone and whose new name
exists prints "already" (an interface rename may already have renamed a
fake's method). When gopls refuses a method because the same package has
an interface with that method ("... if you intend to change both types"),
the row is retried once after the other rows.
"""
import glob
import os
import re
import subprocess
import sys

plan, marker = sys.argv[1], sys.argv[2]
dry = '--dry-run' in sys.argv[3:]
T = os.environ.get('T') or os.path.join(os.environ.get('TMPDIR', '/tmp'), 'en-rename')
DECLPOS = os.path.join(T, 'declpos')
GOPLS = os.path.expanduser('~/go/bin/gopls')

lines = open(plan, encoding='utf-8').read().split('\n')
tag = '<!-- renames:%s -->' % marker
if tag not in lines:
    sys.exit('marker %s not found in %s' % (tag, plan))
i = lines.index(tag) + 1
while i < len(lines) and not lines[i].startswith('|'):
    i += 1
rows = []
textual = {}
while i < len(lines) and lines[i].startswith('|'):
    cells = [c.strip().strip('`').strip() for c in lines[i].strip().strip('|').split('|')]
    i += 1
    if cells[0] == 'old' or set(cells[0]) <= set('-: '):
        continue
    old, new, kind, path = cells[0], cells[1], cells[2], cells[3]
    if kind.startswith('manual: textual'):
        textual[old.split('.')[-1]] = (new.split('.')[-1], os.path.dirname(path))
        continue
    if kind.startswith('manual'):
        continue
    rows.append((old, new, kind, path))
rows.sort(key=lambda r: 0 if r[2].startswith('interface method') else (1 if '.' in r[0] else 2))


def pos(d, name):
    r = subprocess.run([DECLPOS, d, name], capture_output=True, text=True)
    return r.stdout.split() if r.returncode == 0 else []


def rename_textually(old, new, d):
    files = sorted(glob.glob(os.path.join(d, '*.go')))
    decl = re.compile(r'\) %s\(' % re.escape(old))
    hits = [f for f in files for line in open(f, encoding='utf-8') if decl.search(line)]
    if len(hits) != 1:
        sys.exit('textual %s -> %s: expected exactly 1 declaration ") %s(" in %s, found %d' % (old, new, old, d, len(hits)))
    print('textual  %s -> %s  (%s)' % (old, new, d), flush=True)
    if dry:
        return
    call = re.compile(r'\.%s\(' % re.escape(old))
    for f in files:
        src = open(f, encoding='utf-8').read()
        out = call.sub('.%s(' % new, decl.sub(') %s(' % new, src))
        if out != src:
            open(f, 'w', encoding='utf-8').write(out)
    if subprocess.run(['go', 'build', './...']).returncode != 0:
        sys.exit('build failed after the textual rename of %s; fix it, then re-run' % old)


done = 0
deferred = set()
queue = list(rows)
while queue:
    old, new, kind, path = queue.pop(0)
    d = os.path.dirname(path)
    member = new.split('.')[-1]
    oldmember = old.split('.')[-1]
    if oldmember == member:
        continue
    p = pos(d, old)
    if not p:
        same_type = old.rsplit('.', 1)[0] + '.' + member if '.' in old else member
        if pos(d, same_type) or pos(d, new):
            print('already  %s -> %s' % (old, new))
            continue
        sys.exit('NOT FOUND %s in %s (neither the old nor the new name exists)' % (old, d))
    if len(p) != 1:
        sys.exit('AMBIGUOUS %s: %s' % (old, ' '.join(p)))
    print('rename   %s -> %s  (%s)' % (old, new, p[0]), flush=True)
    if dry:
        continue
    r = subprocess.run([GOPLS, 'rename', '-w', p[0], member], capture_output=True, text=True)
    if r.returncode != 0:
        print((r.stdout + r.stderr).strip())
        if 'if you intend to change both types' in (r.stdout + r.stderr) and old not in deferred:
            deferred.add(old)
            queue.append((old, new, kind, path))
            print('deferred %s (its interface row runs first)' % old, flush=True)
            continue
        sys.exit('gopls refused %s -> %s; fix the cause and re-run (finished rows print "already")' % (old, new))
    done += 1
    if kind.startswith('interface method') and oldmember in textual:
        tnew, tdir = textual.pop(oldmember)
        rename_textually(oldmember, tnew, tdir)
for oldmember, (tnew, tdir) in textual.items():
    pending = any(re.search(r'\) %s\(' % re.escape(oldmember), l) for f in glob.glob(os.path.join(tdir, '*.go')) for l in open(f, encoding='utf-8'))
    if pending and dry:
        print('textual  %s -> %s  (%s; runs after its interface row)' % (oldmember, tnew, tdir))
        continue
    if pending:
        sys.exit('textual row %s was not applied: its interface row is not in this table or did not run' % oldmember)
    print('already  %s -> %s (textual)' % (oldmember, tnew))
print('%d renamed, %d rows in table' % (done, len(rows)))
```

Build and check:

```bash
(cd "$T/src/declpos" && go build -o "$T/declpos" .)
cd core-service && "$T/declpos" internal/model Job.SahibiMi; cd ..
```

Expected: one line of the form `internal/model/job.go:#<offset>`. `apply_renames.py` calls `$T/declpos`. Every new shell needs `export T="${TMPDIR:-/tmp}/en-rename"` again. (`$T/src/identdump` is used by Task 28.)

**Verification after every task (VERIFY)**

```bash
PGPASSWORD=kimlik-test-yerel psql -h localhost -p 55432 -U postgres -c 'CREATE DATABASE en_plan'   # once per machine; "already exists" is fine
export KIMLIK_TEST_DB_URL='postgres://postgres:kimlik-test-yerel@localhost:55432/en_plan?sslmode=disable'
test -z "$(gofmt -l .)" && echo GOFMT-OK
go vet ./...
go build ./...
go test -count=1 ./...
~/go/bin/golangci-lint run ./...
go test -count=1 ./tools/wirecheck
```

Expected:
- `GOFMT-OK`. `vet` and `build` print nothing.
- Every package is `ok` (or `[no test files]`). Redis (`localhost:6379`) and Postgres (`localhost:55432`) must be up so the live tests do not skip. The only expected skips are the real-provider tests (`*_RealGroq`, `*_RealSuno`).
- Lint prints `0 issues.`
- The guard prints `ok`. In tasks 7, 10 and 22-26 it prints `ok` only after the step that runs `-update`.

**Rename-completion check (rename tasks)**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md <marker> --dry-run | grep -v '^already'
```

Expected: only the summary line `0 renamed, <n> rows in table`. A `rename` line means a row was missed. Run it for every marker of the task.

**Commits**
- Messages are Turkish (ASCII, as in the existing history) and end exactly with:
  ```
Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
  ```
- Stage explicit paths only:
  - `git mv` stages moves.
  - Then `git add -- $(git diff --name-only)` stages the modified tracked files. Review `git status --short` first: it must list only paths this task is expected to touch, and no `??` line except files the task creates. Add those by name.
  - `git add -A <dir>` is allowed only for exactly the moved package directories (Tasks 4, 8 and 13).
- Never use `git stash`, `git commit --amend`, `--no-verify` or `git add -A` without a path.

**Test database and services**
- Every test run uses the per-plan database `en_plan` through `KIMLIK_TEST_DB_URL` (see VERIFY). The default DSN and the CI DSN are unchanged.
- Redis DB numbers used by tests do not change: handler 12, service 13, middleware 14, identity 15.

---

## Reconciliation rulings

The four scope glossaries (infrastructure, app layer, social, identity) were written in parallel against `de6c92b`. The assembly merged them, checked them against the code, and decided the points below. Every table in Tasks 2-21 already reflects these decisions.

**Checks done at assembly (on `de6c92b`)**
- All 2290 simple rename rows exist in the code as declared (a `go/ast` dump of every declaration, test files included). The exception is the `main.go` locals, which are kept as `manual:` rows with line numbers.
- No old name maps to two new names.
- After the merge, no package has two declarations with the same final name in the same scope (0 collisions). That includes the names that Task 22 adds (`ValidationError`, `invalid`, `internalError`, `aiError`).
- All 222 `.go` files are owned by exactly one task (see Self-review).
- The verified mechanics:
  - gopls interface and package renames on a scratch copy;
  - the Task 7 tags and their exact snapshot diff (+7 `TAG`);
  - the Task 10 template rewrite (hashes) and golden render;
  - the Task 23 sentinel change and its snapshot diff (−1 `STR`);
  - a `swag init` on unchanged code produces no diff.

**Rulings**

- **R1. One glossary.** The declaration tables in Tasks 2-21 together are the merged glossary. They have 2346 rows. Each declaration is renamed in exactly one task, normally the task that owns its file. There are exceptions, and each is a rename that must happen together with an interface in another package:
  - interface methods and their fakes in other packages (`Denetci.Silinmis` and the worker fake in Task 4; `Kopyalayici.Kopyala` and `AudioAnalizci.Analiz` with the social and worker fakes in Task 5);
  - store-interface methods with the social and worker fakes (Task 13);
  - `SosyalServisi` with `sahteSosyal` (Task 14);
  - `KimlikSilici.HesapSil` with its three fakes (Task 9).

  The term table below is for locals and comments.
- **R2. Order.** The order is: guard (1); leaf packages `model`, `config`, `deletionmark`+`auth`, `client`, `middleware` (2-6); then `identity` (7-12) and `social` (13-15), because `service`, `worker`, `handler` and `main` depend on them; then `service`, `worker`, `handler` and `cmd/server`+`e2e` (16-21); then localized messages (22), sentinels (23), string translation (24-26), docs (27) and the final sweep (28). `identity` and `social` are split into a move task and several declaration tasks, so each commit stays reviewable.
- **R3. Implementers in packages that do not import the interface.** These pairs cannot be renamed by gopls alone (Global Constraints, Tooling):
  - `service.KimlikSilici.HesapSil` ↔ `kimlik.Sunucu.HesapSil` (Task 9);
  - `service.SosyalTemizleyici.{YayinlariListele, KullaniciIzleriniSil, KullaniciVerisiniSil}` ↔ `sosyal.PostgresDepo` (Task 13);
  - the 23 `handler.SosyalServisi` methods ↔ `sosyal.Servis` (Task 14).

  For each method, the interface goes first with gopls. The concrete method follows at once with a textual rename in the implementer's package, which is guarded by a one-declaration precondition and by the build (`apply_renames.py`, `manual: textual` rows). All three cases were run end to end on a scratch copy (see Self-review).
- **R4. Template file names and directory stay** (`internal/identity/sablonlar/{giris,kayit,baglama,cakisma}.html`). Renaming them would change `ParseFS`/`ExecuteTemplate` string literals and the guard's asset table and globs in a rename task. They are not Go identifiers. So the A7 exception for "pure Go-name references" is not used anywhere, and every rename task keeps the snapshot byte-identical.
- **R5. Redis JSON tags come first, in their own task (7).** The tags use the **old** field names, which are today's JSON keys. A permanent test pins all six Redis-stored structs, and the snapshot gains exactly 7 `TAG` lines. The template data rename (Task 10) is also its own task, with an exact expected diff (4 `FILE` lines) and a golden render.
- **R6. `depo` → `store`** for repositories (`Store`, `PostgresStore`, `UserStore`, `TokenStore`, `fakeStore`). The one exception is `Storage`: it is used where a type adapts zitadel's `op.Storage` (identity `Depo` → `Storage`, `NewStorage`) and for fakes of `client.StorageClient` (`fakeStorage`, `panickingStorage`, `signingStorage`).
- **R7. `durum` → `status`** (HTTP, job, publication `status`, `ErrInvalidStatus`), except `FedereDurum` → `FederationState`, which is the OAuth `state` record.
- **R8. `isaret` → `mark`**: package `deletionmark`, `deletionmark.Key`. The app glossary's "marker" is overridden, so `TestHesapSil_Isaret24SaatYasar` → `TestAccountService_DeleteMarkLives24Hours`. Identity's `otoFormIsaret` → `autoFormMarker` is a different concept (an HTML marker) and keeps its name.
- **R9. `yabanci`**: "foreign" in test names (`TestFileHandler_Foreign404`, `TestMasterFinal_ForeignStem404`). The error is `client.ErrKeyNotOwned`, and `TestFileSign_YabanciAnahtarReddedilir` → `TestFileService_SignRejectsNotOwnedKey`, so it matches the error name.
- **R10. A field that holds a `client.PrefixDeleter` is `prefixDeleter` everywhere**: `AccountService.prefixDeleter`, `SocialWorker.prefixDeleter`, and the `main.go` local. This overrides the social glossary's `deleter`. The service test fake's field `fakeSocialCleaner.deleter` holds a different thing and keeps `deleter`.
- **R11. Names shared across glossaries (fixed here):**
  - `deletionmark.Checker.IsDeleted`, `RedisChecker`, `Key`, `TTL`;
  - `client.Copier.Copy`, `client.PrefixDeleter`, `client.AudioAnalyzer.Analyze`;
  - `service.AccountDeleter.DeleteAccount` = `identity.Server.DeleteAccount`;
  - `service.SocialCleaner.{ListPublicationIDs, DeleteUserTraces, DeleteUserData}` = `social.PostgresStore` methods;
  - `handler.SocialService` methods = `social.Service` methods;
  - `config.FederationClient{Name, ClientID, ClientSecret, AppleTeamID, AppleKeyID, AppleKeyP8}`;
  - `auth.UpstreamVerifier`, `NewUpstreamVerifier`, `VerifyWithNonce`;
  - `middleware.RateLimiter.LimitWithCode`, `DeletedUserGate`;
  - handler helpers `requestOwner`, `asUser` (also used by the social handler files);
  - worker helpers `ownerCanWrite`, `errOwnerDeleted`, `fakeChecker`, `alwaysDeleted` (also used by the social worker);
  - worker social fakes `fakeSocialCopier`, `fakeAnalyzer`, `fakeSocialDeleter`. These do not collide with `fakeStorage`, `fakeChecker`, `fakeAudio` and `uploadCall`.
- **R12. Optional renames are applied:** `ServerConfig.ApiDomain` → `APIDomain` (initialism; the `mapstructure` tag stays), and the `startWorkerServer` parameter `r2Client` → `storage`.
- **R13. Validation `details` keys** are the Go field names of validated `model` structs (app risk R4). Decision: none of those fields is renamed. They are all English already, and Task 2 renames no field (only two methods, one sentinel and tests). The social package validates by hand and is unaffected.
- **R14. Fiber `BodyParser` and untagged social input structs:** form-encoded bodies would fall back to Go field names. The API contract is JSON-only, so the rename is accepted (social risk 3).
- **R15. Client-visible texts that are not API error messages:**
  - The worker job-error texts (job `error`, WebSocket progress) are translated to English in Task 26. This is accepted: clients branch on status, not text.
  - The identity hosted pages, `ErrSifreKisa`'s text, the OAuth `error_description` `"jeton bu istemci icin verilmemis"` and the `i18n` catalog stay. Localizing the hosted pages is a separate, later piece of work.
- **R16. Comments** are translated in the task that owns the file, using final names, including names that a later task renames. Swag `// @...` lines keep routes, params, tags and type references.
- **R17. Test strings:** `t.Run` labels and failure messages stay byte-identical in rename tasks (A7). Tasks 24-26 translate them. Test-data literals (names, IDs, texts used as input) are never translated.
- **R18. Job-state sentinels (Task 23).** Task 22 does not remove the `err.Error() == "job not completed"` / `"job already completed"` comparisons. So Task 23 replaces them with `service.ErrJobNotCompleted` / `ErrJobAlreadyCompleted` and `errors.Is`, before any translation task.
- **R19. Edits to the verified Task 22:**
  - Its name-mapping section is rewritten with final names. Its hint `LimitKod≈LimitCode` becomes `LimitWithCode`, and the new `socialErrors` field `mesaj` becomes `message`.
  - Its path table was wrong for `kurallar.go`: the final file is `internal/social/profile_rules.go`, not `rules.go`.
  - Its diff headers use final paths.
  - Its snapshot counts include Task 7's 7 lines.
  - Its `git add -A core-service` is replaced with explicit paths.
  - Everything else is verbatim.
- **R20. Term conflicts between glossaries** are resolved by context, as shown in the term table: `iptal` (revoke tokens / cancel contexts and jobs), `kapat` (`Close` / `Shutdown`, after the wrapped API), `ozet` (summary in social / hash in identity) and `satir` (row for DB rows / line for text lines).
- **R21. Per-plan test database `en_plan`** through `KIMLIK_TEST_DB_URL`, so a parallel worktree with its own database cannot interfere. CI keeps its fixed DSN.

In rulings A1-A7 and B1-B10 below (verbatim from the verified bracketing tasks), "Task A" is **Task 1**, and "Task B" is **Task 22**.

**Term table (merged from the four glossaries; 296 entries; for locals, comments and any name a table does not list)**

| Turkish | English | glossaries |
|---|---|---|
| acik (anahtar) | public (key) | infra |
| acik anahtar / ozel anahtar | public key / private key | identity |
| aciklama | description | social |
| ad | name | identity |
| ad / adi / adlari | name / names | infra |
| ad uygunlugu / uygun | username availability / available | social |
| adres | URL | app |
| aile | family | identity |
| aktar | transfer | app |
| alan (form field) | field | identity |
| alan / alanlari | field / fields | infra |
| altyapi acik / kapali | backing enabled / disabled | social |
| anahtar | key | app |
| anahtar (crypto key) | key | identity |
| anahtar (Redis key) | key | identity |
| anahtar duzeni | key layout | app |
| analiz / analizci | analysis / analyzer (verb: analyze) | infra |
| analizci | analyzer | social |
| aralik | interval | app |
| auth istek | auth request | identity |
| avatar rengi | avatar color | social |
| ayar | config | identity |
| ayir / ayrik | isolate / isolated | app |
| Bagla (attach handlers to a mux) | Mount | identity |
| baglama (account link) | link | identity |
| baglan (DB) | connect | identity |
| bakim (queue) | maintenance (concept only) | app |
| basarisiz | fail | social |
| baskasi / baskasinin | other user / other user's | app |
| baslat | start | identity |
| baslik (HTTP) | header | infra |
| begen / begeniyi kaldir | like / unlike | social |
| begendim mi | liked by me | social |
| begeni sayisi | like count | social |
| bekle | wait | infra, app |
| bekleme | pending (link) | identity |
| beklenen | want / expected | app |
| bekleyen | pending | app |
| benim mi / ben miyim | is mine / is me | social |
| bildiren | reporter | social |
| bilet | ticket | app |
| bol / bolme | split | app |
| bolum | section | social |
| bos (no-op) | nop | app |
| bpm | BPM | social |
| butunlesik | integration | social |
| cagri | call | app |
| cakisma | conflict | identity |
| canli | live | app |
| canli (test needing live Redis) | live | infra |
| capraz site | cross-site | identity |
| cerez | cookie | identity |
| cover | cover | social |
| cover-baslat | start cover | social |
| coz (resolve) | resolve | infra |
| dalga formu | waveform | social |
| deneme (test fixture) | test | infra |
| denetci / denetim / denetle | checker / check / check | app |
| denetci / denetle | checker / check | infra |
| denetle / denetci | check / checker | social |
| depo | store | identity |
| depo (R2 storage fake) | storage | app |
| depolama | storage | social |
| desen | pattern | infra, app, social |
| detay | detail | social |
| dogrula / dogrulayici | verify / verifier | infra |
| dogrulama | validation | social |
| dogrulayici | verifier | identity |
| dondur (refresh) | rotate | identity |
| dosya | file | social |
| durum (federation state record) | state | identity |
| durum (HTTP / job) | status | app |
| duzen | layout | infra |
| ekip | team | infra |
| ekip ID | team ID | identity |
| en uzun / en cok / en az | max…Len / max… / min… | social |
| engel | gate | infra |
| enstruman | instrument | infra, social |
| enstruman / davul | instrument / drums | app |
| eposta | email | identity |
| eski | stale | app |
| esleme | link (mapping) | identity |
| esnek | flexible | infra |
| federasyon | federation | infra |
| federe / federasyon | federated / federation | identity |
| gecersiz | invalid | infra, app |
| gecici (hata) | transient (error) | infra |
| gecikmeli | delayed | app |
| geri | callback | identity |
| geri cagirma | auth callback (URL func) | identity |
| girdi | input | social |
| giris | login | identity |
| gizli aktarma | private relay | identity |
| gorev | task | app, social |
| gorunen ad | display name | social |
| govde | body | app, social, identity |
| hata (error var/field) | err | social |
| hata (status) | failed | social |
| hata (UI message const) | msg | identity |
| hata durumlari | failure statuses | infra |
| hatali on(ek) | failing prefix | app |
| havuz | pool | app, social, identity |
| hazir | ready | app, identity |
| hazirla / hazirlaniyor | prepare / preparing | social |
| hedef (copy destination key) | destination key | social |
| hedef (report target) | target | social |
| hep | always | app |
| hesap | account | app, identity |
| hesap (in test R2 config AccountID: "hesap123") | (test data, optional) | app |
| hesap sil | delete account | identity |
| hic (no-op callback) | noop | app |
| hiz limit(i) / hizlimit | rate limit / ratelimit | identity |
| iddia | claims | identity |
| imza | signature / signing | identity |
| imza / imzala / imzali | signature / sign / signed | app |
| imzali | signed | social |
| indir / indirici | download / downloader | app |
| iptal | revoke (tokens, sessions) / cancel (context, job) | app, identity |
| is (job) | job | app |
| isaret | mark | infra |
| isaret (deletion marker) | mark | app |
| islem (DB tx) | tx | social |
| istatistik | stats | social |
| istek | request | infra, social, identity |
| istek sahibi | request owner | app |
| istemci | client | app, identity |
| istemci sir | client secret | identity |
| iz (user traces) | traces | social |
| iz / izler | trace / traces | app |
| izleyici | viewer | social |
| jeton | token | app, identity |
| kapat | Close / Shutdown (follow the method being wrapped) | app, identity |
| kapsam / kapsamlar | scope / scopes | identity |
| kavanoz | jar | identity |
| kaydet / yaz / oku | store / write / read | app |
| kayit | record | social |
| kayit (job record) | record | app |
| kayit (record) | record / entry | identity |
| kayit (sign-up) | signup | identity |
| kendini takip | self-follow | social |
| kes | truncate | app |
| Kesfet | Discover | social |
| Kesfet (product feature "Discover") | social (in identifiers) | infra |
| kimlik | identity | app, identity |
| kimlik (an ID value) | ID | social |
| kimlik dogrulama | authentication | app |
| kod | code | identity |
| kok (root of the social tree) | root | infra |
| kolonlar | columns | social |
| konak | host | infra |
| konum | location | identity |
| kopya / kopyala / kopyalayici | copy / copy / copier | infra, social |
| kosul | condition | social |
| kullanici | user | identity |
| kullanici adi | username | social |
| kullanici basina | per user | infra |
| kurallar | rules | social |
| kurulum hatasi | setup error | infra |
| kuyruk | queue | app, social |
| limitleri | limits | social |
| liste | list | app |
| metin | text | social |
| migration kilit | migration lock | identity |
| migration kilit anahtari | migration lock key | social |
| mod / odak | mode / focus | app |
| mod / tonModu | mode | social |
| neden | reason | social |
| ogeler | items | social |
| oku / yaz / al / sil | get or read / write / take (GETDEL) / delete | identity |
| olcu | bars | social |
| olusturma | created at | social |
| omur | TTL (lifetime) | infra |
| onay (env SUNO_E2E_ONAY) | approval (concept only) | app |
| onek | prefix | infra, app, social |
| oneri | suggestion | social |
| ortam | env | identity |
| oturum | session | identity |
| oturum baglamasi | session binding | identity |
| ozel (private bucket) | private | app |
| ozet | summary (social) / hash (identity, digests) | social, identity |
| panikleyen | panicking | app |
| parca (track) / parcalar (parts) | track / parts | app |
| pencere | window | identity |
| plan | plan | app |
| puan | score | social |
| referans | reference (time) | social |
| rota | route | social |
| sablon | template | identity |
| saglayici | provider | identity |
| saglayici sozlesmesi | provider contract | infra |
| saglik | health | identity |
| sahibi mi | is owned by | infra |
| sahip / sahipli / sahipsiz | owner / owned / ownerless | app |
| sahip(lik) | owner(ship) | identity |
| sahipli | owned | infra |
| sahiplik | ownership | app, social |
| sahte | fake | infra, app, social, identity |
| sar (wrap) | Wrap | identity |
| sarki / sarkilar | song / songs | social |
| satir | row (DB rows) / line (text lines) | app, social |
| say (count, verb) | hit | infra |
| sayac | counter | identity |
| sayfa | page | social |
| sayfa / sayfalar | page / pages | identity |
| sayi / sayisi | count / calls | app |
| secenek | option | app |
| secici | selective | app |
| sekme | tab | social |
| senin icin | for you | social |
| servis | service | app |
| ses (audio service) | audio | app |
| sicaklik | temperature | app |
| sifre | password | identity |
| sil / silme / silinen / silinmis | delete / deletion / deleted / deleted | app |
| silebilir miyim | can delete | social |
| silici | prefixDeleter (fields holding a `client.PrefixDeleter`) / deleter (other) | infra, app, social |
| silindi | deleted | social |
| silinmis | deleted | infra |
| sinir | limit | identity |
| sinir / limit | limit | social |
| sir (secret) | secret | infra |
| sistem mesaji | system message | app |
| son deneme | last attempt | social |
| sonraki imlec | next cursor | social |
| sonuc | result | app |
| sorgu | query | social |
| sorulan / yanit | asked / answer | app |
| sosyal | social | infra, app, social |
| soz / sozler | lyrics | social |
| sozlesme | contract | identity |
| stem ayari / ayarlari | stem settings | infra |
| sunucu | server | identity |
| sure | duration | app |
| sure / sureSn | duration / duration seconds | social |
| sureli | TTL-recording | social |
| tahmin / tahmini sure | estimate / estimated duration | app |
| takip edilen | followee | social |
| takip ediyor (viewer→owner) | viewer follows | social |
| takip ediyor muyum | followed by me | social |
| takipci | follower | social |
| tamamla / TamamlandiIsaretle | complete / MarkDone | identity |
| tamamlayici | completer | identity |
| taninmayan konak | unknown host | app |
| tara / tarayici | scan / scanner | social |
| tarayici | browser | identity |
| tavan | max / limit | app |
| tazele / taze | refresh / fresh | app |
| tekil ihlali | unique violation | social |
| tekrar / yeniden kullanim | reuse | identity |
| tekrar dene | retry | social |
| temizle / temizlik / temizleyici | clean up / cleanup / cleaner | app |
| temizlik | cleanup | social |
| tepe | peak | social |
| tolerans | tolerance | infra |
| ton (musical) | musical key | social |
| tonik | tonic | social |
| tur | kind | app |
| tur (key kind: vocals/stems/...) | kind | infra |
| tur (list kind: sarkilar/coverlar) | kind | social |
| tur (publication genre) | genre | social |
| tur (section / structure / item type) | type | social |
| ucret / ucretli | charge / paid | app |
| ulasilamaz | unreachable | app |
| uret | generate | identity |
| uret / uretim | generate / generation | app |
| uygulama | app | app |
| uygulama (fiber app) | app | infra |
| uzanti | extension | social |
| varsayilan | default | infra |
| veri | data | app |
| veritabani | database | social, identity |
| web anahtari | web key | infra |
| yabanci | foreign (test names) / not owned (`ErrKeyNotOwned`) | app |
| yabanci (anahtar) | not owned (`ErrKeyNotOwned`) | infra |
| yakalayici | interceptor | identity |
| yama | patch | social |
| yanit | response | social, identity |
| yapi / yapi ogesi | structure / structure item | social |
| yardimci | helpers | social |
| yayin | publication | app |
| yayin (JWKS) | published | identity |
| yayinda (status) | published | social |
| yayinla (verb) | publish | social |
| yazar | author | social |
| yeni X (row to insert) | X insert | social |
| yetki | permission / forbidden | social |
| yol | path | identity |
| yorum | comment | infra |
| yorum acik | comments enabled | social |
| yuk | payload | app, social |
| yukari akis | upstream | identity |
| yukle / yukleme / yuklenen | upload / upload / uploaded | app |
| zaman | time | social |
| zamanMs | time ms | social |
| zarf | envelope | infra |
| zorlayici | enforcer | identity |

**Wire guard and localized-error rulings (verbatim)**

**Rulings (design decisions made here)**

Task A (wire guard):
- **A1. Duplicates, no counts.** Every occurrence is one line. Lines are sorted and compared as a multiset, so deleting one of two equal literals shows up as a diff.
- **A2. What `STR` skips.** It skips import paths, arguments of `log.Print*`, `log.Fatal*` and `log.Panic*`, `panic(...)`, and `errors.New`/`fmt.Errorf` **inside function bodies**. Comments are not part of the AST, so they never appear.
  - Deviation from the brief: package-level sentinel errors (`var ErrX = errors.New("...")`) are **kept**. Their text reaches clients: the social sentinels go out as `message` via `err.Error()` until Task B, and identity's `ErrSifreKisa` is rendered into the `kayit.html` page permanently.
  - A rename task that changes a sentinel's text must therefore `-update` the snapshot, and the reviewer must accept that change explicitly.
- **A3. What counts as a `ROUTE`.** A `ROUTE` line is emitted only when a call to `Get/Post/Put/Patch/Delete/Head/Options/All/Group/Use/Route/Static/Handle/HandleFunc` has a string-literal first argument that starts with `/`.
  - This keeps header reads such as `c.Get("Authorization")` out.
  - Identity routes are built from constants (`"GET "+yolGiris`). Those constants are covered as `STR` lines only.
- **A4. Asset lines.** The format is `FILE<TAB><logical-name><TAB><sha256>`.
  - A fixed table maps each logical name to a path. The package-rename tasks edit the paths and never the names.
  - The tool fails if any file matching `internal/*/migrations/*.sql` or `internal/*/sablonlar/*` is missing from the table. If the template directory is renamed, edit that glob as well.
- **A5. Messages stay in the snapshot.** User-facing messages passed to `response.*` and `dogrulamaHatasi` are kept. Task B regenerates the snapshot with `-update`, and its reviewer checks that every removed or added line is message text (see the expected diff in Task B).
- **A6. CI runs the guard.** CI only ran `go test ./internal/...`, so the guard would never have run. The CI step becomes `go test ./internal/... ./pkg/... ./tools/...`.
- **A7. Known blind spot, still exposed until Task B.** These texts are created by in-function `fmt.Errorf` and reach clients today. The guard cannot see them:
  - `ServiceError(c, err.Error())` in `render.go`, `master.go`, `export.go` and `upload.go`;
  - `AIError(c, err.Error())` in `lyrics.go`;
  - the `ErrGecersizStemAyari` wraps in `model/render.go`.

  Separately, `render.go` and `master.go` compare `err.Error() == "job not completed"` (and `"job already completed"`) against `fmt.Errorf` texts in `service/render_service.go` and `service/master_service.go`. Translating those service texts silently turns a 400 into a 500. **The rename tasks must not translate these error texts, and no `v*` tag may be cut between the first rename task and Task B.**

Task B (localized messages):
- **B1. `i18n.Key` is a struct (`type Key struct{ id string }`), not a string.** Any leftover `response.X(c, "free text")` fails to compile, so the compiler does the inventory check. That is how the `"Biraz yavasla"` call in `main.go` and the two in-test literals were found.
- **B2. The language is resolved lazily by `response.Lang(c)`.** It parses Accept-Language once and caches the result in `c.Locals` under a private key type.
  - No middleware needs wiring.
  - Errors from the auth, rate-limit and deleted-user middleware are localized too, even though they run before any handler.
- **B3. Helper signatures.** The new signature is `Error(c, status int, code string, msg i18n.Key, details any, args ...any)`. The other helpers swap `message string` for `msg i18n.Key`. Codes, statuses and the JSON shape are unchanged.
- **B4. Headers.** Every error response sets `Content-Language: tr|en` and `Vary: Accept-Language`.
- **B5. Language parsing.**
  - The highest q value wins, and ties go to the earlier entry.
  - Region subtags are ignored (`en-GB` → en).
  - `q=0`, malformed q values, q>1 and `*` never select a language.
  - The default is `tr`.
  - `Message` falls back to TR for an unknown language and returns the key id for an unknown key.
- **B6. Dynamic `err.Error()` messages are gone.**
  - The 13 `ServiceError(c, err.Error())` sites become `internalError(c, err)`: the error is logged with method and route, and the response is a generic 500 `SERVICE_ERROR` with `KeyInternal`.
  - The 2 AI sites become `aiError(c, err)`.
  - The render stem-settings validation becomes `KeyStemSettingsInvalid`.
  - This is a deliberate behavior change: internal error detail no longer leaks to clients, and it could not have been localized anyway.
- **B7. Social validation.**
  - `dogrulamaHatasi(msg string)` is replaced by `invalid(key i18n.Key, args ...any)`, which returns `*ValidationError{Key, Args}`. That error unwraps to `ErrDogrulama`, so `errors.Is` still matches.
  - The handler checks `errors.As` first and answers 422 `dogrulama` with the localized key.
  - Each sentinel gets a key in the mapping table. Sentinel texts are now used only in logs.
  - The only parameterized messages are the two field-URL messages (`%s` = `masterUrl` / `stem url`).
- **B8. The Fiber `customErrorHandler` also goes through `response.Error`.**
  - Statuses 404, 405, 413 and 426 get specific keys. Any other `*fiber.Error` gets `KeyRequestFailed`, and a non-Fiber error gets `KeyInternal`.
  - The code stays `SERVICE_ERROR`.
  - It now uses `errors.As`, so a wrapped `*fiber.Error` also matches.
- **B9. API values stay as they are inside both languages.** Enum values and wire field names are literal API values, so they are not translated: `herkes/takipciler/link`, `senin-icin/takip/yeni`, `sarkilar/coverlar`, `zamanMs`, `sureSn`, `masterUrl`, `ton.mod`, `hedefTuru`, `neden`.
- **B10. Not localized.** The `details` payloads (validator field errors, `maxSize`, `contentType`) are out of scope. So are the identity HTML/OAuth errors and log text.

---

## Tasks

### Task 1: Wire-contract guard (`tools/wirecheck`)

_Inserted verbatim from the verified bracketing tasks. It runs FIRST, before any rename. Its commands run in `core-service/`, except Step 8 (repository root). In its rulings it is called "Task A"._

**Goal:** Add a committed, mechanical guard. It snapshots everything that can reach the wire, and it fails when a rename task changes any of it.

**Files**
- Create: `core-service/tools/wirecheck/main.go`
- Create: `core-service/tools/wirecheck/wirecheck_test.go`
- Create (generated): `core-service/tools/wirecheck/testdata/wire.txt` (2141 lines at `de6c92b`)
- Modify: `.github/workflows/core-service.yml` (repository root)

**Interfaces**
- `func Snapshot(root string) ([]string, error)` returns the sorted lines for the core-service tree at `root`.
- Line kinds (separator: TAB):
  - `TAG\t<raw tag>`
  - `STR\t<Go-quoted value>`
  - `ROUTE\t<METHOD> <path>`
  - `FILE\t<logical-name>\t<sha256>`
- Commands:
  - `go run ./tools/wirecheck [-root .]` prints the snapshot.
  - `go test ./tools/wirecheck` compares the code with `testdata/wire.txt`.
  - `go test ./tools/wirecheck -update` rewrites the file.
- **Contract for later tasks:**
  - A pure rename leaves `go test ./tools/wirecheck` green.
  - A package-rename task edits only the `assets` paths (and `assetGlobs` if the template directory moves). It never edits logical names.
  - Any other change to the snapshot is a wire change. It needs `-update` plus explicit reviewer sign-off on the `wire.txt` diff.

- [ ] **Step 1: Create the tool**

Create `core-service/tools/wirecheck/main.go`:

```go
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
	"kimlik-migration-001":    "internal/kimlik/migrations/001_kullanici.sql",
	"kimlik-migration-002":    "internal/kimlik/migrations/002_refresh_indeks.sql",
	"kimlik-migration-003":    "internal/kimlik/migrations/003_federe_kimlik.sql",
	"kimlik-template-baglama": "internal/kimlik/sablonlar/baglama.html",
	"kimlik-template-cakisma": "internal/kimlik/sablonlar/cakisma.html",
	"kimlik-template-giris":   "internal/kimlik/sablonlar/giris.html",
	"kimlik-template-kayit":   "internal/kimlik/sablonlar/kayit.html",
	"sosyal-migration-001":    "internal/sosyal/migrations/001_sosyal.sql",
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
```

- [ ] **Step 2: Create the test**

Create `core-service/tools/wirecheck/wirecheck_test.go`:

```go
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
```

- [ ] **Step 3: Generate the snapshot and run the tests**

```bash
go test ./tools/wirecheck -update
go test -count=1 -v ./tools/wirecheck
wc -l tools/wirecheck/testdata/wire.txt
go run ./tools/wirecheck | diff - tools/wirecheck/testdata/wire.txt && echo SAME
go run ./tools/wirecheck | cut -f1 | sort | uniq -c
```

Expected:

```
ok  	github.com/makeasinger/api/tools/wirecheck	0.5s
=== RUN   TestWireSnapshot
--- PASS: TestWireSnapshot
=== RUN   TestDiffCountsDuplicates
--- PASS: TestDiffCountsDuplicates
=== RUN   TestFileLinesRules
--- PASS: TestFileLinesRules
PASS
    2141 tools/wirecheck/testdata/wire.txt
SAME
  10 FILE
  57 ROUTE
1622 STR
 452 TAG
```

Spot-check the snapshot: `grep '^ROUTE' tools/wirecheck/testdata/wire.txt` must list every route in `cmd/server/main.go` and in `handler/sosyal.go`'s `Rotalar` (for example `ROUTE	GET /profil/ben`, `ROUTE	GROUP /api/public`). `grep '^FILE'` must list the 10 assets.

- [ ] **Step 4: Prove the guard catches a wire change, then revert**

```bash
sed -i '' 's/json:"message"/json:"msg"/' pkg/response/response.go
go test -count=1 ./tools/wirecheck
git checkout pkg/response/response.go
go test -count=1 ./tools/wirecheck
```

Expected: the first run FAILs with the diff below, and the second run passes.

```
--- FAIL: TestWireSnapshot
    wirecheck_test.go:55: wire contract changed (--- testdata/wire.txt +++ current code)
        -TAG	json:"message"
        +TAG	json:"msg"
        If this change is intended, run: go test ./tools/wirecheck -update
FAIL
ok  	github.com/makeasinger/api/tools/wirecheck
```

Also check that an unlisted asset is caught:

```bash
cp internal/sosyal/migrations/001_sosyal.sql internal/sosyal/migrations/002_x.sql
go test -count=1 ./tools/wirecheck   # FAIL: ... 002_x.sql matches internal/*/migrations/*.sql but is not in the assets table
rm internal/sosyal/migrations/002_x.sql
```

- [ ] **Step 5: Prove that a pure rename does not change the snapshot, then revert**

This renames an exported sentinel, an exported type, and the `hesapisaret` package itself. For the package, gopls rewrites every import but cannot move the directory (`getFile: ... is a directory`), so `git mv` finishes the move.

```bash
~/go/bin/gopls rename -w internal/sosyal/hatalar.go:13:2 ErrNotFound
~/go/bin/gopls rename -w internal/handler/sosyal.go:52:6 SocialHandler
~/go/bin/gopls rename -w internal/hesapisaret/isaret.go:4:9 deletionmark   # prints the "is a directory" error; imports are already rewritten
git mv internal/hesapisaret internal/deletionmark
go build ./... && go vet ./...
go test -count=1 ./tools/wirecheck ./internal/handler ./internal/middleware ./internal/sosyal
git diff HEAD --stat | tail -1
git reset -q --hard HEAD        # tools/ is untracked here and survives; if already committed, use the same command
```

Expected: every package passes (`ok`), and the stat shows `58 files changed, 175 insertions(+), 175 deletions(-)`. The snapshot is unchanged, even though import paths, identifiers and a directory changed.

- [ ] **Step 6: Run the guard in CI**

In `.github/workflows/core-service.yml` (repository root):

```diff
diff --git a/.github/workflows/core-service.yml b/.github/workflows/core-service.yml
index 9397241..2a472b6 100644
--- a/.github/workflows/core-service.yml
+++ b/.github/workflows/core-service.yml
@@ -106,7 +106,7 @@ jobs:
         run: go vet ./...
 
       - name: Unit testler
-        run: go test ./internal/...
+        run: go test ./internal/... ./pkg/... ./tools/...
 
   docker:
     name: Docker Build & Push
```

- [ ] **Step 7: Lint, vet and test**

```bash
gofmt -l tools
go vet ./tools/...
~/go/bin/golangci-lint run ./...
go test ./internal/... ./pkg/... ./tools/...
```

Expected:
- `gofmt` and `vet` print nothing.
- Lint prints `0 issues.`
- Every package prints `ok`. `pkg/response` prints `[no test files]`.

- [ ] **Step 8: Commit**

```bash
git add .github/workflows/core-service.yml core-service/tools
git commit -F - <<'EOF'
Kablo sozlesmesi bekcisi: tools/wirecheck

Struct etiketleri, string literaller (log, panic ve fonksiyon ici hata
metinleri haric), rota kayitlari ve gomulu varliklarin SHA-256 ozetleri
yol ve tanimlayici icermeyen sirali bir anlik goruntude toplanir.
Isim degistirme gorevleri bu goruntuyu degistirmemelidir; kasitli
degisiklik -update ile kaydedilir ve diff olarak incelenir.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

**Note for every rename task:** finish each task with `go test -count=1 ./tools/wirecheck`.
- A pure identifier or file rename must stay green.
- A package-directory rename edits only the `assets` table paths (and `assetGlobs` if `sablonlar/` moves).
- Do not translate the error texts listed in Ruling A7.


---

### Task 2: `model` (with `pkg/response` and `websocket` checked unchanged)

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/model/enums.go` | (unchanged) |
| `internal/model/expires.go` | (unchanged) |
| `internal/model/export.go` | (unchanged) |
| `internal/model/files.go` | (unchanged) |
| `internal/model/job.go` | (unchanged) |
| `internal/model/job_test.go` | (unchanged) |
| `internal/model/lyrics.go` | (unchanged) |
| `internal/model/master.go` | (unchanged) |
| `internal/model/model_test.go` | (unchanged) |
| `internal/model/render.go` | (unchanged) |
| `internal/model/stem_modu_test.go` | `internal/model/render_test.go` |
| `internal/model/upload.go` | (unchanged) |
| `internal/model/websocket.go` | (unchanged) |
| `internal/websocket/hub.go` | (unchanged) |
| `pkg/response/response.go` | (unchanged) |

**Declarations** (10 rows)

<!-- renames:2 -->
| old | new | kind | file |
|---|---|---|---|
| Job.SahibiMi | Job.IsOwnedBy | method | internal/model/job.go |
| ErrGecersizStemAyari | ErrInvalidStemSettings | var | internal/model/render.go |
| RenderStartRequest.StemAyarlari | RenderStartRequest.StemSettings | method | internal/model/render.go |
| TestJob_SahibiMi | TestJob_IsOwnedBy | func (test) | internal/model/job_test.go |
| TestExpiresPtr_SifirZamanNil | TestExpiresPtr_ZeroTimeIsNil | func (test) | internal/model/model_test.go |
| TestUploadVocalResponse_SureliLinkteExpiresAtVar | TestUploadVocalResponse_ExpiringLinkHasExpiresAt | func (test) | internal/model/model_test.go |
| TestExportResponse_KaliciLinkteExpiresAtHicYok | TestExportResponse_PermanentLinkOmitsExpiresAt | func (test) | internal/model/model_test.go |
| TestStemResult_ExpiresAtAlani | TestStemResult_ExpiresAtField | func (test) | internal/model/model_test.go |
| enstruman | instrumentPtr | func (test helper) | internal/model/render_test.go |
| TestStemAyarlari | TestRenderStartRequest_StemSettings | func (test) | internal/model/render_test.go |

**Wire strings to keep (must stay byte-identical)**

_From the infra glossary, §internal/model:_

| file:line | literal | why |
|---|---|---|
| enums.go:7-18 | Genre values `pop` ... `blues` | API enum |
| enums.go:31-37 | SectionType values `intro` ... `instrumental` | API enum |
| enums.go:49-64 | Instrument values `drums` ... `fx`, `mix`, `rest` | API enum, and the R2 stem file name segment |
| enums.go:71-75 | JobStatus `queued`, `running`, `succeeded`, `failed`, `canceled` | API and Redis job records |
| enums.go:82-84, 91-92 | `auto`, `range`, `fixed`, `manual` | API enum |
| enums.go:99-110 | `C`, `C#`, ... `B` | API enum |
| enums.go:117-118, 125-127, 134-136 | `major`, `minor`, `minimal`, `medium`, `full`, `straight`, `swing`, `half_time` | API enum |
| enums.go:143-145, 152-156 | `clean`, `warm`, `loud`, `default`, `vocal_friendly`, `bass_heavy`, `bright`, `warm` | API enum |
| enums.go:163-165 | `en`, `tr`, `fr` | API enum |
| enums.go:174-176 | StemMode `mix`, `focus`, `stems` | API enum |
| job.go:7-19 | Job json tags (`id`, `type`, `ownerId`, `status`, `progress`, `currentStep`, `error`, `-`, `result`, `createdAt`, `startedAt`, `completedAt`, `retryCount`) | Redis `job:{id}` JSON |
| job.go:30-31 | `"render"`, `"master"` | Job type stored in the job record |
| job.go:36-51 | RenderJobPayload and MasterJobPayload json tags | Payload of the asynq task |
| export.go:7-65 | all json and validate tags | API |
| files.go:8-20 | all json and validate tags | API |
| lyrics.go:5-28 | all json and validate tags (the `oneof` lists must match the enums) | API |
| master.go:7-98 | all json and validate tags, including MasterResultRecord (`fileKey`, ...) stored in Redis | API and Redis |
| render.go:11-15 | RenderStartRequest tags, including `oneof=mix focus stems` and the instrument `oneof` | API |
| render.go:50-167 | all json and validate tags, plus `enums:"drums,bass,...,fx"` at :81 (read by swag) | API and Swagger |
| upload.go:7-15 | json tags | API |
| websocket.go:7-11 | `"progress"`, `"complete"`, `"error"`, `"ping"`, `"pong"` | WebSocket message types |
| websocket.go:16-57 | all json and validate tags | WebSocket and API |

_From the infra glossary, §internal/websocket, pkg/response:_

| file:line | literal | why |
|---|---|---|
| response.go:7-14 | `VALIDATION_ERROR`, `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `RATE_LIMITED`, `JOB_FAILED`, `SERVICE_ERROR`, `AI_ERROR` | Error codes |
| response.go:18, 22-24 | json tags `error`, `code`, `message`, `details` | Error envelope |
| response.go:54 | `"Rate limit exceeded"` | User-visible `message` (already English) |
| hub.go | none of its own. It marshals `model.WS*` types. |  |

**Risks (from the glossaries; names in them are the old ones)**

_From the infra glossary, risk 1:_

1. **Error text that reaches clients and tests.**
   - Error strings defined in this scope are sent to users without passing through an error code:
     - `model.ErrGecersizStemAyari` ("gecersiz stem ayari: ...") goes straight into the API `message` (`handler/render.go:57`).
     - Errors from `client` (Suno errors, `ErrGecersizURL`, `ErrYabanciAnahtar`, "bucket'lar arasi kopya ...") end up in `job.Error` through `worker.failJob(err.Error())`. Clients then see them in status responses and WebSocket `error` messages.
     - The brief treats messages as non-wire and has them localized separately. Translating these strings is allowed, but it changes visible text, so decide that on purpose.
   - Tests that match error substrings must change in the same commit as the strings they match:
     - `suno_client_test.go:136` matches `"zaman asimi"` from `suno_client.go:353`.
     - `suno_client_test.go:220` matches `"kimligi"` from `suno_client.go:192`.
     - `suno_client_test.go:101, 208, 307` match `"yasakli kelime"`, `"bolunemedi"` and `"katalog eslesmesi"`. Those come from the fake provider's `errorMessage`, so they are safe as long as the fake and the assertion stay in sync.
     - `bucket_test.go:98` checks for the absence of `cokgizli` and `X-Amz-Signature`.
   - The user-facing Turkish middleware messages ("Kimlik dogrulanamadi", "Hesap silindi", "Hesap durumu denetlenemedi", "Rate limit denetlenemedi") belong to the localization task. Leave them unless that task says otherwise.

_From the app glossary, risk R4:_

**R4. Validation `details` keys are Go field names.** `formatValidationErrors` (handler/lyrics.go:88-97) uses `e.Field()`, and no `RegisterTagNameFunc` is set anywhere. The keys in `error.details` for `VALIDATION_ERROR` are therefore the **Go struct field names** of `model` request types (for example `ProjectID`, `StemURLs`, `MasterFileURL`, `JobID`, `URLs`). All of them are English today. A model-glossary change to any validated field name (casing included) would change the wire. Model fields carrying a `validate` tag: Album, Arrangement, Artist, Bars, BitDepth, BPM, Brief, Channels, Credits, CurrentLyrics, Density, Emphasis, FileURL, FocusInstrument, Format, Genre, Groove, ID, Instructions, Instruments, JobID, Key, Language, MasterFileURL, MasterURL, Max, Metadata, Min, MixSnapshot, Mode, OffsetMs, Preset, PreviewStartTime, Profile, ProjectID, Quality, SampleRate, Scale, SectionEmphasis, SectionID, SectionType, StemID, StemMode, StemURLs, Structure, TakeID, Title, Tonic, Type, URLs, Value, Vibes, VocalTakes, VocalURLs, VolumeDb, Year.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Rename the file**

```bash
git mv internal/model/stem_modu_test.go internal/model/render_test.go
```

- [ ] **Step 3: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 2 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 4: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- `pkg/response/response.go` and `internal/websocket/hub.go` have no Turkish identifiers. Check that their comments are English and change nothing else.

Files (15): `internal/model/enums.go`, `internal/model/expires.go`, `internal/model/export.go`, `internal/model/files.go`, `internal/model/job.go`, `internal/model/job_test.go`, `internal/model/lyrics.go`, `internal/model/master.go`, `internal/model/model_test.go`, `internal/model/render.go`, `internal/model/render_test.go`, `internal/model/upload.go`, `internal/model/websocket.go`, `internal/websocket/hub.go`, `pkg/response/response.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/model/enums.go internal/model/expires.go internal/model/export.go internal/model/files.go internal/model/job.go internal/model/job_test.go internal/model/lyrics.go internal/model/master.go internal/model/model_test.go internal/model/render.go internal/model/render_test.go internal/model/upload.go internal/model/websocket.go internal/websocket/hub.go pkg/response/response.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 5: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 2 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 6: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 7: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(model): Ingilizce adlandirma

model paketindeki Turkce tanimlayicilar, testler ve yorumlar Ingilizceye
cevrildi; stem_modu_test.go -> render_test.go. Dogrulanan alan adlari
degismedi (details anahtarlari). Kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 3: `config`

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/config/config.go` | (unchanged) |
| `internal/config/config_test.go` | (unchanged) |

**Declarations** (34 rows)

<!-- renames:3 -->
| old | new | kind | file |
|---|---|---|---|
| Config.Sosyal | Config.Social | field | internal/config/config.go |
| RateLimitConfig.SosyalYayinPerHour | RateLimitConfig.SocialPublicationPerHour | field | internal/config/config.go |
| RateLimitConfig.SosyalYorumPerMin | RateLimitConfig.SocialCommentPerMin | field | internal/config/config.go |
| RateLimitConfig.SosyalCoverPerHour | RateLimitConfig.SocialCoverPerHour | field | internal/config/config.go |
| SosyalConfig | SocialConfig | type | internal/config/config.go |
| SosyalConfig.WebAnahtari | SocialConfig.WebKey | field | internal/config/config.go |
| ServerConfig.ApiDomain | ServerConfig.APIDomain | field (initialism fix; applied) | internal/config/config.go |
| AuthConfig.Federasyon | AuthConfig.Federation | field | internal/config/config.go |
| FederasyonIstemci | FederationClient | type | internal/config/config.go |
| FederasyonIstemci.Ad | FederationClient.Name | field | internal/config/config.go |
| FederasyonIstemci.IstemciID | FederationClient.ClientID | field | internal/config/config.go |
| FederasyonIstemci.IstemciSir | FederationClient.ClientSecret | field | internal/config/config.go |
| FederasyonIstemci.AppleEkipID | FederationClient.AppleTeamID | field | internal/config/config.go |
| FederasyonIstemci.AppleAnahtarID | FederationClient.AppleKeyID | field | internal/config/config.go |
| FederasyonIstemci.AppleAnahtarP8 | FederationClient.AppleKeyP8 | field | internal/config/config.go |
| federasyonIstemcileriOlustur | buildFederationClients | func | internal/config/config.go |
| TestLoad_R2IkiBucket | TestLoad_R2TwoBuckets | func (test) | internal/config/config_test.go |
| TestLoad_PresignTTLVarsayilani | TestLoad_PresignTTLDefault | func (test) | internal/config/config_test.go |
| TestAuthVarsayilanlari | TestLoad_AuthDefaults | func (test) | internal/config/config_test.go |
| TestLoad_CozulemeyenTTLVarsayilanaDusmezSessizce0Olur | TestLoad_UnparsableTTLSilentlyBecomesZero | func (test) | internal/config/config_test.go |
| TestLoad_HizLimitiVarsayilanlari | TestLoad_RateLimitDefaults | func (test) | internal/config/config_test.go |
| TestLoad_HizLimitiOrtamDegiskeniEzer | TestLoad_RateLimitEnvOverrides | func (test) | internal/config/config_test.go |
| federasyonEnvTemizle | clearFederationEnv | func (test helper) | internal/config/config_test.go |
| federasyonBul | findFederationClient | func (test helper) | internal/config/config_test.go |
| TestLoad_FederasyonIkisiTamYapilandirilinca | TestLoad_FederationBothFullyConfigured | func (test) | internal/config/config_test.go |
| TestLoad_FederasyonGoogleBossaEklenmez | TestLoad_FederationOmitsGoogleWhenEmpty | func (test) | internal/config/config_test.go |
| TestLoad_FederasyonAppleBossaEklenmez | TestLoad_FederationOmitsAppleWhenEmpty | func (test) | internal/config/config_test.go |
| TestLoad_FederasyonIkisiDeBossaBos | TestLoad_FederationEmptyWhenNoneConfigured | func (test) | internal/config/config_test.go |
| TestLoad_SunoVarsayilanlari | TestLoad_SunoDefaults | func (test) | internal/config/config_test.go |
| TestLoad_SunoModelEnvIleDegisir | TestLoad_SunoModelFromEnv | func (test) | internal/config/config_test.go |
| TestLoad_SosyalYayinLimiti | TestLoad_SocialPublicationLimit | func (test) | internal/config/config_test.go |
| TestLoad_KesfetLimitleriVarsayilan | TestLoad_SocialLimitsDefault | func (test) | internal/config/config_test.go |
| TestLoad_KesfetLimitleriOrtamdan | TestLoad_SocialLimitsFromEnv | func (test) | internal/config/config_test.go |
| TestLoad_WebAnahtariDosyadan | TestLoad_WebKeyFromFile | func (test) | internal/config/config_test.go |

**Wire strings to keep (must stay byte-identical)**

_From the infra glossary, §internal/config, config.yaml, .env.example:_

| file:line | literal | why |
|---|---|---|
| config.go:18 | `"_FILE"` | Docker secret suffix |
| config.go:192-201 | `readSecret("REDIS_PASSWORD")`, `"GROQ_API_KEY"`, `"SUNO_API_KEY"`, `"R2_ACCOUNT_ID"`, `"R2_ACCESS_KEY_ID"`, `"R2_SECRET_ACCESS_KEY"`, `"AUTH_DB_URL"`, `"AUTH_SIGNING_KEY"`, `"AUTH_CRYPTO_KEY"`, `"SOSYAL_WEB_ANAHTARI"` | Env and secret names used by deploy manifests. Keep `SOSYAL_WEB_ANAHTARI` even though the Go field becomes `WebKey`. |
| config.go:203-206 | `"config"`, `"yaml"`, `"."`, `"./config"` | Config file lookup |
| config.go:212-262 | every `viper.BindEnv(key, ENV)` pair, including `"ratelimit.sosyal_yayin_per_hour"`/`"RATELIMIT_SOSYAL_YAYIN_PER_HOUR"`, `"ratelimit.sosyal_yorum_per_min"`/`"RATELIMIT_SOSYAL_YORUM_PER_MIN"`, `"ratelimit.sosyal_cover_per_hour"`/`"RATELIMIT_SOSYAL_COVER_PER_HOUR"` and `"sosyal.web_anahtari"`/`"SOSYAL_WEB_ANAHTARI"` | Config keys and env names |
| config.go:265-345 | every `viper.SetDefault` key and value (for example `"suno.callback_url"` = `https://makesinger-api.celalettindemir.dev/suno/callback`, `"groq.model"` = `openai/gpt-oss-120b`) | Config keys and defaults |
| config.go:352-424 | every `viper.GetX("...")` key, including `"sosyal.web_anahtari"` | Config keys. Fields are mapped by hand, with no mapstructure (see Risks). |
| config.go:445-459 | `"auth.fed_google_client_id"`, `"auth.fed_google_client_secret"`, `"auth.fed_apple_client_id"`, `"auth.fed_apple_team_id"`, `"auth.fed_apple_key_id"`, `"auth.fed_apple_key_p8"` | Config keys |
| config.go:447, :455 | `"google"`, `"apple"` (`FederasyonIstemci.Ad`) | Provider names. kimlik uses them for routes (`/federasyon/{ad}/geri`) and stores them in the DB (`federe_kimlikler.provider`). |
| config_test.go (whole file) | all env names passed to `os.Setenv`/`t.Setenv`/`os.Unsetenv` (`R2_*`, `AUTH_*`, `AUTH_FED_*`, `RATELIMIT_SOSYAL_*`, `RATELIMIT_PUBLIC_*`, `SOSYAL_WEB_ANAHTARI`, `SOSYAL_WEB_ANAHTARI_FILE`, `SUNO_MODEL`, `SUNO_CALLBACK_URL`) | Tests the env contract |
| config.yaml (all lines) | every YAML key: the sections `server`, `redis`, `ratelimit` (including `sosyal_yayin_per_hour`, `sosyal_yorum_per_min`, `sosyal_cover_per_hour`), `sosyal` (`web_anahtari`), `groq`, `r2`, `suno`, `audio` and `auth` | Viper keys. Comments may be translated. |
| .env.example (all lines) | every variable name, and the `AUTH_CLIENT_ID=makesinger-mobil` and `AUTH_REDIRECT_URIS` values | Env contract. Comments may be translated. The callback URLs in comments (`/federasyon/google/geri`, `/federasyon/apple/geri`) are route paths and stay. |

**Risks (from the glossaries; names in them are the old ones)**

_From the infra glossary, risk 3:_

3. **Config is mapped by hand, but other packages use it.**
   - There is no `mapstructure` tag, no `viper.Unmarshal` and no `SetEnvKeyReplacer`. Every field is filled by an explicit `viper.GetX("key")` (config.go:350-425), so renaming Go fields cannot change config keys.
   - Keys exist only as string literals, so do not "translate" `sosyal.web_anahtari`, `ratelimit.sosyal_*`, `SOSYAL_WEB_ANAHTARI` or `RATELIMIT_SOSYAL_*` along with the fields.
   - Fields used outside the package:
     - `cmd/server/main.go`: `Server.ApiDomain` (:53-54), `Sosyal.WebAnahtari` (:268), `RateLimit.Sosyal*PerHour/PerMin` (:314-317).
     - `internal/kimlik/sunucu.go` and its tests: `Auth.Federasyon`, `FederasyonIstemci.{Ad,IstemciID,IstemciSir,AppleEkipID,AppleAnahtarID,AppleAnahtarP8}`, also used by the kimlik `yukari_akis*.go` and `federe_sayfa.go` files.
   - The values of `FederasyonIstemci.Ad` (`"google"`/`"apple"`) are wire.
   - Pre-existing gap: `.env.example` lists `RATELIMIT_LYRICS_PER_MIN`, `..._RENDER_PER_HOUR`, `..._MASTER_PER_HOUR`, `..._EXPORT_PER_HOUR` and `..._UPLOAD_PER_HOUR`. None of them has a `BindEnv`, and without a key replacer `AutomaticEnv` cannot map them, so they have no effect. This is not a rename issue; do not fix it as part of the rename.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 3 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 3: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- `config.yaml` and `.env.example` are **not** edited here. Their bytes are hashed by the guard (`FILE` lines). Task 24 translates their comments.

Files (2): `internal/config/config.go`, `internal/config/config_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/config/config.go internal/config/config_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 4: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 3 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 5: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 6: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(config): Ingilizce adlandirma

config alanlari, fonksiyonlari ve yorumlari Ingilizceye cevrildi;
mapstructure anahtarlari ve env adlari degismedi. Kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 4: `hesapisaret` → `deletionmark`, and `auth`

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/auth/jwks_verifier.go` | (unchanged) |
| `internal/auth/jwks_verifier_test.go` | (unchanged) |
| `internal/hesapisaret/isaret.go` | `internal/deletionmark/mark.go` |
| `internal/hesapisaret/isaret_test.go` | `internal/deletionmark/mark_test.go` |

The package clause `package hesapisaret` becomes `package deletionmark` (manual row below; done by Step 2).

**Declarations** (36 rows)

<!-- renames:4 -->
| old | new | kind | file |
|---|---|---|---|
| EsnekBool | FlexibleBool | type | internal/auth/jwks_verifier.go |
| EsnekBool.UnmarshalJSON | FlexibleBool.UnmarshalJSON | method | internal/auth/jwks_verifier.go |
| YukariAkisDogrulayici | UpstreamVerifier | type | internal/auth/jwks_verifier.go |
| NewYukariAkisDogrulayici | NewUpstreamVerifier | func | internal/auth/jwks_verifier.go |
| YukariAkisDogrulayici.DogrulaNonce | UpstreamVerifier.VerifyWithNonce | method | internal/auth/jwks_verifier.go |
| YukariAkisDogrulayici.Close | UpstreamVerifier.Close | method | internal/auth/jwks_verifier.go |
| kidIcin | kidFor | func (test helper) | internal/auth/jwks_verifier_test.go |
| sahteJWKS | fakeJWKS | func (test helper) | internal/auth/jwks_verifier_test.go |
| sahteJWKSRSA | fakeJWKSRSA | func (test helper) | internal/auth/jwks_verifier_test.go |
| tokenUret | signIDToken | func (test helper) | internal/auth/jwks_verifier_test.go |
| denemeIssuer | testIssuer | const (test) | internal/auth/jwks_verifier_test.go |
| denemeIstemci | testClientID | const (test) | internal/auth/jwks_verifier_test.go |
| TestEsnekBoolStringVeBoolKabulEder | TestFlexibleBool_AcceptsStringAndBool | func (test) | internal/auth/jwks_verifier_test.go |
| TestEsnekBoolBozukDegerHataVerir | TestFlexibleBool_RejectsMalformedValue | func (test) | internal/auth/jwks_verifier_test.go |
| TestAlgSabitlenir | TestUpstreamVerifier_PinsAlgorithm | func (test) | internal/auth/jwks_verifier_test.go |
| TestNonceEslesmezseReddedilir | TestUpstreamVerifier_RejectsNonceMismatch | func (test) | internal/auth/jwks_verifier_test.go |
| TestNonceBosOlamaz | TestUpstreamVerifier_RejectsEmptyNonce | func (test) | internal/auth/jwks_verifier_test.go |
| TestIssuerVeAudienceZorunlu | TestUpstreamVerifier_RequiresIssuerAndAudience | func (test) | internal/auth/jwks_verifier_test.go |
| TestSuresiDolmusTokenReddedilir | TestUpstreamVerifier_RejectsExpiredToken | func (test) | internal/auth/jwks_verifier_test.go |
| TestYukariAkisDogrulayiciZorunluAlanlar | TestNewUpstreamVerifier_RequiredFields | func (test) | internal/auth/jwks_verifier_test.go |
| TestAnahtarRotasyonuTakipEdilir | TestUpstreamVerifier_FollowsKeyRotation | func (test) | internal/auth/jwks_verifier_test.go |
| package hesapisaret | package deletionmark | manual: package deletionmark | isaret.go, isaret_test.go |
| onek | prefix | const | internal/deletionmark/mark.go |
| Omur | TTL | const | internal/deletionmark/mark.go |
| Anahtar | Key | func | internal/deletionmark/mark.go |
| Denetci | Checker | type (interface) | internal/deletionmark/mark.go |
| Denetci.Silinmis | Checker.IsDeleted | interface method | internal/deletionmark/mark.go |
| RedisDenetci | RedisChecker | type | internal/deletionmark/mark.go |
| NewRedisDenetci | NewRedisChecker | func | internal/deletionmark/mark.go |
| RedisDenetci.Silinmis | RedisChecker.IsDeleted | method | internal/deletionmark/mark.go |
| canliRedis | liveRedis | func (test helper) | internal/deletionmark/mark_test.go |
| TestAnahtar_BicimDegismez | TestKey_FormatUnchanged | func (test) | internal/deletionmark/mark_test.go |
| TestOmur_IsKaydiOmruneEsit | TestTTL_EqualsJobRecordTTL | func (test) | internal/deletionmark/mark_test.go |
| TestRedisDenetci_IsaretiOkur | TestRedisChecker_ReadsMark | func (test) | internal/deletionmark/mark_test.go |
| TestRedisDenetci_RedisYoksaHata | TestRedisChecker_ErrorsWithoutRedis | func (test) | internal/deletionmark/mark_test.go |
| sahteDenetci.Silinmis | fakeChecker.IsDeleted | method (test fake of deletionmark.Checker) | internal/worker/render_worker_test.go |

Rows whose kind starts with `manual:` are skipped by `apply_renames.py`. Do each by hand:
- An **embedded field** row needs no action. Renaming the embedded type renames the field and every selector on it. Check the result in the diff.
- A **`var _ X = (*Y)(nil)`** row needs no action. The assertion follows the renamed types. Check it.
- An **anonymous struct field**, **local** or **param** row: run `~/go/bin/gopls rename -w <file>:<line>:<col> <new>` on the declaring identifier.
- A **package** row is done by the package-move step.
- A **"methods unchanged; receiver becomes ..."** row needs no action. The methods keep their `op.*` names and follow the type rename.

**Wire strings to keep (must stay byte-identical)**

_From the infra glossary, §internal/auth:_

| file:line | literal | why |
|---|---|---|
| jwks_verifier.go:34, :36 | `"true"`, `"false"` | Apple sends `email_verified` as a string |
| jwks_verifier.go:47-55 | json tags `sub`, `email`, `email_verified`, `name`, `preferred_username`, `roles`, `nonce` | OIDC id_token claims |
| jwks_verifier.go:131 | `"RS256"` | Pinned algorithm |

_From the infra glossary, §internal/hesapisaret -> internal/deletionmark:_

| file:line | literal | why |
|---|---|---|
| isaret.go:14 | `"kimlik:silindi:"` | Redis key prefix for the deleted-account marker. Existing markers have up to 24 h left. |
| isaret_test.go:27 | `"kimlik:silindi:u1"` | Test that pins the format |

**Risks (from the glossaries; names in them are the old ones)**

_From the infra glossary, risk 2:_

2. **Interface renames break fakes in other packages, and the compiler only catches some of them.**
   - Implementations that must be renamed in the same change:
     - `Checker.IsDeleted` (was `Denetci.Silinmis`): `internal/worker/render_worker_test.go:381` (`sahteDenetci.Silinmis`).
     - `Copier.Copy` (was `Kopyala`): `internal/sosyal/servis_cover_test.go:17` and `internal/worker/sosyal_worker_test.go:58`.
     - `AudioAnalyzer.Analyze` (was `Analiz`): `internal/worker/sosyal_worker_test.go:72`.
   - `PrefixDeleter.DeletePrefix` keeps its name, so the fakes in `service/hesap_service_test.go:32`, `service/hesap_sosyal_test.go:46`, `worker/hesap_worker_test.go:14` and `worker/sosyal_worker_test.go:310` are unaffected.
   - The type-name renames are compile errors, so they are caught. A fake whose method is not renamed can still compile if it is only stored as a concrete type, so grep for the old names.

_From the infra glossary, risk 5:_

5. **Identifiers used from other packages, and naming collisions.**
   - Exported `client` names used elsewhere:
     - `ErrGecersizSegment`: handler/upload.go, service tests.
     - `ErrGecersizURL` and `ErrYabanciAnahtar`: handler/export.go, files.go, master.go, service tests, sosyal/servis_yayin.go.
     - `KullaniciAnahtari`: service/{export,master,upload}_service.go, sosyal/servis_cover.go, worker/{master,render}_worker.go, `e2e/testhelper_test.go`.
     - `SahipliAnahtarCoz`: service/{export,file,master}_service.go, worker/master_worker.go.
     - `KullaniciOnekleri`: service/hesap_service.go.
     - `SosyalAnahtari`: sosyal/anahtarlar.go.
     - `SosyalOneki`: service/hesap_service.go, worker/sosyal_worker.go.
     - `SahipliAnahtarVeyaURLCoz`: sosyal/servis_yayin.go.
     - `Kopyalayici`: cmd/server/main.go, sosyal/servis*.go, worker/sosyal_worker*.go.
     - `OnekSilici`: cmd/server/main.go, service/hesap_service.go, worker/sosyal_worker.go.
     - `AnalizRequest`, `AnalizResponse` and `AudioAnalizci`: worker/sosyal_worker*.go.
     - `SunoStemAdi`: worker/render_worker.go.
   - Other packages:
     - `auth`: `EsnekBool` (kimlik/baglama_sayfa_test.go), and `YukariAkisDogrulayici`, `NewYukariAkisDogrulayici` and `DogrulaNonce` (kimlik/sunucu.go, federe_sayfa.go, yukari_akis*.go and their tests).
     - `middleware`: `LimitKod`, `SilinmisKullaniciEngeli` and `WebAnahtari*` (cmd/server/main.go:268-315).
     - `hesapisaret`: `Anahtar`, `Denetci`, `NewRedisDenetci` and `Omur` (cmd/server/main.go, service/hesap_service*.go, worker/{master,render,sosyal}_worker*.go, middleware). The import path also changes to `internal/deletionmark`.
     - `model`: `SahibiMi` (service/job_kayit.go), `ErrGecersizStemAyari` (handler/render.go, service/sahiplik_canli_test.go) and `StemAyarlari` (service/render_service.go).
   - Collisions avoided on purpose:
     - The `sosyalOnek` const becomes `socialRoot`, not `socialPrefix`, so it does not shadow the exported `SocialPrefix` in the reader's eye.
     - `RateLimiter.say` becomes `hit`, not `count`, because the body has `count, err := artir.Result()`.
     - `ErrYabanciAnahtar` becomes `ErrKeyNotOwned`, not `ErrForeignKey`, which would read as a DB term.
     - In package `middleware`, the test helpers `newApp`, `liveApp`, `ipApp`, `limitedApp` and `gatedApp` are all distinct.
     - The const `testIssuer` exists in both the `auth` and `middleware` tests, which is fine because they are separate packages.
   - The kimlik package has its own unrelated `kodDogrulayici` (PKCE verifier). The identity glossary should translate it to `codeVerifier`, which does not clash with `opVerifier` or `UpstreamVerifier`.

_From the kimlik glossary, risk R4:_

**R4: cross-package API that must change in lockstep (outside this scope but visible).**
- `cmd/server/main.go:86, 94, 100, 192, 234`: `kimlik.Start` → `identity.Start`; `kimlik.Baglan` → `identity.Connect`; `.Kapat` → `.Shutdown`; `.APIAcikAnahtar` → `.APIPublicKey`; `.Hazir` → `.Ready`.
- `internal/service/hesap_service.go:33-36`: `KimlikSilici.HesapSil` must become `DeleteAccount`, or `*identity.Server` stops satisfying it. This is caught at main.go:167 compile time. The fakes in `internal/service/hesap_service_test.go:22` and `internal/handler/hesap_test.go:20` change too.
- `kimlik.ErrKullaniciYok` → `identity.ErrUserNotFound` in `internal/service/hesap_service.go:78`, `hesap_service_test.go:80` and `internal/handler/hesap_test.go:52`.
- `internal/sosyal/veritabani_test.go:27`: `kimlik.Baglan` → `identity.Connect`.
- Comments in `internal/config/config.go:161, 315, 321, 439` and `config_test.go:86, 101, 109` cite `kimlik.istemciIP`, `bcryptCost`, `Start` and `NewHizLimit`.
- Inbound dependencies owned by other glossaries:
  - `config.AuthConfig.Federasyon` and `config.FederasyonIstemci{Ad, IstemciID, IstemciSir, AppleEkipID, AppleAnahtarID, AppleAnahtarP8}`, used in sunucu.go:281-393 (the config package agent will rename these; keep viper keys unchanged);
  - `auth.YukariAkisDogrulayici`, `auth.NewYukariAkisDogrulayici`, `(*YukariAkisDogrulayici).DogrulaNonce` and `auth.Claims{UserID, Email, Name, EmailVerified}`, used in federe_sayfa.go, baglama_sayfa.go, sunucu.go and tests.

  Coordinate the names with the config and auth glossaries.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Move the package**

```bash
off=$(grep -bo '^package hesapisaret' internal/hesapisaret/isaret.go | cut -d: -f1)
~/go/bin/gopls rename -w internal/hesapisaret/isaret.go:#$((off+8)) deletionmark   # prints "getFile: ... is a directory": expected
git mv internal/hesapisaret internal/deletionmark
git mv internal/deletionmark/isaret.go internal/deletionmark/mark.go
git mv internal/deletionmark/isaret_test.go internal/deletionmark/mark_test.go
files=$(gofmt -l .); [ -z "$files" ] || gofmt -w $files
go build ./... && go vet ./...
grep -rn 'hesapisaret' --include='*.go' .
```

Expected:
- `build` and `vet` print nothing.
- `grep` prints only comment lines, such as the `// Package hesapisaret ...` doc comment. The comments step translates them.
- Every importer (`middleware`, `service`, `worker`, `handler`, `cmd/server`) now imports `github.com/makeasinger/api/internal/deletionmark`.

- [ ] **Step 3: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 4 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 4: Do the `manual:` rows**

Follow the rules under the table. Then run:

```bash
go build ./... && go vet ./...
```

- [ ] **Step 5: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- Write the package doc comment as `// Package deletionmark ...`.
- The worker fake `sahteDenetci.Silinmis` is renamed to `IsDeleted` by the interface rename. Its type name (`fakeChecker`) follows in Task 17.

Files (4): `internal/auth/jwks_verifier.go`, `internal/auth/jwks_verifier_test.go`, `internal/deletionmark/mark.go`, `internal/deletionmark/mark_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/auth/jwks_verifier.go internal/auth/jwks_verifier_test.go internal/deletionmark/mark.go internal/deletionmark/mark_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 6: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 4 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 7: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 8: Commit**

```bash
git status --short
git add -A internal/hesapisaret internal/deletionmark
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(deletionmark): hesapisaret paketi deletionmark oldu; auth Ingilizce

internal/hesapisaret -> internal/deletionmark (isaret.go -> mark.go),
Denetci -> Checker, Silinmis -> IsDeleted; auth tanimlayicilari
Ingilizceye cevrildi. Redis anahtar bicimi ayni; kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 5: `client`

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/client/anahtar.go` | `internal/client/user_key.go` |
| `internal/client/anahtar_test.go` | `internal/client/user_key_test.go` |
| `internal/client/audio_analiz.go` | `internal/client/audio_analysis.go` |
| `internal/client/audio_analiz_test.go` | `internal/client/audio_analysis_test.go` |
| `internal/client/audio_client.go` | (unchanged) |
| `internal/client/audio_client_test.go` | (unchanged) |
| `internal/client/bucket.go` | (unchanged) |
| `internal/client/bucket_test.go` | (unchanged) |
| `internal/client/groq_client.go` | (unchanged) |
| `internal/client/groq_client_test.go` | (unchanged) |
| `internal/client/kopya.go` | `internal/client/copy.go` |
| `internal/client/kopya_test.go` | `internal/client/copy_test.go` |
| `internal/client/onek_sil_test.go` | `internal/client/r2_client_delete_prefix_test.go` |
| `internal/client/r2_client.go` | (unchanged) |
| `internal/client/r2_client_test.go` | (unchanged) |
| `internal/client/sosyal_anahtar.go` | `internal/client/social_key.go` |
| `internal/client/sosyal_anahtar_test.go` | `internal/client/social_key_test.go` |
| `internal/client/suno_client.go` | (unchanged) |
| `internal/client/suno_client_test.go` | (unchanged) |

**Declarations** (94 rows)

<!-- renames:5 -->
| old | new | kind | file |
|---|---|---|---|
| ErrGecersizSegment | ErrInvalidSegment | var | internal/client/user_key.go |
| ErrGecersizURL | ErrInvalidURL | var | internal/client/user_key.go |
| ErrYabanciAnahtar | ErrKeyNotOwned | var | internal/client/user_key.go |
| sahipliTurler | ownedKinds | var | internal/client/user_key.go |
| segmentDeseni | segmentPattern | var | internal/client/user_key.go |
| KullaniciAnahtari | UserKey | func | internal/client/user_key.go |
| AnahtarSahibiDogrula | VerifyKeyOwner | func | internal/client/user_key.go |
| SahipliAnahtarCoz | ResolveOwnedKey | func | internal/client/user_key.go |
| KullaniciOnekleri | UserPrefixes | func | internal/client/user_key.go |
| sosyalOnek | socialRoot | const | internal/client/social_key.go |
| yayinKimligiGecerli | validPublicationID | func | internal/client/social_key.go |
| SosyalAnahtari | SocialKey | func | internal/client/social_key.go |
| SosyalOneki | SocialPrefix | func | internal/client/social_key.go |
| SahipliAnahtarVeyaURLCoz | ResolveOwnedKeyOrURL | func | internal/client/social_key.go |
| Kopyalayici | Copier | type (interface) | internal/client/copy.go |
| Kopyalayici.Kopyala | Copier.Copy | interface method | internal/client/copy.go |
| kopyaKaynagi | copySource | func | internal/client/copy.go |
| R2Client.Kopyala | R2Client.Copy | method | internal/client/copy.go |
| AnalizRequest | AnalysisRequest | type | internal/client/audio_analysis.go |
| AnalizResponse | AnalysisResponse | type | internal/client/audio_analysis.go |
| AudioAnalizci | AudioAnalyzer | type (interface) | internal/client/audio_analysis.go |
| AudioAnalizci.Analiz | AudioAnalyzer.Analyze | interface method | internal/client/audio_analysis.go |
| AudioClient.Analiz | AudioClient.Analyze | method | internal/client/audio_analysis.go |
| publicOnek | publicPrefix | const | internal/client/bucket.go |
| r2Konak | r2Host | const | internal/client/bucket.go |
| bilinenKonaklar | knownHosts | func | internal/client/bucket.go |
| OnekSilici | PrefixDeleter | type (interface) | internal/client/r2_client.go |
| sunoStemAdlari | sunoStemNames | var | internal/client/suno_client.go |
| SunoStemAdi | SunoStemName | func | internal/client/suno_client.go |
| stemAlanlari | stemFields | var | internal/client/suno_client.go |
| stemAlanlari.alan (anonymous struct field) | stemFields.field | manual: field | internal/client/suno_client.go |
| sunoHataDurumlari | sunoFailureStatuses | var | internal/client/suno_client.go |
| pollHataToleransi | pollErrorTolerance | const | internal/client/suno_client.go |
| geciciHata | transientError | type | internal/client/suno_client.go |
| geciciHata.Unwrap | transientError.Unwrap | method | internal/client/suno_client.go |
| asGecici | asTransient | func | internal/client/suno_client.go |
| SunoClient.bekle | SunoClient.waitFor | method | internal/client/suno_client.go |
| TestKullaniciAnahtari_Duzen | TestUserKey_Layout | func (test) | internal/client/user_key_test.go |
| TestKullaniciAnahtari_GecersizSegmentReddedilir | TestUserKey_RejectsInvalidSegment | func (test) | internal/client/user_key_test.go |
| TestKullaniciAnahtari_BilinmeyenTurVeDosyasizHata | TestUserKey_UnknownKindOrMissingFileErrors | func (test) | internal/client/user_key_test.go |
| TestAnahtarSahibiDogrula | TestVerifyKeyOwner | func (test) | internal/client/user_key_test.go |
| TestSahipliAnahtarCoz | TestResolveOwnedKey | func (test) | internal/client/user_key_test.go |
| TestKullaniciOnekleri | TestUserPrefixes | func (test) | internal/client/user_key_test.go |
| TestKullaniciOnekleri_GecersizID | TestUserPrefixes_InvalidID | func (test) | internal/client/user_key_test.go |
| TestAnaliz_IstekVeYanit | TestAnalyze_RequestAndResponse | func (test) | internal/client/audio_analysis_test.go |
| TestAnaliz_ServisHatasiDoner | TestAnalyze_ReturnsServiceError | func (test) | internal/client/audio_analysis_test.go |
| TestMasterRequest_AnahtarAlanlariJSON | TestMasterRequest_KeyFieldsJSON | func (test) | internal/client/audio_client_test.go |
| TestMixChannel_AdresAlaniYok | TestMixChannel_HasNoURLField | func (test) | internal/client/audio_client_test.go |
| TestKeyFromURL_TaninmayanKonak | TestKeyFromURL_UnknownHost | func (test) | internal/client/bucket_test.go |
| TestKeyFromURL_HataMesajindaImzaliURLYok | TestKeyFromURL_ErrorOmitsSignedURL | func (test) | internal/client/bucket_test.go |
| TestKeyFromURL_GidisDonus | TestKeyFromURL_RoundTrip | func (test) | internal/client/bucket_test.go |
| TestUnsignedURL_GidisDonus | TestUnsignedURL_RoundTrip | func (test) | internal/client/bucket_test.go |
| TestKeyFromURL_BosVeBozuk | TestKeyFromURL_EmptyAndMalformed | func (test) | internal/client/bucket_test.go |
| TestKeyFromURL_HatalarGecersizURLSarar | TestKeyFromURL_ErrorsWrapErrInvalidURL | func (test) | internal/client/bucket_test.go |
| groqSahte | fakeGroq | func (test helper) | internal/client/groq_client_test.go |
| TestChatCompletion_JSONModuSicaklikVeTokenGonderir | TestChatCompletion_SendsJSONModeTemperatureAndMaxTokens | func (test) | internal/client/groq_client_test.go |
| TestTemperatures_ConfigDegerleriniDoner | TestTemperatures_ReturnsConfigValues | func (test) | internal/client/groq_client_test.go |
| TestChatCompletion_SifirSicaklikGonderilir | TestChatCompletion_SendsZeroTemperature | func (test) | internal/client/groq_client_test.go |
| sahteS3 | fakeS3 | func (test helper) | internal/client/copy_test.go |
| TestKopyala_AyniBucketIcindeCopyObject | TestCopy_SameBucketUsesCopyObject | func (test) | internal/client/copy_test.go |
| TestKopyala_BucketlarArasiReddedilir | TestCopy_RejectsCrossBucket | func (test) | internal/client/copy_test.go |
| TestDeletePrefix_GuvensizOnekReddedilir | TestDeletePrefix_RejectsUnsafePrefix | func (test) | internal/client/r2_client_delete_prefix_test.go |
| TestURLFor_PublicAnahtarKaliciCDNAdresi | TestURLFor_PublicKeyPermanentCDNURL | func (test) | internal/client/r2_client_test.go |
| TestURLFor_PrivateAnahtarPresignedVeBirSaat | TestURLFor_PrivateKeyPresignedOneHour | func (test) | internal/client/r2_client_test.go |
| TestNewR2Client_EksikYapilandirma | TestNewR2Client_IncompleteConfig | func (test) | internal/client/r2_client_test.go |
| TestNewR2Client_BucketAdlariZorunlu | TestNewR2Client_RequiresBucketNames | func (test) | internal/client/r2_client_test.go |
| TestNewR2Client_PublicURLZorunlu | TestNewR2Client_RequiresPublicURL | func (test) | internal/client/r2_client_test.go |
| TestSosyalAnahtari_Duzen | TestSocialKey_Layout | func (test) | internal/client/social_key_test.go |
| TestSosyalAnahtari_GecersizSegmentReddedilir | TestSocialKey_RejectsInvalidSegment | func (test) | internal/client/social_key_test.go |
| TestSosyalOneki_UcParca | TestSocialPrefix_ThreeSegments | func (test) | internal/client/social_key_test.go |
| TestSosyalAnahtari_KullaniciSahipliDegil | TestSocialKey_NotUserOwned | func (test) | internal/client/social_key_test.go |
| TestSahipliAnahtarVeyaURLCoz | TestResolveOwnedKeyOrURL | func (test) | internal/client/social_key_test.go |
| zarfYaz | writeEnvelope | func (test helper) | internal/client/suno_client_test.go |
| TestGenerateMusic_SaglayiciSozlesmesi | TestGenerateMusic_ProviderContract | func (test) | internal/client/suno_client_test.go |
| TestGenerateMusic_ZarfHataKodu | TestGenerateMusic_EnvelopeErrorCode | func (test) | internal/client/suno_client_test.go |
| TestWaitMusic_SuccessIlkParcayiDoner | TestWaitMusic_SuccessReturnsFirstTrack | func (test) | internal/client/suno_client_test.go |
| TestWaitMusic_HataDurumuAnindaDoner | TestWaitMusic_FailureStatusReturnsImmediately | func (test) | internal/client/suno_client_test.go |
| TestWaitMusic_IkiGeciciHatayiTolereEder | TestWaitMusic_ToleratesTwoTransientErrors | func (test) | internal/client/suno_client_test.go |
| TestWaitMusic_UcArdisikHatadaVazgecer | TestWaitMusic_GivesUpAfterThreeConsecutiveErrors | func (test) | internal/client/suno_client_test.go |
| TestWaitMusic_ZamanAsimi | TestWaitMusic_Timeout | func (test) | internal/client/suno_client_test.go |
| TestSplitStems_SaglayiciSozlesmesi | TestSplitStems_ProviderContract | func (test) | internal/client/suno_client_test.go |
| TestWaitStems_EnstrumanlariEslerVokaliAtlar | TestWaitStems_MapsInstrumentsSkipsVocals | func (test) | internal/client/suno_client_test.go |
| TestWaitStems_HataBayragi | TestWaitStems_FailureFlag | func (test) | internal/client/suno_client_test.go |
| TestWaitMusic_BosParcaKimligiHata | TestWaitMusic_EmptyTrackIDErrors | func (test) | internal/client/suno_client_test.go |
| TestIsolateStem_SaglayiciSozlesmesi | TestIsolateStem_ProviderContract | func (test) | internal/client/suno_client_test.go |
| advancedYanit | advancedResponse | const (test) | internal/client/suno_client_test.go |
| TestWaitIsolated_IlkVaryantinExtractVeRemoveAdresleri | TestWaitIsolated_FirstVariantExtractAndRemoveURLs | func (test) | internal/client/suno_client_test.go |
| TestWaitIsolated_BosOriginDataHata | TestWaitIsolated_EmptyOriginDataErrors | func (test) | internal/client/suno_client_test.go |
| TestWaitIsolated_EksikAdresHata | TestWaitIsolated_MissingURLErrors | func (test) | internal/client/suno_client_test.go |
| TestWaitIsolated_HataBayragi | TestWaitIsolated_FailureFlag | func (test) | internal/client/suno_client_test.go |
| TestSunoStemAdi | TestSunoStemName | func (test) | internal/client/suno_client_test.go |
| sahteKopyalayici.Kopyala | fakeCopier.Copy | method (test fake of client.Copier) | internal/sosyal/servis_cover_test.go |
| sahteKopyalayici.Kopyala | fakeSocialCopier.Copy | method (test fake of client.Copier) | internal/worker/sosyal_worker_test.go |
| sahteAnalizci.Analiz | fakeAnalyzer.Analyze | method (test fake of client.AudioAnalyzer) | internal/worker/sosyal_worker_test.go |

Rows whose kind starts with `manual:` are skipped by `apply_renames.py`. Do each by hand:
- An **embedded field** row needs no action. Renaming the embedded type renames the field and every selector on it. Check the result in the diff.
- A **`var _ X = (*Y)(nil)`** row needs no action. The assertion follows the renamed types. Check it.
- An **anonymous struct field**, **local** or **param** row: run `~/go/bin/gopls rename -w <file>:<line>:<col> <new>` on the declaring identifier.
- A **package** row is done by the package-move step.
- A **"methods unchanged; receiver becomes ..."** row needs no action. The methods keep their `op.*` names and follow the type rename.

**Wire strings to keep (must stay byte-identical)**

_From the infra glossary, §internal/client:_

| file:line | literal | why |
|---|---|---|
| anahtar.go:28-32 | `"vocals"`, `"masters"`, `"stems"`, `"exports"`, `"previews"` (`sahipliTurler`) | R2 key prefixes (first segment). Existing objects, `files/sign` and Python `bucket_for` all depend on them. |
| anahtar.go:37 | `` `^[A-Za-z0-9_-]{1,64}(\.[a-z0-9]{1,5})?$` `` | Key segment rule. Changing it changes which keys are valid. |
| anahtar.go:55, :104 | `tur + "/" + ...`, `tur+"/"+userID+"/"` | Layout `<kind>/<userId>/...` and the account-deletion prefixes |
| sosyal_anahtar.go:15 | `"sosyal"` | R2 root for publication copies (`sosyal/{id}/`) |
| sosyal_anahtar.go:27-29 (comment), tests at sosyal_anahtar_test.go:13-15, audio_analiz_test.go:22,28,36 | `master.{ext}`, `onizleme.mp3`, `stems/{stemId}.{ext}` | Social R2 file names. The worker builds them. Do not translate `onizleme.mp3` to `preview.mp3`. |
| sosyal_anahtar.go:42, :51 | `sosyalOnek + "/" + yayinID + "/"` | Social key layout |
| bucket.go:15 | `"exports/"` | Public-bucket prefix. Python `bucket_for` uses the same rule. |
| bucket.go:33 | `"r2.cloudflarestorage.com"` | R2 host suffix |
| bucket.go:61, :63, :83, :85 | `"%s.%s.%s"`, `"%s.%s"`, `"https://%s.%s.%s/%s"`, `"https://%s.%s/%s"` | Accepted and generated R2 URL shapes |
| bucket.go:76 | `"makeasinger-private"` | Fallback bucket name in mock and dev URLs |
| r2_client.go:54, :135 | `"https://%s.r2.cloudflarestorage.com"`, `"https://%s.r2.cloudflarestorage.com/%s"` | R2 endpoint |
| r2_client.go:69 | `"auto"` | R2 region |
| kopya.go:24 | `bucket + "/" + key` | S3 `x-amz-copy-source` format |
| audio_analiz.go:9-12 | json tags `input_key`, `preview_key`, `peak_count`, `preview_seconds` | audio-service `/analiz` request. Python has `extra=forbid`. |
| audio_analiz.go:19-21 | json tags `peaks`, `duration`, `preview_url` | audio-service response |
| audio_analiz.go:35 | `"/analiz"` | audio-service endpoint path. Keep it even though the Go method becomes `Analyze`. |
| audio_client.go:34-98 | all json tags (`volume`, `pan`, `mute`, `solo`, `key`, `stem_keys`, `mix_settings`, `profile`, `vocal_takes`, `output_key`, `output_url`, `duration`, `peak_db`, `lufs`, `input_key`, `format`, `quality`, `sample_rate`, `bit_depth`, `metadata`, `size`, `files`, `filename`, `file_count`) | audio-service contract |
| audio_client.go:114, :123, :132, :140 | `"/master"`, `"/encode"`, `"/zip"`, `"/health"` | audio-service endpoints |
| audio_client.go:170; groq_client.go:115; suno_client.go:387 | `"Content-Type"`, `"application/json"` | HTTP header |
| groq_client.go:29-66 | all json tags (`role`, `content`, `model`, `messages`, `temperature`, `max_tokens`, `response_format`, `type`, `id`, `object`, `created`, `choices`, `index`, `message`, `finish_reason`, `usage`, `prompt_tokens`, `completion_tokens`, `total_tokens`) | Groq/OpenAI API |
| groq_client.go:93-94 | `"system"`, `"user"` | Chat roles |
| groq_client.go:102 | `"json_object"` | Groq JSON mode |
| groq_client.go:110 | `"/chat/completions"` | Groq endpoint |
| groq_client.go:116; suno_client.go:388 | `"Authorization"`, `"Bearer "` | HTTP auth header |
| suno_client.go:73-84 | `"Drum Kit"`, `"Bass"`, `"Piano"`, `"Guitar"`, `"Synth"`, `"String Section"`, `"Brass Section"`, `"Woodwinds"`, `"Percussion"`, `"Synth Pad"`, `"Synth Lead"`, `"Sound Effects"` | Suno `stemName` enum. Must match exactly, including case and spaces. |
| suno_client.go:101-109 | `"drumsUrl"`, `"bassUrl"`, `"guitarUrl"`, `"keyboardUrl"`, `"percussionUrl"`, `"stringsUrl"`, `"synthUrl"`, `"fxUrl"`, `"brassUrl"` | Suno `split_stem` response fields |
| suno_client.go:114-117 | `"CREATE_TASK_FAILED"`, `"GENERATE_AUDIO_FAILED"`, `"CALLBACK_EXCEPTION"`, `"SENSITIVE_WORD_ERROR"` | Suno failure statuses |
| suno_client.go:144-149 | `"customMode"`, `"instrumental"`, `"model"`, `"callBackUrl"`, `"style"`, `"title"` | Suno generate request keys |
| suno_client.go:152, 210, 270 | json tag `taskId` | Suno |
| suno_client.go:154 | `"/api/v1/generate"` | Suno endpoint |
| suno_client.go:168-176 | json tags `status`, `errorMessage`, `response`, `sunoData`, `id`, `audio_url`, `duration` | Suno record-info |
| suno_client.go:178 | `"/api/v1/generate/record-info?taskId="` | Suno endpoint |
| suno_client.go:184, 237, 307 | `"SUCCESS"` | Suno status |
| suno_client.go:204-207, 263-267 | `"taskId"`, `"audioId"`, `"type"`, `"callBackUrl"`, `"stemName"`, `"split_stem"`, `"split_stem_advanced"` | Suno vocal-removal request |
| suno_client.go:212, 272 | `"/api/v1/vocal-removal/generate"` | Suno endpoint |
| suno_client.go:227-229, 286-298 | json tags `successFlag`, `errorMessage`, `response`, `audio_url`, `duration`, `originData`, `extract`, `remove` | Suno |
| suno_client.go:231, 301 | `"/api/v1/vocal-removal/record-info?taskId="` | Suno endpoint |
| suno_client.go:337, 400 | `"[Suno API] ..."` log prefix | Not wire, but ops log greps may depend on it. The Turkish text after the prefix may be translated. |
| suno_client.go:407-409 | json tags `code`, `msg`, `data` | Suno envelope |

_From the sosyal glossary, §4.3 R2 key layout:_

| file:line | literal | why |
|---|---|---|
| the `client.SosyalAnahtari` / `client.SosyalOneki` prefix `sosyal/` | (lives in internal/client/sosyal_anahtar.go, out of scope) | R2 layout `sosyal/{id}/` |

**Risks (from the glossaries; names in them are the old ones)**

_From the infra glossary, risk 1:_

1. **Error text that reaches clients and tests.**
   - Error strings defined in this scope are sent to users without passing through an error code:
     - `model.ErrGecersizStemAyari` ("gecersiz stem ayari: ...") goes straight into the API `message` (`handler/render.go:57`).
     - Errors from `client` (Suno errors, `ErrGecersizURL`, `ErrYabanciAnahtar`, "bucket'lar arasi kopya ...") end up in `job.Error` through `worker.failJob(err.Error())`. Clients then see them in status responses and WebSocket `error` messages.
     - The brief treats messages as non-wire and has them localized separately. Translating these strings is allowed, but it changes visible text, so decide that on purpose.
   - Tests that match error substrings must change in the same commit as the strings they match:
     - `suno_client_test.go:136` matches `"zaman asimi"` from `suno_client.go:353`.
     - `suno_client_test.go:220` matches `"kimligi"` from `suno_client.go:192`.
     - `suno_client_test.go:101, 208, 307` match `"yasakli kelime"`, `"bolunemedi"` and `"katalog eslesmesi"`. Those come from the fake provider's `errorMessage`, so they are safe as long as the fake and the assertion stay in sync.
     - `bucket_test.go:98` checks for the absence of `cokgizli` and `X-Amz-Signature`.
   - The user-facing Turkish middleware messages ("Kimlik dogrulanamadi", "Hesap silindi", "Hesap durumu denetlenemedi", "Rate limit denetlenemedi") belong to the localization task. Leave them unless that task says otherwise.

_From the infra glossary, risk 2:_

2. **Interface renames break fakes in other packages, and the compiler only catches some of them.**
   - Implementations that must be renamed in the same change:
     - `Checker.IsDeleted` (was `Denetci.Silinmis`): `internal/worker/render_worker_test.go:381` (`sahteDenetci.Silinmis`).
     - `Copier.Copy` (was `Kopyala`): `internal/sosyal/servis_cover_test.go:17` and `internal/worker/sosyal_worker_test.go:58`.
     - `AudioAnalyzer.Analyze` (was `Analiz`): `internal/worker/sosyal_worker_test.go:72`.
   - `PrefixDeleter.DeletePrefix` keeps its name, so the fakes in `service/hesap_service_test.go:32`, `service/hesap_sosyal_test.go:46`, `worker/hesap_worker_test.go:14` and `worker/sosyal_worker_test.go:310` are unaffected.
   - The type-name renames are compile errors, so they are caught. A fake whose method is not renamed can still compile if it is only stored as a concrete type, so grep for the old names.

_From the infra glossary, risk 4:_

4. **The R2, Redis and header literals sit next to identifiers being renamed, so they are easy to translate by accident.**
   - `sosyalOnek = "sosyal"`: rename the const (to `socialRoot`) but keep the value.
   - The `onizleme.mp3` file name stays (see section 4).
   - `onek = "kimlik:silindi:"`: the const becomes `prefix` and the value stays.
   - `WebAnahtariBasligi = "X-Web-Anahtari"`: the const becomes `WebKeyHeader` and the value stays.
   - `"/analiz"` stays even though the method becomes `Analyze`.
   - `ratelimit:` key formats and per-route prefixes stay, including the callers' `"sosyal-yayin"`, `"sosyal-yorum"` and `"sosyal-cover"` and the `"yavasla"` code.
   - The `sahipliTurler` list (`vocals`, `masters`, `stems`, `exports`, `previews`) and `publicOnek = "exports/"` must stay identical to Python `bucket_for`.
   - The executor should diff every string literal in these files before and after the rename.

_From the infra glossary, risk 5:_

5. **Identifiers used from other packages, and naming collisions.**
   - Exported `client` names used elsewhere:
     - `ErrGecersizSegment`: handler/upload.go, service tests.
     - `ErrGecersizURL` and `ErrYabanciAnahtar`: handler/export.go, files.go, master.go, service tests, sosyal/servis_yayin.go.
     - `KullaniciAnahtari`: service/{export,master,upload}_service.go, sosyal/servis_cover.go, worker/{master,render}_worker.go, `e2e/testhelper_test.go`.
     - `SahipliAnahtarCoz`: service/{export,file,master}_service.go, worker/master_worker.go.
     - `KullaniciOnekleri`: service/hesap_service.go.
     - `SosyalAnahtari`: sosyal/anahtarlar.go.
     - `SosyalOneki`: service/hesap_service.go, worker/sosyal_worker.go.
     - `SahipliAnahtarVeyaURLCoz`: sosyal/servis_yayin.go.
     - `Kopyalayici`: cmd/server/main.go, sosyal/servis*.go, worker/sosyal_worker*.go.
     - `OnekSilici`: cmd/server/main.go, service/hesap_service.go, worker/sosyal_worker.go.
     - `AnalizRequest`, `AnalizResponse` and `AudioAnalizci`: worker/sosyal_worker*.go.
     - `SunoStemAdi`: worker/render_worker.go.
   - Other packages:
     - `auth`: `EsnekBool` (kimlik/baglama_sayfa_test.go), and `YukariAkisDogrulayici`, `NewYukariAkisDogrulayici` and `DogrulaNonce` (kimlik/sunucu.go, federe_sayfa.go, yukari_akis*.go and their tests).
     - `middleware`: `LimitKod`, `SilinmisKullaniciEngeli` and `WebAnahtari*` (cmd/server/main.go:268-315).
     - `hesapisaret`: `Anahtar`, `Denetci`, `NewRedisDenetci` and `Omur` (cmd/server/main.go, service/hesap_service*.go, worker/{master,render,sosyal}_worker*.go, middleware). The import path also changes to `internal/deletionmark`.
     - `model`: `SahibiMi` (service/job_kayit.go), `ErrGecersizStemAyari` (handler/render.go, service/sahiplik_canli_test.go) and `StemAyarlari` (service/render_service.go).
   - Collisions avoided on purpose:
     - The `sosyalOnek` const becomes `socialRoot`, not `socialPrefix`, so it does not shadow the exported `SocialPrefix` in the reader's eye.
     - `RateLimiter.say` becomes `hit`, not `count`, because the body has `count, err := artir.Result()`.
     - `ErrYabanciAnahtar` becomes `ErrKeyNotOwned`, not `ErrForeignKey`, which would read as a DB term.
     - In package `middleware`, the test helpers `newApp`, `liveApp`, `ipApp`, `limitedApp` and `gatedApp` are all distinct.
     - The const `testIssuer` exists in both the `auth` and `middleware` tests, which is fine because they are separate packages.
   - The kimlik package has its own unrelated `kodDogrulayici` (PKCE verifier). The identity glossary should translate it to `codeVerifier`, which does not clash with `opVerifier` or `UpstreamVerifier`.

_From the sosyal glossary, risk 2:_

2. **Out-of-scope `client` and `hesapisaret` identifiers drive method names in our fakes.** The social service and worker depend on `client.Kopyalayici` (method `Kopyala`), `client.AudioAnalizci` (method `Analiz`, `AnalizRequest`, `AnalizResponse`), `client.OnekSilici`, `client.SosyalAnahtari`, `client.SosyalOneki`, `client.KullaniciAnahtari`, `client.SahipliAnahtarVeyaURLCoz`, `client.ErrYabanciAnahtar`, `client.ResolveURL`, `client.UnsignedURL`, `client.IsPublicKey` and `hesapisaret.Denetci` (which becomes `deletionmark.*`). The worker also uses `sahipYazabilir` and `errSahipSilindi` from `worker/render_worker.go`, and the handler uses `istekSahibi` (handler/istek_sahibi.go) and `kullaniciOlarak` (handler/sahiplik_canli_test.go). The tests use `kimlik.Baglan` (which becomes `identity.*`). The fakes `fakeCopier.Kopyala`, `fakeSocialCopier.Kopyala` and `fakeAnalyzer.Analiz` must take whatever names the client glossary chooses, or they stop satisfying the interfaces. That failure is silent until compile.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Rename the files**

```bash
git mv internal/client/anahtar.go internal/client/user_key.go
git mv internal/client/anahtar_test.go internal/client/user_key_test.go
git mv internal/client/audio_analiz.go internal/client/audio_analysis.go
git mv internal/client/audio_analiz_test.go internal/client/audio_analysis_test.go
git mv internal/client/kopya.go internal/client/copy.go
git mv internal/client/kopya_test.go internal/client/copy_test.go
git mv internal/client/onek_sil_test.go internal/client/r2_client_delete_prefix_test.go
git mv internal/client/sosyal_anahtar.go internal/client/social_key.go
git mv internal/client/sosyal_anahtar_test.go internal/client/social_key_test.go
```

- [ ] **Step 3: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 5 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 4: Do the `manual:` rows**

Follow the rules under the table. Then run:

```bash
go build ./... && go vet ./...
```

- [ ] **Step 5: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- The fakes `sahteKopyalayici.Kopyala` (social and worker tests) and `sahteAnalizci.Analiz` (worker tests) are renamed by the interface renames in this table. Their type names follow in Tasks 15 and 18.

Files (19): `internal/client/audio_analysis.go`, `internal/client/audio_analysis_test.go`, `internal/client/audio_client.go`, `internal/client/audio_client_test.go`, `internal/client/bucket.go`, `internal/client/bucket_test.go`, `internal/client/copy.go`, `internal/client/copy_test.go`, `internal/client/groq_client.go`, `internal/client/groq_client_test.go`, `internal/client/r2_client.go`, `internal/client/r2_client_delete_prefix_test.go`, `internal/client/r2_client_test.go`, `internal/client/social_key.go`, `internal/client/social_key_test.go`, `internal/client/suno_client.go`, `internal/client/suno_client_test.go`, `internal/client/user_key.go`, `internal/client/user_key_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/client/audio_analysis.go internal/client/audio_analysis_test.go internal/client/audio_client.go internal/client/audio_client_test.go internal/client/bucket.go internal/client/bucket_test.go internal/client/copy.go internal/client/copy_test.go internal/client/groq_client.go internal/client/groq_client_test.go internal/client/r2_client.go internal/client/r2_client_delete_prefix_test.go internal/client/r2_client_test.go internal/client/social_key.go internal/client/social_key_test.go internal/client/suno_client.go internal/client/suno_client_test.go internal/client/user_key.go internal/client/user_key_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 6: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 5 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 7: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 8: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(client): Ingilizce adlandirma

client paketinin tanimlayicilari, dosya adlari, testleri ve yorumlari
Ingilizceye cevrildi (Kopyalayici -> Copier, OnekSilici -> PrefixDeleter,
KullaniciAnahtari -> UserKey ...). R2 anahtar duzeni ve hata metinleri
ayni; kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 6: `middleware`

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/middleware/auth.go` | (unchanged) |
| `internal/middleware/auth_test.go` | (unchanged) |
| `internal/middleware/ratelimit.go` | (unchanged) |
| `internal/middleware/ratelimit_canli_test.go` | `internal/middleware/ratelimit_live_test.go` |
| `internal/middleware/ratelimit_ip_test.go` | (unchanged) |
| `internal/middleware/ratelimit_test.go` | (unchanged) |
| `internal/middleware/silinmis.go` | `internal/middleware/deleted_user.go` |
| `internal/middleware/silinmis_test.go` | `internal/middleware/deleted_user_test.go` |

**Declarations** (71 rows)

<!-- renames:6 -->
| old | new | kind | file |
|---|---|---|---|
| opDogrulayici | opVerifier | type | internal/middleware/auth.go |
| opDogrulayici.acik | opVerifier.publicKey | field | internal/middleware/auth.go |
| opDogrulayici.istemci | opVerifier.clientID | field | internal/middleware/auth.go |
| opDogrulayici.kurulumHatasi | opVerifier.setupErr | field | internal/middleware/auth.go |
| opDogrulayici.dogrula | opVerifier.verify | method | internal/middleware/auth.go |
| RateLimiter.LimitKod | RateLimiter.LimitWithCode | method | internal/middleware/ratelimit.go |
| RateLimiter.kullaniciBasina | RateLimiter.perUser | method | internal/middleware/ratelimit.go |
| WebAnahtariBasligi | WebKeyHeader | const | internal/middleware/ratelimit.go |
| istemciIP | clientIP | func | internal/middleware/ratelimit.go |
| ipAnahtari | ipKey | func | internal/middleware/ratelimit.go |
| RateLimiter.say | RateLimiter.hit | method | internal/middleware/ratelimit.go |
| SilinmisKullaniciEngeli | DeletedUserGate | func | internal/middleware/deleted_user.go |
| denemeIssuer | testIssuer | const (test) | internal/middleware/auth_test.go |
| denemeIstemci | testClientID | const (test) | internal/middleware/auth_test.go |
| denemeAnahtar | testKey | func (test helper) | internal/middleware/auth_test.go |
| jetonUretTest | signTestToken | func (test helper) | internal/middleware/auth_test.go |
| uygulama | newApp | func (test helper) | internal/middleware/auth_test.go |
| uygulamaMW | newAppWithMiddleware | func (test helper) | internal/middleware/auth_test.go |
| istek | doRequest | func (test helper) | internal/middleware/auth_test.go |
| TestGecerliJetonKabulEdilir | TestAuthenticate_AcceptsValidToken | func (test) | internal/middleware/auth_test.go |
| TestExpsizJetonReddedilir | TestAuthenticate_RejectsTokenWithoutExp | func (test) | internal/middleware/auth_test.go |
| TestSuresiGecmisJetonReddedilir | TestAuthenticate_RejectsExpiredToken | func (test) | internal/middleware/auth_test.go |
| TestBaskaAnahtarlaImzaliJetonReddedilir | TestAuthenticate_RejectsTokenSignedByOtherKey | func (test) | internal/middleware/auth_test.go |
| TestYanlisIssuerReddedilir | TestAuthenticate_RejectsWrongIssuer | func (test) | internal/middleware/auth_test.go |
| TestIssizJetonReddedilir | TestAuthenticate_RejectsTokenWithoutIssuer | func (test) | internal/middleware/auth_test.go |
| TestAlgNoneReddedilir | TestAuthenticate_RejectsAlgNone | func (test) | internal/middleware/auth_test.go |
| TestHS256AlgKarisikligiReddedilir | TestAuthenticate_RejectsHS256AlgConfusion | func (test) | internal/middleware/auth_test.go |
| TestEksikVeBozukHeader | TestAuthenticate_MissingOrMalformedHeader | func (test) | internal/middleware/auth_test.go |
| TestSubBossaReddedilir | TestAuthenticate_RejectsEmptySub | func (test) | internal/middleware/auth_test.go |
| TestGelecekNbfReddedilir | TestAuthenticate_RejectsFutureNbf | func (test) | internal/middleware/auth_test.go |
| TestGelecekIatReddedilir | TestAuthenticate_RejectsFutureIat | func (test) | internal/middleware/auth_test.go |
| TestYanlisAudReddedilir | TestAuthenticate_RejectsWrongAud | func (test) | internal/middleware/auth_test.go |
| TestAudsuzJetonReddedilir | TestAuthenticate_RejectsTokenWithoutAud | func (test) | internal/middleware/auth_test.go |
| TestDogruAudKabulEdilir | TestAuthenticate_AcceptsMatchingAud | func (test) | internal/middleware/auth_test.go |
| TestIDTokenAccessTokenOlarakReddedilir | TestAuthenticate_RejectsIDTokenAsAccessToken | func (test) | internal/middleware/auth_test.go |
| TestSubBoslukIseReddedilir | TestAuthenticate_RejectsWhitespaceSub | func (test) | internal/middleware/auth_test.go |
| TestEksikYapilandirmadaHerIstekReddedilir | TestNewOPAuthMiddleware_IncompleteConfigRejectsAll | func (test) | internal/middleware/auth_test.go |
| TestOPsuzMiddlewareHerIstegiReddeder | TestAuthenticate_ZeroValueMiddlewareRejectsAll | func (test) | internal/middleware/auth_test.go |
| canliRedis | liveRedis | func (test helper) | internal/middleware/ratelimit_live_test.go |
| canliUygulama | liveApp | func (test helper) | internal/middleware/ratelimit_live_test.go |
| canliYanit | liveResponse | type (test) | internal/middleware/ratelimit_live_test.go |
| canliYanit.durum | liveResponse.status | field (test) | internal/middleware/ratelimit_live_test.go |
| canliYanit.kalan | liveResponse.remaining | field (test) | internal/middleware/ratelimit_live_test.go |
| canliIstek | liveRequest | func (test helper) | internal/middleware/ratelimit_live_test.go |
| TestCanliMutluYolVeSayac | TestLiveLimit_HappyPathAndCounter | func (test) | internal/middleware/ratelimit_live_test.go |
| TestCanliPencereSabitTTLYenilenmez | TestLiveLimit_FixedWindowTTLNotRenewed | func (test) | internal/middleware/ratelimit_live_test.go |
| TestCanliPencereDoluncaSayacSifirlanir | TestLiveLimit_CounterResetsAfterWindow | func (test) | internal/middleware/ratelimit_live_test.go |
| TestCanliMirasTTLsizAnahtarIyilestirilir | TestLiveLimit_HealsLegacyKeyWithoutTTL | func (test) | internal/middleware/ratelimit_live_test.go |
| TestCanliWSTicketVeSignLimit | TestLiveLimit_WSTicketAndSign | func (test) | internal/middleware/ratelimit_live_test.go |
| testWebAnahtari | testWebKey | const (test) | internal/middleware/ratelimit_ip_test.go |
| ipUygulamasi | ipApp | func (test helper) | internal/middleware/ratelimit_ip_test.go |
| ipIstek | ipRequest | func (test helper) | internal/middleware/ratelimit_ip_test.go |
| TestLimitIP_RedisYoksaFailClosed | TestLimitIP_FailsClosedWithoutRedis | func (test) | internal/middleware/ratelimit_ip_test.go |
| TestCanliLimitIP_IPBasinaSayar | TestLiveLimitIP_CountsPerIP | func (test) | internal/middleware/ratelimit_ip_test.go |
| TestCanliLimitIP_GecersizBaslikBaglantiAdresineDuser | TestLiveLimitIP_InvalidHeaderFallsBackToConnIP | func (test) | internal/middleware/ratelimit_ip_test.go |
| TestCanliLimitIP_WebAnahtariOrtakSinir | TestLiveLimitIP_WebKeySharedLimit | func (test) | internal/middleware/ratelimit_ip_test.go |
| TestCanliLimitIP_BosYapilandirmaWebYolunuKapatir | TestLiveLimitIP_EmptyConfigDisablesWebPath | func (test) | internal/middleware/ratelimit_ip_test.go |
| TestCanliLimitKod_OzelKodla429 | TestLiveLimitWithCode_429WithCustomCode | func (test) | internal/middleware/ratelimit_ip_test.go |
| TestLimitKod_KimliksizIstek401 | TestLimitWithCode_UnauthenticatedIs401 | func (test) | internal/middleware/ratelimit_ip_test.go |
| TestIPAnahtari_IPv6Ust64VeIPv4Esleme | TestIPKey_IPv6Upper64AndIPv4Mapped | func (test) | internal/middleware/ratelimit_ip_test.go |
| TestCanliLimitIP_IPv6Ayni64OrtakSayac | TestLiveLimitIP_IPv6Same64SharesCounter | func (test) | internal/middleware/ratelimit_ip_test.go |
| TestCanliLimitIP_IPv4EslemeliAyniSayac | TestLiveLimitIP_IPv4MappedSharesCounter | func (test) | internal/middleware/ratelimit_ip_test.go |
| sinirliUygulama | limitedApp | func (test helper) | internal/middleware/ratelimit_test.go |
| sinirDurum | limitStatus | func (test helper) | internal/middleware/ratelimit_test.go |
| TestUserIDBossaRateLimitFailClosed | TestLimit_FailsClosedWithoutUserID | func (test) | internal/middleware/ratelimit_test.go |
| TestRedisHatasindaFailClosed | TestLimit_FailsClosedOnRedisError | func (test) | internal/middleware/ratelimit_test.go |
| engelliUygulama | gatedApp | func (test helper) | internal/middleware/deleted_user_test.go |
| engelDurum | gateStatus | func (test helper) | internal/middleware/deleted_user_test.go |
| TestSilinmisEngeli_RedisYoksa503 | TestDeletedUserGate_503WithoutRedis | func (test) | internal/middleware/deleted_user_test.go |
| TestSilinmisEngeli_UserIDBossa401 | TestDeletedUserGate_401WithoutUserID | func (test) | internal/middleware/deleted_user_test.go |
| TestSilinmisEngeli_IsaretliKullanici401 | TestDeletedUserGate_401ForMarkedUser | func (test) | internal/middleware/deleted_user_test.go |

**Wire strings to keep (must stay byte-identical)**

_From the infra glossary, §internal/middleware:_

| file:line | literal | why |
|---|---|---|
| auth.go:111-128 | claim names `"jti"`, `"azp"`, `"at_hash"`, `"sub"`, `"email"`, `"name"` | JWT claims |
| auth.go:135 | `"Authorization"` | HTTP header |
| auth.go:141 | `"bearer"` | Auth scheme |
| auth.go:137, 142, 151, 156 | `"Missing authorization header"`, `"Invalid authorization header format"`, `"Invalid or expired token"` | User-visible `message` (already English; localization is a separate task) |
| auth.go:158-160, 167, 175, 183 | Locals keys `"userId"`, `"email"`, `"name"` | String-keyed context. Tests set them directly: `internal/handler/sahiplik_canli_test.go:22` and middleware tests. |
| ratelimit.go:49 | `"ratelimit:%s:%s"` | Redis key, per user |
| ratelimit.go:57 | `"X-Web-Anahtari"` | HTTP header sent by make-singer-frontend. Keep it even though the const becomes `WebKeyHeader`. |
| ratelimit.go:73 | `"ratelimit:%s:web"` | Redis key, shared web counter |
| ratelimit.go:77 | `"ratelimit:%s:ip:%s"` | Redis key, per IP |
| ratelimit.go:90 | `"X-Real-Ip"` | Header set by Traefik |
| ratelimit.go:124 | `"NX"` | Redis SET mode |
| ratelimit.go:133, 139, 167; silinmis.go:22 | `"Retry-After"`, `"5"` | HTTP header and value |
| ratelimit.go:135, 141 | `"Rate limit denetlenemedi"` | User-visible 503 `message` (left for the localization task, see Risks) |
| ratelimit.go:47; silinmis.go:18 | `"Kimlik dogrulanamadi"` | User-visible 401 `message` (localization task) |
| silinmis.go:24, :27 | `"Hesap durumu denetlenemedi"`, `"Hesap silindi"` | User-visible `message` (localization task) |
| ratelimit.go:172-173 | `"X-RateLimit-Limit"`, `"X-RateLimit-Remaining"` | Response headers |
| ratelimit.go:180, 185, 190, 195, 200, 205, 211 | `"lyrics"`, `"render"`, `"master"`, `"export"`, `"upload"`, `"wsticket"`, `"sign"` | Redis rate-limit key prefixes |
| (caller, outside scope) cmd/server/main.go:267, 314-317 | `"public"`, `"sosyal-yayin"`, `"sosyal-yorum"`, `"sosyal-cover"`, plus `LimitKod(..., "yavasla", ...)` | Redis key prefixes and the error code passed into this package |
| ratelimit_*_test.go | `"ratelimit:mutlu:%s"`, `"ratelimit:onizleme:ip:..."`, `"ratelimit:onizleme:web"`, `"ratelimit:yorum:..."`, `"yavasla"`, `"RATE_LIMITED"`, `"X-Real-Ip"` | Tests assert the key formats and codes. The fixture prefixes (`mutlu`, `onizleme`, `yorum`) are test-only and may be translated, but only together with the assertions that use them. |

**Risks (from the glossaries; names in them are the old ones)**

_From the infra glossary, risk 4:_

4. **The R2, Redis and header literals sit next to identifiers being renamed, so they are easy to translate by accident.**
   - `sosyalOnek = "sosyal"`: rename the const (to `socialRoot`) but keep the value.
   - The `onizleme.mp3` file name stays (see section 4).
   - `onek = "kimlik:silindi:"`: the const becomes `prefix` and the value stays.
   - `WebAnahtariBasligi = "X-Web-Anahtari"`: the const becomes `WebKeyHeader` and the value stays.
   - `"/analiz"` stays even though the method becomes `Analyze`.
   - `ratelimit:` key formats and per-route prefixes stay, including the callers' `"sosyal-yayin"`, `"sosyal-yorum"` and `"sosyal-cover"` and the `"yavasla"` code.
   - The `sahipliTurler` list (`vocals`, `masters`, `stems`, `exports`, `previews`) and `publicOnek = "exports/"` must stay identical to Python `bucket_for`.
   - The executor should diff every string literal in these files before and after the rename.

_From the infra glossary, risk 5:_

5. **Identifiers used from other packages, and naming collisions.**
   - Exported `client` names used elsewhere:
     - `ErrGecersizSegment`: handler/upload.go, service tests.
     - `ErrGecersizURL` and `ErrYabanciAnahtar`: handler/export.go, files.go, master.go, service tests, sosyal/servis_yayin.go.
     - `KullaniciAnahtari`: service/{export,master,upload}_service.go, sosyal/servis_cover.go, worker/{master,render}_worker.go, `e2e/testhelper_test.go`.
     - `SahipliAnahtarCoz`: service/{export,file,master}_service.go, worker/master_worker.go.
     - `KullaniciOnekleri`: service/hesap_service.go.
     - `SosyalAnahtari`: sosyal/anahtarlar.go.
     - `SosyalOneki`: service/hesap_service.go, worker/sosyal_worker.go.
     - `SahipliAnahtarVeyaURLCoz`: sosyal/servis_yayin.go.
     - `Kopyalayici`: cmd/server/main.go, sosyal/servis*.go, worker/sosyal_worker*.go.
     - `OnekSilici`: cmd/server/main.go, service/hesap_service.go, worker/sosyal_worker.go.
     - `AnalizRequest`, `AnalizResponse` and `AudioAnalizci`: worker/sosyal_worker*.go.
     - `SunoStemAdi`: worker/render_worker.go.
   - Other packages:
     - `auth`: `EsnekBool` (kimlik/baglama_sayfa_test.go), and `YukariAkisDogrulayici`, `NewYukariAkisDogrulayici` and `DogrulaNonce` (kimlik/sunucu.go, federe_sayfa.go, yukari_akis*.go and their tests).
     - `middleware`: `LimitKod`, `SilinmisKullaniciEngeli` and `WebAnahtari*` (cmd/server/main.go:268-315).
     - `hesapisaret`: `Anahtar`, `Denetci`, `NewRedisDenetci` and `Omur` (cmd/server/main.go, service/hesap_service*.go, worker/{master,render,sosyal}_worker*.go, middleware). The import path also changes to `internal/deletionmark`.
     - `model`: `SahibiMi` (service/job_kayit.go), `ErrGecersizStemAyari` (handler/render.go, service/sahiplik_canli_test.go) and `StemAyarlari` (service/render_service.go).
   - Collisions avoided on purpose:
     - The `sosyalOnek` const becomes `socialRoot`, not `socialPrefix`, so it does not shadow the exported `SocialPrefix` in the reader's eye.
     - `RateLimiter.say` becomes `hit`, not `count`, because the body has `count, err := artir.Result()`.
     - `ErrYabanciAnahtar` becomes `ErrKeyNotOwned`, not `ErrForeignKey`, which would read as a DB term.
     - In package `middleware`, the test helpers `newApp`, `liveApp`, `ipApp`, `limitedApp` and `gatedApp` are all distinct.
     - The const `testIssuer` exists in both the `auth` and `middleware` tests, which is fine because they are separate packages.
   - The kimlik package has its own unrelated `kodDogrulayici` (PKCE verifier). The identity glossary should translate it to `codeVerifier`, which does not clash with `opVerifier` or `UpstreamVerifier`.

_From the app glossary, risk R7:_

**R7. String-keyed Fiber locals.** handler/sahiplik_canli_test.go:22 writes `c.Locals("userId", ...)` by string, and `middleware.GetUserID` must read the same key. If the middleware glossary renames that locals key or wraps it in a constant, this test fake must follow. The value is not on the wire, but it is a string-based lookup.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Rename the files**

```bash
git mv internal/middleware/ratelimit_canli_test.go internal/middleware/ratelimit_live_test.go
git mv internal/middleware/silinmis.go internal/middleware/deleted_user.go
git mv internal/middleware/silinmis_test.go internal/middleware/deleted_user_test.go
```

- [ ] **Step 3: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 6 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 4: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).

Files (8): `internal/middleware/auth.go`, `internal/middleware/auth_test.go`, `internal/middleware/deleted_user.go`, `internal/middleware/deleted_user_test.go`, `internal/middleware/ratelimit.go`, `internal/middleware/ratelimit_ip_test.go`, `internal/middleware/ratelimit_live_test.go`, `internal/middleware/ratelimit_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/middleware/auth.go internal/middleware/auth_test.go internal/middleware/deleted_user.go internal/middleware/deleted_user_test.go internal/middleware/ratelimit.go internal/middleware/ratelimit_ip_test.go internal/middleware/ratelimit_live_test.go internal/middleware/ratelimit_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 5: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 6 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 6: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 7: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(middleware): Ingilizce adlandirma

middleware tanimlayicilari, dosya adlari ve testleri Ingilizceye
cevrildi (LimitKod -> LimitWithCode, SilinmisKullaniciEngeli ->
DeletedUserGate). Redis onekleri ve basliklar ayni; kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 7: identity prep: explicit JSON tags on Redis-stored structs

This is **not** a rename task. It changes no identifier. It makes the later field renames safe: identity glossary risk R1. Every Turkish-named field of a struct that is stored in Redis gets a `json` tag with its **current** name, so the JSON key stays the same after Task 11 renames the field.

**Files**
- Modify: `internal/kimlik/istek.go` (`AuthIstek.IpucuSubject`)
- Modify: `internal/kimlik/oturum.go` (`OturumBaglama.Kayitlar`)
- Modify: `internal/kimlik/yukari_akis.go` (`FedereDurum.Saglayici`, `.Cerez`, `.KodDogrulayici`)
- Modify: `internal/kimlik/baglama_sayfa.go` (`BaglamaBekleme.Saglayici`, `.Eposta`)
- Create: `internal/kimlik/redis_json_test.go` (moves to `internal/identity/redis_json_test.go` in Task 8)
- Modify: `tools/wirecheck/testdata/wire.txt` (regenerated; exactly 7 added `TAG` lines)

Fields that are already English (`AuthRequestID`, `CSRF`, `Nonce`, `Sub`, `UserID`, `ID`, `ClientID`, ...) keep their names, so their keys never change. They need no tag. `OturumKayit` and `AccessKayit` have English field names only. The test pins their keys anyway.

**Redis JSON keys today (verified by marshalling zero values at `de6c92b`)**

- `AuthIstek`: `ID`, `ClientID`, `RedirectURI`, `Scopes`, `ResponseType`, `ResponseMode`, `State`, `Nonce`, `CodeChallenge`, `CodeChallengeMethod`, **`IpucuSubject`**, `Subject`, `AuthTime`, `AMR`
- `OturumBaglama`: **`Kayitlar`**, which holds `[{AuthRequestID, CSRF}]`
- `FedereDurum`: `AuthRequestID`, `Nonce`, **`Saglayici`**, **`Cerez`**, **`KodDogrulayici`**
- `BaglamaBekleme`: `AuthRequestID`, `UserID`, **`Saglayici`**, `Sub`, **`Eposta`**, `CSRF`
- `AccessKayit`: `ID`, `UserID`, `ClientID`, `Scopes`, `ExpiresAt`

The bold names are Turkish. They need explicit `json:"<old name>"` tags when renamed (§3, R1).

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Write the key-pinning test**

Create `internal/kimlik/redis_json_test.go`:

```go
package kimlik

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// jsonKeys returns the sorted top-level JSON keys of v.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %T: %v", v, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestRedisJSON_KeysUnchanged pins the JSON keys of every struct that is
// stored in Redis. Renaming a Go field must not change them: in-flight
// logins, federation callbacks and pending links would break during a
// rolling deploy.
func TestRedisJSON_KeysUnchanged(t *testing.T) {
	tests := []struct {
		v    any
		want []string
	}{
		{AuthIstek{}, []string{"AMR", "AuthTime", "ClientID", "CodeChallenge", "CodeChallengeMethod", "ID", "IpucuSubject", "Nonce", "RedirectURI", "ResponseMode", "ResponseType", "Scopes", "State", "Subject"}},
		{OturumBaglama{}, []string{"Kayitlar"}},
		{OturumKayit{}, []string{"AuthRequestID", "CSRF"}},
		{FedereDurum{}, []string{"AuthRequestID", "Cerez", "KodDogrulayici", "Nonce", "Saglayici"}},
		{BaglamaBekleme{}, []string{"AuthRequestID", "CSRF", "Eposta", "Saglayici", "Sub", "UserID"}},
		{AccessKayit{}, []string{"ClientID", "ExpiresAt", "ID", "Scopes", "UserID"}},
	}
	for _, tt := range tests {
		if got := jsonKeys(t, tt.v); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%T JSON keys = %v, want %v", tt.v, got, tt.want)
		}
	}
}
```

```bash
go test -count=1 -run TestRedisJSON_KeysUnchanged ./internal/kimlik
```

Expected: `ok`. The test passes before the tags exist, because the keys are the Go field names today.

- [ ] **Step 3: Add the tags**

```bash
cd internal/kimlik
perl -0pi -e 's/(type AuthIstek struct \{.*?\n\tIpucuSubject string)\n/$1 `json:"IpucuSubject"`\n/s' istek.go
perl -0pi -e 's/(type OturumBaglama struct \{\n\tKayitlar \[\]OturumKayit)\n/$1 `json:"Kayitlar"`\n/s' oturum.go
perl -0pi -e 's/(type FedereDurum struct \{.*?\n\tSaglayici string)\n/$1 `json:"Saglayici"`\n/s; s/(type FedereDurum struct \{.*?\n\tCerez string)\n/$1 `json:"Cerez"`\n/s; s/(type FedereDurum struct \{.*?\n\tKodDogrulayici string)\n/$1 `json:"KodDogrulayici"`\n/s' yukari_akis.go
perl -0pi -e 's/(type BaglamaBekleme struct \{.*?\n\tSaglayici string)\n/$1 `json:"Saglayici"`\n/s; s/(type BaglamaBekleme struct \{.*?\n\tEposta string)\n/$1 `json:"Eposta"`\n/s' baglama_sayfa.go
gofmt -w istek.go oturum.go yukari_akis.go baglama_sayfa.go
cd ../..
git diff --stat
go test -count=1 -run TestRedisJSON_KeysUnchanged ./internal/kimlik
```

Expected: `4 files changed, 7 insertions(+), 7 deletions(-)`, and the test prints `ok`. Each changed line is one of the seven fields above, with only the tag appended.

- [ ] **Step 4: Review and record the snapshot change**

```bash
go test -count=1 ./tools/wirecheck 2>&1 | grep -E '^\s+[-+](STR|TAG|ROUTE|FILE)'
```

Expected, exactly:

```
        +TAG	json:"Cerez"
        +TAG	json:"Eposta"
        +TAG	json:"IpucuSubject"
        +TAG	json:"Kayitlar"
        +TAG	json:"KodDogrulayici"
        +TAG	json:"Saglayici"
        +TAG	json:"Saglayici"
```

**The reviewer checks:** there are 7 added `TAG` lines, nothing is removed, and each key equals the Go field name that carried it. Then:

```bash
go test ./tools/wirecheck -update
wc -l tools/wirecheck/testdata/wire.txt
```

Expected: `2148 tools/wirecheck/testdata/wire.txt` (2141 + 7).

- [ ] **Step 5: Verify**

Run the VERIFY block from Global Constraints. The guard passes after the `-update` of Step 4.

- [ ] **Step 6: Commit**

```bash
git status --short
git add internal/kimlik/redis_json_test.go
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
fix(kimlik): Redis'e yazilan yapilara acik JSON etiketleri

AuthIstek, OturumBaglama, FedereDurum ve BaglamaBekleme alanlarina
eski Go adlariyla json etiketleri eklendi; alan adlari degisse de
Redis'teki JSON anahtarlari ayni kalir. TestRedisJSON_KeysUnchanged
alti yapinin anahtarlarini sabitler. Kablo goruntusune 7 TAG satiri
eklendi (anahtarlar bugunkuyle ayni).

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 8: identity: package move `internal/kimlik` → `internal/identity` and file renames

This task moves the package and renames its files. It renames **no** declaration except the package clause. Tasks 9-12 rename the declarations.

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/kimlik/akis_test.go` | `internal/identity/flow_test.go` |
| `internal/kimlik/anahtar.go` | `internal/identity/signing_key.go` |
| `internal/kimlik/anahtar_test.go` | `internal/identity/signing_key_test.go` |
| `internal/kimlik/baglama_sayfa.go` | `internal/identity/link_page.go` |
| `internal/kimlik/baglama_sayfa_test.go` | `internal/identity/link_page_test.go` |
| `internal/kimlik/cerez_kavanozu_test.go` | `internal/identity/cookie_jar_test.go` |
| `internal/kimlik/depo.go` | `internal/identity/storage.go` |
| `internal/kimlik/depo_iptal_test.go` | `internal/identity/storage_revoke_test.go` |
| `internal/kimlik/depo_test.go` | `internal/identity/storage_test.go` |
| `internal/kimlik/federe_akis_test.go` | `internal/identity/federation_flow_test.go` |
| `internal/kimlik/federe_depo.go` | `internal/identity/federated_store.go` |
| `internal/kimlik/federe_depo_sahte.go` | `internal/identity/federated_store_fake.go` |
| `internal/kimlik/federe_depo_sozlesme_test.go` | `internal/identity/federated_store_contract_test.go` |
| `internal/kimlik/federe_sayfa.go` | `internal/identity/federation_page.go` |
| `internal/kimlik/federe_sayfa_test.go` | `internal/identity/federation_page_test.go` |
| `internal/kimlik/hesap_sil_test.go` | `internal/identity/account_delete_test.go` |
| `internal/kimlik/hizlimit.go` | `internal/identity/ratelimit.go` |
| `internal/kimlik/hizlimit_test.go` | `internal/identity/ratelimit_test.go` |
| `internal/kimlik/istek.go` | `internal/identity/request.go` |
| `internal/kimlik/istek_test.go` | `internal/identity/request_test.go` |
| `internal/kimlik/istemci.go` | `internal/identity/client.go` |
| `internal/kimlik/istemci_test.go` | `internal/identity/client_test.go` |
| `internal/kimlik/jeton_depo.go` | `internal/identity/token_store.go` |
| `internal/kimlik/jeton_depo_pg_test.go` | `internal/identity/token_store_pg_test.go` |
| `internal/kimlik/jeton_depo_sahte.go` | `internal/identity/token_store_fake.go` |
| `internal/kimlik/jeton_depo_test.go` | `internal/identity/token_store_test.go` |
| `internal/kimlik/kullanici.go` | `internal/identity/user.go` |
| `internal/kimlik/kullanici_depo.go` | `internal/identity/user_store.go` |
| `internal/kimlik/kullanici_depo_sahte.go` | `internal/identity/user_store_fake.go` |
| `internal/kimlik/kullanici_depo_sozlesme_test.go` | `internal/identity/user_store_contract_test.go` |
| `internal/kimlik/kullanici_test.go` | `internal/identity/user_test.go` |
| `internal/kimlik/oturum.go` | `internal/identity/session.go` |
| `internal/kimlik/oturum_sahte.go` | `internal/identity/session_fake.go` |
| `internal/kimlik/pkce.go` | `internal/identity/pkce.go` |
| `internal/kimlik/sahte_yukari_akis_test.go` | `internal/identity/fake_upstream_test.go` |
| `internal/kimlik/sayfa.go` | `internal/identity/page.go` |
| `internal/kimlik/sayfa_test.go` | `internal/identity/page_test.go` |
| `internal/kimlik/sunucu.go` | `internal/identity/server.go` |
| `internal/kimlik/sunucu_test.go` | `internal/identity/server_test.go` |
| `internal/kimlik/veritabani.go` | `internal/identity/database.go` |
| `internal/kimlik/veritabani_test.go` | `internal/identity/database_test.go` |
| `internal/kimlik/yukari_akis.go` | `internal/identity/upstream.go` |
| `internal/kimlik/yukari_akis_apple.go` | `internal/identity/upstream_apple.go` |
| `internal/kimlik/yukari_akis_apple_test.go` | `internal/identity/upstream_apple_test.go` |
| `internal/kimlik/yukari_akis_jeton.go` | `internal/identity/upstream_token.go` |
| `internal/kimlik/yukari_akis_jeton_test.go` | `internal/identity/upstream_token_test.go` |
| `internal/kimlik/yukari_akis_test.go` | `internal/identity/upstream_test.go` |
| `internal/kimlik/redis_json_test.go` | `internal/identity/redis_json_test.go` |

Non-Go files move with the directory. Their names and contents do not change (identity risk R4; migration versions are recorded by file name):

| old path | new path |
|---|---|
| `internal/kimlik/migrations/001_kullanici.sql` | `internal/identity/migrations/001_kullanici.sql` (name and content unchanged) |
| `internal/kimlik/migrations/002_refresh_indeks.sql` | `internal/identity/migrations/002_refresh_indeks.sql` (name and content unchanged) |
| `internal/kimlik/migrations/003_federe_kimlik.sql` | `internal/identity/migrations/003_federe_kimlik.sql` (name and content unchanged) |
| `internal/kimlik/sablonlar/giris.html` | `internal/identity/sablonlar/giris.html` (name and content unchanged) |
| `internal/kimlik/sablonlar/kayit.html` | `internal/identity/sablonlar/kayit.html` (name and content unchanged) |
| `internal/kimlik/sablonlar/baglama.html` | `internal/identity/sablonlar/baglama.html` (name and content unchanged) |
| `internal/kimlik/sablonlar/cakisma.html` | `internal/identity/sablonlar/cakisma.html` (name and content unchanged) |

Also modify: `tools/wirecheck/main.go` (the seven `assets` paths only).

**Wire strings to keep (must stay byte-identical)**

_From the kimlik glossary, §4.4 SQL (table, column, index and constraint names; text unchanged):_

| file:line | literal | why |
|---|---|---|
| veritabani.go:44, 86; veritabani.go:13 (embed) | `"migrations"`, `"migrations/"`, `migrations/*.sql` | embed paths. The file name is the stored version |
| migrations/001_kullanici.sql | whole file, including `users_email_lower_key` and `refresh_tokens_family_idx` | migration content |
| migrations/002_refresh_indeks.sql | whole file (`refresh_tokens_user_client_idx`) | migration content |
| migrations/003_federe_kimlik.sql | whole file: `federe_kimlikler`, columns `saglayici`/`sub`/`user_id`, `federe_kimlikler_user_idx`, `federe_kimlikler_user_saglayici_key` | migration content |

_From the kimlik glossary, §4.7 User-facing text (the localization task will change it; this refactor keeps it byte-identical):_

| file:line | literal |
|---|---|
| sablonlar/*.html | all visible text, `lang="tr"`, CSS, form `name`/`id`/`for`/`action`/`method` attributes, the `minlength="10"` attribute and the `required` attributes |

**Risks (from the glossaries; names in them are the old ones)**

_From the kimlik glossary, risk R4:_

**R4: cross-package API that must change in lockstep (outside this scope but visible).**
- `cmd/server/main.go:86, 94, 100, 192, 234`: `kimlik.Start` → `identity.Start`; `kimlik.Baglan` → `identity.Connect`; `.Kapat` → `.Shutdown`; `.APIAcikAnahtar` → `.APIPublicKey`; `.Hazir` → `.Ready`.
- `internal/service/hesap_service.go:33-36`: `KimlikSilici.HesapSil` must become `DeleteAccount`, or `*identity.Server` stops satisfying it. This is caught at main.go:167 compile time. The fakes in `internal/service/hesap_service_test.go:22` and `internal/handler/hesap_test.go:20` change too.
- `kimlik.ErrKullaniciYok` → `identity.ErrUserNotFound` in `internal/service/hesap_service.go:78`, `hesap_service_test.go:80` and `internal/handler/hesap_test.go:52`.
- `internal/sosyal/veritabani_test.go:27`: `kimlik.Baglan` → `identity.Connect`.
- Comments in `internal/config/config.go:161, 315, 321, 439` and `config_test.go:86, 101, 109` cite `kimlik.istemciIP`, `bcryptCost`, `Start` and `NewHizLimit`.
- Inbound dependencies owned by other glossaries:
  - `config.AuthConfig.Federasyon` and `config.FederasyonIstemci{Ad, IstemciID, IstemciSir, AppleEkipID, AppleAnahtarID, AppleAnahtarP8}`, used in sunucu.go:281-393 (the config package agent will rename these; keep viper keys unchanged);
  - `auth.YukariAkisDogrulayici`, `auth.NewYukariAkisDogrulayici`, `(*YukariAkisDogrulayici).DogrulaNonce` and `auth.Claims{UserID, Email, Name, EmailVerified}`, used in federe_sayfa.go, baglama_sayfa.go, sunucu.go and tests.

  Coordinate the names with the config and auth glossaries.

_From the kimlik glossary, risk R6:_

**R6: other reflection and string lookups.**
- `go:embed` patterns: `sablonlar/*.html` (renamable with care) and `migrations/*.sql` (keep).
- No SQL is built from Go names; all SQL is literal. No viper `mapstructure` tags in scope. The only struct tags in non-test source are `json:"id_token"`/`json:"error"`.
- `r.PathValue("saglayici")` must match `{saglayici}` in the route pattern. Both stay Turkish (route param = wire).
- `SahteFedereDepo.eslesmeSayisi` is declared in a **test** file on a prod type, so rename it in sahte_yukari_akis_test.go alongside the fake.

_From the kimlik glossary, risk R9:_

**R9: documentation drift.** CLAUDE.md:117 names `kimlik:hizlimit:`, the counter suffixes, `/federasyon/*`, `/baglama`, `federe_kimlikler` and `SayfaGetSar`. The wire names there stay valid. The Go identifier `SayfaGetSar` becomes `RateLimiter.WrapPageGet`, so update the doc.

_From the sosyal glossary, risk 6:_

6. **`go:embed` and migrations.** `//go:embed migrations/*.sql`, `ReadDir("migrations")` and `ReadFile("migrations/"+name)` are relative to the package directory. They keep working after `internal/sosyal` becomes `internal/social`, but only if the `migrations/` directory moves with the package. Never rename `001_sosyal.sql`: `sosyal.schema_migrations.version` stores the file name, so a rename would re-run the migration. That happens to be idempotent (`IF NOT EXISTS`), but it is still a wire change. `TestMigrationlarGomuluVeSirali` reads the literal path `"migrations/001_sosyal.sql"`.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Move the package**

```bash
off=$(grep -bo '^package kimlik' internal/kimlik/sunucu.go | cut -d: -f1)
~/go/bin/gopls rename -w internal/kimlik/sunucu.go:#$((off+8)) identity   # prints "getFile: ... is a directory": expected
git mv internal/kimlik internal/identity
files=$(gofmt -l .); [ -z "$files" ] || gofmt -w $files
go build ./... && go vet ./...
grep -rln '"github.com/makeasinger/api/internal/kimlik"' --include='*.go' .
```

Expected:
- The build and vet print nothing.
- The `grep` prints nothing. `cmd/server/main.go`, `internal/middleware`, `internal/service` and `internal/handler` tests now import `.../internal/identity`.
- Every file in `internal/identity` starts with `package identity`.
- `migrations/` and `sablonlar/` moved with the directory. Their `//go:embed` patterns are relative to the package, so they still match.

- [ ] **Step 3: Rename the files**

```bash
git mv internal/identity/akis_test.go internal/identity/flow_test.go
git mv internal/identity/anahtar.go internal/identity/signing_key.go
git mv internal/identity/anahtar_test.go internal/identity/signing_key_test.go
git mv internal/identity/baglama_sayfa.go internal/identity/link_page.go
git mv internal/identity/baglama_sayfa_test.go internal/identity/link_page_test.go
git mv internal/identity/cerez_kavanozu_test.go internal/identity/cookie_jar_test.go
git mv internal/identity/depo.go internal/identity/storage.go
git mv internal/identity/depo_iptal_test.go internal/identity/storage_revoke_test.go
git mv internal/identity/depo_test.go internal/identity/storage_test.go
git mv internal/identity/federe_akis_test.go internal/identity/federation_flow_test.go
git mv internal/identity/federe_depo.go internal/identity/federated_store.go
git mv internal/identity/federe_depo_sahte.go internal/identity/federated_store_fake.go
git mv internal/identity/federe_depo_sozlesme_test.go internal/identity/federated_store_contract_test.go
git mv internal/identity/federe_sayfa.go internal/identity/federation_page.go
git mv internal/identity/federe_sayfa_test.go internal/identity/federation_page_test.go
git mv internal/identity/hesap_sil_test.go internal/identity/account_delete_test.go
git mv internal/identity/hizlimit.go internal/identity/ratelimit.go
git mv internal/identity/hizlimit_test.go internal/identity/ratelimit_test.go
git mv internal/identity/istek.go internal/identity/request.go
git mv internal/identity/istek_test.go internal/identity/request_test.go
git mv internal/identity/istemci.go internal/identity/client.go
git mv internal/identity/istemci_test.go internal/identity/client_test.go
git mv internal/identity/jeton_depo.go internal/identity/token_store.go
git mv internal/identity/jeton_depo_pg_test.go internal/identity/token_store_pg_test.go
git mv internal/identity/jeton_depo_sahte.go internal/identity/token_store_fake.go
git mv internal/identity/jeton_depo_test.go internal/identity/token_store_test.go
git mv internal/identity/kullanici.go internal/identity/user.go
git mv internal/identity/kullanici_depo.go internal/identity/user_store.go
git mv internal/identity/kullanici_depo_sahte.go internal/identity/user_store_fake.go
git mv internal/identity/kullanici_depo_sozlesme_test.go internal/identity/user_store_contract_test.go
git mv internal/identity/kullanici_test.go internal/identity/user_test.go
git mv internal/identity/oturum.go internal/identity/session.go
git mv internal/identity/oturum_sahte.go internal/identity/session_fake.go
git mv internal/identity/sahte_yukari_akis_test.go internal/identity/fake_upstream_test.go
git mv internal/identity/sayfa.go internal/identity/page.go
git mv internal/identity/sayfa_test.go internal/identity/page_test.go
git mv internal/identity/sunucu.go internal/identity/server.go
git mv internal/identity/sunucu_test.go internal/identity/server_test.go
git mv internal/identity/veritabani.go internal/identity/database.go
git mv internal/identity/veritabani_test.go internal/identity/database_test.go
git mv internal/identity/yukari_akis.go internal/identity/upstream.go
git mv internal/identity/yukari_akis_apple.go internal/identity/upstream_apple.go
git mv internal/identity/yukari_akis_apple_test.go internal/identity/upstream_apple_test.go
git mv internal/identity/yukari_akis_jeton.go internal/identity/upstream_token.go
git mv internal/identity/yukari_akis_jeton_test.go internal/identity/upstream_token_test.go
git mv internal/identity/yukari_akis_test.go internal/identity/upstream_test.go
go build ./...
```

- [ ] **Step 4: Point the guard at the new asset paths**

In `tools/wirecheck/main.go`, change the seven `assets` paths from `internal/kimlik/` to `internal/identity/`. Keep the logical names (`kimlik-migration-001`, `kimlik-template-giris`, ...) unchanged. `assetGlobs` (`internal/*/migrations/*.sql`, `internal/*/sablonlar/*`) stays as it is, because the `sablonlar` directory keeps its name.

```bash
perl -pi -e 's#"internal/kimlik/#"internal/identity/#g' tools/wirecheck/main.go
grep -c '"internal/identity/' tools/wirecheck/main.go
grep -n 'internal/kimlik' tools/wirecheck/main.go
go test -count=1 ./tools/wirecheck
```

Expected: `7`, then no output from the second `grep`, then `ok`. The snapshot is unchanged: only file contents are hashed, and the logical names did not move.

- [ ] **Step 5: Package doc comment**

`internal/identity` has no `// Package` comment today. Add none here: Task 9 owns `server.go` and writes the package comment while translating that file.

- [ ] **Step 6: Verify**

Run the VERIFY block from Global Constraints. The guard passes unchanged after Step 4.

- [ ] **Step 7: Commit**

```bash
git status --short
git add -A internal/kimlik internal/identity
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(identity): internal/kimlik -> internal/identity, dosya adlari

Paket ve dizin tasindi; 47 Go dosyasi Ingilizce adlar aldi. Migration
ve sablon dosyalari adlariyla birlikte tasindi. wirecheck varlik
yollari guncellendi (mantiksal adlar ayni); kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 9: identity: OP core (signing key, storage, tokens, users, client, PKCE, server, database)

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

_No file in this task changes its name._

The files were moved and renamed in Task 8. The table lists them for ownership only. This task translates the comments of the files listed in its comments step.

The account-deletion interface `service.KimlikSilici.HesapSil` is implemented by `identity.Server` (`Sunucu`), and `identity` never imports `service`. So part 1 renames the interface method with gopls (which also renames its three test fakes). Right after it, `apply_renames.py` renames the concrete method textually: that is the `manual: textual` row.

**Declarations, part 1: run first** (5 rows)

<!-- renames:9-first -->
| old | new | kind | file |
|---|---|---|---|
| KimlikSilici.HesapSil | AccountDeleter.DeleteAccount | interface method (implemented by kimlik.Sunucu; see R3) | internal/service/hesap_service.go |
| hSahteKimlik.HesapSil | fakeIdentity.DeleteAccount | method (test; implements service.AccountDeleter) | internal/handler/hesap_test.go |
| sahteKimlik.HesapSil | fakeIdentity.DeleteAccount | method (test) | internal/service/hesap_service_test.go |
| bosKimlik.HesapSil | nopIdentity.DeleteAccount | method (test) | internal/worker/hesap_worker_test.go |
| Sunucu.HesapSil | Server.DeleteAccount | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/identity/server.go |

**Declarations, part 2** (117 rows)

<!-- renames:9 -->
| old | new | kind | file |
|---|---|---|---|
| Anahtar | SigningKey | type | internal/identity/signing_key.go |
| Anahtar.ozel | SigningKey.private | field | internal/identity/signing_key.go |
| AnahtarYukle | LoadSigningKey | func | internal/identity/signing_key.go |
| kidUret | deriveKID | func | internal/identity/signing_key.go |
| Anahtar.AcikAnahtar | SigningKey.PublicKey | method | internal/identity/signing_key.go |
| AcikAnahtarKey | PublicKey | type | internal/identity/signing_key.go |
| AcikAnahtarKey.a | PublicKey.key | field | internal/identity/signing_key.go |
| CryptoAnahtar | CryptoKey | func | internal/identity/signing_key.go |
| Depo | Storage | type | internal/identity/storage.go |
| Depo.kullanici | Storage.users | field | internal/identity/storage.go |
| Depo.jetonlar | Storage.tokens | field | internal/identity/storage.go |
| Depo.istekler | Storage.requests | field | internal/identity/storage.go |
| Depo.anahtar | Storage.signingKey | field | internal/identity/storage.go |
| Depo.istemci | Storage.client | field | internal/identity/storage.go |
| Depo.havuz | Storage.pool | field | internal/identity/storage.go |
| NewDepo | NewStorage | func | internal/identity/storage.go |
| Depo.SaglikBagla | Storage.SetHealthDeps | method | internal/identity/storage.go |
| refreshIstek | refreshRequest | type | internal/identity/storage.go |
| Depo.aileIptalEt | Storage.revokeFamilyOf | method | internal/identity/storage.go |
| Depo.userinfoDoldur | Storage.fillUserinfo | method | internal/identity/storage.go |
| (Depo.* op.Storage methods, such as CreateAuthRequest, AuthRequestByID, SaveAuthCode, CreateAccessToken, CreateAccessAndRefreshTokens, TokenRequestByRefreshToken, TerminateSession, RevokeToken, GetRefreshTokenInfo, SigningKey, SignatureAlgorithms, KeySet, GetClientByClientID, AuthorizeClientIDSecret, SetUserinfoFrom*, SetIntrospectionFromToken, GetPrivateClaimsFromScopes, GetKeyByIDAndClientID, ValidateJWTProfileScopes and Health; plus revokeRefresh, revokeAccess, clientIDGetter, amrGetter and authTimeGetter) | unchanged; the receiver type becomes Storage | manual: method ; the receiver type becomes Storage | internal/identity/storage.go |
| MobilIstemci | MobileClient | type | internal/identity/client.go |
| NewMobilIstemci | NewMobileClient | func | internal/identity/client.go |
| (MobilIstemci.* op.Client methods) | unchanged; receiver becomes MobileClient | manual: method ; receiver becomes MobileClient | internal/identity/client.go |
| ErrJetonYok | ErrTokenNotFound | var | internal/identity/token_store.go |
| ErrJetonTekrar | ErrTokenReused | var | internal/identity/token_store.go |
| AccessKayit | AccessRecord | type | internal/identity/token_store.go |
| RefreshKayit | RefreshRecord | type | internal/identity/token_store.go |
| TokenStore.AccessKaydet | TokenStore.SaveAccess | interface method | internal/identity/token_store.go |
| TokenStore.AccessOku | TokenStore.GetAccess | interface method | internal/identity/token_store.go |
| TokenStore.AccessSil | TokenStore.DeleteAccess | interface method | internal/identity/token_store.go |
| TokenStore.RefreshOlustur | TokenStore.CreateRefresh | interface method | internal/identity/token_store.go |
| TokenStore.RefreshDondur | TokenStore.RotateRefresh | interface method | internal/identity/token_store.go |
| TokenStore.RefreshOku | TokenStore.GetRefresh | interface method | internal/identity/token_store.go |
| TokenStore.RefreshIDileOku | TokenStore.GetRefreshByID | interface method | internal/identity/token_store.go |
| TokenStore.RefreshAileID | TokenStore.RefreshFamilyID | interface method | internal/identity/token_store.go |
| TokenStore.AileIptal | TokenStore.RevokeFamily | interface method | internal/identity/token_store.go |
| TokenStore.KullaniciIptal | TokenStore.RevokeUser | interface method | internal/identity/token_store.go |
| jetonUret | generateToken | func | internal/identity/token_store.go |
| jetonOzet | hashToken | func | internal/identity/token_store.go |
| accessAnahtar | accessKey | func | internal/identity/token_store.go |
| PostgresTokenStore.havuz | PostgresTokenStore.pool | field | internal/identity/token_store.go |
| PostgresTokenStore.AccessKaydet | PostgresTokenStore.SaveAccess | method | internal/identity/token_store.go |
| PostgresTokenStore.AccessOku | PostgresTokenStore.GetAccess | method | internal/identity/token_store.go |
| PostgresTokenStore.AccessSil | PostgresTokenStore.DeleteAccess | method | internal/identity/token_store.go |
| PostgresTokenStore.RefreshOlustur | PostgresTokenStore.CreateRefresh | method | internal/identity/token_store.go |
| calistirici | execer | type (interface) | internal/identity/token_store.go |
| PostgresTokenStore.refreshEkle | PostgresTokenStore.insertRefresh | method | internal/identity/token_store.go |
| PostgresTokenStore.RefreshDondur | PostgresTokenStore.RotateRefresh | method | internal/identity/token_store.go |
| PostgresTokenStore.RefreshOku | PostgresTokenStore.GetRefresh | method | internal/identity/token_store.go |
| PostgresTokenStore.RefreshAileID | PostgresTokenStore.RefreshFamilyID | method | internal/identity/token_store.go |
| PostgresTokenStore.RefreshIDileOku | PostgresTokenStore.GetRefreshByID | method | internal/identity/token_store.go |
| PostgresTokenStore.AileIptal | PostgresTokenStore.RevokeFamily | method | internal/identity/token_store.go |
| PostgresTokenStore.KullaniciIptal | PostgresTokenStore.RevokeUser | method | internal/identity/token_store.go |
| SahteTokenStore | FakeTokenStore | type | internal/identity/token_store_fake.go |
| NewSahteTokenStore | NewFakeTokenStore | func | internal/identity/token_store_fake.go |
| SahteTokenStore.AccessKaydet | FakeTokenStore.SaveAccess | method | internal/identity/token_store_fake.go |
| SahteTokenStore.AccessOku | FakeTokenStore.GetAccess | method | internal/identity/token_store_fake.go |
| SahteTokenStore.AccessSil | FakeTokenStore.DeleteAccess | method | internal/identity/token_store_fake.go |
| SahteTokenStore.RefreshOlustur | FakeTokenStore.CreateRefresh | method | internal/identity/token_store_fake.go |
| SahteTokenStore.ekle | FakeTokenStore.insertLocked | method | internal/identity/token_store_fake.go |
| SahteTokenStore.RefreshDondur | FakeTokenStore.RotateRefresh | method | internal/identity/token_store_fake.go |
| SahteTokenStore.RefreshOku | FakeTokenStore.GetRefresh | method | internal/identity/token_store_fake.go |
| SahteTokenStore.RefreshAileID | FakeTokenStore.RefreshFamilyID | method | internal/identity/token_store_fake.go |
| SahteTokenStore.RefreshIDileOku | FakeTokenStore.GetRefreshByID | method | internal/identity/token_store_fake.go |
| SahteTokenStore.AileIptal | FakeTokenStore.RevokeFamily | method | internal/identity/token_store_fake.go |
| SahteTokenStore.aileIptal | FakeTokenStore.revokeFamilyLocked | method | internal/identity/token_store_fake.go |
| SahteTokenStore.KullaniciIptal | FakeTokenStore.RevokeUser | method | internal/identity/token_store_fake.go |
| SahteTokenStore.TumKayitlar | FakeTokenStore.AllRecords | method | internal/identity/token_store_fake.go |
| refreshKopya | copyRefresh | func | internal/identity/token_store_fake.go |
| accessKopya | copyAccess | func | internal/identity/token_store_fake.go |
| zamanKopya | copyTime | func | internal/identity/token_store_fake.go |
| ErrKullaniciYok | ErrUserNotFound | var | internal/identity/user.go |
| ErrEpostaKullanimda | ErrEmailInUse | var | internal/identity/user.go |
| ErrSifreKisa | ErrPasswordTooShort | var (**message text is user-visible; see R3**) | internal/identity/user.go |
| User.SifreDogru | User.PasswordMatches | method | internal/identity/user.go |
| sifreHashle | hashPassword | func | internal/identity/user.go |
| epostaNormalize | normalizeEmail | func | internal/identity/user_store.go |
| UserStore.Sil | UserStore.Delete | interface method | internal/identity/user_store.go |
| PostgresUserStore.havuz | PostgresUserStore.pool | field | internal/identity/user_store.go |
| PostgresUserStore.Sil | PostgresUserStore.Delete | method | internal/identity/user_store.go |
| PostgresUserStore.tekil | PostgresUserStore.queryOne | method | internal/identity/user_store.go |
| SahteUserStore | FakeUserStore | type | internal/identity/user_store_fake.go |
| SahteUserStore.epostayaGor | FakeUserStore.byEmail | field | internal/identity/user_store_fake.go |
| SahteUserStore.idyeGore | FakeUserStore.byID | field | internal/identity/user_store_fake.go |
| NewSahteUserStore | NewFakeUserStore | func | internal/identity/user_store_fake.go |
| sahteEpostaAnahtar | fakeEmailKey | func | internal/identity/user_store_fake.go |
| SahteUserStore.Create | FakeUserStore.Create | method | internal/identity/user_store_fake.go |
| SahteUserStore.ByEmail | FakeUserStore.ByEmail | method | internal/identity/user_store_fake.go |
| SahteUserStore.ByID | FakeUserStore.ByID | method | internal/identity/user_store_fake.go |
| SahteUserStore.Sil | FakeUserStore.Delete | method | internal/identity/user_store_fake.go |
| kullaniciKopya | copyUser | func | internal/identity/user_store_fake.go |
| pkceZorlayici | pkceEnforcer | type | internal/identity/pkce.go |
| pkceS256Zorunlu | requirePKCES256 | func | internal/identity/pkce.go |
| migrationKilitAnahtari | migrationLockKey | const (**value 0x6B696D6C696B31 unchanged**) | internal/identity/server.go |
| Sunucu | Server | type | internal/identity/server.go |
| Sunucu.havuz | Server.pool | field | internal/identity/server.go |
| Sunucu.anahtar | Server.signingKey | field | internal/identity/server.go |
| Sunucu.kullanicilar | Server.users | field | internal/identity/server.go |
| Sunucu.hazir | Server.ready | field | internal/identity/server.go |
| Sunucu.fedIptal | Server.fedCancel | field | internal/identity/server.go |
| Sunucu.dogrulayicilar | Server.verifiers | field | internal/identity/server.go |
| Sunucu.Hazir | Server.Ready | method | internal/identity/server.go |
| yapilandirmaDogrula | validateConfig | func | internal/identity/server.go |
| yukariAkisSabitleri | upstreamDefaults | var | internal/identity/server.go |
| federasyonKur | setupFederation | func | internal/identity/server.go |
| muxKur | buildMux | func | internal/identity/server.go |
| discoveryDuzelt | trimDiscovery | func | internal/identity/server.go |
| issuerGuvensizIzniDogrula | validateInsecureIssuer | func | internal/identity/server.go |
| migrateKilitli | migrateLocked | func | internal/identity/server.go |
| Sunucu.Kapat | Server.Shutdown | method | internal/identity/server.go |
| Sunucu.APIAcikAnahtar | Server.APIPublicKey | method | internal/identity/server.go |
| Baglan | Connect | func | internal/identity/database.go |
| migrationAdlari | migrationNames | func | internal/identity/database.go |
| izlemeTokenStore.KullaniciIptal | trackingTokenStore.RevokeUser | method | internal/identity/storage_revoke_test.go |
| hataliByIDUserStore.Sil | failingByIDUserStore.Delete | method | internal/identity/storage_test.go |
| sayimUserStore.Sil | countingUserStore.Delete | method | internal/identity/page_test.go |

Rows whose kind starts with `manual:` are skipped by `apply_renames.py`. Do each by hand:
- An **embedded field** row needs no action. Renaming the embedded type renames the field and every selector on it. Check the result in the diff.
- A **`var _ X = (*Y)(nil)`** row needs no action. The assertion follows the renamed types. Check it.
- An **anonymous struct field**, **local** or **param** row: run `~/go/bin/gopls rename -w <file>:<line>:<col> <new>` on the declaring identifier.
- A **package** row is done by the package-move step.
- A **"methods unchanged; receiver becomes ..."** row needs no action. The methods keep their `op.*` names and follow the type rename.

**Wire strings to keep (must stay byte-identical)**

_From the kimlik glossary, §4.1 HTTP routes, route params, query and form fields:_

| file:line | literal | why |
|---|---|---|
| sunucu.go:506 | `"/callback"` | `/authorize/callback` route |
| sunucu.go:519 | `"/"` | op provider catch-all (`/oauth/token`, `/userinfo`, `/keys`, `/end_session`) |
| istemci.go:44 | `"?authRequestID="` | query param of the LoginURL |

_From the kimlik glossary, §4.3 Redis key formats:_

| file:line | literal | why |
|---|---|---|
| jeton_depo.go:95 | `"kimlik:at:"` | access-token record JSON |

_From the kimlik glossary, §4.4 SQL (table, column, index and constraint names; text unchanged):_

| file:line | literal | why |
|---|---|---|
| kullanici_depo.go:66, 83, 89, 100 | `users` (id, email, email_verified, name, password_hash, created_at), `lower(email)` | DB schema |
| jeton_depo.go:182, 213, 231, 257, 289, 318, 341, 359, 370 | `refresh_tokens` (id, family_id, user_id, client_id, token_hash, scopes, audience, amr, auth_time, expires_at, used_at, revoked_at) | DB schema |
| kullanici_depo.go:73; federe_depo.go:79, 115, 124 | `"23505"` | SQLSTATE unique_violation |
| veritabani.go:62, 78, 99 | `schema_migrations (version, applied_at)` | migration ledger |
| veritabani.go:44, 86; veritabani.go:13 (embed) | `"migrations"`, `"migrations/"`, `migrations/*.sql` | embed paths. The file name is the stored version |
| sunucu.go:603, 609 | `SELECT pg_advisory_lock($1)` / `pg_advisory_unlock($1)` | advisory lock |
| sunucu.go:31 | `0x6B696D6C696B31` | **advisory lock value**. Every pod in a rolling update must use the same one |

_From the kimlik glossary, §4.5 OAuth / OIDC protocol strings:_

| file:line | literal | why |
|---|---|---|
| anahtar.go:80 | `"sig"` | JWKS `use` |
| sunucu.go:336-350 | the Google/Apple issuer, auth, token and JWKS URLs; scopes `openid email profile` / `openid email name` | upstream constants |
| pkce.go:43 | `"unable to retrieve client by id"` | OAuth `error_description` |
| pkce.go:66 | `"code_challenge is required: this provider only accepts PKCE with code_challenge_method=S256"` | OAuth `error_description` (client-visible) |
| pkce.go:70 | `"code_challenge_method must be S256"` | OAuth `error_description` |
| depo.go:332, 352 | `"jeton bu istemci icin verilmemis"` | `oidc.ErrInvalidClient().WithDescription`, which **is sent to the client** as `error_description`. Keep for now (localization task) |
| sunucu.go:141, 154, 573 | `"http://"` | scheme checks |
| sunucu.go:576 | `"production"` | SERVER_ENV value |
| sunucu.go:584 | `"localhost"`, `"127.0.0.1"`, `"::1"` | insecure-mode hosts |

_From the kimlik glossary, §4.7 User-facing text (the localization task will change it; this refactor keeps it byte-identical):_

| file:line | literal |
|---|---|
| kullanici.go:17 | `"sifre en az 10 karakter olmali"`. **Rendered to the user** via `ErrSifreKisa.Error()` (sayfa.go:631) |

**Risks (from the glossaries; names in them are the old ones)**

_From the kimlik glossary, risk R3:_

**R3: error text that is user-visible or asserted.**
- `ErrSifreKisa.Error()` is rendered into kayit.html (sayfa.go:631), so keep `"sifre en az 10 karakter olmali"`, or decouple it into a msg const with the same text.
- `oidc.ErrInvalidClient().WithDescription("jeton bu istemci icin verilmemis")` reaches the client.
- Tests match error substrings:
  - sunucu_test.go:349 `"veritabanina baglanilamadi"` (the skip detection; translating it without the test makes the test FAIL instead of SKIP when no Postgres is present);
  - sunucu_test.go:373 `"hiz limiti kurulamadi"`;
  - the env-var and config-key substrings (4.9), plus `"https"` (sunucu_test.go:50, 65, 80).

  Translate source and test in the same change.
- hizlimit.go:109-118 messages cite **stale** config keys (`giris_ip_per_min` and so on). While translating, use the real keys `login_ip_per_min`, `login_email_per_hour`, `signup_ip_per_hour` and `trusted_proxies`. No test asserts on them.

_From the kimlik glossary, risk R4:_

**R4: cross-package API that must change in lockstep (outside this scope but visible).**
- `cmd/server/main.go:86, 94, 100, 192, 234`: `kimlik.Start` → `identity.Start`; `kimlik.Baglan` → `identity.Connect`; `.Kapat` → `.Shutdown`; `.APIAcikAnahtar` → `.APIPublicKey`; `.Hazir` → `.Ready`.
- `internal/service/hesap_service.go:33-36`: `KimlikSilici.HesapSil` must become `DeleteAccount`, or `*identity.Server` stops satisfying it. This is caught at main.go:167 compile time. The fakes in `internal/service/hesap_service_test.go:22` and `internal/handler/hesap_test.go:20` change too.
- `kimlik.ErrKullaniciYok` → `identity.ErrUserNotFound` in `internal/service/hesap_service.go:78`, `hesap_service_test.go:80` and `internal/handler/hesap_test.go:52`.
- `internal/sosyal/veritabani_test.go:27`: `kimlik.Baglan` → `identity.Connect`.
- Comments in `internal/config/config.go:161, 315, 321, 439` and `config_test.go:86, 101, 109` cite `kimlik.istemciIP`, `bcryptCost`, `Start` and `NewHizLimit`.
- Inbound dependencies owned by other glossaries:
  - `config.AuthConfig.Federasyon` and `config.FederasyonIstemci{Ad, IstemciID, IstemciSir, AppleEkipID, AppleAnahtarID, AppleAnahtarP8}`, used in sunucu.go:281-393 (the config package agent will rename these; keep viper keys unchanged);
  - `auth.YukariAkisDogrulayici`, `auth.NewYukariAkisDogrulayici`, `(*YukariAkisDogrulayici).DogrulaNonce` and `auth.Claims{UserID, Email, Name, EmailVerified}`, used in federe_sayfa.go, baglama_sayfa.go, sunucu.go and tests.

  Coordinate the names with the config and auth glossaries.

_From the kimlik glossary, risk R5:_

**R5: name collisions and shadowing after rename.**
- Field and method names must differ on the same type. `FedereSayfalar.geriCagirma` and `geriURL` would both want "callbackURL", so the plan uses `authCallback` (field) and `upstreamRedirectURI` (method). `fedOrtam.geri` → `callback` and `.baslat` → `start` are methods on a different type, so they are fine.
- Package level:
  - type `Storage` vs the `storage op.Storage` parameter in `pkceEnforcer.ValidateAuthRequest`: the parameter shadows the type only inside that function, which never uses the type. Harmless.
  - type `SigningKey` vs method `Storage.SigningKey`, and type `PublicKey` vs method `SigningKey.PublicKey`: legal.
  - `Start` (func) vs `FederationPages.Start` (method): legal.
  - `Server.ready` field vs `Ready()` method: legal.
- Test helpers: `al`/`govde` would naturally become `get`/`body`, but many tests already have locals named `body`. That is why the plan uses `mustGet`/`readBody`. Similarly `stepContext` (func) vs `TestStepContext` is fine, and `bindTestSession` (sayfa_test func) is kept apart from `fedEnv.bindSession`.
- Federation-page and authorize-callback tests would both start `TestCallback_…`, so the plan prefixes `TestFederationStart_`, `TestFederationCallback_` and `TestAuthorizeCallback_`.
- Two terms have to stay distinct:
  - "baglama" means **session binding** and also **account link**. Do not merge `BindSession`/`SessionBinding` with `Link`/`PendingLink`.
  - "kayit" means **record** and also **signup**.

_From the kimlik glossary, risk R7:_

**R7: fakes ship in the production binary.** `SahteUserStore`, `SahteTokenStore`, `SahteFedereDepo` and `SahteOturumDepo` live in non-test files and are exported. Renaming them to `Fake*` is safe because nothing outside the package uses them.

_From the app glossary, risk R1:_

**R1. Interface methods implemented across packages.** These renames must land in the same change as the implementations:
- `service.KimlikSilici.HesapSil` → `AccountDeleter.DeleteAccount` is implemented by `*kimlik.Sunucu.HesapSil` (main.go:167 passes `kimlikSunucu`). The identity glossary must rename `Sunucu.HesapSil` → `Server.DeleteAccount`. The test fakes `fakeIdentity` (handler and service) and `nopIdentity` (worker) follow it.
- `service.SosyalTemizleyici.{YayinlariListele, KullaniciIzleriniSil, KullaniciVerisiniSil}` → `SocialCleaner.{ListPublicationIDs, DeleteUserTraces, DeleteUserData}` is implemented by `*sosyal.PostgresDepo` (main.go:167 passes `sosyalDepo`). The social glossary must use exactly these method names, or this glossary must adopt theirs.
- `hesapisaret.Denetci.Silinmis` is implemented by worker test `fakeChecker`. Its new name comes from the deletionmark glossary (I assumed `deletionmark.Checker.IsDeleted`).
- `client.OnekSilici.DeletePrefix` and `client.StorageClient`/`client.AudioProcessor` are already English. The fakes only follow type renames.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Apply part 1 (interface methods and their fakes)**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 9-first
```

Expected: for each interface method, a `rename` line followed by a `textual` line for its concrete implementer; `already` for each fake that gopls renamed with the interface; then `<k> renamed, <n> rows in table`. gopls refuses every rename while any package has type errors, and renaming an interface method breaks the build until the implementer follows. So the script renames the implementer textually right after each interface method (the `manual: textual` rows; it checks that exactly one declaration matches) and runs `go build ./...` before the next row. If it stops, fix the reported cause and re-run it: finished rows print `already`.

- [ ] **Step 3: Check that no code reference to `HesapSil` is left**

```bash
grep -rnwE 'HesapSil' --include='*.go' .
files=$(gofmt -l .); [ -z "$files" ] || gofmt -w $files
go build ./... && go vet ./...
```

Expected:
- The `grep` prints only comment lines and string literals. For example, a `t.Fatalf("<OldName>: %v", err)` message stays as it is under ruling A7. It prints no code reference: no declaration on `*Sunucu`, and no call.
- The build and vet print nothing.


- [ ] **Step 4: Apply the declaration table (part 2)**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 9 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 5: Do the `manual:` rows**

Follow the rules under the table. Then run:

```bash
go build ./... && go vet ./...
```

- [ ] **Step 6: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- `server.go` gets the package comment: `// Package identity is the custom OpenID Provider: ...` (translate the existing description at the top of `Sunucu`).

Files (11): `internal/identity/client.go`, `internal/identity/database.go`, `internal/identity/pkce.go`, `internal/identity/server.go`, `internal/identity/signing_key.go`, `internal/identity/storage.go`, `internal/identity/token_store.go`, `internal/identity/token_store_fake.go`, `internal/identity/user.go`, `internal/identity/user_store.go`, `internal/identity/user_store_fake.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/identity/client.go internal/identity/database.go internal/identity/pkce.go internal/identity/server.go internal/identity/signing_key.go internal/identity/storage.go internal/identity/token_store.go internal/identity/token_store_fake.go internal/identity/user.go internal/identity/user_store.go internal/identity/user_store_fake.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 7: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 9-first --dry-run | grep -v '^already'
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 9 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 8: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 9: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(identity): OP cekirdegi Ingilizce adlandirma

Imza anahtari, depo, jeton, kullanici, istemci, PKCE, sunucu ve
veritabani tanimlayicilari Ingilizceye cevrildi; Sunucu.HesapSil ->
Server.DeleteAccount (service.AccountDeleter ile birlikte). Kablo
goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 10: identity: template data structs and template actions

This task renames the five template data types and their fields **and** changes the `{{.X}}` references in the four templates in the same commit (identity risk R2). Template file names, directory, text and markup stay. The guard therefore shows exactly four changed `FILE` lines, and a golden render proves the HTML output is byte-identical.

**Files**
- Modify: `internal/identity/page.go`, `internal/identity/link_page.go` (by gopls)
- Modify: `internal/identity/sablonlar/giris.html`, `kayit.html`, `baglama.html`, `cakisma.html` (field references only)
- Modify: `tools/wirecheck/testdata/wire.txt` (four `FILE` hashes)
- Temporary (not committed): `internal/identity/template_golden_test.go`

**Declarations** (25 rows)

<!-- renames:10 -->
| old | new | kind | file |
|---|---|---|---|
| baglamaVeri | linkData | type | internal/identity/link_page.go |
| baglamaVeri.Eposta | linkData.Email | field | internal/identity/link_page.go |
| baglamaVeri.BeklemeID | linkData.PendingID | field | internal/identity/link_page.go |
| baglamaVeri.Hata | linkData.Error | field | internal/identity/link_page.go |
| baglamaVeri.BaglamaYolu | linkData.LinkPath | field | internal/identity/link_page.go |
| cakismaVeri | conflictData | type | internal/identity/link_page.go |
| cakismaVeri.Eposta | conflictData.Email | field | internal/identity/link_page.go |
| cakismaVeri.SaglayiciMetni | conflictData.ProviderText | field | internal/identity/link_page.go |
| cakismaVeri.GirisURL | conflictData.LoginURL | field | internal/identity/link_page.go |
| girisVeri | loginData | type | internal/identity/page.go |
| girisVeri.Eposta | loginData.Email | field | internal/identity/page.go |
| girisVeri.Hata | loginData.Error | field | internal/identity/page.go |
| girisVeri.GirisYolu | loginData.LoginPath | field | internal/identity/page.go |
| girisVeri.KayitYolu | loginData.SignupPath | field | internal/identity/page.go |
| girisVeri.Saglayicilar | loginData.Providers | field | internal/identity/page.go |
| SaglayiciButon | ProviderButton | type | internal/identity/page.go |
| SaglayiciButon.Ad | ProviderButton.Name | field | internal/identity/page.go |
| SaglayiciButon.Etiket | ProviderButton.Label | field | internal/identity/page.go |
| SaglayiciButon.Yol | ProviderButton.Path | field | internal/identity/page.go |
| kayitVeri | signupData | type | internal/identity/page.go |
| kayitVeri.Eposta | signupData.Email | field | internal/identity/page.go |
| kayitVeri.Ad | signupData.Name | field | internal/identity/page.go |
| kayitVeri.Hata | signupData.Error | field | internal/identity/page.go |
| kayitVeri.GirisYolu | signupData.LoginPath | field | internal/identity/page.go |
| kayitVeri.KayitYolu | signupData.SignupPath | field | internal/identity/page.go |

**Template field map** (every `{{...}}` action in the four templates; fields not listed keep their name: `AuthRequestID`, `CSRF`)

| old | new |
|---|---|
| `.Eposta` | `.Email` |
| `.Hata` | `.Error` |
| `.BaglamaYolu` | `.LinkPath` |
| `.BeklemeID` | `.PendingID` |
| `.SaglayiciMetni` | `.ProviderText` |
| `.GirisURL` | `.LoginURL` |
| `.GirisYolu` | `.LoginPath` |
| `.KayitYolu` | `.SignupPath` |
| `.Saglayicilar` | `.Providers` |
| `.Yol` | `.Path` |
| `.Etiket` | `.Label` |
| `.Ad` | `.Name` |

**Wire strings to keep (must stay byte-identical)**

_From the kimlik glossary, §4.7 User-facing text (the localization task will change it; this refactor keeps it byte-identical):_

| file:line | literal |
|---|---|
| sablonlar/*.html | all visible text, `lang="tr"`, CSS, form `name`/`id`/`for`/`action`/`method` attributes, the `minlength="10"` attribute and the `required` attributes |

**Risks (from the glossaries; names in them are the old ones)**

_From the kimlik glossary, risk R2:_

**R2: html/template reflection.** The template data structs `girisVeri`, `kayitVeri`, `baglamaVeri`, `cakismaVeri` and `SaglayiciButon` resolve their fields by name at render time. A renamed field with an unedited template fails at **runtime** with "can't evaluate field Eposta" and an empty page; it is not a compile error.

Decision: rename the fields **and** edit only the `{{.X}}` action names listed in 4.8. Text, attributes and markup stay byte-identical. The executor should diff each rendered page before and after the change, for example with a golden render of all four pages from the existing tests.

The template file/dir rename is allowed because only Go references it (§2). Update `go:embed`, `ParseFS` and all 4 `ExecuteTemplate` strings together, or `template.Must` panics at package init and every test fails.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Render the pages before the change**

Create `internal/identity/template_golden_test.go`:

```go
package identity

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestTemplateGolden renders every hosted page into $GOLDEN_DIR. Task 10
// runs it before and after the rename and diffs the two directories. This
// file is temporary: delete it before committing.
func TestTemplateGolden(t *testing.T) {
	dir := os.Getenv("GOLDEN_DIR")
	if dir == "" {
		t.Skip("GOLDEN_DIR not set")
	}
	buttons := []SaglayiciButon{
		{Ad: "google", Etiket: "Google ile giris yap", Yol: "/federasyon/google/baslat?authRequestID=ar-1"},
		{Ad: "apple", Etiket: "Apple ile giris yap", Yol: "/federasyon/apple/baslat?authRequestID=ar-1"},
	}
	pages := []struct {
		out, tmpl string
		data      any
	}{
		{"giris-full.html", "giris.html", girisVeri{AuthRequestID: "ar-1", CSRF: "csrf-1", Eposta: "a@b.co", Hata: "Hata <b>x</b>", GirisYolu: "/giris", KayitYolu: "/kayit", Saglayicilar: buttons}},
		{"giris-empty.html", "giris.html", girisVeri{}},
		{"kayit-full.html", "kayit.html", kayitVeri{AuthRequestID: "ar-1", CSRF: "csrf-1", Eposta: "a@b.co", Ad: "Ali", Hata: "Hata", GirisYolu: "/giris", KayitYolu: "/kayit"}},
		{"kayit-empty.html", "kayit.html", kayitVeri{}},
		{"baglama-full.html", "baglama.html", baglamaVeri{Eposta: "a@b.co", BeklemeID: "p-1", CSRF: "csrf-1", Hata: "Hata", BaglamaYolu: "/baglama"}},
		{"baglama-empty.html", "baglama.html", baglamaVeri{}},
		{"cakisma-full.html", "cakisma.html", cakismaVeri{Eposta: "a@b.co", SaglayiciMetni: "Google", GirisURL: "/giris?authRequestID=ar-1"}},
		{"cakisma-empty.html", "cakisma.html", cakismaVeri{}},
	}
	for _, p := range pages {
		var b bytes.Buffer
		if err := sablonlar.ExecuteTemplate(&b, p.tmpl, p.data); err != nil {
			t.Fatalf("%s: %v", p.out, err)
		}
		if err := os.WriteFile(filepath.Join(dir, p.out), b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
```

```bash
export G="${TMPDIR:-/tmp}/en-golden"
rm -rf "$G" && mkdir -p "$G/before" "$G/after"
GOLDEN_DIR="$G/before" go test -count=1 -run TestTemplateGolden ./internal/identity
ls "$G/before" | wc -l
```

Expected: `ok`, then `8`.

- [ ] **Step 3: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 10 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 4: Rename the template field references**

```bash
cd internal/identity/sablonlar
perl -pi -e 'BEGIN{%m=(Eposta=>"Email",Hata=>"Error",BaglamaYolu=>"LinkPath",BeklemeID=>"PendingID",SaglayiciMetni=>"ProviderText",GirisURL=>"LoginURL",GirisYolu=>"LoginPath",KayitYolu=>"SignupPath",Saglayicilar=>"Providers",Yol=>"Path",Etiket=>"Label",Ad=>"Name")} s/\{\{([^}]*)\}\}/my $a=$1; $a=~s{\.(\w+)}{exists $m{$1} ? ".$m{$1}" : ".$1"}ge; "{{$a}}"/ge' *.html
shasum -a 256 *.html
cd ../../..
git diff --stat -- internal/identity/sablonlar
```

Expected hashes (before → after):

| file | before | after |
|---|---|---|
| `baglama.html` | `6e83357c2be7047c14eb5c6e1c936c41608d75a928c03ad46cb4bc9fef526d96` | `3f0e7c754ac687e409f98dd8c7e4e1c5ce36ffd07e2029fcf44d908dd5b00262` |
| `cakisma.html` | `fb2602b76a0a33d7c21cfc4731d92ff53924f61ed18ff5982545ff085dc8460f` | `961da13a2f0437ff24e0c132daeb1018030c5ab355343b87e1408355ac5a6009` |
| `giris.html` | `f70644682eb50f33130e4937617aa5ebabc18f6ac562e0ca6f30cbc2319403ba` | `e33d4301873e55984131125f547c70e0c4d8475ddec6137caba26252853d6007` |
| `kayit.html` | `475b1863fe1f39fe9a963a9ac9500458ebf01cefa79b9aef68f4ce079fad8f6a` | `489c424030faaf73e5abbb2eaa5941bd8df6da40e8411ebf0c8715a3a5323cb6` |

Review `git diff -- internal/identity/sablonlar`: every changed line differs only inside `{{ }}`, and no text outside an action changed.

- [ ] **Step 5: Render the pages after the change and compare**

```bash
GOLDEN_DIR="$G/after" go test -count=1 -run TestTemplateGolden ./internal/identity
diff -r "$G/before" "$G/after" && echo SAME-HTML
rm internal/identity/template_golden_test.go
```

Expected: `ok`, then `SAME-HTML`. gopls already rewrote the test's composite literals to the new names. If `ExecuteTemplate` fails with `can't evaluate field`, a template reference was missed.

- [ ] **Step 6: Review and record the snapshot change**

```bash
go test -count=1 ./tools/wirecheck 2>&1 | grep -E '^\s+[-+](STR|TAG|ROUTE|FILE)'
```

Expected: exactly 8 lines. There are four `-FILE` lines with the "before" hashes above and four `+FILE` lines with the "after" hashes, under the logical names `kimlik-template-baglama`, `-cakisma`, `-giris` and `-kayit`. There is no `STR`, `TAG` or `ROUTE` line. **The reviewer accepts these 8 lines**, then:

```bash
go test ./tools/wirecheck -update
wc -l tools/wirecheck/testdata/wire.txt
```

Expected: `2148`.

- [ ] **Step 7: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 10 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 8: Verify**

Run the VERIFY block from Global Constraints. The guard passes after the `-update` of Step 6.

- [ ] **Step 9: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(identity): sablon veri yapilari ve sablon alanlari Ingilizce

girisVeri, kayitVeri, baglamaVeri, cakismaVeri ve SaglayiciButon
Ingilizce adlar aldi; sablonlardaki {{.X}} alan referanslari ayni
commit'te degisti. Metin ve isaretleme ayni; dort sayfanin oncesi ve
sonrasi bayt bayt ayni render edilir. Kablo goruntusunde yalnizca
dort sablon ozeti degisti.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 11: identity: pages, sessions, auth requests, federation, upstream, rate limit

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

_No file in this task changes its name._

The files were moved and renamed in Task 8. The fields of the Redis-stored structs (`AuthIstek.IpucuSubject`, `OturumBaglama.Kayitlar`, `FedereDurum.*`, `BaglamaBekleme.*`) got their `json` tags in Task 7. `TestRedisJSON_KeysUnchanged` must still pass after this task.

**Declarations** (261 rows)

<!-- renames:11 -->
| old | new | kind | file |
|---|---|---|---|
| yolBaglama | pathLink | const | internal/identity/link_page.go |
| alanBeklemeID | fieldPendingID | const | internal/identity/link_page.go |
| gizliAktarmaSoneki | privateRelaySuffix | const | internal/identity/link_page.go |
| baglamaBeklemeTTL | pendingLinkTTL | const | internal/identity/link_page.go |
| BaglamaBekleme | PendingLink | type | internal/identity/link_page.go |
| BaglamaBekleme.Saglayici | PendingLink.Provider | field (its json:"Saglayici" tag was added in Task 7) | internal/identity/link_page.go |
| BaglamaBekleme.Eposta | PendingLink.Email | field (its json:"Eposta" tag was added in Task 7) | internal/identity/link_page.go |
| ErrBeklemeYok | ErrPendingLinkNotFound | var | internal/identity/link_page.go |
| ErrYukariAkisEpostaYok | ErrUpstreamNoEmail | var | internal/identity/link_page.go |
| BeklemeDepo | PendingLinkStore | type (interface) | internal/identity/link_page.go |
| BeklemeDepo.BeklemeYaz | PendingLinkStore.WritePendingLink | interface method | internal/identity/link_page.go |
| BeklemeDepo.BeklemeOku | PendingLinkStore.ReadPendingLink | interface method | internal/identity/link_page.go |
| BeklemeDepo.BeklemeAl | PendingLinkStore.TakePendingLink | interface method | internal/identity/link_page.go |
| beklemeAnahtar | pendingLinkKey | func | internal/identity/link_page.go |
| IstekDepo.BeklemeYaz | RequestStore.WritePendingLink | method | internal/identity/link_page.go |
| IstekDepo.BeklemeOku | RequestStore.ReadPendingLink | method | internal/identity/link_page.go |
| IstekDepo.BeklemeAl | RequestStore.TakePendingLink | method | internal/identity/link_page.go |
| IstekDepo.beklemeCoz | RequestStore.decodePendingLink | method | internal/identity/link_page.go |
| Cakisma | Conflict | type | internal/identity/link_page.go |
| Cakisma.BeklemeID | Conflict.PendingID | field | internal/identity/link_page.go |
| Cakisma.Eposta | Conflict.Email | field | internal/identity/link_page.go |
| Cakisma.MevcutSaglayicilar | Conflict.ExistingProviders | field | internal/identity/link_page.go |
| Cakisma.SifreliHesap | Conflict.HasPassword | field | internal/identity/link_page.go |
| Cakisma.saglayici | Conflict.provider | field | internal/identity/link_page.go |
| FedereSayfalar.kullaniciCoz | FederationPages.resolveUser | method | internal/identity/link_page.go |
| FedereSayfalar.saglayicilar | FederationPages.linkedProviders | method | internal/identity/link_page.go |
| FedereSayfalar.cakismaKur | FederationPages.buildConflict | method | internal/identity/link_page.go |
| FedereSayfalar.cakismaYonlendir | FederationPages.handleConflict | method | internal/identity/link_page.go |
| saglayiciEtiket | providerLabel | func | internal/identity/link_page.go |
| saglayiciMetni | providerText | func | internal/identity/link_page.go |
| FedereSayfalar.cakismaRender | FederationPages.renderConflict | method | internal/identity/link_page.go |
| FedereSayfalar.baglamaRender | FederationPages.renderLink | method | internal/identity/link_page.go |
| FedereSayfalar.BaglamaBagla | FederationPages.MountLink | method | internal/identity/link_page.go |
| FedereSayfalar.baglamaBaglamaDogrula | FederationPages.verifyLinkBinding | method | internal/identity/link_page.go |
| FedereSayfalar.baglamaGet | FederationPages.linkGet | method | internal/identity/link_page.go |
| FedereSayfalar.baglamaPost | FederationPages.linkPost | method | internal/identity/link_page.go |
| ErrKimlikYok | ErrIdentityNotFound | var | internal/identity/federated_store.go |
| ErrKimlikKullanimda | ErrIdentityInUse | var | internal/identity/federated_store.go |
| FedereDepo | FederatedStore | type (interface) | internal/identity/federated_store.go |
| FedereDepo.KimlikleKullanici | FederatedStore.UserByIdentity | interface method | internal/identity/federated_store.go |
| FedereDepo.Bagla | FederatedStore.Link | interface method | internal/identity/federated_store.go |
| FedereDepo.FedereKullaniciOlustur | FederatedStore.CreateFederatedUser | interface method | internal/identity/federated_store.go |
| FedereDepo.SaglayicilarByUserID | FederatedStore.ProvidersByUserID | interface method | internal/identity/federated_store.go |
| PostgresFedereDepo | PostgresFederatedStore | type | internal/identity/federated_store.go |
| PostgresFedereDepo.havuz | PostgresFederatedStore.pool | field | internal/identity/federated_store.go |
| NewPostgresFedereDepo | NewPostgresFederatedStore | func | internal/identity/federated_store.go |
| PostgresFedereDepo.KimlikleKullanici | PostgresFederatedStore.UserByIdentity | method | internal/identity/federated_store.go |
| PostgresFedereDepo.Bagla | PostgresFederatedStore.Link | method | internal/identity/federated_store.go |
| PostgresFedereDepo.FedereKullaniciOlustur | PostgresFederatedStore.CreateFederatedUser | method | internal/identity/federated_store.go |
| PostgresFedereDepo.SaglayicilarByUserID | PostgresFederatedStore.ProvidersByUserID | method | internal/identity/federated_store.go |
| federeAnahtar | identityKey | func | internal/identity/federated_store_fake.go |
| SahteFedereDepo | FakeFederatedStore | type | internal/identity/federated_store_fake.go |
| SahteFedereDepo.kullanici | FakeFederatedStore.users | field | internal/identity/federated_store_fake.go |
| SahteFedereDepo.eslesmeler | FakeFederatedStore.links | field | internal/identity/federated_store_fake.go |
| NewSahteFedereDepo | NewFakeFederatedStore | func | internal/identity/federated_store_fake.go |
| SahteFedereDepo.KimlikleKullanici | FakeFederatedStore.UserByIdentity | method | internal/identity/federated_store_fake.go |
| SahteFedereDepo.Bagla | FakeFederatedStore.Link | method | internal/identity/federated_store_fake.go |
| SahteFedereDepo.baglanabilirMi | FakeFederatedStore.canLinkLocked | method | internal/identity/federated_store_fake.go |
| SahteFedereDepo.FedereKullaniciOlustur | FakeFederatedStore.CreateFederatedUser | method | internal/identity/federated_store_fake.go |
| SahteFedereDepo.SaglayicilarByUserID | FakeFederatedStore.ProvidersByUserID | method | internal/identity/federated_store_fake.go |
| FakeUserStore.federeEkle | FakeUserStore.addFederated | method (was SahteUserStore.federeEkle; its type was renamed in an earlier task) | internal/identity/federated_store_fake.go |
| yolFederasyonBaslat | pathFederationStart | const | internal/identity/federation_page.go |
| yolFederasyonGeri | pathFederationCallback | const | internal/identity/federation_page.go |
| saglayiciYerTutucu | providerPlaceholder | const | internal/identity/federation_page.go |
| federasyonBaslatYolu | federationStartPath | func | internal/identity/federation_page.go |
| cerezAdFederasyon | cookieNameFederation | const | internal/identity/federation_page.go |
| hataIstekGecersiz | msgRequestInvalid | const | internal/identity/federation_page.go |
| hataIstekSuresiDoldu | msgRequestExpired | const | internal/identity/federation_page.go |
| hataEpostaYok | msgUpstreamNoEmail | const | internal/identity/federation_page.go |
| federasyonZamanAsimi | federationHTTPTimeout | const | internal/identity/federation_page.go |
| FedereIstekDepo | FederationRequestStore | type (interface) | internal/identity/federation_page.go |
| FedereIstekDepo.IDileOku | FederationRequestStore.GetByID | interface method | internal/identity/federation_page.go |
| FederasyonDepo | FlowStore | type (interface) | internal/identity/federation_page.go |
| FedereSayfalar | FederationPages | type | internal/identity/federation_page.go |
| FedereSayfalar.akislar | FederationPages.upstreams | field | internal/identity/federation_page.go |
| FedereSayfalar.dogrulayicilar | FederationPages.verifiers | field | internal/identity/federation_page.go |
| FedereSayfalar.durum | FederationPages.flows | field | internal/identity/federation_page.go |
| FedereSayfalar.federe | FederationPages.federated | field | internal/identity/federation_page.go |
| FedereSayfalar.kullanici | FederationPages.users | field | internal/identity/federation_page.go |
| FedereSayfalar.istekler | FederationPages.requests | field | internal/identity/federation_page.go |
| FedereSayfalar.oturumlar | FederationPages.sessions | field | internal/identity/federation_page.go |
| FedereSayfalar.geriCagirma | FederationPages.authCallback | field | internal/identity/federation_page.go |
| FedereSayfalar.cerezGuvenli | FederationPages.secureCookie | field | internal/identity/federation_page.go |
| FedereSayfalar.istemci | FederationPages.httpClient | field | internal/identity/federation_page.go |
| FedereSayfalar.hiz | FederationPages.limiter | field | internal/identity/federation_page.go |
| NewFedereSayfalar | NewFederationPages | func | internal/identity/federation_page.go |
| FedereSayfalar.Bagla | FederationPages.Mount | method | internal/identity/federation_page.go |
| FedereSayfalar.geriURL | FederationPages.upstreamRedirectURI | method | internal/identity/federation_page.go |
| FedereSayfalar.federasyonCerezi | FederationPages.federationCookie | method | internal/identity/federation_page.go |
| FedereSayfalar.federasyonCereziSil | FederationPages.clearFederationCookie | method | internal/identity/federation_page.go |
| FedereSayfalar.hataSayfasi | FederationPages.errorPage | method | internal/identity/federation_page.go |
| FedereSayfalar.istekTamamlamaHatasi | FederationPages.writeCompletionError | method | internal/identity/federation_page.go |
| FedereSayfalar.Baslat | FederationPages.Start | method | internal/identity/federation_page.go |
| FedereSayfalar.stateVeKod | FederationPages.stateAndCode | method | internal/identity/federation_page.go |
| FedereSayfalar.Geri | FederationPages.Callback | method | internal/identity/federation_page.go |
| hizLimitOnEki | rateLimitPrefix | const | internal/identity/ratelimit.go |
| hizLimitRedisZamanAsimi | rateLimitRedisTimeout | const | internal/identity/ratelimit.go |
| hataHizLimit | msgRateLimited | const | internal/identity/ratelimit.go |
| hataHizLimitDenetlenemedi | msgRateLimitUnavailable | const | internal/identity/ratelimit.go |
| HizLimitAyar | RateLimitConfig | type | internal/identity/ratelimit.go |
| HizLimitAyar.GirisIPPerMin | RateLimitConfig.LoginIPPerMin | field | internal/identity/ratelimit.go |
| HizLimitAyar.GirisEpostaPerSaat | RateLimitConfig.LoginEmailPerHour | field | internal/identity/ratelimit.go |
| HizLimitAyar.KayitIPPerSaat | RateLimitConfig.SignupIPPerHour | field | internal/identity/ratelimit.go |
| HizLimitAyar.GuvenilenProxy | RateLimitConfig.TrustedProxies | field | internal/identity/ratelimit.go |
| HizLimit | RateLimiter | type | internal/identity/ratelimit.go |
| HizLimit.ayar | RateLimiter.cfg | field | internal/identity/ratelimit.go |
| HizLimit.epostaAnahtari | RateLimiter.emailHMACKey | field | internal/identity/ratelimit.go |
| NewHizLimit | NewRateLimiter | func | internal/identity/ratelimit.go |
| sayac | counter | type | internal/identity/ratelimit.go |
| sayac.anahtar | counter.key | field | internal/identity/ratelimit.go |
| sayac.sinir | counter.limit | field | internal/identity/ratelimit.go |
| sayac.pencere | counter.window | field | internal/identity/ratelimit.go |
| HizLimit.GirisSar | RateLimiter.WrapLogin | method | internal/identity/ratelimit.go |
| HizLimit.SayfaGetSar | RateLimiter.WrapPageGet | method | internal/identity/ratelimit.go |
| HizLimit.BaglamaSar | RateLimiter.WrapLink | method | internal/identity/ratelimit.go |
| HizLimit.HesapSayaci | RateLimiter.AllowAccount | method | internal/identity/ratelimit.go |
| HizLimit.FederasyonBaslatSar | RateLimiter.WrapFederationStart | method | internal/identity/ratelimit.go |
| HizLimit.FederasyonGeriSar | RateLimiter.WrapFederationCallback | method | internal/identity/ratelimit.go |
| HizLimit.KayitSar | RateLimiter.WrapSignup | method | internal/identity/ratelimit.go |
| HizLimit.izinVer | RateLimiter.allow | method | internal/identity/ratelimit.go |
| HizLimit.artir | RateLimiter.incr | method | internal/identity/ratelimit.go |
| HizLimit.bekleSaniye | RateLimiter.retryAfterSeconds | method | internal/identity/ratelimit.go |
| HizLimit.ipAnahtar | RateLimiter.ipKey | method | internal/identity/ratelimit.go |
| epostaSayacAnahtari | emailCounterInput | func | internal/identity/ratelimit.go |
| HizLimit.epostaAnahtar | RateLimiter.emailKey | method | internal/identity/ratelimit.go |
| istemciIP | clientIP | func | internal/identity/ratelimit.go |
| istekTTL | requestTTL | const | internal/identity/request.go |
| kodTTL | codeTTL | const | internal/identity/request.go |
| amrSifre | amrPassword | const | internal/identity/request.go |
| amrFederasyon | amrFederation | const | internal/identity/request.go |
| ErrIstekYok | ErrRequestNotFound | var | internal/identity/request.go |
| ErrIstekZatenTamamlandi | ErrRequestAlreadyCompleted | var | internal/identity/request.go |
| AuthIstek | AuthRequest | type | internal/identity/request.go |
| AuthIstek.IpucuSubject | AuthRequest.HintSubject | field (its json:"IpucuSubject" tag was added in Task 7) | internal/identity/request.go |
| (AuthIstek.GetID … Done, the op.AuthRequest methods) | unchanged; receiver becomes AuthRequest | manual: method ; receiver becomes AuthRequest | internal/identity/request.go |
| IstekDepo | RequestStore | type | internal/identity/request.go |
| NewIstekDepo | NewRequestStore | func | internal/identity/request.go |
| istekAnahtar | requestKey | func | internal/identity/request.go |
| kodAnahtar | codeKey | func | internal/identity/request.go |
| IstekDepo.Olustur | RequestStore.Create | method | internal/identity/request.go |
| IstekDepo.yaz | RequestStore.write | method | internal/identity/request.go |
| IstekDepo.IDileOku | RequestStore.GetByID | method | internal/identity/request.go |
| IstekDepo.KodKaydet | RequestStore.SaveCode | method | internal/identity/request.go |
| IstekDepo.KodlaOku | RequestStore.GetByCode | method | internal/identity/request.go |
| IstekDepo.Sil | RequestStore.Delete | method | internal/identity/request.go |
| IstekDepo.TamamlandiIsaretle | RequestStore.MarkDone | method | internal/identity/request.go |
| oturumAnahtar | sessionKey | func | internal/identity/request.go |
| IstekDepo.OturumYaz | RequestStore.WriteSession | method | internal/identity/request.go |
| IstekDepo.OturumOku | RequestStore.ReadSession | method | internal/identity/request.go |
| cerezAdOturum | cookieNameSession | const | internal/identity/session.go |
| oturumBaytSayisi | sessionTokenBytes | const | internal/identity/session.go |
| oturumBaglamaUstSinir | maxSessionBindings | const | internal/identity/session.go |
| ErrOturumYok | ErrSessionNotFound | var | internal/identity/session.go |
| ErrOturumEslesmedi | ErrSessionMismatch | var | internal/identity/session.go |
| ErrOturumCerezYok | ErrSessionCookieMissing | var | internal/identity/session.go |
| OturumKayit | SessionEntry | type | internal/identity/session.go |
| OturumBaglama | SessionBinding | type | internal/identity/session.go |
| OturumBaglama.Kayitlar | SessionBinding.Entries | field (its json:"Kayitlar" tag was added in Task 7) | internal/identity/session.go |
| OturumBaglama.CSRFBul | SessionBinding.CSRFFor | method | internal/identity/session.go |
| OturumBaglama.Ekle | SessionBinding.Add | method | internal/identity/session.go |
| OturumDepo | SessionStore | type (interface) | internal/identity/session.go |
| OturumDepo.OturumYaz | SessionStore.WriteSession | interface method | internal/identity/session.go |
| OturumDepo.OturumOku | SessionStore.ReadSession | interface method | internal/identity/session.go |
| rasgeleJeton | randomToken | func | internal/identity/session.go |
| csrfEsit | csrfEqual | func | internal/identity/session.go |
| oturumCerezi | sessionCookie | func | internal/identity/session.go |
| oturumBaglamaDogrula | verifySessionBinding | func | internal/identity/session.go |
| SahteOturumDepo | FakeSessionStore | type | internal/identity/session_fake.go |
| SahteOturumDepo.kayitlar | FakeSessionStore.entries | field | internal/identity/session_fake.go |
| sahteOturumKaydi | fakeSessionEntry | type | internal/identity/session_fake.go |
| sahteOturumKaydi.baglama | fakeSessionEntry.binding | field | internal/identity/session_fake.go |
| sahteOturumKaydi.biter | fakeSessionEntry.expiresAt | field | internal/identity/session_fake.go |
| NewSahteOturumDepo | NewFakeSessionStore | func | internal/identity/session_fake.go |
| SahteOturumDepo.OturumYaz | FakeSessionStore.WriteSession | method | internal/identity/session_fake.go |
| SahteOturumDepo.OturumOku | FakeSessionStore.ReadSession | method | internal/identity/session_fake.go |
| yolGiris | pathLogin | const | internal/identity/page.go |
| yolKayit | pathSignup | const | internal/identity/page.go |
| csrfAlan | fieldCSRF | const | internal/identity/page.go |
| IstekTamamlayici | RequestCompleter | type (interface) | internal/identity/page.go |
| IstekTamamlayici.TamamlandiIsaretle | RequestCompleter.MarkDone | interface method | internal/identity/page.go |
| sablonFS | templateFS | var | internal/identity/page.go |
| sablonlar | templates | var | internal/identity/page.go |
| hataGirisBasarisiz | msgLoginFailed | const | internal/identity/page.go |
| hataSunucu | msgServerError | const | internal/identity/page.go |
| hataEpostaKullanimda | msgEmailInUse | const | internal/identity/page.go |
| hataOturum | msgSessionInvalid | const | internal/identity/page.go |
| hataIstekBagli | msgRequestAlreadyUsed | const | internal/identity/page.go |
| Sayfalar | Pages | type | internal/identity/page.go |
| Sayfalar.kullanici | Pages.users | field | internal/identity/page.go |
| Sayfalar.istekler | Pages.requests | field | internal/identity/page.go |
| Sayfalar.oturumlar | Pages.sessions | field | internal/identity/page.go |
| Sayfalar.geriCagirma | Pages.authCallback | field | internal/identity/page.go |
| Sayfalar.cerezGuvenli | Pages.secureCookie | field | internal/identity/page.go |
| Sayfalar.callbackDelege | Pages.callbackDelegate | field | internal/identity/page.go |
| Sayfalar.federasyonlar | Pages.federationProviders | field | internal/identity/page.go |
| NewSayfalar | NewPages | func | internal/identity/page.go |
| Sayfalar.CallbackDelege | Pages.SetCallbackDelegate | method | internal/identity/page.go |
| Sayfalar.FederasyonSaglayicilari | Pages.SetFederationProviders | method | internal/identity/page.go |
| Sayfalar.federasyonButonlari | Pages.federationButtons | method | internal/identity/page.go |
| Sayfalar.Bagla | Pages.Mount | method | internal/identity/page.go |
| Sayfalar.BaglamaKur | Pages.BindSession | method | internal/identity/page.go |
| Sayfalar.AuthorizeSar | Pages.WrapAuthorize | method | internal/identity/page.go |
| baslikFetchSite | headerFetchSite | const | internal/identity/page.go |
| baslikFetchMode | headerFetchMode | const | internal/identity/page.go |
| fetchSiteYok | fetchSiteNone | const | internal/identity/page.go |
| fetchSiteAyniOrigin | fetchSiteSameOrigin | const | internal/identity/page.go |
| fetchSiteAyniSite | fetchSiteSameSite | const | internal/identity/page.go |
| fetchSiteCapraz | fetchSiteCrossSite | const | internal/identity/page.go |
| caprazSiteIstek | isCrossSite | func | internal/identity/page.go |
| girisYonlendirmeID | loginRedirectID | func | internal/identity/page.go |
| baglamaYakalayici | bindingInterceptor | type | internal/identity/page.go |
| baglamaYakalayici.kur | bindingInterceptor.bind | field | internal/identity/page.go |
| baglamaYakalayici.yazildi | bindingInterceptor.wroteHeader | field | internal/identity/page.go |
| baglamaYakalayici.bastirildi | bindingInterceptor.suppressed | field | internal/identity/page.go |
| baglamaYakalayici.WriteHeader / Write | bindingInterceptor.WriteHeader / Write | manual: method / Write | internal/identity/page.go |
| Sayfalar.oturumZorla | Pages.enforceSession | method | internal/identity/page.go |
| oturumRed | rejectSession | func | internal/identity/page.go |
| Sayfalar.Giris | Pages.Login | method | internal/identity/page.go |
| Sayfalar.girisGet | Pages.loginGet | method | internal/identity/page.go |
| Sayfalar.girisPost | Pages.loginPost | method | internal/identity/page.go |
| Sayfalar.girisRender | Pages.renderLogin | method | internal/identity/page.go |
| Sayfalar.Kayit | Pages.Signup | method | internal/identity/page.go |
| Sayfalar.kayitGet | Pages.signupGet | method | internal/identity/page.go |
| Sayfalar.kayitPost | Pages.signupPost | method | internal/identity/page.go |
| Sayfalar.kayitRender | Pages.renderSignup | method | internal/identity/page.go |
| Sayfalar.Callback | Pages.Callback | method | internal/identity/page.go |
| YukariAkis | Upstream | type | internal/identity/upstream.go |
| YukariAkis.Ad | Upstream.Name | field | internal/identity/upstream.go |
| YukariAkis.IstemciID | Upstream.ClientID | field | internal/identity/upstream.go |
| YukariAkis.IstemciSir | Upstream.ClientSecret | field | internal/identity/upstream.go |
| YukariAkis.Kapsamlar | Upstream.Scopes | field | internal/identity/upstream.go |
| YukariAkis.PKCEDestekli | Upstream.SupportsPKCE | field | internal/identity/upstream.go |
| YukariAkis.AppleEkipID | Upstream.AppleTeamID | field | internal/identity/upstream.go |
| YukariAkis.AppleAnahtarID | Upstream.AppleKeyID | field | internal/identity/upstream.go |
| YukariAkis.AppleAnahtarP8 | Upstream.AppleKeyP8 | field | internal/identity/upstream.go |
| kodDogrulayiciUretBaytSayisi | codeVerifierBytes | const | internal/identity/upstream.go |
| kodDogrulayiciUret | generateCodeVerifier | func | internal/identity/upstream.go |
| FedereDurum | FederationState | type | internal/identity/upstream.go |
| FedereDurum.Saglayici | FederationState.Provider | field (its json:"Saglayici" tag was added in Task 7) | internal/identity/upstream.go |
| FedereDurum.Cerez | FederationState.Cookie | field (its json:"Cerez" tag was added in Task 7) | internal/identity/upstream.go |
| FedereDurum.KodDogrulayici | FederationState.CodeVerifier | field (its json:"KodDogrulayici" tag was added in Task 7) | internal/identity/upstream.go |
| ErrDurumYok | ErrStateNotFound | var | internal/identity/upstream.go |
| DurumDepo | StateStore | type (interface) | internal/identity/upstream.go |
| DurumDepo.DurumYaz | StateStore.WriteState | interface method | internal/identity/upstream.go |
| DurumDepo.DurumAl | StateStore.TakeState | interface method | internal/identity/upstream.go |
| federasyonDurumTTL | federationStateTTL | const | internal/identity/upstream.go |
| durumAnahtar | stateKey | func | internal/identity/upstream.go |
| IstekDepo.DurumYaz | RequestStore.WriteState | method | internal/identity/upstream.go |
| IstekDepo.DurumAl | RequestStore.TakeState | method | internal/identity/upstream.go |
| saglayiciApple | providerApple | const | internal/identity/upstream_apple.go |
| saglayiciGoogle | providerGoogle | const | internal/identity/upstream_apple.go |
| appleIstemciSirOmru | appleClientSecretTTL | const | internal/identity/upstream_apple.go |
| appleIstemciSir | appleClientSecret | func | internal/identity/upstream_apple.go |
| ErrYukariAkisYanit | ErrUpstreamResponse | var | internal/identity/upstream_token.go |
| yukariAkisZamanAsimi | upstreamTimeout | const | internal/identity/upstream_token.go |
| yukariAkisYanitSiniri | upstreamResponseLimit | const | internal/identity/upstream_token.go |
| YukariAkis.JetonDegis | Upstream.ExchangeCode | method | internal/identity/upstream_token.go |
| (anon struct in JetonDegis).Hata | .Error (tag json:"error" unchanged) | manual: field | internal/identity/upstream_token.go |
| sahteFedereIstekler.IDileOku | fakeFederationRequests.GetByID | method | internal/identity/federation_page_test.go |
| sahteFedereIstekler.TamamlandiIsaretle | fakeFederationRequests.MarkDone | method | internal/identity/federation_page_test.go |
| sahteTamamlayici.TamamlandiIsaretle | fakeCompleter.MarkDone | method | internal/identity/page_test.go |

Rows whose kind starts with `manual:` are skipped by `apply_renames.py`. Do each by hand:
- An **embedded field** row needs no action. Renaming the embedded type renames the field and every selector on it. Check the result in the diff.
- A **`var _ X = (*Y)(nil)`** row needs no action. The assertion follows the renamed types. Check it.
- An **anonymous struct field**, **local** or **param** row: run `~/go/bin/gopls rename -w <file>:<line>:<col> <new>` on the declaring identifier.
- A **package** row is done by the package-move step.
- A **"methods unchanged; receiver becomes ..."** row needs no action. The methods keep their `op.*` names and follow the type rename.

**Wire strings to keep (must stay byte-identical)**

_From the kimlik glossary, §4.1 HTTP routes, route params, query and form fields:_

| file:line | literal | why |
|---|---|---|
| sayfa.go:18 | `"/giris"` | hosted login page path. MobileClient.LoginURL redirects here |
| sayfa.go:19 | `"/kayit"` | signup page path |
| baglama_sayfa.go:22 | `"/baglama"` | account-link page path (CLAUDE.md documents it) |
| federe_sayfa.go:21 | `"/federasyon/{saglayici}/baslat"` | federation start route |
| federe_sayfa.go:22 | `"/federasyon/{saglayici}/geri"` | federation callback route. **Registered as redirect_uri at Google/Apple** (.env.example:105,109) |
| federe_sayfa.go:27 | `"{saglayici}"` | ServeMux wildcard; must match the `PathValue` key below |
| federe_sayfa.go:328 | `"saglayici"` | `r.PathValue` key (route param) |
| federe_sayfa.go:218 | `"/federasyon/"`, `"/geri"` | redirect_uri sent upstream; must equal the registered URI |
| federe_sayfa.go:239 | `"/federasyon"` | cookie Path of `kimlik_fed` |
| sayfa.go:179, 406, 490, 508, 591, 609; baglama_sayfa.go:376; federe_sayfa.go:335 | `"authRequestID"` | query/form field name |
| baglama_sayfa.go:25 | `"beklemeID"` | /baglama query/form field (template line 21) |
| sayfa.go:23 | `"csrf"` | form field name (all templates) |
| sayfa.go:524, 621; hizlimit.go:153 | `"eposta"` | login/signup form field |
| sayfa.go:525, 623; baglama_sayfa.go:515 | `"sifre"` | password form field |
| sayfa.go:622 | `"ad"` | signup name form field |
| sayfa.go:698 | `"id"` | `/authorize/callback` param (zitadel) |
| federe_sayfa.go:400, 403 | `"state"`, `"code"` | OAuth callback params (GET query and Apple form_post) |
| sayfa.go:217, 220-223; baglama_sayfa.go:417-418; federe_sayfa.go:207-209 | `"GET "`, `"POST "` | method prefix of each mux pattern |

_From the kimlik glossary, §4.2 Cookies:_

| file:line | literal | why |
|---|---|---|
| oturum.go:33 | `"kimlik_oturum"` | session cookie name. Live browsers hold it |
| oturum.go:167 | `"/"` | session cookie Path |
| federe_sayfa.go:55 | `"kimlik_fed"` | federation cookie name |
| federe_sayfa.go:239-243 | `Path "/federasyon"`, HttpOnly, SameSite=None, MaxAge | the clearing cookie must match these attributes exactly |

_From the kimlik glossary, §4.3 Redis key formats:_

| file:line | literal | why |
|---|---|---|
| istek.go:163 | `"kimlik:istek:"` | auth request JSON (10 min TTL) |
| istek.go:164 | `"kimlik:kod:"` | auth code → request id |
| istek.go:303 | `"kimlik:oturum:"` | session binding JSON |
| yukari_akis.go:142 | `"kimlik:fed:"` | federation state JSON |
| baglama_sayfa.go:89 | `"kimlik:baglama:"` | pending-link JSON |
| hizlimit.go:44 | `"kimlik:hizlimit"` | rate-limit prefix (CLAUDE.md documents it) |
| hizlimit.go:384 | `"%s:%s:ip:%s"` | IP counter key layout |
| hizlimit.go:414 | `"%s:%s:eposta:%s"` | email/account counter key layout (`eposta` segment) |
| hizlimit.go:139, 161 | `"giris"` | counter names (POST /giris IP and email) |
| hizlimit.go:187 | `"-get"` | suffix for GET counters |
| sayfa.go:217 / 218 | `"giris"` / `"kayit"` | SayfaGetSar names, giving `giris-get` and `kayit-get` keys |
| baglama_sayfa.go:414 | `"baglama"` | SayfaGetSar name, giving the `baglama-get` key |
| hizlimit.go:216 | `"baglama"` | POST /baglama IP counter |
| hizlimit.go:252 | `"baglama-hesap"` | per-account counter |
| hizlimit.go:279 | `"federasyon-baslat"` | federation-start counter |
| hizlimit.go:300 | `"federasyon-geri"` | federation-callback counter |
| hizlimit.go:318 | `"kayit"` | POST /kayit IP counter |
| hizlimit.go:358 | `"NX"` | Redis SET mode |

_From the kimlik glossary, §4.4 SQL (table, column, index and constraint names; text unchanged):_

| file:line | literal | why |
|---|---|---|
| federe_depo.go:54, 71, 121, 137 | `federe_kimlikler` (saglayici, sub, user_id), `users` join | DB schema |
| federe_depo.go:111 | `INSERT INTO users … NULL …` | DB schema |
| kullanici_depo.go:73; federe_depo.go:79, 115, 124 | `"23505"` | SQLSTATE unique_violation |
| yukari_akis_apple.go:18-19 | `"apple"`, `"google"` | provider names. They are stored in `federe_kimlikler.saglayici`, appear in the `/federasyon/{p}/…` paths and config `Ad`, and sit in Redis state JSON |

_From the kimlik glossary, §4.5 OAuth / OIDC protocol strings:_

| file:line | literal | why |
|---|---|---|
| istek.go:27-28 | `"pwd"`, `"fed"` | `amr` claim values in issued tokens |
| yukari_akis.go:59-71 | `client_id`, `response_type`, `code`, `scope`, `" "`, `state`, `nonce`, `redirect_uri`, `response_mode`, `form_post`, `code_challenge`, `code_challenge_method`, `S256` | upstream authorize request |
| yukari_akis_jeton.go:54-60 | `grant_type`, `authorization_code`, `code`, `redirect_uri`, `client_id`, `client_secret`, `code_verifier` | upstream token request |
| yukari_akis_jeton.go:67 | `Content-Type: application/x-www-form-urlencoded` | upstream token request |
| yukari_akis_jeton.go:87-88 | tags `json:"id_token"`, `json:"error"` | upstream token response |
| yukari_akis_apple.go:42-48 | `iss`, `iat`, `exp`, `aud`, `"https://appleid.apple.com"`, `sub`, `kid` | Apple client_secret JWT |
| baglama_sayfa.go:34 | `"@privaterelay.appleid.com"` | Apple relay domain |

_From the kimlik glossary, §4.6 HTTP headers:_

| file:line | literal | why |
|---|---|---|
| sayfa.go:340-341 | `"Sec-Fetch-Site"`, `"Sec-Fetch-Mode"` | fetch-metadata headers |
| sayfa.go:356-359 | `"none"`, `"same-origin"`, `"same-site"`, `"cross-site"` | header values |
| sayfa.go:313, 432 | `"Location"` | header |
| hizlimit.go:341, 346 | `"Retry-After"`, `"5"` | header |
| hizlimit.go:452 | `"X-Forwarded-For"` | header |

_From the kimlik glossary, §4.7 User-facing text (the localization task will change it; this refactor keeps it byte-identical):_

| file:line | literal |
|---|---|
| sayfa.go:40 | `"E-posta veya sifre hatali."` |
| sayfa.go:44 | `"Bir hata olustu, lutfen tekrar deneyin."` |
| sayfa.go:50 | `"Bu e-posta zaten kullaniliyor."` |
| sayfa.go:56 | `"Oturum dogrulanamadi, lutfen giris sayfasini yeniden acin."` |
| sayfa.go:61 | `"Bu giris istegi zaten kullanildi, lutfen uygulamadan tekrar deneyin."` |
| federe_sayfa.go:59 | `"Giris istegi gecersiz, lutfen uygulamadan tekrar deneyin."` |
| federe_sayfa.go:75 | `"Giris isteginin suresi doldu. Lutfen uygulamaya donup girisi yeniden baslatin."` |
| federe_sayfa.go:81 | `"Saglayici e-posta adresinizi bize vermedigi icin yeni bir hesap olusturulamadi. Lutfen e-posta ve sifre ile giris yapin ya da diger giris yontemini kullanin."` |
| hizlimit.go:53 | `"Cok fazla deneme yapildi, lutfen biraz sonra tekrar deneyin."` |
| hizlimit.go:64 | `"Istek su an denetlenemiyor, lutfen biraz sonra tekrar deneyin."` |
| sayfa.go:183 | `" ile giris yap"` (button label suffix) |
| baglama_sayfa.go:344, 346 | `"Google"`, `"Apple"` (labels) |
| baglama_sayfa.go:358 | `"baska bir yontem"` |
| baglama_sayfa.go:366 | `" veya "` |
| sayfa.go:492, 510, 593, 611 | `"authRequestID eksik"` (400 body) |
| sayfa.go:505, 606, 695; hizlimit.go:149; baglama_sayfa.go:469 | `"form okunamadi"` (400 body) |
| sayfa.go:700 | `"id eksik"` (400 body) |

**Risks (from the glossaries; names in them are the old ones)**

_From the kimlik glossary, risk R1:_

**R1: Redis JSON without tags. A field rename changes stored data and breaks in-flight flows.**
`AuthIstek`, `OturumBaglama`/`OturumKayit`, `FedereDurum`, `BaglamaBekleme` and `AccessKayit` are `json.Marshal`ed into Redis with no tags. Renaming `IpucuSubject`, `Kayitlar`, `Saglayici`, `Cerez`, `KodDogrulayici` or `Eposta` without tags would do the following during a rolling deploy:
- every in-progress login becomes 403 (the session binding comes back empty);
- federation callbacks fail with "provider not configured" (`Saglayici` = "");
- pending links lose the provider and email.

Fix: add explicit tags with the **old** names (§3). The English-named fields need no tag, but adding identical tags is harmless and documents intent. Test files never build these JSON blobs by hand.

_From the kimlik glossary, risk R3:_

**R3: error text that is user-visible or asserted.**
- `ErrSifreKisa.Error()` is rendered into kayit.html (sayfa.go:631), so keep `"sifre en az 10 karakter olmali"`, or decouple it into a msg const with the same text.
- `oidc.ErrInvalidClient().WithDescription("jeton bu istemci icin verilmemis")` reaches the client.
- Tests match error substrings:
  - sunucu_test.go:349 `"veritabanina baglanilamadi"` (the skip detection; translating it without the test makes the test FAIL instead of SKIP when no Postgres is present);
  - sunucu_test.go:373 `"hiz limiti kurulamadi"`;
  - the env-var and config-key substrings (4.9), plus `"https"` (sunucu_test.go:50, 65, 80).

  Translate source and test in the same change.
- hizlimit.go:109-118 messages cite **stale** config keys (`giris_ip_per_min` and so on). While translating, use the real keys `login_ip_per_min`, `login_email_per_hour`, `signup_ip_per_hour` and `trusted_proxies`. No test asserts on them.

_From the kimlik glossary, risk R5:_

**R5: name collisions and shadowing after rename.**
- Field and method names must differ on the same type. `FedereSayfalar.geriCagirma` and `geriURL` would both want "callbackURL", so the plan uses `authCallback` (field) and `upstreamRedirectURI` (method). `fedOrtam.geri` → `callback` and `.baslat` → `start` are methods on a different type, so they are fine.
- Package level:
  - type `Storage` vs the `storage op.Storage` parameter in `pkceEnforcer.ValidateAuthRequest`: the parameter shadows the type only inside that function, which never uses the type. Harmless.
  - type `SigningKey` vs method `Storage.SigningKey`, and type `PublicKey` vs method `SigningKey.PublicKey`: legal.
  - `Start` (func) vs `FederationPages.Start` (method): legal.
  - `Server.ready` field vs `Ready()` method: legal.
- Test helpers: `al`/`govde` would naturally become `get`/`body`, but many tests already have locals named `body`. That is why the plan uses `mustGet`/`readBody`. Similarly `stepContext` (func) vs `TestStepContext` is fine, and `bindTestSession` (sayfa_test func) is kept apart from `fedEnv.bindSession`.
- Federation-page and authorize-callback tests would both start `TestCallback_…`, so the plan prefixes `TestFederationStart_`, `TestFederationCallback_` and `TestAuthorizeCallback_`.
- Two terms have to stay distinct:
  - "baglama" means **session binding** and also **account link**. Do not merge `BindSession`/`SessionBinding` with `Link`/`PendingLink`.
  - "kayit" means **record** and also **signup**.

_From the kimlik glossary, risk R6:_

**R6: other reflection and string lookups.**
- `go:embed` patterns: `sablonlar/*.html` (renamable with care) and `migrations/*.sql` (keep).
- No SQL is built from Go names; all SQL is literal. No viper `mapstructure` tags in scope. The only struct tags in non-test source are `json:"id_token"`/`json:"error"`.
- `r.PathValue("saglayici")` must match `{saglayici}` in the route pattern. Both stay Turkish (route param = wire).
- `SahteFedereDepo.eslesmeSayisi` is declared in a **test** file on a prod type, so rename it in sahte_yukari_akis_test.go alongside the fake.

_From the kimlik glossary, risk R8:_

**R8: log text may feed alerts.** Logs such as `"refresh yeniden kullanimi tespit edildi, aile iptal edildi: family_id=%s"` and `"hiz limiti denetlenemedi, istek reddedildi (fail-closed)"` become English. No dashboard or alert rule in the repo greps them; I searched all non-Go files. Still, tell ops, and keep the `family_id=` token. `federe_sayfa_test.go:1074` (`TestGeriLoglarSizdirmaz`) checks that secrets are absent from logs, not the wording, so it is unaffected.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 11 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 3: Do the `manual:` rows**

Follow the rules under the table. Then run:

```bash
go build ./... && go vet ./...
```

- [ ] **Step 4: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).

Files (12): `internal/identity/federated_store.go`, `internal/identity/federated_store_fake.go`, `internal/identity/federation_page.go`, `internal/identity/link_page.go`, `internal/identity/page.go`, `internal/identity/ratelimit.go`, `internal/identity/request.go`, `internal/identity/session.go`, `internal/identity/session_fake.go`, `internal/identity/upstream.go`, `internal/identity/upstream_apple.go`, `internal/identity/upstream_token.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/identity/federated_store.go internal/identity/federated_store_fake.go internal/identity/federation_page.go internal/identity/link_page.go internal/identity/page.go internal/identity/ratelimit.go internal/identity/request.go internal/identity/session.go internal/identity/session_fake.go internal/identity/upstream.go internal/identity/upstream_apple.go internal/identity/upstream_token.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 5: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 11 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 6: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 7: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(identity): sayfalar, oturum, federasyon Ingilizce adlandirma

Giris/kayit sayfalari, oturum baglama, auth istekleri, federasyon,
yukari akis ve hiz limiti tanimlayicilari Ingilizceye cevrildi.
Cerez adlari, rotalar ve Redis anahtarlari ayni; kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 12: identity: test helpers, fakes and test names

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

_No file in this task changes its name._

The files were moved and renamed in Task 8.

**Declarations** (473 rows)

<!-- renames:12 -->
| old | new | kind | file |
|---|---|---|---|
| testCryptoAnahtari | testCryptoKey | const | internal/identity/flow_test.go |
| akisOrtami | flowEnv | type | internal/identity/flow_test.go |
| akisOrtami.kullanici | flowEnv.users | field | internal/identity/flow_test.go |
| akisOrtami.istekler | flowEnv.requests | field | internal/identity/flow_test.go |
| akisOrtami.federe | flowEnv.federated | field | internal/identity/flow_test.go |
| akisOrtami.yukari | flowEnv.upstreams | field | internal/identity/flow_test.go |
| testSunucu | newTestServer | func | internal/identity/flow_test.go |
| testSunucuAyarli | newTestServerWithLimits | func | internal/identity/flow_test.go |
| testSunucuFedere | newTestServerFederated | func | internal/identity/flow_test.go |
| testSunucuKur | setupTestServer | func | internal/identity/flow_test.go |
| testSunucuKurAyarli | setupTestServerWithUpstream | func | internal/identity/flow_test.go |
| tarayici | newBrowser | func | internal/identity/flow_test.go |
| akisOrtami.authorizeURL | flowEnv.authorizeURL | method | internal/identity/flow_test.go |
| akisOrtami.authorizeURLKapsamli | flowEnv.authorizeURLWithScope | method | internal/identity/flow_test.go |
| al | mustGet | func | internal/identity/flow_test.go |
| govde | readBody | func | internal/identity/flow_test.go |
| authRequestIDCikar | extractAuthRequestID | func | internal/identity/flow_test.go |
| akisCSRFDeseni | flowCSRFPattern | var | internal/identity/flow_test.go |
| akisOrtami.girisDene | flowEnv.tryLogin | method | internal/identity/flow_test.go |
| akisOrtami.girisFormuAc | flowEnv.openLoginForm | method | internal/identity/flow_test.go |
| akisOrtami.girisGonder | flowEnv.submitLogin | method | internal/identity/flow_test.go |
| akisOrtami.girisYap | flowEnv.login | method | internal/identity/flow_test.go |
| kodCikar | extractCode | func | internal/identity/flow_test.go |
| akisOrtami.jetonAl | flowEnv.exchangeToken | method | internal/identity/flow_test.go |
| akisOrtami.testKullanici | flowEnv.createTestUser | method | internal/identity/flow_test.go |
| devralmaDogrula | assertNoTakeover | func | internal/identity/flow_test.go |
| pkceRed | assertPKCERejected | func | internal/identity/flow_test.go |
| alBaslikli | getWithFetchSite | func | internal/identity/flow_test.go |
| fedButonDeseni | fedButtonPattern | var | internal/identity/flow_test.go |
| (anon).ad / .id / .verifier in TestParalelIkiAkis… | .name / .id / .verifier | manual: field | internal/identity/flow_test.go |
| (anon).DeviceUcu / .TokenUcu in TestDiscoveryDesteklenmeyen… | .DeviceEndpoint / .TokenEndpoint (tags unchanged) | manual: field | internal/identity/flow_test.go |
| testSifre | testPassword | const | internal/identity/link_page_test.go |
| iddiaKur | buildClaims | func | internal/identity/link_page_test.go |
| fedOrtam.oturumBagla | fedEnv.bindSession | method | internal/identity/link_page_test.go |
| fedOrtam.baglamaGetIstek | fedEnv.linkGetRequest | method | internal/identity/link_page_test.go |
| fedOrtam.baglamaPostIstek | fedEnv.linkPostRequest | method | internal/identity/link_page_test.go |
| fedOrtam.beklemeKur | fedEnv.setupPendingLink | method | internal/identity/link_page_test.go |
| (anon).ad / .cerez in TestBaglamaGetBaglamasiz… | .name / .cookie | manual: field | internal/identity/link_page_test.go |
| kavanozHedef | jarTarget | const | internal/identity/cookie_jar_test.go |
| (anon).ad/.sameSite/.baglam/.beklenen; .cerezYol/.istekYol; .kaynak/.hedef/.yontem | .name/.sameSite/.ctx/.want; .cookiePath/.requestPath; .from/.to/.method | manual: field | internal/identity/cookie_jar_test.go |
| izlemeTokenStore | trackingTokenStore | type | internal/identity/storage_revoke_test.go |
| izlemeTokenStore.kullaniciIptalSayisi | trackingTokenStore.revokeUserCalls | field | internal/identity/storage_revoke_test.go |
| testDepoJetonlu | newTestStorageWithTokens | func | internal/identity/storage_revoke_test.go |
| testDepo | newTestStorage | func | internal/identity/storage_test.go |
| hataliByIDUserStore | failingByIDUserStore | type | internal/identity/storage_test.go |
| errKullaniciOkunamadi | errUserUnreadable | var | internal/identity/storage_test.go |
| sahteIDTokenIstek | fakeIDTokenRequest | type | internal/identity/storage_test.go |
| refreshAkisiTaklit | simulateRefreshFlow | func | internal/identity/storage_test.go |
| akisOrtami.kullaniciBilgisi | flowEnv.userinfo | method | internal/identity/federation_flow_test.go |
| akisOrtami.federasyonKodu | flowEnv.federationCode | method | internal/identity/federation_flow_test.go |
| akisOrtami.federasyonJetonu | flowEnv.federationTokens | method | internal/identity/federation_flow_test.go |
| erisimJetonuSub | accessTokenSub | func | internal/identity/federation_flow_test.go |
| yukariAkisKoduAl | upstreamCallbackURL | func | internal/identity/federation_flow_test.go |
| federeKurulum | federatedSetup | type | internal/identity/federated_store_contract_test.go |
| federeKurulum.ad | federatedSetup.name | field | internal/identity/federated_store_contract_test.go |
| federeKurulum.depo | federatedSetup.store | field | internal/identity/federated_store_contract_test.go |
| federeKurulum.kullanici | federatedSetup.users | field | internal/identity/federated_store_contract_test.go |
| federeKurulumKurucu | federatedSetupFactory | type | internal/identity/federated_store_contract_test.go |
| federeKurulumKurucu.ad | federatedSetupFactory.name | field | internal/identity/federated_store_contract_test.go |
| federeKurulumKurucu.kur | federatedSetupFactory.build | field | internal/identity/federated_store_contract_test.go |
| federeDepoKurulumlari | federatedStoreSetups | func | internal/identity/federated_store_contract_test.go |
| fedKid | fedKID | func | internal/identity/federation_page_test.go |
| fedRSAAnahtar | fedRSAKey | func | internal/identity/federation_page_test.go |
| sahteSaglayici | fakeProvider | type | internal/identity/federation_page_test.go |
| sahteSaglayici.ad | fakeProvider.providerName | field | internal/identity/federation_page_test.go |
| sahteSaglayici.istemciID | fakeProvider.clientID | field | internal/identity/federation_page_test.go |
| sahteSaglayici.sunucu | fakeProvider.server | field | internal/identity/federation_page_test.go |
| sahteSaglayici.yayinAnahtar | fakeProvider.publishedKey | field | internal/identity/federation_page_test.go |
| sahteSaglayici.imzaAnahtar | fakeProvider.signingKey | field | internal/identity/federation_page_test.go |
| sahteSaglayici.eposta | fakeProvider.email | field | internal/identity/federation_page_test.go |
| sahteSaglayici.isim | fakeProvider.name | field | internal/identity/federation_page_test.go |
| sahteSaglayici.epostaDogrulandi | fakeProvider.emailVerified | field | internal/identity/federation_page_test.go |
| sahteSaglayici.audZorla | fakeProvider.forceAud | field | internal/identity/federation_page_test.go |
| sahteSaglayici.issZorla | fakeProvider.forceIss | field | internal/identity/federation_page_test.go |
| sahteSaglayici.tokenSayisi | fakeProvider.tokenCount | field | internal/identity/federation_page_test.go |
| sahteSaglayici.sonKod | fakeProvider.lastCode | field | internal/identity/federation_page_test.go |
| sahteSaglayici.sonDogrulayici | fakeProvider.lastVerifier | field | internal/identity/federation_page_test.go |
| sahteSaglayici.uretilenTokenlar | fakeProvider.issuedTokens | field | internal/identity/federation_page_test.go |
| yeniSahteSaglayici | newFakeProvider | func | internal/identity/federation_page_test.go |
| sahteSaglayici.ayarla | fakeProvider.configure | method | internal/identity/federation_page_test.go |
| sahteSaglayici.tokenlar | fakeProvider.tokens | method | internal/identity/federation_page_test.go |
| sahteSaglayici.sayilar | fakeProvider.counts | method | internal/identity/federation_page_test.go |
| sahteFedereIstekler | fakeFederationRequests | type | internal/identity/federation_page_test.go |
| sahteFedereIstekler.istekler | fakeFederationRequests.requests | field | internal/identity/federation_page_test.go |
| sahteFedereIstekler.baglanan | fakeFederationRequests.bound | field | internal/identity/federation_page_test.go |
| sahteFedereIstekler.amrler | fakeFederationRequests.amrs | field | internal/identity/federation_page_test.go |
| newSahteFedereIstekler | newFakeFederationRequests | func | internal/identity/federation_page_test.go |
| sahteFedereIstekler.ekle | fakeFederationRequests.add | method | internal/identity/federation_page_test.go |
| sahteFedereIstekler.sil | fakeFederationRequests.drop | method | internal/identity/federation_page_test.go |
| sahteFedereIstekler.amrOku | fakeFederationRequests.amrFor | method | internal/identity/federation_page_test.go |
| fedOrtam | fedEnv | type | internal/identity/federation_page_test.go |
| fedOrtam.f | fedEnv.pages | field | internal/identity/federation_page_test.go |
| fedOrtam.depo | fedEnv.store | field | internal/identity/federation_page_test.go |
| fedOrtam.kullanici | fedEnv.users | field | internal/identity/federation_page_test.go |
| fedOrtam.federe | fedEnv.federated | field | internal/identity/federation_page_test.go |
| fedOrtam.oturumlar | fedEnv.sessions | field | internal/identity/federation_page_test.go |
| fedOrtam.istekler | fedEnv.requests | field | internal/identity/federation_page_test.go |
| fedOrtam.oturumCerez | fedEnv.sessionCookie | field | internal/identity/federation_page_test.go |
| testFedereSayfalar | newTestFederationPages | func | internal/identity/federation_page_test.go |
| fedOrtam.durumOku | fedEnv.peekState | method | internal/identity/federation_page_test.go |
| fedOrtam.baslatHam | fedEnv.startRaw | method | internal/identity/federation_page_test.go |
| fedOrtam.durumSayisi | fedEnv.stateCount | method | internal/identity/federation_page_test.go |
| fedCerezVarMi | hasFedCookie | func | internal/identity/federation_page_test.go |
| fedOrtam.baslat | fedEnv.start | method | internal/identity/federation_page_test.go |
| fedOrtam.geri | fedEnv.callback | method | internal/identity/federation_page_test.go |
| fedOrtam.akisKos | fedEnv.runFlow | method | internal/identity/federation_page_test.go |
| fedOrtam.saglayiciAl | fedEnv.provider | method | internal/identity/federation_page_test.go |
| fedOrtam.beklenenCallback | fedEnv.expectedCallback | method | internal/identity/federation_page_test.go |
| (anon).ad/.cerez/.state/.cerezUret/.hata/.kod/.metin | .name/.cookie/.state/.makeCookie/.err/.code/.text | manual: field | internal/identity/federation_page_test.go |
| testHizLimit | newTestRateLimiter | func | internal/identity/ratelimit_test.go |
| girisPostIstegi | loginPostRequest | func | internal/identity/ratelimit_test.go |
| gecenSayisi | countPassed | func | internal/identity/ratelimit_test.go |
| izinVerenHandler | allowHandler | func | internal/identity/ratelimit_test.go |
| (anon).ad/.xffSatirlari/.guvenilenProxy/.beklenen/.ayar | .name/.xffLines/.trustedProxies/.want/.cfg | manual: field | internal/identity/ratelimit_test.go |
| pgTestKullanici | pgTestUser | func | internal/identity/token_store_pg_test.go |
| yeniRefresh | newRefresh | func | internal/identity/token_store_test.go |
| sozlesmeSonuc | contractResult | type | internal/identity/user_store_contract_test.go |
| sozlesmeSonuc.bulundu | contractResult.found | field | internal/identity/user_store_contract_test.go |
| sozlesmeSonuc.eposta | contractResult.email | field | internal/identity/user_store_contract_test.go |
| sozlesmeSonuc.ad | contractResult.name | field | internal/identity/user_store_contract_test.go |
| sozlesmeSonuc.hata | contractResult.err | field | internal/identity/user_store_contract_test.go |
| sozlesmeHata | contractErr | func | internal/identity/user_store_contract_test.go |
| sonuc | result | func | internal/identity/user_store_contract_test.go |
| (anon).ad/.cagri/.beklenen | .name/.call/.want | manual: field | internal/identity/user_store_contract_test.go |
| sahteOPAyar | fakeOPConfig | type | internal/identity/fake_upstream_test.go |
| sahteOPAyar.Eposta | fakeOPConfig.Email | field | internal/identity/fake_upstream_test.go |
| sahteOPAyar.EpostaDogrulandi | fakeOPConfig.EmailVerified | field | internal/identity/fake_upstream_test.go |
| sahteOPAyar.EpostaDogrulandiString | fakeOPConfig.EmailVerifiedString | field | internal/identity/fake_upstream_test.go |
| sahteOPAyar.Ad | fakeOPConfig.Name | field | internal/identity/fake_upstream_test.go |
| sahteOPAyar.YanlisNonce | fakeOPConfig.WrongNonce | field | internal/identity/fake_upstream_test.go |
| sahteOPAyar.YanlisAudience | fakeOPConfig.WrongAudience | field | internal/identity/fake_upstream_test.go |
| sahteOPAyar.YanlisIssuer | fakeOPConfig.WrongIssuer | field | internal/identity/fake_upstream_test.go |
| sahteOPAyar.YanlisImzaAnahtari | fakeOPConfig.WrongSigningKey | field | internal/identity/fake_upstream_test.go |
| sahteOPAyar.TokenDurumKodu | fakeOPConfig.TokenStatus | field | internal/identity/fake_upstream_test.go |
| sahteOPAyar.TokenGovdesi | fakeOPConfig.TokenBody | field | internal/identity/fake_upstream_test.go |
| sahteOP | fakeOP | type | internal/identity/fake_upstream_test.go |
| sahteOP.ad | fakeOP.name | field | internal/identity/fake_upstream_test.go |
| sahteOP.istemciID | fakeOP.clientID | field | internal/identity/fake_upstream_test.go |
| sahteOP.sunucu | fakeOP.server | field | internal/identity/fake_upstream_test.go |
| sahteOP.yayinAnahtar | fakeOP.publishedKey | field | internal/identity/fake_upstream_test.go |
| sahteOP.imzaAnahtar | fakeOP.signingKey | field | internal/identity/fake_upstream_test.go |
| sahteOP.ayar | fakeOP.cfg | field | internal/identity/fake_upstream_test.go |
| sahteOP.kodNonce | fakeOP.codeNonce | field | internal/identity/fake_upstream_test.go |
| sahteOP.sonAuthorize | fakeOP.lastAuthorize | field | internal/identity/fake_upstream_test.go |
| sahteOP.kodSayisi | fakeOP.codeCount | field | internal/identity/fake_upstream_test.go |
| sahteOP.tokenSayisi | fakeOP.tokenCount | field | internal/identity/fake_upstream_test.go |
| otoFormIsaret | autoFormMarker | const | internal/identity/fake_upstream_test.go |
| sahteYukariAkisOP | newFakeUpstreamOP | func | internal/identity/fake_upstream_test.go |
| sahteOP.ayarla | fakeOP.configure | method | internal/identity/fake_upstream_test.go |
| sahteOP.tokenSayaci | fakeOP.tokenCalls | method | internal/identity/fake_upstream_test.go |
| sahteOP.authorizeSorgusu | fakeOP.lastAuthorizeQuery | method | internal/identity/fake_upstream_test.go |
| cerezBaglam | cookieContext | type | internal/identity/fake_upstream_test.go |
| baglamBaslatma | ctxUserInitiated | const | internal/identity/fake_upstream_test.go |
| baglamAyniSite | ctxSameSite | const | internal/identity/fake_upstream_test.go |
| baglamCaprazGet | ctxCrossSiteGet | const | internal/identity/fake_upstream_test.go |
| baglamCaprazPost | ctxCrossSitePost | const | internal/identity/fake_upstream_test.go |
| cerezBaglam.fetchSite | cookieContext.fetchSite | method | internal/identity/fake_upstream_test.go |
| cerezKavanozu | cookieJar | type | internal/identity/fake_upstream_test.go |
| cerezKavanozu.cerezler | cookieJar.cookies | field | internal/identity/fake_upstream_test.go |
| cerezKavanozu.baglam | cookieJar.ctx | field | internal/identity/fake_upstream_test.go |
| yeniCerezKavanozu | newCookieJar | func | internal/identity/fake_upstream_test.go |
| cerezKavanozu.baglamKur | cookieJar.setContext | method | internal/identity/fake_upstream_test.go |
| cerezKavanozu.cerezSil | cookieJar.deleteCookie | method | internal/identity/fake_upstream_test.go |
| cerezKavanozu.cerezDegeri | cookieJar.cookieValue | method | internal/identity/fake_upstream_test.go |
| cerezKavanozu.tasinirMi | cookieJar.sendable | method | internal/identity/fake_upstream_test.go |
| cerezYoluEsler | cookiePathMatches | func | internal/identity/fake_upstream_test.go |
| yanitKaydi | responseRecord | type | internal/identity/fake_upstream_test.go |
| yanitKaydi.Durum | responseRecord.Status | field | internal/identity/fake_upstream_test.go |
| yanitKaydi.istekURL | responseRecord.requestURL | field | internal/identity/fake_upstream_test.go |
| yanitKaydi.govde | responseRecord.body | field | internal/identity/fake_upstream_test.go |
| yanitKaydi.konum | responseRecord.location | field | internal/identity/fake_upstream_test.go |
| yanitKaydi.cerezler | responseRecord.cookies | field | internal/identity/fake_upstream_test.go |
| yanitKaydi.Govde | responseRecord.Body | method | internal/identity/fake_upstream_test.go |
| yanitKaydi.Konum | responseRecord.Location | method | internal/identity/fake_upstream_test.go |
| yanitKaydi.Cerezler | responseRecord.Cookies | method | internal/identity/fake_upstream_test.go |
| yanitKaydi.KonumKod | responseRecord.LocationCode | method | internal/identity/fake_upstream_test.go |
| yanitKaydi.fedCerezi | responseRecord.fedCookie | method | internal/identity/fake_upstream_test.go |
| fedTarayici | fedBrowser | type | internal/identity/fake_upstream_test.go |
| fedTarayici.ortam | fedBrowser.env | field | internal/identity/fake_upstream_test.go |
| fedTarayici.kavanoz | fedBrowser.jar | field | internal/identity/fake_upstream_test.go |
| fedTarayici.istemci | fedBrowser.client | field | internal/identity/fake_upstream_test.go |
| akisOrtami.YeniTarayici | flowEnv.NewBrowser | method | internal/identity/fake_upstream_test.go |
| fedTarayici.istek | fedBrowser.do | method | internal/identity/fake_upstream_test.go |
| fedTarayici.istekBaslikli | fedBrowser.getWithHeaders | method | internal/identity/fake_upstream_test.go |
| jsonCoz | decodeJSON | func | internal/identity/fake_upstream_test.go |
| otoFormDeseni | autoFormPattern | var | internal/identity/fake_upstream_test.go |
| gizliAlanDeseni | hiddenFieldPattern | var | internal/identity/fake_upstream_test.go |
| otoFormCoz | parseAutoForm | func | internal/identity/fake_upstream_test.go |
| takipSiniri | maxRedirects | const | internal/identity/fake_upstream_test.go |
| fedTarayici.akisiTakipEt | fedBrowser.follow | method | internal/identity/fake_upstream_test.go |
| adimBaglami | stepContext | func | internal/identity/fake_upstream_test.go |
| fedKapsamlar | fedScopes | const | internal/identity/fake_upstream_test.go |
| fedTarayici.AuthorizeBaslat | fedBrowser.StartAuthorize | method | internal/identity/fake_upstream_test.go |
| fedTarayici.authorizeBaslatKapsam | fedBrowser.startAuthorizeWithScope | method | internal/identity/fake_upstream_test.go |
| fedTarayici.FederasyonAkisiniKostur | fedBrowser.RunFederationFlow | method | internal/identity/fake_upstream_test.go |
| fedTarayici.FederasyonBaslat | fedBrowser.StartFederation | method | internal/identity/fake_upstream_test.go |
| fedTarayici.IstekTamamlandiMi | fedBrowser.RequestDone | method | internal/identity/fake_upstream_test.go |
| fedTarayici.KodDegis | fedBrowser.ExchangeCode | method | internal/identity/fake_upstream_test.go |
| fedTarayici.KodDegisTam | fedBrowser.ExchangeCodeFull | method | internal/identity/fake_upstream_test.go |
| beklemeIDDeseni | pendingIDPattern | var | internal/identity/fake_upstream_test.go |
| fedTarayici.BaglamaGonder | fedBrowser.SubmitLink | method | internal/identity/fake_upstream_test.go |
| gevsekHizAyar | relaxedRateLimits | func | internal/identity/fake_upstream_test.go |
| akisOrtamiKur | setupFlowEnv | func | internal/identity/fake_upstream_test.go |
| federasyonKablola | wireFederation | func | internal/identity/fake_upstream_test.go |
| akisOrtami.YukariAkis | flowEnv.Upstream | method | internal/identity/fake_upstream_test.go |
| akisOrtami.KayitOl | flowEnv.SignUp | method | internal/identity/fake_upstream_test.go |
| akisOrtami.SifreyleGiris | flowEnv.LoginWithPassword | method | internal/identity/fake_upstream_test.go |
| akisOrtami.IstekTamamlandiMi | flowEnv.RequestDone | method | internal/identity/fake_upstream_test.go |
| akisOrtami.FedereEslesmeSayisi | flowEnv.FederatedLinkCount | method | internal/identity/fake_upstream_test.go |
| FakeFederatedStore.eslesmeSayisi | FakeFederatedStore.linkCount | method (test-file method on a prod type) (was SahteFedereDepo.eslesmeSayisi; its type was renamed in an earlier task) | internal/identity/fake_upstream_test.go |
| jwtIddialari | jwtClaims | func | internal/identity/fake_upstream_test.go |
| audIcerir | audContains | func | internal/identity/fake_upstream_test.go |
| amrDegerleri | amrValues | func | internal/identity/fake_upstream_test.go |
| akisOrtami.yenile | flowEnv.refresh | method | internal/identity/fake_upstream_test.go |
| sayimUserStore | countingUserStore | type | internal/identity/page_test.go |
| sayimUserStore.ic | countingUserStore.inner | field | internal/identity/page_test.go |
| sayimUserStore.byEmail | countingUserStore.byEmailCalls | field | internal/identity/page_test.go |
| sayimUserStore.create | countingUserStore.createCalls | field | internal/identity/page_test.go |
| sayimUserStore.sayilar | countingUserStore.counts | method | internal/identity/page_test.go |
| sahteTamamlayici | fakeCompleter | type | internal/identity/page_test.go |
| sahteTamamlayici.baglanan | fakeCompleter.bound | field | internal/identity/page_test.go |
| sahteTamamlayici.amrler | fakeCompleter.amrs | field | internal/identity/page_test.go |
| sahteTamamlayici.Hata | fakeCompleter.Err | field | internal/identity/page_test.go |
| newSahteTamamlayici | newFakeCompleter | func | internal/identity/page_test.go |
| sahteTamamlayici.amrOku | fakeCompleter.amrFor | method | internal/identity/page_test.go |
| sayfaTestOrtami | pagesEnv | type | internal/identity/page_test.go |
| sayfaTestOrtami.sayfalar | pagesEnv.pages | field | internal/identity/page_test.go |
| sayfaTestOrtami.kullanici | pagesEnv.users | field | internal/identity/page_test.go |
| sayfaTestOrtami.oturumlar | pagesEnv.sessions | field | internal/identity/page_test.go |
| sayfaTestOrtami.tamamlayan | pagesEnv.completer | field | internal/identity/page_test.go |
| testSayfalar | newTestPages | func | internal/identity/page_test.go |
| csrfDeseni | csrfPattern | var | internal/identity/page_test.go |
| baglamaKur | bindTestSession | func | internal/identity/page_test.go |
| formAc | openForm | func | internal/identity/page_test.go |
| formGoster | showForm | func | internal/identity/page_test.go |
| formIstek | formRequest | func | internal/identity/page_test.go |
| postEt | postForm | func | internal/identity/page_test.go |
| callbackIstek | callbackRequest | func | internal/identity/page_test.go |
| delegeIzleyici | delegateSpy | type | internal/identity/page_test.go |
| delegeIzleyici.cagrildi | delegateSpy.called | field | internal/identity/page_test.go |
| authorizeSar | wrapAuthorize | func | internal/identity/page_test.go |
| bagliMi | isBound | func | internal/identity/page_test.go |
| (anon).ad/.guvenli/.jeton | .name/.secure/.token | manual: field | internal/identity/page_test.go |
| (anon).ad/.degistir/.anahtar/.federe/.p8 | .name/.mutate/.key/.federated/.p8 | manual: field | internal/identity/server_test.go |
| (anon).ad (TestAuthorizeCaprazSite…) | .name | manual: field | internal/identity/page_test.go |
| testP8Uret | generateTestP8 | func | internal/identity/upstream_apple_test.go |
| TestAkisS256IleUctanUcaCalisir | TestFlow_S256EndToEnd | func (test) | internal/identity/flow_test.go |
| TestSomuru1OnBaglamaIleDevralinamaz | TestExploit1_PreBindingCannotTakeOver | func (test) | internal/identity/flow_test.go |
| TestSomuru2GirisSonrasiBaglamaIleDevralinamaz | TestExploit2_PostLoginBindingCannotTakeOver | func (test) | internal/identity/flow_test.go |
| TestGetGirisBaglamasizUretimMuxundaReddedilir | TestBuildMux_LoginGetWithoutBindingRejected | func (test) | internal/identity/flow_test.go |
| TestParalelIkiAkisAyniTarayicidaCalisir | TestFlow_TwoParallelFlowsSameBrowser | func (test) | internal/identity/flow_test.go |
| TestKendiTarayicisiAkisiTamamlar | TestFlow_OwnBrowserCompletes | func (test) | internal/identity/flow_test.go |
| TestAuthorizeCodeChallengesizReddedilir | TestAuthorize_MissingCodeChallengeRejected | func (test) | internal/identity/flow_test.go |
| TestAuthorizePlainReddedilir | TestAuthorize_PlainRejected | func (test) | internal/identity/flow_test.go |
| TestAuthorizeMethodsuzReddedilir | TestAuthorize_MissingMethodRejected | func (test) | internal/identity/flow_test.go |
| TestAuthorizeBilinmeyenMethodReddedilir | TestAuthorize_UnknownMethodRejected | func (test) | internal/identity/flow_test.go |
| TestAuthorizeS256Calisir | TestAuthorize_S256Works | func (test) | internal/identity/flow_test.go |
| TestYanlisVerifierReddedilir | TestToken_WrongVerifierRejected | func (test) | internal/identity/flow_test.go |
| TestPlainChallengeTokenUcundaReddedilir | TestToken_PlainChallengeRejected | func (test) | internal/identity/flow_test.go |
| TestDiscoveryYalnizcaS256IlanEder | TestDiscovery_AdvertisesOnlyS256 | func (test) | internal/identity/flow_test.go |
| TestDiscoveryDesteklenmeyenAkislariIlanEtmez | TestDiscovery_OmitsUnsupportedFlows | func (test) | internal/identity/flow_test.go |
| TestLoginCSRFCaprazSiteEnjeksiyonuTamamlanamaz | TestLoginCSRF_CrossSiteInjectionCannotComplete | func (test) | internal/identity/flow_test.go |
| TestCaprazSiteIstekMevcutBaglamalariBozmaz | TestAuthorize_CrossSiteKeepsExistingBindings | func (test) | internal/identity/flow_test.go |
| TestFederasyonsuzKurulumUclariBaglamaz | TestBuildMux_NoFederationMountsNoEndpoints | func (test) | internal/identity/flow_test.go |
| TestMuxKurFederasyonKablolamasi | TestBuildMux_FederationWiring | func (test) | internal/identity/flow_test.go |
| TestAnahtarYuklenirVeArayuzleriKarsilar | TestLoadSigningKey_SatisfiesInterfaces | func (test) | internal/identity/signing_key_test.go |
| TestAnahtarIDKararli | TestSigningKey_IDStable | func (test) | internal/identity/signing_key_test.go |
| TestAnahtarIDFarkliAnahtarlardaFarkli | TestSigningKey_IDDiffersPerKey | func (test) | internal/identity/signing_key_test.go |
| TestBozukPEMReddedilir | TestLoadSigningKey_RejectsBadPEM | func (test) | internal/identity/signing_key_test.go |
| TestCryptoAnahtarUzunlugu | TestCryptoKey_Length | func (test) | internal/identity/signing_key_test.go |
| TestPKCS8FormatKabulu | TestLoadSigningKey_AcceptsPKCS8 | func (test) | internal/identity/signing_key_test.go |
| TestKisaBitAnahtariReddedilir | TestLoadSigningKey_RejectsShortKey | func (test) | internal/identity/signing_key_test.go |
| TestAcikAnahtarOzelAnahtarSizdirmaz | TestPublicKey_DoesNotLeakPrivateKey | func (test) | internal/identity/signing_key_test.go |
| TestCozumBagliKimlikEpostadanOnceGelir | TestResolveUser_LinkedIdentityBeforeEmail | func (test) | internal/identity/link_page_test.go |
| TestGizliAktarmaAdresiBaglamaTetiklemez | TestResolveUser_PrivateRelayDoesNotTriggerLink | func (test) | internal/identity/link_page_test.go |
| TestEpostaCakismasiSifreliHesapBaglamayaGoturur | TestResolveUser_EmailConflictWithPasswordLeadsToLink | func (test) | internal/identity/link_page_test.go |
| TestEpostaCakismasiSifresizHesapEngellenir | TestResolveUser_EmailConflictWithoutPasswordBlocked | func (test) | internal/identity/link_page_test.go |
| TestSaldirganinOnceActigiHesabaSessizceBaglanmaz | TestResolveUser_NoSilentLinkToPrecreatedAccount | func (test) | internal/identity/link_page_test.go |
| TestGizliAktarmaAdresiZatenVarsa409 | TestResolveUser_ExistingPrivateRelayReturns409 | func (test) | internal/identity/link_page_test.go |
| TestBaglamaGetBaglamasizReddedilir | TestLinkGet_WithoutBindingRejected | func (test) | internal/identity/link_page_test.go |
| TestBaglamaPostCSRFYanlisReddedilir | TestLinkPost_WrongCSRFRejected | func (test) | internal/identity/link_page_test.go |
| TestBaglamaPostDogruSifreBaglarVeGirisYapar | TestLinkPost_CorrectPasswordLinksAndLogsIn | func (test) | internal/identity/link_page_test.go |
| TestBaglamaPostYanlisSifreBaglamaz | TestLinkPost_WrongPasswordDoesNotLink | func (test) | internal/identity/link_page_test.go |
| TestBaglamaBeklemesiTekKullanimlik | TestPendingLink_SingleUse | func (test) | internal/identity/link_page_test.go |
| TestBaglamaBaskaTarayicidaTamamlanamaz | TestLink_CannotCompleteInOtherBrowser | func (test) | internal/identity/link_page_test.go |
| TestBaglamaHizLimitiUygulanir | TestLink_RateLimitApplied | func (test) | internal/identity/link_page_test.go |
| TestBaglamaHesapHizLimitiUygulanir | TestLink_AccountRateLimitApplied | func (test) | internal/identity/link_page_test.go |
| TestBaglamaBeklemesiBaglamasizPostlaYokEdilemez | TestPendingLink_UnboundPostCannotDestroy | func (test) | internal/identity/link_page_test.go |
| TestBaglamaSifresizHesapIcinAcilmaz | TestLink_NotOpenedForPasswordlessAccount | func (test) | internal/identity/link_page_test.go |
| TestBaglamaPostIstekTTLIleDustugunde400Doner | TestLinkPost_ExpiredRequestReturns400 | func (test) | internal/identity/link_page_test.go |
| TestCerezKavanozuSameSiteUygular | TestCookieJar_EnforcesSameSite | func (test) | internal/identity/cookie_jar_test.go |
| TestCerezKavanozuYolSiniri | TestCookieJar_PathScope | func (test) | internal/identity/cookie_jar_test.go |
| TestCerezKavanozuHostAyrimi | TestCookieJar_HostIsolation | func (test) | internal/identity/cookie_jar_test.go |
| TestCerezKavanozuSilme | TestCookieJar_Delete | func (test) | internal/identity/cookie_jar_test.go |
| TestAdimBaglami | TestStepContext | func (test) | internal/identity/cookie_jar_test.go |
| TestTerminateSessionBosKimlikleDepoyaDokunmaz | TestStorage_TerminateSessionEmptyIDsSkipStore | func (test) | internal/identity/storage_revoke_test.go |
| TestGetRefreshTokenInfoYenidenKullanimdaAileyiIptalEder | TestStorage_GetRefreshTokenInfoReuseRevokesFamily | func (test) | internal/identity/storage_revoke_test.go |
| TestGetRefreshTokenInfoBilinmeyenJetonAileyiIptalEtmez | TestStorage_GetRefreshTokenInfoUnknownTokenNoRevoke | func (test) | internal/identity/storage_revoke_test.go |
| TestGetRefreshTokenInfoGecerliJetonuDondurur | TestStorage_GetRefreshTokenInfoReturnsValidToken | func (test) | internal/identity/storage_revoke_test.go |
| TestDiscoveryDuzeltDesteklenmeyenAkislariAyiklar | TestTrimDiscovery_RemovesUnsupportedFlows | func (test) | internal/identity/storage_revoke_test.go |
| TestDepoArayuzuKarsilar | TestStorage_SatisfiesInterface | func (test) | internal/identity/storage_test.go |
| TestTanimsizIstemciReddedilir | TestStorage_UnknownClientRejected | func (test) | internal/identity/storage_test.go |
| TestSecretIleDogrulamaHepReddedilir | TestStorage_ClientSecretAlwaysRejected | func (test) | internal/identity/storage_test.go |
| TestTekrarKullanimInvalidRefreshOlur | TestStorage_ReuseYieldsInvalidRefresh | func (test) | internal/identity/storage_test.go |
| TestJWTProfileDesteklenmez | TestStorage_JWTProfileUnsupported | func (test) | internal/identity/storage_test.go |
| TestGetRefreshTokenInfoGecersizIcinInvalidRefreshDoner | TestStorage_GetRefreshTokenInfoInvalidReturnsInvalidRefresh | func (test) | internal/identity/storage_test.go |
| TestGetRefreshTokenInfoGecerli | TestStorage_GetRefreshTokenInfoValid | func (test) | internal/identity/storage_test.go |
| TestRevokeTokenRefreshAileyiIptalEder | TestStorage_RevokeTokenRefreshRevokesFamily | func (test) | internal/identity/storage_test.go |
| TestRevokeTokenAccessKaydiSiler | TestStorage_RevokeTokenDeletesAccessRecord | func (test) | internal/identity/storage_test.go |
| TestRevokeTokenBilinmeyenJetonSessizceBasarili | TestStorage_RevokeTokenUnknownSucceedsSilently | func (test) | internal/identity/storage_test.go |
| TestSetUserinfoFromScopesBos | TestStorage_SetUserinfoFromScopesEmpty | func (test) | internal/identity/storage_test.go |
| TestIDTokenUretimiKullaniciOkunamazsaBasarisiz | TestStorage_IDTokenFailsWhenUserUnreadable | func (test) | internal/identity/storage_test.go |
| TestHealthBaglantisizHataDoner | TestStorage_HealthWithoutDepsFails | func (test) | internal/identity/storage_test.go |
| TestGetRefreshTokenInfoBaskaIstemcininJetonuReddedilir | TestStorage_GetRefreshTokenInfoOtherClientRejected | func (test) | internal/identity/storage_test.go |
| TestCreateAccessAndRefreshTokensYeniAileBaslatir | TestStorage_CreateAccessAndRefreshTokensStartsFamily | func (test) | internal/identity/storage_test.go |
| TestCreateAccessAndRefreshTokensRotasyonYapar | TestStorage_CreateAccessAndRefreshTokensRotates | func (test) | internal/identity/storage_test.go |
| TestTokenRequestByRefreshTokenYenidenKullanimdaAileyiIptalEder | TestStorage_TokenRequestByRefreshTokenReuseRevokesFamily | func (test) | internal/identity/storage_test.go |
| TestTokenRequestByRefreshTokenHatasiAyrismaz | TestStorage_TokenRequestByRefreshTokenErrorIndistinguishable | func (test) | internal/identity/storage_test.go |
| TestUctanUcaGoogleGirisi | TestE2E_GoogleLogin | func (test) | internal/identity/federation_flow_test.go |
| TestUctanUcaAppleGirisiFormPost | TestE2E_AppleLoginFormPost | func (test) | internal/identity/federation_flow_test.go |
| TestUctanUcaIkinciGirisAyniHesap | TestE2E_SecondLoginSameAccount | func (test) | internal/identity/federation_flow_test.go |
| TestUctanUcaAppleIkinciGiristeEpostaGelmez | TestE2E_AppleSecondLoginWithoutEmail | func (test) | internal/identity/federation_flow_test.go |
| TestSaldiriBaskaTarayicidaBaslatilanAkisTamamlanamaz | TestAttack_FlowFromOtherBrowserCannotComplete | func (test) | internal/identity/federation_flow_test.go |
| TestSaldiriCerezsizCallbackReddedilir | TestAttack_CallbackWithoutCookieRejected | func (test) | internal/identity/federation_flow_test.go |
| TestSaldiriCallbackTekrariReddedilir | TestAttack_CallbackReplayRejected | func (test) | internal/identity/federation_flow_test.go |
| TestSaldiriBaskaUygulamaninTokeniyleGirilemez | TestAttack_OtherAppTokenRejected | func (test) | internal/identity/federation_flow_test.go |
| TestSaldiriNonceEnjeksiyonuReddedilir | TestAttack_NonceInjectionRejected | func (test) | internal/identity/federation_flow_test.go |
| TestSaldiriEpostayaDayaliHesapDevralmaEngellenir | TestAttack_EmailBasedTakeoverBlocked | func (test) | internal/identity/federation_flow_test.go |
| TestSaldiriFederasyonPKCEyiAtlatmaz | TestAttack_FederationDoesNotBypassPKCE | func (test) | internal/identity/federation_flow_test.go |
| TestFedereDepoSozlesmesi | TestFederatedStore_Contract | func (test) | internal/identity/federated_store_contract_test.go |
| TestBaslatCerezKoyarVeDurumYazar | TestFederationStart_SetsCookieAndWritesState | func (test) | internal/identity/federation_page_test.go |
| TestFederasyonCereziSameSiteNone | TestFederationCookie_SameSiteNone | func (test) | internal/identity/federation_page_test.go |
| TestBaslatOturumCerezineDokunmaz | TestFederationStart_LeavesSessionCookie | func (test) | internal/identity/federation_page_test.go |
| TestBaslatBilinmeyenSaglayici404 | TestFederationStart_UnknownProvider404 | func (test) | internal/identity/federation_page_test.go |
| TestBaslatGecersizAuthRequestIDReddedilir | TestFederationStart_InvalidAuthRequestIDRejected | func (test) | internal/identity/federation_page_test.go |
| TestBaslatOturumBaglamasiZorunlu | TestFederationStart_SessionBindingRequired | func (test) | internal/identity/federation_page_test.go |
| TestBaslatCaprazSiteReddedilir | TestFederationStart_CrossSiteRejected | func (test) | internal/identity/federation_page_test.go |
| TestBaslatBaglamasizIstekFederasyonlaTamamlanamaz | TestFederationStart_UnboundRequestCannotComplete | func (test) | internal/identity/federation_page_test.go |
| TestBaslatYapilandirilmamisSaglayici404 | TestFederationStart_UnconfiguredProvider404 | func (test) | internal/identity/federation_page_test.go |
| TestGeriBilinenKimlikGirisYapar | TestFederationCallback_KnownIdentityLogsIn | func (test) | internal/identity/federation_page_test.go |
| TestGeriYeniKimlikHesapAcar | TestFederationCallback_NewIdentityCreatesAccount | func (test) | internal/identity/federation_page_test.go |
| TestGeriCaprazSitePostKabulEdilir | TestFederationCallback_CrossSitePostAccepted | func (test) | internal/identity/federation_page_test.go |
| TestGeriBilinmeyenStateReddedilir | TestFederationCallback_UnknownStateRejected | func (test) | internal/identity/federation_page_test.go |
| TestGeriStateTekrariReddedilir | TestFederationCallback_StateReplayRejected | func (test) | internal/identity/federation_page_test.go |
| TestGeriCerezEslesmezseReddedilir | TestFederationCallback_CookieMismatchRejected | func (test) | internal/identity/federation_page_test.go |
| TestGeriNonceEslesmezseReddedilir | TestFederationCallback_NonceMismatchRejected | func (test) | internal/identity/federation_page_test.go |
| TestGeriGecersizImzaReddedilir | TestFederationCallback_InvalidSignatureRejected | func (test) | internal/identity/federation_page_test.go |
| TestGeriYanlisAudienceReddedilir | TestFederationCallback_WrongAudienceRejected | func (test) | internal/identity/federation_page_test.go |
| TestGeriTamamlanmisIstekBaskaKullaniciyaReddedilir | TestFederationCallback_CompletedRequestOtherUserRejected | func (test) | internal/identity/federation_page_test.go |
| TestGeriSaglayiciKayittanOkunur | TestFederationCallback_ProviderReadFromState | func (test) | internal/identity/federation_page_test.go |
| TestGeriHizLimitiUygulanir | TestFederationCallback_RateLimitApplied | func (test) | internal/identity/federation_page_test.go |
| TestFederasyonBaslatHizLimitiAyriSayilir | TestFederationStart_RateLimitCountedSeparately | func (test) | internal/identity/federation_page_test.go |
| TestGeriLoglarSizdirmaz | TestFederationCallback_LogsDoNotLeak | func (test) | internal/identity/federation_page_test.go |
| TestGeriIstekTTLIleDustugunde400Doner | TestFederationCallback_ExpiredRequestReturns400 | func (test) | internal/identity/federation_page_test.go |
| TestIstekTamamlamaHatasiSiniflandirmasi | TestWriteCompletionError_Classification | func (test) | internal/identity/federation_page_test.go |
| TestYukariAkisEpostaVermezseHesapAcilmaz | TestFederationCallback_NoUpstreamEmailNoAccount | func (test) | internal/identity/federation_page_test.go |
| TestGeriBasarilidaFederasyonCereziSilinir | TestFederationCallback_SuccessClearsFederationCookie | func (test) | internal/identity/federation_page_test.go |
| TestHesapSilCascade | TestServer_DeleteAccountCascades | func (test) | internal/identity/account_delete_test.go |
| TestIstemciIPGuvenilenProxySayisinaGoreCozulur | TestClientIP_ResolvesByTrustedProxyCount | func (test) | internal/identity/ratelimit_test.go |
| TestHizLimitGecersizAyardaKurulmaz | TestNewRateLimiter_RejectsInvalidConfig | func (test) | internal/identity/ratelimit_test.go |
| TestHizLimitRedisYokkenFailClosed | TestRateLimiter_FailsClosedWithoutRedis | func (test) | internal/identity/ratelimit_test.go |
| TestGirisHizLimitiIPBasinaUygulanir | TestRateLimiter_LoginPerIP | func (test) | internal/identity/ratelimit_test.go |
| TestGirisHizLimitiEpostaBasinaUygulanir | TestRateLimiter_LoginPerEmail | func (test) | internal/identity/ratelimit_test.go |
| TestGirisEpostaSayaclariAyrisir | TestRateLimiter_LoginEmailCountersIndependent | func (test) | internal/identity/ratelimit_test.go |
| TestKayitHizLimitiIPBasinaUygulanir | TestRateLimiter_SignupPerIP | func (test) | internal/identity/ratelimit_test.go |
| TestHizLimit429RetryAfterTasir | TestRateLimiter_429HasRetryAfter | func (test) | internal/identity/ratelimit_test.go |
| TestHizLimitEpostayiRedisAnahtarindaDuzMetinTutmaz | TestRateLimiter_NoPlaintextEmailInRedisKey | func (test) | internal/identity/ratelimit_test.go |
| TestGirisHizLimitiUretimMuxundaDevrede | TestBuildMux_LoginRateLimitActive | func (test) | internal/identity/ratelimit_test.go |
| TestGirisEpostaSayaciHarfDuyarsiz | TestRateLimiter_LoginEmailCaseInsensitive | func (test) | internal/identity/ratelimit_test.go |
| TestGirisHizLimitiCokluXFFSatiriIleAtlatilamaz | TestRateLimiter_MultipleXFFLinesCannotBypass | func (test) | internal/identity/ratelimit_test.go |
| TestSayfaGetHizLimitiUygulanir | TestRateLimiter_PageGetLimited | func (test) | internal/identity/ratelimit_test.go |
| TestSayfaGetSayaciPostSayacindanAyri | TestRateLimiter_PageGetCounterSeparateFromPost | func (test) | internal/identity/ratelimit_test.go |
| TestGetHizLimitiUretimMuxundaDevrede | TestBuildMux_GetRateLimitActive | func (test) | internal/identity/ratelimit_test.go |
| TestAuthIstekArayuzuKarsilar | TestAuthRequest_SatisfiesInterface | func (test) | internal/identity/request_test.go |
| TestIstekOlusturVeOku | TestRequestStore_CreateAndGet | func (test) | internal/identity/request_test.go |
| TestKodTekKullanimlik | TestRequestStore_CodeSingleUse | func (test) | internal/identity/request_test.go |
| TestOlmayanIstek | TestRequestStore_MissingRequest | func (test) | internal/identity/request_test.go |
| TestIstekBaslangictaTamamlanmamis | TestAuthRequest_InitiallyNotDone | func (test) | internal/identity/request_test.go |
| TestTamamlandiIsaretleSubjectEzmez | TestRequestStore_MarkDoneKeepsSubject | func (test) | internal/identity/request_test.go |
| TestTamamlandiIsaretleAyniKullaniciNoOp | TestRequestStore_MarkDoneSameUserNoOp | func (test) | internal/identity/request_test.go |
| TestIstekIDIpucuSubjectOlmaz | TestRequestStore_IDIsNotHintSubject | func (test) | internal/identity/request_test.go |
| TestAMRSifreyleGiristePwd | TestAMR_PasswordLoginIsPwd | func (test) | internal/identity/request_test.go |
| TestAMRFederasyondaPwdDegil | TestAMR_FederationIsNotPwd | func (test) | internal/identity/request_test.go |
| TestAMRVarsayilanPwdDegil | TestAMR_DefaultIsNotPwd | func (test) | internal/identity/request_test.go |
| TestAMRNoOpdaEzilmez | TestAMR_NotOverwrittenOnNoOp | func (test) | internal/identity/request_test.go |
| TestAMRRefreshYenilemesindeKorunur | TestAMR_PreservedOnRefresh | func (test) | internal/identity/request_test.go |
| TestOturumBaglamaYazVeOku | TestRequestStore_WriteAndReadSession | func (test) | internal/identity/request_test.go |
| TestMobilIstemciArayuzuKarsilar | TestMobileClient_SatisfiesInterface | func (test) | internal/identity/client_test.go |
| TestMobilIstemciPublicVeNative | TestMobileClient_PublicAndNative | func (test) | internal/identity/client_test.go |
| TestMobilIstemciYalnizcaKodAkisi | TestMobileClient_CodeFlowOnly | func (test) | internal/identity/client_test.go |
| TestMobilIstemciRedirectleri | TestMobileClient_RedirectURIs | func (test) | internal/identity/client_test.go |
| TestMobilIstemciDevModeKapali | TestMobileClient_DevModeOff | func (test) | internal/identity/client_test.go |
| TestMobilIstemciIzinliScopelar | TestMobileClient_AllowedScopes | func (test) | internal/identity/client_test.go |
| TestPGYenidenKullanimAileyiIptalEder | TestPostgresTokenStore_ReuseRevokesFamily | func (test) | internal/identity/token_store_pg_test.go |
| TestPGRefreshOkuHataSirasi | TestPostgresTokenStore_GetRefreshErrorOrder | func (test) | internal/identity/token_store_pg_test.go |
| TestRefreshOlusturVeOku | TestTokenStore_CreateAndGetRefresh | func (test) | internal/identity/token_store_test.go |
| TestRefreshDonerVeEskisiOlur | TestTokenStore_RotateInvalidatesOld | func (test) | internal/identity/token_store_test.go |
| TestKullanilmisJetonAileyiIptalEder | TestTokenStore_UsedTokenRevokesFamily | func (test) | internal/identity/token_store_test.go |
| TestSuresiGecmisRefreshReddedilir | TestTokenStore_ExpiredRefreshRejected | func (test) | internal/identity/token_store_test.go |
| TestOlmayanRefresh | TestTokenStore_MissingRefresh | func (test) | internal/identity/token_store_test.go |
| TestKullaniciIptalHepsiniOldurur | TestTokenStore_RevokeUserRevokesAll | func (test) | internal/identity/token_store_test.go |
| TestJetonDuzMetinSaklanmaz | TestTokenStore_NoPlaintextToken | func (test) | internal/identity/token_store_test.go |
| TestRotasyonSahiplikDogrular | TestTokenStore_RotateChecksOwnership | func (test) | internal/identity/token_store_test.go |
| TestSahteDepoKopyaDondurur | TestFakeTokenStore_ReturnsCopies | func (test) | internal/identity/token_store_test.go |
| TestTumKayitlarKopyaDondurur | TestFakeTokenStore_AllRecordsReturnsCopies | func (test) | internal/identity/token_store_test.go |
| TestAccessKaydetVeOku | TestTokenStore_SaveAndGetAccess | func (test) | internal/identity/token_store_test.go |
| TestUserStoreSozlesmesi | TestUserStore_Contract | func (test) | internal/identity/user_store_contract_test.go |
| TestSahteUserStoreDahiliIsaretciVermez | TestFakeUserStore_NoInternalPointers | func (test) | internal/identity/user_store_contract_test.go |
| TestEpostaNormalize | TestNormalizeEmail | func (test) | internal/identity/user_store_contract_test.go |
| TestSifreHashlenirVeDogrulanir | TestUser_PasswordHashedAndVerified | func (test) | internal/identity/user_test.go |
| TestSifresizKullaniciHicbirSifreyiKabulEtmez | TestUser_PasswordlessAcceptsNoPassword | func (test) | internal/identity/user_test.go |
| TestEpostaBuyukKucukDuyarsiz | TestUserStore_EmailCaseInsensitive | func (test) | internal/identity/user_test.go |
| TestOlmayanKullanici | TestUserStore_MissingUser | func (test) | internal/identity/user_test.go |
| TestKisaSifreReddedilir | TestUserStore_ShortPasswordRejected | func (test) | internal/identity/user_test.go |
| TestGirisSayfasiFormGosterir | TestLogin_ShowsForm | func (test) | internal/identity/page_test.go |
| TestAuthorizeOturumCereziVerir | TestWrapAuthorize_SetsSessionCookie | func (test) | internal/identity/page_test.go |
| TestFormGetBaglamasizReddedilir | TestFormGet_WithoutBindingRejected | func (test) | internal/identity/page_test.go |
| TestParalelIkiIstekAyniCerezeBaglanir | TestBindSession_TwoParallelRequestsSameCookie | func (test) | internal/identity/page_test.go |
| TestOturumBaglamaUstSiniriUygulanir | TestBindSession_EnforcesMaxBindings | func (test) | internal/identity/page_test.go |
| TestCerezSecureBayragiIssuerSemasinaBagli | TestSessionCookie_SecureFollowsIssuerScheme | func (test) | internal/identity/page_test.go |
| TestGirisAuthRequestIDsizReddedilir | TestLogin_MissingAuthRequestIDRejected | func (test) | internal/identity/page_test.go |
| TestGirisCerezsizReddedilirVeSifreDogrulanmaz | TestLogin_NoCookieRejectedBeforePasswordCheck | func (test) | internal/identity/page_test.go |
| TestGirisBaskaAuthRequestIDileReddedilir | TestLogin_OtherAuthRequestIDRejected | func (test) | internal/identity/page_test.go |
| TestGirisUydurmaCerezReddedilir | TestLogin_ForgedCookieRejected | func (test) | internal/identity/page_test.go |
| TestGirisCSRFEksikVeYanlisReddedilir | TestLogin_MissingOrWrongCSRFRejected | func (test) | internal/identity/page_test.go |
| TestGirisGecerliCerezleCalisir | TestLogin_ValidCookieWorks | func (test) | internal/identity/page_test.go |
| TestKayitCerezsizReddedilirVeKullaniciOlusmaz | TestSignup_NoCookieRejectedNoUserCreated | func (test) | internal/identity/page_test.go |
| TestIkinciKayitSubjectEzmez | TestSignup_SecondSignupKeepsSubject | func (test) | internal/identity/page_test.go |
| TestYanlisGirisKullaniciVarligiSizdirmaz | TestLogin_FailureDoesNotRevealUserExistence | func (test) | internal/identity/page_test.go |
| TestSayfaGirdiKacirir | TestPages_EscapeInput | func (test) | internal/identity/page_test.go |
| TestGirisEpostaEchoKacar | TestLogin_EchoedEmailEscaped | func (test) | internal/identity/page_test.go |
| TestKayitSayfasiFormGosterir | TestSignup_ShowsForm | func (test) | internal/identity/page_test.go |
| TestKullanilanEpostaIleKayit | TestSignup_EmailInUse | func (test) | internal/identity/page_test.go |
| TestCallbackCerezsizReddedilir | TestAuthorizeCallback_NoCookieRejected | func (test) | internal/identity/page_test.go |
| TestCallbackYanlisCerezReddedilir | TestAuthorizeCallback_WrongCookieRejected | func (test) | internal/identity/page_test.go |
| TestCallbackDogruCerezleDelegeEder | TestAuthorizeCallback_ValidCookieDelegates | func (test) | internal/identity/page_test.go |
| TestCallbackIDsizReddedilir | TestAuthorizeCallback_MissingIDRejected | func (test) | internal/identity/page_test.go |
| TestBoslukluEpostaIleKayitSonrasiGirisCalisir | TestLogin_WorksAfterSignupWithPaddedEmail | func (test) | internal/identity/page_test.go |
| TestAuthorizeCaprazSiteYeniBaglamaEklemez | TestWrapAuthorize_CrossSiteAddsNoBinding | func (test) | internal/identity/page_test.go |
| TestAuthorizeCaprazSiteOlmayanlardaBaglamaKurulur | TestWrapAuthorize_NonCrossSiteBinds | func (test) | internal/identity/page_test.go |
| TestGirisSayfasiYalnizcaYapilandirilmisSaglayicilariGosterir | TestLogin_ShowsOnlyConfiguredProviders | func (test) | internal/identity/page_test.go |
| TestGirisSayfasiButonuAuthRequestIDTasir | TestLogin_ButtonCarriesAuthRequestID | func (test) | internal/identity/page_test.go |
| TestIssuerBossaBaslamaz | TestStart_EmptyIssuerFails | func (test) | internal/identity/server_test.go |
| TestEksikSirlarlaBaslamaz | TestStart_MissingSecretsFails | func (test) | internal/identity/server_test.go |
| TestHTTPIssuerYerelGelistirmedeGuvensizModaGecer | TestValidateInsecureIssuer_LocalHTTPAllowed | func (test) | internal/identity/server_test.go |
| TestHTTPIssuerUzakHostReddedilir | TestValidateInsecureIssuer_RemoteHostRejected | func (test) | internal/identity/server_test.go |
| TestHTTPIssuerProductiondaReddedilir | TestValidateInsecureIssuer_ProductionRejected | func (test) | internal/identity/server_test.go |
| TestGecersizTTLIleBaslamaz | TestStart_InvalidTTLFails | func (test) | internal/identity/server_test.go |
| TestBosIstemciYapilandirmasiIleBaslamaz | TestStart_EmptyClientConfigFails | func (test) | internal/identity/server_test.go |
| TestGecerliYapilandirmaDogrulamayiGecer | TestValidateConfig_ValidPasses | func (test) | internal/identity/server_test.go |
| TestAppleFederasyonuEksikAlanlarlaBaslamaz | TestValidateConfig_AppleMissingFieldsFails | func (test) | internal/identity/server_test.go |
| TestAppleFederasyonuTamAlanlarlaGecer | TestValidateConfig_AppleCompletePasses | func (test) | internal/identity/server_test.go |
| TestAppleP8BaslangictaAyristirilir | TestValidateConfig_AppleP8ParsedAtStartup | func (test) | internal/identity/server_test.go |
| TestGoogleFederasyonuSecretsizBaslamaz | TestValidateConfig_GoogleWithoutSecretFails | func (test) | internal/identity/server_test.go |
| TestGoogleFederasyonuSecretVarsaGecer | TestValidateConfig_GoogleWithSecretPasses | func (test) | internal/identity/server_test.go |
| TestHazirDinleyiciDurumunuYansitir | TestServer_ReadyReflectsListener | func (test) | internal/identity/server_test.go |
| TestHizLimitiKurulamazsaStartBasarisiz | TestStart_FailsWhenRateLimiterInvalid | func (test) | internal/identity/server_test.go |
| TestMigrationlarGomulu | TestMigrations_Embedded | func (test) | internal/identity/database_test.go |
| TestMigrationSirali | TestMigrations_Ordered | func (test) | internal/identity/database_test.go |
| TestAppleIstemciSirAlanlari | TestAppleClientSecret_Claims | func (test) | internal/identity/upstream_apple_test.go |
| TestAppleIstemciSirOmru | TestAppleClientSecret_Lifetime | func (test) | internal/identity/upstream_apple_test.go |
| TestAppleIstemciSirYanlisAnahtarTipiReddeder | TestAppleClientSecret_RejectsWrongKeyType | func (test) | internal/identity/upstream_apple_test.go |
| TestAppleIstemciSirBozukPEMReddeder | TestAppleClientSecret_RejectsBadPEM | func (test) | internal/identity/upstream_apple_test.go |
| TestJetonDegisGoogleAlanlariGonderir | TestExchangeCode_SendsGoogleFields | func (test) | internal/identity/upstream_token_test.go |
| TestJetonDegisPKCEsizSaglayiciyaDogrulayiciGondermez | TestExchangeCode_NoVerifierWithoutPKCE | func (test) | internal/identity/upstream_token_test.go |
| TestJetonDegisIdTokenYokHataVerir | TestExchangeCode_MissingIDTokenFails | func (test) | internal/identity/upstream_token_test.go |
| TestJetonDegisHTTPHatasiYutulmaz | TestExchangeCode_HTTPErrorNotSwallowed | func (test) | internal/identity/upstream_token_test.go |
| TestJetonDegisZamanAsimiUygular | TestExchangeCode_AppliesTimeout | func (test) | internal/identity/upstream_token_test.go |
| TestDurumTekKullanimlik | TestRequestStore_StateSingleUse | func (test) | internal/identity/upstream_test.go |
| TestBilinmeyenDurumErrDurumYok | TestRequestStore_UnknownStateNotFound | func (test) | internal/identity/upstream_test.go |
| TestAuthorizeURLGoogle | TestUpstream_AuthorizeURLGoogle | func (test) | internal/identity/upstream_test.go |
| TestAuthorizeURLApplePKCEGondermez | TestUpstream_AuthorizeURLAppleOmitsPKCE | func (test) | internal/identity/upstream_test.go |
| TestKodDogrulayiciUret | TestGenerateCodeVerifier | func (test) | internal/identity/upstream_test.go |

Rows whose kind starts with `manual:` are skipped by `apply_renames.py`. Do each by hand:
- An **embedded field** row needs no action. Renaming the embedded type renames the field and every selector on it. Check the result in the diff.
- A **`var _ X = (*Y)(nil)`** row needs no action. The assertion follows the renamed types. Check it.
- An **anonymous struct field**, **local** or **param** row: run `~/go/bin/gopls rename -w <file>:<line>:<col> <new>` on the declaring identifier.
- A **package** row is done by the package-move step.
- A **"methods unchanged; receiver becomes ..."** row needs no action. The methods keep their `op.*` names and follow the type rename.

**Wire strings to keep (must stay byte-identical)**
_No glossary wire row names these files; the global wire rule and the guard still apply._

**Risks (from the glossaries; names in them are the old ones)**

_From the kimlik glossary, risk R3:_

**R3: error text that is user-visible or asserted.**
- `ErrSifreKisa.Error()` is rendered into kayit.html (sayfa.go:631), so keep `"sifre en az 10 karakter olmali"`, or decouple it into a msg const with the same text.
- `oidc.ErrInvalidClient().WithDescription("jeton bu istemci icin verilmemis")` reaches the client.
- Tests match error substrings:
  - sunucu_test.go:349 `"veritabanina baglanilamadi"` (the skip detection; translating it without the test makes the test FAIL instead of SKIP when no Postgres is present);
  - sunucu_test.go:373 `"hiz limiti kurulamadi"`;
  - the env-var and config-key substrings (4.9), plus `"https"` (sunucu_test.go:50, 65, 80).

  Translate source and test in the same change.
- hizlimit.go:109-118 messages cite **stale** config keys (`giris_ip_per_min` and so on). While translating, use the real keys `login_ip_per_min`, `login_email_per_hour`, `signup_ip_per_hour` and `trusted_proxies`. No test asserts on them.

_From the kimlik glossary, risk R5:_

**R5: name collisions and shadowing after rename.**
- Field and method names must differ on the same type. `FedereSayfalar.geriCagirma` and `geriURL` would both want "callbackURL", so the plan uses `authCallback` (field) and `upstreamRedirectURI` (method). `fedOrtam.geri` → `callback` and `.baslat` → `start` are methods on a different type, so they are fine.
- Package level:
  - type `Storage` vs the `storage op.Storage` parameter in `pkceEnforcer.ValidateAuthRequest`: the parameter shadows the type only inside that function, which never uses the type. Harmless.
  - type `SigningKey` vs method `Storage.SigningKey`, and type `PublicKey` vs method `SigningKey.PublicKey`: legal.
  - `Start` (func) vs `FederationPages.Start` (method): legal.
  - `Server.ready` field vs `Ready()` method: legal.
- Test helpers: `al`/`govde` would naturally become `get`/`body`, but many tests already have locals named `body`. That is why the plan uses `mustGet`/`readBody`. Similarly `stepContext` (func) vs `TestStepContext` is fine, and `bindTestSession` (sayfa_test func) is kept apart from `fedEnv.bindSession`.
- Federation-page and authorize-callback tests would both start `TestCallback_…`, so the plan prefixes `TestFederationStart_`, `TestFederationCallback_` and `TestAuthorizeCallback_`.
- Two terms have to stay distinct:
  - "baglama" means **session binding** and also **account link**. Do not merge `BindSession`/`SessionBinding` with `Link`/`PendingLink`.
  - "kayit" means **record** and also **signup**.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 12 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 3: Do the `manual:` rows**

Follow the rules under the table. Then run:

```bash
go build ./... && go vet ./...
```

- [ ] **Step 4: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- `redis_json_test.go` (created in Task 7) already has English names. Check that its comments are English.

Files (25): `internal/identity/account_delete_test.go`, `internal/identity/client_test.go`, `internal/identity/cookie_jar_test.go`, `internal/identity/database_test.go`, `internal/identity/fake_upstream_test.go`, `internal/identity/federated_store_contract_test.go`, `internal/identity/federation_flow_test.go`, `internal/identity/federation_page_test.go`, `internal/identity/flow_test.go`, `internal/identity/link_page_test.go`, `internal/identity/page_test.go`, `internal/identity/ratelimit_test.go`, `internal/identity/redis_json_test.go`, `internal/identity/request_test.go`, `internal/identity/server_test.go`, `internal/identity/signing_key_test.go`, `internal/identity/storage_revoke_test.go`, `internal/identity/storage_test.go`, `internal/identity/token_store_pg_test.go`, `internal/identity/token_store_test.go`, `internal/identity/upstream_apple_test.go`, `internal/identity/upstream_test.go`, `internal/identity/upstream_token_test.go`, `internal/identity/user_store_contract_test.go`, `internal/identity/user_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/identity/account_delete_test.go internal/identity/client_test.go internal/identity/cookie_jar_test.go internal/identity/database_test.go internal/identity/fake_upstream_test.go internal/identity/federated_store_contract_test.go internal/identity/federation_flow_test.go internal/identity/federation_page_test.go internal/identity/flow_test.go internal/identity/link_page_test.go internal/identity/page_test.go internal/identity/ratelimit_test.go internal/identity/redis_json_test.go internal/identity/request_test.go internal/identity/server_test.go internal/identity/signing_key_test.go internal/identity/storage_revoke_test.go internal/identity/storage_test.go internal/identity/token_store_pg_test.go internal/identity/token_store_test.go internal/identity/upstream_apple_test.go internal/identity/upstream_test.go internal/identity/upstream_token_test.go internal/identity/user_store_contract_test.go internal/identity/user_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 5: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 12 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 6: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 7: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(identity): test yardimcilari ve test adlari Ingilizce

identity testlerinin sahte nesneleri, yardimcilari ve test adlari
TestType_Behavior bicimine cevrildi. Test mesajlari ayni; kablo
goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 13: social: package move `internal/sosyal` → `internal/social`, file renames, store layer

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/sosyal/anahtarlar.go` | `internal/social/keys.go` |
| `internal/sosyal/anahtarlar_test.go` | `internal/social/keys_test.go` |
| `internal/sosyal/butunlesik_test.go` | `internal/social/integration_test.go` |
| `internal/sosyal/depo.go` | `internal/social/store.go` |
| `internal/sosyal/depo_akis.go` | `internal/social/store_feed.go` |
| `internal/sosyal/depo_akis_test.go` | `internal/social/store_feed_test.go` |
| `internal/sosyal/depo_cover.go` | `internal/social/store_cover.go` |
| `internal/sosyal/depo_cover_test.go` | `internal/social/store_cover_test.go` |
| `internal/sosyal/depo_onizleme.go` | `internal/social/store_preview.go` |
| `internal/sosyal/depo_onizleme_test.go` | `internal/social/store_preview_test.go` |
| `internal/sosyal/depo_profil_test.go` | `internal/social/store_profile_test.go` |
| `internal/sosyal/depo_sikayet.go` | `internal/social/store_report.go` |
| `internal/sosyal/depo_sikayet_test.go` | `internal/social/store_report_test.go` |
| `internal/sosyal/depo_takip.go` | `internal/social/store_follow.go` |
| `internal/sosyal/depo_takip_test.go` | `internal/social/store_follow_test.go` |
| `internal/sosyal/depo_temizlik.go` | `internal/social/store_cleanup.go` |
| `internal/sosyal/depo_temizlik_test.go` | `internal/social/store_cleanup_test.go` |
| `internal/sosyal/depo_yardimci_test.go` | `internal/social/store_helpers_test.go` |
| `internal/sosyal/depo_yayin.go` | `internal/social/store_publication.go` |
| `internal/sosyal/depo_yayin_test.go` | `internal/social/store_publication_test.go` |
| `internal/sosyal/depo_yorum.go` | `internal/social/store_comment.go` |
| `internal/sosyal/depo_yorum_test.go` | `internal/social/store_comment_test.go` |
| `internal/sosyal/gorunurluk.go` | `internal/social/visibility.go` |
| `internal/sosyal/gorunurluk_test.go` | `internal/social/visibility_test.go` |
| `internal/sosyal/hatalar.go` | `internal/social/errors.go` |
| `internal/sosyal/imlec.go` | `internal/social/cursor.go` |
| `internal/sosyal/imlec_test.go` | `internal/social/cursor_test.go` |
| `internal/sosyal/kurallar.go` | `internal/social/profile_rules.go` |
| `internal/sosyal/kurallar_test.go` | `internal/social/profile_rules_test.go` |
| `internal/sosyal/model.go` | `internal/social/model.go` |
| `internal/sosyal/model_yayin.go` | `internal/social/model_publication.go` |
| `internal/sosyal/model_yorum.go` | `internal/social/model_comment.go` |
| `internal/sosyal/sahte_depo_test.go` | `internal/social/fake_store_test.go` |
| `internal/sosyal/servis.go` | `internal/social/service.go` |
| `internal/sosyal/servis_akis.go` | `internal/social/service_feed.go` |
| `internal/sosyal/servis_akis_test.go` | `internal/social/service_feed_test.go` |
| `internal/sosyal/servis_cover.go` | `internal/social/service_cover.go` |
| `internal/sosyal/servis_cover_test.go` | `internal/social/service_cover_test.go` |
| `internal/sosyal/servis_onizleme.go` | `internal/social/service_preview.go` |
| `internal/sosyal/servis_onizleme_test.go` | `internal/social/service_preview_test.go` |
| `internal/sosyal/servis_profil_test.go` | `internal/social/service_profile_test.go` |
| `internal/sosyal/servis_sikayet.go` | `internal/social/service_report.go` |
| `internal/sosyal/servis_sikayet_test.go` | `internal/social/service_report_test.go` |
| `internal/sosyal/servis_takip.go` | `internal/social/service_follow.go` |
| `internal/sosyal/servis_takip_test.go` | `internal/social/service_follow_test.go` |
| `internal/sosyal/servis_yayin.go` | `internal/social/service_publication.go` |
| `internal/sosyal/servis_yayin_test.go` | `internal/social/service_publication_test.go` |
| `internal/sosyal/servis_yorum.go` | `internal/social/service_comment.go` |
| `internal/sosyal/servis_yorum_test.go` | `internal/social/service_comment_test.go` |
| `internal/sosyal/slug.go` | `internal/social/slug.go` |
| `internal/sosyal/slug_test.go` | `internal/social/slug_test.go` |
| `internal/sosyal/veritabani.go` | `internal/social/database.go` |
| `internal/sosyal/veritabani_test.go` | `internal/social/database_test.go` |
| `internal/sosyal/yayin_kurallari.go` | `internal/social/publication_rules.go` |
| `internal/sosyal/yayin_kurallari_test.go` | `internal/social/publication_rules_test.go` |

The package clause becomes `package social`. Also modify: `tools/wirecheck/main.go` (the `sosyal-migration-001` asset path only).

`service.SosyalTemizleyici` (account deletion) is implemented by `social.PostgresDepo`, and `social` never imports `service`. So part 1 renames each interface method with gopls (the `service` test fake follows), and `apply_renames.py` renames the matching concrete method textually right after it (the `manual: textual` rows).

**Declarations, part 1: run first** (9 rows)

<!-- renames:13-first -->
| old | new | kind | file |
|---|---|---|---|
| SosyalTemizleyici.YayinlariListele | SocialCleaner.ListPublicationIDs | interface method (implemented by sosyal.PostgresDepo; see R3) | internal/service/hesap_service.go |
| SosyalTemizleyici.KullaniciIzleriniSil | SocialCleaner.DeleteUserTraces | interface method (same) | internal/service/hesap_service.go |
| SosyalTemizleyici.KullaniciVerisiniSil | SocialCleaner.DeleteUserData | interface method (same) | internal/service/hesap_service.go |
| sahteSosyalTemizleyici.KullaniciIzleriniSil | fakeSocialCleaner.DeleteUserTraces | method (test) | internal/service/hesap_sosyal_test.go |
| sahteSosyalTemizleyici.YayinlariListele | fakeSocialCleaner.ListPublicationIDs | method (test) | internal/service/hesap_sosyal_test.go |
| sahteSosyalTemizleyici.KullaniciVerisiniSil | fakeSocialCleaner.DeleteUserData | method (test) | internal/service/hesap_sosyal_test.go |
| PostgresDepo.YayinlariListele | PostgresStore.ListPublicationIDs | manual: textual, method (called via service.SosyalTemizleyici) (renamed by apply_renames.py right after the interface method) | internal/social/store_cleanup.go |
| PostgresDepo.KullaniciIzleriniSil | PostgresStore.DeleteUserTraces | manual: textual, method (called via service.SosyalTemizleyici) (renamed by apply_renames.py right after the interface method) | internal/social/store_cleanup.go |
| PostgresDepo.KullaniciVerisiniSil | PostgresStore.DeleteUserData | manual: textual, method (called via service.SosyalTemizleyici) (renamed by apply_renames.py right after the interface method) | internal/social/store_cleanup.go |

**Declarations, part 2** (171 rows)

<!-- renames:13 -->
| old | new | kind | file |
|---|---|---|---|
| AkisDeposu | FeedStore | type (interface) | internal/social/store_feed.go |
| BegeniDeposu | LikeStore | type (interface) | internal/social/store_feed.go |
| PuanliOzet | ScoredSummary | type | internal/social/store_feed.go |
| CoverDeposu | CoverStore | type (interface) | internal/social/store_cover.go |
| OnizlemeDeposu | PreviewStore | type (interface) | internal/social/store_preview.go |
| SikayetDeposu | ReportStore | type (interface) | internal/social/store_report.go |
| TakipDeposu | FollowStore | type (interface) | internal/social/store_follow.go |
| YayinDeposu | PublicationStore | type (interface) | internal/social/store_publication.go |
| HazirlikDeposu | PreparationStore | type (interface) | internal/social/store_publication.go |
| ozetTarayici | summaryScanner | type | internal/social/store_publication.go |
| YorumDeposu | CommentStore | type (interface) | internal/social/store_comment.go |
| ProfilDeposu | ProfileStore | type (interface) | internal/social/store.go |
| Depo | Store | type (interface) | internal/social/store.go |
| PostgresDepo | PostgresStore | type | internal/social/store.go |
| AkisDeposu.ProfilYayinlari | FeedStore.ListProfilePublications | interface method | internal/social/store_feed.go |
| AkisDeposu.AkisYeni | FeedStore.FeedNew | interface method | internal/social/store_feed.go |
| AkisDeposu.AkisTakip | FeedStore.FeedFollowing | interface method | internal/social/store_feed.go |
| AkisDeposu.AkisSeninIcin | FeedStore.FeedForYou | interface method | internal/social/store_feed.go |
| BegeniDeposu.Begen | LikeStore.Like | interface method | internal/social/store_feed.go |
| BegeniDeposu.BegeniyiKaldir | LikeStore.Unlike | interface method | internal/social/store_feed.go |
| CoverDeposu.Coverlar | CoverStore.ListCovers | interface method | internal/social/store_cover.go |
| CoverDeposu.StemListesi | CoverStore.ListStems | interface method | internal/social/store_cover.go |
| OnizlemeDeposu.OnizlemeGetir | PreviewStore.GetPreview | interface method | internal/social/store_preview.go |
| SikayetDeposu.SikayetEkle | ReportStore.InsertReport | interface method | internal/social/store_report.go |
| TakipDeposu.TakipEt | FollowStore.Follow | interface method | internal/social/store_follow.go |
| TakipDeposu.TakibiBirak | FollowStore.Unfollow | interface method | internal/social/store_follow.go |
| TakipDeposu.TakipDurumu | FollowStore.FollowStatus | interface method | internal/social/store_follow.go |
| TakipDeposu.Oneriler | FollowStore.Suggestions | interface method | internal/social/store_follow.go |
| YayinDeposu.YayinEkle | PublicationStore.InsertPublication | interface method | internal/social/store_publication.go |
| YayinDeposu.YayinGetir | PublicationStore.GetPublication | interface method | internal/social/store_publication.go |
| YayinDeposu.YayinSlugIleGetir | PublicationStore.GetPublicationBySlug | interface method | internal/social/store_publication.go |
| YayinDeposu.YayinGuncelle | PublicationStore.UpdatePublication | interface method | internal/social/store_publication.go |
| YayinDeposu.YayinSil | PublicationStore.DeletePublication | interface method | internal/social/store_publication.go |
| YayinDeposu.YayinKaldir | PublicationStore.RemovePublication | interface method | internal/social/store_publication.go |
| YayinDeposu.TekrarDenemeyeAl | PublicationStore.ResetForRetry | interface method | internal/social/store_publication.go |
| HazirlikDeposu.HazirlikGetir | PreparationStore.GetPreparation | interface method | internal/social/store_publication.go |
| HazirlikDeposu.Yayinla | PreparationStore.Publish | interface method | internal/social/store_publication.go |
| HazirlikDeposu.HataYap | PreparationStore.MarkFailed | interface method | internal/social/store_publication.go |
| YorumDeposu.YorumEkle | CommentStore.InsertComment | interface method | internal/social/store_comment.go |
| YorumDeposu.Yorumlar | CommentStore.ListComments | interface method | internal/social/store_comment.go |
| YorumDeposu.YorumGetir | CommentStore.GetComment | interface method | internal/social/store_comment.go |
| YorumDeposu.YorumSil | CommentStore.DeleteComment | interface method | internal/social/store_comment.go |
| ProfilDeposu.ProfilGetir | ProfileStore.GetProfile | interface method | internal/social/store.go |
| ProfilDeposu.ProfilAdlaGetir | ProfileStore.GetProfileByUsername | interface method | internal/social/store.go |
| ProfilDeposu.ProfilKaydet | ProfileStore.UpsertProfile | interface method | internal/social/store.go |
| ProfilDeposu.AdSahipleri | ProfileStore.UsernameOwners | interface method | internal/social/store.go |
| ProfilDeposu.ProfilSayilari | ProfileStore.ProfileCounts | interface method | internal/social/store.go |
| Depo.<ProfilDeposu…OnizlemeDeposu> | Store.<ProfileStore, PublicationStore, PreparationStore, FeedStore, LikeStore, FollowStore, CommentStore, ReportStore, CoverStore, PreviewStore> | manual: embedded interfaces .<ProfileStore, PublicationStore, PreparationStore, FeedStore, LikeStore, FollowStore, CommentStore, ReportStore, CoverStore, PreviewStore> | internal/social/store.go |
| PostgresDepo.zamanSayfasi | PostgresStore.timePage | method | internal/social/store_feed.go |
| PostgresDepo.ProfilYayinlari | PostgresStore.ListProfilePublications | method | internal/social/store_feed.go |
| PostgresDepo.AkisYeni | PostgresStore.FeedNew | method | internal/social/store_feed.go |
| PostgresDepo.AkisTakip | PostgresStore.FeedFollowing | method | internal/social/store_feed.go |
| PostgresDepo.AkisSeninIcin | PostgresStore.FeedForYou | method | internal/social/store_feed.go |
| PostgresDepo.Begen | PostgresStore.Like | method | internal/social/store_feed.go |
| PostgresDepo.BegeniyiKaldir | PostgresStore.Unlike | method | internal/social/store_feed.go |
| PostgresDepo.Coverlar | PostgresStore.ListCovers | method | internal/social/store_cover.go |
| PostgresDepo.StemListesi | PostgresStore.ListStems | method | internal/social/store_cover.go |
| PostgresDepo.OnizlemeGetir | PostgresStore.GetPreview | method | internal/social/store_preview.go |
| PostgresDepo.SikayetEkle | PostgresStore.InsertReport | method | internal/social/store_report.go |
| PostgresDepo.TakipEt | PostgresStore.Follow | method | internal/social/store_follow.go |
| PostgresDepo.TakibiBirak | PostgresStore.Unfollow | method | internal/social/store_follow.go |
| PostgresDepo.TakipDurumu | PostgresStore.FollowStatus | method | internal/social/store_follow.go |
| PostgresDepo.Oneriler | PostgresStore.Suggestions | method | internal/social/store_follow.go |
| PostgresDepo.islemdeSil | PostgresStore.deleteInTx | method | internal/social/store_cleanup.go |
| ozetTarayici.hedefler | summaryScanner.targets | method | internal/social/store_publication.go |
| ozetTarayici.ozet | summaryScanner.summary | method | internal/social/store_publication.go |
| PostgresDepo.yayinGetir | PostgresStore.getPublication | method | internal/social/store_publication.go |
| PostgresDepo.YayinGetir | PostgresStore.GetPublication | method | internal/social/store_publication.go |
| PostgresDepo.YayinSlugIleGetir | PostgresStore.GetPublicationBySlug | method | internal/social/store_publication.go |
| PostgresDepo.YayinEkle | PostgresStore.InsertPublication | method | internal/social/store_publication.go |
| PostgresDepo.YayinGuncelle | PostgresStore.UpdatePublication | method | internal/social/store_publication.go |
| PostgresDepo.YayinSil | PostgresStore.DeletePublication | method | internal/social/store_publication.go |
| PostgresDepo.YayinKaldir | PostgresStore.RemovePublication | method | internal/social/store_publication.go |
| PostgresDepo.TekrarDenemeyeAl | PostgresStore.ResetForRetry | method | internal/social/store_publication.go |
| PostgresDepo.HazirlikGetir | PostgresStore.GetPreparation | method | internal/social/store_publication.go |
| PostgresDepo.Yayinla | PostgresStore.Publish | method | internal/social/store_publication.go |
| PostgresDepo.HataYap | PostgresStore.MarkFailed | method | internal/social/store_publication.go |
| PostgresDepo.YorumEkle | PostgresStore.InsertComment | method | internal/social/store_comment.go |
| PostgresDepo.Yorumlar | PostgresStore.ListComments | method | internal/social/store_comment.go |
| PostgresDepo.YorumGetir | PostgresStore.GetComment | method | internal/social/store_comment.go |
| PostgresDepo.YorumSil | PostgresStore.DeleteComment | method | internal/social/store_comment.go |
| PostgresDepo.ProfilGetir | PostgresStore.GetProfile | method | internal/social/store.go |
| PostgresDepo.ProfilAdlaGetir | PostgresStore.GetProfileByUsername | method | internal/social/store.go |
| PostgresDepo.ProfilKaydet | PostgresStore.UpsertProfile | method | internal/social/store.go |
| PostgresDepo.AdSahipleri | PostgresStore.UsernameOwners | method | internal/social/store.go |
| PostgresDepo.ProfilSayilari | PostgresStore.ProfileCounts | method | internal/social/store.go |
| ozetleriTara | scanSummaries | func | internal/social/store_feed.go |
| yorumTara | scanComment | func | internal/social/store_comment.go |
| NewPostgresDepo | NewPostgresStore | func | internal/social/store.go |
| tekilIhlali | uniqueViolation | func | internal/social/store.go |
| profilTara | scanProfile | func | internal/social/store.go |
| dogrulamaHatasi | validationError | func | internal/social/errors.go |
| migrationAdlari | migrationNames | func | internal/social/database.go |
| puanIfadesi | scoreExpr | const (SQL) | internal/social/store_feed.go |
| onizlemeKosulu | previewCondition | const (SQL) | internal/social/store_preview.go |
| yorumSikayetleriniSil | deleteCommentReportsSQL | const (SQL) | internal/social/store_cleanup.go |
| izSorgulari | traceDeleteQueries | var (SQL list) | internal/social/store_cleanup.go |
| ozetSecimi | summarySelect | const (SQL) | internal/social/store_publication.go |
| ozetKaynagi | summaryFrom | const (SQL) | internal/social/store_publication.go |
| yorumKolonlari | commentColumns | const (SQL) | internal/social/store_comment.go |
| _ (var _ Depo = (*PostgresDepo)(nil)) | var _ Store = (*PostgresStore)(nil) | manual: var _ Store = (*PostgresStore)(nil) | internal/social/store.go |
| profilKolonlari | profileColumns | const (SQL) | internal/social/store.go |
| profilKolonlariP | profileColumnsP | const (SQL) | internal/social/store.go |
| ErrBulunamadi | ErrNotFound | var | internal/social/errors.go |
| ErrProfilYok | ErrNoProfile | var | internal/social/errors.go |
| ErrGecersizKullaniciAdi | ErrInvalidUsername | var | internal/social/errors.go |
| ErrKullaniciAdiAlinmis | ErrUsernameTaken | var | internal/social/errors.go |
| ErrDogrulama | ErrValidation | var | internal/social/errors.go |
| ErrSahiplik | ErrOwnership | var | internal/social/errors.go |
| ErrKaynakBulunamadi | ErrSourceNotFound | var | internal/social/errors.go |
| ErrAltyapiKapali | ErrBackingDisabled | var | internal/social/errors.go |
| ErrStemYok | ErrNoStems | var | internal/social/errors.go |
| ErrDurum | ErrInvalidStatus | var | internal/social/errors.go |
| ErrKimlik | ErrInvalidIdentity | var | internal/social/errors.go |
| ErrKendiniTakip | ErrSelfFollow | var | internal/social/errors.go |
| ErrYorumKapali | ErrCommentsDisabled | var | internal/social/errors.go |
| ErrYetki | ErrForbidden | var | internal/social/errors.go |
| migrationKilitAnahtari | migrationLockKey | const (value 0x736F7379616C31 unchanged) | internal/social/database.go |
| PuanliOzet.<YayinOzeti> | ScoredSummary.<PublicationSummary> | manual: embedded field .<PublicationSummary> | internal/social/store_feed.go |
| PuanliOzet.Puan | ScoredSummary.Score | field (no tag; not serialized) | internal/social/store_feed.go |
| ozetTarayici.o | summaryScanner.base | field | internal/social/store_publication.go |
| ozetTarayici.sahipID | summaryScanner.ownerID | field | internal/social/store_publication.go |
| ozetTarayici.kaynakID | summaryScanner.sourceID | field | internal/social/store_publication.go |
| ozetTarayici.kaynakBaslik | summaryScanner.sourceTitle | field | internal/social/store_publication.go |
| ozetTarayici.kaynakAdi | summaryScanner.sourceUsername | field | internal/social/store_publication.go |
| ozetTarayici.kaynakSilindi | summaryScanner.sourceDeleted | field | internal/social/store_publication.go |
| ozetTarayici.dalgaFormu | summaryScanner.waveform | field | internal/social/store_publication.go |
| ozetTarayici.olusturma | summaryScanner.createdAt | field | internal/social/store_publication.go |
| ozetTarayici.begeni | summaryScanner.likes | field | internal/social/store_publication.go |
| ozetTarayici.yorum | summaryScanner.comments | field | internal/social/store_publication.go |
| ozetTarayici.cover | summaryScanner.covers | field | internal/social/store_publication.go |
| ozetTarayici.begendimMi | summaryScanner.likedByMe | field | internal/social/store_publication.go |
| ozetTarayici.tonik | summaryScanner.tonic | field | internal/social/store_publication.go |
| ozetTarayici.tonModu | summaryScanner.mode | field | internal/social/store_publication.go |
| PostgresDepo.havuz | PostgresStore.pool | field | internal/social/store.go |
| sahteDepo.ProfilGetir | fakeStore.GetProfile | method | internal/social/fake_store_test.go |
| sahteDepo.ProfilAdlaGetir | fakeStore.GetProfileByUsername | method | internal/social/fake_store_test.go |
| sahteDepo.ProfilKaydet | fakeStore.UpsertProfile | method | internal/social/fake_store_test.go |
| sahteDepo.AdSahipleri | fakeStore.UsernameOwners | method | internal/social/fake_store_test.go |
| sahteDepo.ProfilSayilari | fakeStore.ProfileCounts | method | internal/social/fake_store_test.go |
| sahteDepo.YayinEkle | fakeStore.InsertPublication | method | internal/social/fake_store_test.go |
| sahteDepo.YayinGetir | fakeStore.GetPublication | method | internal/social/fake_store_test.go |
| sahteDepo.YayinSlugIleGetir | fakeStore.GetPublicationBySlug | method | internal/social/fake_store_test.go |
| sahteDepo.YayinGuncelle | fakeStore.UpdatePublication | method | internal/social/fake_store_test.go |
| sahteDepo.YayinSil | fakeStore.DeletePublication | method | internal/social/fake_store_test.go |
| sahteDepo.YayinKaldir | fakeStore.RemovePublication | method | internal/social/fake_store_test.go |
| sahteDepo.TekrarDenemeyeAl | fakeStore.ResetForRetry | method | internal/social/fake_store_test.go |
| sahteDepo.HazirlikGetir | fakeStore.GetPreparation | method | internal/social/fake_store_test.go |
| sahteDepo.Yayinla | fakeStore.Publish | method | internal/social/fake_store_test.go |
| sahteDepo.HataYap | fakeStore.MarkFailed | method | internal/social/fake_store_test.go |
| sahteDepo.ProfilYayinlari | fakeStore.ListProfilePublications | method | internal/social/fake_store_test.go |
| sahteDepo.AkisYeni | fakeStore.FeedNew | method | internal/social/fake_store_test.go |
| sahteDepo.AkisTakip | fakeStore.FeedFollowing | method | internal/social/fake_store_test.go |
| sahteDepo.AkisSeninIcin | fakeStore.FeedForYou | method | internal/social/fake_store_test.go |
| sahteDepo.Begen | fakeStore.Like | method | internal/social/fake_store_test.go |
| sahteDepo.BegeniyiKaldir | fakeStore.Unlike | method | internal/social/fake_store_test.go |
| sahteDepo.TakipEt | fakeStore.Follow | method | internal/social/fake_store_test.go |
| sahteDepo.TakibiBirak | fakeStore.Unfollow | method | internal/social/fake_store_test.go |
| sahteDepo.TakipDurumu | fakeStore.FollowStatus | method | internal/social/fake_store_test.go |
| sahteDepo.Oneriler | fakeStore.Suggestions | method | internal/social/fake_store_test.go |
| sahteDepo.YorumEkle | fakeStore.InsertComment | method | internal/social/fake_store_test.go |
| sahteDepo.Yorumlar | fakeStore.ListComments | method | internal/social/fake_store_test.go |
| sahteDepo.YorumGetir | fakeStore.GetComment | method | internal/social/fake_store_test.go |
| sahteDepo.YorumSil | fakeStore.DeleteComment | method | internal/social/fake_store_test.go |
| sahteDepo.SikayetEkle | fakeStore.InsertReport | method | internal/social/fake_store_test.go |
| sahteDepo.Coverlar | fakeStore.ListCovers | method | internal/social/fake_store_test.go |
| sahteDepo.StemListesi | fakeStore.ListStems | method | internal/social/fake_store_test.go |
| sahteDepo.OnizlemeGetir | fakeStore.GetPreview | method | internal/social/fake_store_test.go |
| sahteHazirlikDeposu.HazirlikGetir | fakePreparationStore.GetPreparation | method | internal/worker/sosyal_worker_test.go |
| sahteHazirlikDeposu.Yayinla | fakePreparationStore.Publish | method | internal/worker/sosyal_worker_test.go |
| sahteHazirlikDeposu.HataYap | fakePreparationStore.MarkFailed | method | internal/worker/sosyal_worker_test.go |

Rows whose kind starts with `manual:` are skipped by `apply_renames.py`. Do each by hand:
- An **embedded field** row needs no action. Renaming the embedded type renames the field and every selector on it. Check the result in the diff.
- A **`var _ X = (*Y)(nil)`** row needs no action. The assertion follows the renamed types. Check it.
- An **anonymous struct field**, **local** or **param** row: run `~/go/bin/gopls rename -w <file>:<line>:<col> <new>` on the declaring identifier.
- A **package** row is done by the package-move step.
- A **"methods unchanged; receiver becomes ..."** row needs no action. The methods keep their `op.*` names and follow the type rename.

**Wire strings to keep (must stay byte-identical)**

_From the sosyal glossary, §4.2 Typed or enum constant values (only the identifier changes):_

| file:line | literal | why |
|---|---|---|
| sosyal/veritabani.go:22 | `0x736F7379616C31` | advisory lock value |

_From the sosyal glossary, §4.4 SQL: every query string is wire (schema `sosyal`, tables, columns, constraint names):_

| file:line | content |
|---|---|
| sosyal/depo.go:58 | pg error code `"23505"` |
| sosyal/depo.go:64, 67 | `profilKolonlari`, `profilKolonlariP` column lists |
| sosyal/depo.go:79, 91, 102-109, 126, 142-152 | profile SELECT, INSERT … ON CONFLICT, AdSahipleri and ProfilSayilari queries; `gorunurlukKosulu("y", "$2", false)` |
| sosyal/depo_akis.go:47-51 | timePage `fmt.Sprintf` SQL template |
| sosyal/depo_akis.go:73, 78, 83-84 | feed conditions (`'herkes'`, `'takipciler'`, `'yayinda'`, `sosyal.takip`) |
| sosyal/depo_akis.go:92-103 | `puanIfadesi` scoring SQL (`interval '7 days'`) |
| sosyal/depo_akis.go:112-121 | for-you query (`puan`, `sira_id` aliases) |
| sosyal/depo_akis.go:142-144, 151-152 | `sosyal.begeni` INSERT and DELETE |
| sosyal/depo_cover.go:23, 29 | cover condition; `stem_listesi` select |
| sosyal/depo_onizleme.go:22, 30-34 | `onizlemeKosulu`; preview select |
| sosyal/depo_sikayet.go:16-19 | `sosyal.sikayet` INSERT … ON CONFLICT (bildiren_id, hedef_turu, hedef_id) |
| sosyal/depo_takip.go:22-24, 31-32, 41-43, 56-71 | follow, unfollow and status queries; suggestions query (`interval '30 days'`) |
| sosyal/depo_temizlik.go:14, 33-34, 39-43, 60 | cleanup queries |
| sosyal/depo_yayin.go:50-62, 64-68 | `ozetSecimi`, `ozetKaynagi` |
| sosyal/depo_yayin.go:127-135 | detail query (`hazirlik->'stems'`); `"y.id"` / `"y.slug"` condition literals at :169 and :173 |
| sosyal/depo_yayin.go:184-189 | INSERT sosyal.yayin (… `'hazirlaniyor'`) |
| sosyal/depo_yayin.go:193 | constraint name `"yayin_slug_key"` (Postgres auto-name; wire) |
| sosyal/depo_yayin.go:203-209, 221-223, 234, 241-243, 251-252, 264-266, 286-290, 299-300 | update, delete, retry, preparation, publish and fail queries |
| sosyal/depo_yorum.go:26, 44-50, 70-85, 105-108, 120 | comment queries |
| sosyal/veritabani.go:16 | `//go:embed migrations/*.sql` |
| sosyal/veritabani.go:25 | `ReadDir("migrations")` |
| sosyal/veritabani.go:50, 54 | `pg_advisory_lock($1)`, `pg_advisory_unlock($1)` |
| sosyal/veritabani.go:64 | `CREATE SCHEMA IF NOT EXISTS sosyal` |
| sosyal/veritabani.go:67-71 | `sosyal.schema_migrations (version, applied_at)` |
| sosyal/veritabani.go:82, 89, 102 | `schema_migrations` lookup; `"migrations/" + ad`; INSERT version |
| sosyal/migrations/001_sosyal.sql (whole file, 101 lines) | file name and contents frozen. The migration version is recorded as `001_sosyal.sql`. |

_From the sosyal glossary, §4.6 Message text: NOT wire, to be localized later:_

| file:line | literal | note |
|---|---|---|
| sosyal/hatalar.go:13 | `ErrBulunamadi` = `"bulunamadi"` | message text, localized later. Note: the text happens to equal the code `bulunamadi`. The handler sends `err.Error()` as `message`. |
| sosyal/hatalar.go:14 | `ErrProfilYok` = `"profil henuz olusturulmamis"` | message text, localized later |
| sosyal/hatalar.go:15 | `ErrGecersizKullaniciAdi` = `"gecersiz kullanici adi"` | message text, localized later |
| sosyal/hatalar.go:16 | `ErrKullaniciAdiAlinmis` = `"kullanici adi alinmis"` | message text, localized later |
| sosyal/hatalar.go:17 | `ErrDogrulama` = `"dogrulama hatasi"` | message text, localized later |
| sosyal/hatalar.go:18 | `ErrSahiplik` = `"dosya bu kullaniciya ait degil"` | message text, localized later |
| sosyal/hatalar.go:19 | `ErrKaynakBulunamadi` = `"kaynak yayin bulunamadi"` | message text, localized later |
| sosyal/hatalar.go:20 | `ErrAltyapiKapali` = `"kaynak yayinin altyapisi kapali"` | message text, localized later |
| sosyal/hatalar.go:21 | `ErrStemYok` = `"yayinin stem kopyasi yok"` | message text, localized later |
| sosyal/hatalar.go:22 | `ErrDurum` = `"yayin bu islem icin uygun durumda degil"` | message text, localized later |
| sosyal/hatalar.go:23 | `ErrKimlik` = `"kimlik gecersiz"` | message text (never sent; handler maps it to 401) |
| sosyal/hatalar.go:24 | `ErrKendiniTakip` = `"kendini takip edemezsin"` | message text, localized later |
| sosyal/hatalar.go:25 | `ErrYorumKapali` = `"yayinin yorumlari kapali"` | message text, localized later |
| sosyal/hatalar.go:26 | `ErrYetki` = `"bu islem icin yetkin yok"` | message text, localized later |
| sosyal/*: every `dogrulamaHatasi("…")` literal (imlec.go:60,76,93,123; kurallar.go:68,71,74; servis_akis.go:59,114; servis_sikayet.go:53,56,60,63; servis_yayin.go:64,66; servis_yorum.go:63,66,69,74,87; yayin_kurallari.go:59-131,139-147,158,161,166) | Turkish validation details | Message text, localized later. Each literal embeds JSON field names (`gorunenAd`, `masterUrl`, `zamanMs`, `hedefTuru`, `ton.mod`, `sureSn`…), which should stay as the JSON names when translated. |
| all `fmt.Errorf` / `log.Printf` literals in scope | wrapped error and log text | translate freely (not wire) |

**Risks (from the glossaries; names in them are the old ones)**

_From the sosyal glossary, risk 1:_

1. **`service.SosyalTemizleyici` is implicit interface coupling outside this scope.** `internal/service/hesap_service.go:44-55` declares `YayinlariListele`, `KullaniciIzleriniSil` and `KullaniciVerisiniSil`. `*sosyal.PostgresDepo` satisfies that interface only by method-name match, and `cmd/server/main.go:167` passes `sosyalDepo` into `NewHesapService`. If the service-package glossary picks names other than `ListPublicationIDs`, `DeleteUserTraces` and `DeleteUserData`, main.go fails to compile. Coordinate the names with the `service` glossary owner, or update that interface to these names.

_From the sosyal glossary, risk 2:_

2. **Out-of-scope `client` and `hesapisaret` identifiers drive method names in our fakes.** The social service and worker depend on `client.Kopyalayici` (method `Kopyala`), `client.AudioAnalizci` (method `Analiz`, `AnalizRequest`, `AnalizResponse`), `client.OnekSilici`, `client.SosyalAnahtari`, `client.SosyalOneki`, `client.KullaniciAnahtari`, `client.SahipliAnahtarVeyaURLCoz`, `client.ErrYabanciAnahtar`, `client.ResolveURL`, `client.UnsignedURL`, `client.IsPublicKey` and `hesapisaret.Denetci` (which becomes `deletionmark.*`). The worker also uses `sahipYazabilir` and `errSahipSilindi` from `worker/render_worker.go`, and the handler uses `istekSahibi` (handler/istek_sahibi.go) and `kullaniciOlarak` (handler/sahiplik_canli_test.go). The tests use `kimlik.Baglan` (which becomes `identity.*`). The fakes `fakeCopier.Kopyala`, `fakeSocialCopier.Kopyala` and `fakeAnalyzer.Analiz` must take whatever names the client glossary chooses, or they stop satisfying the interfaces. That failure is silent until compile.

_From the sosyal glossary, risk 5:_

5. **Cross-package callers that must follow the renames.**
   - `cmd/server/main.go:30, 105, 108, 176-178, 266, 313-317, 367, 407-408`: `sosyal.Migrate`, `NewPostgresDepo`, `NewServis(...).KopyalayiciAyarla`, `handler.NewSosyalHandler`, `SosyalLimitleri{Yayin, Yorum, Cover}`, `PublicRotalar`, `Rotalar`, `worker.NewSosyalWorker`, `*worker.SosyalWorker`, `sosyal.TaskTypeHazirla` / `TaskTypeDosyaSil` with `sosyalWorker.Hazirla` / `DosyaSil`.
   - main.go also holds **wire values outside this scope** that must stay as they are: the rate-limit keys `"sosyal-yayin"`, `"sosyal-yorum"` and `"sosyal-cover"` (Redis prefixes) and the asynq queue priority key `"sosyal": 2`, which must equal `QueueSocial`. It also uses the config fields `cfg.Sosyal.WebAnahtari` and `cfg.RateLimit.Sosyal*PerHour/PerMin`, whose `mapstructure` / env keys stay as they are.

_From the sosyal glossary, risk 6:_

6. **`go:embed` and migrations.** `//go:embed migrations/*.sql`, `ReadDir("migrations")` and `ReadFile("migrations/"+name)` are relative to the package directory. They keep working after `internal/sosyal` becomes `internal/social`, but only if the `migrations/` directory moves with the package. Never rename `001_sosyal.sql`: `sosyal.schema_migrations.version` stores the file name, so a rename would re-run the migration. That happens to be idempotent (`IF NOT EXISTS`), but it is still a wire change. `TestMigrationlarGomuluVeSirali` reads the literal path `"migrations/001_sosyal.sql"`.

_From the sosyal glossary, risk 7:_

7. **SQL is assembled from Go-side fragments.** `gorunurlukKosulu` (alias and param via `fmt.Sprintf`), `zamanSayfasi` (`ozetSecimi` + `ozetKaynagi` + condition), `yayinGetir(ctx, "y.id"|"y.slug", …)` and `puanIfadesi` all concatenate strings into SQL. Rename only the Go identifiers. Do not touch the backtick contents, the `%[1]s`/`$N` placeholders or the alias letters (`y`, `p`, `k`, `kp`, `c`, `cv`, `s`, `tk`, `gt`). `TestPostgresDepo_GorunurlukSQLVeGoAyni` checks Go and SQL against each other, so run it against live Postgres after the change.

_From the sosyal glossary, risk 10:_

10. **Test env and DSN.** `KIMLIK_TEST_DB_URL` and the default DSN `postgres://postgres:kimlik-test-yerel@localhost:55432/...` are shared with the identity tests and CI. Keep both literals.

_From the app glossary, risk R1:_

**R1. Interface methods implemented across packages.** These renames must land in the same change as the implementations:
- `service.KimlikSilici.HesapSil` → `AccountDeleter.DeleteAccount` is implemented by `*kimlik.Sunucu.HesapSil` (main.go:167 passes `kimlikSunucu`). The identity glossary must rename `Sunucu.HesapSil` → `Server.DeleteAccount`. The test fakes `fakeIdentity` (handler and service) and `nopIdentity` (worker) follow it.
- `service.SosyalTemizleyici.{YayinlariListele, KullaniciIzleriniSil, KullaniciVerisiniSil}` → `SocialCleaner.{ListPublicationIDs, DeleteUserTraces, DeleteUserData}` is implemented by `*sosyal.PostgresDepo` (main.go:167 passes `sosyalDepo`). The social glossary must use exactly these method names, or this glossary must adopt theirs.
- `hesapisaret.Denetci.Silinmis` is implemented by worker test `fakeChecker`. Its new name comes from the deletionmark glossary (I assumed `deletionmark.Checker.IsDeleted`).
- `client.OnekSilici.DeletePrefix` and `client.StorageClient`/`client.AudioProcessor` are already English. The fakes only follow type renames.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Move the package, rename the files, update the guard**

```bash
off=$(grep -bo '^package sosyal' internal/sosyal/veritabani.go | cut -d: -f1)
~/go/bin/gopls rename -w internal/sosyal/veritabani.go:#$((off+8)) social   # prints "getFile: ... is a directory": expected
git mv internal/sosyal internal/social
git mv internal/social/anahtarlar.go internal/social/keys.go
git mv internal/social/anahtarlar_test.go internal/social/keys_test.go
git mv internal/social/butunlesik_test.go internal/social/integration_test.go
git mv internal/social/depo.go internal/social/store.go
git mv internal/social/depo_akis.go internal/social/store_feed.go
git mv internal/social/depo_akis_test.go internal/social/store_feed_test.go
git mv internal/social/depo_cover.go internal/social/store_cover.go
git mv internal/social/depo_cover_test.go internal/social/store_cover_test.go
git mv internal/social/depo_onizleme.go internal/social/store_preview.go
git mv internal/social/depo_onizleme_test.go internal/social/store_preview_test.go
git mv internal/social/depo_profil_test.go internal/social/store_profile_test.go
git mv internal/social/depo_sikayet.go internal/social/store_report.go
git mv internal/social/depo_sikayet_test.go internal/social/store_report_test.go
git mv internal/social/depo_takip.go internal/social/store_follow.go
git mv internal/social/depo_takip_test.go internal/social/store_follow_test.go
git mv internal/social/depo_temizlik.go internal/social/store_cleanup.go
git mv internal/social/depo_temizlik_test.go internal/social/store_cleanup_test.go
git mv internal/social/depo_yardimci_test.go internal/social/store_helpers_test.go
git mv internal/social/depo_yayin.go internal/social/store_publication.go
git mv internal/social/depo_yayin_test.go internal/social/store_publication_test.go
git mv internal/social/depo_yorum.go internal/social/store_comment.go
git mv internal/social/depo_yorum_test.go internal/social/store_comment_test.go
git mv internal/social/gorunurluk.go internal/social/visibility.go
git mv internal/social/gorunurluk_test.go internal/social/visibility_test.go
git mv internal/social/hatalar.go internal/social/errors.go
git mv internal/social/imlec.go internal/social/cursor.go
git mv internal/social/imlec_test.go internal/social/cursor_test.go
git mv internal/social/kurallar.go internal/social/profile_rules.go
git mv internal/social/kurallar_test.go internal/social/profile_rules_test.go
git mv internal/social/model_yayin.go internal/social/model_publication.go
git mv internal/social/model_yorum.go internal/social/model_comment.go
git mv internal/social/sahte_depo_test.go internal/social/fake_store_test.go
git mv internal/social/servis.go internal/social/service.go
git mv internal/social/servis_akis.go internal/social/service_feed.go
git mv internal/social/servis_akis_test.go internal/social/service_feed_test.go
git mv internal/social/servis_cover.go internal/social/service_cover.go
git mv internal/social/servis_cover_test.go internal/social/service_cover_test.go
git mv internal/social/servis_onizleme.go internal/social/service_preview.go
git mv internal/social/servis_onizleme_test.go internal/social/service_preview_test.go
git mv internal/social/servis_profil_test.go internal/social/service_profile_test.go
git mv internal/social/servis_sikayet.go internal/social/service_report.go
git mv internal/social/servis_sikayet_test.go internal/social/service_report_test.go
git mv internal/social/servis_takip.go internal/social/service_follow.go
git mv internal/social/servis_takip_test.go internal/social/service_follow_test.go
git mv internal/social/servis_yayin.go internal/social/service_publication.go
git mv internal/social/servis_yayin_test.go internal/social/service_publication_test.go
git mv internal/social/servis_yorum.go internal/social/service_comment.go
git mv internal/social/servis_yorum_test.go internal/social/service_comment_test.go
git mv internal/social/veritabani.go internal/social/database.go
git mv internal/social/veritabani_test.go internal/social/database_test.go
git mv internal/social/yayin_kurallari.go internal/social/publication_rules.go
git mv internal/social/yayin_kurallari_test.go internal/social/publication_rules_test.go
perl -pi -e 's#"internal/sosyal/#"internal/social/#g' tools/wirecheck/main.go
grep -c '"internal/social/' tools/wirecheck/main.go
files=$(gofmt -l .); [ -z "$files" ] || gofmt -w $files
go build ./... && go vet ./...
go test -count=1 ./tools/wirecheck
grep -rln '"github.com/makeasinger/api/internal/sosyal"' --include='*.go' .
```

Expected:
- `grep -c` prints `1`. That is the `sosyal-migration-001` asset; its logical name stays.
- The build and vet print nothing, and the guard prints `ok`.
- The last `grep` prints nothing.
- The `migrations/` directory moved with the package. `001_sosyal.sql` keeps its name (social risk 6).

- [ ] **Step 3: Apply part 1 (interface methods and their fakes)**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 13-first
```

Expected: for each interface method, a `rename` line followed by a `textual` line for its concrete implementer; `already` for each fake that gopls renamed with the interface; then `<k> renamed, <n> rows in table`. gopls refuses every rename while any package has type errors, and renaming an interface method breaks the build until the implementer follows. So the script renames the implementer textually right after each interface method (the `manual: textual` rows; it checks that exactly one declaration matches) and runs `go build ./...` before the next row. If it stops, fix the reported cause and re-run it: finished rows print `already`.

- [ ] **Step 4: Check that no code reference to the old cleanup method names is left**

```bash
grep -rnwE 'YayinlariListele|KullaniciIzleriniSil|KullaniciVerisiniSil' --include='*.go' .
files=$(gofmt -l .); [ -z "$files" ] || gofmt -w $files
go build ./... && go vet ./...
```

Expected:
- The `grep` prints only comment lines and string literals. For example, a `t.Fatalf("<OldName>: %v", err)` message stays as it is under ruling A7. It prints no code reference: no declaration on `*PostgresDepo`, and no call.
- The build and vet print nothing.


- [ ] **Step 5: Apply the declaration table (part 2)**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 13 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 6: Do the `manual:` rows**

Follow the rules under the table. Then run:

```bash
go build ./... && go vet ./...
```

- [ ] **Step 7: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- `database.go` (was `veritabani.go`) has the `// Package sosyal ...` doc comment. Write it as `// Package social ...`.

Files (11): `internal/social/database.go`, `internal/social/errors.go`, `internal/social/store.go`, `internal/social/store_cleanup.go`, `internal/social/store_comment.go`, `internal/social/store_cover.go`, `internal/social/store_feed.go`, `internal/social/store_follow.go`, `internal/social/store_preview.go`, `internal/social/store_publication.go`, `internal/social/store_report.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/social/database.go internal/social/errors.go internal/social/store.go internal/social/store_cleanup.go internal/social/store_comment.go internal/social/store_cover.go internal/social/store_feed.go internal/social/store_follow.go internal/social/store_preview.go internal/social/store_publication.go internal/social/store_report.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 8: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 13-first --dry-run | grep -v '^already'
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 13 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 9: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 10: Commit**

```bash
git status --short
git add -A internal/sosyal internal/social
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(social): internal/sosyal -> internal/social, depo katmani

Paket ve dizin tasindi, dosyalar Ingilizce adlar aldi; depo katmani
(Depo -> Store, PostgresDepo -> PostgresStore) ve hata degiskenleri
Ingilizceye cevrildi; SosyalTemizleyici -> SocialCleaner. SQL, sema
ve migration dosya adi ayni; kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 14: social: service, models, rules, cursors, visibility (+ handler `SocialService` methods)

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

_No file in this task changes its name._

The files were moved and renamed in Task 13.

`handler.SosyalServisi` (23 methods) is implemented by `social.Servis`, and `social` never imports `handler`. So part 1 renames each interface method with gopls (the handler fake `sahteSosyal` follows), and `apply_renames.py` renames the matching `Servis` method textually right after it (the `manual: textual` rows). The store methods with the same Turkish names were renamed in Task 13, so exactly one declaration matches each name.

**Declarations, part 1: run first** (69 rows)

<!-- renames:14-first -->
| old | new | kind | file |
|---|---|---|---|
| SosyalServisi.ProfilBen | SocialService.MyProfile | interface method | internal/handler/sosyal.go |
| SosyalServisi.ProfilKaydet | SocialService.SaveProfile | interface method | internal/handler/sosyal.go |
| SosyalServisi.AdUygunMu | SocialService.CheckUsername | interface method | internal/handler/sosyal.go |
| SosyalServisi.ProfilGoster | SocialService.Profile | interface method | internal/handler/sosyal.go |
| SosyalServisi.YayinOlustur | SocialService.CreatePublication | interface method | internal/handler/sosyal.go |
| SosyalServisi.YayinGoster | SocialService.Publication | interface method | internal/handler/sosyal.go |
| SosyalServisi.SlugGoster | SocialService.PublicationBySlug | interface method | internal/handler/sosyal.go |
| SosyalServisi.YayinGuncelle | SocialService.UpdatePublication | interface method | internal/handler/sosyal.go |
| SosyalServisi.YayinSil | SocialService.DeletePublication | interface method | internal/handler/sosyal.go |
| SosyalServisi.TekrarDene | SocialService.Retry | interface method | internal/handler/sosyal.go |
| SosyalServisi.Dosyalar | SocialService.Files | interface method | internal/handler/sosyal.go |
| SosyalServisi.Akis | SocialService.Feed | interface method | internal/handler/sosyal.go |
| SosyalServisi.ProfilYayinlari | SocialService.ProfilePublications | interface method | internal/handler/sosyal.go |
| SosyalServisi.Begeni | SocialService.SetLike | interface method | internal/handler/sosyal.go |
| SosyalServisi.Takip | SocialService.SetFollow | interface method | internal/handler/sosyal.go |
| SosyalServisi.Oneriler | SocialService.Suggestions | interface method | internal/handler/sosyal.go |
| SosyalServisi.Yorumlar | SocialService.Comments | interface method | internal/handler/sosyal.go |
| SosyalServisi.YorumYaz | SocialService.PostComment | interface method | internal/handler/sosyal.go |
| SosyalServisi.YorumSil | SocialService.DeleteComment | interface method | internal/handler/sosyal.go |
| SosyalServisi.Sikayet | SocialService.Report | interface method | internal/handler/sosyal.go |
| SosyalServisi.Coverlar | SocialService.Covers | interface method | internal/handler/sosyal.go |
| SosyalServisi.CoverBaslat | SocialService.StartCover | interface method | internal/handler/sosyal.go |
| SosyalServisi.Onizleme | SocialService.Preview | interface method | internal/handler/sosyal.go |
| sahteSosyal.ProfilBen | fakeSocialService.MyProfile | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.ProfilKaydet | fakeSocialService.SaveProfile | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.AdUygunMu | fakeSocialService.CheckUsername | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.ProfilGoster | fakeSocialService.Profile | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.YayinOlustur | fakeSocialService.CreatePublication | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.YayinGoster | fakeSocialService.Publication | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.SlugGoster | fakeSocialService.PublicationBySlug | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.YayinGuncelle | fakeSocialService.UpdatePublication | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.YayinSil | fakeSocialService.DeletePublication | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.TekrarDene | fakeSocialService.Retry | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.Dosyalar | fakeSocialService.Files | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.Akis | fakeSocialService.Feed | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.ProfilYayinlari | fakeSocialService.ProfilePublications | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.Begeni | fakeSocialService.SetLike | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.Takip | fakeSocialService.SetFollow | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.Oneriler | fakeSocialService.Suggestions | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.Yorumlar | fakeSocialService.Comments | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.YorumYaz | fakeSocialService.PostComment | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.YorumSil | fakeSocialService.DeleteComment | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.Sikayet | fakeSocialService.Report | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.Coverlar | fakeSocialService.Covers | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.CoverBaslat | fakeSocialService.StartCover | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| sahteSosyal.Onizleme | fakeSocialService.Preview | method (test fake of SocialService) | internal/handler/sosyal_test.go |
| Servis.ProfilBen | Service.MyProfile | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service.go |
| Servis.ProfilKaydet | Service.SaveProfile | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service.go |
| Servis.AdUygunMu | Service.CheckUsername | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service.go |
| Servis.ProfilGoster | Service.Profile | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service.go |
| Servis.Akis | Service.Feed | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_feed.go |
| Servis.ProfilYayinlari | Service.ProfilePublications | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_feed.go |
| Servis.Begeni | Service.SetLike | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_feed.go |
| Servis.Coverlar | Service.Covers | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_cover.go |
| Servis.CoverBaslat | Service.StartCover | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_cover.go |
| Servis.Onizleme | Service.Preview | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_preview.go |
| Servis.Sikayet | Service.Report | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_report.go |
| Servis.Takip | Service.SetFollow | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_follow.go |
| Servis.Oneriler | Service.Suggestions | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_follow.go |
| Servis.YayinOlustur | Service.CreatePublication | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_publication.go |
| Servis.YayinGoster | Service.Publication | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_publication.go |
| Servis.SlugGoster | Service.PublicationBySlug | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_publication.go |
| Servis.YayinGuncelle | Service.UpdatePublication | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_publication.go |
| Servis.YayinSil | Service.DeletePublication | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_publication.go |
| Servis.TekrarDene | Service.Retry | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_publication.go |
| Servis.Dosyalar | Service.Files | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_publication.go |
| Servis.Yorumlar | Service.Comments | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_comment.go |
| Servis.YorumYaz | Service.PostComment | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_comment.go |
| Servis.YorumSil | Service.DeleteComment | manual: textual, method (renamed by apply_renames.py right after the interface method) | internal/social/service_comment.go |

**Declarations, part 2** (324 rows)

<!-- renames:14 -->
| old | new | kind | file |
|---|---|---|---|
| GorunurlukGirdisi | VisibilityInput | type | internal/social/visibility.go |
| Sayfa | Page | type (generic Page[T]) | internal/social/cursor.go |
| ZamanImleci | TimeCursor | type | internal/social/cursor.go |
| PuanImleci | ScoreCursor | type | internal/social/cursor.go |
| YorumImleci | CommentCursor | type | internal/social/cursor.go |
| Ton | MusicalKey | type | internal/social/model_publication.go |
| YapiOgesi | StructureItem | type | internal/social/model_publication.go |
| SozBolumu | LyricsSection | type | internal/social/model_publication.go |
| Sozler | Lyrics | type | internal/social/model_publication.go |
| KaynakOzeti | SourceSummary | type | internal/social/model_publication.go |
| YayinOzeti | PublicationSummary | type | internal/social/model_publication.go |
| YayinDetayi | PublicationDetail | type | internal/social/model_publication.go |
| StemGirdisi | StemInput | type | internal/social/model_publication.go |
| YayinIstegi | PublicationRequest | type | internal/social/model_publication.go |
| YayinYaniti | PublicationResponse | type | internal/social/model_publication.go |
| YayinYamasi | PublicationPatch | type | internal/social/model_publication.go |
| DosyaAdresi | FileLink | type | internal/social/model_publication.go |
| YayinDosyalari | PublicationFiles | type | internal/social/model_publication.go |
| HazirlikStemi | PreparationStem | type | internal/social/model_publication.go |
| Hazirlik | Preparation | type | internal/social/model_publication.go |
| StemKaydi | StemRecord | type | internal/social/model_publication.go |
| YeniYayin | PublicationInsert | type | internal/social/model_publication.go |
| YayinKaydi | PublicationRecord | type | internal/social/model_publication.go |
| HazirlikKaydi | PreparationRecord | type | internal/social/model_publication.go |
| HazirlikSonucu | PreparationResult | type | internal/social/model_publication.go |
| Yorum | Comment | type | internal/social/model_comment.go |
| YorumGirdisi | CommentInput | type | internal/social/model_comment.go |
| YeniYorum | CommentInsert | type | internal/social/model_comment.go |
| YorumKaydi | CommentRecord | type | internal/social/model_comment.go |
| ProfilOzeti | ProfileSummary | type | internal/social/model.go |
| Istatistik | Stats | type | internal/social/model.go |
| Profil | Profile | type | internal/social/model.go |
| ProfilGirdisi | ProfileInput | type | internal/social/model.go |
| AdUygunlugu | UsernameAvailability | type | internal/social/model.go |
| ProfilKaydi | ProfileRecord | type | internal/social/model.go |
| ProfilSayilari | ProfileCounts | type | internal/social/model.go |
| BegeniDurumu | LikeStatus | type | internal/social/service_feed.go |
| CoverStemi | CoverStem | type | internal/social/service_cover.go |
| CoverBaslangici | CoverStart | type | internal/social/service_cover.go |
| Onizleme | Preview | type | internal/social/service_preview.go |
| OnizlemeKaydi | PreviewRecord | type | internal/social/service_preview.go |
| SikayetGirdisi | ReportInput | type | internal/social/service_report.go |
| YeniSikayet | ReportInsert | type | internal/social/service_report.go |
| TakipDurumu | FollowStatus | type | internal/social/service_follow.go |
| GorevYuku | TaskPayload | type | internal/social/service_publication.go |
| Kuyruk | Enqueuer | type (interface) | internal/social/service.go |
| Servis | Service | type | internal/social/service.go |
| Kuyruk.Enqueue | Enqueuer.Enqueue | interface method (unchanged; must match *asynq.Client) | internal/social/service.go |
| YayinKaydi.gorunurlukGirdisi | PublicationRecord.visibilityInput | method (receiver k becomes r) | internal/social/model_publication.go |
| ProfilKaydi.ozet | ProfileRecord.summary | method (receiver k becomes r) | internal/social/service.go |
| Servis.profilKur | Service.buildProfile | method | internal/social/service.go |
| Servis.seninIcin | Service.forYou | method | internal/social/service_feed.go |
| Servis.KopyalayiciAyarla | Service.WithCopier | method | internal/social/service_cover.go |
| Servis.onizlemeAdresi | Service.previewURL | method | internal/social/service_preview.go |
| Servis.hazirlamayaKoy | Service.enqueuePrepare | method | internal/social/service_publication.go |
| Servis.anahtarCoz | Service.resolveKey | method | internal/social/service_publication.go |
| Servis.kaynakCoz | Service.resolveSource | method | internal/social/service_publication.go |
| Servis.gorunurYayin | Service.visiblePublication | method | internal/social/service_publication.go |
| Servis.sahibinYayini | Service.ownedPublication | method | internal/social/service_publication.go |
| uzanti | extension | func | internal/social/keys.go |
| MasterHedefi | MasterDestKey | func | internal/social/keys.go |
| StemHedefi | StemDestKey | func | internal/social/keys.go |
| OnizlemeHedefi | PreviewDestKey | func | internal/social/keys.go |
| gorebilir | canSee | func | internal/social/visibility.go |
| DetayGorebilir | CanSeeDetail | func | internal/social/visibility.go |
| ListedeGorebilir | CanSeeInList | func | internal/social/visibility.go |
| gorunurlukKosulu | visibilityCondition | func | internal/social/visibility.go |
| SayfaBoyutu | PageSize | func | internal/social/cursor.go |
| imlecKodla | encodeCursor | func | internal/social/cursor.go |
| imlecCoz | decodeCursor | func | internal/social/cursor.go |
| zamanImleciCoz | decodeTimeCursor | func | internal/social/cursor.go |
| puanImleciCoz | decodeScoreCursor | func | internal/social/cursor.go |
| yorumImleciCoz | decodeCommentCursor | func | internal/social/cursor.go |
| yorumImleci | commentCursorAfter | func | internal/social/cursor.go |
| KullaniciAdiGecerli | ValidUsername | func | internal/social/profile_rules.go |
| AvatarRengi | AvatarColor | func | internal/social/profile_rules.go |
| adAdaylari | usernameCandidates | func | internal/social/profile_rules.go |
| profilGirdisiniDuzenle | normalizeProfileInput | func | internal/social/profile_rules.go |
| kimlikCoz | parseID | func | internal/social/profile_rules.go |
| zamanSayfasiKur | buildTimePage | func | internal/social/service_feed.go |
| CoverStemHedefi | CoverStemDestKey | func | internal/social/service_cover.go |
| gorevYuku | encodeTaskPayload | func | internal/social/service_publication.go |
| yorumSayfasiKur | buildCommentPage | func | internal/social/service_comment.go |
| NewServis | NewService | func | internal/social/service.go |
| kullaniciCoz | parseUserID | func | internal/social/service.go |
| SlugGecerli | ValidSlug | func | internal/social/slug.go |
| slugUret | generateSlug | func | internal/social/slug.go |
| GorunurlukGecerli | ValidVisibility | func | internal/social/publication_rules.go |
| runeAraliginda | runeCountInRange | func | internal/social/publication_rules.go |
| nulIceriyor | containsNUL | func | internal/social/publication_rules.go |
| yayinIsteginiDuzenle | normalizePublicationRequest | func | internal/social/publication_rules.go |
| yamayiDuzenle | normalizePatch | func | internal/social/publication_rules.go |
| uzantiDeseni | extensionPattern | var | internal/social/keys.go |
| GorunurlukHerkes | VisibilityEveryone | const (value "herkes") | internal/social/visibility.go |
| GorunurlukTakipciler | VisibilityFollowers | const (value "takipciler") | internal/social/visibility.go |
| GorunurlukLink | VisibilityLink | const (value "link") | internal/social/visibility.go |
| DurumHazirlaniyor | StatusPreparing | const (value "hazirlaniyor") | internal/social/visibility.go |
| DurumYayinda | StatusPublished | const (value "yayinda") | internal/social/visibility.go |
| DurumHata | StatusFailed | const (value "hata") | internal/social/visibility.go |
| varsayilanSayfa | defaultPageSize | const | internal/social/cursor.go |
| enBuyukSayfa | maxPageSize | const | internal/social/cursor.go |
| kullaniciAdiDeseni | usernamePattern | var | internal/social/profile_rules.go |
| kullaniciAdiEnUzun | maxUsernameLen | const | internal/social/profile_rules.go |
| gorunenAdEnUzun | maxDisplayNameLen | const | internal/social/profile_rules.go |
| bioEnUzun | maxBioLen | const | internal/social/profile_rules.go |
| avatarPaletBoyu | avatarPaletteSize | const | internal/social/profile_rules.go |
| SekmeSeninIcin | TabForYou | const (value "senin-icin") | internal/social/service_feed.go |
| SekmeTakip | TabFollowing | const (value "takip") | internal/social/service_feed.go |
| SekmeYeni | TabNew | const (value "yeni") | internal/social/service_feed.go |
| referansPayi | referenceSkew | const | internal/social/service_feed.go |
| onizlemeOmru | previewURLTTL | const | internal/social/service_preview.go |
| HedefYayin | TargetPublication | const (value "yayin") | internal/social/service_report.go |
| HedefYorum | TargetComment | const (value "yorum") | internal/social/service_report.go |
| sikayetAciklamaEnUzun | maxReportDescriptionLen | const | internal/social/service_report.go |
| sikayetNedenleri | reportReasons | var | internal/social/service_report.go |
| oneriSayisi | suggestionLimit | const | internal/social/service_follow.go |
| TaskTypeHazirla | TaskTypePrepare | const (value "sosyal:hazirla") | internal/social/service_publication.go |
| TaskTypeDosyaSil | TaskTypeDeleteFiles | const (value "sosyal:dosya-sil") | internal/social/service_publication.go |
| KuyrukSosyal | QueueSocial | const (value "sosyal") | internal/social/service_publication.go |
| hazirlaDenemesi | prepareMaxRetry | const | internal/social/service_publication.go |
| hazirlaSaklama | prepareRetention | const | internal/social/service_publication.go |
| dosyaSilmeGecikmesi | deleteFilesDelay | const | internal/social/service_publication.go |
| dosyaSilDenemesi | deleteFilesMaxRetry | const | internal/social/service_publication.go |
| slugDenemesi | slugAttempts | const | internal/social/service_publication.go |
| yorumEnUzun | maxCommentLen | const | internal/social/service_comment.go |
| slugAlfabesi | slugAlphabet | const | internal/social/slug.go |
| slugBoyu | slugLen | const | internal/social/slug.go |
| slugDeseni | slugPattern | var | internal/social/slug.go |
| errSlugCakisti | errSlugConflict | var | internal/social/slug.go |
| stemIDDeseni | stemIDPattern | var | internal/social/publication_rules.go |
| baslikEnUzun | maxTitleLen | const | internal/social/publication_rules.go |
| aciklamaEnUzun | maxDescriptionLen | const | internal/social/publication_rules.go |
| turEnUzun | maxGenreLen | const | internal/social/publication_rules.go |
| tonikEnUzun | maxTonicLen | const | internal/social/publication_rules.go |
| enstrumanEnUzun | maxInstrumentLen | const | internal/social/publication_rules.go |
| bpmEnAz | minBPM | const | internal/social/publication_rules.go |
| bpmEnCok | maxBPM | const | internal/social/publication_rules.go |
| stemEnCok | maxStems | const | internal/social/publication_rules.go |
| yapiEnCok | maxStructureItems | const | internal/social/publication_rules.go |
| bolumEnCok | maxLyricsSections | const | internal/social/publication_rules.go |
| satirEnCok | maxSectionLines | const | internal/social/publication_rules.go |
| toplamSatirEnCok | maxTotalLines | const | internal/social/publication_rules.go |
| satirEnUzun | maxLineLen | const | internal/social/publication_rules.go |
| ogeAlaniEnUzun | maxItemFieldLen | const | internal/social/publication_rules.go |
| GorunurlukGirdisi.SahipID | VisibilityInput.OwnerID | field | internal/social/visibility.go |
| GorunurlukGirdisi.IzleyiciID | VisibilityInput.ViewerID | field | internal/social/visibility.go |
| GorunurlukGirdisi.Gorunurluk | VisibilityInput.Visibility | field | internal/social/visibility.go |
| GorunurlukGirdisi.Durum | VisibilityInput.Status | field | internal/social/visibility.go |
| GorunurlukGirdisi.Silindi | VisibilityInput.Deleted | field | internal/social/visibility.go |
| GorunurlukGirdisi.TakipEdiyor | VisibilityInput.ViewerFollows | field | internal/social/visibility.go |
| Sayfa.Ogeler | Page.Items | field json:"ogeler" | internal/social/cursor.go |
| Sayfa.SonrakiImlec | Page.NextCursor | field json:"sonrakiImlec" | internal/social/cursor.go |
| ZamanImleci.T | TimeCursor.T | field json:"t" (name already fine) | internal/social/cursor.go |
| PuanImleci.P | ScoreCursor.Score | field json:"p" | internal/social/cursor.go |
| PuanImleci.N | ScoreCursor.RefMs | field json:"n" | internal/social/cursor.go |
| YorumImleci.G | CommentCursor.Group | field json:"g" | internal/social/cursor.go |
| YorumImleci.Z | CommentCursor.TimeMs | field json:"z,omitempty" | internal/social/cursor.go |
| YorumImleci.T | CommentCursor.T | field json:"t" (name already fine) | internal/social/cursor.go |
| Ton.Tonik | MusicalKey.Tonic | field json:"tonik" | internal/social/model_publication.go |
| Ton.Mod | MusicalKey.Mode | field json:"mod" | internal/social/model_publication.go |
| YapiOgesi.Tur | StructureItem.Type | field json:"tur" | internal/social/model_publication.go |
| YapiOgesi.Olcu | StructureItem.Bars | field json:"olcu" | internal/social/model_publication.go |
| SozBolumu.BolumID | LyricsSection.SectionID | field json:"bolumId" | internal/social/model_publication.go |
| SozBolumu.Tur | LyricsSection.Type | field json:"tur" | internal/social/model_publication.go |
| SozBolumu.Satirlar | LyricsSection.Lines | field json:"satirlar" | internal/social/model_publication.go |
| Sozler.Bolumler | Lyrics.Sections | field json:"bolumler" | internal/social/model_publication.go |
| KaynakOzeti.YayinID | SourceSummary.PublicationID | field json:"yayinId" | internal/social/model_publication.go |
| KaynakOzeti.Baslik | SourceSummary.Title | field json:"baslik" | internal/social/model_publication.go |
| KaynakOzeti.SahipKullaniciAdi | SourceSummary.OwnerUsername | field json:"sahipKullaniciAdi" | internal/social/model_publication.go |
| KaynakOzeti.Silindi | SourceSummary.Deleted | field json:"silindi" | internal/social/model_publication.go |
| YayinOzeti.Baslik | PublicationSummary.Title | field json:"baslik" | internal/social/model_publication.go |
| YayinOzeti.Sahip | PublicationSummary.Owner | field json:"sahip" | internal/social/model_publication.go |
| YayinOzeti.Tur | PublicationSummary.Genre | field json:"tur" | internal/social/model_publication.go |
| YayinOzeti.Bpm | PublicationSummary.BPM | field json:"bpm" | internal/social/model_publication.go |
| YayinOzeti.Ton | PublicationSummary.MusicalKey | field json:"ton" | internal/social/model_publication.go |
| YayinOzeti.SureSn | PublicationSummary.DurationSec | field json:"sureSn" | internal/social/model_publication.go |
| YayinOzeti.DalgaFormu | PublicationSummary.Waveform | field json:"dalgaFormu" | internal/social/model_publication.go |
| YayinOzeti.BegeniSayisi | PublicationSummary.LikeCount | field json:"begeniSayisi" | internal/social/model_publication.go |
| YayinOzeti.YorumSayisi | PublicationSummary.CommentCount | field json:"yorumSayisi" | internal/social/model_publication.go |
| YayinOzeti.CoverSayisi | PublicationSummary.CoverCount | field json:"coverSayisi" | internal/social/model_publication.go |
| YayinOzeti.BegendimMi | PublicationSummary.LikedByMe | field json:"begendimMi" | internal/social/model_publication.go |
| YayinOzeti.AltyapiAcik | PublicationSummary.BackingEnabled | field json:"altyapiAcik" | internal/social/model_publication.go |
| YayinOzeti.Kaynak | PublicationSummary.Source | field json:"kaynak" | internal/social/model_publication.go |
| YayinOzeti.Olusturma | PublicationSummary.CreatedAt | field json:"olusturma" | internal/social/model_publication.go |
| YayinDetayi.<YayinOzeti> | PublicationDetail.<PublicationSummary> | manual: embedded field (untagged; flattened in JSON) .<PublicationSummary> | internal/social/model_publication.go |
| YayinDetayi.Aciklama | PublicationDetail.Description | field json:"aciklama" | internal/social/model_publication.go |
| YayinDetayi.YorumAcik | PublicationDetail.CommentsEnabled | field json:"yorumAcik" | internal/social/model_publication.go |
| YayinDetayi.Sozler | PublicationDetail.Lyrics | field json:"sozler" | internal/social/model_publication.go |
| YayinDetayi.Yapi | PublicationDetail.Structure | field json:"yapi" | internal/social/model_publication.go |
| YayinDetayi.Durum | PublicationDetail.Status | field json:"durum" | internal/social/model_publication.go |
| YayinDetayi.BenimMi | PublicationDetail.IsMine | field json:"benimMi" | internal/social/model_publication.go |
| YayinDetayi.Gorunurluk | PublicationDetail.Visibility | field json:"gorunurluk" | internal/social/model_publication.go |
| StemGirdisi.Enstruman | StemInput.Instrument | field json:"enstruman" | internal/social/model_publication.go |
| StemGirdisi.Sure | StemInput.Duration | field json:"sure" | internal/social/model_publication.go |
| YayinIstegi.Baslik | PublicationRequest.Title | field json:"baslik" | internal/social/model_publication.go |
| YayinIstegi.Aciklama | PublicationRequest.Description | field json:"aciklama" | internal/social/model_publication.go |
| YayinIstegi.Gorunurluk | PublicationRequest.Visibility | field json:"gorunurluk" | internal/social/model_publication.go |
| YayinIstegi.AltyapiAcik | PublicationRequest.BackingEnabled | field json:"altyapiAcik" | internal/social/model_publication.go |
| YayinIstegi.YorumAcik | PublicationRequest.CommentsEnabled | field json:"yorumAcik" | internal/social/model_publication.go |
| YayinIstegi.MasterURL | PublicationRequest.MasterURL | field json:"masterUrl" (name already fine) | internal/social/model_publication.go |
| YayinIstegi.Stemler | PublicationRequest.Stems | field json:"stemler" | internal/social/model_publication.go |
| YayinIstegi.Sozler | PublicationRequest.Lyrics | field json:"sozler" | internal/social/model_publication.go |
| YayinIstegi.Yapi | PublicationRequest.Structure | field json:"yapi" | internal/social/model_publication.go |
| YayinIstegi.Tur | PublicationRequest.Genre | field json:"tur" | internal/social/model_publication.go |
| YayinIstegi.Bpm | PublicationRequest.BPM | field json:"bpm" | internal/social/model_publication.go |
| YayinIstegi.Ton | PublicationRequest.MusicalKey | field json:"ton" | internal/social/model_publication.go |
| YayinIstegi.SureSn | PublicationRequest.DurationSec | field json:"sureSn" | internal/social/model_publication.go |
| YayinIstegi.KaynakYayinID | PublicationRequest.SourcePublicationID | field json:"kaynakYayinId" | internal/social/model_publication.go |
| YayinYaniti.Durum | PublicationResponse.Status | field json:"durum" | internal/social/model_publication.go |
| YayinYamasi.Aciklama | PublicationPatch.Description | field json:"aciklama" | internal/social/model_publication.go |
| YayinYamasi.AltyapiAcik | PublicationPatch.BackingEnabled | field json:"altyapiAcik" | internal/social/model_publication.go |
| YayinYamasi.Gorunurluk | PublicationPatch.Visibility | field json:"gorunurluk" | internal/social/model_publication.go |
| YayinYamasi.YorumAcik | PublicationPatch.CommentsEnabled | field json:"yorumAcik" | internal/social/model_publication.go |
| HazirlikStemi.Enstruman | PreparationStem.Instrument | field json:"enstruman" (stored in jsonb yayin.hazirlik) | internal/social/model_publication.go |
| HazirlikStemi.Sure | PreparationStem.Duration | field json:"sure" (jsonb) | internal/social/model_publication.go |
| StemKaydi.Enstruman | StemRecord.Instrument | field json:"enstruman" (jsonb yayin.stem_listesi) | internal/social/model_publication.go |
| StemKaydi.Sure | StemRecord.Duration | field json:"sure" (jsonb) | internal/social/model_publication.go |
| StemKaydi.Anahtar | StemRecord.Key | field json:"anahtar" (jsonb) | internal/social/model_publication.go |
| YeniYayin.SahipID | PublicationInsert.OwnerID | field | internal/social/model_publication.go |
| YeniYayin.Baslik | PublicationInsert.Title | field | internal/social/model_publication.go |
| YeniYayin.Aciklama | PublicationInsert.Description | field | internal/social/model_publication.go |
| YeniYayin.Tur | PublicationInsert.Genre | field | internal/social/model_publication.go |
| YeniYayin.Bpm | PublicationInsert.BPM | field | internal/social/model_publication.go |
| YeniYayin.Ton | PublicationInsert.MusicalKey | field | internal/social/model_publication.go |
| YeniYayin.SureSn | PublicationInsert.DurationSec | field | internal/social/model_publication.go |
| YeniYayin.Gorunurluk | PublicationInsert.Visibility | field | internal/social/model_publication.go |
| YeniYayin.AltyapiAcik | PublicationInsert.BackingEnabled | field | internal/social/model_publication.go |
| YeniYayin.YorumAcik | PublicationInsert.CommentsEnabled | field | internal/social/model_publication.go |
| YeniYayin.Sozler | PublicationInsert.Lyrics | field | internal/social/model_publication.go |
| YeniYayin.Yapi | PublicationInsert.Structure | field | internal/social/model_publication.go |
| YeniYayin.Hazirlik | PublicationInsert.Preparation | field | internal/social/model_publication.go |
| YeniYayin.KaynakYayinID | PublicationInsert.SourcePublicationID | field | internal/social/model_publication.go |
| YeniYayin.KaynakBaslik | PublicationInsert.SourceTitle | field | internal/social/model_publication.go |
| YeniYayin.KaynakSahipAdi | PublicationInsert.SourceOwnerUsername | field | internal/social/model_publication.go |
| YayinKaydi.<YayinDetayi> | PublicationRecord.<PublicationDetail> | manual: embedded field .<PublicationDetail> | internal/social/model_publication.go |
| YayinKaydi.SahipID | PublicationRecord.OwnerID | field | internal/social/model_publication.go |
| YayinKaydi.KaynakYayinID | PublicationRecord.SourcePublicationID | field | internal/social/model_publication.go |
| YayinKaydi.StemVar | PublicationRecord.HasStems | field | internal/social/model_publication.go |
| YayinKaydi.MasterAnahtari | PublicationRecord.MasterKey | field | internal/social/model_publication.go |
| YayinKaydi.Silindi | PublicationRecord.Deleted | field | internal/social/model_publication.go |
| YayinKaydi.TakipEdiyor | PublicationRecord.ViewerFollows | field | internal/social/model_publication.go |
| HazirlikKaydi.SahipID | PreparationRecord.OwnerID | field | internal/social/model_publication.go |
| HazirlikKaydi.Durum | PreparationRecord.Status | field | internal/social/model_publication.go |
| HazirlikKaydi.Silindi | PreparationRecord.Deleted | field | internal/social/model_publication.go |
| HazirlikKaydi.Hazirlik | PreparationRecord.Preparation | field | internal/social/model_publication.go |
| HazirlikSonucu.MasterAnahtari | PreparationResult.MasterKey | field | internal/social/model_publication.go |
| HazirlikSonucu.StemListesi | PreparationResult.Stems | field | internal/social/model_publication.go |
| HazirlikSonucu.DalgaFormu | PreparationResult.Waveform | field | internal/social/model_publication.go |
| HazirlikSonucu.OnizlemeAnahtari | PreparationResult.PreviewKey | field | internal/social/model_publication.go |
| Yorum.Yazar | Comment.Author | field json:"yazar" | internal/social/model_comment.go |
| Yorum.Metin | Comment.Text | field json:"metin" | internal/social/model_comment.go |
| Yorum.ZamanMs | Comment.TimeMs | field json:"zamanMs" | internal/social/model_comment.go |
| Yorum.Olusturma | Comment.CreatedAt | field json:"olusturma" | internal/social/model_comment.go |
| Yorum.SilebilirMiyim | Comment.CanDelete | field json:"silebilirMiyim" | internal/social/model_comment.go |
| YorumGirdisi.Metin | CommentInput.Text | field json:"metin" | internal/social/model_comment.go |
| YorumGirdisi.ZamanMs | CommentInput.TimeMs | field json:"zamanMs" | internal/social/model_comment.go |
| YeniYorum.YayinID | CommentInsert.PublicationID | field | internal/social/model_comment.go |
| YeniYorum.YazarID | CommentInsert.AuthorID | field | internal/social/model_comment.go |
| YeniYorum.Metin | CommentInsert.Text | field | internal/social/model_comment.go |
| YeniYorum.ZamanMs | CommentInsert.TimeMs | field | internal/social/model_comment.go |
| YorumKaydi.YayinID | CommentRecord.PublicationID | field | internal/social/model_comment.go |
| YorumKaydi.YazarID | CommentRecord.AuthorID | field | internal/social/model_comment.go |
| YorumKaydi.YayinSahibiID | CommentRecord.PublicationOwnerID | field | internal/social/model_comment.go |
| YorumKaydi.Silindi | CommentRecord.Deleted | field | internal/social/model_comment.go |
| ProfilOzeti.KullaniciAdi | ProfileSummary.Username | field json:"kullaniciAdi" | internal/social/model.go |
| ProfilOzeti.GorunenAd | ProfileSummary.DisplayName | field json:"gorunenAd" | internal/social/model.go |
| ProfilOzeti.AvatarRengi | ProfileSummary.AvatarColor | field json:"avatarRengi" | internal/social/model.go |
| Istatistik.Sarki | Stats.Songs | field json:"sarki" | internal/social/model.go |
| Istatistik.Takipci | Stats.Followers | field json:"takipci" | internal/social/model.go |
| Istatistik.Takip | Stats.Following | field json:"takip" | internal/social/model.go |
| Istatistik.Cover | Stats.Covers | field json:"cover" | internal/social/model.go |
| Profil.<ProfilOzeti> | Profile.<ProfileSummary> | manual: embedded field (flattened) .<ProfileSummary> | internal/social/model.go |
| Profil.Istatistik | Profile.Stats | field json:"istatistik" | internal/social/model.go |
| Profil.TakipEdiyorMuyum | Profile.FollowedByMe | field json:"takipEdiyorMuyum" | internal/social/model.go |
| Profil.BenMiyim | Profile.IsMe | field json:"benMiyim" | internal/social/model.go |
| ProfilGirdisi.KullaniciAdi | ProfileInput.Username | field json:"kullaniciAdi" | internal/social/model.go |
| ProfilGirdisi.GorunenAd | ProfileInput.DisplayName | field json:"gorunenAd" | internal/social/model.go |
| AdUygunlugu.Uygun | UsernameAvailability.Available | field json:"uygun" | internal/social/model.go |
| AdUygunlugu.Oneri | UsernameAvailability.Suggestion | field json:"oneri" | internal/social/model.go |
| ProfilKaydi.KullaniciID | ProfileRecord.UserID | field | internal/social/model.go |
| ProfilKaydi.KullaniciAdi | ProfileRecord.Username | field | internal/social/model.go |
| ProfilKaydi.GorunenAd | ProfileRecord.DisplayName | field | internal/social/model.go |
| ProfilKaydi.Olusturma | ProfileRecord.CreatedAt | field | internal/social/model.go |
| ProfilSayilari.Istatistik | ProfileCounts.Stats | field | internal/social/model.go |
| ProfilSayilari.TakipEdiyorMuyum | ProfileCounts.FollowedByMe | field | internal/social/model.go |
| BegeniDurumu.BegendimMi | LikeStatus.LikedByMe | field json:"begendimMi" | internal/social/service_feed.go |
| BegeniDurumu.BegeniSayisi | LikeStatus.LikeCount | field json:"begeniSayisi" | internal/social/service_feed.go |
| CoverStemi.Enstruman | CoverStem.Instrument | field json:"enstruman" | internal/social/service_cover.go |
| CoverStemi.Sure | CoverStem.Duration | field json:"sure" | internal/social/service_cover.go |
| CoverBaslangici.Kaynak | CoverStart.Source | field json:"kaynak" | internal/social/service_cover.go |
| CoverBaslangici.Tur | CoverStart.Genre | field json:"tur" | internal/social/service_cover.go |
| CoverBaslangici.Bpm | CoverStart.BPM | field json:"bpm" | internal/social/service_cover.go |
| CoverBaslangici.Ton | CoverStart.MusicalKey | field json:"ton" | internal/social/service_cover.go |
| CoverBaslangici.SureSn | CoverStart.DurationSec | field json:"sureSn" | internal/social/service_cover.go |
| CoverBaslangici.Yapi | CoverStart.Structure | field json:"yapi" | internal/social/service_cover.go |
| CoverBaslangici.Sozler | CoverStart.Lyrics | field json:"sozler" | internal/social/service_cover.go |
| CoverBaslangici.Stemler | CoverStart.Stems | field json:"stemler" | internal/social/service_cover.go |
| Onizleme.Baslik | Preview.Title | field json:"baslik" | internal/social/service_preview.go |
| Onizleme.Sahip | Preview.Owner | field json:"sahip" | internal/social/service_preview.go |
| Onizleme.SureSn | Preview.DurationSec | field json:"sureSn" | internal/social/service_preview.go |
| Onizleme.DalgaFormu | Preview.Waveform | field json:"dalgaFormu" | internal/social/service_preview.go |
| Onizleme.OnizlemeURL | Preview.PreviewURL | field json:"onizlemeUrl" | internal/social/service_preview.go |
| Onizleme.Tur | Preview.Genre | field json:"tur" | internal/social/service_preview.go |
| OnizlemeKaydi.<Onizleme> | PreviewRecord.<Preview> | manual: embedded field .<Preview> | internal/social/service_preview.go |
| OnizlemeKaydi.SahipID | PreviewRecord.OwnerID | field (untagged; see risks) | internal/social/service_preview.go |
| OnizlemeKaydi.OnizlemeAnahtari | PreviewRecord.PreviewKey | field (untagged; see risks) | internal/social/service_preview.go |
| SikayetGirdisi.HedefTuru | ReportInput.TargetType | field json:"hedefTuru" | internal/social/service_report.go |
| SikayetGirdisi.HedefID | ReportInput.TargetID | field json:"hedefId" | internal/social/service_report.go |
| SikayetGirdisi.Neden | ReportInput.Reason | field json:"neden" | internal/social/service_report.go |
| SikayetGirdisi.Aciklama | ReportInput.Description | field json:"aciklama" | internal/social/service_report.go |
| YeniSikayet.BildirenID | ReportInsert.ReporterID | field | internal/social/service_report.go |
| YeniSikayet.HedefTuru | ReportInsert.TargetType | field | internal/social/service_report.go |
| YeniSikayet.HedefID | ReportInsert.TargetID | field | internal/social/service_report.go |
| YeniSikayet.YayinID | ReportInsert.PublicationID | field | internal/social/service_report.go |
| YeniSikayet.Neden | ReportInsert.Reason | field | internal/social/service_report.go |
| YeniSikayet.Aciklama | ReportInsert.Description | field | internal/social/service_report.go |
| TakipDurumu.Takipci | FollowStatus.Followers | field json:"takipci" | internal/social/service_follow.go |
| TakipDurumu.TakipEdiyorMuyum | FollowStatus.FollowedByMe | field json:"takipEdiyorMuyum" | internal/social/service_follow.go |
| GorevYuku.YayinID | TaskPayload.PublicationID | field json:"yayinId" (asynq payload) | internal/social/service_publication.go |
| Servis.depo | Service.store | field | internal/social/service.go |
| Servis.kuyruk | Service.queue | field | internal/social/service.go |
| Servis.depolama | Service.storage | field | internal/social/service.go |
| Servis.simdi | Service.now | field | internal/social/service.go |
| Servis.kopyalayici | Service.copier | field | internal/social/service.go |

Rows whose kind starts with `manual:` are skipped by `apply_renames.py`. Do each by hand:
- An **embedded field** row needs no action. Renaming the embedded type renames the field and every selector on it. Check the result in the diff.
- A **`var _ X = (*Y)(nil)`** row needs no action. The assertion follows the renamed types. Check it.
- An **anonymous struct field**, **local** or **param** row: run `~/go/bin/gopls rename -w <file>:<line>:<col> <new>` on the declaring identifier.
- A **package** row is done by the package-move step.
- A **"methods unchanged; receiver becomes ..."** row needs no action. The methods keep their `op.*` names and follow the type rename.

**Wire strings to keep (must stay byte-identical)**

_From the sosyal glossary, §4.1 JSON struct tags (API contract, jsonb columns, cursors, asynq payload):_

| file:line | tag(s) | why |
|---|---|---|
| sosyal/imlec.go:19-20 | `ogeler`, `sonrakiImlec` | list envelope (API) |
| sosyal/imlec.go:38-39 | `t`, `id` | time cursor JSON (opaque, sent back by clients) |
| sosyal/imlec.go:46-48 | `p`, `id`, `n` | score cursor JSON |
| sosyal/imlec.go:103-106 | `g`, `z,omitempty`, `t`, `id` | comment cursor JSON |
| sosyal/model_yayin.go:7-8 | `tonik`, `mod` | API + jsonb |
| sosyal/model_yayin.go:13-15 | `id`, `tur`, `olcu` | API + jsonb `yayin.yapi` |
| sosyal/model_yayin.go:20-22 | `bolumId`, `tur`, `satirlar` | API + jsonb `yayin.sozler` |
| sosyal/model_yayin.go:26 | `bolumler` | API + jsonb `yayin.sozler` |
| sosyal/model_yayin.go:32-35 | `yayinId`, `baslik`, `sahipKullaniciAdi`, `silindi` | API |
| sosyal/model_yayin.go:41-56 | `id`, `slug`, `baslik`, `sahip`, `tur`, `bpm`, `ton`, `sureSn`, `dalgaFormu`, `begeniSayisi`, `yorumSayisi`, `coverSayisi`, `begendimMi`, `altyapiAcik`, `kaynak`, `olusturma` | API (PublicationSummary) |
| sosyal/model_yayin.go:62-68 | `aciklama`, `yorumAcik`, `sozler`, `yapi`, `durum`, `benimMi`, `gorunurluk` | API (PublicationDetail) |
| sosyal/model_yayin.go:74-77 | `id`, `enstruman`, `url`, `sure` | API request |
| sosyal/model_yayin.go:82-95 | `baslik`, `aciklama`, `gorunurluk`, `altyapiAcik`, `yorumAcik`, `masterUrl`, `stemler`, `sozler`, `yapi`, `tur`, `bpm`, `ton`, `sureSn`, `kaynakYayinId` | API request (POST /yayinlar) |
| sosyal/model_yayin.go:100-102 | `id`, `slug`, `durum` | API 202 body |
| sosyal/model_yayin.go:107-110 | `aciklama`, `altyapiAcik`, `gorunurluk`, `yorumAcik` | API PATCH body |
| sosyal/model_yayin.go:115-116 | `url`, `expiresAt,omitempty` | API |
| sosyal/model_yayin.go:120 | `master` | API |
| sosyal/model_yayin.go:127-130 | `id`, `enstruman`, `sure`, `key` | jsonb `yayin.hazirlik.stems[]` (rows already exist) |
| sosyal/model_yayin.go:134-135 | `masterKey`, `stems` | jsonb `yayin.hazirlik` |
| sosyal/model_yayin.go:141-144 | `id`, `enstruman`, `sure`, `anahtar` | jsonb `yayin.stem_listesi` |
| sosyal/model_yorum.go:7-12 | `id`, `yazar`, `metin`, `zamanMs`, `olusturma`, `silebilirMiyim` | API |
| sosyal/model_yorum.go:17-18 | `metin`, `zamanMs` | API request |
| sosyal/model.go:8-10 | `kullaniciAdi`, `gorunenAd`, `avatarRengi` | API |
| sosyal/model.go:16-19 | `sarki`, `takipci`, `takip`, `cover` | API |
| sosyal/model.go:25-28 | `bio`, `istatistik`, `takipEdiyorMuyum`, `benMiyim` | API |
| sosyal/model.go:33-35 | `kullaniciAdi`, `gorunenAd`, `bio` | API request |
| sosyal/model.go:41-42 | `uygun`, `oneri` | API |
| sosyal/servis_akis.go:17-18 | `begendimMi`, `begeniSayisi` | API |
| sosyal/servis_cover.go:14-17 | `id`, `enstruman`, `fileUrl`, `sure` | API |
| sosyal/servis_cover.go:22-29 | `kaynak`, `tur`, `bpm`, `ton`, `sureSn`, `yapi`, `sozler`, `stemler` | API |
| sosyal/servis_onizleme.go:19-24 | `baslik`, `sahip`, `sureSn`, `dalgaFormu`, `onizlemeUrl`, `tur` | public API |
| sosyal/servis_sikayet.go:25-28 | `hedefTuru`, `hedefId`, `neden`, `aciklama` | API request |
| sosyal/servis_takip.go:10-11 | `takipci`, `takipEdiyorMuyum` | API |
| sosyal/servis_yayin.go:38 | `yayinId` | asynq task payload (tasks may already be queued in Redis) |

_From the sosyal glossary, §4.2 Typed or enum constant values (only the identifier changes):_

| file:line | literal | why |
|---|---|---|
| sosyal/gorunurluk.go:7-9 | `"herkes"`, `"takipciler"`, `"link"` | visibility values in the API and the DB CHECK |
| sosyal/gorunurluk.go:11-13 | `"hazirlaniyor"`, `"yayinda"`, `"hata"` | status values in the API and the DB CHECK |
| sosyal/servis_akis.go:10-12 | `"senin-icin"`, `"takip"`, `"yeni"` | `?sekme=` query values |
| sosyal/servis_akis.go:110-111 | `"sarkilar"`, `"coverlar"` | `?tur=` query values |
| sosyal/servis_sikayet.go:13-14 | `"yayin"`, `"yorum"` | `hedefTuru` values; DB CHECK `hedef_turu` |
| sosyal/servis_sikayet.go:20 | `"spam"`, `"taciz"`, `"telif"`, `"uygunsuz"`, `"diger"` | `neden` values; DB CHECK |
| sosyal/yayin_kurallari.go:66 | `"major"`, `"minor"` | `ton.mod` values; DB CHECK |
| sosyal/servis_yayin.go:21 | `"sosyal:hazirla"` | asynq task type (registered in main.go mux) |
| sosyal/servis_yayin.go:24 | `"sosyal:dosya-sil"` | asynq task type |
| sosyal/servis_yayin.go:27 | `"sosyal"` | asynq queue name (main.go:389 queue priority map) |
| sosyal/servis_yayin.go:268 | `"bakim"` | asynq queue name |

_From the sosyal glossary, §4.3 R2 key layout:_

| file:line | literal | why |
|---|---|---|
| sosyal/anahtarlar.go:11 | `` `^\.[a-z0-9]{1,5}$` `` | extension rule must match client segment rule |
| sosyal/anahtarlar.go:24 | `"master"` (+ext) | R2 key `sosyal/{id}/master.{ext}` |
| sosyal/anahtarlar.go:29 | `"stems"` | R2 key `sosyal/{id}/stems/{stemId}.{ext}` |
| sosyal/anahtarlar.go:34 | `"onizleme.mp3"` | R2 key `sosyal/{id}/onizleme.mp3`. Existing objects and `onizleme_anahtari` rows point at it. |
| sosyal/servis_cover.go:45 | `"stems"` | R2 key `stems/{userId}/{folder}/{stemId}.{ext}` |
| sosyal/yayin_kurallari.go:11 | `` `^[A-Za-z0-9_-]{1,64}$` `` | stem ID becomes an R2 key segment |

_From the sosyal glossary, §4.4 SQL: every query string is wire (schema `sosyal`, tables, columns, constraint names):_

| file:line | content |
|---|---|
| sosyal/gorunurluk.go:69, 71 | `gorunurlukKosulu` SQL template (`'link'`, `'yayinda'`, `'herkes'`, `'takipciler'`, `sosyal.takip`, `takipci_id`, `takip_edilen_id`) |

_From the sosyal glossary, §4.6 Message text: NOT wire, to be localized later:_

| file:line | literal | note |
|---|---|---|
| sosyal/slug.go:19 | `errSlugCakisti` = `"slug cakismasi"` | internal, never leaves the package |
| sosyal/*: every `dogrulamaHatasi("…")` literal (imlec.go:60,76,93,123; kurallar.go:68,71,74; servis_akis.go:59,114; servis_sikayet.go:53,56,60,63; servis_yayin.go:64,66; servis_yorum.go:63,66,69,74,87; yayin_kurallari.go:59-131,139-147,158,161,166) | Turkish validation details | Message text, localized later. Each literal embeds JSON field names (`gorunenAd`, `masterUrl`, `zamanMs`, `hedefTuru`, `ton.mod`, `sureSn`…), which should stay as the JSON names when translated. |
| sosyal/servis_yayin.go:125, 129 | `"masterUrl"`, `"stem url"` | field label passed into validation messages (message text) |
| all `fmt.Errorf` / `log.Printf` literals in scope | wrapped error and log text | translate freely (not wire) |

**Risks (from the glossaries; names in them are the old ones)**

_From the sosyal glossary, risk 3:_

3. **Untagged structs that are, or could be, serialized.**
   - `OnizlemeKaydi` (`SahipID`, `OnizlemeAnahtari`) and `YayinKaydi` (`SahipID`, `KaynakYayinID`, `StemVar`, `MasterAnahtari`, `Silindi`, `TakipEdiyor`) embed tagged API types but add untagged internal fields. Today the service returns only the embedded `Onizleme` / `YayinDetayi`. If a rename ever returned the record itself, the Go field names would become JSON keys and leak internal R2 keys. Renaming is safe now, but keep the "return the embedded value" pattern.
   - `PuanliOzet.Puan` is untagged and never serialized; it goes into the cursor as `p`.
   - Embedded types (`YayinOzeti` in `YayinDetayi`, `ProfilOzeti` in `Profil`, `Onizleme` in `OnizlemeKaydi`) are untagged, so their fields are flattened. Renaming the embedded type does not change the JSON. If someone adds a tag or turns the embed into a named field, the JSON nests.
   - **Fiber `BodyParser`** (handler: `ProfilGirdisi`, `YayinIstegi`, `YayinYamasi`, `YorumGirdisi`, `SikayetGirdisi`) also accepts `application/x-www-form-urlencoded`, multipart and XML. Those decoders use `form` / `xml` tags, which none of these structs have, so they fall back to the Go field names. A non-JSON client would see the field rename. Mobile and web send JSON, so the risk is low, but it is not zero.

_From the sosyal glossary, risk 4:_

4. **Name collisions after the rename.**
   - In `social`: `summaryScanner` gets a method `summary()`, so its embedded value field must not also be called `summary`. That is why it is `base`.
   - `PostgresStore.getPublication` and `GetPublication` coexist (different case; this is fine).
   - Same-named type and method pairs are legal: `FollowStatus` type and `FollowStore.FollowStatus` method; `ProfileCounts`; `Preview` type and `Service.Preview`; `Profile` type and `Service.Profile`.
   - `MasterKey` is now a field on three structs (`Preparation`, `PublicationRecord`, `PreparationResult`). That is legal but easy to confuse.
   - In `handler`: `SocialService` must not collide with an imported package name. The handler does not import a package called `service` in these files. Check this if another glossary imports `internal/service` into the handler package.
   - In `worker`: `fakeCopier` / `fakeAnalyzer` might be chosen by the render/master test glossaries for `sahteSes` and similar fakes. That is why this scope uses `fakeSocialCopier`, `fakeSocialDeleter`, `socialTestPubID` and `decodeSocialTaskPayload`. `previewSeconds` and `waveformPeakCount` are new package-level consts in `worker`; check them against renames of `render_worker.go` (`generateWaveform`, `tahminiSure`).
   - `intp` exists in both the `social` tests and the `worker` tests. These are separate packages, so they do not clash.

_From the sosyal glossary, risk 8:_

8. **The cursor format is wire even though it is "opaque".** Clients hold base64url JSON with keys `t`, `id`, `p`, `n`, `g` and `z`, and send it back on later pages. Rename the field identifiers only (for example `ScoreCursor.Score` for `p` and `RefMs` for `n`). Changing a tag invalidates every in-flight cursor, which then fails with 422 `dogrulama`.

_From the sosyal glossary, risk 9:_

9. **Error message and code coincidence, and the test fakes.** `ErrBulunamadi`'s text is `"bulunamadi"`, the same string as the code. When the messages are localized later, the code must still come from the handler table (`sosyalHatalari`), never from `err.Error()`. The handler tests assert only codes, never messages. Inside `sahte_depo_test.go`, the `akisCagrisi.tur` labels (`"yeni"`, `"takip"`, `"senin-icin"`, `"profil"`, `"cover"`) are compared by `servis_akis_test.go` and `servis_cover_test.go`. If they are translated, change all of those files in one step.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Apply part 1 (interface methods and their fakes)**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 14-first
```

Expected: for each interface method, a `rename` line followed by a `textual` line for its concrete implementer; `already` for each fake that gopls renamed with the interface; then `<k> renamed, <n> rows in table`. gopls refuses every rename while any package has type errors, and renaming an interface method breaks the build until the implementer follows. So the script renames the implementer textually right after each interface method (the `manual: textual` rows; it checks that exactly one declaration matches) and runs `go build ./...` before the next row. If it stops, fix the reported cause and re-run it: finished rows print `already`.

- [ ] **Step 3: Check that no code reference to the old `Servis` method names is left**

The handler still has route methods with some of the same Turkish names (`SosyalHandler.ProfilBen`, ...; Task 20 renames them), so this check looks only for method declarations and calls in `internal/social` and `cmd/server`, and for calls through the service interface in `internal/handler`:

```bash
grep -rnE '\) (ProfilBen|ProfilKaydet|AdUygunMu|ProfilGoster|Akis|ProfilYayinlari|Begeni|Coverlar|CoverBaslat|Onizleme|Sikayet|Takip|Oneriler|YayinOlustur|YayinGoster|SlugGoster|YayinGuncelle|YayinSil|TekrarDene|Dosyalar|Yorumlar|YorumYaz|YorumSil)\(|\.(ProfilBen|ProfilKaydet|AdUygunMu|ProfilGoster|Akis|ProfilYayinlari|Begeni|Coverlar|CoverBaslat|Onizleme|Sikayet|Takip|Oneriler|YayinOlustur|YayinGoster|SlugGoster|YayinGuncelle|YayinSil|TekrarDene|Dosyalar|Yorumlar|YorumYaz|YorumSil)\(' --include='*.go' internal/social cmd
grep -rnE 'servis\.(ProfilBen|ProfilKaydet|AdUygunMu|ProfilGoster|Akis|ProfilYayinlari|Begeni|Coverlar|CoverBaslat|Onizleme|Sikayet|Takip|Oneriler|YayinOlustur|YayinGoster|SlugGoster|YayinGuncelle|YayinSil|TekrarDene|Dosyalar|Yorumlar|YorumYaz|YorumSil)\(' --include='*.go' internal/handler
files=$(gofmt -l .); [ -z "$files" ] || gofmt -w $files
go build ./... && go vet ./...
```

Expected: both `grep` commands print nothing, and the build and vet print nothing.


- [ ] **Step 4: Apply the declaration table (part 2)**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 14 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 5: Do the `manual:` rows**

Follow the rules under the table. Then run:

```bash
go build ./... && go vet ./...
```

- [ ] **Step 6: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).

Files (17): `internal/social/cursor.go`, `internal/social/keys.go`, `internal/social/model.go`, `internal/social/model_comment.go`, `internal/social/model_publication.go`, `internal/social/profile_rules.go`, `internal/social/publication_rules.go`, `internal/social/service.go`, `internal/social/service_comment.go`, `internal/social/service_cover.go`, `internal/social/service_feed.go`, `internal/social/service_follow.go`, `internal/social/service_preview.go`, `internal/social/service_publication.go`, `internal/social/service_report.go`, `internal/social/slug.go`, `internal/social/visibility.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/social/cursor.go internal/social/keys.go internal/social/model.go internal/social/model_comment.go internal/social/model_publication.go internal/social/profile_rules.go internal/social/publication_rules.go internal/social/service.go internal/social/service_comment.go internal/social/service_cover.go internal/social/service_feed.go internal/social/service_follow.go internal/social/service_preview.go internal/social/service_publication.go internal/social/service_report.go internal/social/slug.go internal/social/visibility.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 7: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 14-first --dry-run | grep -v '^already'
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 14 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 8: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 9: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(social): servis, modeller ve kurallar Ingilizce

Servis -> Service ve metotlari (handler.SocialService ile birlikte),
modeller, kurallar, imlecler ve gorunurluk Ingilizceye cevrildi. JSON
etiketleri ve enum degerleri ayni; kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 15: social: test helpers, fakes and test names

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

_No file in this task changes its name._

The files were moved and renamed in Task 13.

**Declarations** (212 rows)

<!-- renames:15 -->
| old | new | kind | file |
|---|---|---|---|
| TestHedefAnahtarlari | TestDestKeys | func (test) | internal/social/keys_test.go |
| jsonAlanlari | jsonFields | func | internal/social/integration_test.go |
| TestButunlesik_YayinYasamDongusu | TestIntegration_PublicationLifecycle | func (test) | internal/social/integration_test.go |
| TestButunlesik_AdUygunlugu | TestIntegration_UsernameAvailability | func (test) | internal/social/integration_test.go |
| tumSayfalar | allTimePages | func | internal/social/store_feed_test.go |
| esitSira | assertOrder | func | internal/social/store_feed_test.go |
| akisKurulumu | setupFeed | func | internal/social/store_feed_test.go |
| TestPostgresDepo_AkisYeniVeSayfalama | TestPostgresStore_FeedNewPaging | func (test) | internal/social/store_feed_test.go |
| TestPostgresDepo_AkisTakip | TestPostgresStore_FeedFollowing | func (test) | internal/social/store_feed_test.go |
| TestPostgresDepo_ProfilYayinlari | TestPostgresStore_ListProfilePublications | func (test) | internal/social/store_feed_test.go |
| begeniEkle | insertLikes | func | internal/social/store_feed_test.go |
| TestPostgresDepo_AkisSeninIcinPuanVeSayfalama | TestPostgresStore_FeedForYouScoreAndPaging | func (test) | internal/social/store_feed_test.go |
| TestPostgresDepo_Begeni | TestPostgresStore_Like | func (test) | internal/social/store_feed_test.go |
| TestPostgresDepo_CoverlarGorunurlukVeSira | TestPostgresStore_ListCoversVisibilityAndOrder | func (test) | internal/social/store_cover_test.go |
| TestPostgresDepo_StemListesi | TestPostgresStore_ListStems | func (test) | internal/social/store_cover_test.go |
| TestPostgresDepo_CoverlarProfilsizSahibiGizler | TestPostgresStore_ListCoversHidesOwnerWithoutProfile | func (test) | internal/social/store_cover_test.go |
| TestPostgresDepo_OnizlemeYalnizcaHerkesVeLink | TestPostgresStore_GetPreviewOnlyEveryoneAndLink | func (test) | internal/social/store_preview_test.go |
| TestPostgresDepo_OnizlemeProfilsizSahip | TestPostgresStore_GetPreviewOwnerWithoutProfile | func (test) | internal/social/store_preview_test.go |
| TestPostgresDepo_ProfilKaydetVeGetir | TestPostgresStore_UpsertAndGetProfile | func (test) | internal/social/store_profile_test.go |
| TestPostgresDepo_AlinmisAd | TestPostgresStore_UsernameTaken | func (test) | internal/social/store_profile_test.go |
| TestPostgresDepo_ProfilSayilari | TestPostgresStore_ProfileCounts | func (test) | internal/social/store_profile_test.go |
| TestPostgresDepo_SikayetTekrariYeniKayitAcmaz | TestPostgresStore_DuplicateReportCreatesNoRow | func (test) | internal/social/store_report_test.go |
| testBegeniEkle | testInsertLikeAt | func | internal/social/store_follow_test.go |
| TestPostgresDepo_TakipTekrariGuvenli | TestPostgresStore_FollowIdempotent | func (test) | internal/social/store_follow_test.go |
| TestPostgresDepo_Oneriler | TestPostgresStore_Suggestions | func (test) | internal/social/store_follow_test.go |
| TestPostgresDepo_OnerilerEsitlikteYeniYayinOnde | TestPostgresStore_SuggestionsTieBreaksOnNewestPublication | func (test) | internal/social/store_follow_test.go |
| TestPostgresDepo_KullaniciVerisiniSil | TestPostgresStore_DeleteUserData | func (test) | internal/social/store_cleanup_test.go |
| izSenaryosu | traceScenario | type | internal/social/store_cleanup_test.go |
| izSenaryosu.giden | traceScenario.leaving | field | internal/social/store_cleanup_test.go |
| izSenaryosu.kalan | traceScenario.staying | field | internal/social/store_cleanup_test.go |
| izSenaryosu.gidenYayini | traceScenario.leavingPublication | field | internal/social/store_cleanup_test.go |
| izSenaryosu.kalaninYayini | traceScenario.stayingPublication | field | internal/social/store_cleanup_test.go |
| izSenaryosuKur | setupTraceScenario | func | internal/social/store_cleanup_test.go |
| sikayetSayisi | reportCount | func | internal/social/store_cleanup_test.go |
| TestPostgresDepo_KullaniciIzleriniSil | TestPostgresStore_DeleteUserTraces | func (test) | internal/social/store_cleanup_test.go |
| TestPostgresDepo_KullaniciVerisiniSil_YorumSikayetleri | TestPostgresStore_DeleteUserData_CommentReports | func (test) | internal/social/store_cleanup_test.go |
| TestPostgresDepo_KullaniciVerisiniSil_SilinmisYorumVeKalanSikayetler | TestPostgresStore_DeleteUserData_DeletedCommentAndRemainingReports | func (test) | internal/social/store_cleanup_test.go |
| testYayin | testPublication | type | internal/social/store_helpers_test.go |
| testYayin.SahipID | testPublication.OwnerID | field | internal/social/store_helpers_test.go |
| testYayin.Baslik | testPublication.Title | field | internal/social/store_helpers_test.go |
| testYayin.Gorunurluk | testPublication.Visibility | field | internal/social/store_helpers_test.go |
| testYayin.Durum | testPublication.Status | field | internal/social/store_helpers_test.go |
| testYayin.AltyapiAcik | testPublication.BackingEnabled | field | internal/social/store_helpers_test.go |
| testYayin.KaynakID | testPublication.SourceID | field | internal/social/store_helpers_test.go |
| testYayin.Olusturma | testPublication.CreatedAt | field | internal/social/store_helpers_test.go |
| testYayin.Silindi | testPublication.Deleted | field | internal/social/store_helpers_test.go |
| testYayin.Hazirlik | testPublication.Preparation | field (raw JSON string) | internal/social/store_helpers_test.go |
| slugSayaci | slugCounter | var | internal/social/store_helpers_test.go |
| testYayinEkle | testInsertPublication | func | internal/social/store_helpers_test.go |
| testTakipEkle | testInsertFollow | func | internal/social/store_helpers_test.go |
| testProfilEkle | testInsertProfile | func | internal/social/store_helpers_test.go |
| ornekYeniYayin | samplePublicationInsert | func | internal/social/store_publication_test.go |
| TestPostgresDepo_YayinEkleVeGetir | TestPostgresStore_InsertAndGetPublication | func (test) | internal/social/store_publication_test.go |
| TestPostgresDepo_SlugCakismasi | TestPostgresStore_SlugConflict | func (test) | internal/social/store_publication_test.go |
| TestPostgresDepo_GorunurlukSQLVeGoAyni | TestPostgresStore_VisibilitySQLMatchesGo | func (test) | internal/social/store_publication_test.go |
| TestPostgresDepo_SayaclarVeKaynak | TestPostgresStore_CountersAndSource | func (test) | internal/social/store_publication_test.go |
| TestPostgresDepo_GuncelleSilTekrarDene | TestPostgresStore_UpdateDeleteRetry | func (test) | internal/social/store_publication_test.go |
| TestPostgresDepo_Hazirlik | TestPostgresStore_Preparation | func (test) | internal/social/store_publication_test.go |
| TestPostgresDepo_HataYapVeSilinmisYayinYayinlanmaz | TestPostgresStore_MarkFailedAndDeletedNotPublished | func (test) | internal/social/store_publication_test.go |
| TestPostgresDepo_NilStemlerleEkleVeGetir | TestPostgresStore_InsertAndGetWithNilStems | func (test) | internal/social/store_publication_test.go |
| TestPostgresDepo_NullStemVeSatirlarDetayiBozmaz | TestPostgresStore_NullStemsAndLinesKeepDetailValid | func (test) | internal/social/store_publication_test.go |
| TestPostgresDepo_HataYapVeKaldirKorumalari | TestPostgresStore_MarkFailedAndRemoveGuards | func (test) | internal/social/store_publication_test.go |
| testYorumEkle | testInsertComment | func | internal/social/store_comment_test.go |
| tumYorumSayfalari | allCommentPages | func | internal/social/store_comment_test.go |
| TestPostgresDepo_YorumSirasiVeSayfalama | TestPostgresStore_CommentOrderAndPaging | func (test) | internal/social/store_comment_test.go |
| TestPostgresDepo_YorumEkleGetirSil | TestPostgresStore_InsertGetDeleteComment | func (test) | internal/social/store_comment_test.go |
| TestPostgresDepo_YorumlarProfilsizYazariGizler | TestPostgresStore_ListCommentsHidesAuthorWithoutProfile | func (test) | internal/social/store_comment_test.go |
| TestGorunurlukTablosu | TestVisibility_Table | func (test) | internal/social/visibility_test.go |
| TestGorunurluk_SahipHerDurumuGorur | TestVisibility_OwnerSeesEveryStatus | func (test) | internal/social/visibility_test.go |
| TestGorunurluk_YabanciHazirlanmayiVeHatayiGormez | TestVisibility_StrangerCannotSeePreparingOrFailed | func (test) | internal/social/visibility_test.go |
| TestGorunurluk_SilinmisiKimseGormez | TestVisibility_NobodySeesDeleted | func (test) | internal/social/visibility_test.go |
| TestGorunurluk_BosIzleyiciSahipSayilmaz | TestVisibility_EmptyViewerIsNotOwner | func (test) | internal/social/visibility_test.go |
| TestSayfaBoyutu | TestPageSize | func (test) | internal/social/cursor_test.go |
| TestZamanImleci_GidisDonus | TestTimeCursor_RoundTrip | func (test) | internal/social/cursor_test.go |
| TestPuanImleci_GidisDonus | TestScoreCursor_RoundTrip | func (test) | internal/social/cursor_test.go |
| TestImlec_BozukReddedilir | TestCursor_MalformedRejected | func (test) | internal/social/cursor_test.go |
| TestYorumImleci_ZInt32Siniri | TestCommentCursor_ZInt32Limit | func (test) | internal/social/cursor_test.go |
| TestKullaniciAdiGecerli | TestValidUsername | func (test) | internal/social/profile_rules_test.go |
| TestAvatarRengi_SabitVeAralikta | TestAvatarColor_StableAndInRange | func (test) | internal/social/profile_rules_test.go |
| TestAdAdaylari_SiraVeUzunluk | TestUsernameCandidates_OrderAndLength | func (test) | internal/social/profile_rules_test.go |
| TestProfilGirdisiniDuzenle | TestNormalizeProfileInput | func (test) | internal/social/profile_rules_test.go |
| TestKimlikCoz | TestParseID | func (test) | internal/social/profile_rules_test.go |
| TestProfilGirdisiniDuzenle_NULKarakteriReddedilir | TestNormalizeProfileInput_RejectsNUL | func (test) | internal/social/profile_rules_test.go |
| sahteKuyruk | fakeQueue | type | internal/social/fake_store_test.go |
| sahteKuyruk.gorevler | fakeQueue.tasks | field | internal/social/fake_store_test.go |
| sahteKuyruk.secenekler | fakeQueue.options | field | internal/social/fake_store_test.go |
| sahteKuyruk.hata | fakeQueue.err | field | internal/social/fake_store_test.go |
| sahteKuyruk.once | fakeQueue.before | field | internal/social/fake_store_test.go |
| testServisi | newTestService | func | internal/social/fake_store_test.go |
| sahteDepo | fakeStore | type | internal/social/fake_store_test.go |
| sahteDepo.profiller | fakeStore.profiles | field | internal/social/fake_store_test.go |
| sahteDepo.sayilar | fakeStore.counts | field | internal/social/fake_store_test.go |
| sahteDepo.hata | fakeStore.err | field | internal/social/fake_store_test.go |
| sahteDepo.yayinlar | fakeStore.publications | field | internal/social/fake_store_test.go |
| sahteDepo.eklenen | fakeStore.inserted | field | internal/social/fake_store_test.go |
| sahteDepo.slugCakismasi | fakeStore.slugConflicts | field | internal/social/fake_store_test.go |
| sahteDepo.yamalar | fakeStore.patches | field | internal/social/fake_store_test.go |
| sahteDepo.kaldirilan | fakeStore.removed | field | internal/social/fake_store_test.go |
| sahteDepo.hazirliklar | fakeStore.preparations | field | internal/social/fake_store_test.go |
| sahteDepo.yayinlanan | fakeStore.published | field | internal/social/fake_store_test.go |
| sahteDepo.hataYapilan | fakeStore.failed | field | internal/social/fake_store_test.go |
| sahteDepo.akisOgeleri | fakeStore.feedItems | field | internal/social/fake_store_test.go |
| sahteDepo.sonAkis | fakeStore.lastFeed | field | internal/social/fake_store_test.go |
| sahteDepo.takipler | fakeStore.follows | field | internal/social/fake_store_test.go |
| sahteDepo.oneriKayitlar | fakeStore.suggestionRecords | field | internal/social/fake_store_test.go |
| sahteDepo.sonOneri | fakeStore.lastSuggestion | field | internal/social/fake_store_test.go |
| sahteDepo.yorumKayitlari | fakeStore.commentRecords | field | internal/social/fake_store_test.go |
| sahteDepo.yorumOgeleri | fakeStore.commentItems | field | internal/social/fake_store_test.go |
| sahteDepo.eklenenYorumlar | fakeStore.insertedComments | field | internal/social/fake_store_test.go |
| sahteDepo.silinenYorumlar | fakeStore.deletedComments | field | internal/social/fake_store_test.go |
| sahteDepo.sonYorumImleci | fakeStore.lastCommentCursor | field | internal/social/fake_store_test.go |
| sahteDepo.sonYorumLimiti | fakeStore.lastCommentLimit | field | internal/social/fake_store_test.go |
| sahteDepo.sikayetler | fakeStore.reports | field | internal/social/fake_store_test.go |
| sahteDepo.stemListeleri | fakeStore.stemLists | field | internal/social/fake_store_test.go |
| sahteDepo.onizlemeler | fakeStore.previews | field | internal/social/fake_store_test.go |
| yeniSahteDepo | newFakeStore | func | internal/social/fake_store_test.go |
| sahteDepo.kopya | fakeStore.viewerCopy | method | internal/social/fake_store_test.go |
| sahteDepo.sahibinCanliYayini | fakeStore.ownersLivePublication | method | internal/social/fake_store_test.go |
| akisCagrisi | feedCall | type | internal/social/fake_store_test.go |
| akisCagrisi.tur | feedCall.kind | field | internal/social/fake_store_test.go |
| akisCagrisi.izleyici | feedCall.viewer | field | internal/social/fake_store_test.go |
| akisCagrisi.sahip | feedCall.owner | field | internal/social/fake_store_test.go |
| akisCagrisi.coverlar | feedCall.covers | field | internal/social/fake_store_test.go |
| akisCagrisi.zaman | feedCall.timeCursor | field | internal/social/fake_store_test.go |
| akisCagrisi.puan | feedCall.scoreCursor | field | internal/social/fake_store_test.go |
| akisCagrisi.referans | feedCall.reference | field | internal/social/fake_store_test.go |
| sahteDepo.zamanListesi | fakeStore.timeList | method | internal/social/fake_store_test.go |
| ozetler | sampleSummaries | func | internal/social/service_feed_test.go |
| TestServis_AkisYeniSayfalari | TestService_FeedNewPages | func (test) | internal/social/service_feed_test.go |
| TestServis_AkisSeninIcinReferansSabit | TestService_FeedForYouStableReference | func (test) | internal/social/service_feed_test.go |
| TestServis_AkisSeninIcinGelecekReferansKirpilir | TestService_FeedForYouClampsFutureReference | func (test) | internal/social/service_feed_test.go |
| TestServis_AkisHatalari | TestService_FeedErrors | func (test) | internal/social/service_feed_test.go |
| TestServis_ProfilYayinlari | TestService_ProfilePublications | func (test) | internal/social/service_feed_test.go |
| TestServis_Begeni | TestService_SetLike | func (test) | internal/social/service_feed_test.go |
| sahteKopyalayici | fakeCopier | type | internal/social/service_cover_test.go |
| sahteKopyalayici.kopyalar | fakeCopier.copies | field | internal/social/service_cover_test.go |
| sahteKopyalayici.hata | fakeCopier.err | field | internal/social/service_cover_test.go |
| coverDeposu | coverStore | func | internal/social/service_cover_test.go |
| TestServis_CoverBaslat | TestService_StartCover | func (test) | internal/social/service_cover_test.go |
| TestServis_CoverBaslatImzaliAdres | TestService_StartCoverSignedURL | func (test) | internal/social/service_cover_test.go |
| TestServis_CoverBaslatKopyalayicisiz | TestService_StartCoverWithoutCopier | func (test) | internal/social/service_cover_test.go |
| TestServis_CoverBaslatHatalari | TestService_StartCoverErrors | func (test) | internal/social/service_cover_test.go |
| TestServis_Coverlar | TestService_Covers | func (test) | internal/social/service_cover_test.go |
| sureliDepolama | ttlRecordingStorage | type | internal/social/service_preview_test.go |
| sureliDepolama.sure | ttlRecordingStorage.ttl | field | internal/social/service_preview_test.go |
| sureliDepolama.hata | ttlRecordingStorage.err | field | internal/social/service_preview_test.go |
| onizlemeDeposu | previewStore | func | internal/social/service_preview_test.go |
| TestServis_OnizlemeImzali1Saat | TestService_PreviewSignedForOneHour | func (test) | internal/social/service_preview_test.go |
| TestServis_OnizlemeR2siz | TestService_PreviewWithoutR2 | func (test) | internal/social/service_preview_test.go |
| TestServis_OnizlemeBulunamadi | TestService_PreviewNotFound | func (test) | internal/social/service_preview_test.go |
| kullaniciA | userA | const | internal/social/service_profile_test.go |
| kullaniciB | userB | const | internal/social/service_profile_test.go |
| TestServis_ProfilBenYoksaProfilYok | TestService_MyProfileWithoutProfileReturnsNoProfile | func (test) | internal/social/service_profile_test.go |
| TestServis_GecersizKimlik | TestService_InvalidIdentity | func (test) | internal/social/service_profile_test.go |
| TestServis_ProfilKaydetVeBen | TestService_SaveProfileAndMyProfile | func (test) | internal/social/service_profile_test.go |
| TestServis_ProfilKaydetHatalari | TestService_SaveProfileErrors | func (test) | internal/social/service_profile_test.go |
| TestServis_AdUygunMu | TestService_CheckUsername | func (test) | internal/social/service_profile_test.go |
| TestServis_ProfilGoster | TestService_Profile | func (test) | internal/social/service_profile_test.go |
| TestServis_SikayetYayinVeYorum | TestService_ReportPublicationAndComment | func (test) | internal/social/service_report_test.go |
| TestServis_SikayetHatalari | TestService_ReportErrors | func (test) | internal/social/service_report_test.go |
| TestServis_SikayetBozukKimlikDepoyaGitmez | TestService_ReportMalformedIDSkipsStore | func (test) | internal/social/service_report_test.go |
| takipDeposu | followStore | func | internal/social/service_follow_test.go |
| TestServis_TakipEtVeBirak | TestService_FollowAndUnfollow | func (test) | internal/social/service_follow_test.go |
| TestServis_TakipHatalari | TestService_FollowErrors | func (test) | internal/social/service_follow_test.go |
| TestServis_OnerilerOzetVeSinir | TestService_SuggestionsSummaryAndLimit | func (test) | internal/social/service_follow_test.go |
| yayinX | pubX | const | internal/social/service_publication_test.go |
| yayinY | pubY | const | internal/social/service_publication_test.go |
| profilliDepo | storeWithProfile | func | internal/social/service_publication_test.go |
| sahteYayin | fakePublication | func | internal/social/service_publication_test.go |
| yayinIstegiA | publicationRequestA | func | internal/social/service_publication_test.go |
| secenek | optionValue | func | internal/social/service_publication_test.go |
| TestServis_YayinOlustur_ProfilYok | TestService_CreatePublication_NoProfile | func (test) | internal/social/service_publication_test.go |
| TestServis_YayinOlustur_KaydederVeKuyrugaKoyar | TestService_CreatePublication_SavesAndEnqueues | func (test) | internal/social/service_publication_test.go |
| TestServis_YayinOlustur_AdresHatalari | TestService_CreatePublication_URLErrors | func (test) | internal/social/service_publication_test.go |
| TestServis_YayinOlustur_SlugCakismasiYenidenDenenir | TestService_CreatePublication_RetriesOnSlugConflict | func (test) | internal/social/service_publication_test.go |
| TestServis_YayinOlustur_KuyrukHatasindaGeriAlinir | TestService_CreatePublication_RollsBackOnQueueError | func (test) | internal/social/service_publication_test.go |
| TestServis_YayinOlustur_IptalEdilenBaglamdaGeriAlinir | TestService_CreatePublication_RollsBackOnCanceledContext | func (test) | internal/social/service_publication_test.go |
| TestServis_YayinOlustur_Cover | TestService_CreatePublication_Cover | func (test) | internal/social/service_publication_test.go |
| TestServis_YayinGosterGorunurluk | TestService_PublicationVisibility | func (test) | internal/social/service_publication_test.go |
| TestServis_YayinGuncelle | TestService_UpdatePublication | func (test) | internal/social/service_publication_test.go |
| TestServis_YayinSil | TestService_DeletePublication | func (test) | internal/social/service_publication_test.go |
| TestServis_TekrarDene | TestService_Retry | func (test) | internal/social/service_publication_test.go |
| TestServis_TekrarDene_IptalEdilenBaglamdaHatayaDoner | TestService_Retry_MarksFailedOnCanceledContext | func (test) | internal/social/service_publication_test.go |
| imzaliDepolama | signingStorage | type | internal/social/service_publication_test.go |
| TestServis_Dosyalar | TestService_Files | func (test) | internal/social/service_publication_test.go |
| kullaniciC | userC | const | internal/social/service_comment_test.go |
| yorum1 | comment1 | const | internal/social/service_comment_test.go |
| yorumC | commentC | const | internal/social/service_comment_test.go |
| yorumDeposu | commentStore | func | internal/social/service_comment_test.go |
| TestServis_YorumYaz | TestService_PostComment | func (test) | internal/social/service_comment_test.go |
| TestServis_YorumYazHatalari | TestService_PostCommentErrors | func (test) | internal/social/service_comment_test.go |
| TestServis_YorumlarKapaliVeGorunurluk | TestService_CommentsDisabledAndVisibility | func (test) | internal/social/service_comment_test.go |
| TestServis_YorumlarSayfaVeImlec | TestService_CommentsPageAndCursor | func (test) | internal/social/service_comment_test.go |
| TestServis_YorumImleciDogrulama | TestService_CommentCursorValidation | func (test) | internal/social/service_comment_test.go |
| TestServis_YorumSil | TestService_DeleteComment | func (test) | internal/social/service_comment_test.go |
| TestSlugUret_BicimVeCesitlilik | TestGenerateSlug_FormatAndVariety | func (test) | internal/social/slug_test.go |
| TestSlugGecerli | TestValidSlug | func (test) | internal/social/slug_test.go |
| testHavuz | testPool | func | internal/social/database_test.go |
| TestMigrationlarGomuluVeSirali | TestMigrations_EmbeddedAndSorted | func (test) | internal/social/database_test.go |
| TestMigrate_IkiKezKosarVeTablolariKurar | TestMigrate_RunsTwiceAndCreatesTables | func (test) | internal/social/database_test.go |
| TestMigrate_YayinSilinceBagliKayitlarGider | TestMigrate_DeletingPublicationCascades | func (test) | internal/social/database_test.go |
| gecerliIstek | validRequest | func | internal/social/publication_rules_test.go |
| TestYayinIsteginiDuzenle_Gecerli | TestNormalizePublicationRequest_Valid | func (test) | internal/social/publication_rules_test.go |
| TestYayinIsteginiDuzenle_Hatalar | TestNormalizePublicationRequest_Errors | func (test) | internal/social/publication_rules_test.go |
| TestYayinIsteginiDuzenle_SinirDegerler | TestNormalizePublicationRequest_BoundaryValues | func (test) | internal/social/publication_rules_test.go |
| TestYayinIsteginiDuzenle_CoverAltyapiKapanirStemlerAtilir | TestNormalizePublicationRequest_CoverDisablesBackingDropsStems | func (test) | internal/social/publication_rules_test.go |
| TestYayinIsteginiDuzenle_BosKaynakOrijinaldir | TestNormalizePublicationRequest_EmptySourceIsOriginal | func (test) | internal/social/publication_rules_test.go |
| TestYayinIsteginiDuzenle_KapaliAltyapiStemleriYokSayar | TestNormalizePublicationRequest_DisabledBackingIgnoresStems | func (test) | internal/social/publication_rules_test.go |
| TestYamayiDuzenle | TestNormalizePatch | func (test) | internal/social/publication_rules_test.go |
| TestYayinIsteginiDuzenle_IcIceAlanSinirlari | TestNormalizePublicationRequest_NestedFieldLimits | func (test) | internal/social/publication_rules_test.go |
| TestYayinIsteginiDuzenle_NULKarakteriReddedilir | TestNormalizePublicationRequest_RejectsNUL | func (test) | internal/social/publication_rules_test.go |
| TestYamayiDuzenle_NULKarakteriReddedilir | TestNormalizePatch_RejectsNUL | func (test) | internal/social/publication_rules_test.go |

**Wire strings to keep (must stay byte-identical)**

_From the sosyal glossary, §4.6 Message text: NOT wire, to be localized later:_

| file:line | literal | note |
|---|---|---|
| all `fmt.Errorf` / `log.Printf` literals in scope | wrapped error and log text | translate freely (not wire) |

_From the sosyal glossary, §4.7 Wire literals in test files (the tests pin the contract; keep them):_

| file:line | literal | why |
|---|---|---|
| sosyal/anahtarlar_test.go:7-25 | `"sosyal/"+id+"/master.wav"`, `/master`, `/stems/drums.mp3`, `/onizleme.mp3` | R2 layout assertions |
| sosyal/butunlesik_test.go:80-82, 87, 90 | JSON key list (`id` … `gorunurluk`), `"yayinda"`, `"kullaniciAdi"` | API contract assertions |
| sosyal/butunlesik_test.go:47-48 | `"masters/…"`, `"stems/…"` | R2 source-key layout |
| sosyal/veritabani_test.go:21, 23 | env `KIMLIK_TEST_DB_URL`, default DSN | env var name (test config) |
| sosyal/veritabani_test.go:36, 52, 56, 61, 74-77, 93-112 | TRUNCATE list, `"migrations/001_sosyal.sql"`, table names, `information_schema` query, INSERT/DELETE SQL | schema |
| sosyal/depo_yardimci_test.go:54, 64-70, 80-81 | hazirlik JSON `{"masterKey":…,"stems":[]}`, INSERT sosyal.yayin / sosyal.takip, `'{"bolumler":[]}'` | schema and jsonb shape |
| sosyal/depo_akis_test.go:135; depo_takip_test.go:17; depo_yorum_test.go:20-21; depo_cover_test.go:55; depo_onizleme_test.go:34; depo_sikayet_test.go:33-34, 45; depo_temizlik_test.go:27-32, 59-64, 106-114, 126, 157-159, 220; depo_yayin_test.go:104, 131, 136, 171, 263, 323, 335-336 | raw SQL; `'{"bolumler":[{"bolumId":"b1","tur":"verse","satirlar":null}]}'` | schema and jsonb shape |
| sosyal/depo_onizleme_test.go:35, 53; depo_cover_test.go:54; depo_yayin_test.go:20-21, 244, 250-251; servis_cover_test.go:35-36, 60-65, 87; servis_onizleme_test.go:36, 50, 74; servis_yayin_test.go:37-38, 78-81, 103-105, 373-385 | R2 key literals (`sosyal/…`, `stems/…`, `masters/…`, `onizleme.mp3`) | R2 layout |
| sosyal/servis_yayin_test.go:181, 293 | `"hazirlaniyor"`, `"bakim"` | status value; queue name |
| sosyal/servis_sikayet_test.go:16-85; depo_sikayet_test.go:16-37 | `"yayin"`, `"yorum"`, `"spam"`, `"taciz"`, `"telif"`, `"diger"` | report enum values |
| sosyal/servis_akis_test.go:31, 43, 125, 138; depo_akis_test.go:85, 101, 123; depo_yayin_test.go:188, 193 | `"yeni"`, `"takip"`, `"coverlar"`, `"sarkilar"` | tab and kind query values (some are fake-call labels, see risk 9) |
| sosyal/sahte_depo_test.go:322-334, 462 | `feedCall.kind` labels `"profil"`, `"yeni"`, `"takip"`, `"senin-icin"`, `"cover"` | Test-internal labels, not wire. Keep them, or translate them together with their asserting tests in servis_akis_test.go and servis_cover_test.go. |

**Risks (from the glossaries; names in them are the old ones)**

_From the sosyal glossary, risk 4:_

4. **Name collisions after the rename.**
   - In `social`: `summaryScanner` gets a method `summary()`, so its embedded value field must not also be called `summary`. That is why it is `base`.
   - `PostgresStore.getPublication` and `GetPublication` coexist (different case; this is fine).
   - Same-named type and method pairs are legal: `FollowStatus` type and `FollowStore.FollowStatus` method; `ProfileCounts`; `Preview` type and `Service.Preview`; `Profile` type and `Service.Profile`.
   - `MasterKey` is now a field on three structs (`Preparation`, `PublicationRecord`, `PreparationResult`). That is legal but easy to confuse.
   - In `handler`: `SocialService` must not collide with an imported package name. The handler does not import a package called `service` in these files. Check this if another glossary imports `internal/service` into the handler package.
   - In `worker`: `fakeCopier` / `fakeAnalyzer` might be chosen by the render/master test glossaries for `sahteSes` and similar fakes. That is why this scope uses `fakeSocialCopier`, `fakeSocialDeleter`, `socialTestPubID` and `decodeSocialTaskPayload`. `previewSeconds` and `waveformPeakCount` are new package-level consts in `worker`; check them against renames of `render_worker.go` (`generateWaveform`, `tahminiSure`).
   - `intp` exists in both the `social` tests and the `worker` tests. These are separate packages, so they do not clash.

_From the sosyal glossary, risk 9:_

9. **Error message and code coincidence, and the test fakes.** `ErrBulunamadi`'s text is `"bulunamadi"`, the same string as the code. When the messages are localized later, the code must still come from the handler table (`sosyalHatalari`), never from `err.Error()`. The handler tests assert only codes, never messages. Inside `sahte_depo_test.go`, the `akisCagrisi.tur` labels (`"yeni"`, `"takip"`, `"senin-icin"`, `"profil"`, `"cover"`) are compared by `servis_akis_test.go` and `servis_cover_test.go`. If they are translated, change all of those files in one step.

_From the sosyal glossary, risk 10:_

10. **Test env and DSN.** `KIMLIK_TEST_DB_URL` and the default DSN `postgres://postgres:kimlik-test-yerel@localhost:55432/...` are shared with the identity tests and CI. Keep both literals.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 15 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 3: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).

Files (27): `internal/social/cursor_test.go`, `internal/social/database_test.go`, `internal/social/fake_store_test.go`, `internal/social/integration_test.go`, `internal/social/keys_test.go`, `internal/social/profile_rules_test.go`, `internal/social/publication_rules_test.go`, `internal/social/service_comment_test.go`, `internal/social/service_cover_test.go`, `internal/social/service_feed_test.go`, `internal/social/service_follow_test.go`, `internal/social/service_preview_test.go`, `internal/social/service_profile_test.go`, `internal/social/service_publication_test.go`, `internal/social/service_report_test.go`, `internal/social/slug_test.go`, `internal/social/store_cleanup_test.go`, `internal/social/store_comment_test.go`, `internal/social/store_cover_test.go`, `internal/social/store_feed_test.go`, `internal/social/store_follow_test.go`, `internal/social/store_helpers_test.go`, `internal/social/store_preview_test.go`, `internal/social/store_profile_test.go`, `internal/social/store_publication_test.go`, `internal/social/store_report_test.go`, `internal/social/visibility_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/social/cursor_test.go internal/social/database_test.go internal/social/fake_store_test.go internal/social/integration_test.go internal/social/keys_test.go internal/social/profile_rules_test.go internal/social/publication_rules_test.go internal/social/service_comment_test.go internal/social/service_cover_test.go internal/social/service_feed_test.go internal/social/service_follow_test.go internal/social/service_preview_test.go internal/social/service_profile_test.go internal/social/service_publication_test.go internal/social/service_report_test.go internal/social/slug_test.go internal/social/store_cleanup_test.go internal/social/store_comment_test.go internal/social/store_cover_test.go internal/social/store_feed_test.go internal/social/store_follow_test.go internal/social/store_helpers_test.go internal/social/store_preview_test.go internal/social/store_profile_test.go internal/social/store_publication_test.go internal/social/store_report_test.go internal/social/visibility_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 4: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 15 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 5: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 6: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(social): test yardimcilari ve test adlari Ingilizce

social testlerinin sahte depo, yardimci ve test adlari Ingilizceye
cevrildi. Test verisi ve mesajlari ayni; kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 16: `service`

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/service/anahtar_duzeni_test.go` | `internal/service/key_layout_test.go` |
| `internal/service/canli_redis_test.go` | `internal/service/live_redis_test.go` |
| `internal/service/export_service.go` | (unchanged) |
| `internal/service/file_service.go` | (unchanged) |
| `internal/service/file_service_test.go` | (unchanged) |
| `internal/service/hesap_service.go` | `internal/service/account_service.go` |
| `internal/service/hesap_service_test.go` | `internal/service/account_service_test.go` |
| `internal/service/hesap_sosyal_test.go` | `internal/service/account_service_social_test.go` |
| `internal/service/job_kayit.go` | `internal/service/job_record.go` |
| `internal/service/lyrics_service.go` | (unchanged) |
| `internal/service/lyrics_service_test.go` | (unchanged) |
| `internal/service/master_service.go` | (unchanged) |
| `internal/service/master_service_test.go` | (unchanged) |
| `internal/service/render_service.go` | (unchanged) |
| `internal/service/sahiplik_canli_test.go` | `internal/service/ownership_live_test.go` |
| `internal/service/sonuc_tazeleme_test.go` | `internal/service/result_refresh_test.go` |
| `internal/service/upload_service.go` | (unchanged) |
| `internal/service/ws_ticket_canli_test.go` | `internal/service/ws_ticket_service_live_test.go` |
| `internal/service/ws_ticket_service.go` | (unchanged) |

**Declarations** (117 rows)

<!-- renames:16 -->
| old | new | kind | file |
|---|---|---|---|
| TaskTypeHesapTemizle | TaskTypeAccountCleanup | const (value "hesap:temizle" unchanged) | internal/service/account_service.go |
| ErrHesapYok | ErrAccountNotFound | var | internal/service/account_service.go |
| gecikmeliTemizlik | delayedCleanupDelay | const | internal/service/account_service.go |
| KimlikSilici | AccountDeleter | type (interface) | internal/service/account_service.go |
| Kuyruk | Enqueuer | type (interface; method Enqueue unchanged) | internal/service/account_service.go |
| SosyalTemizleyici | SocialCleaner | type (interface) | internal/service/account_service.go |
| HesapService | AccountService | type | internal/service/account_service.go |
| HesapService.kimlik | AccountService.identity | field | internal/service/account_service.go |
| HesapService.silici | AccountService.prefixDeleter | field | internal/service/account_service.go |
| HesapService.kuyruk | AccountService.queue | field | internal/service/account_service.go |
| HesapService.sosyal | AccountService.social | field | internal/service/account_service.go |
| NewHesapService | NewAccountService | func | internal/service/account_service.go |
| HesapService.Sil | AccountService.Delete | method | internal/service/account_service.go |
| HesapService.Temizle | AccountService.Cleanup | method | internal/service/account_service.go |
| HesapService.sosyalTemizle | AccountService.cleanupSocial | method | internal/service/account_service.go |
| jobAnahtari | jobKey | func | internal/service/job_record.go |
| jobOku | readJob | func | internal/service/job_record.go |
| jobYaz | writeJob | func | internal/service/job_record.go |
| sahipliJobOku | readOwnedJob | func | internal/service/job_record.go |
| wsBiletTTL | wsTicketTTL | const | internal/service/ws_ticket_service.go |
| ErrBiletGecersiz | ErrInvalidTicket | var | internal/service/ws_ticket_service.go |
| biletDeseni | ticketPattern | var | internal/service/ws_ticket_service.go |
| biletAnahtari | ticketKey | func | internal/service/ws_ticket_service.go |
| sahteSes | fakeAudio | type (test) | internal/service/key_layout_test.go |
| TestUploadService_AnahtarKullaniciOnekli | TestUploadService_KeyHasUserPrefix | func (test) | internal/service/key_layout_test.go |
| TestUploadService_GecersizSegmentYuklenmez | TestUploadService_InvalidSegmentNotUploaded | func (test) | internal/service/key_layout_test.go |
| silenDepo | deletingStorage | type (test; embeds fakeStorage) | internal/service/key_layout_test.go |
| silenDepo.silinen | deletingStorage.deleted | field (test) | internal/service/key_layout_test.go |
| TestUploadService_SilmeDeterministikAnahtar | TestUploadService_DeleteUsesDeterministicKey | func (test) | internal/service/key_layout_test.go |
| TestExportService_BaskasininMasteriReddedilir | TestExportService_RejectsOtherUsersMaster | func (test) | internal/service/key_layout_test.go |
| TestExportService_CiktiKullaniciOnekli | TestExportService_OutputHasUserPrefix | func (test) | internal/service/key_layout_test.go |
| TestExportService_ZipteYabanciStemReddedilir | TestExportService_RejectsForeignStemInZip | func (test) | internal/service/key_layout_test.go |
| TestMasterService_StartFinalYabanciStemReddedilir | TestMasterService_StartFinalRejectsForeignStem | func (test) | internal/service/key_layout_test.go |
| TestExportService_MockDaliSahiplikDenetler | TestExportService_MockBranchChecksOwnership | func (test) | internal/service/key_layout_test.go |
| TestService_TaninmayanKonakGecersizURL | TestService_UnknownHostIsInvalidURL | func (test) | internal/service/key_layout_test.go |
| canliRedisAdresi | liveRedisAddr | const (test) | internal/service/live_redis_test.go |
| canliRedis | liveRedis | func (test) | internal/service/live_redis_test.go |
| imzaKullanici | signUserID | const (test) | internal/service/file_service_test.go |
| ozelURL | privateURL | func (test) | internal/service/file_service_test.go |
| TestFileSign_SirayiKoruyupTazeAdresUretir | TestFileService_SignKeepsOrderAndRefreshesURLs | func (test) | internal/service/file_service_test.go |
| TestFileSign_YabanciAnahtarReddedilir | TestFileService_SignRejectsNotOwnedKey | func (test) | internal/service/file_service_test.go |
| TestFileSign_TaninmayanKonakReddedilir | TestFileService_SignRejectsUnknownHost | func (test) | internal/service/file_service_test.go |
| TestFileSign_YabanciVarsaHicImzalanmaz | TestFileService_SignSignsNothingWhenAnyForeign | func (test) | internal/service/file_service_test.go |
| TestFileSign_PublicAnahtardaExpiresYok | TestFileService_SignPublicKeyHasNoExpiry | func (test) | internal/service/file_service_test.go |
| sahteKimlik | fakeIdentity | type (test) | internal/service/account_service_test.go |
| sahteKimlik.silinen | fakeIdentity.deleted | field (test) | internal/service/account_service_test.go |
| sahteKimlik.hata | fakeIdentity.err | field (test) | internal/service/account_service_test.go |
| sahteSilici | fakeDeleter | type (test) | internal/service/account_service_test.go |
| sahteSilici.onekler | fakeDeleter.prefixes | field (test) | internal/service/account_service_test.go |
| sahteSilici.hata | fakeDeleter.err | field (test) | internal/service/account_service_test.go |
| sahteKuyruk | fakeQueue | type (test) | internal/service/account_service_test.go |
| sahteKuyruk.gorevler | fakeQueue.tasks | field (test) | internal/service/account_service_test.go |
| sahteKuyruk.secenekler | fakeQueue.options | field (test) | internal/service/account_service_test.go |
| ulasilamazRedis | unreachableRedis | func (test) | internal/service/account_service_test.go |
| hesapID | accountID | const (test) | internal/service/account_service_test.go |
| TestHesapSil_HerSeyiSilerVeIkinciTemizligiKurar | TestAccountService_DeleteRemovesAllAndSchedulesCleanup | func (test) | internal/service/account_service_test.go |
| TestHesapSil_KimlikYoksaErrHesapYok | TestAccountService_DeleteMissingIdentityIsNotFound | func (test) | internal/service/account_service_test.go |
| TestHesapSil_KimlikHatasindaR2yeDokunulmaz | TestAccountService_DeleteIdentityErrorLeavesR2Untouched | func (test) | internal/service/account_service_test.go |
| TestHesapSil_R2HatasiIstegiBozmaz | TestAccountService_DeleteR2ErrorDoesNotFailRequest | func (test) | internal/service/account_service_test.go |
| TestHesapSil_GecersizIDHicbirSeySilmez | TestAccountService_DeleteInvalidIDDeletesNothing | func (test) | internal/service/account_service_test.go |
| TestHesapTemizle_R2YoksaHataYok | TestAccountService_CleanupWithoutR2NoError | func (test) | internal/service/account_service_test.go |
| TestHesapSil_Isaret24SaatYasar | TestAccountService_DeleteMarkLives24Hours | func (test) | internal/service/account_service_test.go |
| TestHesapSil_GecikmeliTemizlikBirSaatSonra | TestAccountService_DeleteSchedulesCleanupOneHourLater | func (test) | internal/service/account_service_test.go |
| sahteSosyalTemizleyici | fakeSocialCleaner | type (test) | internal/service/account_service_social_test.go |
| sahteSosyalTemizleyici.idler | fakeSocialCleaner.ids | field (test) | internal/service/account_service_social_test.go |
| sahteSosyalTemizleyici.listeHata | fakeSocialCleaner.listErr | field (test) | internal/service/account_service_social_test.go |
| sahteSosyalTemizleyici.silinen | fakeSocialCleaner.deleted | field (test) | internal/service/account_service_social_test.go |
| sahteSosyalTemizleyici.izHata | fakeSocialCleaner.tracesErr | field (test) | internal/service/account_service_social_test.go |
| sahteSosyalTemizleyici.izlerSilinen | fakeSocialCleaner.tracesDeleted | field (test) | internal/service/account_service_social_test.go |
| sahteSosyalTemizleyici.sil | fakeSocialCleaner.deleter | field (test) | internal/service/account_service_social_test.go |
| sahteSosyalTemizleyici.izdekiOnekSayi | fakeSocialCleaner.prefixCountAtTraces | field (test) | internal/service/account_service_social_test.go |
| seciciSilici | selectiveDeleter | type (test) | internal/service/account_service_social_test.go |
| seciciSilici.onekler | selectiveDeleter.prefixes | field (test) | internal/service/account_service_social_test.go |
| seciciSilici.hataliOn | selectiveDeleter.failPrefix | field (test) | internal/service/account_service_social_test.go |
| sosyalY1 | socialPub1 | const (test) | internal/service/account_service_social_test.go |
| sosyalY2 | socialPub2 | const (test) | internal/service/account_service_social_test.go |
| TestHesapSil_SosyalOnekleriVeSatirlariSiler | TestAccountService_DeleteRemovesSocialPrefixesAndRows | func (test) | internal/service/account_service_social_test.go |
| TestHesapTemizle_R2HatasindaProfilYineSilinir | TestAccountService_CleanupDeletesProfileDespiteR2Error | func (test) | internal/service/account_service_social_test.go |
| TestHesapTemizle_IzSilmeHatasiR2yiDurdurmaz | TestAccountService_CleanupTraceErrorDoesNotStopR2 | func (test) | internal/service/account_service_social_test.go |
| TestHesapTemizle_SosyalR2HatasindaSatirlarKalir | TestAccountService_CleanupSocialR2ErrorKeepsRows | func (test) | internal/service/account_service_social_test.go |
| TestHesapTemizle_SosyalListeHatasi | TestAccountService_CleanupSocialListError | func (test) | internal/service/account_service_social_test.go |
| TestHesapTemizle_R2YokkenSosyalSatirlarSilinir | TestAccountService_CleanupWithoutR2DeletesSocialRows | func (test) | internal/service/account_service_social_test.go |
| lyricsSahteGroq | fakeLyricsGroq | func (test) | internal/service/lyrics_service_test.go |
| systemMesaji | systemMessage | func (test) | internal/service/lyrics_service_test.go |
| TestRewrite_IstenenDilinSystemPromptunuKullanir | TestLyricsService_RewriteUsesRequestedLanguagePrompt | func (test) | internal/service/lyrics_service_test.go |
| TestRewrite_DilVerilmezseIngilizce | TestLyricsService_RewriteDefaultsToEnglish | func (test) | internal/service/lyrics_service_test.go |
| TestGenerate_UretimSicakliginiKullanir | TestLyricsService_GenerateUsesGenerateTemperature | func (test) | internal/service/lyrics_service_test.go |
| isKaydet | storeJob | func (test) | internal/service/ownership_live_test.go |
| TestRenderService_BaskasininIsi404 | TestRenderService_OtherUsersJobNotFound | func (test) | internal/service/ownership_live_test.go |
| TestRenderService_BaskasiIptalEdemez | TestRenderService_OtherUserCannotCancel | func (test) | internal/service/ownership_live_test.go |
| TestRenderService_BaskasininSonucu404 | TestRenderService_OtherUsersResultNotFound | func (test) | internal/service/ownership_live_test.go |
| TestRenderService_SahipsizKayitKimseyeAcilmaz | TestRenderService_OwnerlessRecordHiddenFromAll | func (test) | internal/service/ownership_live_test.go |
| TestMasterService_BaskasininIsi404 | TestMasterService_OtherUsersJobNotFound | func (test) | internal/service/ownership_live_test.go |
| bekleyenGorevPayload | pendingTaskPayload | func (test) | internal/service/ownership_live_test.go |
| TestRenderService_StartRenderSahibiYazar | TestRenderService_StartRenderWritesOwner | func (test) | internal/service/ownership_live_test.go |
| TestMasterService_StartFinalSahibiYazar | TestMasterService_StartFinalWritesOwner | func (test) | internal/service/ownership_live_test.go |
| TestRenderService_StartRenderStemModunuYazar | TestRenderService_StartRenderWritesStemMode | func (test) | internal/service/ownership_live_test.go |
| TestRenderService_StartRenderVarsayilanMix | TestRenderService_StartRenderDefaultsToMix | func (test) | internal/service/ownership_live_test.go |
| TestRenderService_GecersizStemAyariIsYaratmaz | TestRenderService_InvalidStemSettingsCreateNoJob | func (test) | internal/service/ownership_live_test.go |
| sahteDepo | fakeStorage | type (test) | internal/service/result_refresh_test.go |
| sahteDepo.cagri | fakeStorage.calls | field (test) | internal/service/result_refresh_test.go |
| sahteDepo.yuklenen | fakeStorage.uploaded | field (test) | internal/service/result_refresh_test.go |
| sahteDepo.urlForCnt | fakeStorage.urlForCount | field (test) | internal/service/result_refresh_test.go |
| eskiKayitBytes | staleRecordBytes | func (test) | internal/service/result_refresh_test.go |
| TestMasterService_BuildResult_AdresHerOkumadaTazelenir | TestMasterService_BuildResultRefreshesURLOnEveryRead | func (test) | internal/service/result_refresh_test.go |
| TestMasterService_BuildResult_AnahtarsizKayitHata | TestMasterService_BuildResultFailsWithoutKey | func (test) | internal/service/result_refresh_test.go |
| TestRenderService_BuildResult_StemAdresleriTazelenir | TestRenderService_BuildResultRefreshesStemURLs | func (test) | internal/service/result_refresh_test.go |
| TestRenderService_BuildResult_KayittaImzaYok | TestRenderService_BuildResultRecordHasNoSignature | func (test) | internal/service/result_refresh_test.go |
| TestUploadService_TekImzalama | TestUploadService_SignsOnce | func (test) | internal/service/result_refresh_test.go |
| TestUploadService_MockAdresi_KeyFromURLIleCozulur | TestUploadService_MockURLResolvesWithKeyFromURL | func (test) | internal/service/result_refresh_test.go |
| TestUploadService_GetSignedURLMock_KeyFromURLIleCozulur | TestUploadService_GetSignedURLMockResolvesWithKeyFromURL | func (test) | internal/service/result_refresh_test.go |
| TestRenderService_BuildResultStemModunuTasir | TestRenderService_BuildResultCarriesStemMode | func (test) | internal/service/result_refresh_test.go |
| biletServisi | ticketService | func (test) | internal/service/ws_ticket_service_live_test.go |
| TestWSBilet_TekKullanimlik | TestWSTicketService_SingleUse | func (test) | internal/service/ws_ticket_service_live_test.go |
| TestWSBilet_BaskasininIsineVerilmez | TestWSTicketService_NotIssuedForOtherUsersJob | func (test) | internal/service/ws_ticket_service_live_test.go |
| TestWSBilet_BaskaIseKullanilamaz | TestWSTicketService_NotUsableForOtherJob | func (test) | internal/service/ws_ticket_service_live_test.go |
| TestWSBilet_BicimsizBiletRedisiSormadanReddedilir | TestWSTicketService_MalformedTicketRejectedWithoutRedis | func (test) | internal/service/ws_ticket_service_live_test.go |

**Wire strings to keep (must stay byte-identical)**

_From the app glossary, §4.1 asynq task types and queues:_

| file:line | literal | why |
|---|---|---|
| internal/service/hesap_service.go:20 | `"hesap:temizle"` | asynq task type; tasks already queued in Redis carry it |
| internal/service/render_service.go:18 | `"render:process"` | asynq task type |
| internal/service/render_service.go:19 | `"master:process"` | asynq task type |
| internal/service/hesap_service.go:97 | `"bakim"` | queue name |
| internal/service/render_service.go:124 | `"render"` | queue name |
| internal/service/master_service.go:114 | `"master"` | queue name |
| internal/service/sahiplik_canli_test.go:129,158,181,198 | `"render"`, `"master"` | test inspects real queue names |

_From the app glossary, §4.2 asynq task payload JSON (persisted in Redis):_

| file:line | literal | why |
|---|---|---|
| internal/service/hesap_service.go:91 | map key `"userId"` | `hesap:temizle` payload |
| internal/service/render_service.go:272-273 | `"jobId"`, `"payload"` | render task envelope |
| internal/service/master_service.go:187-188 | `"jobId"`, `"payload"` | master task envelope |
| internal/service/hesap_service_test.go:72 | tag `json:"userId"` | test decodes payload |
| internal/service/sahiplik_canli_test.go:101 | tag `json:"payload"` | test decodes envelope |

_From the app glossary, §4.3 Redis keys:_

| file:line | literal | why |
|---|---|---|
| internal/service/job_kayit.go:20 | `"job:%s"` | job record key (`job:{jobId}`) |
| internal/service/ws_ticket_service.go:39 | `"wsbilet:"` | WS ticket key prefix |
| internal/service/ws_ticket_service.go:25 | regex `` `^[A-Za-z0-9_-]{43}$` `` | ticket format contract |
| internal/handler/sahiplik_canli_test.go:32 | `"job:is1"` | test writes the real key format (test data `is1` may become `job1`) |
| internal/handler/ws_ticket_canli_test.go:25 | `"job:"` | same |
| internal/service/ws_ticket_canli_test.go:57 | `"wsbilet:*"` | test input that must be rejected |
| (indirect) hesap_service.go:84 | `hesapisaret.Anahtar(userID)` → `kimlik:silindi:<id>` | key built in deletionmark |

_From the app glossary, §4.4 HTTP routes, params, query and form fields, headers:_

| file:line | literal | why |
|---|---|---|
| internal/handler/sahiplik_canli_test.go:22 | `c.Locals("userId", ...)` | must equal the key `middleware.GetUserID` reads (R7) |

_From the app glossary, §4.5 Response JSON keys, codes and enum values:_

| file:line | literal | why |
|---|---|---|
| internal/service/export_service.go:80,102,139,161,258,272 | `"mp3"`, `"wav"` | `format` value in responses and audio-service requests |
| internal/service/lyrics_service.go:151,169 | tags `json:"drafts"`, `json:"lines"` | parse the Groq output format that the prompt asks for |
| internal/service/sonuc_tazeleme_test.go:61-67 | `"fileKey"`, `"fileUrl"`, `"expiresAt"`, `"duration"`, `"profile"`, `"peakDb"`, `"lufs"` | stored master-record JSON shape |
| internal/service/sonuc_tazeleme_test.go:160 | `"fileUrl"` | asserts the record has no URL |
| internal/handler/ws_ticket_canli_test.go:39 | `{"jobId":...}` | request body |

_From the app glossary, §4.6 R2 key layout and audio-service contract:_

| file:line | literal | why |
|---|---|---|
| internal/service/master_service.go:40 | `"previews"` | R2 key segment |
| internal/service/export_service.go:55,132,218,251,265,280 | `"exports"` | R2 key segment |
| internal/service/upload_service.go:41,72 | `"vocals"` | R2 key segment |
| internal/service/export_service.go:182,195,208 | `"stems/stem_%d.wav"`, `"vocals/vocal_%d.wav"`, `"master.wav"` | file names inside the exported ZIP (user-visible) |
| internal/service/export_service.go:65-74 | `"title"`, `"artist"`, `"album"`, `"year"` | metadata keys sent to audio-service |
| internal/service/upload_service.go:53; internal/worker/render_worker.go:377 | `"audio/wav"`, `"audio/mpeg"` | R2 content types |
| internal/service/hesap_sosyal_test.go:69,101 | `"sosyal/" + id + "/"` | R2 layout `sosyal/{id}/` |
| internal/service/*_test.go, internal/worker/*_test.go, e2e | `"vocals/…"`, `"masters/…"`, `"stems/…"`, `"exports/…"` key literals | test the R2 layout |

_From the app glossary, §4.9 User-visible messages (keep byte-identical in this refactor; the localization task owns them):_

| file:line | literal | channel |
|---|---|---|
| internal/service/render_service.go:168, master_service.go:152 | `"job not completed"` | compared by string in handler/render.go:129 and master.go:171 (R5) |
| internal/service/render_service.go:187 | `"job already completed"` | compared by string in handler/render.go:167 (R5) |

_From the app glossary, §4.10 Prompts and mock content (behavior, keep):_

| file:line | literal | why |
|---|---|---|
| internal/service/lyrics_service.go:94-105 | `"English"`, `"Turkish"`, `"French"` and the system prompt | LLM behavior; lyrics_service_test asserts `"Turkish"`/`"English"` |
| internal/service/lyrics_service.go:111-143 | generate and rewrite prompts | LLM behavior and output format |
| internal/service/lyrics_service.go:198-223 | mock lyrics | mock responses |

**Risks (from the glossaries; names in them are the old ones)**

_From the app glossary, risk R1:_

**R1. Interface methods implemented across packages.** These renames must land in the same change as the implementations:
- `service.KimlikSilici.HesapSil` → `AccountDeleter.DeleteAccount` is implemented by `*kimlik.Sunucu.HesapSil` (main.go:167 passes `kimlikSunucu`). The identity glossary must rename `Sunucu.HesapSil` → `Server.DeleteAccount`. The test fakes `fakeIdentity` (handler and service) and `nopIdentity` (worker) follow it.
- `service.SosyalTemizleyici.{YayinlariListele, KullaniciIzleriniSil, KullaniciVerisiniSil}` → `SocialCleaner.{ListPublicationIDs, DeleteUserTraces, DeleteUserData}` is implemented by `*sosyal.PostgresDepo` (main.go:167 passes `sosyalDepo`). The social glossary must use exactly these method names, or this glossary must adopt theirs.
- `hesapisaret.Denetci.Silinmis` is implemented by worker test `fakeChecker`. Its new name comes from the deletionmark glossary (I assumed `deletionmark.Checker.IsDeleted`).
- `client.OnekSilici.DeletePrefix` and `client.StorageClient`/`client.AudioProcessor` are already English. The fakes only follow type renames.

_From the app glossary, risk R5:_

**R5. Error texts that reach the client or are compared as strings.**
- Handlers pass `err.Error()` from services straight into `error.message` (render, master, export, upload, lyrics via `AIError`). Workers write `err.Error()` into the job `error` field and the WebSocket error frame. Translating these Turkish `fmt.Errorf` texts (listed in §4.9) changes user-visible output. They look like log text, but they are wire. Keep them byte-identical for now and leave them to the localization task. Only plain log lines (`log.Printf`) and test failure messages are safe to translate.
- handler/render.go:129,167 and master.go:171 compare `err.Error() == "job not completed"` and `"job already completed"`. Do not change these strings. A safer later step is sentinel errors, but that is a behavior change outside this refactor.

_From the app glossary, risk R6:_

**R6. Duplicate Redis key builders.** `master_worker.go:329,348` builds `fmt.Sprintf("job:%s", ...)` itself instead of calling `jobAnahtari`/`jobKey`. A rename there cannot break it, but anyone who "unifies" it must keep `job:` exactly. The handler test (sahiplik_canli_test.go:32) and ws_ticket_canli_test.go:25 also hard-code `"job:"`.

_From the app glossary, risk R9:_

**R9. Name collisions after renaming.**
- worker package: `fakeAudio`, `fakeStorage`, `fakeChecker` and `uploadCall` must not collide with the social worker test fakes (`sahteHazirlikDeposu`, `kopya`, `sahteKopyalayici`, `sahteAnalizci`, `sosyalSilici`, `sosyalKurulum`). The social task should use names such as `fakePreparationStore`, `copyCall`, `fakeCopier`, `fakeAnalyzer`, `socialDeleter`, and must **not** reuse `fakeStorage`, `fakeChecker`, `fakeAudio` or `uploadCall`. I named the test type `uploadCall` rather than `upload` so it cannot shadow the many local `upload`/`uploadStems` identifiers.
- service package: `fakeStorage` (from the shared `sahteDepo`) is embedded by `deletingStorage`. `liveRedis` and `testR2Cfg` exist once per package. `storeJob` was chosen over `saveJob` to avoid confusion with the `saveJob` methods.
- handler package: `liveRedis`, `asUser`, `fakeIdentity` and `nopQueue` must not be reused by the social handler tests (their fakes are `sahteSosyal`, `sosyalUygulamasi`, and so on). I dropped the `h` prefix of `hSahteKimlik`/`hBosKuyruk` because nothing in the package collides with it.
- Identically named types exist in different packages (`fakeAudio`, `fakeStorage`, `fakeIdentity`, `liveRedis`, `testR2Cfg` in service and worker or handler). That is legal Go.

_From the app glossary, risk R10:_

**R10. The comma-ok and receiver names in renamed types stay valid.** For example, `(s *HesapService)` keeps `s` and `(w *HesapWorker)` keeps `w`. `fakeIdentity` in the handler uses a value receiver `k`, which should become `f` (or `i`); the service `fakeIdentity` uses `k` and should become `f`. This is only a consistency note.

_From the app glossary, risk R11:_

**R11. Comments reference old names.** Many comments cite identifiers by name, and they must be updated together with the code: `RenderService.GetResult` and `KeyFromURL` (English), `sahipliJobOku` (in CLAUDE.md as well), `client.KullaniciAnahtari`, `HesapService.Sil`, `SosyalTemizleyici`, `hesapisaret.Denetci`. A stale comment at service/sonuc_tazeleme_test.go:150 names `TestRenderService_BuildResult_SaklananJSONdaImzaliURLYok`, but the function is `..._KayittaImzaYok`. CLAUDE.md (repo root) and docs/API.md mention `sahipliJobOku`, `HesapService.Sil`, `hesapisaret.*`, `KullaniciAnahtari` and others. Docs outside core-service Go code are outside this glossary, but the executor should update CLAUDE.md, or flag it.

_From the app glossary, risk R14:_

**R14. Behavior-asserting literals in tests.** render_style_test.go:27 asserts the exact Suno style string, which includes the Turkish test inputs `huzunlu, umutlu`. lyrics_service_test asserts `"Turkish"`/`"English"` appear in the prompt. render_worker_test.go:321 asserts `"Drum Kit"`. hesap_service_test/hesap_worker_test assert 5 user prefixes, and hesap_sosyal_test asserts `"sosyal/<id>/"`. Translating test data is optional, but it must keep producer and assertion in sync (§4.11).

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Rename the files**

```bash
git mv internal/service/anahtar_duzeni_test.go internal/service/key_layout_test.go
git mv internal/service/canli_redis_test.go internal/service/live_redis_test.go
git mv internal/service/hesap_service.go internal/service/account_service.go
git mv internal/service/hesap_service_test.go internal/service/account_service_test.go
git mv internal/service/hesap_sosyal_test.go internal/service/account_service_social_test.go
git mv internal/service/job_kayit.go internal/service/job_record.go
git mv internal/service/sahiplik_canli_test.go internal/service/ownership_live_test.go
git mv internal/service/sonuc_tazeleme_test.go internal/service/result_refresh_test.go
git mv internal/service/ws_ticket_canli_test.go internal/service/ws_ticket_service_live_test.go
```

- [ ] **Step 3: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 16 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 4: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- Ruling A7 applies in full. The `fmt.Errorf("job not completed")` and `fmt.Errorf("job already completed")` texts in `render_service.go` and `master_service.go` must not change: handlers compare them until Task 23.

Files (19): `internal/service/account_service.go`, `internal/service/account_service_social_test.go`, `internal/service/account_service_test.go`, `internal/service/export_service.go`, `internal/service/file_service.go`, `internal/service/file_service_test.go`, `internal/service/job_record.go`, `internal/service/key_layout_test.go`, `internal/service/live_redis_test.go`, `internal/service/lyrics_service.go`, `internal/service/lyrics_service_test.go`, `internal/service/master_service.go`, `internal/service/master_service_test.go`, `internal/service/ownership_live_test.go`, `internal/service/render_service.go`, `internal/service/result_refresh_test.go`, `internal/service/upload_service.go`, `internal/service/ws_ticket_service.go`, `internal/service/ws_ticket_service_live_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/service/account_service.go internal/service/account_service_social_test.go internal/service/account_service_test.go internal/service/export_service.go internal/service/file_service.go internal/service/file_service_test.go internal/service/job_record.go internal/service/key_layout_test.go internal/service/live_redis_test.go internal/service/lyrics_service.go internal/service/lyrics_service_test.go internal/service/master_service.go internal/service/master_service_test.go internal/service/ownership_live_test.go internal/service/render_service.go internal/service/result_refresh_test.go internal/service/upload_service.go internal/service/ws_ticket_service.go internal/service/ws_ticket_service_live_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 5: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 16 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 6: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 7: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(service): Ingilizce adlandirma

service paketi (HesapService -> AccountService, job_kayit.go ->
job_record.go ...) Ingilizceye cevrildi. Hata metinleri ayni (A7);
kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 17: `worker` (non-social files and shared helpers)

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/worker/hesap_worker.go` | `internal/worker/account_worker.go` |
| `internal/worker/hesap_worker_test.go` | `internal/worker/account_worker_test.go` |
| `internal/worker/master_hesap_test.go` | `internal/worker/master_worker_deletion_test.go` |
| `internal/worker/master_worker.go` | (unchanged) |
| `internal/worker/mock_adres_test.go` | `internal/worker/mock_url_test.go` |
| `internal/worker/render_style.go` | (unchanged) |
| `internal/worker/render_style_test.go` | (unchanged) |
| `internal/worker/render_worker.go` | (unchanged) |
| `internal/worker/render_worker_test.go` | (unchanged) |

**Declarations** (81 rows)

<!-- renames:17 -->
| old | new | kind | file |
|---|---|---|---|
| HesapWorker | AccountWorker | type | internal/worker/account_worker.go |
| HesapWorker.hesap | AccountWorker.accounts | field | internal/worker/account_worker.go |
| NewHesapWorker | NewAccountWorker | func | internal/worker/account_worker.go |
| (anon) yuk struct in ProcessTask | payload (field UserID + tag unchanged) | manual: local (field UserID + tag unchanged) | hesap_worker.go:22 |
| styleTavani | maxStyleLen | const | internal/worker/render_style.go |
| kes | truncate | func | internal/worker/render_style.go |
| stemBoyutTavani | maxStemSize | const | internal/worker/render_worker.go |
| errSahipSilindi | errOwnerDeleted | var (also used by sosyal_worker.go:100) | internal/worker/render_worker.go |
| sahipSilinmis | ownerDeleted | func | internal/worker/render_worker.go |
| sahipYazabilir | ownerCanWrite | func (also used by sosyal_worker.go:99) | internal/worker/render_worker.go |
| RenderWorker.indirici | RenderWorker.downloader | field | internal/worker/render_worker.go |
| RenderWorker.pollAraligi | RenderWorker.pollInterval | field | internal/worker/render_worker.go |
| RenderWorker.hesap | RenderWorker.deletionChecker | field | internal/worker/render_worker.go |
| RenderWorker.sunoIleUret | RenderWorker.generateWithSuno | method | internal/worker/render_worker.go |
| stemPlani | stemPlan | func | internal/worker/render_worker.go |
| RenderWorker.stemAktar | RenderWorker.transferStem | method | internal/worker/render_worker.go |
| tahminiSure | estimateDuration | func | internal/worker/render_worker.go |
| MasterWorker.hesap | MasterWorker.deletionChecker | field | internal/worker/master_worker.go |
| kayitSilici | recordingDeleter | type (test) | internal/worker/account_worker_test.go |
| kayitSilici.onekler | recordingDeleter.prefixes | field (test) | internal/worker/account_worker_test.go |
| bosKimlik | nopIdentity | type (test) | internal/worker/account_worker_test.go |
| TestHesapWorker_OnekleriTemizler | TestAccountWorker_CleansPrefixes | func (test) | internal/worker/account_worker_test.go |
| TestHesapWorker_BozukYukTekrarlanmaz | TestAccountWorker_MalformedPayloadNotRetried | func (test) | internal/worker/account_worker_test.go |
| sahteSes | fakeAudio | type (test) | internal/worker/master_worker_deletion_test.go |
| sahteSes.masterSayisi | fakeAudio.masterCalls | field (test) | internal/worker/master_worker_deletion_test.go |
| sahteSes.hata | fakeAudio.err | field (test) | internal/worker/master_worker_deletion_test.go |
| TestMasterWorker_SilinmisSahipIcinYuklemeYok | TestMasterWorker_NoUploadForDeletedOwner | func (test) | internal/worker/master_worker_deletion_test.go |
| TestMasterWorker_DenetimHatasindaTekrarDenenir | TestMasterWorker_CheckErrorIsRetryable | func (test) | internal/worker/master_worker_deletion_test.go |
| TestMasterWorker_SilinmemisSahipteMasterCagrilir | TestMasterWorker_CallsMasterForLiveOwner | func (test) | internal/worker/master_worker_deletion_test.go |
| TestMasterWorker_DenetciYoksaDenetimYapilmaz | TestMasterWorker_NoCheckWithoutChecker | func (test) | internal/worker/master_worker_deletion_test.go |
| TestMasterWorker_MockCiktisiCozulebilir | TestMasterWorker_MockOutputResolvable | func (test) | internal/worker/mock_url_test.go |
| TestRenderWorker_MockStemAdresiCozulebilir | TestRenderWorker_MockStemURLResolvable | func (test) | internal/worker/mock_url_test.go |
| TestBuildMixSettings_AdresTasimaz | TestMasterWorker_BuildMixSettingsCarriesNoURL | func (test) | internal/worker/mock_url_test.go |
| TestRenderWorker_MockModlari | TestRenderWorker_MockModes | func (test) | internal/worker/mock_url_test.go |
| (anon table fields) mod / odak / beklenen | mode / focus / want | manual: anon struct fields (test, local) / focus / want | mock_adres_test.go:110-112 |
| sahteSuno | fakeSuno | type (test) | internal/worker/render_worker_test.go |
| sahteSuno.uretHata | fakeSuno.generateErr | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.bekleHata | fakeSuno.waitErr | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.bolHata | fakeSuno.splitErr | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.uretimIstegi | fakeSuno.generateReq | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.uretimSayisi | fakeSuno.generateCalls | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.stemBekleHata | fakeSuno.stemWaitErr | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.bolArgs | fakeSuno.splitArgs | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.bolSayisi | fakeSuno.splitCalls | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.ayirArgs | fakeSuno.isolateArgs | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.ayirSayisi | fakeSuno.isolateCalls | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.ayirHata | fakeSuno.isolateErr | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.ayrik | fakeSuno.isolated | field (test) | internal/worker/render_worker_test.go |
| sahteSuno.ayrikBekleHata | fakeSuno.isolatedWaitErr | field (test) | internal/worker/render_worker_test.go |
| yukleme | uploadCall | type (test) | internal/worker/render_worker_test.go |
| yukleme.tip | uploadCall.contentType | field (test) | internal/worker/render_worker_test.go |
| yukleme.veri | uploadCall.data | field (test) | internal/worker/render_worker_test.go |
| sahteDepo | fakeStorage | type (test) | internal/worker/render_worker_test.go |
| sahteDepo.yuklemeler | fakeStorage.uploads | field (test) | internal/worker/render_worker_test.go |
| sesSunucusu | audioServer | func (test) | internal/worker/render_worker_test.go |
| denetciliWorker | workerWithChecker | func (test) | internal/worker/render_worker_test.go |
| hic | noProgress | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_MutluYol | TestRenderWorker_SunoHappyPath | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_UretimReddiTekrarDenenebilir | TestRenderWorker_SunoGenerateRejectionIsRetryable | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_UcretliAdimSonrasiTekrarYok | TestRenderWorker_SunoNoRetryAfterPaidStep | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_DepoYoksaSunoCagrilmaz | TestRenderWorker_SunoNotCalledWithoutStorage | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_SureYoksaTahmin | TestRenderWorker_SunoEstimatesMissingDuration | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_SahipsizIsteSunoCagrilmaz | TestRenderWorker_SunoNotCalledForOwnerlessJob | func (test) | internal/worker/render_worker_test.go |
| panikleyenDepo | panickingStorage | type (test; embeds fakeStorage) | internal/worker/render_worker_test.go |
| TestSunoIleUret_UcretSonrasiPanicTekrarDenenmez | TestRenderWorker_SunoPanicAfterChargeNotRetried | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_MixModuAyirmaYapmaz | TestRenderWorker_SunoMixModeDoesNotSplit | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_BosModMixGibi | TestRenderWorker_SunoEmptyModeActsAsMix | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_MixModuParcaAdresiYoksaTekrarYok | TestRenderWorker_SunoMixModeMissingTrackURLNoRetry | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_FocusModuIkiStem | TestRenderWorker_SunoFocusModeTwoStems | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_FocusAyirmaHatasiSkipRetry | TestRenderWorker_SunoFocusIsolateErrorSkipsRetry | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_FocusEnstrumaniEslenmiyorsaSunoCagrilmaz | TestRenderWorker_SunoFocusUnmappedInstrumentNotCalled | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_FocusAyirmaHatasiTekrarYok | TestRenderWorker_SunoFocusIsolateWaitErrorNoRetry | func (test) | internal/worker/render_worker_test.go |
| sahteDenetci | fakeChecker | type (test; also used by sosyal_worker_test.go:169,182,192) | internal/worker/render_worker_test.go |
| sahteDenetci.yanit | fakeChecker.answer | field (test) | internal/worker/render_worker_test.go |
| sahteDenetci.sorulan | fakeChecker.asked | field (test) | internal/worker/render_worker_test.go |
| hepSilinmis | alwaysDeleted | func (test; also used by sosyal_worker_test.go:169) | internal/worker/render_worker_test.go |
| TestSunoIleUret_SilinmisSahipIcinSunoCagrilmaz | TestRenderWorker_SunoNotCalledForDeletedOwner | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_DenetimHatasindaUcretOncesiTekrarDenenir | TestRenderWorker_SunoCheckErrorBeforeChargeIsRetryable | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_UcretSonrasiSilinenSahipIcinYuklemeYok | TestRenderWorker_SunoNoUploadForOwnerDeletedAfterCharge | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_HerYuklemedenOnceDenetler | TestRenderWorker_SunoChecksBeforeEveryUpload | func (test) | internal/worker/render_worker_test.go |
| TestSunoIleUret_SilinmemisSahipteDenetimHerAdimdaYapilir | TestRenderWorker_SunoChecksEveryStepForLiveOwner | func (test) | internal/worker/render_worker_test.go |

Rows whose kind starts with `manual:` are skipped by `apply_renames.py`. Do each by hand:
- An **embedded field** row needs no action. Renaming the embedded type renames the field and every selector on it. Check the result in the diff.
- A **`var _ X = (*Y)(nil)`** row needs no action. The assertion follows the renamed types. Check it.
- An **anonymous struct field**, **local** or **param** row: run `~/go/bin/gopls rename -w <file>:<line>:<col> <new>` on the declaring identifier.
- A **package** row is done by the package-move step.
- A **"methods unchanged; receiver becomes ..."** row needs no action. The methods keep their `op.*` names and follow the type rename.

**Wire strings to keep (must stay byte-identical)**

_From the app glossary, §4.2 asynq task payload JSON (persisted in Redis):_

| file:line | literal | why |
|---|---|---|
| internal/worker/hesap_worker.go:23 | tag `json:"userId"` | decodes that payload |
| internal/worker/render_worker.go:79-80 | tags `json:"jobId"`, `json:"payload"` | decode the envelope |
| internal/worker/master_worker.go:50-51 | tags `json:"jobId"`, `json:"payload"` | decode the envelope |
| internal/worker/hesap_worker_test.go:26 | `{"userId":"..."}` | test payload |

_From the app glossary, §4.3 Redis keys:_

| file:line | literal | why |
|---|---|---|
| internal/worker/master_worker.go:329,348 | `"job:%s"` | same key, built separately (see R6) |

_From the app glossary, §4.5 Response JSON keys, codes and enum values:_

| file:line | literal | why |
|---|---|---|
| internal/worker/render_worker.go:418 | `"RENDER_FAILED"` | WebSocket error code |
| internal/worker/master_worker.go:325 | `"MASTER_FAILED"` | WebSocket error code |

_From the app glossary, §4.6 R2 key layout and audio-service contract:_

| file:line | literal | why |
|---|---|---|
| internal/worker/render_worker.go:145,334,454 | `"stems"` | R2 key segment |
| internal/worker/master_worker.go:106,356 | `"masters"` | R2 key segment |
| internal/service/upload_service.go:53; internal/worker/render_worker.go:377 | `"audio/wav"`, `"audio/mpeg"` | R2 content types |
| internal/service/*_test.go, internal/worker/*_test.go, e2e | `"vocals/…"`, `"masters/…"`, `"stems/…"`, `"exports/…"` key literals | test the R2 layout |
| internal/worker/render_worker_test.go:321 | `"Drum Kit"` | Suno stem name contract |

_From the app glossary, §4.9 User-visible messages (keep byte-identical in this refactor; the localization task owns them):_

| file:line | literal | channel |
|---|---|---|
| worker/render_worker.go:92,115,123,306,311,317 | `"Invalid payload"`, `"Failed to save result"`, `"Stem URL uretilemedi: %v"`, `"Gecersiz is sahibi veya proje"` | job `error` and WS message |
| worker/render_worker.go:110 | `err.Error()` of `sunoIleUret`: :140 `"depolama yapilandirilmamis, Suno cagrilmadi: %w"`, :146 `"is sahibi/proje anahtara uygun degil, Suno cagrilmadi: %w: %w"`, :153 `"stem ayari gecersiz, Suno cagrilmadi: %w: %w"`, :161,163 `"suno cagrilmadi: %w"`, :176,194 `"music generation failed"`, :187 `"render panic"`, :208 `"mix render failed"` + `"suno parca adresi donmedi"`, :216,221 `"instrument isolation failed"`, :232,237,240 `"stem splitting failed"` + `"suno hic enstruman stem'i donmedi"`, :247 `"stem upload failed"`, :263 `"focus modu enstruman ister"`, :267 `"enstruman ayrilamaz: %s"`, :342 `"%s stem'i aktarilamadi: %w"`, :368 `"indirme durumu %d"`, :375 `"stem %d bayt tavanini asiyor"`, :28 `"is sahibinin hesabi silindi"`, :44 `"hesap durumu denetlenemedi: %w"` | job `error` and WS message (R5) |
| worker/master_worker.go:63,87,97,108,116,136,156,201,206 | `"Invalid payload"`, `"Vokal URL cozulemedi: %v"`, `"Stem URL cozulemedi: %v"`, `"Gecersiz is sahibi veya proje"`, `err.Error()` of `sahipYazabilir` (see above), `"Mastering failed: %v"`, `"Master URL uretilemedi: %v"` | job `error` and WS message |
| worker/render_worker.go:166-250, :282-289; master_worker.go:78,81,104,132,140,141,170,177-183 | progress step texts (`"Building music prompt..."`, `"Generating music..."`, …) | `currentStep` and WS progress (already English) |

_From the app glossary, §4.10 Prompts and mock content (behavior, keep):_

| file:line | literal | why |
|---|---|---|
| internal/worker/render_style.go:18-45 | `"instrumental"`, `"%d BPM"`, `" arrangement"`, `" groove"`, `"featuring "` | Suno style text; render_style_test.go:27 asserts the exact output |
| internal/worker/render_worker.go:169 | `"%s instrumental"` | Suno title |

**Risks (from the glossaries; names in them are the old ones)**

_From the app glossary, risk R2:_

**R2. Cross-file use inside packages, with files owned by other tasks:**
- handler: `istekSahibi` → `requestOwner` and `formatValidationErrors` are called from `sosyal_akis.go`, `sosyal_cover.go`, `sosyal_profil.go`, `sosyal_sikayet.go`, `sosyal_takip.go`, `sosyal_yayin.go` and `sosyal_yorum.go` (about 23 call sites). The test helper `kullaniciOlarak` → `asUser` is used by `sosyal_test.go:180` and `sosyal_yayin_test.go:56`.
- worker: `sahipYazabilir` → `ownerCanWrite` and `errSahipSilindi` → `errOwnerDeleted` are used by `sosyal_worker.go:99-100`. The test helpers `sahteDenetci` → `fakeChecker` and `hepSilinmis` → `alwaysDeleted` are used by `sosyal_worker_test.go:169,182,192`. For consistency, `SosyalWorker`'s `hesap` field should become `deletionChecker`, the same as `RenderWorker` and `MasterWorker`.
- The social-file task must use the names above, or both tasks must be applied together. If either side is applied alone, the package does not compile.

_From the app glossary, risk R5:_

**R5. Error texts that reach the client or are compared as strings.**
- Handlers pass `err.Error()` from services straight into `error.message` (render, master, export, upload, lyrics via `AIError`). Workers write `err.Error()` into the job `error` field and the WebSocket error frame. Translating these Turkish `fmt.Errorf` texts (listed in §4.9) changes user-visible output. They look like log text, but they are wire. Keep them byte-identical for now and leave them to the localization task. Only plain log lines (`log.Printf`) and test failure messages are safe to translate.
- handler/render.go:129,167 and master.go:171 compare `err.Error() == "job not completed"` and `"job already completed"`. Do not change these strings. A safer later step is sentinel errors, but that is a behavior change outside this refactor.

_From the app glossary, risk R6:_

**R6. Duplicate Redis key builders.** `master_worker.go:329,348` builds `fmt.Sprintf("job:%s", ...)` itself instead of calling `jobAnahtari`/`jobKey`. A rename there cannot break it, but anyone who "unifies" it must keep `job:` exactly. The handler test (sahiplik_canli_test.go:32) and ws_ticket_canli_test.go:25 also hard-code `"job:"`.

_From the app glossary, risk R9:_

**R9. Name collisions after renaming.**
- worker package: `fakeAudio`, `fakeStorage`, `fakeChecker` and `uploadCall` must not collide with the social worker test fakes (`sahteHazirlikDeposu`, `kopya`, `sahteKopyalayici`, `sahteAnalizci`, `sosyalSilici`, `sosyalKurulum`). The social task should use names such as `fakePreparationStore`, `copyCall`, `fakeCopier`, `fakeAnalyzer`, `socialDeleter`, and must **not** reuse `fakeStorage`, `fakeChecker`, `fakeAudio` or `uploadCall`. I named the test type `uploadCall` rather than `upload` so it cannot shadow the many local `upload`/`uploadStems` identifiers.
- service package: `fakeStorage` (from the shared `sahteDepo`) is embedded by `deletingStorage`. `liveRedis` and `testR2Cfg` exist once per package. `storeJob` was chosen over `saveJob` to avoid confusion with the `saveJob` methods.
- handler package: `liveRedis`, `asUser`, `fakeIdentity` and `nopQueue` must not be reused by the social handler tests (their fakes are `sahteSosyal`, `sosyalUygulamasi`, and so on). I dropped the `h` prefix of `hSahteKimlik`/`hBosKuyruk` because nothing in the package collides with it.
- Identically named types exist in different packages (`fakeAudio`, `fakeStorage`, `fakeIdentity`, `liveRedis`, `testR2Cfg` in service and worker or handler). That is legal Go.

_From the app glossary, risk R10:_

**R10. The comma-ok and receiver names in renamed types stay valid.** For example, `(s *HesapService)` keeps `s` and `(w *HesapWorker)` keeps `w`. `fakeIdentity` in the handler uses a value receiver `k`, which should become `f` (or `i`); the service `fakeIdentity` uses `k` and should become `f`. This is only a consistency note.

_From the app glossary, risk R14:_

**R14. Behavior-asserting literals in tests.** render_style_test.go:27 asserts the exact Suno style string, which includes the Turkish test inputs `huzunlu, umutlu`. lyrics_service_test asserts `"Turkish"`/`"English"` appear in the prompt. render_worker_test.go:321 asserts `"Drum Kit"`. hesap_service_test/hesap_worker_test assert 5 user prefixes, and hesap_sosyal_test asserts `"sosyal/<id>/"`. Translating test data is optional, but it must keep producer and assertion in sync (§4.11).

_From the sosyal glossary, risk 4:_

4. **Name collisions after the rename.**
   - In `social`: `summaryScanner` gets a method `summary()`, so its embedded value field must not also be called `summary`. That is why it is `base`.
   - `PostgresStore.getPublication` and `GetPublication` coexist (different case; this is fine).
   - Same-named type and method pairs are legal: `FollowStatus` type and `FollowStore.FollowStatus` method; `ProfileCounts`; `Preview` type and `Service.Preview`; `Profile` type and `Service.Profile`.
   - `MasterKey` is now a field on three structs (`Preparation`, `PublicationRecord`, `PreparationResult`). That is legal but easy to confuse.
   - In `handler`: `SocialService` must not collide with an imported package name. The handler does not import a package called `service` in these files. Check this if another glossary imports `internal/service` into the handler package.
   - In `worker`: `fakeCopier` / `fakeAnalyzer` might be chosen by the render/master test glossaries for `sahteSes` and similar fakes. That is why this scope uses `fakeSocialCopier`, `fakeSocialDeleter`, `socialTestPubID` and `decodeSocialTaskPayload`. `previewSeconds` and `waveformPeakCount` are new package-level consts in `worker`; check them against renames of `render_worker.go` (`generateWaveform`, `tahminiSure`).
   - `intp` exists in both the `social` tests and the `worker` tests. These are separate packages, so they do not clash.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Rename the files**

```bash
git mv internal/worker/hesap_worker.go internal/worker/account_worker.go
git mv internal/worker/hesap_worker_test.go internal/worker/account_worker_test.go
git mv internal/worker/master_hesap_test.go internal/worker/master_worker_deletion_test.go
git mv internal/worker/mock_adres_test.go internal/worker/mock_url_test.go
```

- [ ] **Step 3: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 17 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 4: Do the `manual:` rows**

Follow the rules under the table. Then run:

```bash
go build ./... && go vet ./...
```

- [ ] **Step 5: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- The shared helpers (`ownerCanWrite`, `errOwnerDeleted`, `fakeChecker`, `alwaysDeleted`) are also used by `sosyal_worker*.go`. gopls updates those callers. Their comments belong to Task 18.

Files (9): `internal/worker/account_worker.go`, `internal/worker/account_worker_test.go`, `internal/worker/master_worker.go`, `internal/worker/master_worker_deletion_test.go`, `internal/worker/mock_url_test.go`, `internal/worker/render_style.go`, `internal/worker/render_style_test.go`, `internal/worker/render_worker.go`, `internal/worker/render_worker_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/worker/account_worker.go internal/worker/account_worker_test.go internal/worker/master_worker.go internal/worker/master_worker_deletion_test.go internal/worker/mock_url_test.go internal/worker/render_style.go internal/worker/render_style_test.go internal/worker/render_worker.go internal/worker/render_worker_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 6: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 17 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 7: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 8: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(worker): Ingilizce adlandirma

worker paketinin sosyal disi dosyalari ve ortak yardimcilari
Ingilizceye cevrildi. asynq gorev adlari ve kuyruklar ayni; kablo
goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 18: `worker`: social worker

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/worker/sosyal_worker.go` | `internal/worker/social_worker.go` |
| `internal/worker/sosyal_worker_test.go` | `internal/worker/social_worker_test.go` |

**Declarations** (60 rows)

<!-- renames:18 -->
| old | new | kind | file |
|---|---|---|---|
| dalgaTepeSayisi | waveformPeakCount | const | internal/worker/social_worker.go |
| onizlemeSaniyesi | previewSeconds | const | internal/worker/social_worker.go |
| SosyalWorker | SocialWorker | type | internal/worker/social_worker.go |
| SosyalWorker.depo | SocialWorker.store | field | internal/worker/social_worker.go |
| SosyalWorker.kopyalayici | SocialWorker.copier | field | internal/worker/social_worker.go |
| SosyalWorker.analizci | SocialWorker.analyzer | field | internal/worker/social_worker.go |
| SosyalWorker.silici | SocialWorker.prefixDeleter | field | internal/worker/social_worker.go |
| SosyalWorker.hesap | SocialWorker.deletionChecker | field | internal/worker/social_worker.go |
| SosyalWorker.sonDeneme | SocialWorker.isLastAttempt | field | internal/worker/social_worker.go |
| NewSosyalWorker | NewSocialWorker | func | internal/worker/social_worker.go |
| asynqSonDeneme | asynqIsLastAttempt | func | internal/worker/social_worker.go |
| gorevYukunuCoz | decodeSocialTaskPayload | func (prefixed to avoid clashes in package worker) | internal/worker/social_worker.go |
| SosyalWorker.Hazirla | SocialWorker.Prepare | method | internal/worker/social_worker.go |
| SosyalWorker.sahipDenetle | SocialWorker.checkOwner | method | internal/worker/social_worker.go |
| SosyalWorker.basarisiz | SocialWorker.fail | method | internal/worker/social_worker.go |
| SosyalWorker.kopyala | SocialWorker.copyFiles | method | internal/worker/social_worker.go |
| SosyalWorker.kopyalaBir | SocialWorker.copyOne | method | internal/worker/social_worker.go |
| SosyalWorker.analizEt | SocialWorker.analyze | method | internal/worker/social_worker.go |
| SosyalWorker.DosyaSil | SocialWorker.DeleteFiles | method | internal/worker/social_worker.go |
| sYayin | socialTestPubID | const | internal/worker/social_worker_test.go |
| sSahip | socialTestOwnerID | const | internal/worker/social_worker_test.go |
| sahteHazirlikDeposu | fakePreparationStore | type | internal/worker/social_worker_test.go |
| sahteHazirlikDeposu.kayit | fakePreparationStore.record | field | internal/worker/social_worker_test.go |
| sahteHazirlikDeposu.okumaHata | fakePreparationStore.readErr | field | internal/worker/social_worker_test.go |
| sahteHazirlikDeposu.yayinlanan | fakePreparationStore.published | field | internal/worker/social_worker_test.go |
| sahteHazirlikDeposu.hataYapildi | fakePreparationStore.markedFailed | field | internal/worker/social_worker_test.go |
| kopya | copyCall | type | internal/worker/social_worker_test.go |
| kopya.kaynak | copyCall.src | field | internal/worker/social_worker_test.go |
| kopya.hedef | copyCall.dst | field | internal/worker/social_worker_test.go |
| sahteKopyalayici | fakeSocialCopier | type | internal/worker/social_worker_test.go |
| sahteKopyalayici.kopyalar | fakeSocialCopier.copies | field | internal/worker/social_worker_test.go |
| sahteKopyalayici.hata | fakeSocialCopier.err | field | internal/worker/social_worker_test.go |
| sahteAnalizci | fakeAnalyzer | type | internal/worker/social_worker_test.go |
| sahteAnalizci.istek | fakeAnalyzer.req | field | internal/worker/social_worker_test.go |
| sahteAnalizci.yanit | fakeAnalyzer.resp | field | internal/worker/social_worker_test.go |
| sahteAnalizci.hata | fakeAnalyzer.err | field | internal/worker/social_worker_test.go |
| tepeler | fillPeaks | func | internal/worker/social_worker_test.go |
| hazirKayit | preparingRecord | func | internal/worker/social_worker_test.go |
| sosyalKurulum | socialSetup | type | internal/worker/social_worker_test.go |
| sosyalKurulum.depo | socialSetup.store | field | internal/worker/social_worker_test.go |
| sosyalKurulum.kopya | socialSetup.copier | field | internal/worker/social_worker_test.go |
| sosyalKurulum.analiz | socialSetup.analyzer | field | internal/worker/social_worker_test.go |
| sosyalTestWorker | newSocialTestWorker | func | internal/worker/social_worker_test.go |
| hazirlaGorevi | prepareTask | func | internal/worker/social_worker_test.go |
| TestSosyalWorker_KopyalarAnalizEderYayinlar | TestSocialWorker_CopiesAnalyzesPublishes | func (test) | internal/worker/social_worker_test.go |
| TestSosyalWorker_YokSilinmisYaDaHazirOlanAtlanir | TestSocialWorker_SkipsMissingDeletedOrReady | func (test) | internal/worker/social_worker_test.go |
| TestSosyalWorker_SilinmisSahipKopyalanmazHataOlur | TestSocialWorker_DeletedOwnerNotCopiedMarkedFailed | func (test) | internal/worker/social_worker_test.go |
| TestSosyalWorker_SahipIsinOrtasindaSilinirse | TestSocialWorker_OwnerDeletedMidJob | func (test) | internal/worker/social_worker_test.go |
| TestSosyalWorker_DenetimHatasiTekrarDenenir | TestSocialWorker_CheckErrorRetried | func (test) | internal/worker/social_worker_test.go |
| TestSosyalWorker_KopyaHatasiSonDenemedeHataOlur | TestSocialWorker_CopyErrorOnLastAttemptMarksFailed | func (test) | internal/worker/social_worker_test.go |
| TestSosyalWorker_SonDenemedeSureDolarsaHataOlur | TestSocialWorker_DeadlineOnLastAttemptMarksFailed | func (test) | internal/worker/social_worker_test.go |
| TestSosyalWorker_IptalEdilenIsHataYapmaz | TestSocialWorker_CanceledJobNotMarkedFailed | func (test) | internal/worker/social_worker_test.go |
| TestSosyalWorker_AnalizHatasiYayiniDurdurmaz | TestSocialWorker_AnalyzeErrorDoesNotBlockPublish | func (test) | internal/worker/social_worker_test.go |
| TestSosyalWorker_AnalizCiktisiDenetlenir | TestSocialWorker_AnalyzeOutputValidated | func (test) | internal/worker/social_worker_test.go |
| TestSosyalWorker_R2VeAnalizYokken | TestSocialWorker_WithoutR2AndAnalyzer | func (test) | internal/worker/social_worker_test.go |
| TestSosyalWorker_BozukYukTekrarlanmaz | TestSocialWorker_MalformedPayloadNotRetried | func (test) | internal/worker/social_worker_test.go |
| sosyalSilici | fakeSocialDeleter | type | internal/worker/social_worker_test.go |
| sosyalSilici.onekler | fakeSocialDeleter.prefixes | field | internal/worker/social_worker_test.go |
| sosyalSilici.hata | fakeSocialDeleter.err | field | internal/worker/social_worker_test.go |
| TestSosyalWorker_DosyaSil | TestSocialWorker_DeleteFiles | func (test) | internal/worker/social_worker_test.go |

**Wire strings to keep (must stay byte-identical)**

_From the sosyal glossary, §4.7 Wire literals in test files (the tests pin the contract; keep them):_

| file:line | literal | why |
|---|---|---|
| worker/sosyal_worker_test.go:89-92, 116, 125-144, 288, 295, 316, 335 | `masters/…`, `stems/…`, `{"yayinId":…}`, `sosyal/{id}/master.wav`, `/stems/*.mp3`, `/onizleme.mp3`, prefix `sosyal/{id}/` | asynq payload and R2 layout |

_From the sosyal glossary, §4.8 Worker:_

| file:line | literal | why |
|---|---|---|
| worker/sosyal_worker.go:20-21 | `64`, `30` | Peak and preview-seconds contract values (numbers, not names). |
| worker/sosyal_worker.go:53 (via `sosyal.GorevYuku`) | `yayinId` | payload key |

**Risks (from the glossaries; names in them are the old ones)**

_From the sosyal glossary, risk 4:_

4. **Name collisions after the rename.**
   - In `social`: `summaryScanner` gets a method `summary()`, so its embedded value field must not also be called `summary`. That is why it is `base`.
   - `PostgresStore.getPublication` and `GetPublication` coexist (different case; this is fine).
   - Same-named type and method pairs are legal: `FollowStatus` type and `FollowStore.FollowStatus` method; `ProfileCounts`; `Preview` type and `Service.Preview`; `Profile` type and `Service.Profile`.
   - `MasterKey` is now a field on three structs (`Preparation`, `PublicationRecord`, `PreparationResult`). That is legal but easy to confuse.
   - In `handler`: `SocialService` must not collide with an imported package name. The handler does not import a package called `service` in these files. Check this if another glossary imports `internal/service` into the handler package.
   - In `worker`: `fakeCopier` / `fakeAnalyzer` might be chosen by the render/master test glossaries for `sahteSes` and similar fakes. That is why this scope uses `fakeSocialCopier`, `fakeSocialDeleter`, `socialTestPubID` and `decodeSocialTaskPayload`. `previewSeconds` and `waveformPeakCount` are new package-level consts in `worker`; check them against renames of `render_worker.go` (`generateWaveform`, `tahminiSure`).
   - `intp` exists in both the `social` tests and the `worker` tests. These are separate packages, so they do not clash.

_From the app glossary, risk R2:_

**R2. Cross-file use inside packages, with files owned by other tasks:**
- handler: `istekSahibi` → `requestOwner` and `formatValidationErrors` are called from `sosyal_akis.go`, `sosyal_cover.go`, `sosyal_profil.go`, `sosyal_sikayet.go`, `sosyal_takip.go`, `sosyal_yayin.go` and `sosyal_yorum.go` (about 23 call sites). The test helper `kullaniciOlarak` → `asUser` is used by `sosyal_test.go:180` and `sosyal_yayin_test.go:56`.
- worker: `sahipYazabilir` → `ownerCanWrite` and `errSahipSilindi` → `errOwnerDeleted` are used by `sosyal_worker.go:99-100`. The test helpers `sahteDenetci` → `fakeChecker` and `hepSilinmis` → `alwaysDeleted` are used by `sosyal_worker_test.go:169,182,192`. For consistency, `SosyalWorker`'s `hesap` field should become `deletionChecker`, the same as `RenderWorker` and `MasterWorker`.
- The social-file task must use the names above, or both tasks must be applied together. If either side is applied alone, the package does not compile.

_From the app glossary, risk R9:_

**R9. Name collisions after renaming.**
- worker package: `fakeAudio`, `fakeStorage`, `fakeChecker` and `uploadCall` must not collide with the social worker test fakes (`sahteHazirlikDeposu`, `kopya`, `sahteKopyalayici`, `sahteAnalizci`, `sosyalSilici`, `sosyalKurulum`). The social task should use names such as `fakePreparationStore`, `copyCall`, `fakeCopier`, `fakeAnalyzer`, `socialDeleter`, and must **not** reuse `fakeStorage`, `fakeChecker`, `fakeAudio` or `uploadCall`. I named the test type `uploadCall` rather than `upload` so it cannot shadow the many local `upload`/`uploadStems` identifiers.
- service package: `fakeStorage` (from the shared `sahteDepo`) is embedded by `deletingStorage`. `liveRedis` and `testR2Cfg` exist once per package. `storeJob` was chosen over `saveJob` to avoid confusion with the `saveJob` methods.
- handler package: `liveRedis`, `asUser`, `fakeIdentity` and `nopQueue` must not be reused by the social handler tests (their fakes are `sahteSosyal`, `sosyalUygulamasi`, and so on). I dropped the `h` prefix of `hSahteKimlik`/`hBosKuyruk` because nothing in the package collides with it.
- Identically named types exist in different packages (`fakeAudio`, `fakeStorage`, `fakeIdentity`, `liveRedis`, `testR2Cfg` in service and worker or handler). That is legal Go.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Rename the files**

```bash
git mv internal/worker/sosyal_worker.go internal/worker/social_worker.go
git mv internal/worker/sosyal_worker_test.go internal/worker/social_worker_test.go
```

- [ ] **Step 3: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 18 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 4: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).

Files (2): `internal/worker/social_worker.go`, `internal/worker/social_worker_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/worker/social_worker.go internal/worker/social_worker_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 5: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 18 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 6: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 7: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(worker): sosyal worker Ingilizce adlandirma

SosyalWorker -> SocialWorker, sosyal_worker.go -> social_worker.go.
Gorev adlari ve R2 anahtarlari ayni; kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 19: `handler` (non-social files and shared helpers)

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/handler/canli_redis_test.go` | `internal/handler/live_redis_test.go` |
| `internal/handler/export.go` | (unchanged) |
| `internal/handler/files.go` | (unchanged) |
| `internal/handler/files_test.go` | (unchanged) |
| `internal/handler/hesap.go` | `internal/handler/account.go` |
| `internal/handler/hesap_test.go` | `internal/handler/account_test.go` |
| `internal/handler/istek_sahibi.go` | `internal/handler/request_owner.go` |
| `internal/handler/lyrics.go` | (unchanged) |
| `internal/handler/master.go` | (unchanged) |
| `internal/handler/render.go` | (unchanged) |
| `internal/handler/sahiplik_canli_test.go` | `internal/handler/render_ownership_live_test.go` |
| `internal/handler/upload.go` | (unchanged) |
| `internal/handler/ws_ticket.go` | (unchanged) |
| `internal/handler/ws_ticket_canli_test.go` | `internal/handler/ws_ticket_live_test.go` |

**Declarations** (31 rows)

<!-- renames:19 -->
| old | new | kind | file |
|---|---|---|---|
| HesapHandler | AccountHandler | type | internal/handler/account.go |
| NewHesapHandler | NewAccountHandler | func | internal/handler/account.go |
| HesapHandler.Sil | AccountHandler.Delete | method | internal/handler/account.go |
| istekSahibi | requestOwner | func | internal/handler/request_owner.go |
| canliRedis | liveRedis | func (test) | internal/handler/live_redis_test.go |
| imzaUygulamasi | signApp | func (test) | internal/handler/files_test.go |
| imzaDurum | signStatus | func (test) | internal/handler/files_test.go |
| TestFileHandler_Imzalar | TestFileHandler_Signs | func (test) | internal/handler/files_test.go |
| TestFileHandler_Yabanci404 | TestFileHandler_Foreign404 | func (test) | internal/handler/files_test.go |
| TestFileHandler_GecersizGirdi400 | TestFileHandler_InvalidInput400 | func (test) | internal/handler/files_test.go |
| TestFileHandler_Kimliksiz401 | TestFileHandler_Unauthenticated401 | func (test) | internal/handler/files_test.go |
| hSahteKimlik | fakeIdentity | type (test) | internal/handler/account_test.go |
| hSahteKimlik.hata | fakeIdentity.err | field (test) | internal/handler/account_test.go |
| hBosKuyruk | nopQueue | type (test) | internal/handler/account_test.go |
| hesapUygulamasi | accountApp | func (test) | internal/handler/account_test.go |
| hesapSilDurum | accountDeleteStatus | func (test) | internal/handler/account_test.go |
| TestHesapHandler_Sil204 | TestAccountHandler_Delete204 | func (test) | internal/handler/account_test.go |
| TestHesapHandler_Yok404 | TestAccountHandler_NotFound404 | func (test) | internal/handler/account_test.go |
| TestHesapHandler_KimliksizIstek401 | TestAccountHandler_Unauthenticated401 | func (test) | internal/handler/account_test.go |
| TestHesapHandler_KimlikHatasi500 | TestAccountHandler_IdentityError500 | func (test) | internal/handler/account_test.go |
| kullaniciOlarak | asUser | func (test; also used by sosyal_test.go:180, sosyal_yayin_test.go:56) | internal/handler/render_ownership_live_test.go |
| renderUygulamasi | renderApp | func (test) | internal/handler/render_ownership_live_test.go |
| TestRenderHandler_BaskasininIsi404 | TestRenderHandler_OtherUsersJob404 | func (test) | internal/handler/render_ownership_live_test.go |
| TestRenderHandler_SahibiOkur | TestRenderHandler_OwnerReads | func (test) | internal/handler/render_ownership_live_test.go |
| TestRenderHandler_KimliksizIstek401 | TestRenderHandler_Unauthenticated401 | func (test) | internal/handler/render_ownership_live_test.go |
| biletIsID | ticketJobID | const (test) | internal/handler/ws_ticket_live_test.go |
| biletUygulamasi | ticketApp | func (test) | internal/handler/ws_ticket_live_test.go |
| biletAl | fetchTicket | func (test) | internal/handler/ws_ticket_live_test.go |
| TestWSTicketHandler_SahibiBiletleBaglanir | TestWSTicketHandler_OwnerConnectsWithTicket | func (test) | internal/handler/ws_ticket_live_test.go |
| TestWSTicketHandler_BaskasinaBilet404 | TestWSTicketHandler_OtherUserTicket404 | func (test) | internal/handler/ws_ticket_live_test.go |
| TestWSTicketHandler_BiletsizBaglanti401 | TestWSTicketHandler_NoTicket401 | func (test) | internal/handler/ws_ticket_live_test.go |

**Wire strings to keep (must stay byte-identical)**

_From the app glossary, §4.3 Redis keys:_

| file:line | literal | why |
|---|---|---|
| internal/handler/sahiplik_canli_test.go:32 | `"job:is1"` | test writes the real key format (test data `is1` may become `job1`) |
| internal/handler/ws_ticket_canli_test.go:25 | `"job:"` | same |

_From the app glossary, §4.4 HTTP routes, params, query and form fields, headers:_

| file:line | literal | why |
|---|---|---|
| internal/handler/render.go:84,119,157; master.go:126,161 | `c.Params("jobId")` | route param |
| internal/handler/ws_ticket.go:62 | `c.Query("ticket")`, `c.Params("jobId")` | WS query and param |
| internal/handler/upload.go:51,56,61,64 | form fields `"projectId"`, `"sectionId"`, `"takeName"`, `"file"` | multipart contract |
| internal/handler/upload.go:135-137 | `"takeId"` param, `"projectId"`, `"sectionId"` query | delete contract |
| internal/handler/upload.go:78 | `"Content-Type"` | header |
| internal/handler/upload.go:80-88 | the 9 audio MIME types | accepted upload types |
| internal/handler/sahiplik_canli_test.go:22 | `c.Locals("userId", ...)` | must equal the key `middleware.GetUserID` reads (R7) |

_From the app glossary, §4.5 Response JSON keys, codes and enum values:_

| file:line | literal | why |
|---|---|---|
| internal/handler/upload.go:72-73,93 | `"maxSize"`, `"fileSize"`, `"contentType"` | `error.details` keys |
| internal/handler/lyrics.go:92 | `e.Field()` → Go field names of `model` request structs | `error.details` keys are Go field names (R4) |
| internal/handler/files_test.go:41-64 | `"urls"`, `"files"` | request and response keys |
| internal/handler/ws_ticket_canli_test.go:39 | `{"jobId":...}` | request body |

_From the app glossary, §4.8 Swagger annotations (generate docs/):_

| file:line | literal | why |
|---|---|---|
| internal/handler/hesap.go:28 | `@Router /api/account [delete]` |  |
| internal/handler/ws_ticket.go:35 | `@Router /api/ws/ticket [post]` |  |
| internal/handler/files.go:36 | `@Router /api/files/sign [post]` |  |
| internal/handler/lyrics.go:36,68 | `@Router /api/lyrics/generate`, `/api/lyrics/rewrite [post]` |  |
| internal/handler/render.go:38,77,112,150 | `@Router /api/render/start`, `/status/{jobId}`, `/result/{jobId}`, `/cancel/{jobId}` |  |
| internal/handler/master.go:39,77,119,154 | `@Router /api/master/preview`, `/final`, `/status/{jobId}`, `/result/{jobId}` |  |
| internal/handler/export.go:40,84,128 | `@Router /api/export/mp3`, `/wav`, `/stems [post]` |  |
| internal/handler/upload.go:43,129 | `@Router /api/upload/vocal [post]`, `/api/upload/vocal/{takeId} [delete]` |  |
| internal/handler/*.go | `@Param` names (`jobId`, `takeId`, `projectId`, `sectionId`, `takeName`, `file`, `request`), `@Tags`, the `model.*`/`response.*` type refs | the type refs follow any model rename (R12) |

_From the app glossary, §4.9 User-visible messages (keep byte-identical in this refactor; the localization task owns them):_

| file:line | literal | channel |
|---|---|---|
| handler/*.go (16 sites: export.go:44,88,132; files.go:40; hesap.go:32; master.go:43,81,123,158; render.go:42,81,116,154; upload.go:47,133; ws_ticket.go:39) | `"Kimlik dogrulanamadi"` | 401 message |
| handler/hesap.go:36 | `"Hesap bulunamadi"` | 404 message |
| handler/hesap.go:38 | `"Hesap silinemedi"` | 500 message |
| handler/ws_ticket.go:54 / :64 / :67 | `"Bilet uretilemedi"` / `"Gecersiz WebSocket bileti"` / `"Bilet denetlenemedi"` | 500 / 401 / 500 |
| handler/files.go:57 | `"Adres uretilemedi"` | 500 |
| handler/export.go:62,106,150; files.go:55; master.go:99 | `"Gecersiz dosya URL'si"` | 400 |
| handler English messages (export, files, lyrics, master, render, upload, ws_ticket) | `"Invalid request body"`, `"Validation failed"`, `"Job not found"`, `"File not found"`, `"Job ID is required"`, `"Job not completed yet"`, `"Job already completed"`, `"projectId is required"`, `"sectionId is required"`, `"File is required"`, `"File size exceeds 50MB limit"`, `"Invalid file type. Supported: WAV, M4A, MP3, AAC"`, `"Failed to open file"`, `"Invalid projectId or sectionId"`, `"takeId, projectId and sectionId are required"`, `"Invalid takeId, projectId or sectionId"` | already English |
| render.go:57,59,94,132,170; master.go:57,101,136,174; export.go:64,108,152; upload.go:110,146; lyrics.go:49,81 | `err.Error()` passed into the message | **service error texts reach the client** (R5). Affected strings: master_service.go:62 `"stem URL: %w"`, :67 `"vokal URL: %w"`, :168 `"is kaydinda nesne anahtari yok"`, :173 `"master URL uretilemedi: %w"`; render_service.go:46 `"stem kaydinda nesne anahtari yok: %s"`, :50 `"stem URL uretilemedi: %w"`; export_service.go:46,123 `"master URL cozulemedi: %w"`, :178,191,204 `"zip girdisi cozulemedi: %w"`, :96,155,238 `"export URL uretilemedi: %w"`; upload_service.go:55 `"vokal yuklenemedi: %w"`; plus the English ones in render/master/lyrics services |

**Risks (from the glossaries; names in them are the old ones)**

_From the app glossary, risk R2:_

**R2. Cross-file use inside packages, with files owned by other tasks:**
- handler: `istekSahibi` → `requestOwner` and `formatValidationErrors` are called from `sosyal_akis.go`, `sosyal_cover.go`, `sosyal_profil.go`, `sosyal_sikayet.go`, `sosyal_takip.go`, `sosyal_yayin.go` and `sosyal_yorum.go` (about 23 call sites). The test helper `kullaniciOlarak` → `asUser` is used by `sosyal_test.go:180` and `sosyal_yayin_test.go:56`.
- worker: `sahipYazabilir` → `ownerCanWrite` and `errSahipSilindi` → `errOwnerDeleted` are used by `sosyal_worker.go:99-100`. The test helpers `sahteDenetci` → `fakeChecker` and `hepSilinmis` → `alwaysDeleted` are used by `sosyal_worker_test.go:169,182,192`. For consistency, `SosyalWorker`'s `hesap` field should become `deletionChecker`, the same as `RenderWorker` and `MasterWorker`.
- The social-file task must use the names above, or both tasks must be applied together. If either side is applied alone, the package does not compile.

_From the app glossary, risk R4:_

**R4. Validation `details` keys are Go field names.** `formatValidationErrors` (handler/lyrics.go:88-97) uses `e.Field()`, and no `RegisterTagNameFunc` is set anywhere. The keys in `error.details` for `VALIDATION_ERROR` are therefore the **Go struct field names** of `model` request types (for example `ProjectID`, `StemURLs`, `MasterFileURL`, `JobID`, `URLs`). All of them are English today. A model-glossary change to any validated field name (casing included) would change the wire. Model fields carrying a `validate` tag: Album, Arrangement, Artist, Bars, BitDepth, BPM, Brief, Channels, Credits, CurrentLyrics, Density, Emphasis, FileURL, FocusInstrument, Format, Genre, Groove, ID, Instructions, Instruments, JobID, Key, Language, MasterFileURL, MasterURL, Max, Metadata, Min, MixSnapshot, Mode, OffsetMs, Preset, PreviewStartTime, Profile, ProjectID, Quality, SampleRate, Scale, SectionEmphasis, SectionID, SectionType, StemID, StemMode, StemURLs, Structure, TakeID, Title, Tonic, Type, URLs, Value, Vibes, VocalTakes, VocalURLs, VolumeDb, Year.

_From the app glossary, risk R5:_

**R5. Error texts that reach the client or are compared as strings.**
- Handlers pass `err.Error()` from services straight into `error.message` (render, master, export, upload, lyrics via `AIError`). Workers write `err.Error()` into the job `error` field and the WebSocket error frame. Translating these Turkish `fmt.Errorf` texts (listed in §4.9) changes user-visible output. They look like log text, but they are wire. Keep them byte-identical for now and leave them to the localization task. Only plain log lines (`log.Printf`) and test failure messages are safe to translate.
- handler/render.go:129,167 and master.go:171 compare `err.Error() == "job not completed"` and `"job already completed"`. Do not change these strings. A safer later step is sentinel errors, but that is a behavior change outside this refactor.

_From the app glossary, risk R7:_

**R7. String-keyed Fiber locals.** handler/sahiplik_canli_test.go:22 writes `c.Locals("userId", ...)` by string, and `middleware.GetUserID` must read the same key. If the middleware glossary renames that locals key or wraps it in a constant, this test fake must follow. The value is not on the wire, but it is a string-based lookup.

_From the app glossary, risk R9:_

**R9. Name collisions after renaming.**
- worker package: `fakeAudio`, `fakeStorage`, `fakeChecker` and `uploadCall` must not collide with the social worker test fakes (`sahteHazirlikDeposu`, `kopya`, `sahteKopyalayici`, `sahteAnalizci`, `sosyalSilici`, `sosyalKurulum`). The social task should use names such as `fakePreparationStore`, `copyCall`, `fakeCopier`, `fakeAnalyzer`, `socialDeleter`, and must **not** reuse `fakeStorage`, `fakeChecker`, `fakeAudio` or `uploadCall`. I named the test type `uploadCall` rather than `upload` so it cannot shadow the many local `upload`/`uploadStems` identifiers.
- service package: `fakeStorage` (from the shared `sahteDepo`) is embedded by `deletingStorage`. `liveRedis` and `testR2Cfg` exist once per package. `storeJob` was chosen over `saveJob` to avoid confusion with the `saveJob` methods.
- handler package: `liveRedis`, `asUser`, `fakeIdentity` and `nopQueue` must not be reused by the social handler tests (their fakes are `sahteSosyal`, `sosyalUygulamasi`, and so on). I dropped the `h` prefix of `hSahteKimlik`/`hBosKuyruk` because nothing in the package collides with it.
- Identically named types exist in different packages (`fakeAudio`, `fakeStorage`, `fakeIdentity`, `liveRedis`, `testR2Cfg` in service and worker or handler). That is legal Go.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Rename the files**

```bash
git mv internal/handler/canli_redis_test.go internal/handler/live_redis_test.go
git mv internal/handler/hesap.go internal/handler/account.go
git mv internal/handler/hesap_test.go internal/handler/account_test.go
git mv internal/handler/istek_sahibi.go internal/handler/request_owner.go
git mv internal/handler/sahiplik_canli_test.go internal/handler/render_ownership_live_test.go
git mv internal/handler/ws_ticket_canli_test.go internal/handler/ws_ticket_live_test.go
```

- [ ] **Step 3: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 19 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 4: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- `requestOwner` and `asUser` (shared helpers) are also called from `sosyal*.go`. gopls updates those callers. Their comments belong to Task 20.
- Swag annotations: keep every `// @...` line as it is.

Files (14): `internal/handler/account.go`, `internal/handler/account_test.go`, `internal/handler/export.go`, `internal/handler/files.go`, `internal/handler/files_test.go`, `internal/handler/live_redis_test.go`, `internal/handler/lyrics.go`, `internal/handler/master.go`, `internal/handler/render.go`, `internal/handler/render_ownership_live_test.go`, `internal/handler/request_owner.go`, `internal/handler/upload.go`, `internal/handler/ws_ticket.go`, `internal/handler/ws_ticket_live_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/handler/account.go internal/handler/account_test.go internal/handler/export.go internal/handler/files.go internal/handler/files_test.go internal/handler/live_redis_test.go internal/handler/lyrics.go internal/handler/master.go internal/handler/render.go internal/handler/render_ownership_live_test.go internal/handler/request_owner.go internal/handler/upload.go internal/handler/ws_ticket.go internal/handler/ws_ticket_live_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 5: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 19 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 6: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 7: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(handler): Ingilizce adlandirma

handler paketinin sosyal disi dosyalari ve ortak yardimcilari
(istekSahibi -> requestOwner, kullaniciOlarak -> asUser) Ingilizceye
cevrildi. Rotalar, kodlar ve mesajlar ayni; kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 20: `handler`: social handlers

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `internal/handler/sosyal.go` | `internal/handler/social.go` |
| `internal/handler/sosyal_akis.go` | `internal/handler/social_feed.go` |
| `internal/handler/sosyal_akis_test.go` | `internal/handler/social_feed_test.go` |
| `internal/handler/sosyal_cover.go` | `internal/handler/social_cover.go` |
| `internal/handler/sosyal_cover_test.go` | `internal/handler/social_cover_test.go` |
| `internal/handler/sosyal_onizleme.go` | `internal/handler/social_preview.go` |
| `internal/handler/sosyal_onizleme_test.go` | `internal/handler/social_preview_test.go` |
| `internal/handler/sosyal_profil.go` | `internal/handler/social_profile.go` |
| `internal/handler/sosyal_sikayet.go` | `internal/handler/social_report.go` |
| `internal/handler/sosyal_sikayet_test.go` | `internal/handler/social_report_test.go` |
| `internal/handler/sosyal_takip.go` | `internal/handler/social_follow.go` |
| `internal/handler/sosyal_takip_test.go` | `internal/handler/social_follow_test.go` |
| `internal/handler/sosyal_test.go` | `internal/handler/social_test.go` |
| `internal/handler/sosyal_yayin.go` | `internal/handler/social_publication.go` |
| `internal/handler/sosyal_yayin_test.go` | `internal/handler/social_publication_test.go` |
| `internal/handler/sosyal_yorum.go` | `internal/handler/social_comment.go` |
| `internal/handler/sosyal_yorum_test.go` | `internal/handler/social_comment_test.go` |

**Declarations** (125 rows)

<!-- renames:20 -->
| old | new | kind | file |
|---|---|---|---|
| SosyalServisi | SocialService | type (interface) | internal/handler/social.go |
| _ (var _ SosyalServisi = (*sosyal.Servis)(nil)) | var _ SocialService = (*social.Service)(nil) | manual: var _ SocialService = (*social.Service)(nil) | internal/handler/social.go |
| SosyalHandler | SocialHandler | type | internal/handler/social.go |
| SosyalHandler.servis | SocialHandler.service | field | internal/handler/social.go |
| NewSosyalHandler | NewSocialHandler | func | internal/handler/social.go |
| SosyalLimitleri | SocialLimits | type | internal/handler/social.go |
| SosyalLimitleri.Yayin | SocialLimits.Publication | field | internal/handler/social.go |
| SosyalLimitleri.Yorum | SocialLimits.Comment | field | internal/handler/social.go |
| SosyalLimitleri.Cover | SocialLimits.Cover | field (unchanged) | internal/handler/social.go |
| SosyalHandler.Rotalar | SocialHandler.Routes | method | internal/handler/social.go |
| sosyalHatalari | socialErrors | var | internal/handler/social.go |
| sosyalHatalari[].hata | socialErrors[].err | manual: field (anonymous struct) [].err | internal/handler/social.go |
| sosyalHatalari[].durum | socialErrors[].status | manual: field (anonymous struct) [].status | internal/handler/social.go |
| sosyalHatalari[].kod | socialErrors[].code | manual: field (anonymous struct) [].code | internal/handler/social.go |
| sosyalHata | socialError | func | internal/handler/social.go |
| govdeHatasi | badBodyError | func | internal/handler/social.go |
| SosyalHandler.Akis | SocialHandler.Feed | method | internal/handler/social_feed.go |
| SosyalHandler.ProfilYayinlari | SocialHandler.ProfilePublications | method | internal/handler/social_feed.go |
| SosyalHandler.Begen | SocialHandler.Like | method | internal/handler/social_feed.go |
| SosyalHandler.BegeniyiKaldir | SocialHandler.Unlike | method | internal/handler/social_feed.go |
| SosyalHandler.begeni | SocialHandler.setLike | method | internal/handler/social_feed.go |
| SosyalHandler.Coverlar | SocialHandler.Covers | method | internal/handler/social_cover.go |
| SosyalHandler.CoverBaslat | SocialHandler.StartCover | method | internal/handler/social_cover.go |
| SosyalHandler.PublicRotalar | SocialHandler.PublicRoutes | method | internal/handler/social_preview.go |
| SosyalHandler.Onizleme | SocialHandler.Preview | method | internal/handler/social_preview.go |
| SosyalHandler.ProfilBen | SocialHandler.MyProfile | method | internal/handler/social_profile.go |
| SosyalHandler.ProfilKaydet | SocialHandler.SaveProfile | method | internal/handler/social_profile.go |
| SosyalHandler.AdUygun | SocialHandler.CheckUsername | method | internal/handler/social_profile.go |
| SosyalHandler.Profil | SocialHandler.Profile | method | internal/handler/social_profile.go |
| SosyalHandler.Sikayet | SocialHandler.Report | method | internal/handler/social_report.go |
| SosyalHandler.TakipEt | SocialHandler.Follow | method | internal/handler/social_follow.go |
| SosyalHandler.TakibiBirak | SocialHandler.Unfollow | method | internal/handler/social_follow.go |
| SosyalHandler.takip | SocialHandler.setFollow | method | internal/handler/social_follow.go |
| SosyalHandler.Oneriler | SocialHandler.Suggestions | method | internal/handler/social_follow.go |
| SosyalHandler.YayinOlustur | SocialHandler.CreatePublication | method | internal/handler/social_publication.go |
| SosyalHandler.Yayin | SocialHandler.Publication | method | internal/handler/social_publication.go |
| SosyalHandler.Slug | SocialHandler.PublicationBySlug | method | internal/handler/social_publication.go |
| SosyalHandler.YayinGuncelle | SocialHandler.UpdatePublication | method | internal/handler/social_publication.go |
| SosyalHandler.YayinSil | SocialHandler.DeletePublication | method | internal/handler/social_publication.go |
| SosyalHandler.TekrarDene | SocialHandler.Retry | method | internal/handler/social_publication.go |
| SosyalHandler.Dosyalar | SocialHandler.Files | method | internal/handler/social_publication.go |
| SosyalHandler.Yorumlar | SocialHandler.Comments | method | internal/handler/social_comment.go |
| SosyalHandler.YorumYaz | SocialHandler.PostComment | method | internal/handler/social_comment.go |
| SosyalHandler.YorumSil | SocialHandler.DeleteComment | method | internal/handler/social_comment.go |
| sosyalKullanici | socialTestUser | const | internal/handler/social_test.go |
| sahteSosyal | fakeSocialService | type | internal/handler/social_test.go |
| sahteSosyal.hata | fakeSocialService.err | field | internal/handler/social_test.go |
| sahteSosyal.profil | fakeSocialService.profile | field | internal/handler/social_test.go |
| sahteSosyal.adUygunlugu | fakeSocialService.availability | field | internal/handler/social_test.go |
| sahteSosyal.yayinYaniti | fakeSocialService.pubResponse | field | internal/handler/social_test.go |
| sahteSosyal.detay | fakeSocialService.detail | field | internal/handler/social_test.go |
| sahteSosyal.dosyalar | fakeSocialService.files | field | internal/handler/social_test.go |
| sahteSosyal.sayfa | fakeSocialService.page | field | internal/handler/social_test.go |
| sahteSosyal.begeni | fakeSocialService.like | field | internal/handler/social_test.go |
| sahteSosyal.takip | fakeSocialService.follow | field | internal/handler/social_test.go |
| sahteSosyal.oneriler | fakeSocialService.suggestions | field | internal/handler/social_test.go |
| sahteSosyal.yorum | fakeSocialService.comment | field | internal/handler/social_test.go |
| sahteSosyal.yorumSayfa | fakeSocialService.commentPage | field | internal/handler/social_test.go |
| sahteSosyal.cover | fakeSocialService.cover | field (unchanged) | internal/handler/social_test.go |
| sahteSosyal.onizleme | fakeSocialService.preview | field | internal/handler/social_test.go |
| sahteSosyal.sonKullanici | fakeSocialService.lastUser | field | internal/handler/social_test.go |
| sahteSosyal.sonAd | fakeSocialService.lastUsername | field | internal/handler/social_test.go |
| sahteSosyal.sonProfil | fakeSocialService.lastProfile | field | internal/handler/social_test.go |
| sahteSosyal.sonYayin | fakeSocialService.lastID | field (holds pub id, slug or comment id) | internal/handler/social_test.go |
| sahteSosyal.sonIstek | fakeSocialService.lastRequest | field | internal/handler/social_test.go |
| sahteSosyal.sonYama | fakeSocialService.lastPatch | field | internal/handler/social_test.go |
| sahteSosyal.sonSekme | fakeSocialService.lastTab | field | internal/handler/social_test.go |
| sahteSosyal.sonTur | fakeSocialService.lastKind | field | internal/handler/social_test.go |
| sahteSosyal.sonImlec | fakeSocialService.lastCursor | field | internal/handler/social_test.go |
| sahteSosyal.sonLimit | fakeSocialService.lastLimit | field | internal/handler/social_test.go |
| sahteSosyal.sonBegen | fakeSocialService.lastLike | field | internal/handler/social_test.go |
| sahteSosyal.sonTakip | fakeSocialService.lastFollow | field | internal/handler/social_test.go |
| sahteSosyal.sonYorum | fakeSocialService.lastComment | field | internal/handler/social_test.go |
| sahteSosyal.sonSikayet | fakeSocialService.lastReport | field | internal/handler/social_test.go |
| gecir | passThrough | func | internal/handler/social_test.go |
| sosyalUygulamasi | newSocialApp | func | internal/handler/social_test.go |
| sosyalUygulamasiLimitli | newSocialAppWithLimits | func | internal/handler/social_test.go |
| sosyalIstek | doSocialRequest | func | internal/handler/social_test.go |
| hataKodu | errorCode | func | internal/handler/social_test.go |
| fmtDogrulama | wrapValidation | func | internal/handler/social_test.go |
| TestSosyal_ProfilBenYoksa404ProfilYok | TestSocialHandler_MyProfileMissing404NoProfile | func (test) | internal/handler/social_test.go |
| TestSosyal_ProfilBenSozlesmeAlanlari | TestSocialHandler_MyProfileContractFields | func (test) | internal/handler/social_test.go |
| TestSosyal_ProfilKaydetHataKodlari | TestSocialHandler_SaveProfileErrorCodes | func (test) | internal/handler/social_test.go |
| TestSosyal_ProfilKaydetGovdeyiIletir | TestSocialHandler_SaveProfileForwardsBody | func (test) | internal/handler/social_test.go |
| TestSosyal_BozukGovde422 | TestSocialHandler_MalformedBody422 | func (test) | internal/handler/social_test.go |
| TestSosyal_AdUygunOneriNullVeyaDolu | TestSocialHandler_CheckUsernameSuggestionNullOrSet | func (test) | internal/handler/social_test.go |
| TestSosyal_ProfilYok404Bulunamadi | TestSocialHandler_ProfileMissing404NotFound | func (test) | internal/handler/social_test.go |
| TestSosyal_KimliksizIstek401 | TestSocialHandler_Unauthenticated401 | func (test) | internal/handler/social_test.go |
| TestSosyal_AkisSayfaZarfi | TestSocialHandler_FeedPageEnvelope | func (test) | internal/handler/social_feed_test.go |
| TestSosyal_AkisSonSayfaImlecNull | TestSocialHandler_FeedLastPageCursorNull | func (test) | internal/handler/social_feed_test.go |
| TestSosyal_AkisGecersizSekme422 | TestSocialHandler_FeedInvalidTab422 | func (test) | internal/handler/social_feed_test.go |
| TestSosyal_ProfilYayinlari | TestSocialHandler_ProfilePublications | func (test) | internal/handler/social_feed_test.go |
| TestSosyal_BegeniPutVeDelete | TestSocialHandler_LikePutAndDelete | func (test) | internal/handler/social_feed_test.go |
| TestSosyal_CoverBaslatSozlesme | TestSocialHandler_StartCoverContract | func (test) | internal/handler/social_cover_test.go |
| TestSosyal_CoverHataKodlari | TestSocialHandler_CoverErrorCodes | func (test) | internal/handler/social_cover_test.go |
| TestSosyal_CoverlarSayfasiVeLimit | TestSocialHandler_CoversPageAndLimit | func (test) | internal/handler/social_cover_test.go |
| publicUygulama | newPublicApp | func | internal/handler/social_preview_test.go |
| TestSosyal_OnizlemeKimliksizVeSozlesme | TestSocialHandler_PreviewUnauthenticatedContract | func (test) | internal/handler/social_preview_test.go |
| TestSosyal_Onizleme404JSON | TestSocialHandler_Preview404JSON | func (test) | internal/handler/social_preview_test.go |
| TestSosyal_OnizlemeIPLimitiUygulanir | TestSocialHandler_PreviewIPLimitApplied | func (test) | internal/handler/social_preview_test.go |
| TestSosyal_OnizlemeNoStoreHerYanitta | TestSocialHandler_PreviewNoStoreOnEveryResponse | func (test) | internal/handler/social_preview_test.go |
| panikBekle | expectPanic | func | internal/handler/social_preview_test.go |
| TestSosyal_EksikLimiterKayittaPanikEder | TestSocialHandler_MissingLimiterPanicsAtRegistration | func (test) | internal/handler/social_preview_test.go |
| TestSosyal_Sikayet201Govdesiz | TestSocialHandler_Report201NoBody | func (test) | internal/handler/social_report_test.go |
| TestSosyal_SikayetHatalari | TestSocialHandler_ReportErrors | func (test) | internal/handler/social_report_test.go |
| TestSosyal_TakipYaniti | TestSocialHandler_FollowResponse | func (test) | internal/handler/social_follow_test.go |
| TestSosyal_KendiniTakip422 | TestSocialHandler_SelfFollow422 | func (test) | internal/handler/social_follow_test.go |
| TestSosyal_TakipProfilYok404 | TestSocialHandler_FollowMissingProfile404 | func (test) | internal/handler/social_follow_test.go |
| TestSosyal_OnerilerDizi | TestSocialHandler_SuggestionsArray | func (test) | internal/handler/social_follow_test.go |
| sosyalHamIstek | doSocialRawRequest | func | internal/handler/social_follow_test.go |
| ornekDetay | sampleDetail | func | internal/handler/social_publication_test.go |
| TestSosyal_YayinOlustur202 | TestSocialHandler_CreatePublication202 | func (test) | internal/handler/social_publication_test.go |
| TestSosyal_YayinOlusturHataKodlari | TestSocialHandler_CreatePublicationErrorCodes | func (test) | internal/handler/social_publication_test.go |
| TestSosyal_YayinOlusturHizSiniri | TestSocialHandler_CreatePublicationRateLimit | func (test) | internal/handler/social_publication_test.go |
| TestSosyal_YayinDetaySozlesmeAlanlari | TestSocialHandler_PublicationDetailContractFields | func (test) | internal/handler/social_publication_test.go |
| TestSosyal_SlugVeYok404 | TestSocialHandler_SlugAndMissing404 | func (test) | internal/handler/social_publication_test.go |
| TestSosyal_YayinGuncelleYalnizcaGonderilenAlanlar | TestSocialHandler_UpdatePublicationOnlySentFields | func (test) | internal/handler/social_publication_test.go |
| TestSosyal_YayinSil204Ve404 | TestSocialHandler_DeletePublication204And404 | func (test) | internal/handler/social_publication_test.go |
| TestSosyal_TekrarDene202Ve409 | TestSocialHandler_Retry202And409 | func (test) | internal/handler/social_publication_test.go |
| TestSosyal_DosyalarMasterURL | TestSocialHandler_FilesMasterURL | func (test) | internal/handler/social_publication_test.go |
| TestSosyal_YorumYaz201VeSozlesme | TestSocialHandler_PostComment201Contract | func (test) | internal/handler/social_comment_test.go |
| TestSosyal_YorumHataKodlari | TestSocialHandler_CommentErrorCodes | func (test) | internal/handler/social_comment_test.go |
| TestSosyal_YorumlarSayfasi | TestSocialHandler_CommentsPage | func (test) | internal/handler/social_comment_test.go |
| TestSosyal_YorumSil | TestSocialHandler_DeleteComment | func (test) | internal/handler/social_comment_test.go |
| TestSosyal_YorumLimitiYalnizcaYazmada | TestSocialHandler_CommentLimitOnlyOnPost | func (test) | internal/handler/social_comment_test.go |

Rows whose kind starts with `manual:` are skipped by `apply_renames.py`. Do each by hand:
- An **embedded field** row needs no action. Renaming the embedded type renames the field and every selector on it. Check the result in the diff.
- A **`var _ X = (*Y)(nil)`** row needs no action. The assertion follows the renamed types. Check it.
- An **anonymous struct field**, **local** or **param** row: run `~/go/bin/gopls rename -w <file>:<line>:<col> <new>` on the declaring identifier.
- A **package** row is done by the package-move step.
- A **"methods unchanged; receiver becomes ..."** row needs no action. The methods keep their `op.*` names and follow the type rename.

**Wire strings to keep (must stay byte-identical)**

_From the sosyal glossary, §4.5 HTTP routes, params, query keys, headers, error codes (handler):_

| file:line | literal | why |
|---|---|---|
| handler/sosyal.go:75-103 | `/profil/ben`, `/profil/kullanici-adi/uygun`, `/profiller/:ad`, `/profiller/:ad/yayinlar`, `/akis`, `/yayinlar`, `/yayinlar/:id`, `/yayinlar/:id/tekrar-dene`, `/yayinlar/:id/dosyalar`, `/s/:slug`, `/yayinlar/:id/begeni`, `/profiller/:ad/takip`, `/oneriler`, `/yayinlar/:id/yorumlar`, `/yorumlar/:id`, `/sikayetler`, `/yayinlar/:id/coverlar`, `/yayinlar/:id/cover-baslat` | URL paths and route params |
| handler/sosyal_onizleme.go:17 | `/s/:slug` | public route |
| handler/sosyal_onizleme.go:23 | `"no-store"` (`Cache-Control`) | HTTP header value |
| handler/sosyal_akis.go:15, 28-29, 51; sosyal_cover.go:16, 30; sosyal_profil.go:51, 64; sosyal_takip.go:23; sosyal_yayin.go:33, 46, 63, 76, 88, 101; sosyal_yorum.go:15, 33, 46; sosyal_onizleme.go:31 | `c.Query("sekme"/"imlec"/"limit"/"tur"/"ad")`, `c.Params("id"/"ad"/"slug")` | query and route param names |
| handler/sosyal.go:113-125 | `"bulunamadi"`, `"profil_yok"`, `"gecersiz_kullanici_adi"`, `"kullanici_adi_alinmis"`, `"dogrulama"`, `"sahiplik"`, `"kaynak_bulunamadi"`, `"altyapi_kapali"`, `"stem_yok"`, `"durum"`, `"kendini_takip"`, `"yorum_kapali"`, `"yetki"` | error codes |
| handler/sosyal.go:141 | `"sunucu_hatasi"` | error code |
| handler/sosyal.go:147 | `"dogrulama"` | error code |
| handler/sosyal_profil.go:20 | `"profil_yok"` | error code (404 variant) |

_From the sosyal glossary, §4.6 Message text: NOT wire, to be localized later:_

| file:line | literal | note |
|---|---|---|
| handler/*: `"Kimlik dogrulanamadi"` (every handler), sosyal.go:141 `"Islem tamamlanamadi"`, sosyal.go:147 `"Gecersiz istek govdesi"` | response messages | localized later |
| handler/sosyal.go:73, sosyal_onizleme.go:15 | panic text | translate freely |

_From the sosyal glossary, §4.7 Wire literals in test files (the tests pin the contract; keep them):_

| file:line | literal | why |
|---|---|---|
| handler/sosyal_test.go:180, 194, 211-212, 218-312 | `/api/social/...` paths, `Content-Type: application/json`, `"error"`/`"code"`, codes (`profil_yok`, `kullanici_adi_alinmis`, `gecersiz_kullanici_adi`, `dogrulama`, `sunucu_hatasi`, `bulunamadi`), JSON bodies and keys (`kullaniciAdi`, `gorunenAd`, `avatarRengi`, `bio`, `istatistik`, `takipEdiyorMuyum`, `benMiyim`, `sarki`, `takipci`, `takip`, `cover`, `uygun`, `oneri`), `?ad=` | contract |
| handler/sosyal_akis_test.go:13-65 | `/api/social/akis?sekme=yeni&imlec=abc`, `senin-icin`, `limit=999`, `sonrakiImlec`, `ogeler`, `/profiller/veli/yayinlar?tur=coverlar&limit=5`, `/yayinlar/y1/begeni`, `begendimMi`, `begeniSayisi` | contract |
| handler/sosyal_cover_test.go:18-21, 33-38, 52-59 | cover-baslat path; expected JSON body (line 19-21); codes `altyapi_kapali`, `bulunamadi`, `durum`; `/coverlar?imlec=i` | contract |
| handler/sosyal_onizleme_test.go:19-21, 31, 36, 40, 44, 51-53, 61, 80, 84, 113, 117 | `/api/public`, `/api`, `/social`, `/api/public/s/…`, expected JSON (line 36), `Cache-Control`/`no-store`, `RATE_LIMITED`, `bulunamadi` | contract |
| handler/sosyal_sikayet_test.go:12-13, 21-22, 33-37 | `/api/social/sikayetler`, bodies (`hedefTuru`, `hedefId`, `neden`, `aciklama`), codes | contract |
| handler/sosyal_takip_test.go:20-54, 69 | `/profiller/ayse/takip`, `takipci`, `takipEdiyorMuyum`, `kendini_takip`, `bulunamadi`, `/oneriler`, expected `[{"kullaniciAdi":…}]`, `[]` | contract |
| handler/sosyal_yayin_test.go:24-25, 26, 40-47, 58, 76-93, 100-151 | POST /yayinlar body (all request keys), `durum`/`hazirlaniyor`, codes, `RATE_LIMITED`, detail key list, `2026-10-12T10:00:00Z`, `ton`/`tonik`, `/s/Ab3dEf9h`, `{"gorunurluk":"link"}`, `{"altyapiAcik":true}`, `stem_yok`, `/tekrar-dene`, `durum`, `/dosyalar`, `master`/`url`/`expiresAt` | contract |
| handler/sosyal_yorum_test.go:19-20, 29, 41-62, 69, 73, 80-111 | `/yayinlar/abc/yorumlar`, bodies (`metin`, `zamanMs`), expected JSON (line 20), codes `yorum_kapali`, `profil_yok`, `bulunamadi`, `dogrulama`, `yetki`, `yavasla`, `?imlec=xyz`, `sonrakiImlec`, `/yorumlar/y9` | contract |

**Risks (from the glossaries; names in them are the old ones)**

_From the sosyal glossary, risk 3:_

3. **Untagged structs that are, or could be, serialized.**
   - `OnizlemeKaydi` (`SahipID`, `OnizlemeAnahtari`) and `YayinKaydi` (`SahipID`, `KaynakYayinID`, `StemVar`, `MasterAnahtari`, `Silindi`, `TakipEdiyor`) embed tagged API types but add untagged internal fields. Today the service returns only the embedded `Onizleme` / `YayinDetayi`. If a rename ever returned the record itself, the Go field names would become JSON keys and leak internal R2 keys. Renaming is safe now, but keep the "return the embedded value" pattern.
   - `PuanliOzet.Puan` is untagged and never serialized; it goes into the cursor as `p`.
   - Embedded types (`YayinOzeti` in `YayinDetayi`, `ProfilOzeti` in `Profil`, `Onizleme` in `OnizlemeKaydi`) are untagged, so their fields are flattened. Renaming the embedded type does not change the JSON. If someone adds a tag or turns the embed into a named field, the JSON nests.
   - **Fiber `BodyParser`** (handler: `ProfilGirdisi`, `YayinIstegi`, `YayinYamasi`, `YorumGirdisi`, `SikayetGirdisi`) also accepts `application/x-www-form-urlencoded`, multipart and XML. Those decoders use `form` / `xml` tags, which none of these structs have, so they fall back to the Go field names. A non-JSON client would see the field rename. Mobile and web send JSON, so the risk is low, but it is not zero.

_From the sosyal glossary, risk 9:_

9. **Error message and code coincidence, and the test fakes.** `ErrBulunamadi`'s text is `"bulunamadi"`, the same string as the code. When the messages are localized later, the code must still come from the handler table (`sosyalHatalari`), never from `err.Error()`. The handler tests assert only codes, never messages. Inside `sahte_depo_test.go`, the `akisCagrisi.tur` labels (`"yeni"`, `"takip"`, `"senin-icin"`, `"profil"`, `"cover"`) are compared by `servis_akis_test.go` and `servis_cover_test.go`. If they are translated, change all of those files in one step.

_From the app glossary, risk R2:_

**R2. Cross-file use inside packages, with files owned by other tasks:**
- handler: `istekSahibi` → `requestOwner` and `formatValidationErrors` are called from `sosyal_akis.go`, `sosyal_cover.go`, `sosyal_profil.go`, `sosyal_sikayet.go`, `sosyal_takip.go`, `sosyal_yayin.go` and `sosyal_yorum.go` (about 23 call sites). The test helper `kullaniciOlarak` → `asUser` is used by `sosyal_test.go:180` and `sosyal_yayin_test.go:56`.
- worker: `sahipYazabilir` → `ownerCanWrite` and `errSahipSilindi` → `errOwnerDeleted` are used by `sosyal_worker.go:99-100`. The test helpers `sahteDenetci` → `fakeChecker` and `hepSilinmis` → `alwaysDeleted` are used by `sosyal_worker_test.go:169,182,192`. For consistency, `SosyalWorker`'s `hesap` field should become `deletionChecker`, the same as `RenderWorker` and `MasterWorker`.
- The social-file task must use the names above, or both tasks must be applied together. If either side is applied alone, the package does not compile.

_From the app glossary, risk R9:_

**R9. Name collisions after renaming.**
- worker package: `fakeAudio`, `fakeStorage`, `fakeChecker` and `uploadCall` must not collide with the social worker test fakes (`sahteHazirlikDeposu`, `kopya`, `sahteKopyalayici`, `sahteAnalizci`, `sosyalSilici`, `sosyalKurulum`). The social task should use names such as `fakePreparationStore`, `copyCall`, `fakeCopier`, `fakeAnalyzer`, `socialDeleter`, and must **not** reuse `fakeStorage`, `fakeChecker`, `fakeAudio` or `uploadCall`. I named the test type `uploadCall` rather than `upload` so it cannot shadow the many local `upload`/`uploadStems` identifiers.
- service package: `fakeStorage` (from the shared `sahteDepo`) is embedded by `deletingStorage`. `liveRedis` and `testR2Cfg` exist once per package. `storeJob` was chosen over `saveJob` to avoid confusion with the `saveJob` methods.
- handler package: `liveRedis`, `asUser`, `fakeIdentity` and `nopQueue` must not be reused by the social handler tests (their fakes are `sahteSosyal`, `sosyalUygulamasi`, and so on). I dropped the `h` prefix of `hSahteKimlik`/`hBosKuyruk` because nothing in the package collides with it.
- Identically named types exist in different packages (`fakeAudio`, `fakeStorage`, `fakeIdentity`, `liveRedis`, `testR2Cfg` in service and worker or handler). That is legal Go.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Rename the files**

```bash
git mv internal/handler/sosyal.go internal/handler/social.go
git mv internal/handler/sosyal_akis.go internal/handler/social_feed.go
git mv internal/handler/sosyal_akis_test.go internal/handler/social_feed_test.go
git mv internal/handler/sosyal_cover.go internal/handler/social_cover.go
git mv internal/handler/sosyal_cover_test.go internal/handler/social_cover_test.go
git mv internal/handler/sosyal_onizleme.go internal/handler/social_preview.go
git mv internal/handler/sosyal_onizleme_test.go internal/handler/social_preview_test.go
git mv internal/handler/sosyal_profil.go internal/handler/social_profile.go
git mv internal/handler/sosyal_sikayet.go internal/handler/social_report.go
git mv internal/handler/sosyal_sikayet_test.go internal/handler/social_report_test.go
git mv internal/handler/sosyal_takip.go internal/handler/social_follow.go
git mv internal/handler/sosyal_takip_test.go internal/handler/social_follow_test.go
git mv internal/handler/sosyal_test.go internal/handler/social_test.go
git mv internal/handler/sosyal_yayin.go internal/handler/social_publication.go
git mv internal/handler/sosyal_yayin_test.go internal/handler/social_publication_test.go
git mv internal/handler/sosyal_yorum.go internal/handler/social_comment.go
git mv internal/handler/sosyal_yorum_test.go internal/handler/social_comment_test.go
```

- [ ] **Step 3: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 20 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 4: Do the `manual:` rows**

Follow the rules under the table. Then run:

```bash
go build ./... && go vet ./...
```

- [ ] **Step 5: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- The three `socialErrors` anonymous-struct fields (`hata/durum/kod` → `err/status/code`) are `manual:` rows. Rename each with gopls at its declaration in the `struct { ... }` of `var socialErrors`.

Files (17): `internal/handler/social.go`, `internal/handler/social_comment.go`, `internal/handler/social_comment_test.go`, `internal/handler/social_cover.go`, `internal/handler/social_cover_test.go`, `internal/handler/social_feed.go`, `internal/handler/social_feed_test.go`, `internal/handler/social_follow.go`, `internal/handler/social_follow_test.go`, `internal/handler/social_preview.go`, `internal/handler/social_preview_test.go`, `internal/handler/social_profile.go`, `internal/handler/social_publication.go`, `internal/handler/social_publication_test.go`, `internal/handler/social_report.go`, `internal/handler/social_report_test.go`, `internal/handler/social_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" internal/handler/social.go internal/handler/social_comment.go internal/handler/social_comment_test.go internal/handler/social_cover.go internal/handler/social_cover_test.go internal/handler/social_feed.go internal/handler/social_feed_test.go internal/handler/social_follow.go internal/handler/social_follow_test.go internal/handler/social_preview.go internal/handler/social_preview_test.go internal/handler/social_profile.go internal/handler/social_publication.go internal/handler/social_publication_test.go internal/handler/social_report.go internal/handler/social_report_test.go internal/handler/social_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 6: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 20 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 7: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 8: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(handler): sosyal handler Ingilizce adlandirma

SosyalHandler -> SocialHandler, sosyal*.go -> social*.go; hata tablosu
ve test yardimcilari Ingilizceye cevrildi. Rotalar ve hata kodlari
ayni; kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 21: `cmd/server`, `e2e` and swagger regeneration

**Files (renamed with `git mv`; unchanged files are listed so every file has one owner)**

| old path (relative to core-service/) | new path |
|---|---|
| `cmd/server/main.go` | (unchanged) |
| `docs/docs.go` | (unchanged) |
| `e2e/export_test.go` | (unchanged) |
| `e2e/health_test.go` | (unchanged) |
| `e2e/lyrics_real_test.go` | (unchanged) |
| `e2e/lyrics_test.go` | (unchanged) |
| `e2e/master_test.go` | (unchanged) |
| `e2e/render_real_test.go` | (unchanged) |
| `e2e/render_test.go` | (unchanged) |
| `e2e/testhelper_test.go` | (unchanged) |
| `e2e/upload_test.go` | (unchanged) |

No file is renamed. `docs/docs.go` is generated by swag (the swagger step checks it).

**Declarations** (21 rows)

<!-- renames:21 -->
| old | new | kind | file |
|---|---|---|---|
| kimlikSunucu | identityServer | manual: local var (main.go line 86 at de6c92b) | cmd/server/main.go |
| kapatCtx | shutdownCtx | manual: local var (main.go line 92 at de6c92b) | cmd/server/main.go |
| iptal | cancel | manual: local var (main.go line 92 at de6c92b) | cmd/server/main.go |
| sosyalHavuz | socialPool | manual: local var (main.go line 100 at de6c92b) | cmd/server/main.go |
| sosyalDepo | socialStore | manual: local var (main.go line 108 at de6c92b) | cmd/server/main.go |
| storage | storage | manual: local var (client.StorageClient) (unchanged) (main.go line 145 at de6c92b) | cmd/server/main.go |
| silici | prefixDeleter | manual: local var (client.OnekSilici, renamed client.PrefixDeleter in Task 5) (main.go line 161 at de6c92b) | cmd/server/main.go |
| hesapService | accountService | manual: local var (main.go line 167 at de6c92b) | cmd/server/main.go |
| hesapHandler | accountHandler | manual: local var (main.go line 168 at de6c92b) | cmd/server/main.go |
| kopyalayici | copier | manual: local var (client.Kopyalayici, renamed client.Copier in Task 5) (main.go line 172 at de6c92b) | cmd/server/main.go |
| sosyalHandler | socialHandler | manual: local var (main.go line 176 at de6c92b) | cmd/server/main.go |
| sosyalWorker | socialWorker | manual: local var (main.go line 178 at de6c92b) | cmd/server/main.go |
| kimlikHazir | identityReady | manual: local var (main.go line 234 at de6c92b) | cmd/server/main.go |
| durum | status | manual: local var (main.go line 239 at de6c92b) | cmd/server/main.go |
| startWorkerServer param r2Client client.StorageClient | storage | manual: param (main.go line 364 at de6c92b) | cmd/server/main.go |
| startWorkerServer param hesapService | accountService | manual: param (main.go line 366 at de6c92b) | cmd/server/main.go |
| startWorkerServer param sosyalWorker | socialWorker | manual: param (main.go line 367 at de6c92b) | cmd/server/main.go |
| hesapDenetci | deletionChecker | manual: local var (main.go line 399 at de6c92b) | cmd/server/main.go |
| testAnahtar | testKey | var (test) | e2e/testhelper_test.go |
| TestMasterFinal_YabanciStem404 | TestMasterFinal_ForeignStem404 | func (test) | e2e/master_test.go |
| TestMasterFinal_TaninmayanKonak400 | TestMasterFinal_UnknownHost400 | func (test) | e2e/master_test.go |

Rows whose kind starts with `manual:` are skipped by `apply_renames.py`. Do each by hand:
- An **embedded field** row needs no action. Renaming the embedded type renames the field and every selector on it. Check the result in the diff.
- A **`var _ X = (*Y)(nil)`** row needs no action. The assertion follows the renamed types. Check it.
- An **anonymous struct field**, **local** or **param** row: run `~/go/bin/gopls rename -w <file>:<line>:<col> <new>` on the declaring identifier.
- A **package** row is done by the package-move step.
- A **"methods unchanged; receiver becomes ..."** row needs no action. The methods keep their `op.*` names and follow the type rename.

**Wire strings to keep (must stay byte-identical)**

_From the app glossary, §4.1 asynq task types and queues:_

| file:line | literal | why |
|---|---|---|
| cmd/server/main.go:387-390 | `"render"`, `"master"`, `"sosyal"`, `"bakim"` | asynq server queue weights |
| cmd/server/main.go:404-408 | `service.TaskTypeRender`, `service.TaskTypeMaster`, `service.TaskTypeHesapTemizle`, `sosyal.TaskTypeHazirla` (`sosyal:hazirla`), `sosyal.TaskTypeDosyaSil` (`sosyal:dosya-sil`) | mux registration; only the identifiers get renamed |
| e2e/render_real_test.go:171 | `"render"`, `"master"` | test asynq server queues |

_From the app glossary, §4.3 Redis keys:_

| file:line | literal | why |
|---|---|---|
| cmd/server/main.go:267 | `"public"` | rate-limit prefix → `ratelimit:public:...` |
| cmd/server/main.go:314,315,317 | `"sosyal-yayin"`, `"sosyal-yorum"`, `"sosyal-cover"` | rate-limit prefixes → Redis keys |

_From the app glossary, §4.4 HTTP routes, params, query and form fields, headers:_

| file:line | literal | why |
|---|---|---|
| cmd/server/main.go:222,229,259 | `"/"`, `"/health"`, `"/swagger/*"` | routes |
| cmd/server/main.go:266 | `"/api/public"` | public social route group |
| cmd/server/main.go:271 | `"/api"` | API group |
| cmd/server/main.go:274-276 | `"/lyrics"`, `"/generate"`, `"/rewrite"` | routes |
| cmd/server/main.go:279-283 | `"/render"`, `"/start"`, `"/status/:jobId"`, `"/result/:jobId"`, `"/cancel/:jobId"` | routes and the `jobId` param |
| cmd/server/main.go:286-290 | `"/master"`, `"/preview"`, `"/final"`, `"/status/:jobId"`, `"/result/:jobId"` | routes |
| cmd/server/main.go:293-296 | `"/export"`, `"/mp3"`, `"/wav"`, `"/stems"` | routes |
| cmd/server/main.go:299-301 | `"/upload"`, `"/vocal"`, `"/vocal/:takeId"` | routes |
| cmd/server/main.go:304 | `"/ws/ticket"` | route |
| cmd/server/main.go:307 | `"/files/sign"` | route |
| cmd/server/main.go:309 | `"/account"` | route (`DELETE /api/account`) |
| cmd/server/main.go:313 | `"/social"` | social group |
| cmd/server/main.go:321,329,330 | `"/ws"`, `"/ws/jobs/:jobId"`, `"jobId"` | WebSocket routes and param |
| cmd/server/main.go:216-218 | CORS strings | CORS contract |
| e2e/testhelper_test.go:116-159, e2e/render_real_test.go:128-160, e2e/lyrics_real_test.go:56-59 | route strings | test copies of the main.go routes |
| e2e/* `/api/...` paths | request paths | contract under test |

_From the app glossary, §4.5 Response JSON keys, codes and enum values:_

| file:line | literal | why |
|---|---|---|
| cmd/server/main.go:224 | `"timestamp"` | `/` body |
| cmd/server/main.go:239,241 | `"ok"`, `"degraded"` | `/health` status values |
| cmd/server/main.go:246-253 | `"status"`, `"services"`, `"groq"`, `"suno"`, `"r2"`, `"audio"`, `"auth"`, `"kimlik"` | `/health` body. The `"kimlik"` key stays even though it is Turkish |
| cmd/server/main.go:417,425-427 | `"Internal Server Error"`, `"error"`, `"code"`, `"SERVICE_ERROR"`, `"message"` | error envelope from the Fiber error handler |
| cmd/server/main.go:316 | `"yavasla"` | 429 error code for comments |
| cmd/server/main.go:316 | `"Biraz yavasla"` | 429 message (user-facing; the localization task owns it) |
| e2e/* | `"jobId"`, `"status"`, `"queued"`, `"canceled"`, `"success"`, `"fileUrl"`, `"format"`, `"quality"`, `"bitDepth"`, `"sampleRate"`, `"fileCount"`, `"id"`, `"drafts"`, `"lines"`, `"error"`, `"code"`, `"NOT_FOUND"`, `"UNAUTHORIZED"`, `"VALIDATION_ERROR"`, `"bpm"`, `"duration"`, `"key"`, `"tonic"`, `"scale"`, `"stems"`, `"instrument"`, `"currentStep"`, `"progress"`, and the request-body JSON keys | contract asserted by e2e |

_From the app glossary, §4.6 R2 key layout and audio-service contract:_

| file:line | literal | why |
|---|---|---|
| internal/service/*_test.go, internal/worker/*_test.go, e2e | `"vocals/…"`, `"masters/…"`, `"stems/…"`, `"exports/…"` key literals | test the R2 layout |

_From the app glossary, §4.7 Env vars and config:_

| file:line | literal | why |
|---|---|---|
| e2e/render_real_test.go:203 | `"SUNO_E2E_ONAY"` | env var, documented in CLAUDE.md |
| e2e/render_real_test.go:34 | `".env"` | file name |
| cmd/server/main.go:84 | `AUTH_ISSUER` inside the fatal message | env var name (the message is a log line and may be translated around it) |
| e2e/*: messages that mention `SUNO_API_KEY`, `GROQ_API_KEY` | env names inside skip messages | keep the names |

_From the app glossary, §4.8 Swagger annotations (generate docs/):_

| file:line | literal | why |
|---|---|---|
| cmd/server/main.go:35-44 | `@title`, `@securityDefinitions.apikey BearerAuth`, `@name Authorization` | API metadata |

**Risks (from the glossaries; names in them are the old ones)**

_From the app glossary, risk R3:_

**R3. Names from other glossaries that my scope calls** (main.go, handlers, services, workers and tests must follow those glossaries). The name after each arrow is my assumption:
- `client`: `KullaniciAnahtari`, `KullaniciOnekleri`, `SahipliAnahtarCoz`, `SosyalOneki`, `SunoStemAdi`, `ErrYabanciAnahtar`, `ErrGecersizURL`, `ErrGecersizSegment`, `OnekSilici` → `PrefixDeleter`, `Kopyalayici` → `Copier`.
- `model`: `ErrGecersizStemAyari`, `Job.SahibiMi` (job_kayit.go:51), `RenderStartRequest.StemAyarlari` (render_service.go:76).
- `kimlik` → `identity`: `Start`, `Baglan`, `ErrKullaniciYok`, `Sunucu`, `Sunucu.Kapat`, `Sunucu.Hazir`, `Sunucu.APIAcikAnahtar`, `Sunucu.HesapSil`.
- `hesapisaret` → `deletionmark`: `Anahtar`, `Omur`, `Denetci`, `NewRedisDenetci`, `Denetci.Silinmis`.
- `sosyal` → `social`: `Migrate`, `NewPostgresDepo`, `NewServis` → `NewService`, `Servis.KopyalayiciAyarla`, `TaskTypeHazirla`, `TaskTypeDosyaSil`.
- handler social files: `NewSosyalHandler`, `SosyalHandler.PublicRotalar`, `SosyalHandler.Rotalar`, `SosyalLimitleri{Yayin, Yorum, Cover}`. worker: `NewSosyalWorker`, `SosyalWorker.Hazirla`, `SosyalWorker.DosyaSil`.
- `middleware`: `SilinmisKullaniciEngeli`, `RateLimiter.LimitKod`, `LimitIP`.
- `config`: `cfg.Sosyal.WebAnahtari`, `cfg.RateLimit.SosyalYayinPerHour`, `SosyalYorumPerMin`, `SosyalCoverPerHour`. The field names may change, but their `mapstructure` tags and env names must not.

_From the app glossary, risk R12:_

**R12. `docs/` is generated.** `docs/docs.go`, `swagger.json` and `swagger.yaml` come from `swag init -g cmd/server/main.go`. Their definitions are named after Go types (`model.WSTicketRequest`, `response.ErrorResponse`, and so on). Any model or response type rename changes the generated definition names, but not the runtime API. Regenerate them; do not hand-edit. The current swagger has no social routes (the social handlers have no `@Router` annotations), so regenerating does not add or remove routes as long as the annotations in §4.8 are unchanged. `docs/API.md` is hand-written and documents wire values (`hesap:temizle`, `bakim`, `kimlik:silindi:`). Leave those values as they are.

_From the app glossary, risk R13:_

**R13. main.go typed-nil pattern.** The `storage`, `silici` and `kopyalayici` interface variables (main.go:145, 161, 172) exist to avoid the typed-nil trap. When renaming to `storage`, `prefixDeleter` and `copier`, keep the explicit `if r2Client != nil { x = r2Client }` blocks and the comments that explain them. Do not collapse them into direct assignments.

_From the sosyal glossary, risk 5:_

5. **Cross-package callers that must follow the renames.**
   - `cmd/server/main.go:30, 105, 108, 176-178, 266, 313-317, 367, 407-408`: `sosyal.Migrate`, `NewPostgresDepo`, `NewServis(...).KopyalayiciAyarla`, `handler.NewSosyalHandler`, `SosyalLimitleri{Yayin, Yorum, Cover}`, `PublicRotalar`, `Rotalar`, `worker.NewSosyalWorker`, `*worker.SosyalWorker`, `sosyal.TaskTypeHazirla` / `TaskTypeDosyaSil` with `sosyalWorker.Hazirla` / `DosyaSil`.
   - main.go also holds **wire values outside this scope** that must stay as they are: the rate-limit keys `"sosyal-yayin"`, `"sosyal-yorum"` and `"sosyal-cover"` (Redis prefixes) and the asynq queue priority key `"sosyal": 2`, which must equal `QueueSocial`. It also uses the config fields `cfg.Sosyal.WebAnahtari` and `cfg.RateLimit.Sosyal*PerHour/PerMin`, whose `mapstructure` / env keys stay as they are.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Apply the declaration table**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 21 &&
files=$(gofmt -l .) && { [ -z "$files" ] || gofmt -w $files; } &&   # gopls does not realign columns
go build ./... && go vet ./...
```

Expected: one `rename` or `already` line per automatic row, then `<k> renamed, <n> rows in table`. The build and vet print nothing. If gopls refuses a row, the script stops and names it. Fix the cause (usually a local variable that already has the new name: rename the local first), then re-run. Rows that are done print `already`.

- [ ] **Step 3: Do the `manual:` rows**

Follow the rules under the table. Then run:

```bash
go build ./... && go vet ./...
```

- [ ] **Step 4: Translate comments and the remaining locals**

In the files listed below:
- Translate every Turkish comment into English. Use the final names from this plan, including names that a later task renames.
- Keep directive lines exactly as they are: `//go:embed`, `//go:build`, `//nolint:`, and swag `// @...` annotations (see Global Constraints).
- Rename the remaining Turkish local variables, parameters and named results. They are not in the tables (see Global Constraints, Tooling). Use `~/go/bin/gopls rename -w <file>:<line>:<col> <newName>`.
- **Do not edit any string literal** (ruling A7).
- `main.go` locals and params are table rows of kind `manual: local var` / `manual: param` (the app glossary lists them with line numbers). Rename each with gopls at its declaration.

Files (11): `cmd/server/main.go`, `docs/docs.go`, `e2e/export_test.go`, `e2e/health_test.go`, `e2e/lyrics_real_test.go`, `e2e/lyrics_test.go`, `e2e/master_test.go`, `e2e/render_real_test.go`, `e2e/render_test.go`, `e2e/testhelper_test.go`, `e2e/upload_test.go`

```bash
grep -nE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" cmd/server/main.go docs/docs.go e2e/export_test.go e2e/health_test.go e2e/lyrics_real_test.go e2e/lyrics_test.go e2e/master_test.go e2e/render_real_test.go e2e/render_test.go e2e/testhelper_test.go e2e/upload_test.go
```

Expected: no output, except comment lines that quote a wire value or test data on purpose. Review each remaining hit.

- [ ] **Step 5: Regenerate swagger and compare**

```bash
~/go/bin/swag init -g cmd/server/main.go
git diff --stat -- docs
```

Expected: no change under `docs/`. The annotations name only English model types and routes, and `swag` v1.16.4 is the version that produced the committed output. If `docs/` changes, inspect it. Only a type name that a rename task changed may differ (none is expected). Keep a correct change and mention it in the commit message.

- [ ] **Step 6: Check that every table row is done**

```bash
python3 "$T/apply_renames.py" ../docs/superpowers/plans/2026-10-12-backend-english-naming.md 21 --dry-run | grep -v '^already'
```

Expected: one line per command, of the form `0 renamed, <n> rows in table`. A `rename` or `textual` line is a row that was missed. Run the script without `--dry-run` for that marker, then repeat this step.

- [ ] **Step 7: Verify**

Run the VERIFY block from Global Constraints. The guard must pass unchanged. Do not regenerate `wire.txt` in this task.

- [ ] **Step 8: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(server): main ve e2e Ingilizce adlandirma, swagger yenilendi

cmd/server yerelleri, e2e testleri ve yorumlari Ingilizceye cevrildi;
swag ciktisi yeniden uretildi. Kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 22: Localized API error messages (TR/EN by Accept-Language)

_Inserted verbatim from the verified bracketing tasks, with three edits made by the assembly: the name-mapping section is rewritten with the final names, file paths are the final paths, and the snapshot line counts and the commit command (explicit paths) are adjusted. It runs after all renames (Tasks 2-21). In its rulings it is called "Task B"._

**Goal:** Make the `message` of every API error localized by `Accept-Language`, with Turkish as the default.
- Error codes, HTTP statuses and the JSON shape `{"error":{"code","message","details"}}` do not change.
- Every error response gets `Content-Language` and `Vary: Accept-Language`.

**Name mapping (read first)**

This task was written and verified against the pre-rename code at `de6c92b` + Task 1. When it runs, Tasks 2-21 have landed. The diffs and code blocks below still show the **pre-rename** identifiers. Write them with the final names from this table. Match each hunk by its string literals. The renames left those untouched, and the wire guard proves it. Line numbers in the hunk headers are pre-rename and will not match.

New identifiers introduced here are already English and stay as written:
- `i18n.*`;
- `response.Lang`;
- handler `internalError` and `aiError`;
- social `ValidationError` and `invalid`.

The assembly checked these names against every final name in the target packages, and there is no collision. `social.ValidationError` (type) and `social.validationError` (the renamed `dogrulamaHatasi`, func) differ in case. This task deletes the func.

Paths (final; the file paths in the diff headers below are already rewritten to these):

| Pre-rename path | Final path |
|---|---|
| `internal/handler/sosyal.go` | `internal/handler/social.go` |
| `internal/handler/sosyal_akis.go` | `internal/handler/social_feed.go` |
| `internal/handler/sosyal_cover.go` | `internal/handler/social_cover.go` |
| `internal/handler/sosyal_profil.go` | `internal/handler/social_profile.go` |
| `internal/handler/sosyal_sikayet.go` | `internal/handler/social_report.go` |
| `internal/handler/sosyal_takip.go` | `internal/handler/social_follow.go` |
| `internal/handler/sosyal_yayin.go` | `internal/handler/social_publication.go` |
| `internal/handler/sosyal_yorum.go` | `internal/handler/social_comment.go` |
| `internal/handler/sosyal_onizleme_test.go` | `internal/handler/social_preview_test.go` |
| `internal/handler/sosyal_yorum_test.go` | `internal/handler/social_comment_test.go` |
| `internal/handler/hesap.go` | `internal/handler/account.go` |
| `internal/handler/{export,files,lyrics,master,render,upload,ws_ticket}.go` | unchanged |
| `internal/middleware/silinmis.go` | `internal/middleware/deleted_user.go` |
| `internal/middleware/{auth,ratelimit}.go`, `ratelimit_ip_test.go` | unchanged |
| `internal/sosyal/hatalar.go` | `internal/social/errors.go` |
| `internal/sosyal/imlec.go` | `internal/social/cursor.go` |
| `internal/sosyal/kurallar.go` | `internal/social/profile_rules.go` |
| `internal/sosyal/servis_akis.go` | `internal/social/service_feed.go` |
| `internal/sosyal/servis_sikayet.go` | `internal/social/service_report.go` |
| `internal/sosyal/servis_yayin.go` | `internal/social/service_publication.go` |
| `internal/sosyal/servis_yorum.go` | `internal/social/service_comment.go` |
| `internal/sosyal/yayin_kurallari.go` | `internal/social/publication_rules.go` |
| `cmd/server/main.go`, `pkg/response/response.go` | unchanged |
| new: `internal/i18n/*`, `internal/handler/errors.go`, `internal/handler/i18n_test.go`, `pkg/response/response_test.go` | as written (no existing file has these paths) |

Identifiers (pre-rename → final; the task that renamed it):

| Package | Pre-rename | Final | Task |
|---|---|---|---|
| — | package `sosyal`, import `.../internal/sosyal` | package `social`, import `.../internal/social` | 13 |
| social | `ErrDogrulama` | `ErrValidation` | 13 |
| social | `ErrKimlik` | `ErrInvalidIdentity` | 13 |
| social | `ErrProfilYok` | `ErrNoProfile` | 13 |
| social | `ErrBulunamadi` | `ErrNotFound` | 13 |
| social | `ErrGecersizKullaniciAdi` | `ErrInvalidUsername` | 13 |
| social | `ErrKullaniciAdiAlinmis` | `ErrUsernameTaken` | 13 |
| social | `ErrSahiplik` | `ErrOwnership` | 13 |
| social | `ErrKaynakBulunamadi` | `ErrSourceNotFound` | 13 |
| social | `ErrAltyapiKapali` | `ErrBackingDisabled` | 13 |
| social | `ErrStemYok` | `ErrNoStems` | 13 |
| social | `ErrDurum` | `ErrInvalidStatus` | 13 |
| social | `ErrKendiniTakip` | `ErrSelfFollow` | 13 |
| social | `ErrYorumKapali` | `ErrCommentsDisabled` | 13 |
| social | `ErrYetki` | `ErrForbidden` | 13 |
| social | `dogrulamaHatasi` (deleted by this task) | `validationError` | 13 |
| social | `Servis.anahtarCoz`, its parameter `alan` | `Service.resolveKey`, parameter `field` | 14 |
| handler | `sosyalHatalari` | `socialErrors` | 20 |
| handler | `sosyalHatalari[].hata` / `.durum` / `.kod` | `socialErrors[].err` / `.status` / `.code` | 20 |
| handler | new field `mesaj` (added by this task) | `message` | 22 |
| handler | `sosyalHata` | `socialError` | 20 |
| handler | `govdeHatasi` | `badBodyError` | 20 |
| handler | `istekSahibi` | `requestOwner` | 19 |
| handler | `SosyalHandler` | `SocialHandler` | 20 |
| handler | `SosyalHandler.Rotalar` / `.PublicRotalar` | `SocialHandler.Routes` / `.PublicRoutes` | 20 |
| handler | `SosyalLimitleri` | `SocialLimits` | 20 |
| handler tests | `sahteSosyal` | `fakeSocialService` | 20 |
| handler tests | `sosyalUygulamasi` | `newSocialApp` | 20 |
| handler tests | `sosyalKullanici` | `socialTestUser` | 20 |
| handler tests | `hataKodu` | `errorCode` | 20 |
| middleware | `RateLimiter.LimitKod`, params `kod` / `mesaj` | `RateLimiter.LimitWithCode`, params `code` / `msg` | 6 |
| middleware | `RateLimiter.kullaniciBasina` | `RateLimiter.perUser` | 6 |
| middleware | `RateLimiter.say` | `RateLimiter.hit` | 6 |
| middleware | `SilinmisKullaniciEngeli` | `DeletedUserGate` | 6 |
| model | `ErrGecersizStemAyari` | `ErrInvalidStemSettings` | 2 |
| client | `ErrGecersizSegment` | `ErrInvalidSegment` | 5 |
| client | `ErrGecersizURL` | `ErrInvalidURL` | 5 |
| client | `ErrYabanciAnahtar` | `ErrKeyNotOwned` | 5 |
| service | `ErrJobNotFound` | `ErrJobNotFound` (unchanged) | — |
| service | `ErrHesapYok` | `ErrAccountNotFound` | 16 |
| service | `ErrBiletGecersiz` | `ErrInvalidTicket` | 16 |

For any other pre-rename identifier in a hunk (a local, a test helper), use the name the code has now. The declaration tables of Tasks 2-21 list every renamed declaration.

**Interfaces**

```go
// package i18n (internal/i18n), stdlib only
type Lang string
const ( TR Lang = "tr"; EN Lang = "en"; Default = TR )
type Key struct{ id string }            // var KeyXxx = Key{"xxx"} in catalog.go
func (k Key) String() string
func FromAcceptLanguage(header string) Lang
func Message(lang Lang, key Key, args ...any) string

// package response (pkg/response)
func Lang(c *fiber.Ctx) i18n.Lang      // parses Accept-Language once, caches in c.Locals
func Error(c *fiber.Ctx, status int, code string, msg i18n.Key, details interface{}, args ...any) error
func ValidationError(c *fiber.Ctx, msg i18n.Key, details interface{}) error
func Unauthorized(c *fiber.Ctx, msg i18n.Key) error
func Forbidden(c *fiber.Ctx, msg i18n.Key) error
func NotFound(c *fiber.Ctx, msg i18n.Key) error
func RateLimited(c *fiber.Ctx) error   // i18n.KeyRateLimited
func ServiceError(c *fiber.Ctx, msg i18n.Key) error
func AIError(c *fiber.Ctx, msg i18n.Key) error

// package social (was sosyal)
type ValidationError struct { Key i18n.Key; Args []any }   // Unwrap() == ErrDogrulama (post-rename name)
func invalid(key i18n.Key, args ...any) error

// package handler
func internalError(c *fiber.Ctx, err error) error   // logs err, 500 SERVICE_ERROR KeyInternal
func aiError(c *fiber.Ctx, err error) error         // logs err, 502 AI_ERROR KeyAIUnavailable

// package middleware
func (rl *RateLimiter) LimitKod(keyPrefix string, maxRequests int, window time.Duration, kod string, mesaj i18n.Key) fiber.Handler
```

**Inventory (verified)**

| Source | Count | Becomes |
|---|---|---|
| `response.*` lines in handlers and middleware | 131 changed lines | keys per the table below |
| `dogrulamaHatasi(...)` calls in social | 44 | `invalid(i18n.Key...)` |
| social sentinel mapping (`err.Error()` as message) | 13 entries | `mesaj` column |
| `sosyal.ErrKimlik` → 401 | 1 | `KeyUnauthenticated` |
| `social_profile.go` 404 `profil_yok` with `err.Error()` | 1 | `KeySocialProfileMissing` |
| `ServiceError(c, err.Error())` | 13 | `internalError(c, err)` |
| `AIError(c, err.Error())` | 2 | `aiError(c, err)` |
| render `ValidationError(c, err.Error())` | 1 | `KeyStemSettingsInvalid` |
| `main.go` `LimitKod(..., "Biraz yavasla")` and `customErrorHandler` | 2 | `KeySlowDown`; status-mapped keys |

Literal → key (handlers and middleware):

| Old literal | Key |
|---|---|
| `Kimlik dogrulanamadi` (×41) | `KeyUnauthenticated` |
| `Missing authorization header` | `KeyAuthHeaderMissing` |
| `Invalid authorization header format` | `KeyAuthHeaderInvalid` |
| `Invalid or expired token` (×2) | `KeyTokenInvalid` |
| `Hesap silindi` | `KeyAccountDeleted` |
| `Hesap durumu denetlenemedi` | `KeyAccountStatusUnavailable` |
| `Hesap bulunamadi` | `KeyAccountNotFound` |
| `Hesap silinemedi` | `KeyAccountDeleteFailed` |
| `Rate limit exceeded` (in `RateLimited`) | `KeyRateLimited` |
| `Rate limit denetlenemedi` (×2) | `KeyRateLimitUnavailable` |
| `Biraz yavasla` | `KeySlowDown` |
| `Invalid request body` (×10), `Gecersiz istek govdesi` | `KeyInvalidBody` |
| `Validation failed` (×10) | `KeyValidationFailed` |
| `Islem tamamlanamadi`, every `err.Error()` 500 | `KeyInternal` |
| `Job ID is required` (×5) | `KeyJobIDRequired` |
| `Job not found` (×6) | `KeyJobNotFound` |
| `Job not completed yet` (×2) | `KeyJobNotCompleted` |
| `Job already completed` | `KeyJobAlreadyCompleted` |
| `File not found` (×5) | `KeyFileNotFound` |
| `Gecersiz dosya URL'si` (×5) | `KeyInvalidFileURL` |
| `Adres uretilemedi` | `KeyURLSignFailed` |
| `projectId is required` / `sectionId is required` | `KeyProjectIDRequired` / `KeySectionIDRequired` |
| `File is required` | `KeyFileRequired` |
| `File size exceeds 50MB limit` | `KeyFileTooLarge` |
| `Invalid file type. Supported: WAV, M4A, MP3, AAC` | `KeyFileTypeInvalid` |
| `Failed to open file` | `KeyFileOpenFailed` |
| `Invalid projectId or sectionId` | `KeyUploadIDsInvalid` |
| `takeId, projectId and sectionId are required` | `KeyVocalIDsRequired` |
| `Invalid takeId, projectId or sectionId` | `KeyVocalIDsInvalid` |
| `Bilet uretilemedi` / `Gecersiz WebSocket bileti` / `Bilet denetlenemedi` | `KeyTicketIssueFailed` / `KeyTicketInvalid` / `KeyTicketCheckFailed` |

The full key catalog, with TR and EN text for each key, is `internal/i18n/catalog.go` in Step 2.

- [ ] **Step 1: Write the failing i18n tests**

Create `core-service/internal/i18n/i18n_test.go`:

```go
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
```

Run: `go test ./internal/i18n`
Expected: a build failure (`undefined: FromAcceptLanguage`, `undefined: catalog`, …).

- [ ] **Step 2: Implement the i18n package**

Create `core-service/internal/i18n/i18n.go`:

```go
// Package i18n localizes user-facing API error messages. The language
// comes from the Accept-Language header (Turkish by default); messages
// are looked up by Key in a fixed TR/EN catalog (catalog.go). Error codes
// are not localized: clients branch on the code, the message is for
// people.
package i18n

import (
	"fmt"
	"strconv"
	"strings"
)

// Lang is a supported message language.
type Lang string

const (
	TR Lang = "tr"
	EN Lang = "en"

	// Default is used when the header names no supported language.
	Default = TR
)

// Key identifies one message in the catalog. It is a struct, not a
// string, so a stray string literal passed where a Key is expected does
// not compile. Its id is never sent to clients.
type Key struct{ id string }

func (k Key) String() string { return k.id }

// FromAcceptLanguage picks the supported language with the highest q
// value from an Accept-Language header ("en-US,en;q=0.9,tr;q=0.8").
// Ties go to the earlier entry; region subtags are ignored ("en-GB" is
// EN); q=0, malformed q values and "*" never select a language. An empty
// or unsupported header yields Default.
func FromAcceptLanguage(header string) Lang {
	best, bestQ := Default, 0.0
	for _, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		lang, ok := supported(tag)
		if !ok {
			continue
		}
		if q := quality(params); q > bestQ {
			best, bestQ = lang, q
		}
	}
	return best
}

// supported maps a language tag to a Lang by its primary subtag.
func supported(tag string) (Lang, bool) {
	primary, _, _ := strings.Cut(strings.TrimSpace(tag), "-")
	primary, _, _ = strings.Cut(primary, "_")
	switch strings.ToLower(primary) {
	case "tr":
		return TR, true
	case "en":
		return EN, true
	}
	return "", false
}

// quality returns the q parameter (1 when absent, 0 when malformed).
func quality(params string) float64 {
	for _, p := range strings.Split(params, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "q") {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || q < 0 || q > 1 {
			return 0
		}
		return q
	}
	return 1
}

// Message returns the text for key in lang, formatted with args
// (fmt.Sprintf verbs) when any are given. An unknown key returns the key
// itself, so a missing entry is visible rather than an empty message.
func Message(lang Lang, key Key, args ...any) string {
	t, ok := catalog[key]
	if !ok {
		return key.id
	}
	s := t.tr
	if lang == EN {
		s = t.en
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}
```

Create `core-service/internal/i18n/catalog.go` (the complete key catalog):

```go
package i18n

// Message keys. Every key needs a catalog entry with both languages
// (enforced by TestCatalogComplete).
var (
	// Authentication and account state.
	KeyUnauthenticated          = Key{"unauthenticated"}
	KeyAuthHeaderMissing        = Key{"auth_header_missing"}
	KeyAuthHeaderInvalid        = Key{"auth_header_invalid"}
	KeyTokenInvalid             = Key{"token_invalid"}
	KeyAccountDeleted           = Key{"account_deleted"}
	KeyAccountStatusUnavailable = Key{"account_status_unavailable"}
	KeyAccountNotFound          = Key{"account_not_found"}
	KeyAccountDeleteFailed      = Key{"account_delete_failed"}

	// Rate limiting.
	KeyRateLimited          = Key{"rate_limited"}
	KeyRateLimitUnavailable = Key{"rate_limit_unavailable"}
	KeySlowDown             = Key{"slow_down"}

	// Generic request and server errors.
	KeyInvalidBody      = Key{"invalid_body"}
	KeyValidationFailed = Key{"validation_failed"}
	KeyInternal         = Key{"internal"}
	KeyAIUnavailable    = Key{"ai_unavailable"}
	KeyRouteNotFound    = Key{"route_not_found"}
	KeyMethodNotAllowed = Key{"method_not_allowed"}
	KeyBodyTooLarge     = Key{"body_too_large"}
	KeyUpgradeRequired  = Key{"upgrade_required"}
	KeyRequestFailed    = Key{"request_failed"}

	// Jobs, files, uploads and WebSocket tickets.
	KeyJobIDRequired       = Key{"job_id_required"}
	KeyJobNotFound         = Key{"job_not_found"}
	KeyJobNotCompleted     = Key{"job_not_completed"}
	KeyJobAlreadyCompleted = Key{"job_already_completed"}
	KeyStemSettingsInvalid = Key{"stem_settings_invalid"}
	KeyFileNotFound        = Key{"file_not_found"}
	KeyInvalidFileURL      = Key{"invalid_file_url"}
	KeyURLSignFailed       = Key{"url_sign_failed"}
	KeyProjectIDRequired   = Key{"project_id_required"}
	KeySectionIDRequired   = Key{"section_id_required"}
	KeyFileRequired        = Key{"file_required"}
	KeyFileTooLarge        = Key{"file_too_large"}
	KeyFileTypeInvalid     = Key{"file_type_invalid"}
	KeyFileOpenFailed      = Key{"file_open_failed"}
	KeyUploadIDsInvalid    = Key{"upload_ids_invalid"}
	KeyVocalIDsRequired    = Key{"vocal_ids_required"}
	KeyVocalIDsInvalid     = Key{"vocal_ids_invalid"}
	KeyTicketIssueFailed   = Key{"ticket_issue_failed"}
	KeyTicketInvalid       = Key{"ticket_invalid"}
	KeyTicketCheckFailed   = Key{"ticket_check_failed"}

	// Social service errors (one per sentinel error).
	KeySocialNotFound         = Key{"social_not_found"}
	KeySocialProfileMissing   = Key{"social_profile_missing"}
	KeySocialUsernameInvalid  = Key{"social_username_invalid"}
	KeySocialUsernameTaken    = Key{"social_username_taken"}
	KeySocialValidation       = Key{"social_validation"}
	KeySocialOwnership        = Key{"social_ownership"}
	KeySocialSourceNotFound   = Key{"social_source_not_found"}
	KeySocialBackingClosed    = Key{"social_backing_closed"}
	KeySocialNoStems          = Key{"social_no_stems"}
	KeySocialBadStatus        = Key{"social_bad_status"}
	KeySocialSelfFollow       = Key{"social_self_follow"}
	KeySocialCommentsDisabled = Key{"social_comments_disabled"}
	KeySocialForbidden        = Key{"social_forbidden"}

	// Social field validation (422 dogrulama).
	KeyCursorInvalid             = Key{"cursor_invalid"}
	KeyFeedTabInvalid            = Key{"feed_tab_invalid"}
	KeyProfileFeedTypeInvalid    = Key{"profile_feed_type_invalid"}
	KeyProfileTextNUL            = Key{"profile_text_nul"}
	KeyDisplayNameLength         = Key{"display_name_length"}
	KeyBioLength                 = Key{"bio_length"}
	KeyTextFieldsNUL             = Key{"text_fields_nul"}
	KeyTitleLength               = Key{"title_length"}
	KeyDescriptionLength         = Key{"description_length"}
	KeyDescriptionNUL            = Key{"description_nul"}
	KeyDescriptionInvalidChars   = Key{"description_invalid_chars"}
	KeyVisibilityInvalid         = Key{"visibility_invalid"}
	KeyScaleModeInvalid          = Key{"scale_mode_invalid"}
	KeyTonicEmpty                = Key{"tonic_empty"}
	KeyGenreLength               = Key{"genre_length"}
	KeyDurationPositive          = Key{"duration_positive"}
	KeyBPMRange                  = Key{"bpm_range"}
	KeyMasterURLRequired         = Key{"master_url_required"}
	KeyStructureTooLong          = Key{"structure_too_long"}
	KeyStructureItemInvalid      = Key{"structure_item_invalid"}
	KeyLyricsTooLong             = Key{"lyrics_too_long"}
	KeyLyricsSectionInvalid      = Key{"lyrics_section_invalid"}
	KeyLyricsLineInvalid         = Key{"lyrics_line_invalid"}
	KeyStemRequired              = Key{"stem_required"}
	KeyTooManyStems              = Key{"too_many_stems"}
	KeyStemIDInvalid             = Key{"stem_id_invalid"}
	KeyStemIDDuplicate           = Key{"stem_id_duplicate"}
	KeyStemFieldsInvalid         = Key{"stem_fields_invalid"}
	KeyFieldFileURLInvalid       = Key{"field_file_url_invalid"}
	KeyFieldFileNotPrivate       = Key{"field_file_not_private"}
	KeyCommentTextNUL            = Key{"comment_text_nul"}
	KeyCommentTextLength         = Key{"comment_text_length"}
	KeyCommentTimeNegative       = Key{"comment_time_negative"}
	KeyCommentTimeTooLarge       = Key{"comment_time_too_large"}
	KeyCommentTimeBeyondDuration = Key{"comment_time_beyond_duration"}
	KeyReportTargetInvalid       = Key{"report_target_invalid"}
	KeyReportReasonInvalid       = Key{"report_reason_invalid"}
)

// texts holds one message in both languages. Verbs (%s) must match.
type texts struct{ tr, en string }

var catalog = map[Key]texts{
	KeyUnauthenticated:          {"Kimlik doğrulanamadı.", "Authentication failed."},
	KeyAuthHeaderMissing:        {"Authorization başlığı eksik.", "Missing Authorization header."},
	KeyAuthHeaderInvalid:        {"Authorization başlığı geçersiz; beklenen biçim: Bearer <jeton>.", "Invalid Authorization header; expected format: Bearer <token>."},
	KeyTokenInvalid:             {"Erişim jetonu geçersiz ya da süresi dolmuş.", "The access token is invalid or expired."},
	KeyAccountDeleted:           {"Bu hesap silindi.", "This account has been deleted."},
	KeyAccountStatusUnavailable: {"Hesap durumu denetlenemedi, lütfen biraz sonra tekrar deneyin.", "Could not check the account status; please try again shortly."},
	KeyAccountNotFound:          {"Hesap bulunamadı.", "Account not found."},
	KeyAccountDeleteFailed:      {"Hesap silinemedi, lütfen tekrar deneyin.", "Could not delete the account; please try again."},

	KeyRateLimited:          {"Çok fazla istek gönderildi, lütfen biraz bekleyin.", "Too many requests; please wait a moment."},
	KeyRateLimitUnavailable: {"İstek sınırı denetlenemedi, lütfen biraz sonra tekrar deneyin.", "Could not check the rate limit; please try again shortly."},
	KeySlowDown:             {"Biraz yavaşla.", "Slow down a little."},

	KeyInvalidBody:      {"Geçersiz istek gövdesi.", "Invalid request body."},
	KeyValidationFailed: {"Doğrulama başarısız.", "Validation failed."},
	KeyInternal:         {"İşlem tamamlanamadı, lütfen tekrar deneyin.", "The operation could not be completed; please try again."},
	KeyAIUnavailable:    {"Yapay zekâ servisi şu anda yanıt veremiyor, lütfen tekrar deneyin.", "The AI service could not respond; please try again."},
	KeyRouteNotFound:    {"İstenen adres bulunamadı.", "The requested endpoint was not found."},
	KeyMethodNotAllowed: {"Bu adres bu HTTP yöntemini desteklemiyor.", "This endpoint does not support this HTTP method."},
	KeyBodyTooLarge:     {"İstek gövdesi çok büyük.", "The request body is too large."},
	KeyUpgradeRequired:  {"Bu adres bir WebSocket bağlantısı gerektirir.", "This endpoint requires a WebSocket connection."},
	KeyRequestFailed:    {"İstek işlenemedi.", "The request could not be processed."},

	KeyJobIDRequired:       {"İş kimliği (jobId) zorunludur.", "Job ID is required."},
	KeyJobNotFound:         {"İş bulunamadı.", "Job not found."},
	KeyJobNotCompleted:     {"İş henüz tamamlanmadı.", "The job has not completed yet."},
	KeyJobAlreadyCompleted: {"İş zaten tamamlandı.", "The job has already completed."},
	KeyStemSettingsInvalid: {"Stem ayarları geçersiz: focusInstrument yalnızca focus modunda kullanılır ve aranjmanda bulunmalıdır.", "Invalid stem settings: focusInstrument is only used in focus mode and must be part of the arrangement."},
	KeyFileNotFound:        {"Dosya bulunamadı.", "File not found."},
	KeyInvalidFileURL:      {"Geçersiz dosya adresi.", "Invalid file URL."},
	KeyURLSignFailed:       {"Dosya adresi üretilemedi.", "Could not generate the file URL."},
	KeyProjectIDRequired:   {"projectId zorunludur.", "projectId is required."},
	KeySectionIDRequired:   {"sectionId zorunludur.", "sectionId is required."},
	KeyFileRequired:        {"Dosya zorunludur.", "A file is required."},
	KeyFileTooLarge:        {"Dosya boyutu 50 MB sınırını aşıyor.", "The file exceeds the 50 MB limit."},
	KeyFileTypeInvalid:     {"Geçersiz dosya türü. Desteklenenler: WAV, M4A, MP3, AAC.", "Invalid file type. Supported: WAV, M4A, MP3, AAC."},
	KeyFileOpenFailed:      {"Dosya açılamadı.", "Could not open the file."},
	KeyUploadIDsInvalid:    {"Geçersiz projectId ya da sectionId.", "Invalid projectId or sectionId."},
	KeyVocalIDsRequired:    {"takeId, projectId ve sectionId zorunludur.", "takeId, projectId and sectionId are required."},
	KeyVocalIDsInvalid:     {"Geçersiz takeId, projectId ya da sectionId.", "Invalid takeId, projectId or sectionId."},
	KeyTicketIssueFailed:   {"Bilet üretilemedi.", "Could not issue a ticket."},
	KeyTicketInvalid:       {"Geçersiz WebSocket bileti.", "Invalid WebSocket ticket."},
	KeyTicketCheckFailed:   {"Bilet denetlenemedi.", "Could not verify the ticket."},

	KeySocialNotFound:         {"Aranan içerik bulunamadı.", "The requested item was not found."},
	KeySocialProfileMissing:   {"Profil henüz oluşturulmamış.", "The profile has not been created yet."},
	KeySocialUsernameInvalid:  {"Geçersiz kullanıcı adı.", "Invalid username."},
	KeySocialUsernameTaken:    {"Bu kullanıcı adı alınmış.", "This username is already taken."},
	KeySocialValidation:       {"İstek doğrulanamadı.", "The request is invalid."},
	KeySocialOwnership:        {"Dosya bu kullanıcıya ait değil.", "The file does not belong to this user."},
	KeySocialSourceNotFound:   {"Kaynak yayın bulunamadı.", "The source publication was not found."},
	KeySocialBackingClosed:    {"Kaynak yayının altyapısı paylaşıma kapalı.", "The source publication's backing track is not shared."},
	KeySocialNoStems:          {"Yayının stem kopyası yok.", "The publication has no stem copies."},
	KeySocialBadStatus:        {"Yayın bu işlem için uygun durumda değil.", "The publication is not in a state that allows this action."},
	KeySocialSelfFollow:       {"Kendini takip edemezsin.", "You cannot follow yourself."},
	KeySocialCommentsDisabled: {"Bu yayının yorumları kapalı.", "Comments are turned off for this publication."},
	KeySocialForbidden:        {"Bu işlem için yetkin yok.", "You are not allowed to do this."},

	KeyCursorInvalid:             {"İmleç geçersiz.", "Invalid cursor."},
	KeyFeedTabInvalid:            {"sekme senin-icin, takip ya da yeni olmalı.", "sekme must be senin-icin, takip or yeni."},
	KeyProfileFeedTypeInvalid:    {"tur sarkilar ya da coverlar olmalı.", "tur must be sarkilar or coverlar."},
	KeyProfileTextNUL:            {"Görünen ad ve biyografi NUL karakteri içeremez.", "Display name and bio cannot contain NUL characters."},
	KeyDisplayNameLength:         {"Görünen ad 1-40 karakter olmalı.", "Display name must be 1-40 characters."},
	KeyBioLength:                 {"Biyografi en çok 160 karakter olabilir.", "Bio can be at most 160 characters."},
	KeyTextFieldsNUL:             {"Metin alanları NUL karakteri içeremez.", "Text fields cannot contain NUL characters."},
	KeyTitleLength:               {"Başlık 1-80 karakter olmalı.", "Title must be 1-80 characters."},
	KeyDescriptionLength:         {"Açıklama en çok 300 karakter olabilir.", "Description can be at most 300 characters."},
	KeyDescriptionNUL:            {"Açıklama NUL karakteri içeremez.", "Description cannot contain NUL characters."},
	KeyDescriptionInvalidChars:   {"Açıklama geçersiz karakter içeriyor.", "Description contains invalid characters."},
	KeyVisibilityInvalid:         {"Görünürlük herkes, takipciler ya da link olmalı.", "Visibility must be herkes, takipciler or link."},
	KeyScaleModeInvalid:          {"ton.mod major ya da minor olmalı.", "ton.mod must be major or minor."},
	KeyTonicEmpty:                {"ton.tonik boş olamaz.", "ton.tonik cannot be empty."},
	KeyGenreLength:               {"Tür 1-40 karakter olmalı.", "Genre must be 1-40 characters."},
	KeyDurationPositive:          {"sureSn sıfırdan büyük olmalı.", "sureSn must be greater than zero."},
	KeyBPMRange:                  {"BPM 20-300 aralığında olmalı.", "BPM must be between 20 and 300."},
	KeyMasterURLRequired:         {"masterUrl zorunludur.", "masterUrl is required."},
	KeyStructureTooLong:          {"Şarkı yapısı çok uzun.", "The song structure is too long."},
	KeyStructureItemInvalid:      {"Şarkı yapısında geçersiz bir öğe var.", "The song structure contains an invalid item."},
	KeyLyricsTooLong:             {"Sözler çok uzun.", "The lyrics are too long."},
	KeyLyricsSectionInvalid:      {"Söz bölümü geçersiz.", "Invalid lyrics section."},
	KeyLyricsLineInvalid:         {"Söz satırı geçersiz.", "Invalid lyrics line."},
	KeyStemRequired:              {"Altyapı paylaşımı açıkken en az bir stem gerekli.", "At least one stem is required when the backing track is shared."},
	KeyTooManyStems:              {"Çok fazla stem var.", "Too many stems."},
	KeyStemIDInvalid:             {"Stem kimliği geçersiz.", "Invalid stem ID."},
	KeyStemIDDuplicate:           {"Stem kimliği tekrar ediyor.", "Duplicate stem ID."},
	KeyStemFieldsInvalid:         {"Stem alanları geçersiz.", "Invalid stem fields."},
	KeyFieldFileURLInvalid:       {"%s: geçersiz dosya adresi.", "%s: invalid file URL."},
	KeyFieldFileNotPrivate:       {"%s: paylaşılmamış (özel) bir dosya olmalı.", "%s: must be a private (unshared) file."},
	KeyCommentTextNUL:            {"Yorum metni NUL karakteri içeremez.", "Comment text cannot contain NUL characters."},
	KeyCommentTextLength:         {"Yorum metni 1-500 karakter olmalı.", "Comment text must be 1-500 characters."},
	KeyCommentTimeNegative:       {"zamanMs negatif olamaz.", "zamanMs cannot be negative."},
	KeyCommentTimeTooLarge:       {"zamanMs çok büyük.", "zamanMs is too large."},
	KeyCommentTimeBeyondDuration: {"zamanMs şarkının süresini aşıyor.", "zamanMs exceeds the song's duration."},
	KeyReportTargetInvalid:       {"hedefTuru yayin ya da yorum olmalı.", "hedefTuru must be yayin or yorum."},
	KeyReportReasonInvalid:       {"neden spam, taciz, telif, uygunsuz ya da diger olmalı.", "neden must be spam, taciz, telif, uygunsuz or diger."},
}
```

Run: `go test -count=1 -v ./internal/i18n`
Expected: `TestFromAcceptLanguage`, `TestMessage` and `TestCatalogComplete` PASS. `ok  github.com/makeasinger/api/internal/i18n`.

- [ ] **Step 3: Switch `pkg/response` to keys**

Replace `core-service/pkg/response/response.go` with:

```go
package response

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
)

// Error codes
const (
	CodeValidationError = "VALIDATION_ERROR"
	CodeUnauthorized    = "UNAUTHORIZED"
	CodeForbidden       = "FORBIDDEN"
	CodeNotFound        = "NOT_FOUND"
	CodeRateLimited     = "RATE_LIMITED"
	CodeJobFailed       = "JOB_FAILED"
	CodeServiceError    = "SERVICE_ERROR"
	CodeAIError         = "AI_ERROR"
)

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// langKey is the c.Locals key holding the request's i18n.Lang.
type langKey struct{}

// Lang returns the message language of the request, parsed once from
// Accept-Language and cached in c.Locals.
func Lang(c *fiber.Ctx) i18n.Lang {
	if l, ok := c.Locals(langKey{}).(i18n.Lang); ok {
		return l
	}
	l := i18n.FromAcceptLanguage(c.Get(fiber.HeaderAcceptLanguage))
	c.Locals(langKey{}, l)
	return l
}

// Error writes {"error":{"code","message","details"}}. code is never
// localized; message is msg rendered in the request's language (args
// fill its verbs). Content-Language names that language and
// Vary: Accept-Language keeps caches from mixing languages.
func Error(c *fiber.Ctx, status int, code string, msg i18n.Key, details interface{}, args ...any) error {
	lang := Lang(c)
	c.Set(fiber.HeaderContentLanguage, string(lang))
	c.Vary(fiber.HeaderAcceptLanguage)
	return c.Status(status).JSON(ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: i18n.Message(lang, msg, args...),
			Details: details,
		},
	})
}

func ValidationError(c *fiber.Ctx, msg i18n.Key, details interface{}) error {
	return Error(c, fiber.StatusBadRequest, CodeValidationError, msg, details)
}

func Unauthorized(c *fiber.Ctx, msg i18n.Key) error {
	return Error(c, fiber.StatusUnauthorized, CodeUnauthorized, msg, nil)
}

func Forbidden(c *fiber.Ctx, msg i18n.Key) error {
	return Error(c, fiber.StatusForbidden, CodeForbidden, msg, nil)
}

func NotFound(c *fiber.Ctx, msg i18n.Key) error {
	return Error(c, fiber.StatusNotFound, CodeNotFound, msg, nil)
}

func RateLimited(c *fiber.Ctx) error {
	return Error(c, fiber.StatusTooManyRequests, CodeRateLimited, i18n.KeyRateLimited, nil)
}

func ServiceError(c *fiber.Ctx, msg i18n.Key) error {
	return Error(c, fiber.StatusInternalServerError, CodeServiceError, msg, nil)
}

func AIError(c *fiber.Ctx, msg i18n.Key) error {
	return Error(c, fiber.StatusBadGateway, CodeAIError, msg, nil)
}

func OK(c *fiber.Ctx, data interface{}) error {
	return c.JSON(data)
}

func Created(c *fiber.Ctx, data interface{}) error {
	return c.Status(fiber.StatusCreated).JSON(data)
}

func Accepted(c *fiber.Ctx, data interface{}) error {
	return c.Status(fiber.StatusAccepted).JSON(data)
}

func NoContent(c *fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}
```

Create `core-service/pkg/response/response_test.go`:

```go
package response

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
)

func TestError_ShapeAndLanguage(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		return ValidationError(c, i18n.KeyFileTooLarge, map[string]any{"maxSize": 1})
	})
	for _, tt := range []struct{ header, lang, msg string }{
		{"", "tr", "Dosya boyutu 50 MB sınırını aşıyor."},
		{"en-GB,en;q=0.8", "en", "The file exceeds the 50 MB limit."},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Language", tt.header)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		var body map[string]map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		e := body["error"]
		if resp.StatusCode != http.StatusBadRequest || e["code"] != CodeValidationError || e["message"] != tt.msg || e["details"] == nil || len(e) != 3 {
			t.Errorf("%q: %d %s", tt.header, resp.StatusCode, raw)
		}
		if got := resp.Header.Get(fiber.HeaderContentLanguage); got != tt.lang {
			t.Errorf("%q: Content-Language = %q", tt.header, got)
		}
	}
}

func TestLang_CachedInLocals(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		first := Lang(c)
		c.Request().Header.Set(fiber.HeaderAcceptLanguage, "tr")
		if second := Lang(c); second != first {
			t.Errorf("second Lang = %q, want cached %q", second, first)
		}
		return c.SendString(string(first))
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "en")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := io.ReadAll(resp.Body); string(raw) != "en" {
		t.Fatalf("Lang = %q", raw)
	}
}
```

Run: `go test -count=1 ./pkg/response` → `ok`.

Then run `go build ./...`. Expected: build errors (`cannot use "..." (untyped string constant) as i18n.Key value`) at every remaining call site. Ruling B1 makes those errors the to-do list for Steps 4–6.

- [ ] **Step 4: Handlers — error helpers, literal keys and the social mapping**

Create `core-service/internal/handler/errors.go`:

```go
package handler

import (
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/pkg/response"
)

// internalError logs an unexpected service error and answers with the
// generic localized 500 SERVICE_ERROR. The error text stays in the log:
// it can carry internal detail and cannot be localized.
func internalError(c *fiber.Ctx, err error) error {
	log.Printf("%s %s: %v", c.Method(), c.Route().Path, err)
	return response.ServiceError(c, i18n.KeyInternal)
}

// aiError is internalError for the AI provider: 502 AI_ERROR.
func aiError(c *fiber.Ctx, err error) error {
	log.Printf("%s %s: %v", c.Method(), c.Route().Path, err)
	return response.AIError(c, i18n.KeyAIUnavailable)
}
```

Apply these diffs. Each one also adds the import `"github.com/makeasinger/api/internal/i18n"`; run gofmt afterwards to sort it.

```diff
diff --git a/core-service/internal/handler/social.go b/core-service/internal/handler/social.go
index 1d15acb..5d892ea 100644
--- a/core-service/internal/handler/social.go
+++ b/core-service/internal/handler/social.go
@@ -7,4 +7,5 @@ import (
 
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/sosyal"
 	"github.com/makeasinger/api/pkg/response"
@@ -110,34 +111,40 @@ var sosyalHatalari = []struct {
 	durum int
 	kod   string
+	mesaj i18n.Key
 }{
-	{sosyal.ErrBulunamadi, fiber.StatusNotFound, "bulunamadi"},
-	{sosyal.ErrProfilYok, fiber.StatusConflict, "profil_yok"},
-	{sosyal.ErrGecersizKullaniciAdi, fiber.StatusUnprocessableEntity, "gecersiz_kullanici_adi"},
-	{sosyal.ErrKullaniciAdiAlinmis, fiber.StatusConflict, "kullanici_adi_alinmis"},
-	{sosyal.ErrDogrulama, fiber.StatusUnprocessableEntity, "dogrulama"},
-	{sosyal.ErrSahiplik, fiber.StatusForbidden, "sahiplik"},
-	{sosyal.ErrKaynakBulunamadi, fiber.StatusNotFound, "kaynak_bulunamadi"},
-	{sosyal.ErrAltyapiKapali, fiber.StatusForbidden, "altyapi_kapali"},
-	{sosyal.ErrStemYok, fiber.StatusUnprocessableEntity, "stem_yok"},
-	{sosyal.ErrDurum, fiber.StatusConflict, "durum"},
-	{sosyal.ErrKendiniTakip, fiber.StatusUnprocessableEntity, "kendini_takip"},
-	{sosyal.ErrYorumKapali, fiber.StatusForbidden, "yorum_kapali"},
-	{sosyal.ErrYetki, fiber.StatusForbidden, "yetki"},
+	{sosyal.ErrBulunamadi, fiber.StatusNotFound, "bulunamadi", i18n.KeySocialNotFound},
+	{sosyal.ErrProfilYok, fiber.StatusConflict, "profil_yok", i18n.KeySocialProfileMissing},
+	{sosyal.ErrGecersizKullaniciAdi, fiber.StatusUnprocessableEntity, "gecersiz_kullanici_adi", i18n.KeySocialUsernameInvalid},
+	{sosyal.ErrKullaniciAdiAlinmis, fiber.StatusConflict, "kullanici_adi_alinmis", i18n.KeySocialUsernameTaken},
+	{sosyal.ErrDogrulama, fiber.StatusUnprocessableEntity, "dogrulama", i18n.KeySocialValidation},
+	{sosyal.ErrSahiplik, fiber.StatusForbidden, "sahiplik", i18n.KeySocialOwnership},
+	{sosyal.ErrKaynakBulunamadi, fiber.StatusNotFound, "kaynak_bulunamadi", i18n.KeySocialSourceNotFound},
+	{sosyal.ErrAltyapiKapali, fiber.StatusForbidden, "altyapi_kapali", i18n.KeySocialBackingClosed},
+	{sosyal.ErrStemYok, fiber.StatusUnprocessableEntity, "stem_yok", i18n.KeySocialNoStems},
+	{sosyal.ErrDurum, fiber.StatusConflict, "durum", i18n.KeySocialBadStatus},
+	{sosyal.ErrKendiniTakip, fiber.StatusUnprocessableEntity, "kendini_takip", i18n.KeySocialSelfFollow},
+	{sosyal.ErrYorumKapali, fiber.StatusForbidden, "yorum_kapali", i18n.KeySocialCommentsDisabled},
+	{sosyal.ErrYetki, fiber.StatusForbidden, "yetki", i18n.KeySocialForbidden},
 }
 
 // sosyalHata, servis hatasini sozlesme govdesine cevirir:
-// {"error":{"code","message"}}. Taninmayan hata 500 sunucu_hatasi olur
-// ve ayrintisi yalnizca loga yazilir.
+// {"error":{"code","message"}}. Alan bazli dogrulama hatasi kendi
+// mesajini tasir; digerleri tablodaki mesaji alir. Taninmayan hata 500
+// sunucu_hatasi olur ve ayrintisi yalnizca loga yazilir.
 func sosyalHata(c *fiber.Ctx, err error) error {
 	if errors.Is(err, sosyal.ErrKimlik) {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
+	}
+	var v *sosyal.ValidationError
+	if errors.As(err, &v) {
+		return response.Error(c, fiber.StatusUnprocessableEntity, "dogrulama", v.Key, nil, v.Args...)
 	}
 	for _, e := range sosyalHatalari {
 		if errors.Is(err, e.hata) {
-			return response.Error(c, e.durum, e.kod, err.Error(), nil)
+			return response.Error(c, e.durum, e.kod, e.mesaj, nil)
 		}
 	}
 	log.Printf("sosyal: %s %s: %v", c.Method(), c.Route().Path, err)
-	return response.Error(c, fiber.StatusInternalServerError, "sunucu_hatasi", "Islem tamamlanamadi", nil)
+	return response.Error(c, fiber.StatusInternalServerError, "sunucu_hatasi", i18n.KeyInternal, nil)
 }
 
@@ -145,4 +152,4 @@ func sosyalHata(c *fiber.Ctx, err error) error {
 // hatasi 400 degil 422 dogrulama'dir.
 func govdeHatasi(c *fiber.Ctx) error {
-	return response.Error(c, fiber.StatusUnprocessableEntity, "dogrulama", "Gecersiz istek govdesi", nil)
+	return response.Error(c, fiber.StatusUnprocessableEntity, "dogrulama", i18n.KeyInvalidBody, nil)
 }
diff --git a/core-service/internal/handler/social_profile.go b/core-service/internal/handler/social_profile.go
index 4f1cda1..663ee96 100644
--- a/core-service/internal/handler/social_profile.go
+++ b/core-service/internal/handler/social_profile.go
@@ -5,4 +5,5 @@ import (
 
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/sosyal"
 	"github.com/makeasinger/api/pkg/response"
@@ -14,9 +15,9 @@ func (h *SosyalHandler) ProfilBen(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	p, err := h.servis.ProfilBen(c.Context(), userID)
 	if errors.Is(err, sosyal.ErrProfilYok) {
-		return response.Error(c, fiber.StatusNotFound, "profil_yok", err.Error(), nil)
+		return response.Error(c, fiber.StatusNotFound, "profil_yok", i18n.KeySocialProfileMissing, nil)
 	}
 	if err != nil {
@@ -30,5 +31,5 @@ func (h *SosyalHandler) ProfilKaydet(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	var g sosyal.ProfilGirdisi
@@ -47,5 +48,5 @@ func (h *SosyalHandler) AdUygun(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	u, err := h.servis.AdUygunMu(c.Context(), userID, c.Query("ad"))
@@ -60,5 +61,5 @@ func (h *SosyalHandler) Profil(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	p, err := h.servis.ProfilGoster(c.Context(), userID, c.Params("ad"))
diff --git a/core-service/internal/handler/social_feed.go b/core-service/internal/handler/social_feed.go
index 57d5c2e..3b8d9b5 100644
--- a/core-service/internal/handler/social_feed.go
+++ b/core-service/internal/handler/social_feed.go
@@ -3,4 +3,5 @@ package handler
 import (
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/sosyal"
 	"github.com/makeasinger/api/pkg/response"
@@ -11,5 +12,5 @@ func (h *SosyalHandler) Akis(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	sayfa, err := h.servis.Akis(c.Context(), userID, c.Query("sekme"), c.Query("imlec"), sosyal.SayfaBoyutu(c.Query("limit")))
@@ -24,5 +25,5 @@ func (h *SosyalHandler) ProfilYayinlari(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	sayfa, err := h.servis.ProfilYayinlari(c.Context(), userID, c.Params("ad"), c.Query("tur"),
@@ -47,5 +48,5 @@ func (h *SosyalHandler) begeni(c *fiber.Ctx, begen bool) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	b, err := h.servis.Begeni(c.Context(), userID, c.Params("id"), begen)
diff --git a/core-service/internal/handler/social_cover.go b/core-service/internal/handler/social_cover.go
index da0d621..a294f0f 100644
--- a/core-service/internal/handler/social_cover.go
+++ b/core-service/internal/handler/social_cover.go
@@ -3,4 +3,5 @@ package handler
 import (
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/sosyal"
 	"github.com/makeasinger/api/pkg/response"
@@ -12,5 +13,5 @@ func (h *SosyalHandler) Coverlar(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	sayfa, err := h.servis.Coverlar(c.Context(), userID, c.Params("id"), c.Query("imlec"), sosyal.SayfaBoyutu(c.Query("limit")))
@@ -26,5 +27,5 @@ func (h *SosyalHandler) CoverBaslat(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	b, err := h.servis.CoverBaslat(c.Context(), userID, c.Params("id"))
diff --git a/core-service/internal/handler/social_report.go b/core-service/internal/handler/social_report.go
index af0b204..81d9b16 100644
--- a/core-service/internal/handler/social_report.go
+++ b/core-service/internal/handler/social_report.go
@@ -3,4 +3,5 @@ package handler
 import (
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/sosyal"
 	"github.com/makeasinger/api/pkg/response"
@@ -12,5 +13,5 @@ func (h *SosyalHandler) Sikayet(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	var g sosyal.SikayetGirdisi
diff --git a/core-service/internal/handler/social_follow.go b/core-service/internal/handler/social_follow.go
index 1560c97..448f910 100644
--- a/core-service/internal/handler/social_follow.go
+++ b/core-service/internal/handler/social_follow.go
@@ -3,4 +3,5 @@ package handler
 import (
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/pkg/response"
 )
@@ -19,5 +20,5 @@ func (h *SosyalHandler) takip(c *fiber.Ctx, takip bool) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	d, err := h.servis.Takip(c.Context(), userID, c.Params("ad"), takip)
@@ -32,5 +33,5 @@ func (h *SosyalHandler) Oneriler(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	o, err := h.servis.Oneriler(c.Context(), userID)
diff --git a/core-service/internal/handler/social_publication.go b/core-service/internal/handler/social_publication.go
index 5cdc2c3..0de3c25 100644
--- a/core-service/internal/handler/social_publication.go
+++ b/core-service/internal/handler/social_publication.go
@@ -3,4 +3,5 @@ package handler
 import (
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/sosyal"
 	"github.com/makeasinger/api/pkg/response"
@@ -12,5 +13,5 @@ func (h *SosyalHandler) YayinOlustur(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	var ist sosyal.YayinIstegi
@@ -29,5 +30,5 @@ func (h *SosyalHandler) Yayin(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	y, err := h.servis.YayinGoster(c.Context(), userID, c.Params("id"))
@@ -42,5 +43,5 @@ func (h *SosyalHandler) Slug(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	y, err := h.servis.SlugGoster(c.Context(), userID, c.Params("slug"))
@@ -55,5 +56,5 @@ func (h *SosyalHandler) YayinGuncelle(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	var yama sosyal.YayinYamasi
@@ -72,5 +73,5 @@ func (h *SosyalHandler) YayinSil(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	if err := h.servis.YayinSil(c.Context(), userID, c.Params("id")); err != nil {
@@ -84,5 +85,5 @@ func (h *SosyalHandler) TekrarDene(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	y, err := h.servis.TekrarDene(c.Context(), userID, c.Params("id"))
@@ -97,5 +98,5 @@ func (h *SosyalHandler) Dosyalar(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	d, err := h.servis.Dosyalar(c.Context(), userID, c.Params("id"))
diff --git a/core-service/internal/handler/social_comment.go b/core-service/internal/handler/social_comment.go
index af3691f..d786fa1 100644
--- a/core-service/internal/handler/social_comment.go
+++ b/core-service/internal/handler/social_comment.go
@@ -3,4 +3,5 @@ package handler
 import (
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/sosyal"
 	"github.com/makeasinger/api/pkg/response"
@@ -11,5 +12,5 @@ func (h *SosyalHandler) Yorumlar(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	sayfa, err := h.servis.Yorumlar(c.Context(), userID, c.Params("id"), c.Query("imlec"), sosyal.SayfaBoyutu(c.Query("limit")))
@@ -25,5 +26,5 @@ func (h *SosyalHandler) YorumYaz(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	var g sosyal.YorumGirdisi
@@ -42,5 +43,5 @@ func (h *SosyalHandler) YorumSil(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	if err := h.servis.YorumSil(c.Context(), userID, c.Params("id")); err != nil {
diff --git a/core-service/internal/handler/account.go b/core-service/internal/handler/account.go
index ed94107..5c5d899 100644
--- a/core-service/internal/handler/account.go
+++ b/core-service/internal/handler/account.go
@@ -5,4 +5,5 @@ import (
 
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/service"
 	"github.com/makeasinger/api/pkg/response"
@@ -30,11 +31,11 @@ func (h *HesapHandler) Sil(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	if err := h.service.Sil(c.Context(), userID); err != nil {
 		if errors.Is(err, service.ErrHesapYok) {
-			return response.NotFound(c, "Hesap bulunamadi")
+			return response.NotFound(c, i18n.KeyAccountNotFound)
 		}
-		return response.ServiceError(c, "Hesap silinemedi")
+		return response.ServiceError(c, i18n.KeyAccountDeleteFailed)
 	}
 	return c.SendStatus(fiber.StatusNoContent)
diff --git a/core-service/internal/handler/files.go b/core-service/internal/handler/files.go
index b702d16..1a147b3 100644
--- a/core-service/internal/handler/files.go
+++ b/core-service/internal/handler/files.go
@@ -7,4 +7,5 @@ import (
 	"github.com/gofiber/fiber/v2"
 	"github.com/makeasinger/api/internal/client"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/makeasinger/api/internal/service"
@@ -38,22 +39,22 @@ func (h *FileHandler) Sign(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	var req model.FileSignRequest
 	if err := c.BodyParser(&req); err != nil {
-		return response.ValidationError(c, "Invalid request body", nil)
+		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
 	}
 	if err := h.validator.Struct(&req); err != nil {
-		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
+		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 	result, err := h.service.Sign(c.Context(), userID, req.URLs)
 	if err != nil {
 		if errors.Is(err, client.ErrYabanciAnahtar) {
-			return response.NotFound(c, "File not found")
+			return response.NotFound(c, i18n.KeyFileNotFound)
 		}
 		if errors.Is(err, client.ErrGecersizURL) {
-			return response.ValidationError(c, "Gecersiz dosya URL'si", nil)
+			return response.ValidationError(c, i18n.KeyInvalidFileURL, nil)
 		}
-		return response.ServiceError(c, "Adres uretilemedi")
+		return response.ServiceError(c, i18n.KeyURLSignFailed)
 	}
 	return response.OK(c, result)
diff --git a/core-service/internal/handler/ws_ticket.go b/core-service/internal/handler/ws_ticket.go
index f8b822e..16755e8 100644
--- a/core-service/internal/handler/ws_ticket.go
+++ b/core-service/internal/handler/ws_ticket.go
@@ -6,4 +6,5 @@ import (
 	"github.com/go-playground/validator/v10"
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/makeasinger/api/internal/service"
@@ -37,12 +38,12 @@ func (h *WSTicketHandler) Issue(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	var req model.WSTicketRequest
 	if err := c.BodyParser(&req); err != nil {
-		return response.ValidationError(c, "Invalid request body", nil)
+		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
 	}
 	if err := h.validator.Struct(&req); err != nil {
-		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
+		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 
@@ -50,7 +51,7 @@ func (h *WSTicketHandler) Issue(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, service.ErrJobNotFound) {
-			return response.NotFound(c, "Job not found")
+			return response.NotFound(c, i18n.KeyJobNotFound)
 		}
-		return response.ServiceError(c, "Bilet uretilemedi")
+		return response.ServiceError(c, i18n.KeyTicketIssueFailed)
 	}
 	return response.OK(c, result)
@@ -62,8 +63,8 @@ func (h *WSTicketHandler) Gate(c *fiber.Ctx) error {
 	err := h.service.Redeem(c.Context(), c.Query("ticket"), c.Params("jobId"))
 	if errors.Is(err, service.ErrBiletGecersiz) {
-		return response.Unauthorized(c, "Gecersiz WebSocket bileti")
+		return response.Unauthorized(c, i18n.KeyTicketInvalid)
 	}
 	if err != nil {
-		return response.ServiceError(c, "Bilet denetlenemedi")
+		return response.ServiceError(c, i18n.KeyTicketCheckFailed)
 	}
 	return c.Next()
diff --git a/core-service/internal/handler/upload.go b/core-service/internal/handler/upload.go
index caa2143..9c59ba6 100644
--- a/core-service/internal/handler/upload.go
+++ b/core-service/internal/handler/upload.go
@@ -7,4 +7,5 @@ import (
 	"github.com/gofiber/fiber/v2"
 	"github.com/makeasinger/api/internal/client"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/service"
 	"github.com/makeasinger/api/pkg/response"
@@ -45,5 +46,5 @@ func (h *UploadHandler) Vocal(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
@@ -51,10 +52,10 @@ func (h *UploadHandler) Vocal(c *fiber.Ctx) error {
 	projectID := c.FormValue("projectId")
 	if projectID == "" {
-		return response.ValidationError(c, "projectId is required", nil)
+		return response.ValidationError(c, i18n.KeyProjectIDRequired, nil)
 	}
 
 	sectionID := c.FormValue("sectionId")
 	if sectionID == "" {
-		return response.ValidationError(c, "sectionId is required", nil)
+		return response.ValidationError(c, i18n.KeySectionIDRequired, nil)
 	}
 
@@ -64,10 +65,10 @@ func (h *UploadHandler) Vocal(c *fiber.Ctx) error {
 	file, err := c.FormFile("file")
 	if err != nil {
-		return response.ValidationError(c, "File is required", nil)
+		return response.ValidationError(c, i18n.KeyFileRequired, nil)
 	}
 
 	// Validate file size
 	if file.Size > maxUploadSize {
-		return response.ValidationError(c, "File size exceeds 50MB limit", map[string]interface{}{
+		return response.ValidationError(c, i18n.KeyFileTooLarge, map[string]interface{}{
 			"maxSize":  maxUploadSize,
 			"fileSize": file.Size,
@@ -90,5 +91,5 @@ func (h *UploadHandler) Vocal(c *fiber.Ctx) error {
 
 	if !validTypes[contentType] {
-		return response.ValidationError(c, "Invalid file type. Supported: WAV, M4A, MP3, AAC", map[string]interface{}{
+		return response.ValidationError(c, i18n.KeyFileTypeInvalid, map[string]interface{}{
 			"contentType": contentType,
 		})
@@ -98,5 +99,5 @@ func (h *UploadHandler) Vocal(c *fiber.Ctx) error {
 	f, err := file.Open()
 	if err != nil {
-		return response.ServiceError(c, "Failed to open file")
+		return response.ServiceError(c, i18n.KeyFileOpenFailed)
 	}
 	defer f.Close()
@@ -106,7 +107,7 @@ func (h *UploadHandler) Vocal(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, client.ErrGecersizSegment) {
-			return response.ValidationError(c, "Invalid projectId or sectionId", nil)
+			return response.ValidationError(c, i18n.KeyUploadIDsInvalid, nil)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
@@ -131,5 +132,5 @@ func (h *UploadHandler) DeleteVocal(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 	takeID := c.Params("takeId")
@@ -137,12 +138,12 @@ func (h *UploadHandler) DeleteVocal(c *fiber.Ctx) error {
 	sectionID := c.Query("sectionId")
 	if takeID == "" || projectID == "" || sectionID == "" {
-		return response.ValidationError(c, "takeId, projectId and sectionId are required", nil)
+		return response.ValidationError(c, i18n.KeyVocalIDsRequired, nil)
 	}
 
 	if err := h.service.DeleteVocal(c.Context(), userID, projectID, sectionID, takeID); err != nil {
 		if errors.Is(err, client.ErrGecersizSegment) {
-			return response.ValidationError(c, "Invalid takeId, projectId or sectionId", nil)
+			return response.ValidationError(c, i18n.KeyVocalIDsInvalid, nil)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
diff --git a/core-service/internal/handler/lyrics.go b/core-service/internal/handler/lyrics.go
index e180dab..e5af356 100644
--- a/core-service/internal/handler/lyrics.go
+++ b/core-service/internal/handler/lyrics.go
@@ -4,4 +4,5 @@ import (
 	"github.com/go-playground/validator/v10"
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/makeasinger/api/internal/service"
@@ -38,14 +39,14 @@ func (h *LyricsHandler) Generate(c *fiber.Ctx) error {
 	var req model.LyricsGenerateRequest
 	if err := c.BodyParser(&req); err != nil {
-		return response.ValidationError(c, "Invalid request body", nil)
+		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
 	}
 
 	if err := h.validator.Struct(&req); err != nil {
-		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
+		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 
 	result, err := h.service.Generate(c.Context(), &req)
 	if err != nil {
-		return response.AIError(c, err.Error())
+		return aiError(c, err)
 	}
 
@@ -70,14 +71,14 @@ func (h *LyricsHandler) Rewrite(c *fiber.Ctx) error {
 	var req model.LyricsRewriteRequest
 	if err := c.BodyParser(&req); err != nil {
-		return response.ValidationError(c, "Invalid request body", nil)
+		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
 	}
 
 	if err := h.validator.Struct(&req); err != nil {
-		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
+		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 
 	result, err := h.service.Rewrite(c.Context(), &req)
 	if err != nil {
-		return response.AIError(c, err.Error())
+		return aiError(c, err)
 	}
 
diff --git a/core-service/internal/handler/render.go b/core-service/internal/handler/render.go
index 1eeaaf6..39920d2 100644
--- a/core-service/internal/handler/render.go
+++ b/core-service/internal/handler/render.go
@@ -6,4 +6,5 @@ import (
 	"github.com/go-playground/validator/v10"
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/makeasinger/api/internal/service"
@@ -40,14 +41,14 @@ func (h *RenderHandler) Start(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
 	var req model.RenderStartRequest
 	if err := c.BodyParser(&req); err != nil {
-		return response.ValidationError(c, "Invalid request body", nil)
+		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
 	}
 
 	if err := h.validator.Struct(&req); err != nil {
-		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
+		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 
@@ -55,7 +56,7 @@ func (h *RenderHandler) Start(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, model.ErrGecersizStemAyari) {
-			return response.ValidationError(c, err.Error(), nil)
+			return response.ValidationError(c, i18n.KeyStemSettingsInvalid, nil)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
@@ -79,10 +80,10 @@ func (h *RenderHandler) Status(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
 	jobID := c.Params("jobId")
 	if jobID == "" {
-		return response.ValidationError(c, "Job ID is required", nil)
+		return response.ValidationError(c, i18n.KeyJobIDRequired, nil)
 	}
 
@@ -90,7 +91,7 @@ func (h *RenderHandler) Status(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, service.ErrJobNotFound) {
-			return response.NotFound(c, "Job not found")
+			return response.NotFound(c, i18n.KeyJobNotFound)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
@@ -114,10 +115,10 @@ func (h *RenderHandler) Result(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
 	jobID := c.Params("jobId")
 	if jobID == "" {
-		return response.ValidationError(c, "Job ID is required", nil)
+		return response.ValidationError(c, i18n.KeyJobIDRequired, nil)
 	}
 
@@ -125,10 +126,10 @@ func (h *RenderHandler) Result(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, service.ErrJobNotFound) {
-			return response.NotFound(c, "Job not found")
+			return response.NotFound(c, i18n.KeyJobNotFound)
 		}
 		if err.Error() == "job not completed" {
-			return response.ValidationError(c, "Job not completed yet", nil)
+			return response.ValidationError(c, i18n.KeyJobNotCompleted, nil)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
@@ -152,10 +153,10 @@ func (h *RenderHandler) Cancel(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
 	jobID := c.Params("jobId")
 	if jobID == "" {
-		return response.ValidationError(c, "Job ID is required", nil)
+		return response.ValidationError(c, i18n.KeyJobIDRequired, nil)
 	}
 
@@ -163,10 +164,10 @@ func (h *RenderHandler) Cancel(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, service.ErrJobNotFound) {
-			return response.NotFound(c, "Job not found")
+			return response.NotFound(c, i18n.KeyJobNotFound)
 		}
 		if err.Error() == "job already completed" {
-			return response.ValidationError(c, "Job already completed", nil)
+			return response.ValidationError(c, i18n.KeyJobAlreadyCompleted, nil)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
diff --git a/core-service/internal/handler/master.go b/core-service/internal/handler/master.go
index 244edbf..f33a96a 100644
--- a/core-service/internal/handler/master.go
+++ b/core-service/internal/handler/master.go
@@ -7,4 +7,5 @@ import (
 	"github.com/gofiber/fiber/v2"
 	"github.com/makeasinger/api/internal/client"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/makeasinger/api/internal/service"
@@ -41,19 +42,19 @@ func (h *MasterHandler) Preview(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
 	var req model.MasterPreviewRequest
 	if err := c.BodyParser(&req); err != nil {
-		return response.ValidationError(c, "Invalid request body", nil)
+		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
 	}
 
 	if err := h.validator.Struct(&req); err != nil {
-		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
+		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 
 	result, err := h.service.Preview(c.Context(), userID, &req)
 	if err != nil {
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
@@ -79,14 +80,14 @@ func (h *MasterHandler) Final(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
 	var req model.MasterFinalRequest
 	if err := c.BodyParser(&req); err != nil {
-		return response.ValidationError(c, "Invalid request body", nil)
+		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
 	}
 
 	if err := h.validator.Struct(&req); err != nil {
-		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
+		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 
@@ -94,10 +95,10 @@ func (h *MasterHandler) Final(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, client.ErrYabanciAnahtar) {
-			return response.NotFound(c, "File not found")
+			return response.NotFound(c, i18n.KeyFileNotFound)
 		}
 		if errors.Is(err, client.ErrGecersizURL) {
-			return response.ValidationError(c, "Gecersiz dosya URL'si", nil)
+			return response.ValidationError(c, i18n.KeyInvalidFileURL, nil)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
@@ -121,10 +122,10 @@ func (h *MasterHandler) Status(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
 	jobID := c.Params("jobId")
 	if jobID == "" {
-		return response.ValidationError(c, "Job ID is required", nil)
+		return response.ValidationError(c, i18n.KeyJobIDRequired, nil)
 	}
 
@@ -132,7 +133,7 @@ func (h *MasterHandler) Status(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, service.ErrJobNotFound) {
-			return response.NotFound(c, "Job not found")
+			return response.NotFound(c, i18n.KeyJobNotFound)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
@@ -156,10 +157,10 @@ func (h *MasterHandler) Result(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
 	jobID := c.Params("jobId")
 	if jobID == "" {
-		return response.ValidationError(c, "Job ID is required", nil)
+		return response.ValidationError(c, i18n.KeyJobIDRequired, nil)
 	}
 
@@ -167,10 +168,10 @@ func (h *MasterHandler) Result(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, service.ErrJobNotFound) {
-			return response.NotFound(c, "Job not found")
+			return response.NotFound(c, i18n.KeyJobNotFound)
 		}
 		if err.Error() == "job not completed" {
-			return response.ValidationError(c, "Job not completed yet", nil)
+			return response.ValidationError(c, i18n.KeyJobNotCompleted, nil)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
diff --git a/core-service/internal/handler/export.go b/core-service/internal/handler/export.go
index 864d0dd..dbcda47 100644
--- a/core-service/internal/handler/export.go
+++ b/core-service/internal/handler/export.go
@@ -7,4 +7,5 @@ import (
 	"github.com/gofiber/fiber/v2"
 	"github.com/makeasinger/api/internal/client"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/makeasinger/api/internal/service"
@@ -42,14 +43,14 @@ func (h *ExportHandler) MP3(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
 	var req model.ExportMP3Request
 	if err := c.BodyParser(&req); err != nil {
-		return response.ValidationError(c, "Invalid request body", nil)
+		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
 	}
 
 	if err := h.validator.Struct(&req); err != nil {
-		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
+		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 
@@ -57,10 +58,10 @@ func (h *ExportHandler) MP3(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, client.ErrYabanciAnahtar) {
-			return response.NotFound(c, "File not found")
+			return response.NotFound(c, i18n.KeyFileNotFound)
 		}
 		if errors.Is(err, client.ErrGecersizURL) {
-			return response.ValidationError(c, "Gecersiz dosya URL'si", nil)
+			return response.ValidationError(c, i18n.KeyInvalidFileURL, nil)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
@@ -86,14 +87,14 @@ func (h *ExportHandler) WAV(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
 	var req model.ExportWAVRequest
 	if err := c.BodyParser(&req); err != nil {
-		return response.ValidationError(c, "Invalid request body", nil)
+		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
 	}
 
 	if err := h.validator.Struct(&req); err != nil {
-		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
+		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 
@@ -101,10 +102,10 @@ func (h *ExportHandler) WAV(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, client.ErrYabanciAnahtar) {
-			return response.NotFound(c, "File not found")
+			return response.NotFound(c, i18n.KeyFileNotFound)
 		}
 		if errors.Is(err, client.ErrGecersizURL) {
-			return response.ValidationError(c, "Gecersiz dosya URL'si", nil)
+			return response.ValidationError(c, i18n.KeyInvalidFileURL, nil)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
@@ -130,14 +131,14 @@ func (h *ExportHandler) Stems(c *fiber.Ctx) error {
 	userID, ok := istekSahibi(c)
 	if !ok {
-		return response.Unauthorized(c, "Kimlik dogrulanamadi")
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
 	}
 
 	var req model.ExportStemsRequest
 	if err := c.BodyParser(&req); err != nil {
-		return response.ValidationError(c, "Invalid request body", nil)
+		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
 	}
 
 	if err := h.validator.Struct(&req); err != nil {
-		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
+		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 
@@ -145,10 +146,10 @@ func (h *ExportHandler) Stems(c *fiber.Ctx) error {
 	if err != nil {
 		if errors.Is(err, client.ErrYabanciAnahtar) {
-			return response.NotFound(c, "File not found")
+			return response.NotFound(c, i18n.KeyFileNotFound)
 		}
 		if errors.Is(err, client.ErrGecersizURL) {
-			return response.ValidationError(c, "Gecersiz dosya URL'si", nil)
+			return response.ValidationError(c, i18n.KeyInvalidFileURL, nil)
 		}
-		return response.ServiceError(c, err.Error())
+		return internalError(c, err)
 	}
 
```

- [ ] **Step 5: Middleware and main**

```diff
diff --git a/core-service/internal/middleware/auth.go b/core-service/internal/middleware/auth.go
index 0548c18..86f281b 100644
--- a/core-service/internal/middleware/auth.go
+++ b/core-service/internal/middleware/auth.go
@@ -9,4 +9,5 @@ import (
 	"github.com/gofiber/fiber/v2"
 	"github.com/golang-jwt/jwt/v5"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/pkg/response"
 )
@@ -135,10 +136,10 @@ func (m *AuthMiddleware) Authenticate() fiber.Handler {
 		authHeader := c.Get("Authorization")
 		if authHeader == "" {
-			return response.Unauthorized(c, "Missing authorization header")
+			return response.Unauthorized(c, i18n.KeyAuthHeaderMissing)
 		}
 
 		parts := strings.SplitN(authHeader, " ", 2)
 		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
-			return response.Unauthorized(c, "Invalid authorization header format")
+			return response.Unauthorized(c, i18n.KeyAuthHeaderInvalid)
 		}
 
@@ -149,10 +150,10 @@ func (m *AuthMiddleware) Authenticate() fiber.Handler {
 		// baska bir dogrulama yolu yok.
 		if m.op == nil {
-			return response.Unauthorized(c, "Invalid or expired token")
+			return response.Unauthorized(c, i18n.KeyTokenInvalid)
 		}
 		sub, eposta, ad, err := m.op.dogrula(tokenString)
 		if err != nil {
 			// Hata metni jeton icerigi tasiyabilir; loglanmaz.
-			return response.Unauthorized(c, "Invalid or expired token")
+			return response.Unauthorized(c, i18n.KeyTokenInvalid)
 		}
 		c.Locals("userId", sub)
diff --git a/core-service/internal/middleware/ratelimit.go b/core-service/internal/middleware/ratelimit.go
index fdba1ee..0ddda92 100644
--- a/core-service/internal/middleware/ratelimit.go
+++ b/core-service/internal/middleware/ratelimit.go
@@ -12,4 +12,5 @@ import (
 
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/pkg/response"
 	"github.com/redis/go-redis/v9"
@@ -31,6 +32,6 @@ func (rl *RateLimiter) Limit(keyPrefix string, maxRequests int, window time.Dura
 // LimitKod, Limit'in asimda genel RATE_LIMITED yerine verilen kodla 429
 // donen bicimidir. Sozlesmesi kendi kodunu isteyen uclar icindir (orn.
-// Kesfet yorumlari: 429 yavasla).
-func (rl *RateLimiter) LimitKod(keyPrefix string, maxRequests int, window time.Duration, kod, mesaj string) fiber.Handler {
+// Kesfet yorumlari: 429 yavasla). mesaj, istegin dilinde yazilir.
+func (rl *RateLimiter) LimitKod(keyPrefix string, maxRequests int, window time.Duration, kod string, mesaj i18n.Key) fiber.Handler {
 	return rl.kullaniciBasina(keyPrefix, maxRequests, window, func(c *fiber.Ctx) error {
 		return response.Error(c, fiber.StatusTooManyRequests, kod, mesaj, nil)
@@ -45,5 +46,5 @@ func (rl *RateLimiter) kullaniciBasina(keyPrefix string, maxRequests int, window
 			// hatadir. Eskiden burada rate limit ATLANIYORDU (return
 			// c.Next()), bu da kota bypass'i demekti.
-			return response.Unauthorized(c, "Kimlik dogrulanamadi")
+			return response.Unauthorized(c, i18n.KeyUnauthenticated)
 		}
 		return rl.say(c, fmt.Sprintf("ratelimit:%s:%s", keyPrefix, userID), maxRequests, window, asim)
@@ -133,5 +134,5 @@ func (rl *RateLimiter) say(c *fiber.Ctx, key string, maxRequests int, window tim
 		c.Set("Retry-After", "5")
 		return response.Error(c, fiber.StatusServiceUnavailable,
-			response.CodeServiceError, "Rate limit denetlenemedi", nil)
+			response.CodeServiceError, i18n.KeyRateLimitUnavailable, nil)
 	}
 	count, err := artir.Result()
@@ -139,5 +140,5 @@ func (rl *RateLimiter) say(c *fiber.Ctx, key string, maxRequests int, window tim
 		c.Set("Retry-After", "5")
 		return response.Error(c, fiber.StatusServiceUnavailable,
-			response.CodeServiceError, "Rate limit denetlenemedi", nil)
+			response.CodeServiceError, i18n.KeyRateLimitUnavailable, nil)
 	}
 
diff --git a/core-service/internal/middleware/deleted_user.go b/core-service/internal/middleware/deleted_user.go
index 1ca25bd..c6c227f 100644
--- a/core-service/internal/middleware/deleted_user.go
+++ b/core-service/internal/middleware/deleted_user.go
@@ -4,4 +4,5 @@ import (
 	"github.com/gofiber/fiber/v2"
 	"github.com/makeasinger/api/internal/hesapisaret"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/pkg/response"
 )
@@ -16,5 +17,5 @@ func SilinmisKullaniciEngeli(denetci hesapisaret.Denetci) fiber.Handler {
 		userID := GetUserID(c)
 		if userID == "" {
-			return response.Unauthorized(c, "Kimlik dogrulanamadi")
+			return response.Unauthorized(c, i18n.KeyUnauthenticated)
 		}
 		silinmis, err := denetci.Silinmis(c.Context(), userID)
@@ -22,8 +23,8 @@ func SilinmisKullaniciEngeli(denetci hesapisaret.Denetci) fiber.Handler {
 			c.Set("Retry-After", "5")
 			return response.Error(c, fiber.StatusServiceUnavailable,
-				response.CodeServiceError, "Hesap durumu denetlenemedi", nil)
+				response.CodeServiceError, i18n.KeyAccountStatusUnavailable, nil)
 		}
 		if silinmis {
-			return response.Unauthorized(c, "Hesap silindi")
+			return response.Unauthorized(c, i18n.KeyAccountDeleted)
 		}
 		return c.Next()
diff --git a/core-service/cmd/server/main.go b/core-service/cmd/server/main.go
index e064718..38be34c 100644
--- a/core-service/cmd/server/main.go
+++ b/core-service/cmd/server/main.go
@@ -3,4 +3,5 @@ package main
 import (
 	"context"
+	"errors"
 	"log"
 	"os"
@@ -25,4 +26,5 @@ import (
 	"github.com/makeasinger/api/internal/handler"
 	"github.com/makeasinger/api/internal/hesapisaret"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/kimlik"
 	"github.com/makeasinger/api/internal/middleware"
@@ -31,4 +33,5 @@ import (
 	ws "github.com/makeasinger/api/internal/websocket"
 	"github.com/makeasinger/api/internal/worker"
+	"github.com/makeasinger/api/pkg/response"
 )
 
@@ -314,5 +317,5 @@ func main() {
 		Yayin: rateLimiter.Limit("sosyal-yayin", cfg.RateLimit.SosyalYayinPerHour, time.Hour),
 		Yorum: rateLimiter.LimitKod("sosyal-yorum", cfg.RateLimit.SosyalYorumPerMin, time.Minute,
-			"yavasla", "Biraz yavasla"),
+			"yavasla", i18n.KeySlowDown),
 		Cover: rateLimiter.Limit("sosyal-cover", cfg.RateLimit.SosyalCoverPerHour, time.Hour),
 	})
@@ -413,18 +416,28 @@ func startWorkerServer(
 }
 
+// customErrorHandler answers errors that reach Fiber (unknown routes,
+// oversized bodies, panics caught by recover) in the shared error shape
+// with code SERVICE_ERROR and a localized message chosen by status.
 func customErrorHandler(c *fiber.Ctx, err error) error {
 	code := fiber.StatusInternalServerError
-	message := "Internal Server Error"
+	msg := i18n.KeyInternal
 
-	if e, ok := err.(*fiber.Error); ok {
+	var e *fiber.Error
+	if errors.As(err, &e) {
 		code = e.Code
-		message = e.Message
+		switch code {
+		case fiber.StatusNotFound:
+			msg = i18n.KeyRouteNotFound
+		case fiber.StatusMethodNotAllowed:
+			msg = i18n.KeyMethodNotAllowed
+		case fiber.StatusRequestEntityTooLarge:
+			msg = i18n.KeyBodyTooLarge
+		case fiber.StatusUpgradeRequired:
+			msg = i18n.KeyUpgradeRequired
+		default:
+			msg = i18n.KeyRequestFailed
+		}
 	}
 
-	return c.Status(code).JSON(fiber.Map{
-		"error": fiber.Map{
-			"code":    "SERVICE_ERROR",
-			"message": message,
-		},
-	})
+	return response.Error(c, code, response.CodeServiceError, msg, nil)
 }
```

- [ ] **Step 6: Social — key-carrying validation errors (44 sites)**

```diff
diff --git a/core-service/internal/social/errors.go b/core-service/internal/social/errors.go
index 1ddc8e3..68e4634 100644
--- a/core-service/internal/social/errors.go
+++ b/core-service/internal/social/errors.go
@@ -3,5 +3,6 @@ package sosyal
 import (
 	"errors"
-	"fmt"
+
+	"github.com/makeasinger/api/internal/i18n"
 )
 
@@ -27,6 +28,20 @@ var (
 )
 
-// dogrulamaHatasi, ErrDogrulama'yi alan bazli bir aciklamayla sarar.
-func dogrulamaHatasi(mesaj string) error {
-	return fmt.Errorf("%w: %s", ErrDogrulama, mesaj)
+// ValidationError is a field-level validation failure. errors.Is matches
+// it against ErrDogrulama (422 dogrulama); the HTTP layer renders Key
+// with Args in the request's language.
+type ValidationError struct {
+	Key  i18n.Key
+	Args []any
+}
+
+func (e *ValidationError) Error() string {
+	return ErrDogrulama.Error() + ": " + i18n.Message(i18n.TR, e.Key, e.Args...)
+}
+
+func (e *ValidationError) Unwrap() error { return ErrDogrulama }
+
+// invalid returns a *ValidationError for key.
+func invalid(key i18n.Key, args ...any) error {
+	return &ValidationError{Key: key, Args: args}
 }
diff --git a/core-service/internal/social/cursor.go b/core-service/internal/social/cursor.go
index baa2b1e..638e226 100644
--- a/core-service/internal/social/cursor.go
+++ b/core-service/internal/social/cursor.go
@@ -4,4 +4,5 @@ import (
 	"encoding/base64"
 	"encoding/json"
+	"github.com/makeasinger/api/internal/i18n"
 	"math"
 	"strconv"
@@ -58,5 +59,5 @@ func imlecCoz(ham string, v any) error {
 	b, err := base64.RawURLEncoding.DecodeString(ham)
 	if err != nil || json.Unmarshal(b, v) != nil {
-		return dogrulamaHatasi("imlec gecersiz")
+		return invalid(i18n.KeyCursorInvalid)
 	}
 	return nil
@@ -74,5 +75,5 @@ func zamanImleciCoz(ham string) (*ZamanImleci, error) {
 	id, ok := kimlikCoz(im.ID)
 	if !ok || im.T.IsZero() {
-		return nil, dogrulamaHatasi("imlec gecersiz")
+		return nil, invalid(i18n.KeyCursorInvalid)
 	}
 	im.ID = id
@@ -91,5 +92,5 @@ func puanImleciCoz(ham string) (*PuanImleci, error) {
 	id, ok := kimlikCoz(im.ID)
 	if !ok || im.N <= 0 {
-		return nil, dogrulamaHatasi("imlec gecersiz")
+		return nil, invalid(i18n.KeyCursorInvalid)
 	}
 	im.ID = id
@@ -121,5 +122,5 @@ func yorumImleciCoz(ham string) (*YorumImleci, error) {
 		((im.G == 0 && im.Z != nil && *im.Z >= 0 && *im.Z <= math.MaxInt32) || (im.G == 1 && im.Z == nil))
 	if !gecerli {
-		return nil, dogrulamaHatasi("imlec gecersiz")
+		return nil, invalid(i18n.KeyCursorInvalid)
 	}
 	im.ID = id
diff --git a/core-service/internal/social/profile_rules.go b/core-service/internal/social/profile_rules.go
index 8444ceb..e370ca2 100644
--- a/core-service/internal/social/profile_rules.go
+++ b/core-service/internal/social/profile_rules.go
@@ -2,4 +2,5 @@ package sosyal
 
 import (
+	"github.com/makeasinger/api/internal/i18n"
 	"hash/fnv"
 	"regexp"
@@ -66,11 +67,11 @@ func profilGirdisiniDuzenle(g ProfilGirdisi) (ProfilGirdisi, error) {
 	g.Bio = strings.TrimSpace(g.Bio)
 	if nulIceriyor(g.GorunenAd, g.Bio) {
-		return g, dogrulamaHatasi("gorunenAd ve bio NUL karakteri iceremez")
+		return g, invalid(i18n.KeyProfileTextNUL)
 	}
 	if n := utf8.RuneCountInString(g.GorunenAd); n < 1 || n > gorunenAdEnUzun {
-		return g, dogrulamaHatasi("gorunenAd 1-40 karakter olmali")
+		return g, invalid(i18n.KeyDisplayNameLength)
 	}
 	if utf8.RuneCountInString(g.Bio) > bioEnUzun {
-		return g, dogrulamaHatasi("bio en cok 160 karakter olabilir")
+		return g, invalid(i18n.KeyBioLength)
 	}
 	return g, nil
diff --git a/core-service/internal/social/service_feed.go b/core-service/internal/social/service_feed.go
index 0098b43..328f09d 100644
--- a/core-service/internal/social/service_feed.go
+++ b/core-service/internal/social/service_feed.go
@@ -3,4 +3,5 @@ package sosyal
 import (
 	"context"
+	"github.com/makeasinger/api/internal/i18n"
 	"time"
 )
@@ -57,5 +58,5 @@ func (s *Servis) Akis(ctx context.Context, kullaniciID, sekme, hamImlec string,
 		return s.seninIcin(ctx, id, hamImlec, limit)
 	}
-	return nil, dogrulamaHatasi("sekme senin-icin, takip ya da yeni olmali")
+	return nil, invalid(i18n.KeyFeedTabInvalid)
 }
 
@@ -112,5 +113,5 @@ func (s *Servis) ProfilYayinlari(ctx context.Context, izleyiciID, ad, tur, hamIm
 		coverlar = true
 	default:
-		return nil, dogrulamaHatasi("tur sarkilar ya da coverlar olmali")
+		return nil, invalid(i18n.KeyProfileFeedTypeInvalid)
 	}
 	imlec, err := zamanImleciCoz(hamImlec)
diff --git a/core-service/internal/social/service_report.go b/core-service/internal/social/service_report.go
index 4dc7352..76e2212 100644
--- a/core-service/internal/social/service_report.go
+++ b/core-service/internal/social/service_report.go
@@ -3,4 +3,5 @@ package sosyal
 import (
 	"context"
+	"github.com/makeasinger/api/internal/i18n"
 	"strings"
 	"unicode/utf8"
@@ -51,15 +52,15 @@ func (s *Servis) Sikayet(ctx context.Context, kullaniciID string, g SikayetGirdi
 	}
 	if g.HedefTuru != HedefYayin && g.HedefTuru != HedefYorum {
-		return dogrulamaHatasi("hedefTuru yayin ya da yorum olmali")
+		return invalid(i18n.KeyReportTargetInvalid)
 	}
 	if !sikayetNedenleri[g.Neden] {
-		return dogrulamaHatasi("neden spam, taciz, telif, uygunsuz ya da diger olmali")
+		return invalid(i18n.KeyReportReasonInvalid)
 	}
 	aciklama := strings.TrimSpace(g.Aciklama)
 	if utf8.RuneCountInString(aciklama) > sikayetAciklamaEnUzun {
-		return dogrulamaHatasi("aciklama en cok 300 karakter olabilir")
+		return invalid(i18n.KeyDescriptionLength)
 	}
 	if nulIceriyor(aciklama) {
-		return dogrulamaHatasi("aciklama gecersiz karakter iceriyor")
+		return invalid(i18n.KeyDescriptionInvalidChars)
 	}
 	hedefID, ok := kimlikCoz(g.HedefID)
diff --git a/core-service/internal/social/service_publication.go b/core-service/internal/social/service_publication.go
index 4a70e43..409353f 100644
--- a/core-service/internal/social/service_publication.go
+++ b/core-service/internal/social/service_publication.go
@@ -6,4 +6,5 @@ import (
 	"errors"
 	"fmt"
+	"github.com/makeasinger/api/internal/i18n"
 	"log"
 	"time"
@@ -62,7 +63,7 @@ func (s *Servis) anahtarCoz(ham, kullaniciID, alan string) (string, error) {
 		return "", ErrSahiplik
 	case err != nil:
-		return "", dogrulamaHatasi(alan + " gecersiz dosya adresi")
+		return "", invalid(i18n.KeyFieldFileURLInvalid, alan)
 	case client.IsPublicKey(key):
-		return "", dogrulamaHatasi(alan + " ozel bir dosya olmali")
+		return "", invalid(i18n.KeyFieldFileNotPrivate, alan)
 	}
 	return key, nil
diff --git a/core-service/internal/social/service_comment.go b/core-service/internal/social/service_comment.go
index 79dd4d1..5fb070d 100644
--- a/core-service/internal/social/service_comment.go
+++ b/core-service/internal/social/service_comment.go
@@ -3,4 +3,5 @@ package sosyal
 import (
 	"context"
+	"github.com/makeasinger/api/internal/i18n"
 	"math"
 	"strings"
@@ -61,16 +62,16 @@ func (s *Servis) YorumYaz(ctx context.Context, kullaniciID, yayinID string, g Yo
 	metin := strings.TrimSpace(g.Metin)
 	if nulIceriyor(metin) {
-		return nil, dogrulamaHatasi("metin NUL karakteri iceremez")
+		return nil, invalid(i18n.KeyCommentTextNUL)
 	}
 	if n := utf8.RuneCountInString(metin); n < 1 || n > yorumEnUzun {
-		return nil, dogrulamaHatasi("metin 1-500 karakter olmali")
+		return nil, invalid(i18n.KeyCommentTextLength)
 	}
 	if g.ZamanMs != nil && *g.ZamanMs < 0 {
-		return nil, dogrulamaHatasi("zamanMs negatif olamaz")
+		return nil, invalid(i18n.KeyCommentTimeNegative)
 	}
 	// zaman_ms sutunu integer'dir; sureSn ust sinirsiz oldugundan sure
 	// denetimi tek basina INSERT tasmasini (500) onlemez.
 	if g.ZamanMs != nil && *g.ZamanMs > math.MaxInt32 {
-		return nil, dogrulamaHatasi("zamanMs cok buyuk")
+		return nil, invalid(i18n.KeyCommentTimeTooLarge)
 	}
 	k, err := s.gorunurYayin(ctx, id, yayinID)
@@ -85,5 +86,5 @@ func (s *Servis) YorumYaz(ctx context.Context, kullaniciID, yayinID string, g Yo
 	}
 	if g.ZamanMs != nil && *g.ZamanMs > k.SureSn*1000 {
-		return nil, dogrulamaHatasi("zamanMs sarkinin suresini asiyor")
+		return nil, invalid(i18n.KeyCommentTimeBeyondDuration)
 	}
 	y, err := s.depo.YorumEkle(ctx, YeniYorum{
diff --git a/core-service/internal/social/publication_rules.go b/core-service/internal/social/publication_rules.go
index 8f598d8..2fcd210 100644
--- a/core-service/internal/social/publication_rules.go
+++ b/core-service/internal/social/publication_rules.go
@@ -2,4 +2,5 @@ package sosyal
 
 import (
+	"github.com/makeasinger/api/internal/i18n"
 	"regexp"
 	"strings"
@@ -57,27 +58,27 @@ func yayinIsteginiDuzenle(ist *YayinIstegi) error {
 	switch {
 	case nulIceriyor(ist.Baslik, ist.Aciklama, ist.Tur, ist.Ton.Tonik, ist.Ton.Mod, ist.MasterURL):
-		return dogrulamaHatasi("metin alanlari NUL karakteri iceremez")
+		return invalid(i18n.KeyTextFieldsNUL)
 	case !runeAraliginda(ist.Baslik, 1, baslikEnUzun):
-		return dogrulamaHatasi("baslik 1-80 karakter olmali")
+		return invalid(i18n.KeyTitleLength)
 	case !runeAraliginda(ist.Aciklama, 0, aciklamaEnUzun):
-		return dogrulamaHatasi("aciklama en cok 300 karakter olabilir")
+		return invalid(i18n.KeyDescriptionLength)
 	case !GorunurlukGecerli(ist.Gorunurluk):
-		return dogrulamaHatasi("gorunurluk herkes, takipciler ya da link olmali")
+		return invalid(i18n.KeyVisibilityInvalid)
 	case ist.Ton.Mod != "major" && ist.Ton.Mod != "minor":
-		return dogrulamaHatasi("ton.mod major ya da minor olmali")
+		return invalid(i18n.KeyScaleModeInvalid)
 	case !runeAraliginda(ist.Ton.Tonik, 1, tonikEnUzun):
-		return dogrulamaHatasi("ton.tonik bos olamaz")
+		return invalid(i18n.KeyTonicEmpty)
 	case !runeAraliginda(ist.Tur, 1, turEnUzun):
-		return dogrulamaHatasi("tur 1-40 karakter olmali")
+		return invalid(i18n.KeyGenreLength)
 	case ist.SureSn <= 0:
-		return dogrulamaHatasi("sureSn sifirdan buyuk olmali")
+		return invalid(i18n.KeyDurationPositive)
 	case ist.Bpm < bpmEnAz || ist.Bpm > bpmEnCok:
-		return dogrulamaHatasi("bpm 20-300 araliginda olmali")
+		return invalid(i18n.KeyBPMRange)
 	case ist.MasterURL == "":
-		return dogrulamaHatasi("masterUrl zorunlu")
+		return invalid(i18n.KeyMasterURLRequired)
 	case len(ist.Yapi) > yapiEnCok:
-		return dogrulamaHatasi("yapi cok uzun")
+		return invalid(i18n.KeyStructureTooLong)
 	case len(ist.Sozler.Bolumler) > bolumEnCok:
-		return dogrulamaHatasi("sozler cok uzun")
+		return invalid(i18n.KeyLyricsTooLong)
 	}
 	if ist.Yapi == nil {
@@ -87,5 +88,5 @@ func yayinIsteginiDuzenle(ist *YayinIstegi) error {
 		if o.Olcu < 0 || nulIceriyor(o.ID, o.Tur) ||
 			!runeAraliginda(o.ID, 1, ogeAlaniEnUzun) || !runeAraliginda(o.Tur, 0, ogeAlaniEnUzun) {
-			return dogrulamaHatasi("yapi ogesi gecersiz")
+			return invalid(i18n.KeyStructureItemInvalid)
 		}
 	}
@@ -98,13 +99,13 @@ func yayinIsteginiDuzenle(ist *YayinIstegi) error {
 		toplamSatir += len(b.Satirlar)
 		if len(b.Satirlar) > satirEnCok || toplamSatir > toplamSatirEnCok {
-			return dogrulamaHatasi("sozler cok uzun")
+			return invalid(i18n.KeyLyricsTooLong)
 		}
 		if nulIceriyor(b.BolumID, b.Tur) || !runeAraliginda(b.BolumID, 0, ogeAlaniEnUzun) ||
 			!runeAraliginda(b.Tur, 0, ogeAlaniEnUzun) {
-			return dogrulamaHatasi("soz bolumu gecersiz")
+			return invalid(i18n.KeyLyricsSectionInvalid)
 		}
 		for _, satir := range b.Satirlar {
 			if nulIceriyor(satir) || !runeAraliginda(satir, 0, satirEnUzun) {
-				return dogrulamaHatasi("soz satiri gecersiz")
+				return invalid(i18n.KeyLyricsLineInvalid)
 			}
 		}
@@ -129,21 +130,21 @@ func yayinIsteginiDuzenle(ist *YayinIstegi) error {
 	}
 	if len(ist.Stemler) == 0 {
-		return dogrulamaHatasi("altyapi acikken en az bir stem gerekli")
+		return invalid(i18n.KeyStemRequired)
 	}
 	if len(ist.Stemler) > stemEnCok {
-		return dogrulamaHatasi("cok fazla stem")
+		return invalid(i18n.KeyTooManyStems)
 	}
 	gorulen := make(map[string]bool, len(ist.Stemler))
 	for _, st := range ist.Stemler {
 		if !stemIDDeseni.MatchString(st.ID) {
-			return dogrulamaHatasi("stem id gecersiz")
+			return invalid(i18n.KeyStemIDInvalid)
 		}
 		if gorulen[st.ID] {
-			return dogrulamaHatasi("stem id tekrar ediyor")
+			return invalid(i18n.KeyStemIDDuplicate)
 		}
 		gorulen[st.ID] = true
 		if st.URL == "" || st.Sure <= 0 || !runeAraliginda(st.Enstruman, 1, enstrumanEnUzun) ||
 			nulIceriyor(st.URL, st.Enstruman) {
-			return dogrulamaHatasi("stem alanlari gecersiz")
+			return invalid(i18n.KeyStemFieldsInvalid)
 		}
 	}
@@ -156,13 +157,13 @@ func yamayiDuzenle(y *YayinYamasi) error {
 		a := strings.TrimSpace(*y.Aciklama)
 		if !runeAraliginda(a, 0, aciklamaEnUzun) {
-			return dogrulamaHatasi("aciklama en cok 300 karakter olabilir")
+			return invalid(i18n.KeyDescriptionLength)
 		}
 		if nulIceriyor(a) {
-			return dogrulamaHatasi("aciklama NUL karakteri iceremez")
+			return invalid(i18n.KeyDescriptionNUL)
 		}
 		y.Aciklama = &a
 	}
 	if y.Gorunurluk != nil && !GorunurlukGecerli(*y.Gorunurluk) {
-		return dogrulamaHatasi("gorunurluk herkes, takipciler ya da link olmali")
+		return invalid(i18n.KeyVisibilityInvalid)
 	}
 	return nil
```

Run: `go build ./... && grep -rn "dogrulamaHatasi" internal` (use the post-rename function name). Expected: the build succeeds and grep finds nothing.

- [ ] **Step 7: Update the existing tests and add the handler-level language test**

```diff
diff --git a/core-service/internal/handler/social_preview_test.go b/core-service/internal/handler/social_preview_test.go
index 7781126..150ccb7 100644
--- a/core-service/internal/handler/social_preview_test.go
+++ b/core-service/internal/handler/social_preview_test.go
@@ -8,4 +8,5 @@ import (
 
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/sosyal"
 	"github.com/makeasinger/api/pkg/response"
@@ -18,5 +19,5 @@ func publicUygulama(s *sahteSosyal, ipLimiti fiber.Handler) *fiber.App {
 	h := NewSosyalHandler(s)
 	h.PublicRotalar(app.Group("/api/public"), ipLimiti)
-	api := app.Group("/api", func(c *fiber.Ctx) error { return response.Unauthorized(c, "auth") })
+	api := app.Group("/api", func(c *fiber.Ctx) error { return response.Unauthorized(c, i18n.KeyUnauthenticated) })
 	h.Rotalar(api.Group("/social"), SosyalLimitleri{Yayin: gecir, Yorum: gecir, Cover: gecir})
 	return app
diff --git a/core-service/internal/handler/social_comment_test.go b/core-service/internal/handler/social_comment_test.go
index f02073a..28c5056 100644
--- a/core-service/internal/handler/social_comment_test.go
+++ b/core-service/internal/handler/social_comment_test.go
@@ -7,4 +7,5 @@ import (
 
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/sosyal"
 	"github.com/makeasinger/api/pkg/response"
@@ -92,5 +93,5 @@ func TestSosyal_YorumSil(t *testing.T) {
 func TestSosyal_YorumLimitiYalnizcaYazmada(t *testing.T) {
 	yavasla := func(c *fiber.Ctx) error {
-		return response.Error(c, fiber.StatusTooManyRequests, "yavasla", "Biraz yavasla", nil)
+		return response.Error(c, fiber.StatusTooManyRequests, "yavasla", i18n.KeySlowDown, nil)
 	}
 	s := &sahteSosyal{
diff --git a/core-service/internal/middleware/ratelimit_ip_test.go b/core-service/internal/middleware/ratelimit_ip_test.go
index ba58eb2..dba4bc9 100644
--- a/core-service/internal/middleware/ratelimit_ip_test.go
+++ b/core-service/internal/middleware/ratelimit_ip_test.go
@@ -10,4 +10,5 @@ import (
 
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/i18n"
 	"github.com/redis/go-redis/v9"
 )
@@ -150,5 +151,5 @@ func TestCanliLimitKod_OzelKodla429(t *testing.T) {
 		c.Locals("userId", "kullanici-yorum")
 		return c.Next()
-	}, NewRateLimiter(rdb).LimitKod("yorum", 1, time.Minute, "yavasla", "Biraz yavasla"),
+	}, NewRateLimiter(rdb).LimitKod("yorum", 1, time.Minute, "yavasla", i18n.KeySlowDown),
 		func(c *fiber.Ctx) error { return c.SendString("tamam") })
 
@@ -167,5 +168,5 @@ func TestLimitKod_KimliksizIstek401(t *testing.T) {
 	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
 	app := fiber.New()
-	app.Get("/uc", NewRateLimiter(rdb).LimitKod("yorum", 1, time.Minute, "yavasla", "x"),
+	app.Get("/uc", NewRateLimiter(rdb).LimitKod("yorum", 1, time.Minute, "yavasla", i18n.KeySlowDown),
 		func(c *fiber.Ctx) error { return c.SendString("tamam") })
 	if durum, _ := ipIstek(t, app, nil); durum != fiber.StatusUnauthorized {
```

No other existing test asserts message text. The tests that check `errors.Is(err, ErrDogrulama)` keep passing through `Unwrap`.

Create `core-service/internal/handler/i18n_test.go`:

```go
package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/sosyal"
)

// TestErrorMessages_FollowAcceptLanguage: the message follows
// Accept-Language (Turkish without it) while status and code stay fixed.
func TestErrorMessages_FollowAcceptLanguage(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		user     string
		status   int
		code     string
		header   string
		wantLang string
		wantMsg  string
	}{
		{"sentinel en", sosyal.ErrProfilYok, sosyalKullanici, http.StatusNotFound, "profil_yok", "en-US,en;q=0.9", "en", "The profile has not been created yet."},
		{"sentinel default tr", sosyal.ErrProfilYok, sosyalKullanici, http.StatusNotFound, "profil_yok", "", "tr", "Profil henüz oluşturulmamış."},
		{"validation args en", &sosyal.ValidationError{Key: i18n.KeyFieldFileURLInvalid, Args: []any{"masterUrl"}}, sosyalKullanici,
			http.StatusUnprocessableEntity, "dogrulama", "en", "en", "masterUrl: invalid file URL."},
		{"validation args tr", &sosyal.ValidationError{Key: i18n.KeyFieldFileURLInvalid, Args: []any{"masterUrl"}}, sosyalKullanici,
			http.StatusUnprocessableEntity, "dogrulama", "tr-TR", "tr", "masterUrl: geçersiz dosya adresi."},
		{"unauthenticated en", nil, "", http.StatusUnauthorized, "UNAUTHORIZED", "de, en;q=0.5", "en", "Authentication failed."},
		{"unauthenticated tr", nil, "", http.StatusUnauthorized, "UNAUTHORIZED", "fr", "tr", "Kimlik doğrulanamadı."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := sosyalUygulamasi(&sahteSosyal{hata: tt.err}, tt.user)
			req := httptest.NewRequest(http.MethodGet, "/api/social/profil/ben", nil)
			if tt.header != "" {
				req.Header.Set("Accept-Language", tt.header)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("body %s: %v", raw, err)
			}
			if resp.StatusCode != tt.status || body.Error.Code != tt.code || body.Error.Message != tt.wantMsg {
				t.Fatalf("got %d %q %q, want %d %q %q", resp.StatusCode, body.Error.Code, body.Error.Message, tt.status, tt.code, tt.wantMsg)
			}
			if got := resp.Header.Get("Content-Language"); got != tt.wantLang {
				t.Errorf("Content-Language = %q, want %q", got, tt.wantLang)
			}
			if got := resp.Header.Get("Vary"); got != "Accept-Language" {
				t.Errorf("Vary = %q", got)
			}
		})
	}
}
```

Run: `go vet ./... && go test -count=1 ./internal/handler ./internal/middleware ./internal/sosyal ./pkg/response ./internal/i18n`
Expected: every package prints `ok`. `TestErrorMessages_FollowAcceptLanguage` passes 6 subtests: same status and code, the English message for `en`, Turkish with no header or with an unsupported one.

- [ ] **Step 8: Regenerate the wire snapshot and review the diff**

```bash
go test -count=1 ./tools/wirecheck 2>&1 | grep '^\s*[-+]' | grep -v '^\s*[-+]STR'   # expected: only the "--- FAIL" header line, no TAG/ROUTE/FILE lines
go test -count=1 ./tools/wirecheck 2>&1 | grep -c '^\s*+STR'                      # expected: 284
go test ./tools/wirecheck -update
git diff --stat tools/wirecheck/testdata/wire.txt
```

Expected: the snapshot goes from 2148 to 2271 lines (verified as 2141 → 2264 before Task 7 added 7 `TAG` lines). There are 161 removed `STR` lines and 284 added ones. **The reviewer checks:**
- No `TAG`, `ROUTE` or `FILE` line changed.
- The removed lines are exactly the old message literals: the literal → key table and the 37 distinct social validation texts (35 plain texts plus the `" gecersiz dosya adresi"` / `" ozel bir dosya olmali"` fragments).
- The removed lines also include 5 lines from the old `customErrorHandler` `fiber.Map`: `"Internal Server Error"`, `"SERVICE_ERROR"`, `"code"`, `"error"`, `"message"`. They are still emitted on the wire through `response.CodeServiceError` and the `ErrorResponse`/`ErrorDetail` JSON tags, which are unchanged (`TAG json:"code"` etc. remain).
- The added lines are only the catalog's key ids and its TR/EN texts.
- Every error code literal (`"dogrulama"`, `"profil_yok"`, `"sunucu_hatasi"`, `"yavasla"`, `"VALIDATION_ERROR"`, …) is still present.

- [ ] **Step 9: Full verification**

```bash
PGPASSWORD=kimlik-test-yerel psql -h localhost -p 55432 -U postgres -c 'CREATE DATABASE en_plan'   # once; skip if it exists
export KIMLIK_TEST_DB_URL='postgres://postgres:kimlik-test-yerel@localhost:55432/en_plan?sslmode=disable'
gofmt -l . | grep -v '^docs/'
go vet ./...
~/go/bin/golangci-lint run ./...
go test -count=1 ./...
```

Expected:
- `gofmt` prints nothing, and `vet` passes.
- Lint prints `0 issues.`
- Every package is `ok`: `e2e`, `auth`, `client`, `config`, `handler`, `hesapisaret` (deletionmark), `i18n`, `kimlik` (identity, ~40s), `middleware`, `model`, `service`, `sosyal` (social), `worker`, `pkg/response`, `tools/wirecheck`.
- With Redis and Postgres up, the only skips are the 4 real-provider tests (`*_RealGroq`, `*_RealSuno`). Verified: 811 tests run, 4 skipped, 0 failed. Task 7 added one more test (`TestRedisJSON_KeysUnchanged`).

- [ ] **Step 10: Commit**

```bash
git add core-service/internal/i18n core-service/internal/handler core-service/internal/middleware \
  core-service/internal/social core-service/cmd/server/main.go core-service/pkg/response \
  core-service/tools/wirecheck/testdata/wire.txt
git status --short   # must print nothing: every change is staged
git commit -F - <<'EOF'
Hata mesajlari Accept-Language'a gore TR/EN

internal/i18n: dil secimi (q degerli Accept-Language, varsayilan tr),
anahtar kataloglu mesajlar ve katalog butunlugu testi. pkg/response
yardimcilari serbest metin yerine i18n.Key alir; Content-Language ve
Vary: Accept-Language yazilir. Sosyal dogrulama hatalari anahtar ve
parametre tasir (422 dogrulama korunur). Servis hata metinleri artik
yanita sizmaz, loglanir. Hata kodlari ve JSON bicimi degismedi;
wirecheck goruntusu yalnizca mesaj metinleri icin guncellendi.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

**Follow-ups (not in this task):**
- Tell mobile and web that `message` is now localized and that `Content-Language` is set. Nothing breaks for them, since they branch on `code`.
- The swagger `docs/` examples are unaffected.

---

### Task 23: Sentinel errors for job-state checks (no more `err.Error() == "..."`)

`handler/render.go` and `handler/master.go` decide between 400 and 500 by comparing `err.Error()` with `"job not completed"` and `"job already completed"`. Those texts come from `fmt.Errorf` in `service/render_service.go` and `service/master_service.go`. Translating them would silently turn a 400 into a 500 (ruling A7). This task replaces them with exported sentinels and `errors.Is`, so Task 26 can translate freely. The sentinel texts stay as they are.

**Files**
- Modify: `internal/service/job_record.go` (two new sentinels next to `ErrJobNotFound`)
- Modify: `internal/service/render_service.go` (`GetResult`, `CancelRender`), `internal/service/master_service.go` (`GetResult`)
- Modify: `internal/handler/render.go` (`Result`, `Cancel`), `internal/handler/master.go` (`Result`)
- Create: `internal/handler/job_state_live_test.go`
- Modify: `tools/wirecheck/testdata/wire.txt` (one `STR` line removed)

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
git status --short
go test -count=1 ./tools/wirecheck
```

- [ ] **Step 2: Write the regression test**

Create `internal/handler/job_state_live_test.go`:

```go
package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// jobStateApp serves render and master result/cancel for user-a, with one
// queued and one finished render job and one queued master job in Redis.
func jobStateApp(t *testing.T) *fiber.App {
	t.Helper()
	rdb := liveRedis(t)
	jobs := []model.Job{
		{ID: "r-queued", Type: model.JobTypeRender, OwnerID: "user-a", Status: model.JobStatusQueued, CreatedAt: time.Now()},
		{ID: "r-done", Type: model.JobTypeRender, OwnerID: "user-a", Status: model.JobStatusSucceeded, CreatedAt: time.Now()},
		{ID: "m-queued", Type: model.JobTypeMaster, OwnerID: "user-a", Status: model.JobStatusQueued, CreatedAt: time.Now()},
	}
	for _, j := range jobs {
		raw, _ := json.Marshal(j)
		if err := rdb.Set(context.Background(), "job:"+j.ID, raw, time.Hour).Err(); err != nil {
			t.Fatalf("write job: %v", err)
		}
	}
	render := NewRenderHandler(service.NewRenderService(rdb, nil, nil, &config.R2Config{}), validator.New())
	master := NewMasterHandler(service.NewMasterService(rdb, nil, nil, &config.R2Config{}), validator.New())
	app := fiber.New()
	app.Get("/render/result/:jobId", asUser("user-a"), render.Result)
	app.Post("/render/cancel/:jobId", asUser("user-a"), render.Cancel)
	app.Get("/master/result/:jobId", asUser("user-a"), master.Result)
	return app
}

func TestJobStateErrors_Are400(t *testing.T) {
	app := jobStateApp(t)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/render/result/r-queued", nil),
		httptest.NewRequest(http.MethodPost, "/render/cancel/r-done", nil),
		httptest.NewRequest(http.MethodGet, "/master/result/m-queued", nil),
	} {
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), `"code":"VALIDATION_ERROR"`) {
			t.Errorf("%s %s = %d %s, want 400 VALIDATION_ERROR", req.Method, req.URL.Path, resp.StatusCode, body)
		}
	}
}
```

```bash
go test -count=1 -run TestJobStateErrors_Are400 -v ./internal/handler
```

Expected: `PASS`. The test pins today's behaviour. It was checked at `de6c92b` (with the pre-rename helper names): changing the service text to `"job not finished"` makes it fail with `GET /render/result/r-queued = 500 ... want 400 VALIDATION_ERROR`.

- [ ] **Step 3: Add the sentinels**

In `internal/service/job_record.go`, directly below `var ErrJobNotFound = errors.New("job not found")`:

```go

// ErrJobNotCompleted is returned when a result is requested before the job
// has succeeded. Handlers answer it with 400.
var ErrJobNotCompleted = errors.New("job not completed")

// ErrJobAlreadyCompleted is returned when a finished job is canceled.
// Handlers answer it with 400.
var ErrJobAlreadyCompleted = errors.New("job already completed")
```

- [ ] **Step 4: Return them from the services and match them in the handlers**

```bash
perl -pi -e 's/return nil, fmt.Errorf\("job not completed"\)/return nil, ErrJobNotCompleted/; s/return nil, fmt.Errorf\("job already completed"\)/return nil, ErrJobAlreadyCompleted/' internal/service/render_service.go internal/service/master_service.go
perl -pi -e 's/if err.Error\(\) == "job not completed" \{/if errors.Is(err, service.ErrJobNotCompleted) {/; s/if err.Error\(\) == "job already completed" \{/if errors.Is(err, service.ErrJobAlreadyCompleted) {/' internal/handler/render.go internal/handler/master.go
grep -rn 'job not completed\|job already completed' --include='*.go' internal
go build ./... && go vet ./internal/service ./internal/handler
go test -count=1 -run TestJobStateErrors_Are400 ./internal/handler
```

Expected:
- `grep` prints exactly the two sentinel declarations in `job_record.go`.
- The build and vet print nothing, and the test passes. `fmt` is still used in both service files.

- [ ] **Step 5: Review and record the snapshot change**

```bash
go test -count=1 ./tools/wirecheck 2>&1 | grep -E '^\s+[-+](STR|TAG|ROUTE|FILE)'
```

Expected, exactly one line (verified):

```
        -STR	"job not completed"
```

The two handler comparisons (`"job not completed"` ×2, `"job already completed"` ×1) were `STR` lines. The two new package-level sentinels are `STR` lines too (ruling A2), so the multiset loses one `"job not completed"`. **The reviewer accepts this line**, then:

```bash
go test ./tools/wirecheck -update
wc -l tools/wirecheck/testdata/wire.txt
```

Expected: `2270` (2271 − 1).

- [ ] **Step 6: Verify**

Run the VERIFY block from Global Constraints.

- [ ] **Step 7: Commit**

```bash
git status --short
git add internal/handler/job_state_live_test.go
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
fix(handler): is durumu hatalari sentinel ve errors.Is ile

render/master Result ve Cancel, servis hata metnini err.Error() ile
karsilastirmak yerine service.ErrJobNotCompleted ve
service.ErrJobAlreadyCompleted ile errors.Is kullanir. Metin cevrilse
de 400 yanit 500 olmaz; TestJobStateErrors_Are400 bunu sabitler.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 24: Translate error, log and test-message strings: infrastructure packages

**Files:** `internal/model`, `internal/config`, `internal/deletionmark`, `internal/auth`, `internal/client`, `internal/middleware`, `pkg/response`, `internal/websocket`, `config.yaml`, `.env.example`

**What to translate (rules for Tasks 24-26)**

Translate into plain English:
- `errors.New` and `fmt.Errorf` texts inside functions. Keep each `%w`, `%v`, `%s`, `%d` and `%q` verb and the argument order.
- Package-level sentinel texts (`var ErrX = errors.New("...")`), unless a "Keep" bullet below covers them. After Task 22 they reach only logs (ruling B7).
- Log messages (`log.Print*`, `log.Fatal*`, `log.Panic*`) and `panic(...)` texts.
- Test messages: `t.Fatal*`, `t.Error*`, `t.Log*`, `t.Skip*` and the like. Keep each verb and argument.
- `t.Run` subtest labels.
- Worker job-error texts written to the job record or to WebSocket progress. They reach clients as the job `error` text, which was accepted as an English-only text (reconciliation ruling R15).

Keep byte-identical:
- Everything in the "Wire strings to keep" tables of Tasks 2-21 for the files of this task: SQL, Redis key formats, error codes, routes, query and form keys, JSON tags, enum values, asynq task and queue names, R2 key layouts, env and config keys, cookie and header names, OAuth/OIDC strings and advisory lock values.
- Test data and fixtures: user names, bios, comment texts, IDs such as `"kullanici-a"`, and literals that mirror wire. They are inputs, not messages.
- `internal/i18n` catalog texts. They are the localized messages.
- Identity hosted-page text (identity glossary §4.7): the templates, the page messages, `ErrSifreKisa`'s text (rendered into `kayit.html`) and the `"authRequestID eksik"` / `"form okunamadi"` / `"id eksik"` 400 bodies. Also keep the OAuth `error_description` `"jeton bu istemci icin verilmemis"` and the env-var substrings that `server_test.go` asserts (§4.9).
- AI prompts and mock content (app glossary §4.10).

**Procedure (each of Tasks 24-26, for its file list)**

1. List the candidates:

   ```bash
   grep -nE '"[^"]*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b[^"]*"' <files>
   ```

   Also read each file once: short Turkish texts such as `"okunamadi: %w"` or `"bekleniyordu"` may not contain a listed word.
2. Translate each candidate that a "Translate" bullet covers. Leave each one that a "Keep" bullet covers.
3. When a text is asserted somewhere, change the assertion in the same step. Check with `grep -rn '<old fragment>' --include='*_test.go' .`. Examples are a `strings.Contains(err.Error(), "...")`, an expected log line, or the `akisCagrisi.tur` labels (social risk 9).
4. Run the guard and review its diff:

   ```bash
   go test -count=1 ./tools/wirecheck 2>&1 | grep -E '^\s+[-+](STR|TAG|ROUTE|FILE)'
   ```

   Allowed lines are `-STR`/`+STR` pairs for translated package-level sentinel texts. Task 24 also has the `FILE` lines for `config.yaml` and `.env.example`. **Any `TAG` or `ROUTE` line, or a `STR` line that is not a translated sentinel, is a mistake: revert it.** Some sentinel texts equal an error code (social `ErrBulunamadi` is `"bulunamadi"`, social risk 9). Translating such a sentinel removes one of two equal `STR` lines. Check that the code literal is still in the snapshot (`grep -c '^STR	"bulunamadi"$' tools/wirecheck/testdata/wire.txt` stays at least 1 after `-update`). The reviewer accepts the list, then run `go test ./tools/wirecheck -update`.
5. Run the VERIFY block. Then commit.

**Also in this task:** translate the comments of `config.yaml` and `.env.example` (lines starting with `#`). Keys, values and env names stay as they are. Their `FILE` hashes (`config-yaml`, `env-example`) change. Those two `-FILE`/`+FILE` pairs are expected in Step 3.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Translate**

```bash
grep -nE '"[^"]*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b[^"]*"' internal/model/*.go internal/config/*.go internal/deletionmark/*.go internal/auth/*.go internal/client/*.go internal/middleware/*.go pkg/response/*.go internal/websocket/*.go config.yaml .env.example
```

Work through the candidates with the rules above. Run the package tests after each file.

- [ ] **Step 3: Review and record the snapshot change**

```bash
go test -count=1 ./tools/wirecheck 2>&1 | grep -E '^\s+[-+](STR|TAG|ROUTE|FILE)'
```

Expected: only the lines allowed by Procedure item 4. **The reviewer accepts them**, then:

```bash
go test ./tools/wirecheck -update
```

- [ ] **Step 4: Verify**

Run the VERIFY block from Global Constraints. The guard passes after the `-update` of Step 3.

- [ ] **Step 5: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor: altyapi paketlerinde hata, log ve test mesajlari Ingilizce

model, config, deletionmark, auth, client, middleware, response ve
websocket paketlerinin hata, log ve test mesajlari Ingilizceye
cevrildi; config.yaml ve .env.example yorumlari da. Anahtarlar,
degerler ve hata kodlari ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 25: Translate error, log and test-message strings: identity

**Files:** `internal/identity` (all `.go` files; templates are not touched)

Follow "What to translate" and "Procedure" in Task 24.

Identity-specific: the identity glossary §4.11 lists what is free to translate: logs, `fmt.Errorf`/`errors.New` except `ErrSifreKisa`, and the `oturumRed` location labels, which only prefix a log line. §4.7, §4.9 and §4.10 list what stays. The rate-limit key names in §4.3 look like the `oturumRed` labels but are Redis keys: keep them.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Translate**

```bash
grep -nE '"[^"]*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b[^"]*"' internal/identity/*.go
```

Work through the candidates with the rules above. Run the package tests after each file.

- [ ] **Step 3: Review and record the snapshot change**

```bash
go test -count=1 ./tools/wirecheck 2>&1 | grep -E '^\s+[-+](STR|TAG|ROUTE|FILE)'
```

Expected: only the lines allowed by Procedure item 4. **The reviewer accepts them**, then:

```bash
go test ./tools/wirecheck -update
```

- [ ] **Step 4: Verify**

Run the VERIFY block from Global Constraints. The guard passes after the `-update` of Step 3.

- [ ] **Step 5: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor(identity): hata, log ve test mesajlari Ingilizce

identity paketinin log, hata ve test mesajlari Ingilizceye cevrildi.
Barindirilan sayfa metinleri, ErrSifreKisa ve OAuth
error_description metni ayni (sayfa yerellestirmesi ayri is).

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 26: Translate error, log and test-message strings: social, service, worker, handler, cmd/server, e2e

**Files:** `internal/social`, `internal/service`, `internal/worker`, `internal/handler`, `cmd/server`, `e2e`

Follow "What to translate" and "Procedure" in Task 24.

Worker job errors: the texts written as the job `error` (render, master and social workers) are translated too (ruling R15). Mention it in the commit message, because clients that display the job `error` see English text from now on.

- [ ] **Step 1: Start from a clean, green tree**

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
git status --short
go test -count=1 ./tools/wirecheck
```

Expected: `git status` prints nothing, and the guard prints `ok`.

- [ ] **Step 2: Translate**

```bash
grep -nE '"[^"]*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b[^"]*"' internal/social/*.go internal/service/*.go internal/worker/*.go internal/handler/*.go cmd/server/*.go e2e/*.go
```

Work through the candidates with the rules above. Run the package tests after each file.

- [ ] **Step 3: Review and record the snapshot change**

```bash
go test -count=1 ./tools/wirecheck 2>&1 | grep -E '^\s+[-+](STR|TAG|ROUTE|FILE)'
```

Expected: only the lines allowed by Procedure item 4. **The reviewer accepts them**, then:

```bash
go test ./tools/wirecheck -update
```

- [ ] **Step 4: Verify**

Run the VERIFY block from Global Constraints. The guard passes after the `-update` of Step 3.

- [ ] **Step 5: Commit**

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor: uygulama katmaninda hata, log ve test mesajlari Ingilizce

social, service, worker, handler, cmd/server ve e2e icindeki hata,
log ve test mesajlari Ingilizceye cevrildi. Is kayitlarina yazilan
hata metinleri de artik Ingilizce. API mesajlari i18n katalogundan
gelir; kodlar ve JSON bicimi ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 27: Documentation: CLAUDE.md, docs/API.md, CI comments

**Files**
- Modify: `CLAUDE.md` (repository root)
- Modify: `core-service/docs/API.md`
- Modify: `.github/workflows/core-service.yml` (comments only)

Wire values stay as they are in every document: routes (`/api/social/yayinlar`, `/giris`, `/baglama`, `/federasyon/*`), Redis keys (`kimlik:silindi:`, `kimlik:hizlimit:`), asynq names (`hesap:temizle`, `sosyal:hazirla`, `sosyal:dosya-sil`, queues `sosyal`, `bakim`), R2 layouts (`sosyal/{yayinId}/`), error codes, env names, the schema `sosyal`, and the table `federe_kimlikler`. Only Go names and file paths change.

- [ ] **Step 1: Update Go names and paths in `CLAUDE.md`**

| Old (in CLAUDE.md today) | New |
|---|---|
| `internal/kimlik/`, `internal/kimlik` | `internal/identity/`, `internal/identity` |
| `internal/sosyal/`, `sosyal/` (layer bullet) | `internal/social/`, `social/` |
| `hesapisaret/`, `hesapisaret.Denetci` | `deletionmark/`, `deletionmark.Checker` |
| `hesap` (handler file), `sosyal*` (handler files), `handler/sosyal.go` | `account`, `social*`, `handler/social.go` |
| `hesap_worker.go`, `sosyal_worker.go` | `account_worker.go`, `social_worker.go` |
| `silinmis.go` | `deleted_user.go` |
| `internal/service/job_kayit.go`, `sahipliJobOku` | `internal/service/job_record.go`, `readOwnedJob` |
| `SosyalHandler.Rotalar`, `SosyalHandler.PublicRotalar` | `SocialHandler.Routes`, `SocialHandler.PublicRoutes` |
| `LimitKod` | `LimitWithCode` |
| `SilinmisKullaniciEngeli`, `middleware.SilinmisKullaniciEngeli` | `DeletedUserGate`, `middleware.DeletedUserGate` |
| `Servis`, `PostgresDepo`, `DetayGorebilir`, `gorunurlukKosulu` | `Service`, `PostgresStore`, `CanSeeDetail`, `visibilityCondition` |
| `client.Kopyalayici` | `client.Copier` |
| `HesapService`, `service.HesapService.Sil` | `AccountService`, `service.AccountService.Delete` |
| `Anahtar(userID)`, `Omur`, `Denetci`, `RedisDenetci` (deletion mark) | `Key(userID)`, `TTL`, `Checker`, `RedisChecker` |
| `hesapisaret.Omur` | `deletionmark.TTL` |
| `YukariAkisDogrulayici` | `UpstreamVerifier` |
| `kimlik.Sunucu.HesapSil` | `identity.Server.DeleteAccount` |
| `client.KullaniciOnekleri`, `client.KullaniciAnahtari` | `client.UserPrefixes`, `client.UserKey` |
| `client.SahipliAnahtarCoz`, `client.SahipliAnahtarVeyaURLCoz`, `client.SosyalAnahtari` | `client.ResolveOwnedKey`, `client.ResolveOwnedKeyOrURL`, `client.SocialKey` |
| `SosyalTemizleyici`, `KullaniciIzleriniSil`, `KullaniciVerisiniSil` | `SocialCleaner`, `DeleteUserTraces`, `DeleteUserData` |
| `SayfaGetSar` | `WrapPageGet` |

```bash
cd "$(git rev-parse --show-toplevel)"
grep -nE 'internal/(kimlik|sosyal|hesapisaret)|hesapisaret|Sosyal[A-Z]|Kimlik[A-Z]|Hesap(Service|Sil)|LimitKod|Silinmis|Kopyalayici|Kullanici(Anahtari|Onekleri)|SahipliAnahtar|SosyalAnahtari|PostgresDepo|\bServis\b|DetayGorebilir|gorunurlukKosulu|sahipliJobOku|job_kayit|YukariAkis|Sunucu|Denetci|\bOmur\b|SayfaGetSar|(hesap|sosyal)_worker|silinmis\.go' CLAUDE.md
```

Expected after the edit: no output. Wire values such as `hesap:temizle` and `sosyal/{yayinId}/` do not match this pattern.

- [ ] **Step 2: Add the naming convention to `CLAUDE.md`**

Add this section after "API response conventions":

```markdown
### Naming and messages

- Go identifiers, file names, comments, log and error texts and test names are English (`TestType_Behavior`). Package names: `identity` (custom OpenID Provider), `social` (Keşfet), `deletionmark` (deleted-account marker).
- The wire contract keeps its Turkish values: routes, JSON/query/form keys, error codes, DB schema and migration file names, Redis keys, asynq task and queue names, R2 key layouts, env and config keys, cookie and header names. `go test ./tools/wirecheck` fails on any change to them; an intended change is recorded with `-update` and reviewed.
- API error `message`s come from `internal/i18n` (TR default, EN by `Accept-Language`); never pass free text to `pkg/response`. Codes do not change with the language.
```

- [ ] **Step 3: Document the message language in `core-service/docs/API.md`**

In "Error Response Format", after the sentence about `details`, add:

```markdown
### Message language

`message` is localized. The server picks Turkish or English from `Accept-Language` (highest q value wins, region subtags are ignored, `q=0` and `*` never select a language) and defaults to Turkish (`tr`). Every error response carries `Content-Language: tr|en` and `Vary: Accept-Language`. Clients must branch on `code`, which never changes with the language. Keys in `details` are not localized: validation `details` keys are the Go field names of the request structs (for example `Genre`).
```

Then update the two example messages to the Turkish default texts from `internal/i18n/catalog.go`:
- In "429 response", `"Rate limit exceeded"` becomes `"Çok fazla istek gönderildi, lütfen biraz bekleyin."`.
- In "Validation error details", `"Validation failed"` becomes `"Doğrulama başarısız."`.

```bash
grep -n 'Rate limit exceeded\|"Validation failed"' core-service/docs/API.md
```

Expected: no example `message` keeps the old English text. The table description "Rate limit exceeded" in "Error Codes" is prose and stays.

- [ ] **Step 4: CI comments**

In `.github/workflows/core-service.yml`, translate the Postgres comment block (above `postgres:`) into English and use the final test names. Its list (`TestUserStoreSozlesmesi/postgres, TestFedereDepoSozlesmesi/postgres, TestJetonDepoPostgres, TestMigrate`) becomes `TestUserStore_Contract/postgres`, `TestFederatedStore_Contract/postgres`, `TestPostgresTokenStore_*` and `TestMigrate_*`. In the Redis comment, `(kimlik)` becomes `(identity)` and `muxKur` becomes `buildMux`. Keep the DSN text, the port, the password and every YAML key. Then check:

```bash
python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/core-service.yml'))" && echo YAML-OK
git diff --stat
```

Expected: `YAML-OK`, and 3 files changed.

- [ ] **Step 5: Commit**

```bash
git status --short
git add CLAUDE.md core-service/docs/API.md .github/workflows/core-service.yml
git commit -F - <<'EOF'
docs: Ingilizce adlar, mesaj dili ve adlandirma kurali

CLAUDE.md yeni paket, dosya ve tanimlayici adlarini kullanir ve
adlandirma/mesaj kuralini anlatir. API.md mesajlarin Accept-Language'a
gore TR/EN oldugunu, Content-Language ve Vary basliklarini ve kodlarin
degismedigini belgeler. CI yorumlari Ingilizce ve guncel test adlariyla.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

### Task 28: Final sweep for leftover Turkish identifiers

This last task proves that nothing Turkish is left in Go names, file names and comments, outside the wire values. It changes code only if the checks find something.

- [ ] **Step 1: Declarations, fields, params and locals**

Create `$T/src/identdump/main.go` with the program below, and a `go.mod` with `module identdump` and `go 1.25`. It prints every declared identifier (package-level names, methods, fields, params, results, locals, labels and import aliases) whose mixedCaps words include a Turkish word.

```go
// Command identdump prints every identifier declared in the Go files under
// a root (package-level names, methods, fields, params, results, locals,
// labels and import aliases) whose mixedCaps words include a Turkish word
// from the list below. Usage: identdump <root>
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const turkish = `acik aciklama ad adi adlari adres aile akis aktar aktarma alan alanlari altyapi anahtar anahtari analiz analizci
aralik ayar ayari ayarlari ayir ayrik bagla baglama baglamasi baglan bakim basarisiz basina baskasi baskasinin baslat baslik
begen begendim begeni begeniyi bekle bekleme beklenen bekleniyor bekleyen ben benim bildiren bilet bir bolme bolum bos bul
butunlesik cagirma cagri cakisma canli capraz cerez cevir cok coverlar coz dalga davul dene deneme denetci denetim denetle depo
depolama desen detay dogrula dogrulama dogrulayici dondur dosya durum durumlari duzen duzeni edilen ediyor ekip ekle engel
enstruman eposta eski esleme esnek federasyon federe formu gecerli gecersiz gecici gecikmeli geri getir girdi giris gizli gonder
gorev goster gorunen gorunurluk govde guncelle hata hatali hatasi havuz hazir hazirla hazirlaniyor hazirlik hedef hep hesap hic
hizlimit icin iddia ihlali iki ilk imlec imza imzala imzali indir indirici ipucu iptal isaret islem istatistik istek istemci iz
izler izleyici jeton kaldir kapali kapat kapsam kapsamlar kavanoz kaydet kaynak kayit kendini kesfet kilit kimlik kod kok kolonlar
konak konum kopya kopyala kopyalayici kosul kullanici kullanim kurallar kurulum kuyruk limitleri liste listele mesaj mesaji metin
miyim muyum neden odak ogeler ogesi oku olcu olustur olusturma omur onay onek oneri onizleme ortam oturum ozel ozet panikleyen
parca parcalar pencere puan referans rengi rota sablon saglayici saglik sahibi sahip sahipli sahiplik sahipsiz sahte sarki
sarkilar satir sayac sayfa sayfalar sayi sayisi secenek secici sekme senin servis ses sicaklik sifre sikayet sil silebilir
silici silindi silinen silinmis silme sinir sistem sonraki sonuc sorgu sorulan sosyal sozler sozlesme sozlesmesi sunucu sure
sureli suresn tahmin tahmini takip takipci tamamla tamamlayici taninmayan tara tarayici tavan taze tazele tekil tekrar temizle
temizleyici temizlik tepe tolerans tonik tonmodu tum ucret ucretli ulasilamaz uret uretim uygulama uygun uygunlugu uzanti uzun
varsayilan veri veritabani yabanci yakalayici yama yanit yapi yardimci yayin yayinda yayinla yaz yazar yeni yeniden yetki yok
yol yorum yuk yukari yukle yukleme yuklenen zaman zamanms zarf zorlayici`

var words = map[string]bool{}

// parts splits a mixedCaps or snake_case name into lower-case words.
func parts(name string) []string {
	var out []string
	var cur []rune
	rs := []rune(name)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range rs {
		switch {
		case r == '_' || unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

func main() {
	for _, w := range strings.Fields(turkish) {
		words[w] = true
	}
	root := os.Args[1]
	fset := token.NewFileSet()
	report := func(id *ast.Ident) {
		if id == nil || id.Name == "_" {
			return
		}
		for _, p := range parts(id.Name) {
			if words[p] {
				pos := fset.Position(id.Pos())
				fmt.Printf("%s:%d:%d\t%s\n", pos.Filename, pos.Line, pos.Column, id.Name)
				return
			}
		}
	}
	fields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				report(n)
			}
		}
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		report(f.Name)
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ImportSpec:
				report(n.Name)
			case *ast.FuncDecl:
				report(n.Name)
				fields(n.Recv)
			case *ast.FuncType:
				fields(n.Params)
				fields(n.Results)
			case *ast.TypeSpec:
				report(n.Name)
			case *ast.StructType:
				fields(n.Fields)
			case *ast.InterfaceType:
				fields(n.Methods)
			case *ast.ValueSpec:
				for _, id := range n.Names {
					report(id)
				}
			case *ast.AssignStmt:
				if n.Tok == token.DEFINE {
					for _, e := range n.Lhs {
						if id, ok := e.(*ast.Ident); ok {
							report(id)
						}
					}
				}
			case *ast.RangeStmt:
				if n.Tok == token.DEFINE {
					if id, ok := n.Key.(*ast.Ident); ok {
						report(id)
					}
					if id, ok := n.Value.(*ast.Ident); ok {
						report(id)
					}
				}
			case *ast.LabeledStmt:
				report(n.Label)
			}
			return true
		})
		return nil
	})
}
```

```bash
cd core-service
export T="${TMPDIR:-/tmp}/en-rename"
(cd "$T/src/identdump" && go build -o "$T/identdump" .)
"$T/identdump" .
```

Expected: no output. Each hit is a declaration that a table or a comments step missed. Rename it with gopls following the term table (Reconciliation rulings), then run the command again. The program was checked against the plan's tables: none of the 2346 final names is flagged, and at `de6c92b` it flags 4076 declarations.

- [ ] **Step 2: File names**

```bash
find . -name '*.go' | grep -E '(kullanici|yayin|sosyal|kimlik|hesap|depo|anahtar|jeton|oturum|sayfa|istek|hata|sahte|canli|sozlesme|baglama|federe|cerez|imlec|akis|takip|yorum|sikayet|onizleme|hazirlik|gorunurluk|isaret|sunucu|istemci|yukari|hizlimit|veritabani|servis|kurallar|butunlesik|temizlik|sahiplik|tazeleme)'
find . -path ./docs -prune -o -type d -print | grep -E 'kimlik|sosyal|hesapisaret'
```

Expected: no output from either command. `internal/identity/sablonlar/` and the migration files keep their names on purpose (ruling R4; identity risk R4; social risk 6), and they are not `.go` files.

- [ ] **Step 3: Comments**

```bash
grep -rnE "//.*\b(ve|ile|icin|bir|bu|degil|olarak|eger|yoksa|sonra|gibi|kadar|veya|ama|cunku|zaten|artik|hic|tum|sadece|yalnizca|olur|olmaz|edilir|yapilir|doner|dondurur|uzerinden|tarafindan|olan|oldugu|gore|daha|ayni|iki|bos|yok|hata|hatasi|kullanici|yayin|sayfa|istek|yanit|anahtar|dosya|bekleniyordu|geldi|olmali|basarisiz|gecersiz|bulunamadi|silinemedi|okunamadi|yazilamadi)\b" --include='*.go' .
```

Expected: no output, except comment lines that quote a wire value or test data on purpose, for example the comment on a SQL constant that names the `sosyal` schema. Translate any other hit.

- [ ] **Step 4: Verify and commit if anything changed**

Run the VERIFY block from Global Constraints. If Steps 1-3 changed nothing, there is no commit. Otherwise:

```bash
git status --short
git add -- $(git diff --name-only)
git commit -F - <<'EOF'
refactor: son Turkce ad ve yorum kalintilari

Son tarama ile bulunan Turkce tanimlayici, dosya adi ve yorumlar
Ingilizceye cevrildi. Kablo goruntusu ayni.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

## Self-review

- **File coverage.** `find core-service -name '*.go'` at `de6c92b` lists 222 files. Each is in exactly one task's file table (renamed or listed as unchanged): Task 2: 15, Task 3: 2, Task 4: 4, Task 5: 19, Task 6: 8, Task 8: 47, Task 13: 55, Task 16: 19, Task 17: 9, Task 18: 2, Task 19: 14, Task 20: 17, Task 21: 11. Task 7 creates `redis_json_test.go`, which Task 8 moves. Tasks 22 and 23 create `internal/i18n/*`, `internal/handler/errors.go`, `internal/handler/i18n_test.go`, `pkg/response/response_test.go` and `internal/handler/job_state_live_test.go`. None of those paths exists before. Non-Go files that move: the identity migrations and templates (Task 8) and the social migration (Task 13). Their names and contents are unchanged.
- **Declaration coverage.** 2346 table rows per task: 2: 10, 3: 34, 4: 36, 5: 94, 6: 71, 9: 122, 10: 25, 11: 261, 12: 473, 13: 180, 14: 393, 15: 212, 16: 117, 17: 81, 18: 60, 19: 31, 20: 125, 21: 21. The `go/ast` dump of all declarations at `de6c92b` finds every old name of an automatic row. A dictionary scan of the remaining Turkish-looking declarations found none that is not in a table. Task 28 repeats the scan on the final tree.
- **No double renames.** No `(package, old name)` appears in two rows, and no old name maps to two new names. Rows that touch the same declaration from two glossaries were merged. Examples: the fakes of cross-package interfaces, and `sahteSosyal`, whose collapsed "every method" row was expanded into 23 explicit rows.
- **Collisions.** After applying all rows, each package scope has unique final names. Task 22's new names were included in the check.
- **Cross-task names.** The interface/implementer pairs in ruling R11 have the same method names on both sides. Tasks 9, 13 and 14 rename them in one step each. Task 22's mapping table, Task 23's code and Task 27's map use the final names from the tables. Task 23's test uses `liveRedis` and `asUser` (Tasks 19 and 20), `NewRenderHandler`/`NewMasterHandler` and `service.NewRenderService`/`NewMasterService` (unchanged).
- **Placeholders.** None. Every rename row has a concrete new name. Locals and parameters are not in the tables on purpose (Global Constraints, Tooling). Their naming rules are in the Design section.
- **Dry run.** The mechanical steps of Tasks 2-21 were executed from this plan's text on a scratch copy of `de6c92b` + Task 1. Those steps are the moves, every table row through `apply_renames.py`, the three interface/implementer cases, the Task 7 tags and the Task 10 template rewrite. The comment and local-variable steps were skipped. Results:
  - Every task built and vetted, and kept the guard green. The snapshot was 2148 lines after Task 7, and only the 4 expected `FILE` lines changed in Task 10.
  - Every completion check reported 0 missed rows, and the golden render was identical.
  - At the end, the full test suite passed with Redis and Postgres up, `golangci-lint` reported 0 issues, and `swag init` changed nothing under `docs/`.
  - The leftovers were only local variables and parameters, which the comments-and-locals steps handle. No package-level name, field, method or file name was left.
  - Task 23 was then run on that tree. Its test passed, and the guard diff was exactly `-STR "job not completed"`.
  - The dry run caused two fixes to the plan. A `gofmt -w` now follows every rename, because gopls does not realign columns. And the script now renames each concrete method right after its interface method, because gopls refuses every rename while the build is broken.
- **Wire.** Each rename task ends with the unchanged guard. Every task that changes the snapshot lists the exact lines: 7 (+7 `TAG`), 10 (4 `FILE` pairs), 22 (the counts in its Step 8), 23 (−1 `STR`), and 24-26 (only translated sentinel `STR` pairs, plus 2 `FILE` pairs in Task 24).


---

## Design (approved 2026-10-12)

_Copied verbatim from the approved brief. The user later added one decision: contract error `message`s become TR/EN by `Accept-Language` (Task 22). Codes, JSON shapes, paths, DB, Redis and asynq names stay._

### Approved design (from the user, 2026-10-12)
- Every Go identifier and file name in core-service becomes English, following Go best practice. That covers package names, types, functions, methods, consts, vars, struct fields, interfaces, test names, file names, comments, log messages and test failure messages.
- The WIRE CONTRACT does NOT change. Mobile, web, DB, Redis and deploys depend on it:
  - JSON/query/form struct tags; URL paths and route params (`/api/social/yayinlar`, `/giris`, `:ad`);
  - error codes (`bulunamadi`, `VALIDATION_ERROR`, OAuth `invalid_grant`);
  - DB schema, table and column names, migration file names and contents (migration versions are recorded by name);
  - Redis key formats and prefixes (`kimlik:silindi:`, `ratelimit:`, `wsbilet:`, `kimlik:hizlimit:`);
  - asynq task type names and queue names (`sosyal:hazirla`, `bakim`, `hesap:temizle`); R2 key layouts (`sosyal/{id}/`, `stems/`);
  - env var and config key names; cookie names; HTML template content and its user-facing text (template FILE names may change if they are only referenced from Go);
  - HTTP header names (`X-Web-Anahtari`); OAuth/OIDC protocol strings; advisory lock values.
- User-facing API error `message`s will become localized (TR/EN via Accept-Language) in a separate task. Do not design that.
- Fixed package renames (use exactly these):

  | Old | New |
  |---|---|
  | `internal/kimlik` | `internal/identity` |
  | `internal/sosyal` | `internal/social` |
  | `internal/hesapisaret` | `internal/deletionmark` |

  All other package names stay (`handler`, `service`, `worker`, `client`, `config`, `auth`, `middleware`, `model`, `websocket`, `response`).

### Go naming rules to apply
- Use mixedCaps and standard initialisms: ID, URL, HTTP, JSON, SQL, API, R2, OIDC, PKCE.
- No stutter: in package `social`, write `social.Service`, not `social.SocialService`, and `social.NewService`.
- Errors are `ErrNotFound`. Interfaces are single-method `-er` names where natural (`Copier`, `PrefixDeleter`).
- Receivers are 1–2 letters, consistent per type.
- Getters have no "Get" prefix unless the repository convention already uses one. Repository methods may use Get/List/Insert/Delete verbs.
- Test names are `TestType_Behavior` in English.
- File names are snake_case English, and `_test.go` files mirror their source file.
- Prefer domain terms from the mobile contract translated consistently:

  | Turkish | English |
  |---|---|
  | yayin | publication |
  | profil | profile |
  | takip | follow |
  | begeni | like |
  | yorum | comment |
  | sikayet | report |
  | imlec | cursor |
  | akis | feed |
  | gorunurluk | visibility |
  | altyapi | backing |
  | kaynak | source |
  | stem | stem |
  | hazirlik | preparation |
  | onizleme | preview |
  | depo | store (repository) |
  | servis | service |
  | kullanici | user |
  | hesap | account |
  | silinme | deletion |
  | sahip | owner |
  | slug | slug |
  | durum | status |
  | kod | code |
  | anahtar | key |
  | jeton | token |
  | oturum | session |
  | federe / yukari akis | federation / upstream |
  | baglama | link (account linking) |
  | hizlimit | ratelimit |
  | sablon | template |
  | sayfa | page |
  | cerez | cookie |
  | istemci | client |
  | sunucu | server |

  Pick one English word per Turkish concept and use it everywhere. Record any new choice in your glossary's "Term choices" section.
