// Build/reference utilities. Never imported by the production SEA.
import { createHash } from "node:crypto";
import { readFileSync, lstatSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
export const HERE = fileURLToPath(new URL(".", import.meta.url));
export const ROOT = resolve(HERE, "../../..");
export const MAX_ARCHIVE = 200 << 20;
export const digest = (data) => createHash("sha256").update(data).digest("hex");
export const readJSON = (path) => JSON.parse(readFileSync(path, "utf8"));
export const versions = () => readJSON(new URL("../versions/index.json", import.meta.url)).versions;
// Python's ensure_ascii, sorted-key compact representation is the existing
// authenticated manifest/advisory format. Do not silently change its encoding.
export function canonical(value) {
  if (Array.isArray(value)) return "[" + value.map(canonical).join(",") + "]";
  if (value !== null && typeof value === "object") {
    return (
      "{" +
      Object.keys(value)
        .sort()
        .map((k) => canonical(k) + ":" + canonical(value[k]))
        .join(",") +
      "}"
    );
  }
  return JSON.stringify(value).replace(
    /[\u007f-\uffff]/g,
    (c) => "\\u" + c.charCodeAt(0).toString(16).padStart(4, "0"),
  );
}
export function regularFile(path, limit = MAX_ARCHIVE) {
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.size > limit) throw new Error("invalid or oversized file");
  return readFileSync(path);
}
export function safeMember(name) {
  if (
    !name ||
    name.includes("\\") ||
    name.includes("\0") ||
    name.split("/").some((p) => ["", ".", ".."].includes(p))
  ) {
    throw new Error("unsafe package member");
  }
  return name.split("/");
}
export async function readURL(url, limit) {
  if (!url.startsWith("https://")) throw new Error("HTTPS required");
  for (let attempt = 0; ; attempt++) {
    try {
      const response = await fetch(url, {
        signal: AbortSignal.timeout(60000),
        headers: { "User-Agent": "etherview-wasm-packages" },
      });
      if (!response.ok) {
        await response.body?.cancel();
        throw new Error("upstream request failed");
      }
      const chunks = [];
      let size = 0;
      for await (const chunk of response.body) {
        size += chunk.length;
        if (size > limit) throw new Error("download exceeds limit");
        chunks.push(chunk);
      }
      return Buffer.concat(chunks);
    } catch (error) {
      if (attempt === 2 || error.message === "download exceeds limit") throw error;
      await delay((attempt + 1) * 1000);
    }
  }
}
export async function mapConcurrent(values, callback, workers = 8) {
  const result = new Array(values.length);
  let index = 0;
  await Promise.all(
    Array.from({ length: Math.min(workers, values.length) }, async () => {
      while (index < values.length) {
        const i = index++;
        result[i] = await callback(values[i]);
      }
    }),
  );
  return result;
}
