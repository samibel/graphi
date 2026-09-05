# Threat model for the retrieval evaluation procedure

**The evaluation procedure defends against error, accident and drift. It does not defend against its
own author, and it never claimed to.**

This document draws that boundary. It exists because three review rounds hardened the procedure's
tamper-resistance one demonstrated forgery at a time and each round found the next layer, and because
the actor those checks would defend against **owns the repository**: they can write any file, author
any commit and rewrite any history. No check that lives inside the repository can be authoritative
against the person who controls the repository. Each round buys a slightly higher wall around a
building with no roof.

Bounding the model is a **scope decision, not a dismissal**. It is only honest if the boundary is
drawn precisely enough that a reader of any claim resting on this evaluation knows what the evidence
does and does not establish. That is what the rest of this document does.

The owner decision this records is `projects/graphi/stories/SW-280/decision-threat-model.md`
(2026-09-05). It is not an amendment to `docs/eval/retrieval/methodology.md`, which is a frozen input
of the SW-280 run and may not change under it; SW-281 adopts this document at its new freeze.

## What the procedure defends against

- **Error** — a stale, mistyped or programmatically wrong value; a file written to the wrong path; a
  digest that no longer matches the bytes it names.
- **Accident** — an artifact deleted or overwritten without intent; a run re-executed against a
  changed input; two processes racing at one address.
- **Drift** — a frozen input changing under a run; a gate that stops running and is not noticed; a
  claim in a document ageing out of agreement with the code.

These are the failures that actually happen to an evaluation that nobody is attacking, and they are
the failures that would otherwise silently produce a number nobody could reproduce. The procedure
takes them seriously and refuses rather than degrades.

## What the procedure does not defend against

**Deliberate falsification by someone with write access to this repository.**

No in-repository check can. The operator who would forge an artifact is the operator who runs the
checks, owns the files the checks read, authors the commits the checks resolve, and can rewrite the
history the checks appeal to. A check that lives in the repository is evidence about the repository's
contents, not evidence about the intentions of the person who wrote them. Hardening it further does
not change that; it only moves the point at which a determined author has to do slightly more work,
and every round of that work so far has been demonstrated in minutes.

Two consequences follow, and both are deliberate:

1. Hardening of this class is no longer paid for a demonstrated forgery at a time. Where a specific
   gap is worth closing anyway it is scheduled against the moment it could first buy something — see
   the symlink bypass below, which is deferred with a deadline rather than accepted — but the class
   as a whole is not reopened without a new owner decision.
2. Any claim resting on this evaluation carries the disclosure sentence below.

## The controls that remain in force

Nothing here is reopened by this decision. Each of these was closed against a demonstrated failure
and each stays closed:

- **Write-once sealing.** A response, a grade and an adjudicator answer are written once, created
  with `O_CREATE|O_EXCL`. Sealing identical material again is idempotent; sealing *different*
  material at an address that already exists is refused by name. This closes the re-grade retry loop.
- **The digest chain.** The ordering evidence is the chain of content addresses — a record naming a
  digest could not have been written before the bytes that hash to it existed — and not a
  manufactured chronology over mtimes.
- **Prompt reconstruction from pre-registered bytes.** Every committed prompt is rebuilt from the
  pre-registered bundle bytes and question text and must be byte-identical, so a prompt carrying an
  expected answer cannot be substituted after pre-registration and removed afterwards.
- **The mandatory sidecar manifest.** Every file the decision reads outside the sealed records is
  content-addressed in `sidecar-manifest.json`. A recorded sidecar that is absent, whose bytes
  differ, an unrecorded sidecar that is present, or a manifest belonging to another run is a refusal
  to decide — and deleting the manifest is itself a refusal to decide.
- **Mandatory provenance.** A run with no evidence of transport, indexed checkout, embedder and
  candidate cannot release. Absence is a release refusal, not a blank row in a report.
- **Provider/model binding.** Every rater, grader and adjudicator identity is recorded with its
  provider and model version, and a sealed artifact under a pre-registered slot must match it.
