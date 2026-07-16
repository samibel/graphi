// "Show blast-radius" command (AC-2): reads the symbol under the cursor, queries
// the daemon over the NEGOTIATED analyzer route, and renders impacted
// symbols/files as QuickPick items with file:line citations that navigate the
// editor on selection. Strictly read-only.
import * as vscode from "vscode";
import type { Connection } from "./connection";
import type { GraphiClient } from "./graphiClient";
import { hasResource } from "./graphiClient";
import { toCitationItems, toSearchCitations, type CitationItem } from "./citations";
import type { SearchMatch } from "./contract";

// CitationItem is re-exported for callers that pair this command with reveal().
export type { CitationItem };

/** symbolUnderCursor returns the word at the active editor's cursor. */
export function symbolUnderCursor(editor: vscode.TextEditor | undefined): string {
  if (!editor) return "";
  const range = editor.document.getWordRangeAtPosition(editor.selection.active);
  return range ? editor.document.getText(range) : "";
}

/**
 * reveal opens filePath:line in the editor (click-navigable citation). The path
 * is constrained to a workspace-resolvable document: a webview/Engine-supplied
 * path is resolved against the workspace folder and must not escape it (S5). An
 * unresolvable/escaping path is rejected silently (no path traversal, no open).
 */
export async function reveal(filePath: string, line: number): Promise<void> {
  const uri = resolveWorkspaceUri(filePath);
  if (!uri) return;
  const doc = await vscode.workspace.openTextDocument(uri);
  const ed = await vscode.window.showTextDocument(doc);
  const pos = new vscode.Position(Math.max(0, line - 1), 0);
  ed.revealRange(new vscode.Range(pos, pos), vscode.TextEditorRevealType.InCenter);
  ed.selection = new vscode.Selection(pos, pos);
}

/**
 * Resolve an Engine/webview-supplied path to a URI inside an open workspace
 * folder. Returns null when there is no workspace, the path escapes the folder,
 * or it cannot be resolved — fail-closed against traversal (S5). Exported for
 * unit testing the path-safety logic.
 */
export function resolveWorkspaceUri(
  filePath: string,
  folders: readonly { uri: vscode.Uri }[] | undefined = vscode.workspace
    .workspaceFolders,
): vscode.Uri | null {
  if (!filePath) return null;
  if (!folders || folders.length === 0) return null;
  const isAbsolute = filePath.startsWith("/") || /^[A-Za-z]:[\\/]/.test(filePath);
  for (const folder of folders) {
    const candidate = isAbsolute
      ? vscode.Uri.file(filePath)
      : vscode.Uri.joinPath(folder.uri, filePath);
    const root = folder.uri.fsPath.replace(/[\\/]+$/, "");
    const target = candidate.fsPath;
    // Must be contained within the folder (block ../ traversal and escapes).
    if (target === root || target.startsWith(root + "/") || target.startsWith(root + "\\")) {
      return candidate;
    }
  }
  return null;
}

/**
 * Resolve cursor text to an exact graph NodeId for an interactive command.
 * Ambiguity always requires an explicit user choice; cancellation performs no
 * analyzer request. A fuzzy search hit is never treated as identity.
 */
export async function chooseSymbolMatch(
  client: GraphiClient,
  symbolText: string,
): Promise<SearchMatch | null> {
  const resolution = await client.resolveSymbol(symbolText);
  if (resolution.outcome === "not_found") {
    void vscode.window.showInformationMessage(
      `graphi: no exact indexed symbol found for "${symbolText}".`,
    );
    return null;
  }
  if (resolution.outcome === "found") return resolution.matches[0];

  const picked = await vscode.window.showQuickPick(
    toSearchCitations(resolution.matches),
    {
      placeHolder: `"${symbolText}" is ambiguous — select the exact symbol`,
    },
  );
  if (!picked) return null;
  return resolution.matches.find((match) => match.node_id === picked.label) ?? null;
}

/** runBlastRadius is the command handler. */
export async function runBlastRadius(conn: Connection): Promise<void> {
  let client = conn.client();
  if (!client) {
    const ok = await conn.refresh();
    if (!ok) {
      offlinePrompt();
      return;
    }
    client = conn.client();
  }
  if (!client) return;
  const contract = conn.contract();
  if (!contract || !hasResource(contract, "search")) {
    void vscode.window.showInformationMessage(
      "graphi: this Engine does not advertise symbol search.",
    );
    return;
  }
  const route = conn.analyzerRoute();
  if (!route) {
    void vscode.window.showInformationMessage(
      "graphi: this Engine does not advertise an impact analyzer.",
    );
    return;
  }
  const sym = symbolUnderCursor(vscode.window.activeTextEditor);
  if (!sym) {
    void vscode.window.showWarningMessage("graphi: place the cursor on a symbol first.");
    return;
  }
  try {
    const match = await chooseSymbolMatch(client, sym);
    if (!match) return;
    const impact = await client.getImpact(route, match.node_id);
    if (impact.outcome === "not_found") {
      void vscode.window.showInformationMessage(
        `graphi: "${match.qualified_name}" disappeared from the index; retry after ingest.`,
      );
      return;
    }
    const items = toCitationItems(impact, new Map());
    if (items.length === 0) {
      void vscode.window.showInformationMessage(
        `graphi: no blast-radius for "${match.qualified_name}".`,
      );
      return;
    }
    const picked = await vscode.window.showQuickPick(items, {
      placeHolder: `Blast-radius of ${match.qualified_name} (${items.length} impacted)`,
    });
    if (picked?.filePath && picked.line !== undefined) {
      await reveal(picked.filePath, picked.line);
    }
  } catch (e) {
    void vscode.window.showErrorMessage(`graphi: ${sanitizeErr(e)}`);
  }
}

/** Shared offline toast with a Retry affordance. */
export function offlinePrompt(): void {
  void vscode.window
    .showErrorMessage(
      "graphi daemon is offline. Start it with `graphi http` and retry.",
      "Retry",
    )
    .then((b) => b === "Retry" && vscode.commands.executeCommand("graphi.retry"));
}

/** Sanitize an error for display — message only, never URL/token internals. */
export function sanitizeErr(e: unknown): string {
  if (e instanceof Error) return e.message;
  return "request failed";
}
