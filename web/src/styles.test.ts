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
      stylesheet.matchAll(
        /@import\s+"\.\/styles\/(?<name>[^"]+)"(?: layer\(components\.[a-z]+\))?;/gu,
      ),
      (match) => match.groups?.name,
    );

    expect(imports).toEqual([
      "foundation.css",
      "ui-theme.css",
      "explorer.css",
      "wallet.css",
      "analytics.css",
      "forms.css",
      "responsive.css",
    ]);
    expect(readStylesheet("contracts/ContractArtifactPanel.tsx")).toContain(
      'import "@/styles/artifacts.css";',
    );
  });

  it("loads shared explorer rules before transaction and execution overrides", () => {
    const stylesheet = readStylesheet("styles/explorer.css");
    const imports = Array.from(
      stylesheet.matchAll(/@import\s+"\.\/explorer\/(?<name>[^"]+)";/gu),
      (match) => match.groups?.name,
    );

    expect(imports).toEqual([
      "shared.css",
      "home.css",
      "tables.css",
      "chain.css",
      "pending.css",
      "entities.css",
      "transaction.css",
      "calldata.css",
      "logs.css",
      "traces.css",
    ]);
  });

  it("centers full-width inline actions", () => {
    const stylesheet = readStylesheet("styles/wallet.css");
    const rule = stylesheet.match(/\.inline-button\.full\s*\{(?<body>[^}]*)\}/u);

    expect(rule?.groups?.body).toMatch(/\bwidth:\s*100%;/u);
    expect(rule?.groups?.body).toMatch(/\bjustify-content:\s*center;/u);
  });

  it("soft-wraps read-only raw calldata inside its textarea", () => {
    const stylesheet = readStylesheet("styles/explorer/calldata.css");
    const rawCalldataRule = stylesheet.match(
      /\.transaction-calldata-raw-value\s*\{(?<body>[^}]*)\}/u,
    );

    expect(rawCalldataRule?.groups?.body).toMatch(/\boverflow-wrap:\s*anywhere;/u);
    expect(rawCalldataRule?.groups?.body).toMatch(/\bwhite-space:\s*pre-wrap;/u);
    expect(rawCalldataRule?.groups?.body).toMatch(/\bword-break:\s*break-word;/u);
  });
});
