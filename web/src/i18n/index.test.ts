import { describe, expect, it, vi } from "vitest";

import i18n from "./index";

describe("bilingual preference", () => {
  it("applies a stored language to the document during first initialization", async () => {
    window.localStorage.setItem("etherview.language", "zh");
    document.documentElement.lang = "en";
    vi.resetModules();

    const fresh = await import("./index");

    expect(fresh.default.resolvedLanguage).toBe("zh");
    expect(document.documentElement.lang).toBe("zh-CN");
  });

  it("updates the document language and persists both supported locales", async () => {
    await i18n.changeLanguage("zh");
    expect(document.documentElement.lang).toBe("zh-CN");
    expect(window.localStorage.getItem("etherview.language")).toBe("zh");
    expect(i18n.t("nav.blocks")).toBe("区块");

    await i18n.changeLanguage("en");
    expect(document.documentElement.lang).toBe("en");
    expect(window.localStorage.getItem("etherview.language")).toBe("en");
    expect(i18n.t("nav.blocks")).toBe("Blocks");
  });
});
