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
  it("downloads only successful CSV and revokes its URL", async () => {
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
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    await downloadAddressCSV(request, "csrf", new AbortController().signal);
    expect(click).toHaveBeenCalledOnce();
    expect(create).toHaveBeenCalledOnce();
    expect(fetcher.mock.calls[0]?.[0]).toBe("/api/v1/users/me/exports/address-activity");
    expect(fetcher.mock.calls[0]?.[1].headers.get("X-CSRF-Token")).toBe("csrf");
    await vi.runAllTimersAsync();
    expect(revoke).toHaveBeenCalledWith("blob:download");
  });
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
