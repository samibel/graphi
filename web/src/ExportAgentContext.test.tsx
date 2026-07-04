import { afterEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
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
  evidence: ["pkg/foo.go:12", "pkg/bar.go:7"],
};

async function click(el: Element) {
  await act(async () => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await new Promise((r) => setTimeout(r, 0));
  });
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  delete (document as unknown as Record<string, unknown>).execCommand;
});

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

  it("renders the Focus section for the wired node and evidence path:line lines", async () => {
    const { container } = await renderAt(
      <ExportAgentContext node={node} edges={[edge]} />,
    );
    const md = container.querySelector("pre")!.textContent!;
    expect(md).toContain("## Focus");
    expect(md).toContain("**pkg.Foo** (function) at pkg/foo.go:10");
    expect(md).toContain("  - evidence: pkg/foo.go:12");
    expect(md).toContain("  - evidence: pkg/bar.go:7");
  });

  it("markdown export matches the snapshot", async () => {
    const { container } = await renderAt(
      <ExportAgentContext node={node} edges={[edge]} />,
    );
    expect(container.querySelector("pre")!.textContent).toMatchSnapshot();
  });

  it("Copy uses navigator.clipboard and confirms with a Copied state", async () => {
    const writeText = vi.fn<(text: string) => Promise<void>>(async () => {});
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    const { container, unmount } = await renderAt(
      <ExportAgentContext node={node} edges={[edge]} />,
    );
    const copyBtn = container.querySelector('[data-testid="copy-button"]')!;
    expect(copyBtn.textContent).toBe("Copy");
    await click(copyBtn);
    expect(writeText).toHaveBeenCalledTimes(1);
    const copiedText = writeText.mock.calls[0][0];
    expect(copiedText).toContain("# Agent Context");
    expect(copiedText).toContain("## Focus");
    expect(copiedText).toBe(container.querySelector("pre")!.textContent);
    expect(copyBtn.textContent).toBe("Copied");
    unmount(); // clears the "Copied" reset timer
  });

  it("Copy copies the ACTIVE tab (json output after switching tabs)", async () => {
    const writeText = vi.fn<(text: string) => Promise<void>>(async () => {});
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    const { container, unmount } = await renderAt(
      <ExportAgentContext node={node} edges={[edge]} />,
    );
    const jsonTab = Array.from(container.querySelectorAll(".export-tabs button")).find(
      (b) => b.textContent === "json",
    )!;
    await click(jsonTab);
    await click(container.querySelector('[data-testid="copy-button"]')!);
    const copiedText = writeText.mock.calls[0][0];
    expect(JSON.parse(copiedText)).toMatchObject({ node: { id: "n1" } });
    unmount(); // clears the "Copied" reset timer
  });

  it("falls back to a textarea + execCommand when the Clipboard API is absent", async () => {
    // jsdom has no navigator.clipboard by default; make that explicit.
    vi.stubGlobal("navigator", { ...navigator, clipboard: undefined });
    const execCommand = vi.fn(() => true);
    (document as Document & { execCommand: typeof execCommand }).execCommand =
      execCommand;
    const { container, unmount } = await renderAt(
      <ExportAgentContext node={node} edges={[edge]} />,
    );
    const copyBtn = container.querySelector('[data-testid="copy-button"]')!;
    await click(copyBtn);
    expect(execCommand).toHaveBeenCalledWith("copy");
    expect(copyBtn.textContent).toBe("Copied");
    unmount(); // clears the "Copied" reset timer
  });
});
