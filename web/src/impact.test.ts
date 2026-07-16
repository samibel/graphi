import { describe, expect, it } from "vitest";
import { impactNodeIDs } from "./impact";

describe("impactNodeIDs", () => {
  it("reads ids from the canonical engine reached-node shape", () => {
    const ids = impactNodeIDs({
      analyzer: "impact",
      outcome: "found",
      symbol: "seed",
      nodes: [
        {
          node: {
            id: "pkg.A",
            kind: "function",
            qualified_name: "pkg.A",
            source_path: "pkg/a.go",
            line: 7,
            column: 1,
          },
          reached_via: {
            id: "edge-1",
            from: "seed",
            to: "pkg.A",
            kind: "calls",
            confidence_tier: "confirmed",
            confidence: 1,
            reason: "call",
            evidence: ["pkg/a.go:7"],
          },
          depth: 1,
        },
      ],
    });

    expect([...ids]).toEqual(["pkg.A"]);
  });

  it("treats an omitted node list as empty", () => {
    expect(impactNodeIDs({ analyzer: "impact", outcome: "empty", symbol: "seed" }).size).toBe(0);
  });
});
