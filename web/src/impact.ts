import type { ImpactResult } from "./payload";

/** Extract the canonical reached-node ids from engine/analysis.Analysis. */
export function impactNodeIDs(impact: ImpactResult): Set<string> {
  return new Set((impact.nodes ?? []).map((reached) => reached.node.id));
}
