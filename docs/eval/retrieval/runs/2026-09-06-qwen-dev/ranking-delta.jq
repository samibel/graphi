# Compare existing harness metrics; no rescoring or population selection.
# Usage: jq -s -f ranking-delta.jq before-report.json after-report.json
def candidate:
  [.reproducible.baselines[] | select(.name == "semantic_first")]
  | if length != 1 then error("expected exactly one semantic_first baseline")
    else .[0] end;

if length != 2 then error("expected before and after reports") else . end
| if .[0].reproducible.dataset.sha256 != .[1].reproducible.dataset.sha256
  then error("different datasets") else . end
| (.[0] | candidate) as $before
| (.[1] | candidate) as $after
| if ($before.queries | length) != 44
    or ([$before.queries[] | [.id, .stratum, .split, .metrics.scored]]
        != [$after.queries[] | [.id, .stratum, .split, .metrics.scored]])
    or ([$before.queries[] | .split == "dev"] | all | not)
  then error("different, incomplete or non-development populations") else . end
| [range(0; $before.queries | length) as $i
   | $before.queries[$i] as $b | $after.queries[$i] as $a
   | select($b.metrics.scored)
   | {id: $b.id, stratum: $b.stratum,
      before: $b.metrics.ndcg_at_10, after: $a.metrics.ndcg_at_10,
      delta: ($a.metrics.ndcg_at_10 - $b.metrics.ndcg_at_10),
      before_top3: [$b.hits[0:3][].qualified_name],
      after_top3: [$a.hits[0:3][].qualified_name]}] as $queries
| if ($queries | length) != 41 then error("expected 41 scored dev queries") else . end
| {scored: ($queries | length),
   improved: ([$queries[] | select(.delta > 0)] | length),
   regressed: ([$queries[] | select(.delta < 0)] | length),
   unchanged: ([$queries[] | select(.delta == 0)] | length),
   overall: {before: $before.overall.metrics, after: $after.overall.metrics},
   strata: [($before.strata | keys[]) as $s
            | {stratum: $s, before: $before.strata[$s], after: $after.strata[$s]}],
   queries_by_delta: ($queries | sort_by(.delta, .id))}
