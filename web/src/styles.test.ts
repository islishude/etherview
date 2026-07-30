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
});
