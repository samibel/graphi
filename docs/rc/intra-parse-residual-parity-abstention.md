# Intra/parse residual real-repository parity abstention (2026-08-26, W5.n SW-200)

> **All six intra/parse residual languages — css, hcl, json, markdown, toml,
> yaml — keep `GA-LANG-<lang>-G4` at UNKNOWN at SW-200 close.** This document
> records the abstention and names its mechanism. SW-200 built the instrument
> and ran it, twice per language, on a named machine, today; what it could not
> do is produce a PASS, and it did not manufacture one.
>
> Per SW-200 AC-3 — *"`GA-LANG-<lang>-G4` shall flip to PASS with URI and sha
> per language **only if** SW-201's pin is at v3 measured standard AND the
> two-dispatch parse-determinism agrees; otherwise the row stays UNKNOWN with
> the SW-200-specific reason named"* — the second conjunct **held** and the
> first **did not**. This is the sibling of
> [`cross-file-residual-abstention.md`](cross-file-residual-abstention.md)
> (SW-195, nine languages, one measurement, eight abstentions), and it is
> written in the same shape for the same reason.

## TL;DR

| language | v3 real-repo pin on `main`? | dispatches run | two dispatches agreed? | G4 disposition | reason |
|---|---|---|---|---|---|
| css | **NO** | 2 realrepo + 2 fixture | **YES** (verdict, counts, refusal sets all identical) | **UNKNOWN** | no pin; publication refused; below the FR-7 floor |
| hcl | **NO** | 2 realrepo + 2 fixture | **YES** | **UNKNOWN** | as above |
| json | **NO** (and none on SW-201's branch either) | 2 realrepo + 2 fixture | **YES** | **UNKNOWN** | as above, **plus no fixture at all** — zero exercised rows |
| markdown | **NO** | 2 realrepo + 2 fixture | **YES** | **UNKNOWN** | as above |
| toml | **NO** | 2 realrepo + 2 fixture | **YES** | **UNKNOWN** | as above |
| yaml | **NO** | 2 realrepo + 2 fixture | **YES** | **UNKNOWN** | as above |

Six languages, zero real-repository pins, twenty-four dispatches, zero PASS
rows in the evidence posture. The raw samples are checked in under
[`../eval/runs/2026-08-26-Darwin-ARM64/`](../eval/runs/2026-08-26-Darwin-ARM64/),
one leaf per language, each with its own `environment.json` and `notes.md`.

## 1. The machine, and the date

Everything below was measured on **2026-08-26** on **Apple M2 Max, 12 cores,
64 GiB, darwin/arm64, kernel 25.6.0, go1.26.6**, runner class
`Darwin-ARM64/apple-m2-max`, filesystem apfs, page cache **not** dropped. No
figure in this document was copied from another run, another machine or another
story. Wall-clock figures, where they appear, are labelled wall-clock.

## 2. Three mechanisms refuse, and each was measured

### 2.1 No real-repository pin for any of the six

```
$ jq -r '.entries[] | select(.language=="css" or .language=="hcl" or .language=="json"
    or .language=="markdown" or .language=="toml" or .language=="yaml")
    | "\(.name)\ttier \(.tier)"' corpus/manifest.json
tier1-fixture-hero-css       tier 1
tier1-fixture-hero-hcl       tier 1
tier1-fixture-hero-markdown  tier 1
tier1-fixture-hero-toml      tier 1
tier1-fixture-hero-yaml      tier 1
```

Five tier-1 fixtures (SW-202's hero trees), **no json entry at all**, and
**nothing above tier 1** for any of the six. A tier-1 fixture is not
real-repository evidence, and `internal/parity`'s `AllowLocal` is false in the
production posture precisely so that a row cannot silently fall back to one.

SW-201 — this story's declared dependency, ticket status `done` — landed
five v3 pins (bootstrap `v5.3.8`, opentofu `v1.9.4`, cmark `0.31.2`,
toml-test `v2.2.0`, argo-cd `v3.5.1`) and one honest `no_pin` abstention for
json. **Those commits are not on `main`.** Branch
`sw-201-w5o-corpus-pins-v3-intra-parse` is 75 behind and 4 ahead of `main`,
with no PR — verified with `git merge-base --is-ancestor`. This is the same
blocker SW-203 hit and recorded for the six `GA-LANG-<lang>-G7` rows, and it is
unchanged.

**What lifts it:** SW-201's branch landing on `main`. For json specifically, a
pin has to exist first; SW-201 filed json as a declared gap, not as an
oversight, so lifting json's abstention is new work and not a merge.

### 2.2 Publication is refused while the candidate does not match the bytes

`internal/parity/provenance.go` fails closed when the product binary at HEAD
differs from the candidate's. Measured here, with the project's own
authoritative recipe (`go build -trimpath -buildvcs=false`, `CGO_ENABLED=0`,
`./cmd/graphi`, sha256):

| tree | sha256 of `./cmd/graphi` |
|---|---|
| candidate `9f687849` (`internal/parityreport/report.go`) | `0de6e64d6174f1793efbe8d3d0b2beb6561c3095a965f7ecdac3e86bfef46ebf` |
| `main` @ `9a9d3af` | `af8b971b2bd3e1c1c100b85b3754bf0043563b3d294a1066149534e49488ed15` |

The candidate digest reproduces the one the 2026-08-26 D7 waiver record and the
`D7DEBT-001` filing both published, independently, on a different tree. The HEAD
digest is new — `main` has moved since `5515fda`'s `80da0a15…`.

The consequence is mechanical and is in `parityreport.Report.Finalize`: a
non-empty product diff appends a `not_publishable_because` reason, so
`Publishable` is false, and `cmd/parity`'s `-verdict-diff` and `-counts-diff`
both stop at *"at least one run is NOT publishable — publication refused"* and
exit **2**. AC-1's "two dispatches agreeing at exit 0 on `-verdict-diff` and
`-counts-diff`" is therefore unreachable on this tree **for every family**, not
only this one.

This is `D7DEBT-001`, unpaid. **SW-200 AC-6 forbids this story moving
`parityreport.CandidateSHA`** — the candidate move belongs to SW-188 and, for
the arrears, to `D7DEBT-001`. The constant is untouched by this story.

### 2.3 The family is below the FR-7 completeness floor

`parityreport.Report.Finalize` computes

```go
classesComplete := declaredChangeClasses >= FR7ChangeClasses && …   // FR7ChangeClasses = 15
```

Each of the six languages declares **6** change classes in its SW-199 YAML, and
the dispatch-reachable axis crossing is **2** cells (see §3), so the crossed
count is **12**. Twelve is below fifteen, so `Complete` is false whatever the
rows say — and the refusal string prints the arithmetic verbatim:

```
refused: incomplete run: 12 of 12 declared change classes decided, 0 deferred;
         0 of 0 declared crash conditions decided, 0 deferred; skipped=false,
         harness-error=false (FR-7 requires 15 declared classes)
```

That line is from the **css fixture posture**, where all twelve rows PASSed. A
family whose every row passes is still refused. Filed as **PARITY-011**.

**No axis was invented to clear the floor.** A third axis would take the count
to 18 and the refusal would disappear; the only candidate available
(`-meta` sidecar on disk vs in memory) was considered and rejected, because it
would have been chosen for its arithmetic rather than for what it measures, and
because it is not the "both stores" axis the story's witness shape names.

## 3. What the witness shape could and could not be, at dispatch level

SW-200's witness shape is *"parse twice, byte-stable AST serialisation, **both
stores**, **both profile axes**"*. Two of those four are reachable through the
built binary and two are not, and the split is measured rather than assumed.

| axis | hermetic table (`engine/conformance`) | this dispatch harness | why |
|---|---|---|---|
| store | `{mem, sqlite}` | **`{sqlite}` only** | Every CLI path that persists a graph opens SQLite. `cmd/graphi.openStore` maps an empty `-db` to an in-process `MemStore` whose contents die with the process, and `graphi snapshot create` hardcodes `graphstore.OpenSQLite`. **No dispatch-driven full index can emit a MemStore envelope at all.** |
| profile | `{ingest.New zero value, Balanced}` | `{resolved default, Fast}` | `core/profile.ResolveProfile` maps "no flag, no env" to Balanced, so the library's zero value is unreachable from any CLI; Balanced and Deep are identical since ADR 0010, so Fast is the only behaviourally distinct second rung. This is `jvmAxes()`'s reasoning, verbatim and for the same reason. |

The store row is stated rather than papered over. Relabelling a second SQLite
configuration as "the other store" would be the LANGHONEST-001 circular-claim
shape one axis over, and `TestParseDet_AxisIsTwoCellsAndSaysWhichStore` exists
so that a future edit adding a second `Store` value has to justify itself in a
test.

**AC-5 is therefore met in its byte-stability half and not in its store half.**
The two-dispatch byte-stable discipline holds per language (§4); "both stores"
does not hold at dispatch level and cannot.

## 4. What DID hold: the two-dispatch discipline, per language

Every one of the six languages was dispatched twice per posture, serially, into
separate workdirs. In all twelve pairs the two dispatches produced **identical
verdict sets, identical per-row counts and snapshot digests, and bit-identical
refusal sets**:

| mode | pairs run | exit 0 | exit 2 | what the exit code means here |
|---|---|---|---|---|
| `-verdict-diff` | 12 | 0 | **12** | *"verdict sets agree, but at least one run is NOT publishable — publication refused."* The agreement is real; the exit code is §2.2. |
| `-counts-diff` | 12 | 0 | **12** | *"counts agree, but at least one run is NOT publishable — publication refused."* |
| `-refusal-diff` | 12 | **12** | 0 | *"the two dispatches refuse for bit-identically the same reasons — the refusal is DETERMINISTIC."* Exit 0 here says only that; it never means publishable, and `cmd/parity`'s own comment forbids reading it that way. |

`-refusal-diff` is SW-204's mode and exists for exactly this state: a pair of
runs that both refuse, where the two publication gates say nothing. Its twelve
exit-0 results are the strongest true statement available about this tree, and
they are reported as what they are.

Additionally, the per-row snapshot digests are byte-stable across dispatches in
every language, including json's all-SKIPPED pair:

| language | digest of the sorted (row → full/inc sha) set, dispatch a | dispatch b | |
|---|---|---|---|
| css | `006482459bfa4866` | `006482459bfa4866` | byte-stable |
| hcl | `f4278417c8ba9c86` | `f4278417c8ba9c86` | byte-stable |
| json | `ccdfd36677c7184e` | `ccdfd36677c7184e` | byte-stable |
| markdown | `debe827fab7cc6a4` | `debe827fab7cc6a4` | byte-stable |
| toml | `6a93ed72219e76c7` | `6a93ed72219e76c7` | byte-stable |
| yaml | `feffd02cdde4ec7a` | `feffd02cdde4ec7a` | byte-stable |

## 5. The fixture posture, and why it is not evidence

Alongside the production posture, the driver was run at `-max-tier 1
-allow-local` over SW-202's checked-in hero fixtures. That posture exercises
every code path the real-repo posture would: materialize, plan a real edit,
apply it, baseline index, incremental sync, **two independent full passes**,
emit three snapshot envelopes, byte-compare, count §12.3 store-level figures,
run the non-vacuity witness.

All twelve rows PASSed for css, hcl, markdown, toml and yaml. json's twelve
SKIPPED, because there is no json fixture either.

| language | fixture | rows PASS | example row: full nodes/edges | full-pass sha (12) |
|---|---|---|---|---|
| css | `tier1-fixture-hero-css` | 12/12 | `css_rename_selector` 10/7 | `8f468a91451f` |
| hcl | `tier1-fixture-hero-hcl` | 12/12 | `hcl_rename_block_label` 10/7 | `fbca957cf23b` |
| json | — | 0/12 (all SKIPPED) | — | — |
| markdown | `tier1-fixture-hero-markdown` | 12/12 | `markdown_rename_heading` 10/7 | `83ae8316f2b4` |
| toml | `tier1-fixture-hero-toml` | 12/12 | `toml_rename_table` 10/7 | `d52b335963ae` |
| yaml | `tier1-fixture-hero-yaml` | 12/12 | `yaml_rename_key` 11/8 | `66067c722695` |

**This is not G4 evidence and no G4 row cites it.** A tier-1 fixture is a
fixture; the whole point of `AllowLocal` defaulting false is that a matrix row
which ran on one is not the §12.3 gate. It is recorded because "the instrument
was built and never executed" and "the instrument was built, executed, and
refused for named reasons" are different facts, and only the second one is true.

One observation worth recording rather than discarding: for all five exercised
languages **every row's digest is identical between the `profile=default` and
`profile=fast` cells.** That is consistent with Fast skipping the resolve
passes over languages that have no cross-file resolution to skip, and it is
reported as an observation, not as a proof — a single fixture shape is not a
sample.

## 6. What would lift each abstention, per language

| language | what is owed | owner |
|---|---|---|
| css | SW-201's bootstrap `v5.3.8` pin on `main` | **SW-201** (branch exists, unmerged) |
| hcl | SW-201's opentofu `v1.9.4` pin on `main` | **SW-201** |
| json | a pin to exist at all — SW-201 filed json as an honest `no_pin` abstention, so this is new corpus work, not a merge | **unowned** |
| markdown | SW-201's cmark `0.31.2` pin on `main` | **SW-201** |
| toml | SW-201's toml-test `v2.2.0` pin on `main` | **SW-201** |
| yaml | SW-201's argo-cd `v3.5.1` pin on `main` | **SW-201** |

And, for **all six** regardless of pins:

- `D7DEBT-001` — the candidate move, without which `-verdict-diff` and
  `-counts-diff` cannot exit 0 for any family.
- `PARITY-011` — the FR-7 completeness floor, without which this family cannot
  be `Complete` even with pins and a matching candidate.

Three independent blockers means landing SW-201's branch alone would **not**
turn any of these rows PASS. That is stated here so a future reader does not
budget one merge for a three-part debt.
