# Preregistration inputs — embedded-model development qualification

Recorded 2026-09-18, **before any result exists**. That timing is the whole
point of two of the values below; see each section.

This directory is `QualificationCandidateExcludedPath`, so writing here does not
move `candidate_diff_sha256`.

## `bootstrap_seed` — derived, not chosen

```
1077775116515082045
```

A preregistered seed exists to make the confidence interval unmanipulable: a
seed re-rolled after seeing an interval is the exact manipulation preregistration
prevents. A hand-picked number satisfies the letter of that rule and leaves no
way to show it was not picked for its result.

So this seed is **derived**, following the convention this repository already
uses for its savings bootstrap (`SavingsBootstrapSeedMethod`,
`internal/eval/retrieval/measurement_contract.go:43`):

```
sha256(dataset_sha256 + "\n" + version_tag), first 8 bytes, big-endian uint64
```

| input | value |
|---|---|
| `dataset_sha256` | `33760861d5c78f551d4203342f2e3b7350e030882bd74ab6ce166d00ce8168ba` |
| `version_tag` | `embedded-model-qualification-schema-1` |
| sha256 | `0ef506c217a2ff3dc779751d593059a30bd942732b968f866ea2a44fbdac88fd` |
| first 8 bytes → uint64 | `1077775116515082045` |

Anyone can recompute it from the frozen dataset alone. It is non-zero, as
`ValidateQualificationPreregistration` requires. Changing the dataset changes the
seed — which is correct: a different dataset is a different experiment.

**Deviation from the repo convention, stated rather than hidden:** the savings
contract's second component is `measurement_contract_version`. This
qualification has no such value, so the tag names its schema version instead.
Same construction, different second input.

## `reference_machine` — probed here, must match where `measure` runs

```json
{
  "os": "darwin",
  "os_version": "25.6.0",
  "cpu": "Apple M2 Max",
  "physical_cores": 12,
  "runtime_threads": 8
}
```

`runtime_threads: 8` is not a host property — it is what the sidecar reported
in its own `/v1/attestation` on 2026-09-18, once it was running against the real
pinned artifacts. It cannot be probed before then.

Probed with exactly the `sysctl` keys the Go probe reads
(`internal/eval/retrieval/qualification_machine_darwin.go`), NOT the recipe an
earlier revision of `preregistration.md` gave:

- `os` is the lowercase GOOS value the probe hardcodes. `uname -s` prints
  `Darwin` and would be refused.
- `os_version` is `sysctl -n kern.osrelease` (`25.6.0`), not
  `sw_vers -productVersion` (`26.6.2` on this machine). The two differ and only
  the kernel release is read.

### Two conditions on these values

1. **`runtime_threads` came from the sidecar, not the host.** It is the
   sidecar's own effective thread count and `measure` compares it. If the
   sidecar is ever started with a different thread configuration, this value
   moves and must be re-read from `/v1/attestation`.
2. **These values describe the machine this was probed on.** If `measure` runs
   anywhere else, every field must be re-probed there. `measure` observes the
   machine itself and fails closed on any difference — at the END of a long
   measurement, which is the expensive place to find out.

## `grader_prompt_sha256`

```
ec24be9ffc0ba0496094b1641e1d747162d48c2ecd09cc2a8a4ebe89d75d17e4
```

Of `grading-rubric.md` in this directory. Confirmed two ways: `shasum -a 256`
by hand, and the `digests` subcommand given `--grading-rubric`, which agreed.

**This digest is only valid while that file is untouched.** Any edit — even
reformatting — changes it, and the rubric's own opening paragraph says editing
after the freeze fails the run. Recompute with `digests` immediately before
sealing rather than copying this value forward.

Note that `candidate_diff_sha256` EXCLUDES this run directory, so a later edit
to the rubric does NOT move it. The only things that catch rubric drift are
`grader_prompt_sha256` and the blind-evidence precondition record — exactly the
pair whose non-verification invalidated the V5 run
(`runs/2026-09-13-product-compact-v5-dev/FINDING-rubric-was-not-presealed.md`).

## First live sidecar run — 2026-09-18

The sidecar ran against the real pinned artifacts for the first time. Its
`/v1/attestation`:

```json
{"protocol":"graphi-coderank/3",
 "identity_digest":"3c25c2fce1982d0abc134ff373b4cfba66cf554e69a7fa33e1dd3274017bbfc2",
 "epoch":"8d3ad4a0bdbc994d17b30771ed16af347a2c849f9949e2ee8a1dcb3a27be12bf",
 "dimension":768,
 "peak_rss_bytes":1538179072,
 "artifact_bytes":547945013,
 "runtime_threads":8}
```

### Both operating-budget gates pass, with room

| measured | value | ceiling | headroom |
|---|---:|---:|---|
| `artifact_bytes` | 547,945,013 | 1 GiB (1,073,741,824) | 49 % spare |
| `peak_rss_bytes` | 1,538,179,072 | 2 GiB (2,147,483,648) | 28 % spare |

This settles an earlier scare of my own making: I told Codex the model was
"several GB", which would have made `DEVELOPMENT PROMOTION: NO` structurally
predetermined. I had never measured it. It is 523 MB.

### Go and Python agree on the durable identity

The sidecar reports `identity_digest 3c25c2fce198…`. The Go side, computing
`Manifest.IdentityDigest()` from the manifest file alone, produces the same
value inside `arms.M3_coderank.embedder_id`. Two independent implementations,
one identity — the cross-check the protocol depends on, confirmed against real
artifacts rather than a fixture.

### Pinned artifacts

| | |
|---|---|
| model tree | `~/models/CodeRankEmbed`, 523 MB |
| revision | `3c4b60807d71f79b43f3c4363786d9493691f8b1` (confirmed twice: local download metadata and the HF API) |
| `model.sha256` | `8f651727eb12644935f9c6faaf276ba9707ee3ffc4618c5a80cfe2190902ad76` |
| `tokenizer.sha256` | `418e99ca78903ffd5ec3ba8b76576febba7b2e16301c3125369e6ed38172247e` |
| runtime | `sentence-transformers 6.1.0`, digest `1c76d17b6e76e6c8fc6766f383b8f8ec86a836bbca7542f1182337e2d690008a` |
| manifest | `~/models/coderank.json`, sha256 `15f4b86e8df8291f364894658b1f820f45b5674bd86569007ea41ff16821ef63` |

A `.cache/huggingface/` directory that `huggingface-cli --local-dir` left inside
the tree was removed before any digest was taken — it is invisible to `ls`, feeds
`tree_digest` and `artifact_bytes`, and is not reproducible.

**Unpinned dependency, stated rather than hidden:** the model's custom code
imports `einops` (`modeling_hf_nomic_bert.py:18`), but `einops` is not in
`RUNTIME_PACKAGES`, so it does not enter `runtime.sha256`. Installed here at
`0.8.2`. A dependency that carries model behaviour is not covered by the runtime
pin.

## Still outstanding

`candidate_sha` / `candidate_diff_sha256` — require the frozen tree. Everything
else is settled; `digests` computes them once the tree is clean.
