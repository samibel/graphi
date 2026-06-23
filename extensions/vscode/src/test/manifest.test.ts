// AC-6 manifest content assertion (host-independent). The VSIX is produced by
// `vsce package`; this test asserts the manifest DECLARES the required surfaces
// (activationEvents, contributed views, commands, configuration) so packaging
// cannot silently drop them. A full install/run is covered by the integration
// tests, which require a VS Code host.
import { describe, expect, it } from "vitest";
import { readFileSync } from "fs";
import { resolve } from "path";

const pkg = JSON.parse(
  readFileSync(resolve(__dirname, "../../package.json"), "utf8"),
) as {
  main: string;
  engines: { vscode: string };
  activationEvents: string[];
  contributes: {
    views?: Record<string, Array<{ id: string }>>;
    commands?: Array<{ command: string }>;
    configuration?: { properties?: Record<string, unknown> };
  };
};

describe("VSIX manifest content (AC-6)", () => {
  it("declares the bundled host entry + vscode engine", () => {
    expect(pkg.main).toBe("./out/extension.js");
    expect(pkg.engines.vscode).toMatch(/\^1\./);
  });

  it("declares activation events incl. the tree view", () => {
    expect(pkg.activationEvents).toContain("onView:graphiResults");
    expect(pkg.activationEvents).toContain("onCommand:graphi.showGraph");
  });

  it("contributes the graphiResults view", () => {
    const views = Object.values(pkg.contributes.views ?? {}).flat();
    expect(views.some((v) => v.id === "graphiResults")).toBe(true);
  });

  it("contributes the required commands", () => {
    const cmds = (pkg.contributes.commands ?? []).map((c) => c.command);
    for (const c of [
      "graphi.blastRadius",
      "graphi.search",
      "graphi.showGraph",
      "graphi.retry",
      "graphi.setAuthToken",
    ]) {
      expect(cmds).toContain(c);
    }
  });

  it("contributes configuration incl. URL, perf bounds, reconnect", () => {
    const props = pkg.contributes.configuration?.properties ?? {};
    for (const key of [
      "graphi.daemonUrl",
      "graphi.maxDepth",
      "graphi.maxNodes",
      "graphi.maxEdges",
      "graphi.reconnect.maxAttempts",
      "graphi.reconnect.maxIntervalMs",
    ]) {
      expect(props).toHaveProperty(key);
    }
  });

  it("does NOT store the auth token as a settings key (SecretStorage only)", () => {
    const props = pkg.contributes.configuration?.properties ?? {};
    expect(props).not.toHaveProperty("graphi.authToken");
    expect(props).not.toHaveProperty("graphi.token");
  });
});
