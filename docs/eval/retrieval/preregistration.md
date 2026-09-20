# Authoring `preregistration.json`

The commented template for the embedded-model development qualification's sealed
preregistration. The strict, paste-ready copy of the same file is
[`preregistration.example.json`](preregistration.example.json); it follows this
directory's convention for a deliberately non-live value — an all-zero digest or
a `REPLACE-WITH-…` string, exactly as
[`coderank-sidecar-manifest.example.json`](coderank-sidecar-manifest.example.json)
does. Neither file is a runnable preregistration.

The schema is `QualificationPreregistration` in
`internal/eval/retrieval/model_qualification.go`; it is validated fail-closed by
`ValidateQualificationPreregistration` in the same file, and the loader decodes
with `DisallowUnknownFields`, so an extra key is a refusal and a comment cannot
live in the JSON itself.

## Author this file LAST

**Every change to the GrapHi repository invalidates this file, and the CodeRank
arm cannot be filled in until the CodeRank manifest is final. Freeze the
preregistration after all other work is complete — not before.**

Two orderings are forced, and getting either wrong invalidates the run:

1. `arms.M3_coderank.manifest_sha256` is the SHA-256 of the CodeRank sidecar
   manifest's exact file bytes, and `embedder_id`, `admission_sha256` and the
   sixth field of `fingerprint_canonical` are all functions of that manifest's
   parsed contents. None of them exists until the manifest is finalized. Editing
   the manifest afterwards — even reformatting it — changes all four.
2. `candidate_sha` is the commit `measure` binds the run to, and
   `candidate_diff_sha256` is the digest of the canonical diff between that
   commit and the candidate worktree outside the frozen run directory
   (`docs/eval/retrieval/runs/embedded-model-qualification`). Any commit, any
   uncommitted edit, any new file anywhere else in the repository moves one or
   both. `ObserveCandidateBinding` refuses a candidate worktree that is dirty
   outside the run directory, and refuses one whose tree differs from the frozen
   candidate outside it at all.

A consequence of (2) worth stating outright: **`candidate_diff_sha256` can only
ever be `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`**, the
digest of a zero-byte diff. `ObserveCandidateBinding` errors unless the set of
differing paths is empty, and an empty path set means empty diff bytes. Any other
value is not "the value for this run"; it is proof the tree is not frozen yet.

## The helper

Do not hand-compute the digests. The `digests` subcommand derives every field
that is a pure function of artifacts already on disk, using the same helpers the
gate checks them with:

```bash
CGO_ENABLED=0 go run ./cmd/embedded-model-qualification digests \
  --dataset /absolute/run/dataset.json \
  --candidate-root /absolute/graphi \
  --manifest /absolute/pins/coderank.json \
  --grading-rubric /absolute/graphi/docs/eval/retrieval/runs/embedded-model-qualification/grading-rubric.md \
  > /absolute/run/fragment.json
```

stdout is the JSON fragment and nothing else, so it can be redirected or piped.
stderr carries the authoring notes: one `cannot compute yet — requires …` block
per field the helper refused to invent, each naming the artifact it waits on and
the exact derivation once that artifact exists. The helper never emits a
placeholder digest, because a fabricated 64-hex value passes every structural
check in `ValidateQualificationPreregistration` and fails much later, at capture,
where it reads as model drift rather than as an authoring mistake.

Exit codes: `0` the candidate tree is freezable, `1` it is not (commit or discard
first — the two candidate fields in that fragment are wrong, not merely absent),
`2` usage or an input path that does not resolve.

Every optional flag may be omitted; the corresponding fields are then reported as
outstanding rather than guessed, which is the normal state of an authoring
session in progress.

There is **no `--graph-generation` flag**. See the next section for why.

## `graph_generation` is bound at runtime, not preregistered

`fingerprint_canonical` is `embed.Fingerprint.Canonical()`: eight
length-prefixed fields in a fixed order.

```
0 model_id          4 dim
1 revision          5 document_schema
2 model_sha256      6 chunker_config
3 tokenizer_sha256  7 graph_generation
```

Fields 0-6 are functions of pinned artifacts. They are preregistered and
compared **exactly**, field by field; a refusal names the field that moved, by
index and by name.

