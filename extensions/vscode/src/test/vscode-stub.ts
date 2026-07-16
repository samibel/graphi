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
  readonly range: Range;
  constructor(public readonly uri: Uri, positionOrRange: Position | Range) {
    this.range =
      positionOrRange instanceof Range
        ? positionOrRange
        : new Range(positionOrRange, positionOrRange);
  }
}
class Selection extends Range {}

class MarkdownString {
  value = "";
  isTrusted: boolean | undefined;
  constructor(value?: string) {
    this.value = value ?? "";
  }
  appendMarkdown(value: string): MarkdownString {
    this.value += value;
    return this;
  }
}

class Hover {
  constructor(
    public readonly contents: MarkdownString,
    public readonly range?: Range,
  ) {}
}

class Disposable {
  constructor(private readonly callOnDispose: () => unknown = () => undefined) {}
  dispose(): void {
    this.callOnDispose();
  }
}

class EventEmitter<T> {
  readonly event = (_listener: (event: T) => unknown): Disposable => new Disposable();
  fire(_event?: T): void {}
  dispose(): void {}
}

class TreeItem {
  description?: string;
  tooltip?: string;
  iconPath?: ThemeIcon;
  command?: unknown;
  constructor(
    public readonly label: string,
    public readonly collapsibleState: number,
  ) {}
}

class ThemeIcon {
  constructor(public readonly id: string) {}
}

const TreeItemCollapsibleState = { None: 0 };

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
  showQuickPick: () => Promise.resolve(undefined),
  onDidChangeTextEditorSelection: () => new Disposable(),
  createWebviewPanel: () => {
    throw new Error("createWebviewPanel is not configured in this unit test");
  },
};

const commands = { executeCommand: () => Promise.resolve(undefined) };

const TextEditorRevealType = { InCenter: 2 };
const ViewColumn = { Beside: 2 };

export {
  Uri,
  Position,
  Range,
  Location,
  Selection,
  MarkdownString,
  Hover,
  Disposable,
  EventEmitter,
  TreeItem,
  ThemeIcon,
  TreeItemCollapsibleState,
  workspace,
  window,
  commands,
  TextEditorRevealType,
  ViewColumn,
};
