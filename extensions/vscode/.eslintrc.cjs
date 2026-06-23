// ESLint (flat-config is avoided to keep eslint v8 + typescript-eslint v8 simple).
// Enforces no-floating-promises so all async host work is awaited or `void`-ed,
// which is how we keep network off the UI thread without dangling rejections.
module.exports = {
  root: true,
  parser: "@typescript-eslint/parser",
  parserOptions: {
    project: ["./tsconfig.json", "./tsconfig.webview.json"],
    tsconfigRootDir: __dirname,
  },
  plugins: ["@typescript-eslint"],
  extends: [
    "eslint:recommended",
    "plugin:@typescript-eslint/recommended",
  ],
  env: { node: true, browser: true, es2020: true },
  rules: {
    "@typescript-eslint/no-floating-promises": "error",
    "@typescript-eslint/no-explicit-any": "off",
    "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_" }],
    "no-undef": "off"
  },
  ignorePatterns: ["out/**", "node_modules/**", "*.mjs", "*.cjs"],
};
