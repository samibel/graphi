import { afterEach, describe, expect, it, vi } from "vitest";
import { act, useState } from "react";
import { renderAt, flush } from "./testRender";
import { AgentToolsPanel } from "./AgentToolsPanel";
import type { AgentToolResponse } from "./graphiClient";
import {
  agentToolPayload,
  agentToolPayloadEmpty,
  agentToolPayloadHeuristic,
} from "./__fixtures__/contract";

const relatedFiles = vi.fn<(t: string, d?: string) => Promise<AgentToolResponse>>();
const changeRisk = vi.fn<(t: string) => Promise<AgentToolResponse>>();
const agentBrief = vi.fn<(t?: string) => Promise<AgentToolResponse>>();

vi.mock("./graphiClient", () => ({
  relatedFiles: (t: string, d?: string) => relatedFiles(t, d),
  changeRisk: (t: string) => changeRisk(t),
  agentBrief: (t?: string) => agentBrief(t),
}));

afterEach(() => {
  vi.clearAllMocks();
});

function button(container: HTMLElement, label: string): HTMLButtonElement {
  const btn = Array.from(
    container.querySelectorAll<HTMLButtonElement>(".tool-buttons button"),
  ).find((b) => b.textContent === label);
  if (!btn) throw new Error(`no "${label}" button`);
  return btn;
}

async function click(el: HTMLElement) {
  await act(async () => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await new Promise((r) => setTimeout(r, 0));
  });
}

describe("AgentToolsPanel", () => {
  it("Related files → renders ranked items with reason, evidence path:line and confidence", async () => {
    relatedFiles.mockResolvedValue({ available: true, result: agentToolPayload });
    const { container } = await renderAt(
      <AgentToolsPanel target="n1" targetLabel="pkg.Func" briefAdvertised={true} />,
    );
    await click(button(container, "Related files"));
    expect(relatedFiles).toHaveBeenCalledWith("n1", undefined);
    const text = container.textContent!;
    expect(text).toContain("related_files — ok");
    expect(text).toContain("#1");
    expect(text).toContain("pkg/a.go");
    expect(text).toContain("co-changed 12 times");
    expect(text).toContain("pkg/a.go:10");
    expect(text).toContain("confidence: high");
    expect(text).toContain("(normalized)");
  });

  it("Change risk → heuristic confidence is visibly marked with the data-tier convention", async () => {
    changeRisk.mockResolvedValue({
      available: true,
      result: agentToolPayloadHeuristic,
    });
    const { container } = await renderAt(
      <AgentToolsPanel target="n1" briefAdvertised={true} />,
    );
    await click(button(container, "Change risk"));
    const result = container.querySelector('[data-testid="tool-result"]');
    expect(result?.getAttribute("data-tier")).toBe("heuristic");
    expect(container.textContent).toContain("(heuristic)");
    expect(container.textContent).toContain("heuristic — verify before acting");
    // Truncated limits render a visible partial-result marker.
    expect(container.textContent).toContain("showing 1 of 9 (truncated, 8 dropped)");
  });

  it("typed unavailable → clear unavailable state, not blank UI", async () => {
    relatedFiles.mockResolvedValue({
      available: false,
      reason: "capability unavailable",
    });
    const { container } = await renderAt(
      <AgentToolsPanel target="n1" briefAdvertised={true} />,
    );
    await click(button(container, "Related files"));
    const state = container.querySelector('[data-testid="tool-unavailable"]');
    expect(state).not.toBeNull();
    expect(state!.textContent).toContain(
      "related_files unavailable — capability unavailable",
    );
    expect(container.querySelector('[data-testid="tool-result"]')).toBeNull();
  });

  it("empty outcome → explicit empty state", async () => {
    relatedFiles.mockResolvedValue({
      available: true,
      result: agentToolPayloadEmpty,
    });
    const { container } = await renderAt(
      <AgentToolsPanel target="n1" briefAdvertised={true} />,
    );
    await click(button(container, "Related files"));
    expect(container.textContent).toContain("related_files — empty");
    expect(container.textContent).toContain("No results for this target.");
  });

  it("rejected call → error state, no crash", async () => {
    changeRisk.mockRejectedValue(new Error("network down"));
    const { container } = await renderAt(
      <AgentToolsPanel target="n1" briefAdvertised={true} />,
    );
    await click(button(container, "Change risk"));
    expect(container.textContent).toContain("change_risk failed: network down");
  });

  it("agent brief is gated on the /contract advertisement", async () => {
    const { container } = await renderAt(
      <AgentToolsPanel target="n1" briefAdvertised={false} />,
    );
    const brief = button(container, "Agent brief");
    expect(brief.disabled).toBe(true);
    expect(container.textContent).toContain(
      "agent brief not advertised by the daemon",
    );
    await click(brief); // disabled → no call
    expect(agentBrief).not.toHaveBeenCalled();
  });

  it("agent brief runs with the target as topic when advertised", async () => {
    agentBrief.mockResolvedValue({ available: true, result: agentToolPayload });
    const { container } = await renderAt(
      <AgentToolsPanel target="n1" briefAdvertised={true} />,
    );
    await click(button(container, "Agent brief"));
    expect(agentBrief).toHaveBeenCalledWith("n1");
    expect(container.textContent).toContain("agent_brief — ok");
  });

  it("target-scoped buttons are disabled without a target", async () => {
    const { container } = await renderAt(
      <AgentToolsPanel target={null} briefAdvertised={true} />,
    );
    expect(button(container, "Related files").disabled).toBe(true);
    expect(button(container, "Change risk").disabled).toBe(true);
    expect(button(container, "Agent brief").disabled).toBe(false);
  });

  it("drops a stale result when the target changes", async () => {
    relatedFiles.mockResolvedValue({ available: true, result: agentToolPayload });
    function Harness() {
      const [target, setTarget] = useState("n1");
      return (
        <>
          <button data-testid="switch-target" onClick={() => setTarget("n2")} />
          <AgentToolsPanel target={target} briefAdvertised={true} />
        </>
      );
    }
    const { container } = await renderAt(<Harness />);
    await click(button(container, "Related files"));
    expect(container.querySelector('[data-testid="tool-result"]')).not.toBeNull();
    await click(
      container.querySelector<HTMLButtonElement>('[data-testid="switch-target"]')!,
    );
    await flush();
    expect(container.querySelector('[data-testid="tool-result"]')).toBeNull();
  });
});
