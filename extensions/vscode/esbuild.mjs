// Two-bundle build (SW-048). The extension host and the webview run in different
// runtimes, so they get separate esbuild entries:
//   - host:    platform=node, format=cjs, `vscode` external  → out/extension.js
//   - webview: platform=browser, format=iife, fully bundled   → out/webview/main.js
// `vscode:prepublish` runs this with --production (minify, no sourcemaps) so the
// VSIX ships only bundled output (sources/tests excluded via .vscodeignore).
import { build, context } from "esbuild";
import { rmSync } from "fs";

const production = process.argv.includes("--production");
const watch = process.argv.includes("--watch");

// Clean prior output so stale artifacts (e.g. tsc-compiled test .js) never ship
// in the VSIX. esbuild emits only the two bundles below.
if (!watch) rmSync("out", { recursive: true, force: true });

/** @type {import('esbuild').BuildOptions} */
const common = {
  bundle: true,
  sourcemap: !production,
  minify: production,
  logLevel: "info",
};

const host = {
  ...common,
  entryPoints: ["src/extension.ts"],
  outfile: "out/extension.js",
  platform: "node",
  format: "cjs",
  target: "node18",
  // `vscode` is provided by the host at runtime; never bundle it.
  external: ["vscode"],
};

const webview = {
  ...common,
  entryPoints: ["src/webview/main.tsx"],
  outfile: "out/webview/main.js",
  platform: "browser",
  format: "iife",
  target: "es2020",
  // React automatic runtime keeps the bundle self-contained (no JSX globals).
  jsx: "automatic",
  loader: { ".css": "text" },
};

async function run() {
  if (watch) {
    const [hc, wc] = await Promise.all([context(host), context(webview)]);
    await Promise.all([hc.watch(), wc.watch()]);
    console.log("esbuild: watching host + webview bundles…");
  } else {
    await Promise.all([build(host), build(webview)]);
    console.log("esbuild: built host + webview bundles.");
  }
}

run().catch((err) => {
  console.error(err);
  process.exit(1);
});
