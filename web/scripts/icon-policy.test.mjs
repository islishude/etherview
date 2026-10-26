import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join, relative } from "node:path";
import test from "node:test";

const root = fileURLToPath(new URL("../src/", import.meta.url));
function sources(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? sources(path) : [path];
  });
}

test("UI icons use the shared Lucide boundary, without character or CSS image substitutes", () => {
  for (const path of sources(root)) {
    const name = relative(root, path);
    if (name.endsWith(".test.tsx") || !/\.(tsx|css)$/.test(name)) continue;
    const source = readFileSync(path, "utf8");
    assert.doesNotMatch(source, /[⎘✓↗↺⌄›◇＋×▦]/u, name);
    assert.doesNotMatch(source, /data:image\/svg\+xml/, name);
    assert.doesNotMatch(source, /\b(?:status-dot|pulse-dot|system-address-badge)\b/, name);
    if (name === "components/UIIcon.tsx") continue;
    // The only hand-authored SVG represents time-series data, not an icon.
    if (name !== "pages/ChartsPages.tsx") assert.doesNotMatch(source, /<svg\b/, name);
    const imported = source.match(/import\s*\{([^}]+)\}\s*from "lucide-react"/s);
    for (const identifier of (imported?.[1] ?? "").split(",")) {
      const component = identifier.trim();
      if (!component || component.startsWith("type ")) continue;
      assert.doesNotMatch(source, new RegExp(`<${component}\\b`), name);
    }
    assert.doesNotMatch(source, /<Icon\b/, name);
  }
});
