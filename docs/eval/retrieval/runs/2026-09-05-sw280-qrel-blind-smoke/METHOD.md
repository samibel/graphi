# SW-280 — method notes for the qrel-blind smoke evaluation

This is the method record for SW-266 slice 6. It does **not** amend
`docs/eval/retrieval/methodology.md`, which is SW-274's frozen measurement contract; this evaluation
inherits that contract and adds nothing to the token estimand.

## What this evaluation is

A **qrel-blind smoke evaluation**: can somebody who sees only the question and the exact serialized
`task_context/2` bundle answer the question? The raters see our bundle format and our question set.
It is not a system-blind evaluation, not a human panel, and not an estimate of general
answerability. Its pass count is a separate gate and does not enter the token estimand or its
interval (`docs/eval/retrieval/methodology.md`, "Estimand and claim boundary").

## The order of operations, and why it is the deliverable

1. **Freeze.** `precondition-record.json` names every input by content hash with its freeze commit
   and timestamp: the sealed dataset, the candidate sha and its 1200-token budget, the comparator
   version, the tokenizer pin and its vocabulary digest, the budgets and targets files, this
   directory's grading rubric, the frozen methodology, and the frozen claim wording. The evaluation
   refuses to start when any of these is absent, and fails the run when any hash differs at the end.
2. **Derive and pre-register.** `N` is read from the sealed dataset as the count of answerable
   holdout queries. `k` is derived from `N` by code — the smallest integer in `[0, N]` whose
   two-sided exact Clopper-Pearson 95% lower bound is at least `3/4`. Both, together with the
   content address of every query text and every captured bundle, are written to
   `pre-registration.json` and committed **before the first rater response exists**.
3. **Rate.** Each primary rater is given the question text and the preserved bundle bytes. Every
   response is content-addressed the moment it is recorded, and names the pre-registration's own
   hash — which a response produced earlier could not have done. The prompt each rater received is
   committed under `prompts/`, and `decide` rebuilds every one of them from the pre-registered
   bundle bytes and question and refuses a prompt that is not byte-identical, so a prompt carrying
   an expected answer cannot be substituted after pre-registration and removed afterwards.

   **Blindness here is instruction-enforced, not sandbox-enforced.** Each rater was a subagent with
   file tools, in a checkout that contains the answer key for every one of these questions
   (`internal/eval/retrieval/testdata/datasets/cobra-v2.json`), and it was instructed to read exactly
   one file. Nothing prevented it from reading more, no tool-call transcript was preserved, and this
   evaluation therefore cannot show that no rater consulted the key. What it can show, and does: the
   committed prompts rebuild exactly and carry no judgement, qrel, expected answer or rubric; 38 of
   the 128 primary responses declined with `INSUFFICIENT`; and the graded outcomes track a naive
   "was a grade-3 span inside a retrieved snippet?" predictor on 54 of 64 queries.
4. **Grade.** Each answered response is graded against the frozen rubric by a recorded grader. Each
   grade names the content address of the response it graded.
5. **Adjudicate, only on a disagreement.** The adjudicator answers from the same question, the same
   bundle and the same prompt, and `decide` compares all three against the pre-registered values.
   Its response is frozen and content-addressed **before** the disclosure record exists; the
   disclosure record names that frozen hash and the hash of every primary response for the query
   and of every grade bound to those responses. Majority of the three graded outcomes decides the
   query.

   The disclosure record carries **no timestamp**. It used to record `disclosed_at` as the
   adjudicator response's file modification time plus exactly one second — a number the code
   generated, over an mtime anybody can set, which every disclosure satisfied because every
   disclosure was written that way. It looked like proof of an order and established nothing, so it
   is gone. What the artifacts do establish is the digest chain: a record naming those digests could
   not have been written before they existed. What they do **not** establish, and what no mechanism
   in this slice evidences, is that no primary response or grade reached the adjudicator by another
   route — adjudicator blindness is instruction-enforced here too, and every adjudication artifact
   carries that limitation as a fixed, checked string rather than leaving it to a reader.
6. **Decide.** Every frozen input hash is recomputed and compared, every prompt is rebuilt, the
   sidecar manifest is required to account for every file the decision reads outside the sealed
   records, and the capture provenance is required to bind the bytes to the candidate implementation
   and the indexed checkout they were recorded against — with each of its commit ids checked for
   being a commit id and its excluded path checked against the directory this run was frozen into.
   A pass count below `k`, any drifted input, an unaccounted sidecar, or an unbound capture records
   `RELEASE: NO` or refuses outright.

## Rules that have no exception

- A missing, empty or refused response is a failure for that rater **and** a failure for its query.
  It is not adjudicated, re-requested or replaced.
- Two primary failures are a query failure and are not adjudicated.
- `k` is never clamped to `N`. For `N <= 12` no `k` exists at the `3/4` floor, and the evaluation
  records `RELEASE: NO` with that reason rather than lowering the bar.
- There is no flag, environment variable, configuration key or report field that lowers `k`, waives
  a query, excludes a query from `N`, retries a graded response or forces a pass.
- **Sealing is append-only.** A response, a grade and an adjudicator answer are written once. Sealing
  the same raw material again is idempotent, because every field the seal derives comes from the raw
  file rather than from the clock; sealing *different* material for an address that already exists is
  refused by name. This closes the one supported retry loop the evaluation had: editing a raw `FAIL`
  to `PASS` and re-running `seal` used to overwrite the sealed grade in place, and enough repetitions
  turned `RELEASE: NO` into `RELEASE: YES` with no flag and nothing in the report to see. The seal
  cannot defend against deleting a committed artifact and re-sealing in its place; that defence is
  the run directory's own git history, where such a deletion is a visible removal. **This limitation
  now covers the sidecars too**, and it is the whole of what remains uncovered: deleting or replacing
  `capture-provenance.json` or `disclosed-grading-concerns.json` is refused while
  `sidecar-manifest.json` records them, and deleting the manifest is itself a refusal to decide, so
  what is left is an author who removes the manifest **and** the sidecar together and re-seals both.
  That is a deletion of committed files and git history is the evidence of it. Nothing here defends
  against an author who rewrites the run directory's history.
