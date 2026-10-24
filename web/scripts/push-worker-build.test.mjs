import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import vm from "node:vm";
import { build, createServer } from "vite";
import { prepareDistribution } from "./prepare-assets.mjs";
import { measureInitialAssets } from "./check-asset-budget.mjs";

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), "etherview-push-worker-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(join(root, "src/workers"), { recursive: true });
  writeFileSync(
    join(root, "src/workers/push-sw.ts"),
    'import { value } from "../shared"; self.value = value satisfies string;',
  );
  writeFileSync(join(root, "src/shared.ts"), 'export const value: string = "worker-shared-value";');
  writeFileSync(join(root, "index.html"), '<script type="module" src="/src/main.ts"></script>');
  writeFileSync(
    join(root, "src/main.ts"),
    'import workerURL from "./workers/push-sw.ts?worker&url"; document.title = workerURL;',
  );
  return {
    root,
    configFile: fileURLToPath(new URL("../vite.config.ts", import.meta.url)),
    logLevel: "silent",
  };
}

test("native worker import bundles TypeScript separately and includes it in the manifest", async (t) => {
  const config = fixture(t);
  await build(config);
  const dist = join(config.root, "dist");
  const manifest = prepareDistribution(dist);
  const worker = readFileSync(join(dist, "push-sw.js"), "utf8");
  const self = {};
  vm.runInNewContext(worker, { self });
  assert.equal(self.value, "worker-shared-value");
  assert.equal(manifest.assets["push-sw.js"].identity.bytes, Buffer.byteLength(worker));
  const budget = fileURLToPath(new URL("../asset-budget.json", import.meta.url));
  const { files } = measureInitialAssets(dist, budget);
  assert.equal(files.includes("push-sw.js"), false);
  const pageCode = files
    .filter((file) => file.endsWith(".js"))
    .map((file) => readFileSync(join(dist, file), "utf8"))
    .join("\n");
  assert.match(pageCode, /\/push-sw\.js/u);
  assert.equal(pageCode.includes("worker-shared-value"), false);
  writeFileSync(join(config.root, "src/workers/push-sw.ts"), "const invalid: string = ;");
  await assert.rejects(build(config));
});

test("native dev worker URL serves a module with permission for root scope", async (t) => {
  const server = await createServer({
    ...fixture(t),
    server: { host: "127.0.0.1", port: 0, watch: null },
  });
  t.after(() => server.close());
  await server.listen();
  const origin = server.resolvedUrls.local[0];
  const wrapper = await fetch(new URL("src/workers/push-sw.ts?worker&url", origin));
  const code = await wrapper.text();
  const url = JSON.parse(code.match(/export default ("[^"]+")/u)[1]);
  assert.match(url, /worker_file&type=module/u);
  const response = await fetch(new URL(url, origin));
  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type"), /javascript/u);
  assert.equal(response.headers.get("service-worker-allowed"), "/");
  const worker = await response.text();
  assert.equal(worker.includes("satisfies string"), false);
  assert.match(worker, /import.*shared/u);
});
