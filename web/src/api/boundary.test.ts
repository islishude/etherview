import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const sourceRoot = `${process.cwd()}/src`;

describe("browser backend boundary", () => {
  it("routes every production backend request through the generated OpenAPI client", () => {
    const violations: string[] = [];
    const walletMethods = new Set<string>();
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

      const syntax = inspectWalletSyntax(file, source);
      if (syntax.rawRequestCalls > 0 && relative !== "wallet/WalletProvider.tsx") {
        violations.push(`${relative}: raw provider request outside injected-provider boundary`);
      }
      if (syntax.requestProviderCalls.length > 0) {
        if (relative !== "wallet/WalletProvider.tsx") {
          violations.push(`${relative}: wallet RPC outside injected-provider boundary`);
        }
        for (const method of syntax.requestProviderCalls) {
          if (method === undefined) {
            violations.push(`${relative}: dynamic wallet RPC method`);
            continue;
          }
          walletMethods.add(method);
          if (!allowedWalletMethods.has(method)) {
            violations.push(`${relative}: unsupported wallet RPC ${method}`);
          }
        }
      }
      if (
        relative === "wallet/WalletProvider.tsx" &&
        syntax.dynamicMethodProperties > 0
      ) {
        violations.push(`${relative}: dynamic wallet RPC method`);
      }
      if (
        syntax.eip6963EventReferences > 0 &&
        relative !== "wallet/eip6963.ts" &&
        relative !== "wallet/WalletProvider.tsx"
      ) {
        violations.push(`${relative}: EIP-6963 discovery outside wallet boundary`);
      }
    }

    expect(violations).toEqual([]);
    expect([...walletMethods].sort()).toEqual([...allowedWalletMethods].sort());
  });

  it("keeps the OpenAPI paths type in the sole transport module", () => {
    const client = readFileSync(`${sourceRoot}/api/client.ts`, "utf8");
    const hooks = readFileSync(`${sourceRoot}/api/hooks.ts`, "utf8");

    expect(client).toContain('import type { paths } from "./schema.gen"');
    expect(client).toContain("createClient<paths>");
    expect(hooks).toContain('import { apiClient, requireEnvelope } from "./client"');
    expect(hooks).not.toMatch(/\bfetch\s*\(/u);
  });

  it("keeps raw providers private and allows only the fixed wallet RPC surface", () => {
    const file = `${sourceRoot}/wallet/WalletProvider.tsx`;
    const provider = readFileSync(file, "utf8");
    const interfaceNames = ["WalletOption", "ActiveWallet", "WalletContextValue"];
    const publicInterfaces = interfaceNames.map((name) => interfaceSource(provider, name));
    const publicSurface = publicInterfaces.join("\n");
    const syntax = inspectWalletSyntax(file, provider);

    expect(publicInterfaces.every((source) => source.length > 0)).toBe(true);
    expect(publicSurface).not.toMatch(
      /\b(?:EIP1193Provider|EIP6963ProviderDetail|provider|detail|request|removeListener)\b/u,
    );
    expect(syntax.rawRequestCalls).toBe(1);
    expect(
      [...new Set(syntax.requestProviderCalls.filter((method) => method !== undefined))].sort(),
    ).toEqual([...allowedWalletMethods].sort());
    expect(syntax.requestProviderCalls).not.toContain(undefined);
    expect(syntax.dynamicMethodProperties).toBe(0);
  });

  it("recognizes alternate wallet-call syntax instead of letting it bypass the gate", () => {
    expect(
      walletRequestMethods("requestActiveProvider(wallet, { method })"),
    ).toEqual([undefined]);
    expect(
      walletRequestMethods(
        'requestActiveProvider(wallet, { ["method"]: "eth_signTypedData_v4" })',
      ),
    ).toEqual([undefined]);
    expect(
      walletRequestMethods(
        "requestActiveProvider(wallet, { method: `personal_sign` })",
      ),
    ).toEqual(["personal_sign"]);
    expect(
      walletRequestMethods(
        'requestActiveProvider(wallet, { method: "eth_call", ...override })',
      ),
    ).toEqual([undefined]);
    expect(
      inspectWalletSyntax(
        `${sourceRoot}/outside.ts`,
        'window.addEventListener("eip6963:announceProvider", listener)',
      ).eip6963EventReferences,
    ).toBeGreaterThan(0);
  });
});

const allowedWalletMethods = new Set([
  "eth_accounts",
  "eth_call",
  "eth_chainId",
  "eth_requestAccounts",
  "eth_sendTransaction",
]);

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

