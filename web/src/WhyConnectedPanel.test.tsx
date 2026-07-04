import { renderAt } from "./testRender";
import { WhyConnectedPanel } from "./WhyConnectedPanel";
import type { ResultEdge } from "./types";

const fixture: ResultEdge = {
  id: "e1",
  from: "A",
  to: "B",
  kind: "calls",
  confidence_tier: "heuristic",
  confidence: 0.4,
  reason: "static call",
  evidence: ["a.go:5"],
};

describe("WhyConnectedPanel", () => {
  it("renders edge kind, tier, confidence, reason and evidence", async () => {
    const { container } = await renderAt(<WhyConnectedPanel edges={[fixture]} />);
    expect(container.textContent).toContain("Why connected");
    expect(container.textContent).toContain("calls");
    expect(container.textContent).toContain("(heuristic)");
    expect(container.textContent).toContain("0.40");
    expect(container.textContent).toContain("static call");
    expect(container.textContent).toContain("a.go:5");
  });

  it("renders nothing when no edges", async () => {
    const { container } = await renderAt(<WhyConnectedPanel edges={[]} />);
    expect(container.firstChild).toBeNull();
  });
});
