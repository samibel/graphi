import { describe, expect, it } from "vitest";
import { assertLoopbackBase, isLoopbackBase } from "./loopback";

describe("loopback guard (S1 — zero-outbound)", () => {
  it("accepts loopback origins and same-origin relative base", () => {
    expect(isLoopbackBase("")).toBe(true);
    expect(isLoopbackBase("http://127.0.0.1:8080")).toBe(true);
    expect(isLoopbackBase("http://localhost:8080")).toBe(true);
    expect(isLoopbackBase("http://[::1]:8080")).toBe(true);
  });

  it("rejects any non-loopback origin", () => {
    expect(isLoopbackBase("http://example.com")).toBe(false);
    expect(isLoopbackBase("https://graphi.cloud")).toBe(false);
    expect(isLoopbackBase("http://10.0.0.1")).toBe(false);
    expect(isLoopbackBase("http://169.254.1.1")).toBe(false);
  });

  it("assertLoopbackBase throws fail-closed on a non-loopback target", () => {
    expect(() => assertLoopbackBase("http://evil.example")).toThrow(/loopback-only/);
    expect(() => assertLoopbackBase("http://127.0.0.1:8080")).not.toThrow();
  });
});