function inspectWalletSyntax(
  file: string,
  source: string,
): {
  rawRequestCalls: number;
  requestProviderCalls: Array<string | undefined>;
  dynamicMethodProperties: number;
  eip6963EventReferences: number;
} {
  const rawRequestCalls = [
    ...source.matchAll(
      /(?:\.\s*request|\[\s*["'`]request["'`]\s*\])\s*\(/gu,
    ),
  ].length;
  const requestProviderCalls = file.endsWith("/wallet/WalletProvider.tsx")
    ? walletRequestMethods(source)
    : [
        ...source.matchAll(
          /\bmethod\s*:\s*(["'`])((?:eth|personal|wallet)_[a-zA-Z0-9_]+)\1/gu,
        ),
      ].map((match) => match[2]);
  const dynamicMethodProperties = requestProviderCalls.filter(
    (method) => method === undefined,
  ).length;
  const eip6963EventReferences = [
    ...source.matchAll(
      /\bEIP6963_(?:ANNOUNCE|REQUEST)_EVENT\b|["'`]eip6963:(?:announceProvider|requestProvider)["'`]/gu,
    ),
  ].length;

  return {
    rawRequestCalls,
    requestProviderCalls,
    dynamicMethodProperties,
    eip6963EventReferences,
  };
}

function walletRequestMethods(source: string): Array<string | undefined> {
  const methods: Array<string | undefined> = [];
  const calls = /\b(requestProvider|requestActiveProvider)\s*\(/gu;
  for (let match = calls.exec(source); match; match = calls.exec(source)) {
    const name = match[1];
    const prefix = source.slice(Math.max(0, match.index - 24), match.index);
    if (name === "requestProvider" && /function\s*$/u.test(prefix)) continue;

    const open = calls.lastIndex - 1;
    const parsed = callArguments(source, open);
    if (!parsed) {
      methods.push(undefined);
      continue;
    }
    calls.lastIndex = parsed.end + 1;
    const first = parsed.arguments[0]?.trim();
    const second = parsed.arguments[1]?.trim();
    if (
      name === "requestProvider" &&
      first === "session.detail.provider" &&
      second === "arguments_"
    ) {
      continue;
    }
    methods.push(requestObjectMethod(second));
  }
  return methods;
}

function callArguments(
  source: string,
  open: number,
): { arguments: string[]; end: number } | undefined {
  const arguments_: string[] = [];
  let start = open + 1;
  let parentheses = 1;
  let braces = 0;
  let brackets = 0;
  let quote: "'" | '"' | "`" | undefined;
  let escaped = false;
  let lineComment = false;
  let blockComment = false;

  for (let offset = open + 1; offset < source.length; offset += 1) {
    const character = source[offset]!;
    const next = source[offset + 1];
    if (lineComment) {
      if (character === "\n") lineComment = false;
      continue;
    }
    if (blockComment) {
      if (character === "*" && next === "/") {
        blockComment = false;
        offset += 1;
      }
      continue;
    }
    if (quote) {
      if (escaped) {
        escaped = false;
      } else if (character === "\\") {
        escaped = true;
      } else if (character === quote) {
        quote = undefined;
      }
      continue;
    }
    if (character === "/" && next === "/") {
      lineComment = true;
      offset += 1;
      continue;
    }
    if (character === "/" && next === "*") {
      blockComment = true;
      offset += 1;
      continue;
    }
    if (character === "'" || character === '"' || character === "`") {
      quote = character;
      continue;
    }
    if (character === "(") parentheses += 1;
    if (character === "{") braces += 1;
    if (character === "[") brackets += 1;
    if (character === "}") braces -= 1;
    if (character === "]") brackets -= 1;
    if (character === ")") {
      parentheses -= 1;
      if (parentheses === 0) {
        arguments_.push(source.slice(start, offset));
        return { arguments: arguments_, end: offset };
      }
    }
    if (
      character === "," &&
      parentheses === 1 &&
      braces === 0 &&
      brackets === 0
    ) {
      arguments_.push(source.slice(start, offset));
      start = offset + 1;
    }
  }
  return undefined;
}

function requestObjectMethod(source: string | undefined): string | undefined {
  if (!source || hasTopLevelSpread(source)) return undefined;
  const method = source.match(
    /^\{\s*method\s*:\s*(["'`])((?:eth|personal|wallet)_[a-zA-Z0-9_]+)\1(?:\s*[,}])/u,
  );
  if (!method?.[2]) return undefined;
  const methodNames = [...source.matchAll(/\bmethod\b|["'`]method["'`]/gu)];
  return methodNames.length === 1 ? method[2] : undefined;
}

function hasTopLevelSpread(source: string): boolean {
  let braces = 0;
  let brackets = 0;
  let parentheses = 0;
  let quote: "'" | '"' | "`" | undefined;
  let escaped = false;
  for (let offset = 0; offset < source.length; offset += 1) {
    const character = source[offset]!;
    if (quote) {
      if (escaped) {
        escaped = false;
      } else if (character === "\\") {
        escaped = true;
      } else if (character === quote) {
        quote = undefined;
      }
      continue;
    }
    if (character === "'" || character === '"' || character === "`") {
      quote = character;
      continue;
    }
    if (character === "{") braces += 1;
    if (character === "[") brackets += 1;
    if (character === "(") parentheses += 1;
    if (
      character === "." &&
      source.slice(offset, offset + 3) === "..." &&
      braces === 1 &&
      brackets === 0 &&
      parentheses === 0
    ) {
      return true;
    }
    if (character === "}") braces -= 1;
    if (character === "]") brackets -= 1;
    if (character === ")") parentheses -= 1;
  }
  return false;
}

function interfaceSource(source: string, name: string): string {
  const declaration = source.indexOf(`interface ${name}`);
  if (declaration < 0) return "";
  const open = source.indexOf("{", declaration);
  if (open < 0) return "";
  let depth = 0;
  for (let offset = open; offset < source.length; offset += 1) {
    if (source[offset] === "{") depth += 1;
    if (source[offset] === "}") {
      depth -= 1;
      if (depth === 0) return source.slice(declaration, offset + 1);
    }
  }
  return "";
}
