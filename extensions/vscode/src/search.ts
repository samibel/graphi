// "graphi: search" command (AC-2): input box → /search → QuickPick with
// file:line citations → navigate on selection. Strictly read-only.
import * as vscode from "vscode";
import type { Connection } from "./connection";
import { reveal, offlinePrompt, sanitizeErr, type CitationItem } from "./blastRadius";
import { toSearchCitations } from "./citations";

export async function runSearch(conn: Connection): Promise<void> {
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
  const q = await vscode.window.showInputBox({
    prompt: "graphi symbol/graph search",
    placeHolder: "e.g. pkg.Func",
  });
  if (!q) return;
  try {
    const res = await client.search(q);
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
    void vscode.window.showErrorMessage(`graphi: ${sanitizeErr(e)}`);
  }
}
