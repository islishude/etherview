import * as sdk from "./heyapi/sdk.gen";
import { describe, expect, it, vi } from "vitest";

import {
  ApiError,
  createExplorerClient,
  requireEnvelope,
  requireNoContent,
  requireResponse,
} from "./client";

const hash = `0x${"12".repeat(32)}`;
const address = `0x${"34".repeat(20)}`;

describe("generated OpenAPI client boundary", () => {
  it("keeps large integers as strings and sends a same-origin request", async () => {
    const quantity =
      "115792089237316195423570985008687907853269984665640564039457584007913129639935";
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      Response.json({
        data: [
          {
            hash,
            number: quantity,
            parent_hash: hash,
            timestamp: "2026-01-01T00:00:00Z",
            transaction_count: 0,
            gas_used: "0",
            canonical: true,
            finality: "latest",
            completeness: {
              core: "complete",
              trace: "unavailable",
              metadata: "unavailable",
              state: "unavailable",
            },
          },
        ],
        meta: { request_id: "request-1", chain_id: "1", next_cursor: "opaque" },
      }),
    );
    const client = createExplorerClient(fetcher);

    const result = requireEnvelope(await sdk.listBlocks({ client: client, query: { limit: 25 } }));

    expect(result.data[0]?.number).toBe(quantity);
    expect(fetcher).toHaveBeenCalledOnce();
    expect(fetcher.mock.calls[0]?.[0]).toBe("/api/v1/blocks?limit=25");
    expect(fetcher.mock.calls[0]?.[1]).toMatchObject({
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
    });
  });

  it("returns the structured API failure without exposing the response body", async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      Response.json(
        {
          error: {
            code: "TRACE_UNAVAILABLE",
            message: "Trace capability is unavailable",
            request_id: "request-1",
          },
        },
        { status: 503 },
      ),
    );
    const client = createExplorerClient(fetcher);

    const result = await sdk.getTransactionTrace({ client: client, path: { hash } });
    expect(() => requireEnvelope(result)).toThrowError(
      expect.objectContaining<Partial<ApiError>>({
        status: 503,
        code: "TRACE_UNAVAILABLE",
        requestId: "request-1",
      }),
    );
  });

  it("posts JSON with an API key header without putting the credential in the URL", async () => {
    const secret = "ev_live_this-must-stay-in-a-header";
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      Response.json({
        data: {
          id: "018f3b52-0b3d-7bf1-b65f-6f214827cb41",
          status: "queued",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        },
        meta: { request_id: "request-2", chain_id: "1" },
      }),
    );
    const client = createExplorerClient(fetcher);
    const body = {
      compiler_version: "0.8.30",
      input_kind: "standard_json" as const,
      language: "solidity" as const,
      input: { language: "Solidity" },
    };

    requireEnvelope(
      await sdk.submitAddressVerification({
        client: client,
        body,
        headers: { "X-API-Key": secret },
        path: { address },
      }),
    );

    const [url, request] = fetcher.mock.calls[0] ?? [];
    expect(url).toBe(`/api/v1/contracts/${address}/verification`);
    expect(String(url)).not.toContain(secret);
    expect(request).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      body: JSON.stringify(body),
    });
    const headers = new Headers(request?.headers);
    expect(headers.get("Content-Type")).toBe("application/json");
    expect(headers.get("X-API-Key")).toBe(secret);
  });

  it("rejects paths that could escape the fixed API prefix", async () => {
    const fetcher = vi.fn<typeof fetch>();
    const client = createExplorerClient(fetcher);

    await expect(client.get({ url: "../metrics" })).rejects.toThrow("/api/v1 boundary");
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("rejects a success body that is not the documented data/meta envelope", async () => {
    const client = createExplorerClient(
      vi.fn<typeof fetch>().mockResolvedValue(Response.json({ data: [] })),
    );

    const result = await sdk.listBlocks({ client: client });
    expect(() => requireEnvelope(result)).toThrowError(
      expect.objectContaining<Partial<ApiError>>({ code: "INVALID_RESPONSE" }),
    );
  });

  it("rejects a success envelope missing required native metadata", async () => {
    const client = createExplorerClient(
      vi.fn<typeof fetch>().mockResolvedValue(Response.json({ data: [], meta: {} })),
    );

    const result = await sdk.listBlocks({ client: client });
    expect(() => requireEnvelope(result)).toThrowError(
      expect.objectContaining<Partial<ApiError>>({ code: "INVALID_RESPONSE" }),
    );
  });

  it("does not trust an error body missing its required request ID", async () => {
    const client = createExplorerClient(
      vi
        .fn<typeof fetch>()
        .mockResolvedValue(
          Response.json({ error: { code: "LEAKED_CODE", message: "untrusted" } }, { status: 503 }),
        ),
    );

    const result = await sdk.listBlocks({ client: client });
    expect(() => requireEnvelope(result)).toThrowError(
      expect.objectContaining<Partial<ApiError>>({
        code: "HTTP_ERROR",
        requestId: undefined,
      }),
    );
  });
});