- **Refusal on an unsatisfiable `N`.** For a population no `k` in `[0, N]` can serve at the `3/4`
  floor, the derivation returns a typed error naming `N` and the evaluation records `RELEASE: NO`.
  `k` is never clamped to `N` and there is no best-available mode.
- **Commit ids must resolve.** A candidate binding naming a well-shaped but nonexistent commit id is
  refused, because a stale, garbage-collected or programmatically wrong SHA is exactly the error
  class this procedure still covers.

## The known open gap, deferred by an explicit owner decision: symlink containment bypass

Physical containment of the run directory is **lexical**. `filepath.Abs` and `filepath.Rel` do not
resolve symlinks (`cmd/retrieval-eval/blindeval.go:485`), so a complete run directory placed behind
an in-repository symlink that points outside the repository passes the containment check and is read,
sealed and decided from an external mutable directory — one whose contents git never saw, and which
therefore sits outside the git history the append-only limitation appeals to.

**The fix is one call: `filepath.EvalSymlinks`, applied before the containment comparison.**

This gap is **open and deferred by an explicit owner decision, not closed and not accepted.** Three
things fix its disposition:

- **It is out of scope for the SW-280 run specifically, because that run fails by 26 passes.** Its
  recorded result is 31 reviewed / 30 corrected out of 64 against a pre-registered `k=56`, and
  `RELEASE: NO`. No forged or relocated artifact could reach that outcome: changing it would require
  wholesale deliberate replacement of at least 26 corrected outcomes plus binding evidence. A
  relocated run directory buys nothing here.
- **It is required before any run that reports `RELEASE: YES` is published.** That is the first
  moment a relocated run directory could buy anything, and it is therefore a **precondition of the
  release gate** — not an aspiration, not a nice-to-have, and not something a passing run may be
  published while still owing. A passing run published with lexical containment still in place is a
  run published against this document.
- **It is deliberately not implemented on the SW-280 branch**, whose result it cannot move and whose
  scope is closed.

Until it is closed, a reader should treat "the run directory is inside the repository" as a lexical
statement about a path string rather than as a statement about where the bytes physically live.

### A related and narrower limit worth naming

The commit-existence check resolves the ids that name commits **in this repository** — the binding's
`candidate_sha` and `frozen_candidate_sha`. The binding's `checkout_sha` and the provenance's
`repo_sha` name commits in the *indexed corpus* repository (for the SW-280 run, a pinned Cobra
clone), which is not present when the decision is taken, so they are checked for being commit ids and
for agreeing with each other, but their existence is not resolved at decide time. That is a limit of
what the decision step can observe, not a defence being withheld.

## The disclosure sentence

Any published claim resting on this evaluation carries this sentence, or one that says the same thing
without softening it:

> The evidence for this number is designed to detect error, accident and drift. It is **not**
> designed to detect deliberate falsification by someone with write access to this repository, and
> it should not be read as establishing that none occurred.

That sentence is the price of bounding the model, and it is a fair one — because the alternative was
never "evidence that survives a hostile author", it was "evidence that looks like it might, for as
long as nobody tests it."

## What actually establishes trust here, and it is not the wall

The controls that genuinely bind an author are the ones a reader can check **from outside**:

- committed digests and content addresses, verifiable by recomputation from the repository;
- git history, in which a deletion or a replacement is visible;
- byte-identical reproduction of the ledgers and the dataset from committed inputs;
- the frozen rule's hash, unchanged since before the first issue was fetched;
- **publication of the raw per-query data and the scoring code**, so the number can be recomputed
  rather than believed.

The last of those is worth more than every check discussed in this document, and it is not yet done.
An independent outside critique of this track's acceptance criteria made exactly that point: the work
has built an **internally-verifiable** audit trail that no external reader will ever see, when what
makes a number credible is what a sceptic can recompute. **SW-281 should treat publishing the raw
per-query data and the scoring code as a primary deliverable, not an appendix.**
