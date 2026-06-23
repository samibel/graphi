// "graphi: search" command (AC-3): input box → /search → QuickPick with
// file:line citations → navigate on selection.
import * as vscode from "vscode";
import type { Connection } from "./connection";
import { reveal, type CitationItem } from "./blastRadius";
import { toSearchCitations } from "./citations";

export async function runSearch(conn: Connection): Promise<void> {
  const client = conn.client();
  if (!client) {
    const ok = await conn.refresh();
    if (!ok) {
      void vscode.window.showErrorMessage(
        "graphi daemon is offline. Start it with `graphi http` and retry.",
        "Retry",
      ).then((b) => b === "Retry" && vscode.commands.executeCommand("graphi.retry"));
      return;
    }
  }
  const q = await vscode.window.showInputBox({
    prompt: "graphi symbol/graph search",
    placeHolder: "e.g. pkg.Func",
  });
  if (!q) return;
  try {
    const res = await client!.search(q);
    const items: CitationItem[] = toSearchCitations(res.matches);
    if (items.length === 0) {
      void vscode.window.showInformationMessage(`graphi: no matches for "${q}".`);
      return;
    }
    const picked = await vscode.window.showQuickPick(items, {
      placeHolder: `${items.length} matches for "${q}"`,
    });
    if (picked?.filePath && picked.line !== undefined) {
      await reveal(picked.filePath, picked.line);
    }
  } catch (e) {
    void vscode.window.showErrorMessage(`graphi: ${String(e)}`);
  }
}
