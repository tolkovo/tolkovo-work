// Shared ESLint flat config for TypeScript + React workspaces (docs/00 §7).
// Usage in a workspace eslint.config.js:
//   import { tolkovoConfig } from "@tolkovo/config/eslint";
//   export default tolkovoConfig({ tsconfigRootDir: import.meta.dirname });
import js from "@eslint/js";
import reactHooks from "eslint-plugin-react-hooks";
import tseslint from "typescript-eslint";

// Letters of the UI languages. A JSX text node with any of them is a hardcoded UI string.
const LETTERS = "[A-Za-zА-Яа-яЁё]";

// Color literals: hex, rgb/hsl/oklch/lab functions. Colors come only from tokens (docs/00 §7.3).
const COLOR_LITERAL = String.raw`(#[0-9a-fA-F]{3,8}\b|\b(rgba?|hsla?|oklch|oklab|lab|lch|hwb)\()`;

// Attributes that are shown to people or read by screen readers.
const TEXT_ATTRIBUTES = "^(aria-label|aria-description|title|placeholder|alt)$";

const colorRules = [
  {
    selector: `Literal[value=/${COLOR_LITERAL}/]`,
    message: "Color literals are forbidden; use design tokens (docs/00 §7.3).",
  },
  {
    selector: `TemplateElement[value.raw=/${COLOR_LITERAL}/]`,
    message: "Color literals are forbidden; use design tokens (docs/00 §7.3).",
  },
];

const jsxRules = [
  {
    selector: `JSXText[value=/${LETTERS}/]`,
    message: "UI strings go through t('namespace.key') (docs/00 §7.5).",
  },
  {
    selector: `JSXAttribute[name.name=/${TEXT_ATTRIBUTES}/] > Literal[value=/${LETTERS}/]`,
    message: "UI strings go through t('namespace.key') (docs/00 §7.5).",
  },
  {
    selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']",
    message: "dangerouslySetInnerHTML is forbidden (docs/00 §7.3).",
  },
];

/**
 * @param {{ tsconfigRootDir: string, project?: string[], ignores?: string[] }} options
 *   project — tsconfig files that together cover every linted file.
 */
export function tolkovoConfig({ tsconfigRootDir, project = ["./tsconfig.json"], ignores = [] }) {
  return tseslint.config(
    { ignores: ["dist/**", "coverage/**", ...ignores] },
    js.configs.recommended,
    ...tseslint.configs.recommendedTypeChecked,
    {
      languageOptions: {
        parserOptions: { project, tsconfigRootDir },
      },
      rules: {
        // TanStack Router control flow: `throw redirect(...)` / `throw notFound()`.
        "@typescript-eslint/only-throw-error": [
          "error",
          {
            allow: [
              { from: "package", package: "@tanstack/router-core", name: ["Redirect", "NotFoundError"] },
            ],
          },
        ],
        "@typescript-eslint/no-explicit-any": "error",
        "@typescript-eslint/ban-ts-comment": [
          "error",
          { "ts-ignore": true, "ts-expect-error": "allow-with-description" },
        ],
        "@typescript-eslint/consistent-type-imports": "error",
        "@typescript-eslint/no-unused-vars": [
          "error",
          { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
        ],
        "no-eval": "error",
        "no-implied-eval": "error",
      },
    },
    {
      files: ["**/*.{ts,tsx}"],
      ...reactHooks.configs.flat.recommended,
    },
    {
      files: ["**/*.ts"],
      ignores: ["**/*.test.ts", "**/*.gen.ts"],
      rules: { "no-restricted-syntax": ["error", ...colorRules] },
    },
    {
      files: ["**/*.tsx"],
      ignores: ["**/*.test.tsx", "**/test/**"],
      rules: { "no-restricted-syntax": ["error", ...jsxRules, ...colorRules] },
    },
    {
      files: ["**/*.{js,mjs}"],
      ...tseslint.configs.disableTypeChecked,
    },
  );
}
