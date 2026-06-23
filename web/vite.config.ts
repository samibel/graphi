/// <reference types="vitest" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// graphi web client build config (SW-045). The client dials the SW-044 HTTP/SSE
// surface at VITE_GRAPHI_URL (default loopback). The dev server proxies API/SSE
// calls to the local graphi daemon so the browser only ever talks loopback
// (zero-outbound client contract, S1).
const DAEMON = "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/contract": DAEMON,
      "/query": DAEMON,
      "/search": DAEMON,
      "/analyze": DAEMON,
      "/healthz": DAEMON,
      "/events": { target: DAEMON, changeOrigin: false, ws: false },
      // The wiki data fetch (wikiClient.ts) and the SPA routes share the /wiki*
      // path namespace (the router matches the Engine's path-style links 1:1).
      // `bypass` lets the dev server own browser HISTORY: a document navigation
      // (Accept: text/html — direct load / deep-link of /wiki/c/:id) is served
      // the SPA shell, while the client's DATA request (Accept: text/markdown)
      // is proxied to the daemon. This is the SPA history fallback for /wiki*.
      "/wiki": {
        target: DAEMON,
        bypass(req) {
          const accept = req.headers.accept ?? "";
          if (accept.includes("text/html")) return "/index.html";
          return undefined; // proxy the data request to the daemon
        },
      },
    },
  },
  // Non-/wiki deep links (none today, but future-proof) fall back to the SPA.
  appType: "spa",
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./src/test-setup.ts"],
  },
});
