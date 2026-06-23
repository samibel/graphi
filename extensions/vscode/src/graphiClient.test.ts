import { describe, expect, it } from "vitest";
import { assertLoopback, GraphiClient } from "./graphiClient";

describe("assertLoopback", () => {
  it("accepts loopback URLs", () => {
    expect(() => assertLoopback("http://127.0.0.1:8080")).not.toThrow();
    expect(() => assertLoopback("http://localhost:8080")).not.toThrow();
    expect(() => assertLoopback("http://[::1]:8080")).not.toThrow();
  });

  it("refuses non-loopback URLs (zero-outbound contract)", () => {
    expect(() => assertLoopback("http://0.0.0.0:8080")).toThrow(/non-loopback/);
    expect(() => assertLoopback("http://8.8.8.8:8080")).toThrow(/non-loopback/);
    expect(() => assertLoopback("http://example.com:8080")).toThrow(/non-loopback/);
  });

  it("refuses malformed URLs", () => {
    expect(() => assertLoopback("not a url")).toThrow(/invalid/);
  });
});

describe("GraphiClient constructor", () => {
  it("constructs on loopback", () => {
    expect(() => new GraphiClient("http://127.0.0.1:8080")).not.toThrow();
  });

  it("throws on non-loopback at construction (fails fast)", () => {
    expect(() => new GraphiClient("http://10.0.0.1:8080")).toThrow(/non-loopback/);
  });
});
