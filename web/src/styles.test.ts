import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

describe("shared button layout", () => {
  it("centers full-width inline actions", () => {
    const stylesheet = readFileSync(
      join(process.cwd(), "src/styles.css"),
      "utf8",
    );
    const rule = stylesheet.match(/\.inline-button\.full\s*\{(?<body>[^}]*)\}/u);

    expect(rule?.groups?.body).toMatch(/\bwidth:\s*100%;/u);
    expect(rule?.groups?.body).toMatch(/\bjustify-content:\s*center;/u);
  });

  it("keeps inactive tabs transparent and active tabs branded", () => {
    const stylesheet = readFileSync(
      join(process.cwd(), "src/styles.css"),
      "utf8",
    );
    const inactiveRule = stylesheet.match(/\.transaction-tab\s*\{(?<body>[^}]*)\}/u);
    const activeRule = stylesheet.match(/\.transaction-tab\.active\s*\{(?<body>[^}]*)\}/u);

    expect(inactiveRule?.groups?.body).toMatch(/\bbackground:\s*transparent;/u);
    expect(activeRule?.groups?.body).toMatch(/\bbackground:\s*var\(--brand\);/u);
  });

  it("normalizes address tab entries before applying the active state", () => {
    const stylesheet = readFileSync(
      join(process.cwd(), "src/styles.css"),
      "utf8",
    );
    const addressRule = stylesheet.match(/\.transaction-tabs\s*>\s*\.transaction-tab\s*\{(?<body>[^}]*)\}/u);
    const addressActiveRule = stylesheet.match(/\.transaction-tabs\s*>\s*\.transaction-tab\.active\s*\{(?<body>[^}]*)\}/u);

    expect(addressRule?.groups?.body).toMatch(/\bmargin-inline-start:\s*0;/u);
    expect(addressRule?.groups?.body).toMatch(/\bborder:\s*0;/u);
    expect(addressRule?.groups?.body).toMatch(/\bbackground:\s*transparent;/u);
    expect(addressActiveRule?.groups?.body).toMatch(/\bbackground:\s*var\(--brand\);/u);
  });
});
