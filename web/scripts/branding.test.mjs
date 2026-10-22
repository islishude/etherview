import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync, rmSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { build, createServer } from "vite";
import { JSDOM } from "jsdom";
import { brandingPlugin, loadBranding } from "./branding.ts";
import { prepareDistribution } from "./prepare-assets.mjs";
import { measureInitialAssets } from "./check-asset-budget.mjs";

const svg =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="12"/></svg>';
const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aWZkAAAAASUVORK5CYII=",
  "base64",
);
const index = readFileSync(new URL("../index.html", import.meta.url), "utf8").replace(
  "/src/main.tsx",
  "/main.js",
);

function fixture(t, config = {}) {
  const root = mkdtempSync(join(tmpdir(), "etherview branding "));
  t.after(() => rmSync(root, { force: true, recursive: true }));
  writeFileSync(join(root, "brand.json"), JSON.stringify(config));
  writeFileSync(join(root, "mark.svg"), svg);
  writeFileSync(join(root, "tab icon.png"), png);
  return root;
}

test("defaults and partial configurations share the existing icon", (t) => {
  const defaults = loadBranding("web/branding/default");
  assert.equal(defaults.brand.name, "Etherview");
  assert.equal(defaults.brand.title, "Etherview");
  assert.equal(defaults.brand.description, "Etherview execution-layer block explorer");
  assert.equal(defaults.brand.logo, defaults.brand.favicon);
  assert.equal(defaults.assets.length, 1);
  const partial = loadBranding(fixture(t, { name: "测试 Explorer", logo: "mark.svg" }));
  assert.equal(partial.brand.title, "测试 Explorer");
  assert.equal(partial.brand.description, "测试 Explorer execution-layer block explorer");
  assert.equal(partial.brand.favicon, defaults.brand.favicon);
  assert.notEqual(partial.brand.logo, partial.brand.favicon);
});

test("independent icons override the shared icon and content changes invalidate URLs", (t) => {
  const root = fixture(t, { icon: "mark.svg", favicon: "tab icon.png" });
  const first = loadBranding(root);
  assert.equal(first.brand.faviconType, "image/png");
  assert.match(first.brand.logo, /^\/assets\/brand-[a-f0-9]{8}\.svg$/u);
  assert.match(first.brand.favicon, /^\/assets\/brand-[a-f0-9]{8}\.png$/u);
  writeFileSync(join(root, "mark.svg"), svg.replace('r="12"', 'r="10"'));
  const second = loadBranding(root);
  assert.notEqual(first.brand.logo, second.brand.logo);
  assert.equal(first.brand.favicon, second.brand.favicon);
});

test("rejects malformed configuration, missing files and invalid images", (t) => {
  for (const config of [
    null,
    [],
    { extra: "ignored?" },
    { name: " " },
    { name: 4 },
    { title: "x\n" },
    { name: "x".repeat(81) },
    { icon: "missing.svg" },
    { icon: "https://example.com/a.svg" },
    { icon: "/tmp/a.svg" },
    { icon: "data:image/svg+xml,svg" },
  ]) {
    assert.throws(() => loadBranding(fixture(t, config)), /web-branding:/u);
  }
  const root = fixture(t, { icon: "broken.jpg" });
  writeFileSync(join(root, "broken.jpg"), "image");
  assert.throws(() => loadBranding(root), /SVG or PNG/u);
  writeFileSync(
    join(root, "brand.json"),
    JSON.stringify({ icon: "mark.svg", logo: "tab icon.png", favicon: "tab icon.png" }),
  );
  writeFileSync(join(root, "mark.svg"), "<html>invalid image</html>");
  assert.throws(() => loadBranding(root), /contents do not match/u);
  writeFileSync(join(root, "brand.json"), "{");
  assert.throws(() => loadBranding(root), /valid JSON/u);
  rmSync(join(root, "brand.json"));
  assert.throws(() => loadBranding(root), /cannot read brand.json/u);
  assert.throws(() => loadBranding(join(root, "missing")), /directory does not exist/u);
});

test("rejects traversal and symbolic links escaping the branding directory", (t) => {
  const outside = fixture(t);
  const root = fixture(t, { icon: "link.svg" });
  symlinkSync(join(outside, "mark.svg"), join(root, "link.svg"));
  assert.throws(() => loadBranding(root), /stay inside/u);
  writeFileSync(
    join(root, "brand.json"),
    JSON.stringify({ icon: `../${outside.split("/").at(-1)}/mark.svg` }),
  );
  assert.throws(() => loadBranding(root), /stay inside/u);
});