describe("Hey API compatibility", () => {
  it.each([
    { url: "/status", baseUrl: "https://outside.example/api/v1" },
    { url: "/status", baseUrl: `${window.location.origin}/metrics` },
    { url: "/../../metrics" },
    { url: "/%2e%2e/%2e%2e/metrics" },
  ])("rejects boundary escapes: %j", async (options) => {
    const fetcher = vi.fn<typeof fetch>();
    await expect(createExplorerClient(fetcher).get(options)).rejects.toThrow("/api/v1 boundary");
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("encodes opaque query values and omits undefined values", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(Response.json({ data: [], meta: { request_id: "r", chain_id: "1" } }));
    await sdk.listBlocks({
      client: createExplorerClient(fetcher),
      query: { cursor: "a+b/&?=", limit: undefined },
    });
    expect(fetcher.mock.calls[0]?.[0]).toBe("/api/v1/blocks?cursor=a%2Bb%2F%26%3F%3D");
  });

  it("handles 204 and preserves CSRF headers", async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status: 204 }));
    const result = await sdk.logoutAuthSession({
      client: createExplorerClient(fetcher),
      headers: { "X-CSRF-Token": "csrf" },
    });
    expect(() => requireNoContent(result)).not.toThrow();
    expect(new Headers(fetcher.mock.calls[0]?.[1]?.headers).get("X-CSRF-Token")).toBe("csrf");
    expect(() => requireNoContent({ response: Response.json({}) })).toThrow(ApiError);
    expect(() =>
      requireNoContent({ response: new Response(null, { status: 204 }), error: {} }),
    ).toThrow(ApiError);
  });

  it("preserves network and cancellation rejections", async () => {
    const failure = new TypeError("network failed");
    const client = createExplorerClient(vi.fn<typeof fetch>().mockRejectedValue(failure));
    await expect(sdk.getStatus({ client })).rejects.toBe(failure);
    const controller = new AbortController();
    const abortedClient = createExplorerClient(async (_input, init) => {
      expect(init?.signal?.aborted).toBe(true);
      throw init?.signal?.reason;
    });
    controller.abort();
    await expect(
      sdk.getStatus({ client: abortedClient, signal: controller.signal }),
    ).rejects.toMatchObject({ name: "AbortError" });
  });

  it("forwards the SDK request signal and cancels an in-flight request", async () => {
    const controller = new AbortController();
    let notifyStarted!: (signal: AbortSignal) => void;
    const started = new Promise<AbortSignal>((resolve) => {
      notifyStarted = resolve;
    });
    let sdkSignal: AbortSignal | undefined;
    const client = createExplorerClient(
      (_input, init) =>
        new Promise<Response>((_resolve, reject) => {
          const signal = init!.signal!;
          signal.addEventListener("abort", () => reject(signal.reason), { once: true });
          notifyStarted(signal);
        }),
    );
    client.interceptors.request.use((request) => {
      sdkSignal = request.signal;
      return request;
    });
    const pending = sdk.getStatus({ client, signal: controller.signal });
    const forwardedSignal = await started;
    controller.abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(forwardedSignal).toBe(sdkSignal);
    expect(forwardedSignal.aborted).toBe(true);
  });

  it("preserves parse errors on successful invalid JSON", async () => {
    const client = createExplorerClient(async () => new Response("not JSON"));
    await expect(sdk.getStatus({ client })).rejects.toBeInstanceOf(SyntaxError);
  });

  it("normalizes non-JSON HTTP failures without leaking their bodies", async () => {
    const client = createExplorerClient(
      async () => new Response("private proxy failure", { status: 502 }),
    );
    const result = await sdk.getStatus({ client });
    expect(() => requireEnvelope(result)).toThrowError(
      expect.objectContaining({
        status: 502,
        code: "HTTP_ERROR",
        message: "Explorer API request failed (502)",
      }),
    );
  });

  it("preserves binary bytes and response headers", async () => {
    const bytes = new Uint8Array([0, 255, 128, 10, 13]);
    const client = createExplorerClient(
      async () =>
        new Response(bytes, {
          headers: {
            "Content-Type": "application/octet-stream",
            "Content-Disposition": "attachment; filename=blob-1-0.bin",
          },
        }),
    );
    const response = requireResponse(
      await sdk.getBlobContent({ client, path: { hash, index: "0" }, parseAs: "stream" }),
    );
    expect(new Uint8Array(await response.arrayBuffer())).toEqual(bytes);
    expect(response.headers.get("Content-Type")).toBe("application/octet-stream");
    expect(response.headers.get("Content-Disposition")).toBe("attachment; filename=blob-1-0.bin");
  });
});
