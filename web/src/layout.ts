// Deterministic radial (BFS-ring) layout for the neighborhood graph. The seed
// sits at the center, its direct neighbors on ring 1, depth-2 nodes on ring 2,
// and so on. Pure and dependency-free so it is unit-testable without Sigma;
// GraphView feeds the result into Graphology node attributes.
//
// Determinism matters twice: the layout is informative (distance from the
// center = hop distance from the seed), and an SSE refresh with the same graph
// reproduces the same positions, so the viewport does not re-scramble (AC-6).

export interface LayoutPoint {
  x: number;
  y: number;
}

interface LayoutEdge {
  from: string;
  to: string;
}

/**
 * Compute positions for `nodeIds`. `seedId` (when present in the graph) is the
 * BFS root; otherwise the highest-degree node is used. Nodes unreachable from
 * the root are placed on an extra outer ring. Within a ring, nodes are ordered
 * by their BFS parent's angle (then id) so subtrees stay angularly grouped.
 */
export function radialLayout(
  nodeIds: string[],
  edges: LayoutEdge[],
  seedId: string | null,
): Map<string, LayoutPoint> {
  const pos = new Map<string, LayoutPoint>();
  if (nodeIds.length === 0) return pos;
  const ids = [...nodeIds].sort();

  const adjacency = new Map<string, string[]>();
  for (const id of ids) adjacency.set(id, []);
  for (const e of edges) {
    if (adjacency.has(e.from) && adjacency.has(e.to) && e.from !== e.to) {
      adjacency.get(e.from)!.push(e.to);
      adjacency.get(e.to)!.push(e.from);
    }
  }

  const root =
    seedId !== null && adjacency.has(seedId)
      ? seedId
      : ids.reduce((best, id) =>
          adjacency.get(id)!.length > adjacency.get(best)!.length ? id : best,
        );

  // BFS ring assignment (neighbors visited in sorted order → deterministic).
  const level = new Map<string, number>([[root, 0]]);
  const parent = new Map<string, string>();
  const queue = [root];
  while (queue.length > 0) {
    const cur = queue.shift()!;
    for (const next of [...adjacency.get(cur)!].sort()) {
      if (!level.has(next)) {
        level.set(next, level.get(cur)! + 1);
        parent.set(next, cur);
        queue.push(next);
      }
    }
  }

  let maxLevel = 0;
  for (const l of level.values()) maxLevel = Math.max(maxLevel, l);
  // Disconnected leftovers go on one extra outer ring.
  const strayLevel = maxLevel + 1;
  for (const id of ids) if (!level.has(id)) level.set(id, strayLevel);

  const rings = new Map<number, string[]>();
  for (const id of ids) {
    const l = level.get(id)!;
    if (!rings.has(l)) rings.set(l, []);
    rings.get(l)!.push(id);
  }

  const angle = new Map<string, number>([[root, 0]]);
  pos.set(root, { x: 0, y: 0 });
  for (let l = 1; l <= strayLevel; l++) {
    const members = rings.get(l);
    if (!members || members.length === 0) continue;
    members.sort((a, b) => {
      const pa = angle.get(parent.get(a) ?? "") ?? 0;
      const pb = angle.get(parent.get(b) ?? "") ?? 0;
      if (pa !== pb) return pa - pb;
      return a < b ? -1 : 1;
    });
    members.forEach((id, i) => {
      const theta = (2 * Math.PI * i) / members.length;
      angle.set(id, theta);
      pos.set(id, { x: l * Math.cos(theta), y: l * Math.sin(theta) });
    });
  }
  return pos;
}
