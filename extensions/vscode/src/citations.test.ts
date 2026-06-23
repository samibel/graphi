import { describe, expect, it } from "vitest";
import { toCitationItems } from "./citations";
import type { ImpactResult, ResultNode } from "./contract";

describe("toCitationItems", () => {
  it("maps impacted ids to file:line citations", () => {
    const impact: ImpactResult = {
      analyzer: "impact",
      impacted: ["pkg.A", "pkg.B"],
      provenance: { tier: "confirmed" },
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
    const impact: ImpactResult = { analyzer: "impact", impacted: ["ghost"] };
    const items = toCitationItems(impact, new Map());
    expect(items).toHaveLength(1);
    expect(items[0].filePath).toBeUndefined();
  });
});
