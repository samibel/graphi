// Ambient declaration for the VS Code webview global injected at runtime.
// Only referenced inside rendered HTML strings (graphWebview), never in Node.
declare function acquireVsCodeApi(): { postMessage(msg: unknown): void };
