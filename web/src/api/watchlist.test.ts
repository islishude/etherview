import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadAddressCSV } from "./watchlist";
const request = {
  address: "0x1111111111111111111111111111111111111111",
  kind: "transaction" as const,
  direction: "both" as const,
  from: "2026-09-01T00:00:00Z",
  to: "2026-09-02T00:00:00Z",
};
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
describe("generated CSV client", () => {
  it.each(["transaction", "erc20", "nft"] as const)(
    "downloads %s CSV with the selected address and UTC range and revokes its URL",
    async (kind) => {
      vi.useFakeTimers();
      const fetcher = vi.fn().mockResolvedValue(
        new Response("amount\r\n12345678901234567890\r\n", {
          headers: { "Content-Type": "text/csv" },
        }),
      );
      vi.stubGlobal("fetch", fetcher);
      const create = vi.fn(() => "blob:download");
      const revoke = vi.fn();
      vi.stubGlobal(
        "URL",
        Object.assign(class extends URL {}, { createObjectURL: create, revokeObjectURL: revoke }),
      );
      let filename = "";
      const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
        this: HTMLAnchorElement,
      ) {
        filename = this.download;
      });
      await downloadAddressCSV(
        {
          ...request,
          kind,
          address: "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
          from: "2026-09-01T08:00:00+08:00",
        },
        "csrf",
        new AbortController().signal,
      );
      expect(filename).toBe(
        `0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-2026-09-01T00-00-00Z-2026-09-02T00-00-00Z-${kind}.csv`,
      );
      expect(click).toHaveBeenCalledOnce();
      expect(create).toHaveBeenCalledOnce();
      expect(fetcher.mock.calls[0]?.[0]).toBe("/api/v1/users/me/exports/address-activity");
      expect(fetcher.mock.calls[0]?.[1].headers.get("X-CSRF-Token")).toBe("csrf");
      await vi.runAllTimersAsync();
      expect(revoke).toHaveBeenCalledWith("blob:download");
    },
  );
  it.each([{ status: 204 }, { status: 200, headers: { "Content-Length": "0" } }])(
    "rejects explicitly empty export responses: %j",
    async (init) => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, init)));
      const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
      await expect(
        downloadAddressCSV(request, "csrf", new AbortController().signal),
      ).rejects.toMatchObject({ code: "HTTP_ERROR" });
      expect(click).not.toHaveBeenCalled();
    },
  );
  it("does not download a JSON error as a CSV file", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: { code: "export_limit", message: "too large", request_id: "test" },
          }),
          { status: 422, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    await expect(
      downloadAddressCSV(request, "csrf", new AbortController().signal),
    ).rejects.toMatchObject({ code: "export_limit" });
    expect(click).not.toHaveBeenCalled();
  });
});
