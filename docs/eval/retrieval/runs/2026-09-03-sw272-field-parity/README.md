# SW-272 — field-parity and FTS5 operator control

This is a dev-only diagnostic over every frozen Cobra `nl_behaviour` dev query (`cb-11` through
`cb-16`). It contains no holdout. The primary dataset keeps only exact grade-3 qrels: grade-1/2
qrels are absent, `relevant_min_grade` is 3, and NDCG therefore uses the same exact-answer
evidence as Top-1, recall, MRR, and first-relevant rank. Every result below has resolution
**n=6; one query is 1/6 (16.7%) of the measured population**. Decimal places are retained for
reproduction, not as a claim of statistical precision.

## Corrected verdict on the original all-terms run

> The field-parity question remains unresolved. SW-272 established that the production
> all-terms-prefix FTS query is degenerate on all six measured natural-language questions:
> neither name-only nor full-document indexing admitted any candidate. It therefore cannot
> distinguish document-field effects from query-semantics failure or support a
> BM25-versus-semantic conclusion.

Field asymmetry was **not refuted** by the original comparison. It was untested because both
lexical cells hit the empty-set floor (resolution: n=6; one query is 1/6).

The added disjunctive operator control does admit candidates. In the measured cells, full-v3
raises exact-grade-3 NDCG@10 from 0.1052 to 0.4944 (+0.3892) and first-relevant-found from 1/6 to
6/6. First-relevant rank improves on 5/6 questions and worsens on 1/6. Thus **the full-v3 cell
helps this disjunctive ranker on this pinned six-query diagnostic** (resolution: n=6; one query
is 1/6). This is not a general lexical-versus-semantic result or a CoIR comparison.

There is one attribution limit visible in the provenance: the production qualified-name FTS
table contains all 938 graph nodes, while the full-v3 table contains the 768 documents admitted
by the static model. The control holds each row's table, tokenizer, bytes, driver, BM25 call, and
ranking SQL fixed while changing only AND to OR; the observed cross-row improvement is therefore
the effect of the measured full-v3 cell as a whole, not a pure same-universe estimate of text
fields alone.

## Aggregate 2×3 — exact grade 3

NDCG@10 (resolution: n=6; one query is 1/6):

| Indexed text | `fts_all_terms_prefix` | `fts5_or_control` | Static semantic |
|---|---:|---:|---:|
| Qualified name only | 0.0000 | 0.1052 | 0.5696 |
| Full admitted v3 document | 0.0000 | 0.4944 | 0.7103 |
| Full − name | +0.0000 | **+0.3892** | +0.1407 |

All aggregate scorer outputs (resolution: n=6; one query is 1/6):

| Cell | Top-1 | Recall@5 | Recall@10 | MRR@10 | NDCG@10 | First relevant found | Mean rank when found |
|---|---:|---:|---:|---:|---:|---:|---:|
| Name all-terms (`lexical`) | 0.0000 | 0.0000 | 0.0000 | 0.0000 | 0.0000 | 0/6 | n/a |
| Name OR (`fts5_or_control`) | 0.0000 | 0.1667 | 0.1667 | 0.0833 | 0.1052 | 1/6 | 2.0000 |
| Name semantic (`semantic_name_only`) | 0.3333 | 0.6667 | 0.8333 | 0.4861 | 0.5696 | 5/6 | 2.8000 |
| Full-v3 all-terms (`lexical_full_document`) | 0.0000 | 0.0000 | 0.0000 | 0.0000 | 0.0000 | 0/6 | n/a |
| Full-v3 OR (`fts5_or_control_full_document`) | 0.0000 | 0.9167 | 0.9167 | 0.3806 | 0.4944 | 6/6 | 3.0000 |
| Full-v3 semantic (`semantic_first`) | 0.5000 | 0.8333 | 0.8333 | 0.6667 | 0.7103 | 5/6 | 1.4000 |

`cb-14` has two exact grade-3 spans; recall means can therefore move by half a query's recall as
well as by whole-query incidence. Query counts and first-relevant-found still have resolution
n=6 / one query = 1/6.

