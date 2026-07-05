// radialLayout is the pure BFS-ring layout: seed at the origin, one ring per
// hop, deterministic across calls (AC-6 — an SSE refresh must not re-scramble
// the viewport).
import { describe, expect, it } from "vitest";
import { radialLayout } from "./layout";

const dist = (p: { x: number; y: number }) => Math.hypot(p.x, p.y);

describe("radialLayout", () => {
  it("returns an empty map for an empty graph", () => {
    expect(radialLayout([], [], null).size).toBe(0);
  });

  it("places the seed at the origin and neighbors on hop-distance rings", () => {
    const pos = radialLayout(
      ["seed", "a", "b", "c"],
      [
        { from: "seed", to: "a" },
        { from: "seed", to: "b" },
        { from: "b", to: "c" },
      ],
      "seed",
    );
    expect(pos.get("seed")).toEqual({ x: 0, y: 0 });
    expect(dist(pos.get("a")!)).toBeCloseTo(1);
    expect(dist(pos.get("b")!)).toBeCloseTo(1);
    expect(dist(pos.get("c")!)).toBeCloseTo(2);
  });

  it("is deterministic: same graph → identical positions", () => {
    const nodes = ["n1", "n2", "n3", "n4"];
    const edges = [
      { from: "n1", to: "n2" },
      { from: "n1", to: "n3" },
      { from: "n3", to: "n4" },
    ];
    const a = radialLayout(nodes, edges, "n1");
    const b = radialLayout([...nodes].reverse(), edges, "n1");
    for (const id of nodes) expect(a.get(id)).toEqual(b.get(id));
  });

  it("falls back to the highest-degree node when the seed is unknown", () => {
    const pos = radialLayout(
      ["hub", "x", "y"],
      [
        { from: "hub", to: "x" },
        { from: "hub", to: "y" },
      ],
      "missing",
    );
    expect(pos.get("hub")).toEqual({ x: 0, y: 0 });
  });

  it("places disconnected nodes on an outer ring, never dropping them", () => {
    const pos = radialLayout(
      ["seed", "a", "stray"],
      [{ from: "seed", to: "a" }],
      "seed",
    );
    expect(pos.size).toBe(3);
    expect(dist(pos.get("stray")!)).toBeGreaterThan(dist(pos.get("a")!));
  });
});
