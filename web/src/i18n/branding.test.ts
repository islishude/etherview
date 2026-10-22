import { describe, expect, it, vi } from "vitest";

vi.mock("virtual:etherview-branding", () => ({ brand: { name: "测试 <Explorer> & Co" } }));

import i18n from "./index";

describe("build-time brand interpolation", () => {
  it("uses the configured name in both languages without replacing chain labels", async () => {
    for (const language of ["en", "zh"]) {
      await i18n.changeLanguage(language);
      expect(i18n.t("app.home")).toContain("测试 <Explorer> & Co");
      const resources = i18n.getResourceBundle(language, "translation");
      const rendered: string[] = [];
      function visit(value: Record<string, unknown>, prefix = "") {
        for (const [key, entry] of Object.entries(value)) {
          const path = prefix ? `${prefix}.${key}` : key;
          if (typeof entry === "string" && entry.includes("{{siteName}}")) {
            rendered.push(i18n.t(path));
          } else if (entry && typeof entry === "object") {
            visit(entry as Record<string, unknown>, path);
          }
        }
      }
      visit(resources);
      expect(rendered.length).toBeGreaterThan(5);
      for (const text of rendered) {
        expect(text).toContain("测试 <Explorer> & Co");
        expect(text).not.toContain("Etherview");
        expect(text).not.toContain("{{siteName}}");
      }
    }
    await i18n.changeLanguage("en");
  });
});
