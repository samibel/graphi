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

  it("renders empty state", async () => {
    const { container } = await renderAt(
      <SymbolSearchPanel query="Bar" results={[]} onSelect={() => {}} />,
    );
    expect(container.textContent).toContain("No matches.");
  });
});
