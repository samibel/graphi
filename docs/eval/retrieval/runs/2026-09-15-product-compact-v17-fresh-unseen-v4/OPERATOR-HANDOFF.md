# Content-free operator handoff

Operate only from the committed clean v4 curation tip. Do not open or print the
sealed dataset. Before every mutating command, run `go run
./cmd/retrieval-eval -h >/dev/null`, validate every argument read-only, and run
`operator-preflight.zsh <repo-root> <clean-pinned-cobra-checkout>`. Do not use a
failed mutation to discover arguments; do not retry or overwrite.

Immutable values: Contract 2, N=64, k=56, response budget 1,200,
follow-up cap 120, candidate `26a36f0958cb16d925c4dd871cda76d0e117b15a`
with tree `6ab1d7075c787b7a8884114d5d732be812cb325f`, and Cobra
`a0a6ae020bb3899ff0276067863e50523f897370`.

1. Verify all committed hashes and absence of phase outputs.
2. Freeze once with `-blind-eval freeze`, `-blind-eval-contract 2`, the exact
   run directory, and the sealed dataset path. Commit the precondition.
3. Capture once with `-blind-eval capture`, Contract 2, exact run directory,
   `-repo cobra`, **`-checkout <clean-pinned-cobra-checkout>`**, and the exact
   production embedder. Validate 64/64 bundles and commit the full capture plus
   harness pre-registration before any rater executes.
4. Dispatch the two pre-registered primary slots without repository, qrel, or
   answer-span access. Persist raw outputs write-once.
5. Before sealing, repeat help and read-only checks. The seal checklist must
   include `-blind-eval seal`, Contract 2, exact run directory, and
   **`-checkout <clean-pinned-cobra-checkout>`**. Seal once.
6. Grade only generated packets under the frozen rubric; adjudicate only valid
   disagreements. Seal each phase once. Decide only after the complete digest
   chain, participant identities, candidate/corpus bindings, N/k, and clean
   committed state all validate.

A missing, malformed, partial, or drifted member is a refusal/`RELEASE: NO`,
never an exclusion from N. The curator performed none of these phases.
