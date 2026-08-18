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

> ## Correction 2026-08-19 — two of the three published "honest losses" cannot occur
>
> Per D6 nothing below is rewritten. §"The honest losses" stands exactly as
> published; **read it through this note**, which corrects it and adds two
> records the published body does not carry.
>
> ### 1. Losses 1 and 2 were asserted without checking the parser registry
>
> A file loses an `imports` edge only if it was a committed `file` node in the
> first place, and a `file` node is minted by a parser's **SymbolExtractor**
> (`core/parse/mapping.go`, `core/parse/extract_go.go`). A file whose extension
> has **no registered parser** returns `parse.ErrNoParser` and is silently
> untracked (`engine/ingest/parsefile.go`); a file whose parser is **parse-only**
> mints no nodes either. Neither was checked when the losses were written.
>
> | Published loss | Corrected status, measured |
> |---|---|
> | 1. `.proto` beside its generated `.pb.go` | **Cannot occur.** No `.proto` parser is registered (`core/parse/defaults.go`), so a `.proto` is never a `file` node and was never an `imports` target. It loses nothing because it had nothing. |
> | 2a. `testdata/` fixtures | **Cannot occur.** `testdata/` is a *different directory* from the one the import resolves to, so its files were never targets of an import of the parent package — before this change or after it. |
> | 2b. `//go:embed` assets, illustrated by `.tmpl` / `.json` | **Half true, and the two illustrations are the wrong ones.** `.tmpl` has no registered parser; `.json` has one but it is **parse-only** (`JSONParser.ExtractsSymbols() == false`, `core/parse/parser_json.go`), so neither is ever a `file` node. An embedded `.sql`, `.md`, `.yml`/`.yaml`, `.toml`, `.css` or `.sh` in the package directory **is** a node and **is** a real loss. |
> | 3. A cgo package's `.c` / `.h` | **True as published.** Confirmed. |
>
> ### 2. The losses, restated as the two a user can actually hit
>
> **(a) Parseable non-Go build inputs sitting in the package directory** — a
> `.sql`, `.md`, `.yml`/`.yaml`, `.toml`, `.css` or `.sh`, whether pulled in by
> `//go:embed` or merely co-located. Their parsers extract symbols, so they are
> committed `file` nodes, and the directory-fan-out `imports` edge was their only
> path. **(b) A cgo package's `.c`/`.h` sources**, as published in loss 3.
>
> **The operation-level consequence is part of the loss, not a footnote.**
> `related_files` on a `.md` / `.yml` **anchor** now answers an explicit empty
> outcome — that inbound `imports` edge was the file node's only cross-file path
> in either direction. It is the consequence a user is most likely to meet, and
> it is now stated on the readme surface as well as here. Pinned by
> `TestRelatedFiles_MarkdownAnchorLosesItsOnlyPath`.
>
> **Measured, not reasoned.** Two binaries (`facc014` pre-fix, `3b8d43f`
> post-fix) over one fixture repo whose `tax/` package directory holds
> `tax.go`, `tax_test.go`, `README.md`, `.golangci.yml`, `values.yaml`,
> `config.toml`, `style.css`, `deploy.sh`, `gen.sql`, `helper.c`, `helper.h`,
> `rates.json`, `api.proto`, `page.tmpl`, `notes.txt` and a `testdata/` holding
> `golden.go` + `case.yml`. Pre-fix, `graphi related-files app/main.go` returned
> **11** targets — every one of the above **except** `rates.json`, `api.proto`,
> `page.tmpl`, `notes.txt` and everything under `testdata/`, none of which
> resolve as a node at all (`related_files: no symbol or file matched`). Post-fix
> it returns `tax/tax.go` alone. The dropped set is exactly loss (a) + loss (b).
>
> ### 3. Two records the published body does not carry
>
> - **`.pyi`'s exclusion is currently unobservable, not a measured choice.** The
>   registered Python parser claims only `.py` (`core/parse/parser_python.go`),
>   so a `.pyi` is never a `file` node and never was an `imports` target — at
>   this commit the exclusion costs and saves exactly nothing. The reasoning for
>   it (a stub declares no runtime module; `import foo` never binds `foo.pyi`;
>   its symbols would duplicate the `.py` beside it) stands on the merits and
>   should be re-decided **with a measurement** if a Python parser ever claims
>   `.pyi`.
> - **`Foo_Test.GO` remains a target — a known corner, deliberately not closed.**
>   The two halves of `goPackageFile` differ in case-sensitivity on purpose (see
>   §"Case-sensitivity"), and their combination admits `Foo_Test.GO`: the
>   extension matches case-insensitively, the `_test.go` suffix does not match at
>   all. `go/build` would not treat that file as Go source in the first place, so
>   there is no ground truth being violated, and the behaviour is pre-existing
>   rather than introduced here. Closing it would mean deciding what `go/build`
>   means on a case-insensitive filesystem, which is a larger question than this
>   ADR. Measured post-fix on the fixture above: `tax/Foo_Test.GO` is still a
>   target.

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
| SQL | *unchanged* | its resolver deliberately resolves nothing, so it emits no `imports` edge to filter |

