import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

function readStylesheet(name: string) {
  return readFileSync(join(process.cwd(), "src", name), "utf8");
}

describe("shared button layout", () => {
  it("loads style modules in documented cascade order", () => {
    const stylesheet = readStylesheet("styles.css");
    const imports = Array.from(
      stylesheet.matchAll(/@import\s+"\.\/styles\/(?<name>[^"]+)";/gu),
      (match) => match.groups?.name,
    );

    expect(imports).toEqual([
      "foundation.css",
      "explorer.css",
      "wallet.css",
      "account.css",
      "analytics.css",
      "verification.css",
      "artifacts.css",
      "responsive.css",
    ]);
  });

  it("centers full-width inline actions", () => {
    const stylesheet = readStylesheet("styles/wallet.css");
    const rule = stylesheet.match(/\.inline-button\.full\s*\{(?<body>[^}]*)\}/u);

    expect(rule?.groups?.body).toMatch(/\bwidth:\s*100%;/u);
    expect(rule?.groups?.body).toMatch(/\bjustify-content:\s*center;/u);
  });

  it("keeps inactive tabs transparent and active tabs branded", () => {
    const stylesheet = readStylesheet("styles/explorer.css");
    const inactiveRule = stylesheet.match(/\.transaction-tab\s*\{(?<body>[^}]*)\}/u);
    const activeRule = stylesheet.match(/\.transaction-tab\.active\s*\{(?<body>[^}]*)\}/u);

    expect(inactiveRule?.groups?.body).toMatch(/\bbackground:\s*transparent;/u);
    expect(activeRule?.groups?.body).toMatch(/\bbackground:\s*var\(--brand\);/u);
  });

  it("normalizes address tab entries before applying the active state", () => {
    const stylesheet = readStylesheet("styles/explorer.css");
    const addressRule = stylesheet.match(/\.transaction-tabs\s*>\s*\.transaction-tab\s*\{(?<body>[^}]*)\}/u);
    const addressActiveRule = stylesheet.match(/\.transaction-tabs\s*>\s*\.transaction-tab\.active\s*\{(?<body>[^}]*)\}/u);

    expect(addressRule?.groups?.body).toMatch(/\bmargin-inline-start:\s*0;/u);
    expect(addressRule?.groups?.body).toMatch(/\bborder:\s*0;/u);
    expect(addressRule?.groups?.body).toMatch(/\bbackground:\s*transparent;/u);
    expect(addressActiveRule?.groups?.body).toMatch(/\bbackground:\s*var\(--brand\);/u);
  });

  it("soft-wraps read-only raw calldata inside its textarea", () => {
    const stylesheet = readStylesheet("styles/explorer.css");
    const rawCalldataRule = stylesheet.match(/\.transaction-calldata-raw-value\s*\{(?<body>[^}]*)\}/u);

    expect(rawCalldataRule?.groups?.body).toMatch(/\boverflow-wrap:\s*anywhere;/u);
    expect(rawCalldataRule?.groups?.body).toMatch(/\bwhite-space:\s*pre-wrap;/u);
    expect(rawCalldataRule?.groups?.body).toMatch(/\bword-break:\s*break-word;/u);
  });

  it("stacks address transaction status badges consistently", () => {
    const stylesheet = readStylesheet("styles/explorer.css");
    const statusRule = stylesheet.match(
      /\.address-activity-table\s+\.transaction-status-group\s*\{(?<body>[^}]*)\}/u,
    );

    expect(statusRule?.groups?.body).toMatch(/\bflex-direction:\s*column;/u);
    expect(statusRule?.groups?.body).toMatch(/\bflex-wrap:\s*nowrap;/u);
    expect(statusRule?.groups?.body).toMatch(/\balign-items:\s*flex-start;/u);
  });
});
