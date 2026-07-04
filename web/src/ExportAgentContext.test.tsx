import { renderAt } from "./testRender";
import { ExportAgentContext } from "./ExportAgentContext";
import type { ResultNode, ResultEdge } from "./types";

const node: ResultNode = {
  id: "n1",
  kind: "function",
  qualified_name: "pkg.Foo",
  source_path: "pkg/foo.go",
  line: 10,
  column: 1,
};

const edge: ResultEdge = {
  id: "e1",
  from: "n1",
  to: "n2",
  kind: "calls",
  confidence_tier: "confirmed",
  confidence: 0.95,
  reason: "static call",
  evidence: ["pkg/foo.go:12"],
};

describe("ExportAgentContext", () => {
  it("renders Markdown export by default", async () => {
    const { container } = await renderAt(
      <ExportAgentContext node={node} edges={[edge]} />,
    );
    expect(container.textContent).toContain("Export agent context");
    expect(container.textContent).toContain("pkg.Foo");
    expect(container.textContent).toContain("calls");
  });

  it("renders no node fallback", async () => {
    const { container } = await renderAt(<ExportAgentContext />);
    expect(container.textContent).toContain("Agent Context");
  });
});
