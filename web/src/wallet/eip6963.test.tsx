import { useState } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  assertWalletChain,
  chainsMatch,
  EIP6963_ANNOUNCE_EVENT,
  EIP6963_REQUEST_EVENT,
  normalizeChainID,
  WalletBoundaryError,
  type EIP1193Provider,
  type EIP1193RequestArguments,
} from "./eip6963";
import { useWallet, WalletProvider } from "./WalletProvider";

const account = "0x0000000000000000000000000000000000000001";

describe("EIP-6963 wallet boundary", () => {
  it("normalizes hexadecimal and decimal chain IDs", () => {
    expect(normalizeChainID("0x1")).toBe("1");
    expect(normalizeChainID("10")).toBe("10");
    expect(normalizeChainID("invalid")).toBeUndefined();
    expect(chainsMatch("0x01", "1")).toBe(true);
  });

  it("fails closed when the configured chain is unavailable or mismatched", () => {
    expect(() => assertWalletChain("0x1", undefined)).toThrowError(
      expect.objectContaining<Partial<WalletBoundaryError>>({ code: "CHAIN_UNAVAILABLE" }),
    );
    expect(() => assertWalletChain("0xa", "1")).toThrowError(
      expect.objectContaining<Partial<WalletBoundaryError>>({ code: "CHAIN_MISMATCH" }),
    );
  });

  it("discovers a provider and sends contract reads directly to it", async () => {
    const request = vi.fn(async ({ method }: EIP1193RequestArguments): Promise<unknown> => {
      if (method === "eth_requestAccounts") return [account];
      if (method === "eth_chainId") return "0x1";
      if (method === "eth_call") return "0x1234";
      throw new Error(`unexpected method ${method}`);
    });
    const provider: EIP1193Provider = { request: request as EIP1193Provider["request"] };
    const announce = () => {
      window.dispatchEvent(
        new CustomEvent(EIP6963_ANNOUNCE_EVENT, {
          detail: {
            info: {
              uuid: "test-wallet",
              name: "Test Wallet",
              icon: "data:image/png;base64,",
              rdns: "dev.etherview.test-wallet",
            },
            provider,
          },
        }),
      );
    };
    window.addEventListener(EIP6963_REQUEST_EVENT, announce);

    render(
      <WalletProvider>
        <WalletHarness />
      </WalletProvider>,
    );

    expect(await screen.findByText("Test Wallet")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Test Wallet" }));
    expect(await screen.findByText(account)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByText("0x1234")).toBeInTheDocument();

    await waitFor(() => {
      expect(request).toHaveBeenCalledWith(
        expect.objectContaining({ method: "eth_call" }),
      );
    });
    window.removeEventListener(EIP6963_REQUEST_EVENT, announce);
  });
});

function WalletHarness() {
  const wallet = useWallet();
  const provider = wallet.providers[0];
  const [result, setResult] = useState<string>();
  return (
    <div>
      {provider && (
        <button type="button" onClick={() => void wallet.connect(provider.info.uuid)}>
          {provider.info.name}
        </button>
      )}
      {wallet.active && <span>{wallet.active.account}</span>}
      <button
        type="button"
        disabled={!wallet.active}
        onClick={() => {
          void wallet.readContract({ to: account, data: "0x" }, "1").then(setResult);
        }}
      >
        Read
      </button>
      {result && <output>{result}</output>}
    </div>
  );
}
