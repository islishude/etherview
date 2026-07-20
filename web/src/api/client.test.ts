import { describe, expect, it, vi } from "vitest";

import { ApiError, createExplorerClient, requireEnvelope } from "./client";

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

    const result = requireEnvelope(
      await client.GET("/blocks", { params: { query: { limit: 25 } } }),
    );

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

    const result = await client.GET("/transactions/{hash}/trace", {
      params: { path: { hash } },
    });
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
      address,
      at_block_hash: hash,
      code_hash: hash,
      compiler_version: "0.8.30",
      contract_identifier: "Example.sol:Example",
      creation_bytecode: "0x6000",
      language: "solidity" as const,
      runtime_bytecode: "0x6000",
      standard_json: { language: "Solidity" },
      submit_to_sourcify: false,
    };

    requireEnvelope(
      await client.POST("/verification/jobs", {
        body,
        headers: { "X-API-Key": secret },
      }),
    );

    const [url, request] = fetcher.mock.calls[0] ?? [];
    expect(url).toBe("/api/v1/verification/jobs");
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

    await expect(
      // @ts-expect-error Deliberately exercise the runtime defense behind the generated path union.
      client.GET("../metrics"),
    ).rejects.toThrow("/api/v1 boundary");
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("rejects a success body that is not the documented data/meta envelope", async () => {
    const client = createExplorerClient(
      vi.fn<typeof fetch>().mockResolvedValue(Response.json({ data: [] })),
    );

    const result = await client.GET("/blocks");
    expect(() => requireEnvelope(result)).toThrowError(
      expect.objectContaining<Partial<ApiError>>({ code: "INVALID_RESPONSE" }),
    );
  });

  it("rejects a success envelope missing required native metadata", async () => {
    const client = createExplorerClient(
      vi.fn<typeof fetch>().mockResolvedValue(Response.json({ data: [], meta: {} })),
    );

    const result = await client.GET("/blocks");
    expect(() => requireEnvelope(result)).toThrowError(
      expect.objectContaining<Partial<ApiError>>({ code: "INVALID_RESPONSE" }),
    );
  });

  it("does not trust an error body missing its required request ID", async () => {
    const client = createExplorerClient(
      vi
        .fn<typeof fetch>()
        .mockResolvedValue(
          Response.json(
            { error: { code: "LEAKED_CODE", message: "untrusted" } },
            { status: 503 },
          ),
        ),
    );

    const result = await client.GET("/blocks");
    expect(() => requireEnvelope(result)).toThrowError(
      expect.objectContaining<Partial<ApiError>>({
        code: "HTTP_ERROR",
        requestId: undefined,
      }),
    );
  });
});
