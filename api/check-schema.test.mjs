import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

const checker = resolve(import.meta.dirname, "check-schema.mjs");
const source = readFileSync(resolve(import.meta.dirname, "openapi.yaml"), "utf8");
const overview = "      operationId: getChartOverview\n      tags: [stats]\n";

function fixture(t, document) {
  const directory = mkdtempSync(join(tmpdir(), "etherview-openapi-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const path = join(directory, "openapi.yaml");
  writeFileSync(path, document);
  return path;
}

function check(t, document) {
  const path = fixture(t, document);
  const result = spawnSync(process.execPath, [checker, path], { encoding: "utf8" });
  assert.ifError(result.error);
  assert.equal(result.signal, null);
  return result;
}

for (const parameters of ["", "      parameters: []\n"]) {
  test(`accepts ${parameters ? "empty" : "omitted"} parameter arrays`, (t) => {
    assert.equal(source.split(overview).length, 2);
    const result = check(t, source.replace(overview, overview + parameters));
    assert.equal(result.status, 0, result.stderr);
  });
}

for (const parameters of ["      parameters:\n", "      parameters: {}\n"]) {
  test(`rejects invalid parameter arrays: ${parameters.trim()}`, (t) => {
    assert.equal(source.split(overview).length, 2);
    const result = check(t, source.replace(overview, overview + parameters));
    assert.equal(result.status, 1, result.stdout + result.stderr);
    assert.match(result.stderr, /parameters.*must be array/);
    assert.match(result.stderr, /stats~1charts~1overview/);
  });
}

const minimal = `openapi: 3.0.3
info:
  title: Schema regression
  version: "1.0"
paths:
  /example:
    get:
      responses:
        "200":
          description: Success
          content:
            application/json:
              schema:
                type: string
`;

for (const [name, document, diagnostic] of [
  ["invalid schema type", minimal.replace("type: string", "type: invalid"), /type/],
  [
    "missing required responses",
    minimal.slice(0, minimal.indexOf("      responses:")) + "      summary: Example\n",
    /responses/,
  ],
  [
    "unresolved reference",
    minimal.replace("type: string", '$ref: "#/components/schemas/Missing"'),
    /components|Missing/,
  ],
]) {
  test(`rejects ${name}`, (t) => {
    const result = check(t, document);
    assert.equal(result.status, 1, result.stdout + result.stderr);
    assert.match(result.stderr, diagnostic);
  });
}

test("rejects remote references before DNS or HTTP access", (t) => {
  const reference = "https://example.invalid/schema.json";
  const path = fixture(t, minimal.replace("type: string", `$ref: "${reference}"`));
  const marker = join(dirname(path), "network-access");
  const preload = join(dirname(path), "reject-network.mjs");
  writeFileSync(
    preload,
    `import dns from "node:dns";
import http from "node:http";
import https from "node:https";
import { writeFileSync } from "node:fs";
function rejectNetwork() {
  writeFileSync(${JSON.stringify(marker)}, "attempted");
  throw new Error("Unexpected network access during schema validation");
}
dns.lookup = dns.promises.lookup = http.request = https.request = globalThis.fetch = rejectNetwork;
`,
  );
  const result = spawnSync(process.execPath, ["--import", preload, checker, path], {
    encoding: "utf8",
  });
  assert.ifError(result.error);
  assert.equal(result.status, 1, result.stdout + result.stderr);
  assert.match(result.stderr, /Unable to resolve \$ref pointer/);
  assert.equal(existsSync(marker), false, "schema validation must not attempt network access");
});