Field 7 is different, and an earlier revision of this document got it wrong. It
told you to build the index once and read `index.commit_generation` out of the
graphstore metadata. That value cannot be preregistered, because it is not a
property of the source tree at all:
`mintCommitGeneration` (`engine/ingest/warmstart.go`) mints it from
`crypto/rand` on **every** committed graph mutation. Two builds of the same
commit, the same tree and the same binary produce different generations — for
example `a4babe5c82f1a6e355ac363d1fa7d070` and
`c91cee9e050b4d0042042c9af208f012`. The randomness is deliberate: it makes a
monotonic-counter race between two ingesting processes impossible to lose
silently. But it means a preregistered field 7 could never match what the run
observes, so the gate that compared the whole canonical byte for byte was not
strict — it was unsatisfiable.

So the helper emits the documented constant
`runtime-bound-graph-generation` (`QualificationGraphGenerationPlaceholder`)
in field 7, and no gate ever compares that constant against a run. It is
deliberately not hex-shaped, so nobody mistakes it for an observation.

What IS checked, and was checked nowhere before, is the generation's internal
consistency: **every arm and every observation of one run must name the same
graph generation**, and the reindex that produced the index must name the same
one as the embedder that was measured against it. That is the only thing a
random token can attest, and it is the thing that actually matters — an arm
measured against a different graph makes the paired arm-vs-arm comparison
meaningless. It is the `graph_generation_consistency` gate.

The practical consequence for authoring: the two Potion arms are now fully
derivable from the repository alone. No trial index build is needed for them,
and the helper reports nothing outstanding for them.

## The fields

Three classes:

- **frozen** — copied from a Go constant. Never retype one.
  `validateQualificationThresholds` compares the whole threshold struct at once
  and names no field on refusal, so one wrong digit costs a debugging session.
- **operator** — an observation or a decision only a person can supply.
- **derived** — a digest or pin computed from an artifact that must exist first.

