import { defineConfig } from "vitest/config";
import * as path from "path";

// Unit tests run in Node. The `vscode` module is only available inside the real
// extension host, so it is aliased to a minimal stub for the unit-test surface
// (path/URI resolution). Files that need the full host API are covered by the
// integration tests, not these vitest unit tests.
export default defineConfig({
  resolve: {
    alias: {
      vscode: path.resolve(__dirname, "src/test/vscode-stub.ts"),
    },
  },
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