- **Every artifact the decision reads is accounted for.** The two sidecars are optional in shape and
  load-bearing in effect: the provenance decides whether the capture is bound, and the concern record
  subtracts from the corrected count. `sidecar-manifest.json` content-addresses both, and the
  decision refuses when a recorded sidecar is absent, when its bytes differ from what was recorded,
  when a sidecar the manifest does not name is present, or when the manifest belongs to another run.
  The one transition it permits is a sidecar the manifest recorded as **absent** appearing later,
  because a disclosed concern can only ever subtract and a provenance is assessed on its own content
  before it binds anything. Deleting the concern record used to raise the corrected count silently,
  and at the threshold that is `RELEASE: NO` becoming `RELEASE: YES`.
- **A missing or unverifiable artifact never produces a better outcome than a present one.** Every
  refusal above points the same way: when the decision cannot establish something, it refuses or
  records `RELEASE: NO`.
- **A disclosed grading concern may only ever subtract.** A counted pass the bundle-only rule does
  not support is disclosed beside the result and lowers the corrected count. There is no concern
  shape that raises a count, because converting a counted failure into a pass is precisely the
  re-grade the append-only seal exists to prevent. The release is decided on the smaller of the
  reviewed and corrected counts.
- **An unbound capture cannot release.** Recording a commit is not binding to it, so the capture
  refuses to run over a dirty candidate worktree or a dirty indexed checkout, and refuses when the
  candidate tree differs from the frozen candidate anywhere outside this run directory. A run whose
  provenance carries no such binding records `RELEASE: NO` on that ground alone. A binding is also
  not taken at its word: `candidate_sha`, `frozen_candidate_sha` and `checkout_sha` must each be a
  40-character commit id, and the path the comparison excludes must be exactly the directory this run
  was frozen into — which the precondition record already names, because the grading rubric it froze
  lives there.
- **The run directory may not swallow the implementation.** The run directory is the one path the
  candidate comparison excludes, because the run writes into it after the freeze. `freeze`, `capture`,
  `seal` and `decide` all refuse a run directory outside the repository, at the repository root, or
  holding candidate source: an exclusion that broad makes `git diff … -- . ':(exclude)<path>'` return
  nothing, so a later retrieval-improving commit would compare as identical to the frozen candidate.

## How to reproduce

```text
go run ./cmd/retrieval-eval -blind-eval freeze  -blind-eval-dir <this directory> -dataset internal/eval/retrieval/testdata/datasets/cobra-v2.json
go run ./cmd/retrieval-eval -blind-eval capture -blind-eval-dir <this directory> -repo cobra -checkout <pinned cobra clone> -embedder static:potion-code-16M-v2@<pin>
go run ./cmd/retrieval-eval -blind-eval decide  -blind-eval-dir <this directory>
```

The rating and grading steps between `capture` and `decide` write `responses-raw/`, `grades-raw/`
and `adjudications-raw/`, and `-blind-eval seal` turns them into the content-addressed records under
`responses/`, `grades/` and `adjudications/`; they are dispatches, not PR-time tests.

## How to recompute a content address by hand

`prompt_sha256` is the plain SHA-256 of `prompts/<query id>.txt`, and `bundle_sha256` is the plain
SHA-256 of the base64-decoded `payload.bytes` in `bundles/<query id>.json`. The self-referential
addresses — `pre_registration_sha256`, a response's `sha256`, a grade's `sha256` — are the SHA-256 of
the record's JSON with its own `sha256` field set to the empty string, marshalled the way Go's
`encoding/json` marshals it: **struct field declaration order** (which is the order the committed
files are already written in), no indentation, no trailing newline, and `<`, `>` and `&` escaped as
`\u003c`, `\u003e` and `\u0026`.

So, in any language: read the committed file preserving key order, set `"sha256"` to `""`, re-emit
minified with those three characters escaped, and hash. In Python:

```python
d = json.load(open(path), object_pairs_hook=collections.OrderedDict)
d["sha256"] = ""
raw = json.dumps(d, ensure_ascii=False, separators=(",", ":"))
for a, b in (("<", "\\u003c"), (">", "\\u003e"), ("&", "\\u0026")):
    raw = raw.replace(a, b)
hashlib.sha256(raw.encode()).hexdigest()   # == the record's own sha256
```

This was checked against `responses/cb-17--primary-rater-1.json`, which reproduces
`e5c00644d379b9d69ea7676a22cf917726db0fa5a8c9c4970708135481153fa3`.

## What this run's own records get wrong, and cannot have corrected

- `pre-registration.json`'s `precondition_record_commit` names `e48a1ba3…`, the candidate commit at
  freeze time. The precondition record does not exist in that commit; it first appears in `e2f104ed`.
  The field cannot be corrected in place, because the pre-registration is content-addressed and all
  136 responses name that address — rewriting it would destroy the ordering evidence it exists to
  provide. The capture instrument now resolves the containing commit from git and refuses to
  pre-register an uncommitted precondition record, so no later run can repeat it.
- `capture-provenance.json` carries no candidate binding, because it was written before the binding
  existed. `decide` records that absence and refuses the release on it.
