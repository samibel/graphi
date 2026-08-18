# ADR 0011 — An `imports` edge targets the package's SOURCE files (the LINK-001 fix)

- Status: **Accepted** (language-GA Wave 0, W0.f-5, 2026-08-19)
- Story: language-GA Wave 0 package order item 1
  ([`../plan/2026-08-wave0-handoff-v1.md`](../plan/2026-08-wave0-handoff-v1.md) §5)
- Spec-Gate: `engine/link/pkgtargets_test.go` (per-language emission + the AC-3
  read-parameter claim) and `engine/conformance/importstargets_test.go`
  (both stores × both index-profile axes), demonstrated **red without the fix**
- Depends: ADR 0009 (module-aware import resolution — it decides WHICH directory
  an import resolves to; this ADR decides which files *within* it are targets)
  and ADR 0010 (removing the Balanced aggregation is what made this defect
  dominant under the shipped default)
- Feeds: the W0.f-5 re-measurement (candidate move + real-repo matrix), which is
  its own change and had **not** run when this ADR was written

## Problem

LINK-001, measured on pinned real repositories
([`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md)): an
`imports` edge targeted **every file node in the resolved directory** —
`README.md`, `.golangci.yml`, `.sh` and `_test.go` included — instead of the
imported package's source files.

`engine/link/index.go` filled `fileNodesByDir` from every `file` node, and
`packageFileNodes` returned that whole list to the Go resolver;
`clausePackageFileNodes` did the same for Python, C# and Rust. On the pinned
clones: cobra carried 44 edges onto `.md`/`.yml` plus 126 onto `_test.go` out of
340; grpc-go carried 2 120 onto `.md`/`.sh` out of 23 575. End-to-end,
`graphi related-files -max-files 12 doc/man_docs.go` on cobra returned 12 items
where 5 were relevant, four of the additions not Go source at all.

A `README.md` is not part of a Go package in any sense the import declaration
expresses, so the edge is **wrong in every profile**. The defect is
pre-existing; ADR 0010 did not introduce it — removing the Balanced
edge-collapse restored the edges the aggregation had been swallowing, which made
a latent wrong-edge class dominant under the shipped default.

## Decision

**An `imports` edge targets the imported package's SOURCE files. Membership is
decided on the FILE EXTENSION, in the resolver, at READ time.**

Three parts, each load-bearing:

1. **Extension, not clause, not registry.** `engine/link/index.go` gains
   `packageFileFilter`, a `func(sourcePath string) bool`, and `extSetFilter`,
   which builds one from a static per-language extension set. Precedent:
   `tsExts` (`engine/link/resolve_typescript.go`).
2. **In the resolver.** Each language supplies its own membership rule:
   `goPackageFile` (`resolve_go.go`), and `binder.pkgTargetExts` for the
   clause-keyed fan-out — `pyPkgExts`, `csharpPkgExts`, `rustPkgExts`.
3. **At read time.** The filter is a parameter of *reading* `fileNodesByDir`
   (`packageFileNodes`, `clausePackageFileNodes`), never a narrowing of what
   `SymbolIndex.Add` records. `fileNodesByDir` also feeds `hasPackage` and
   `DirsForImport`, which must keep reading the UNFILTERED list — see the
   rejected alternatives.

### The per-language rule

| Language | Targets | Note |
|---|---|---|
| Go | `.go`, minus `_test.go` | see the `_test.go` ruling below |
| Python | `.py` **and** `.ipynb` | `test_*.py` is **NOT** excluded |
| C# | `.cs` | |
| Rust | `.rs` | no separate test-file extension exists |
| Java, Kotlin | *unchanged* | one file→`package` edge; no directory fan-out |
| TypeScript family, C, C++, Ruby, PHP, Lua, Bash | *unchanged* | exact target paths via `importFileTargets` |

**The `_test.go` ruling, stated explicitly because it is the one arguable case.**
Test files ARE package members — `go build` compiles them into the package under
`go test` — but they are **not importable**: no import declaration anywhere can
reach a symbol declared in `foo_test.go`. An `imports` edge models the import
declaration, so the importable set is the right one. This is also the ruling
`engine/typeresolve/pkggraph.go:103` already makes for the same question on the
type-checked side, so the heuristic and type-checked layers now agree rather than
contradict. The filter binds the TARGET side only: `foo_test.go` remains a
first-class *importer*, and an external test package's edge onto `foo.go` is
unchanged.

**Case-sensitivity, because the two halves differ on purpose.** The EXTENSION is
matched **case-insensitively**, mirroring `parse.Registry.ParserFor`, which
lowercases before selecting a parser (`core/parse/registry.go` `normalizeExt`) —
that is what decides whether a `file` node exists at all, so a `Main.PY` from a
Windows-authored repository is an indexed Python module and dropping its edges
would be a recall regression this change introduced rather than found. The
`_test.go` suffix is matched **case-sensitively**, because `go/build` does the
same: a file literally named `x_TEST.go` is not a test file to the Go toolchain,
so it stays importable and stays a target.

**Why Python's rule is not Go's.** `.ipynb` is included because a notebook's
committed file node keeps the `.ipynb` path (`engine/ingest/notebook.go`) while
its code cells mint real, resolvable Python symbols — dropping it would delete
working edges. `test_*.py` is NOT excluded because Python test modules are
ordinary importable modules. The Go exclusion rests on non-importability, not on
the word "test"; applying it to Python would be cargo-culting a rule past its
reason.

## Rejected alternatives

- **Filtering at `SymbolIndex.Add`** — the tempting one-liner and the most
  dangerous. `fileNodesByDir` also feeds `hasPackage` (`index.go`) and
  `DirsForImport`, whose invariant permits over- but never under-approximation.
  `hasPackage` answers "does the repo contain this package?", and answering
  "absent" for a directory whose files the emission filter rejects mints a
  phantom external node. `DirsForImport` answers a re-link *scheduling*
  question; under-approximating it freezes an edge permanently — exactly the
  ADR 0009 defect class. Pinned by
  `TestPackageFileFilter_IsAReadParameter_NotAnIndexNarrowing`.
- **A clause-based membership filter** — every tree-sitter parser sets a
  symbol's clause to the directory base name
  (`core/parse/parser_tswalk.go:240-251`) and Markdown/YAML mint real symbols,
  so in gin's `internal/json` a clause filter passes `README.md` straight
  through.
- **Asking `Registry.ParserFor` for a node's language** — the registry's
  contents are build-tag dependent (`graphi_broad` / CGO), which would make
  committed graph bytes depend on how the binary was built.
- **Widening `dependentsOf` over the clause relation** — makes every re-link
  O(repo); already rejected by ADR 0009.

## Consequences

- **Product-byte change.** Graphs lose the `imports` edges that targeted
  non-source files. The parity-matrix candidate moves again; previously measured
  rows stay STALE until re-measured. That measurement is its own change.
- **`neighborhood` changes shape at depth ≥ 2.** `query.Neighborhood`
  (`engine/query/service.go:389`) walks all edge kinds and excludes only
  `package`/`external` nodes (`:483`), so today it reaches `.md`/`.yml` file
  nodes through an `imports` edge and will stop doing so. Whether `file` nodes
  belong in neighborhood traversal at all is a separate product question,
  deliberately not decided here.
- **`related_files` precision improves and its recall narrows** on the same
  operation ADR 0010's review measured regressing.
- **LINK-001's disclosure is retracted in this same change**, per the disclosure
  contract: the readme "Known limits" bullet, the doctor `known-defects` check,
  its test, and the registration in `cmd/graphi/doctor.go`.

### The two honest losses

These are real and are accepted, not worked around. Both have the same cause:
graphi models **no embed and no codegen relation**, so for these files the
directory-fan-out `imports` edge was their ONLY path in the graph. Removing a
wrong edge that was carrying real weight still removes real weight.

1. **`.proto` beside its generated `.pb.go`.** The `.proto` is the authoritative
   source of the generated file, and the only relation that would express it —
   "generated from" — does not exist in the model. After this change the
   `.proto` has no edge to the package it generates.
2. **`testdata/` fixtures and `//go:embed` assets.** An embedded `.tmpl`, `.sql`
   or `.json` is a genuine build input of the package that embeds it, and
   `//go:embed` states so in the source. graphi models no embed relation, so
   those files lose their only graph path too.

Building an embed/codegen relation is its own epic. Recording the loss here is
the alternative to pretending the fix is free.

## Verification

- Red-without / green-with, on both stores and both index-profile axes:
  `engine/conformance/importstargets_test.go` reported
  `[<axis>] app/main.go imports targets = [tax/.golangci.yml tax/README.md tax/tax.go tax/tax_test.go], want [tax/tax.go]`
  on all four axes with the fix reverted, and passes with it.
- Controls: all four build-tag variants of a `package json` directory remain
  targets; `foo_test.go → foo.go` is unchanged; a Python `.ipynb` package member
  remains a target.
- The immune families (Java, Kotlin, TypeScript, C) produce a **byte-identical**
  snapshot across the change — sha256 `6ee30533…` on all four axes before and
  after (`TestImportsEdge_ImmuneLanguagesUnchanged`).
- **NOT re-measured by this change:** the real-repository matrix rows. They
  stand as published until `internal/parity` re-runs on the moved candidate.
