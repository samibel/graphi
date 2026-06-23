import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// graphi web client build config. The client dials the SW-039 HTTP/SSE surface
// at VITE_GRAPHI_URL (default http://127.0.0.1:8080 — loopback only).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    // The dev server proxies API/SSE calls to the local graphi http daemon so
    // the browser only ever talks loopback (zero-outbound client contract).
    proxy: {
      "/query": "http://127.0.0.1:8080",
      "/analyze": "http://127.0.0.1:8080",
      "/events": { target: "http://127.0.0.1:8080", changeOrigin: false, ws: false },
      "/wiki": "http://127.0.0.1:8080",
    },
  },
  test: {
    globals: true,
    environment: "node",
  },
});
