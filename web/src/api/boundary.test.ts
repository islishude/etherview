import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const sourceRoot = `${process.cwd()}/src`;

describe("browser backend boundary", () => {
  it("routes every production backend request through the generated OpenAPI client", () => {
    const violations: string[] = [];
    for (const file of productionSources(sourceRoot)) {
      const relative = file.slice(sourceRoot.length + 1);
      const source = readFileSync(file, "utf8");

      if (relative !== "api/client.ts" && /\b(?:globalThis\.)?fetch\s*\(/u.test(source)) {
        violations.push(`${relative}: direct fetch`);
      }
      if (relative !== "api/client.ts" && /["'`]\/api\/(?:v1|v2)\b/u.test(source)) {
        violations.push(`${relative}: literal backend path`);
      }
      if (
        /(?:VITE_|ETHERVIEW_|\bDATABASE_URL\b|\bRPC_URL\b|\bimport\.meta\.env\b|\bprocess\.env\b)/u.test(
          source,
        )
      ) {
        violations.push(`${relative}: server environment input`);
      }
      if (
        !relative.startsWith("wallet/") &&
        /["']eth_(?:call|sendTransaction)["']/u.test(source)
      ) {
        violations.push(`${relative}: wallet RPC outside injected-provider boundary`);
      }
    }

    expect(violations).toEqual([]);
  });

  it("keeps the OpenAPI paths type in the sole transport module", () => {
    const client = readFileSync(`${sourceRoot}/api/client.ts`, "utf8");
    const hooks = readFileSync(`${sourceRoot}/api/hooks.ts`, "utf8");

    expect(client).toContain('import type { paths } from "./schema.gen"');
    expect(client).toContain("createClient<paths>");
    expect(hooks).toContain('import { apiClient, requireEnvelope } from "./client"');
    expect(hooks).not.toMatch(/\bfetch\s*\(/u);
  });
});

function productionSources(directory: string): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = `${directory}/${entry.name}`;
    if (entry.isDirectory()) {
      files.push(...productionSources(path));
      continue;
    }
    if (
      /\.tsx?$/u.test(entry.name) &&
      !/\.test\.tsx?$/u.test(entry.name) &&
      entry.name !== "schema.gen.ts"
    ) {
      files.push(path);
    }
  }
  return files;
}
