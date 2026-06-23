// Minimal `vscode` module stub for vitest unit tests. Only the surface used by
// the unit-tested modules (path resolution, URIs) is implemented; the real API
// is exercised by the (host-bound) integration tests, not these unit tests.
import * as nodePath from "path";

class Uri {
  private constructor(public readonly fsPath: string) {}
  static file(p: string): Uri {
    return new Uri(p);
  }
  static joinPath(base: Uri, ...segs: string[]): Uri {
    return new Uri(nodePath.join(base.fsPath, ...segs));
  }
}

class Position {
  constructor(public readonly line: number, public readonly character: number) {}
}
class Range {
  constructor(public readonly start: Position, public readonly end: Position) {}
}
class Location {
  constructor(public readonly uri: Uri, public readonly position: Position) {}
}
class Selection extends Range {}

const workspace = {
  workspaceFolders: undefined as readonly { uri: Uri }[] | undefined,
  getConfiguration() {
    return { get: () => undefined };
  },
};

const window = {
  activeTextEditor: undefined,
  showErrorMessage: () => Promise.resolve(undefined),
  showWarningMessage: () => Promise.resolve(undefined),
  showInformationMessage: () => Promise.resolve(undefined),
};

const commands = { executeCommand: () => Promise.resolve(undefined) };

const TextEditorRevealType = { InCenter: 2 };

export {
  Uri,
  Position,
  Range,
  Location,
  Selection,
  workspace,
  window,
  commands,
  TextEditorRevealType,
};
