import { encodeAbiParameters } from "viem";
import { describe, expect, it, vi } from "vitest";
import { checkTopupBalance, readTopupBalance } from "./balance";

const owner = "0x1111111111111111111111111111111111111111";
const asset = "0x2222222222222222222222222222222222222222";
describe("top-up wallet balance", () => {
  it.each([0n, 1n, 900719925474099312345n, (1n << 256n) - 1n])(
    "reads and compares %s without rounding",
    async (value) => {
      const read = vi.fn().mockResolvedValue(encodeAbiParameters([{ type: "uint256" }], [value]));
      expect(await readTopupBalance(read, owner, asset, "48815")).toBe(value);
      expect(read).toHaveBeenCalledWith(
        { to: asset, data: `0x70a08231${owner.slice(2).padStart(64, "0")}` },
        "48815",
      );
      if (value > 0n)
        expect(await checkTopupBalance(read, owner, asset, "48815", value.toString())).toBe(value);
      await expect(
        checkTopupBalance(read, owner, asset, "48815", (value + 1n).toString()),
      ).rejects.toMatchObject({ reason: "insufficient" });
    },
  );
  it("fails closed on malformed RPC data without exposing provider details", async () => {
    const read = vi.fn().mockResolvedValue("0x");
    await expect(readTopupBalance(read, owner, asset, "1")).rejects.toMatchObject({
      reason: "unavailable",
    });
    read.mockRejectedValue(new Error("https://private-rpc/secret"));
    await expect(readTopupBalance(read, owner, asset, "1")).rejects.toThrow(
      "Top-up balance check failed",
    );
  });
});
