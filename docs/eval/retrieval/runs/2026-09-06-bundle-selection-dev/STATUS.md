# Bundle selection development status

Implementation, fresh development measurements and final validation complete.
No delegation remains active for this task.

- Actual emitted-source overlap: 33/40 -> 36/40 questions.
- At least one complete grade-3 span: 29/40 -> 33/40 questions.
- Every grade-3 span complete: 17/40 -> 21/40 questions.
- Complete grade-3 spans: 31/63 -> 39/63.
- Mean complete MCP response: 7173.2 -> 8018.6 cl100k tokens (increased).
- All 44 dev queries captured; 44/44 byte/digest/token identical across
  independent index builds. The snippet budget remains 1200.
- Ranking unchanged from architecture-dev: architecture 0.4624671522846825,
  NL behaviour 0.7029047223507069, exact identifier top1 4/4.
- Historical smoke and bundle-coverage references are not fresh measurements.
  Release and equal-recall savings remain unproven.

See README.md for the final validation record, ablations, limitations and commands.

Final validation (CGO_ENABLED=0):

- `go test ./...`: 136 test-bearing packages passed; zero failures.
- Focused final context, taskctx, retrieval, runtime and payload tests passed.
- `go build ./...`, `go run ./cmd/layerguard`, `git diff --check`: passed.
- Raw aggregate: 761/761 reproduced, zero discrepancies/unknowns.
- All seven ranking hit files byte-identical to architecture-dev/after.
- Fresh final MCP capture: all 44 queries, two independent indexes, exact
  source roundtrips, snippet limit <=1200, identical bytes/digests/counts.
- Frozen methodology, targets and dataset hashes unchanged.
- `-check-targets`: expected exit 1, historical qrel_blind_smoke is still red.
  No holdout execution, release pass, commit or push.
