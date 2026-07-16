import { describe, expect, it } from "vitest";
import { toCitationItems, toSearchCitations } from "./citations";
import type { ImpactResult, ResultNode } from "./contract";

describe("toCitationItems", () => {
  it("maps impacted ids to file:line citations", () => {
    const impact: ImpactResult = {
      analyzer: "impact",
      outcome: "found",
      symbol: "seed",
      nodes: [
        {
          node: { id: "pkg.A", kind: "func", qualified_name: "pkg.A", source_path: "a.go", line: 10, column: 1 },
          reached_via: { id: "e1", from: "seed", to: "pkg.A", kind: "calls", confidence_tier: "confirmed", confidence: 1, reason: "call", evidence: ["a.go:10"] },
          depth: 1,
        },
        {
          node: { id: "pkg.B", kind: "func", qualified_name: "pkg.B", source_path: "b.go", line: 42, column: 1 },
          reached_via: { id: "e2", from: "pkg.A", to: "pkg.B", kind: "calls", confidence_tier: "confirmed", confidence: 1, reason: "call", evidence: ["b.go:42"] },
          depth: 2,
        },
      ],
    };
    const nodes = new Map<string, ResultNode>([
      ["pkg.A", { id: "pkg.A", kind: "func", qualified_name: "pkg.A", source_path: "a.go", line: 10, column: 1 }],
      ["pkg.B", { id: "pkg.B", kind: "func", qualified_name: "pkg.B", source_path: "b.go", line: 42, column: 1 }],
    ]);
    const items = toCitationItems(impact, nodes);
    expect(items).toHaveLength(2);
    expect(items[0].filePath).toBe("a.go");
    expect(items[0].line).toBe(10);
    expect(items[0].detail).toBe("a.go:10");
    expect(items[1].detail).toBe("b.go:42");
  });

  it("handles impacted ids with no resolved node (citation still listed)", () => {
    const impact: ImpactResult = {
      analyzer: "impact",
      outcome: "empty",
      symbol: "ghost",
      nodes: [],
    };
    const items = toCitationItems(impact, new Map());
    expect(items).toHaveLength(0);
  });

  it("maps the canonical engine search fields to citations", () => {
    const items = toSearchCitations([
      {
        node_id: "n1",
        kind: "function",
        qualified_name: "pkg.Search",
        source_path: "pkg/search.go",
        line: 17,
        column: 3,
        rank: -1.5,
      },
    ]);
    expect(items).toEqual([
      {
        label: "n1",
        description: "pkg.Search",
        detail: "pkg/search.go:17",
        filePath: "pkg/search.go",
        line: 17,
      },
    ]);
  });
});