## Per query and field rank deltas — exact grade 3

Each cell is `NDCG@10 / first-relevant rank`; `miss` means no exact grade-3 span in the top 10.
`ΔR` is the field delta `name rank − full-v3 rank`, with a miss coded as rank 11; positive means
the full-v3 cell ranks the first exact answer earlier. This is top-10-censored notation, not an
uncensored rank. Every row is one observation (resolution: n=6; one query is 1/6).

| Query | Name all-terms | Name OR | Name semantic | Full all-terms | Full OR | Full semantic | AND ΔR | OR ΔR | Semantic ΔR |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| cb-11 | 0.0000 / miss | 0.0000 / miss | 1.0000 / 1 | 0.0000 / miss | 0.6309 / 2 | 1.0000 / 1 | 0 | +9 | 0 |
| cb-12 | 0.0000 / miss | 0.0000 / miss | 1.0000 / 1 | 0.0000 / miss | 0.3869 / 5 | 0.6309 / 2 | 0 | +6 | −1 |
| cb-13 | 0.0000 / miss | 0.6309 / 2 | 0.6309 / 2 | 0.0000 / miss | 0.4307 / 4 | 0.6309 / 2 | 0 | −2 | 0 |
| cb-14 | 0.0000 / miss | 0.0000 / miss | 0.0000 / miss | 0.0000 / miss | 0.3869 / 2 | 0.0000 / miss | 0 | +9 | 0 |
| cb-15 | 0.0000 / miss | 0.0000 / miss | 0.4307 / 4 | 0.0000 / miss | 0.6309 / 2 | 1.0000 / 1 | 0 | +9 | +3 |
| cb-16 | 0.0000 / miss | 0.0000 / miss | 0.3562 / 6 | 0.0000 / miss | 0.5000 / 3 | 1.0000 / 1 | 0 | +8 | +5 |

For the OR column, the mean censored rank improvement is +6.5 positions; this is a descriptive
mean over n=6, where one query is 1/6, not a population estimate.

## Operator-control construction and conformance

`fts5_or_control` is an operator control, never a reference. Within each indexed-text row it
uses the same FTS5 table, tokenizer declaration, document bytes, CGo-free `modernc.org/sqlite`
driver, and parameterless `bm25()` ranking. The sole query change is joining the existing quoted
prefix terms with explicit `OR` instead of spaces (implicit AND). No stopwords, normalization,
field weights, or tunable parameters were added. It is explicitly **not a CoIR-compatible
reference**.

`TestRun_FTS5ORControlConformance` contains four named fixtures, whose verbose passing output is
in `conformance.log`:

- `single_term_identical_fts5_or_control` proves name-table AND and OR rankings, including raw
  BM25 scores, are identical for a single term.
- `single_term_identical_fts5_or_control_full_document` proves the same for the full-v3 table.
- `multi_term_OR_admits_name-proper-subset` proves name-table OR admits a document containing a
  proper subset when AND returns empty.
- `multi_term_OR_admits_full-document-proper-subset` proves the same for the full-v3 table.

## Reproducibility record

The content-addressed report and twelve raw series record:

- both generated MATCH expressions for each of `cb-11` through `cb-16`;
- the exact executed SQL, both FTS schema declarations, and the explicit absence of a `tokenize=`
  clause in either schema;
- `modernc.org/sqlite v1.52.0`, its module and go.mod sums, and SQLite runtime `3.53.2`;
- Cobra SHA `a0a6ae020bb3899ff0276067863e50523f897370`, ordered node/document IDs and SHA-256 text hashes
  for 938 qualified-name rows and 768 full-v3 rows, plus each v3 semantic `text_hash`;
- derived exact-grade-3 dataset SHA-256
  `871f9a51244c31c378334e39957082cfa67bd2794a3d7d11349fedb27c23fb1e` and source dataset SHA-256
  `be604ff7b17db5c35b0c63ddbb5d758633535e81e6771858ff860c724fb50d82`;
