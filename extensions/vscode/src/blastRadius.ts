// "Show blast-radius" command (AC-2): reads the symbol under the cursor, queries
// the daemon, and renders impacted symbols/files as QuickPick items with
// file:line citations that navigate the editor on selection.
import * as vscode from "vscode";
import type { Connection } from "./connection";
import { toCitationItems, type CitationItem } from "./citations";

// CitationItem is re-exported for callers that pair this command with reveal().
export type { CitationItem };

/** symbolUnderCursor returns the word at the active editor's cursor. */
export function symbolUnderCursor(editor: vscode.TextEditor | undefined): string {
	if (!editor) return "";
	const range = editor.document.getWordRangeAtPosition(editor.selection.active);
	return range ? editor.document.getText(range) : "";
}

/** reveal opens filePath:line in the editor (click-navigable citation). */
export async function reveal(filePath: string, line: number): Promise<void> {
  const doc = await vscode.workspace.openTextDocument(filePath);
  const ed = await vscode.window.showTextDocument(doc);
  const pos = new vscode.Position(Math.max(0, line - 1), 0);
  ed.revealRange(new vscode.Range(pos, pos), vscode.TextEditorRevealType.InCenter);
  ed.selection = new vscode.Selection(pos, pos);
}

/** runBlastRadius is the command handler. */
export async function runBlastRadius(conn: Connection): Promise<void> {
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
  const sym = symbolUnderCursor(vscode.window.activeTextEditor);
  if (!sym) {
    void vscode.window.showWarningMessage("graphi: place the cursor on a symbol first.");
    return;
  }
  try {
    const [impact, neigh] = await Promise.all([
      client!.getImpact(sym),
      client!.getNeighborhood(sym, 1),
    ]);
    const nodes = new Map(neigh.nodes.map((n) => [n.id, n]));
    const items = toCitationItems(impact, nodes);
    if (items.length === 0) {
      void vscode.window.showInformationMessage(`graphi: no blast-radius for "${sym}".`);
      return;
    }
    const picked = await vscode.window.showQuickPick(items, {
      placeHolder: `Blast-radius of ${sym} (${items.length} impacted)`,
    });
    if (picked?.filePath && picked.line !== undefined) {
      await reveal(picked.filePath, picked.line);
    }
  } catch (e) {
    void vscode.window.showErrorMessage(`graphi: ${String(e)}`);
  }
}
