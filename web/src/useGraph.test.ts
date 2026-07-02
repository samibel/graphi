// chooseMatch is the pure disambiguation rule for typed seeds: unique exact
// qualified-name match wins, then a unique last-segment name match, then a
// single-result list; anything else is ambiguous (null → candidate picker).
import { describe, expect, it, vi } from "vitest";
import type { SearchMatch } from "./types";

vi.mock("./graphiClient", () => ({
  getContract: vi.fn(),
  resolveAnalyzerRoute: vi.fn(),
  fetchNeighborhood: vi.fn(),
  fetchImpact: vi.fn(),
  searchSymbols: vi.fn(),
  subscribeSSE: vi.fn(),
  SchemaMismatchError: class extends Error {},
}));
const { chooseMatch } = await import("./useGraph");

function m(id: string, qn: string): SearchMatch {
  return {
    node_id: id,
    kind: "function",
    qualified_name: qn,
    source_path: "a.go",
    line: 1,
    column: 1,
    rank: -1,
  };
}

describe("chooseMatch", () => {
  it("returns null for no matches", () => {
    expect(chooseMatch("x", [])).toBeNull();
  });

  it("returns the single match", () => {
    expect(chooseMatch("anything", [m("s1", "pkg.Func")])?.node_id).toBe("s1");
  });

  it("prefers a unique case-insensitive exact qualified-name match", () => {
    const got = chooseMatch("MAIN.RELEASE", [
      m("s1", "main.release"),
      m("s2", "main.releaseAll"),
    ]);
    expect(got?.node_id).toBe("s1");
  });

  it("resolves a unique last-segment name match (bare 'release')", () => {
    const got = chooseMatch("release", [
      m("s1", "main.release"),
      m("s2", "cmd/release/main.go"),
    ]);
    expect(got?.node_id).toBe("s1");
  });

  it("returns null when several matches share the name (ambiguous)", () => {
    expect(
      chooseMatch("release", [m("s1", "main.release"), m("s2", "pkg.release")]),
    ).toBeNull();
  });
});
