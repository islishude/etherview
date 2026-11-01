import { afterEach, expect, it, vi } from "vitest";
import { readBlob } from "./consensus";

afterEach(() => vi.unstubAllGlobals());

const hash = `0x${"11".repeat(32)}`;

it("retains the exact block number and block-scoped index in the download filename", async () => {
  const fetch = vi.fn(async (request: RequestInfo | URL, options?: RequestInit) => {
    expect(request).toBe(`/api/v1/blocks/${hash}/blobs/42/content`);
    expect(options?.method).toBe("GET");
    const headers = new Headers(options?.headers);
    expect(headers.get("X-CSRF-Token")).toBe("csrf");
    expect(headers.has("X-Turnstile-Token")).toBe(false);
    return new Response(new Uint8Array(131072), {
      headers: { "Content-Disposition": "attachment; filename=blob-9007199254740993-42.bin" },
    });
  });
  vi.stubGlobal("fetch", fetch);
  const result = await readBlob(hash, "42", "csrf");
  expect(result.filename).toBe("blob-9007199254740993-42.bin");
  expect(result.data.byteLength).toBe(131072);
  expect(fetch).toHaveBeenCalledOnce();
});

it.each([
  "",
  "attachment; filename=blob-9007199254740993-0.bin",
  "attachment; filename=blob-01-42.bin",
  "attachment; filename=../../blob-1-42.bin",
])("rejects an invalid or mismatched download filename: %s", async (disposition) => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(new Uint8Array(131072), {
          headers: { "Content-Disposition": disposition },
        }),
    ),
  );
  await expect(readBlob(hash, "42", "csrf")).rejects.toThrow("blob_storage_unavailable");
});
