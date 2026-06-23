// Flat ESLint config for the graphi web client (SW-045).
// Enforces typescript-eslint recommended + react-hooks rules, and two
// security rules from the refinement: ban dangerouslySetInnerHTML (S3) and
// reject hard-coded non-loopback fetch/EventSource targets (S1).
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";

export default tseslint.config(
  {
    ignores: ["dist/**", "node_modules/**", "src/contract.gen.ts"],
  },
  ...tseslint.configs.recommended,
  {
    files: ["src/**/*.{ts,tsx}"],
    plugins: { "react-hooks": reactHooks },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // S3: forbid raw HTML injection of (untrusted) API-derived strings.
      "no-restricted-syntax": [
        "error",
        {
          selector:
            "JSXAttribute[name.name='dangerouslySetInnerHTML']",
          message:
            "dangerouslySetInnerHTML is banned (S3): API-derived strings must render as escaped text.",
        },
        {
          // S1: no hard-coded non-loopback network targets in fetch/EventSource.
          selector:
            "Literal[value=/^https?:\\/\\/(?!127\\.0\\.0\\.1|localhost|\\[::1\\])/]",
          message:
            "Non-loopback URL literal is banned (S1): the client is loopback-only / zero-outbound.",
        },
      ],
    },
  },
  {
    files: ["**/*.test.ts", "**/*.test.tsx"],
    rules: {
      "@typescript-eslint/no-explicit-any": "off",
      "@typescript-eslint/no-non-null-assertion": "off",
      // Tests intentionally pass non-loopback URLs as STRING ARGUMENTS to assert
      // the loopback guard rejects them — they are not real network targets.
      "no-restricted-syntax": "off",
    },
  },
);