test("bounds input sizes and accepts a self-closing SVG", (t) => {
  const root = fixture(t, { icon: "mark.svg" });
  writeFileSync(join(root, "mark.svg"), '<svg xmlns="http://www.w3.org/2000/svg"/>');
  assert.equal(loadBranding(root).assets.length, 1);
  writeFileSync(join(root, "mark.svg"), "x".repeat(1024 * 1024 + 1));
  assert.throws(() => loadBranding(root), /at most 1048576/u);
  writeFileSync(join(root, "brand.json"), " ".repeat(16 * 1024 + 1));
  assert.throws(() => loadBranding(root), /at most 16384/u);
});

test("production HTML and scripts contain only public branding, with escaped text and manifested images", async (t) => {
  const name = '测试 <Explorer> & "friends"';
  const title = '</title><script>alert("x")</script>';
  const root = fixture(t, {
    name,
    title,
    description: '"/><script>bad</script> & description',
    icon: "mark.svg",
    favicon: "tab icon.png",
  });
  writeFileSync(join(root, "index.html"), index);
  writeFileSync(
    join(root, "main.js"),
    'import { brand } from "virtual:etherview-branding"; document.querySelector("#root").textContent = JSON.stringify(brand);',
  );
  const dist = join(root, "dist");
  await build({
    configFile: false,
    root,
    plugins: [brandingPlugin(root)],
    logLevel: "silent",
    build: { outDir: dist },
  });
  const html = readFileSync(join(dist, "index.html"), "utf8");
  const document = new JSDOM(html).window.document;
  assert.equal(document.title, title);
  assert.equal(
    document.querySelector('meta[name="description"]').content,
    '\"/><script>bad</script> & description',
  );
  assert.equal(document.querySelectorAll("script").length, 1);
  const favicon = document.querySelector('link[rel="icon"]');
  assert.equal(favicon.type, "image/png");
  const { assets, brand } = loadBranding(root);
  assert.equal(favicon.getAttribute("href"), brand.favicon);
  assert.equal(
    document.querySelector('link[rel="preload"][as="image"]').getAttribute("href"),
    brand.logo,
  );
  const manifest = prepareDistribution(dist);
  for (const asset of assets) {
    assert.deepEqual(readFileSync(join(dist, asset.fileName)), asset.bytes);
    assert.equal(manifest.assets[asset.fileName].identity.bytes, asset.bytes.length);
  }
  // Run the production budget gate against the PNG/SVG combination as well.
  const budget = join(root, "budget.json");
  writeFileSync(
    budget,
    JSON.stringify({
      schema: "etherview-web-asset-budget-v1",
      maximumInitialRawBytes: 1000000,
      maximumInitialGzipBytes: 310000,
      forbiddenInitialPatterns: [],
    }),
  );
  const measured = measureInitialAssets(dist, budget);
  for (const asset of assets) assert.ok(measured.files.includes(asset.fileName));
  const script = readFileSync(
    join(dist, document.querySelector("script").getAttribute("src")),
    "utf8",
  );
  assert.ok(script.includes(brand.logo));
  assert.ok(!script.includes(root));
  assert.ok(!html.includes("__BRAND_"));
});

test("development serves the same branded HTML and images", async (t) => {
  const root = fixture(t, { name: "开发 Explorer", icon: "tab icon.png" });
  writeFileSync(join(root, "index.html"), index);
  writeFileSync(
    join(root, "main.js"),
    'import { brand } from "virtual:etherview-branding"; console.log(brand.name);',
  );
  const server = await createServer({
    configFile: false,
    root,
    plugins: [brandingPlugin(root)],
    logLevel: "silent",
    server: { port: 0, host: "127.0.0.1" },
  });
  t.after(() => server.close());
  await server.listen();
  const base = `http://127.0.0.1:${server.httpServer.address().port}`;
  const document = new JSDOM(await (await fetch(base)).text()).window.document;
  assert.equal(document.title, "开发 Explorer");
  const icon = await fetch(
    new URL(document.querySelector('link[rel="icon"]').getAttribute("href"), base),
  );
  assert.equal(icon.headers.get("content-type"), "image/png");
  assert.deepEqual(Buffer.from(await icon.arrayBuffer()), png);
  const head = await fetch(new URL(loadBranding(root).brand.logo, base), { method: "HEAD" });
  assert.equal(head.status, 200);
  assert.equal(await head.text(), "");
});
