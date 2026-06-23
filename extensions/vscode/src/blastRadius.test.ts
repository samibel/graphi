import { describe, expect, it } from "vitest";
import { resolveWorkspaceUri } from "./blastRadius";
import { Uri } from "./test/vscode-stub";
import type * as vscode from "vscode";

// The stub Uri is structurally compatible with what resolveWorkspaceUri reads
// (fsPath + joinPath); cast for the static check (real Uri only at runtime host).
const folders = [{ uri: Uri.file("/work/repo") }] as unknown as {
  uri: vscode.Uri;
}[];

describe("resolveWorkspaceUri — path-traversal guard (S5)", () => {
  it("resolves a relative path inside the workspace", () => {
    const uri = resolveWorkspaceUri("pkg/a.go", folders);
    expect(uri?.fsPath).toBe("/work/repo/pkg/a.go");
  });

  it("accepts an absolute path inside the workspace", () => {
    const uri = resolveWorkspaceUri("/work/repo/pkg/a.go", folders);
    expect(uri?.fsPath).toBe("/work/repo/pkg/a.go");
  });

  it("rejects ../ traversal that escapes the workspace", () => {
    expect(resolveWorkspaceUri("../../etc/passwd", folders)).toBeNull();
  });

  it("rejects an absolute path outside the workspace", () => {
    expect(resolveWorkspaceUri("/etc/passwd", folders)).toBeNull();
  });

  it("rejects when there is no workspace folder", () => {
    expect(resolveWorkspaceUri("a.go", undefined)).toBeNull();
    expect(resolveWorkspaceUri("a.go", [])).toBeNull();
  });

  it("rejects empty paths", () => {
    expect(resolveWorkspaceUri("", folders)).toBeNull();
  });
});
