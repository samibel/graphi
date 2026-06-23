import { afterEach, describe, expect, it } from "vitest";
import { WikiMarkdown } from "./WikiMarkdown";
import { renderAt } from "./testRender";
import { community1Md, communitySingletonMd } from "./__fixtures__/wiki";

let mounted: { unmount: () => void } | null = null;
afterEach(() => {
  mounted?.unmount();
  mounted = null;
});

describe("WikiMarkdown — safe, link-rewriting render (Slice 2)", () => {
  it("rewrites /wiki/c/N cross-links into in-client router links (AC-3)", async () => {
    const r = await renderAt(<WikiMarkdown body={community1Md} />);
    mounted = r;
    const anchors = Array.from(r.container.querySelectorAll("a"));
    const hrefs = anchors.map((a) => a.getAttribute("href"));
    // react-router <Link> renders an <a href> pointing at the route; the id is
    // preserved verbatim from the source (no re-slug).
    expect(hrefs).toContain("/wiki/c/2");
    expect(hrefs).toContain("/wiki/c/3");
  });

  it("rewrites the [← back to index](/wiki) back-link to the index route", async () => {
    const r = await renderAt(<WikiMarkdown body={community1Md} />);
    mounted = r;
    const back = Array.from(r.container.querySelectorAll("a")).find((a) =>
      (a.textContent ?? "").includes("back to index"),
    );
    expect(back?.getAttribute("href")).toBe("/wiki");
  });

  it("preserves cross-link ORDER exactly as the source (preservation, AC-2)", async () => {
    const r = await renderAt(<WikiMarkdown body={community1Md} />);
    mounted = r;
    const xrefHrefs = Array.from(r.container.querySelectorAll("a"))
      .map((a) => a.getAttribute("href"))
      .filter((h) => h?.startsWith("/wiki/c/"));
    expect(xrefHrefs).toEqual(["/wiki/c/2", "/wiki/c/3"]); // source order
  });

  it("preserves member text + order verbatim (no reorder/rewrite)", async () => {
    const r = await renderAt(<WikiMarkdown body={community1Md} />);
    mounted = r;
    const codes = Array.from(r.container.querySelectorAll("code")).map(
      (c) => c.textContent,
    );
    // The first four code spans are the member qualified names, in source order.
    expect(codes.slice(0, 4)).toEqual([
      "pkg/a.Alpha",
      "pkg/a.Beta",
      "pkg/a.Gamma",
      "pkg/a.Delta",
    ]);
  });

  it("HARD-disables raw HTML: <script>/<img onerror> render inert (S3)", async () => {
    const malicious =
      "# Pwn\n\nhello <script>window.__x=1</script> world " +
      '<img src=x onerror="window.__y=1"> and `safe`\n';
    const r = await renderAt(<WikiMarkdown body={malicious} />);
    mounted = r;
    // No live script/img element smuggled in.
    expect(r.container.querySelector("script")).toBeNull();
    expect(r.container.querySelector("img")).toBeNull();
    // No global side-effect executed.
    expect(
      (window as unknown as { __x?: number; __y?: number }).__x,
    ).toBeUndefined();
    expect(
      (window as unknown as { __x?: number; __y?: number }).__y,
    ).toBeUndefined();
    // The legitimate code span still renders as escaped text.
    expect(r.container.querySelector("code")?.textContent).toBe("safe");
  });

  it("renders a singleton (size-1) community body without error (AC-5)", async () => {
    const r = await renderAt(<WikiMarkdown body={communitySingletonMd} />);
    mounted = r;
    expect(r.container.querySelector("code")?.textContent).toBe("pkg/c.Solo");
    // Singleton has no cross-links (neighbors _none_) → no /wiki/c/ anchors,
    // but the back-link to /wiki is present.
    const hrefs = Array.from(r.container.querySelectorAll("a")).map((a) =>
      a.getAttribute("href"),
    );
    expect(hrefs).toEqual(["/wiki"]);
  });
});
