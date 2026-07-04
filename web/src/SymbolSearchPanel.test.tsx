import { renderAt } from "./testRender";
import { SymbolSearchPanel } from "./SymbolSearchPanel";
import type { SearchMatch } from "./types";

const fixtures: SearchMatch[] = [
  {
    node_id: "n1",
    kind: "function",
    qualified_name: "pkg.Foo",
    source_path: "pkg/foo.go",
    line: 10,
    column: 1,
    rank: 1,
  },
];

describe("SymbolSearchPanel", () => {
  it("renders search results with kind, path and line", async () => {
    const { container } = await renderAt(
      <SymbolSearchPanel query="Foo" results={fixtures} onSelect={() => {}} />,
    );
    expect(container.textContent).toContain("Search results for “Foo”");
    expect(container.textContent).toContain("pkg.Foo");
    expect(container.textContent).toContain("function · pkg/foo.go:10");
  });

  it("displays the match rank per row (position + rank score)", async () => {
    const twoRanked: SearchMatch[] = [
      { ...fixtures[0], rank: -1.5 },
      {
        ...fixtures[0],
        node_id: "n2",
        qualified_name: "pkg.FooBar",
        rank: -1.0,
      },
    ];
    const { container } = await renderAt(
      <SymbolSearchPanel query="Foo" results={twoRanked} onSelect={() => {}} />,
    );
    const rows = Array.from(container.querySelectorAll("li"));
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain("#1");
    expect(rows[0].textContent).toContain("rank -1.50");
    expect(rows[1].textContent).toContain("#2");
    expect(rows[1].textContent).toContain("rank -1");
    // No confidence field → no confidence text.
    expect(container.textContent).not.toContain("confidence");
  });

  it("displays confidence when the field is present", async () => {
    const withConfidence: SearchMatch[] = [{ ...fixtures[0], confidence: 0.87 }];
    const { container } = await renderAt(
      <SymbolSearchPanel
        query="Foo"
        results={withConfidence}
        onSelect={() => {}}
      />,
    );
    expect(container.textContent).toContain("rank 1");
    expect(container.textContent).toContain("confidence 0.87");
  });

  it("renders empty state", async () => {
    const { container } = await renderAt(
      <SymbolSearchPanel query="Bar" results={[]} onSelect={() => {}} />,
    );
    expect(container.textContent).toContain("No matches.");
  });
});