- every complete top-10 ranking, including empty rankings, with raw BM25 scores on every FTS hit;
- query-transform source `internal/eval/retrieval/full_document_lexical.go` at SHA-256
  `640a1871fa71954c3db68f4ef553f207cc79d670b1da0040ad76abb9cec0c2ee`, frozen before execution
  against base commit `55c8a8a76cf2f847ded21e074c5a2bd5f7000dd2`; the candidate remains honestly marked `+dirty`
  because the orchestrator owns the eventual commit.

`run.json` content-addresses the dataset, report, and all twelve raw hit/latency series.
`aggregate-output.log` records independent reproduction of 197/197 metrics with zero discrepant
and zero unknown. `SHA256SUMS` covers every run artifact except itself.

## Gates and limitation

`gofmt -l .`, `CGO_ENABLED=0 go vet ./...`, and `go run ./cmd/layerguard` passed. The uncached
SW-272-affected package set also passed. The required `CGO_ENABLED=0 go test ./... -count=1` did
**not** pass in this managed execution environment: TCP and Unix socket binds are forbidden
(`operation not permitted`), nested release/bench tests override the outer `GOFLAGS` and expose
the linked-worktree VCS-stamping failure, and the resource-contended run produced extension-host
timeouts and one latency UNKNOWN. `gates/go-test.log` records the command and verbatim failure
excerpts; the other gate logs contain their real output. An ordinary checkout/environment that
permits local sockets must rerun the exact full test command before this work is accepted.

The optional secondary grade-≥2 scoring was not published: the primary requirement is exact
grade 3, and this report's content-addressed dataset/scorer contract binds one qrel policy. The
earlier grade-≥2 run is superseded and is not mixed into this primary artifact.

No holdout query was read or measured. No target file, recorded number outside this run directory,
or public claim sentence was changed.

## Recompute

~~~bash
export GOFLAGS=-buildvcs=false
GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/graphi-sw272-go-cache}" CGO_ENABLED=0 \
go run ./cmd/retrieval-eval \
  -aggregate docs/eval/retrieval/runs/2026-09-03-sw272-field-parity

SW272_COBRA_ROOT="${SW272_COBRA_ROOT:?set to a read-only cobra clone at a0a6ae020bb3899ff0276067863e50523f897370}"
SW272_MODEL_DIR="${SW272_MODEL_DIR:?set to the verified static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b artifact directory}"
test "$(git -C "$SW272_COBRA_ROOT" rev-parse HEAD)" = a0a6ae020bb3899ff0276067863e50523f897370
test -d "$SW272_MODEL_DIR"
SW272_TMP="$(mktemp -d)"
GRAPHI_STATIC_MODEL_DIR="$SW272_MODEL_DIR" \
GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/graphi-sw272-go-cache}" CGO_ENABLED=0 \
go run ./cmd/retrieval-eval \
  -field-parity \
  -manifest corpus/manifest.json \
  -repo cobra \
  -checkout "$SW272_COBRA_ROOT" \
  -dataset internal/eval/retrieval/testdata/datasets/cobra-v1.json \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -out "$SW272_TMP/report.json" \
  -export-raw "$SW272_TMP/run" \
  -runner-class static-local \
  -repeats 3 \
  -date 2026-09-03
GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/graphi-sw272-go-cache}" CGO_ENABLED=0 \
go run ./cmd/retrieval-eval -aggregate "$SW272_TMP/run"

(cd docs/eval/retrieval/runs/2026-09-03-sw272-field-parity && shasum -a 256 -c SHA256SUMS)
~~~

## Redaction note

Absolute paths under the operator's home directory were replaced before commit: the
recompute variables above are REQUIRED (`:?`) with no default, following the SW-264
precedent, and one occurrence of the home prefix in `execution.log` was replaced with
`$HOME`. Only the environment prefix changed; no measured value, count, score or hash was
altered. `SHA256SUMS` was regenerated afterwards and covers the redacted files.

