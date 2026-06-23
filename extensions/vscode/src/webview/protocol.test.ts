import { describe, expect, it } from "vitest";
import { parseWebviewMessage } from "./protocol";

describe("parseWebviewMessage — inbound trust boundary (S5)", () => {
  it("accepts a well-formed ready message", () => {
    expect(parseWebviewMessage({ kind: "ready" })).toEqual({ kind: "ready" });
  });
  it("accepts a well-formed select message", () => {
    expect(parseWebviewMessage({ kind: "select", id: "pkg.X" })).toEqual({
      kind: "select",
      id: "pkg.X",
    });
  });
  it("accepts a well-formed reveal message", () => {
    expect(parseWebviewMessage({ kind: "reveal", path: "a.go", line: 4 })).toEqual({
      kind: "reveal",
      path: "a.go",
      line: 4,
    });
  });
  it("rejects unknown kinds", () => {
    expect(parseWebviewMessage({ kind: "exec", cmd: "rm -rf /" })).toBeNull();
  });
  it("rejects malformed select/reveal payloads", () => {
    expect(parseWebviewMessage({ kind: "select" })).toBeNull();
    expect(parseWebviewMessage({ kind: "reveal", path: "a.go" })).toBeNull();
    expect(parseWebviewMessage({ kind: "reveal", path: 1, line: 2 })).toBeNull();
  });
  it("rejects non-objects", () => {
    expect(parseWebviewMessage(null)).toBeNull();
    expect(parseWebviewMessage("reveal")).toBeNull();
  });
});