`.ipynb` appears in Python's set only. A notebook's language is its kernel
language and ingest links per language, so a Rust or C# notebook could not be an
`imports` target today. That is a deliberate non-decision, not an oversight:
neither has been observed, and adding an extension on speculation is how a
membership rule stops being reviewable.

**The `_test.go` ruling, stated explicitly because it is the one arguable case.**
Test files ARE package members — `go build` compiles them into the package under
`go test` — but they are **not importable**: no import declaration anywhere can
reach a symbol declared in `foo_test.go`. An `imports` edge models the import
declaration, so the importable set is the right one. This is also the ruling
`engine/typeresolve/pkggraph.go:103` already makes for the same question on the
type-checked side, so the two layers agree **on the `_test.go` question** — the
one this ADR decides. *Corrected in review, so the claim is not read wider than
it is true:* they still differ on case, because `pkggraph.go:100` matches `.go`
case-**sensitively** while this filter does not (see below), so a `Helper.GO` is
an `imports` target that typeresolve skips. That is a pre-existing typeresolve
scoping choice, not something this change introduced, and it is not reopened
here. The filter binds the TARGET side only: `foo_test.go` remains a
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
  contents are build-tag dependent (`graphi_broad` / CGO), so an edge SET would
  become a function of how the binary was built. *Stated precisely after review,
  because the first phrasing overclaimed:* which files become `file` nodes at
  all already depends on that same registry, so the registry is not a
  build-independent/dependent boundary in general. The point is narrower and
  still decisive — a **static** list keeps the membership rule reviewable in one
  place and independent of a registry that a build tag can grow underneath it,
  and it is what makes the per-language reasoning above (`.ipynb` in, `.pyi`
  out) an explicit decision rather than a side effect.
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
- **An existing index needs `graphi rebuild`, not `graphi sync`.** Verified with
  both binaries on one fixture: after upgrading, `sync` reports "up to date" and
  the wrong edges survive, because drift is content-hash based and no source
  file changed, so nothing is re-linked. This is a property of every linker-rule
  change, not of this one, but it is the kind of thing that is only obvious in
  hindsight — a reader who assumes "new binary ⇒ corrected graph" is wrong.

### The honest losses

> **CORRECTED 2026-08-19 — see the correction block at the top of this ADR.**
> Losses 1 and 2 below cannot occur; loss 3 stands. Nothing in this section is
> rewritten, per D6.

These are real and are accepted, not worked around. They share one cause:
graphi models **no embed, codegen or cgo relation**, so for these files the
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
3. **A cgo package's `.c` / `.h` sources.** *Added in review: the ticket named
   two losses and this is a third the exploration missed.* The C parser claims
   `.c` and `.h` (`core/parse/parser_c.go`), so those files ARE committed file
   nodes, and in a cgo package they are genuine build inputs of the Go package
   beside them. `goPackageFile` admits only `.go`, so they lose their path
   exactly as the `.proto` does. Cgo packages are not rare.

**An operation-level consequence, stated because it is not obvious from the
edge-level rule.** `related_files` accepts a repo-relative PATH anchor, and a
Markdown file mints only intra-file symbols while same-file neighbours are
skipped — so the inbound `imports` edge was a `.md`/`.yml` file node's only
cross-file path *in either direction*. `graphi related-files README.md` now
returns an explicit empty outcome where it used to list the importing packages.
Pinned by `TestRelatedFiles_MarkdownAnchorLosesItsOnlyPath`, which asserts the
LOSS deliberately: it is a recorded fact for a reviewer to weigh, not something
a user should discover.

Building an embed/codegen/cgo relation is its own epic. Recording the losses
here is the alternative to pretending the fix is free.

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
