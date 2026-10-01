# Static model pin rotation governance

Current governed revision: `e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`.

The production static embedder is pinned to `potion-code-16M-v2` at the
revision above. `pins.go` is the source of truth for the model, tokenizer,
configuration, and module SHA-256 values.

## Approval

A repository maintainer responsible for semantic search must approve a pin
rotation in the pull request. The change must explain why the upstream
revision is being adopted and name the approving maintainer.

## Required rotation record and re-measurement

Before changing the pin:

1. Update all four SHA-256 values in `pins.go` from verified artifact bytes.
2. Regenerate the Model2Vec oracle fixtures under
   `engine/embed/static/testdata/oracle/`.
3. Run the production embedder with `CGO_ENABLED=0` on two architectures using
   the same verified artifact handoff. The canonical vector output must remain
   byte-exact across architectures; do not round or introduce a tolerance.
4. Replace the retained cross-architecture record under
   `docs/eval/static-embedder-cross-arch/` with the new selector, artifact
   hashes, environments, and exact vector digest.
5. Run the shipped retrieval, semantic-search, privacy, and release test suites.

Generated evaluation runs are CI artifacts and are not part of pin governance.
They may be regenerated for analysis, but they are never a release-policy
prerequisite and must not be committed under `docs/`.