```jsonc
{
  // frozen — QualificationSchemaVersion. The only accepted schema.
  "schema_version": 1,

  // derived — SHA-256 of the frozen dataset file's EXACT bytes.
  //   requires: the curated 64-query development dataset.
  //   how:      helper --dataset. Reformatting the file changes this.
  //   note:     the gate refuses a dataset id or digest listed in
  //             spentQualificationDatasetIDs / spentQualificationDatasetSHA256.
  //             A spent holdout cannot be re-run; you need a fresh one.
  "dataset_sha256": "0000000000000000000000000000000000000000000000000000000000000000",

  // derived — the pinned corpus commit, 40 lowercase hex.
  //   requires: nothing beyond the dataset: it MUST equal dataset.repo_sha
  //             (validateQualificationStatsInputs compares them), so the
  //             dataset is the authority and this is a copy of it.
  //   how:      helper --dataset reads it out.
  "source_repo_sha": "REPLACE-WITH-DATASET-REPO-SHA",

  // derived — the GrapHi candidate commit the run is bound to, 40 lowercase hex.
  //   requires: every repository change to be committed first.
  //   how:      helper --candidate-root (defaults to that worktree's HEAD), or
  //             pin an explicit commit with --candidate-sha.
  "candidate_sha": "REPLACE-WITH-FROZEN-CANDIDATE-COMMIT-SHA",

  // derived — and in practice fixed. See "Author this file LAST" above: the only
  // value a run can succeed with is the empty-diff digest below.
  //   requires: a candidate worktree clean outside the run directory.
  //   how:      helper --candidate-root; it prints the observed digest and says
  //             NOT READY when the tree is not frozen.
  "candidate_diff_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",

  // derived from a repository constant — SHA-256 of RaterInstructions in
  // internal/eval/retrieval/blindeval_prompt.go, the frozen instruction block
  // BuildRaterPrompt writes verbatim at the head of every rater prompt.
  //   why a constant: validateBlindEvidenceSets requires ONE reader prompt
  //   digest to match all 64 queries of all eight subjects, and only a constant
  //   can. Editing the instruction block changes this value.
  //   how:      the helper always emits it.
  "reader_prompt_sha256": "9e6f38c03e1e21e0c71d1d646ac13cd1073116b567cfebfa05b0e8522b1faccb",

  // derived — SHA-256 of the grading rubric file's exact bytes. The blind
  // evidence's precondition record must freeze this same digest under role
  // "grading_rubric" or the finalizer refuses the evidence.
  //   requires: the rubric, authored inside
  //             docs/eval/retrieval/runs/embedded-model-qualification.
  //   how:      helper --grading-rubric.
  "grader_prompt_sha256": "0000000000000000000000000000000000000000000000000000000000000000",

  "arms": {
    // frozen — the lexical negative control carries its label and NOTHING else.
    // validateQualificationArms refuses it outright if it carries any embedding
    // pin: a lexical arm with an embedder identity is not a control.
    "M0_lexical": { "label": "M0_lexical" },

    // M1 and M2 are the same pinned Potion weights under two admission budgets.
    // Everything except the graph generation comes from engine/embed/static's
    // pin table, so the helper emits all of it with no external input.
    "M1_potion_512": {
      "label": "M1_potion_512",

      // derived from repo pins — qualificationPotionIdentity(512). The gate
      // recomputes it and compares; a typo is a refusal.
      "embedder_id": "static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b",

      // derived from repo pins — the eight-field length-prefixed
      // embed.Fingerprint.Canonical(). Fields 0-6 come from the pin table;
      // field 7 carries the constant "runtime-bound-graph-generation" and is
      // NOT preregistered (see "graph_generation is bound at runtime" above).
      //   requires: nothing. The helper always emits this arm in full.
      //   how:      the helper emits it; never retype it.
      //   note:     the template below keeps a REPLACE-WITH marker instead of
      //             the real value, because an example must never be runnable.
      "fingerprint_canonical": "REPLACE-WITH-EIGHT-FIELD-CANONICAL-FINGERPRINT",

      // derived from repo pins — Potion has no manifest FILE, so this digest is
      // taken over PinnedPotionArtifactManifest(): the pinned selector line,
      // then one "<sha256>  <filename>" line per pinned file in sorted name
      // order, every line newline-terminated. A pin rotation changes it.
      //   note:    the two Potion arms MUST share this value, and it must differ
      //            from the CodeRank arm's.
      "manifest_sha256": "d919fce66e91e9afd693b2387655592be8014d9b77e2f51712c6ae286ec97f78",

      // derived from repo pins — SHA-256 of the admission profile's canonical
      // string. MUST differ between M1 and M2: an equal value would mean the
      // 512-vs-8192 comparison compares nothing.
      "admission_sha256": "d686c1edad9ba106d210c6a73d36242871f3fdc70f978ef735e14b1d0d09a966"
    },

    "M2_potion_8192": {
      "label": "M2_potion_8192",
      "embedder_id": "static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree-eval-max-8192:6e5cd9953e0f",
      "fingerprint_canonical": "REPLACE-WITH-EIGHT-FIELD-CANONICAL-FINGERPRINT",
      "manifest_sha256": "d919fce66e91e9afd693b2387655592be8014d9b77e2f51712c6ae286ec97f78",
      "admission_sha256": "6e5cd9953e0fdb6d1119d29856894f0f53bf8885741169a8bd8748650cd69649"
    },

    // Every M3 pin waits on the finalized CodeRank manifest. Fill this arm LAST.
    "M3_coderank": {
      "label": "M3_coderank",

      // derived — "coderank:" + model.id + "@" + model.revision + ":" +
      // Manifest.IdentityDigest().
      //   requires: the finalized manifest.
      //   how:      helper --manifest.
      "embedder_id": "REPLACE-WITH-CODERANK-EMBEDDER-ID",

      // derived — as for the Potion arms, but field 6 is the DURABLE PROFILE:
      // the manifest re-serialized with its endpoint removed (the endpoint is
      // the one relocatable field — the same pinned model on another port is the
      // same embedding space — and the gate rejects a profile that still carries
      // one). Field 7 is the runtime-bound placeholder, as everywhere else.
      //   requires: the finalized manifest, and nothing else.
      "fingerprint_canonical": "REPLACE-WITH-EIGHT-FIELD-CANONICAL-FINGERPRINT",

      // derived — SHA-256 of the manifest file's EXACT bytes. `measure` re-reads
      // the file before and after constructing the embedder and refuses on any
      // difference, so reformatting the manifest mid-run fails the run.
      "manifest_sha256": "0000000000000000000000000000000000000000000000000000000000000000",

      // derived — SHA-256 of Manifest.AdmissionSpec().String(). Must differ from
      // both Potion admission digests.
      "admission_sha256": "0000000000000000000000000000000000000000000000000000000000000000"
    }
  },

  // frozen — QualificationCompactVersion.
  "compact_version": "task_context/2-compact/17",
  // frozen — QualificationTokenBudget.
  "token_budget": 1200,
  // frozen — QualificationBootstrapSamples.
  "bootstrap_samples": 100000,

  // operator — any non-zero uint64. Write it down BEFORE any result is opened.
  // Re-rolling a seed after seeing a confidence interval is the exact
  // manipulation preregistration exists to prevent.
  "bootstrap_seed": 1,

  // frozen — every value below is a constant in
  // internal/eval/retrieval/model_qualification.go. validateQualification
  // Thresholds compares the struct as a whole and names no field on refusal.
  "thresholds": {
    "min_passes": 56,                        // QualificationMinPasses
    "min_paired_gain": 9,                    // QualificationMinPairedGain
    "min_weak_strata_with_positive_gain": 2, // QualificationMinWeakStrataWithPositiveGain
    "bootstrap_confidence": 0.95,            // QualificationBootstrapConfidence
    "max_sidecar_rss_bytes": 2147483648,     // QualificationMaxSidecarRSSBytes  (2 << 30)
    "max_artifact_bytes": 1073741824,        // QualificationMaxArtifactBytes    (1 << 30)
    "max_query_p95_millis": 1000,            // QualificationMaxQueryP95Millis
    "min_query_samples": 100,                // QualificationMinQuerySamples
    "max_reindex_seconds": 600               // QualificationMaxReindexSeconds
  },

  // operator — this block must describe the machine that will run `measure`,
  // exactly. measure observes os_version, cpu, physical_cores and the sidecar's
  // effective runtime thread count itself and fails closed on any difference, so
  // a stale copy of a previous run's block is a refusal at the end of a long
  // measurement.
  "reference_machine": {
    // operator — the LOWERCASE GOOS value, NOT `uname -s`. The probe hardcodes
    // it (`qualification_machine_darwin.go`, `_linux.go`): macOS is "darwin",
    // never "Darwin"; Linux is "linux". `uname -s` prints "Darwin" and would be
    // refused.
    "os": "darwin",
    // operator — on macOS `sysctl -n kern.osrelease` (the KERNEL release, e.g.
    // "25.6.0"), NOT `sw_vers -productVersion` (the product version, e.g.
    // "26.6.2"). The two differ and only the kernel release is what the probe
    // reads. On Linux: `uname -r`.
    "os_version": "REPLACE-WITH-EXACT-OS-VERSION",
    // operator — `sysctl -n machdep.cpu.brand_string`, or /proc/cpuinfo's
    // "model name".
    "cpu": "REPLACE-WITH-EXACT-CPU-BRAND-STRING",
    // operator — `sysctl -n hw.physicalcpu`, or lscpu's socket x core count.
    // Must be positive.
    "physical_cores": 0,
    // operator — the sidecar's own effective thread count, not the core count.
    // Must be positive.
    "runtime_threads": 0,
    // operator — the literal declaration. Background load cannot be inferred, so
    // measure seals this string and requires it to EQUAL the
    // --background-metadata argument byte for byte. Include the content digest
    // of the retained process-inventory artifact, not just its path — e.g.
    // "captured=2026-09-16T10:00:00Z; inventory_sha256=<sha256>".
    "background_load": "REPLACE-WITH-LITERAL-BACKGROUND-LOAD-DECLARATION"
  }
}
```

## Order of operations

1. Finish every repository change. Commit.
2. Finalize the CodeRank sidecar manifest; verify it with
   `scripts/eval/coderank_sidecar.py verify`.
3. Author the grading rubric under
   `docs/eval/retrieval/runs/embedded-model-qualification`. Commit.
4. Confirm `git status --porcelain` is empty outside the run directory.
5. Run `digests` with every flag. It should exit 0. (There is no trial index
   build step: `graph_generation` is bound when `measure` reindexes.)
6. Paste the fragment into `preregistration.json`, add `bootstrap_seed` and
   `reference_machine`, and freeze the file.
7. Re-run `digests` once more. If anything moved, something in the repository
   changed after step 1 and the file must be re-derived.
