import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toHex } from "viem";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { AuthChallenge } from "@/api/auth";
import {
  assertWalletChain,
  buildAddEthereumChainParameter,
  chainsMatch,
  EIP6963_ANNOUNCE_EVENT,
  EIP6963_REQUEST_EVENT,
  isContractCalldata,
  isContractResult,
  isTransactionHash,
  isUint256Quantity,
  isWalletSignature,
  MAX_SIGN_MESSAGE_BYTES,
  normalizeChainID,
  normalizeProviderChainID,
  snapshotProviderDetail,
  toWalletBoundaryError,
  WalletBoundaryError,
  type EIP1193Event,
  type EIP1193Provider,
  type EIP1193RequestArguments,
  type EIP6963ProviderDetail,
} from "./eip6963";
import { useWallet, WalletProvider } from "./WalletProvider";

const accountA = "0x1111111111111111111111111111111111111111";
const accountB = "0x2222222222222222222222222222222222222222";
const target = "0x3333333333333333333333333333333333333333";
const transactionHash = `0x${"a".repeat(64)}`;
const walletSignature = `0x${"b".repeat(130)}`;
const walletChallengeID = "018f3b52-0b3d-7bf1-b65f-6f214827cb43";
const providerUUID = "00000000-0000-4000-8000-000000000001";
const secondaryProviderUUID = "00000000-0000-4000-8000-000000000002";
const requestListeners: EventListener[] = [];

afterEach(() => {
  for (const listener of requestListeners.splice(0)) {
    window.removeEventListener(EIP6963_REQUEST_EVENT, listener);
  }
});

describe("EIP-6963 wallet boundary", () => {
  it("validates and snapshots bounded EIP-6963 provider metadata", async () => {
    const fake = createFakeProvider();
    const source = providerDetail(fake.provider);
    const snapshot = snapshotProviderDetail(source);

    expect(snapshot).toBeDefined();
    expect(Object.isFrozen(snapshot)).toBe(true);
    expect(Object.isFrozen(snapshot?.info)).toBe(true);
    source.info.name = "Mutated Wallet";
    expect(snapshot?.info.name).toBe("Test Wallet");

    expect(
      snapshotProviderDetail(providerDetail(fake.provider, { uuid: "not-a-uuid" })),
    ).toBeUndefined();
    expect(
      snapshotProviderDetail(providerDetail(fake.provider, { icon: "https://wallet/icon.png" })),
    ).toBeUndefined();
    expect(
      snapshotProviderDetail(providerDetail(fake.provider, { rdns: "org.etherview.ſpoof" })),
    ).toBeUndefined();
    expect(
      snapshotProviderDetail({
        ...providerDetail(fake.provider),
        provider: { request: fake.provider.request },
      }),
    ).toBeUndefined();

    const hostile = Object.create(null) as Record<string, unknown>;
    Object.defineProperty(hostile, "info", {
      get() {
        throw new Error("hostile getter");
      },
    });
    hostile.provider = fake.provider;
    expect(snapshotProviderDetail(hostile)).toBeUndefined();

    fake.provider.request = vi.fn(async () => "0x2") as EIP1193Provider["request"];
    await expect(snapshot?.provider.request({ method: "eth_chainId" })).resolves.toBe("0x1");
    expect(Object.isFrozen(snapshot?.provider)).toBe(true);
  });

  it("normalizes only bounded chain IDs and validates wallet wire values", () => {
    expect(normalizeChainID("0x1")).toBe("1");
    expect(normalizeChainID("10")).toBe("10");
    expect(normalizeChainID(Number.MAX_SAFE_INTEGER)).toBe(String(Number.MAX_SAFE_INTEGER));
    expect(normalizeChainID(Number.MAX_SAFE_INTEGER + 1)).toBeUndefined();
    expect(normalizeChainID(1n << 256n)).toBeUndefined();
    expect(normalizeChainID(" 1")).toBeUndefined();
    expect(normalizeChainID("+1")).toBeUndefined();
    expect(normalizeChainID((1n << 256n).toString())).toBeUndefined();
    expect(normalizeProviderChainID("0x1")).toBe("1");
    expect(normalizeProviderChainID("1")).toBeUndefined();
    expect(normalizeProviderChainID("0x01")).toBeUndefined();
    expect(normalizeProviderChainID(`0x1${"0".repeat(64)}`)).toBeUndefined();
    expect(chainsMatch("0x01", "1")).toBe(true);

    expect(isContractCalldata("0x1234")).toBe(true);
    expect(isContractCalldata("0x123")).toBe(false);
    expect(isContractCalldata(`0x${"00".repeat(128 * 1024 + 1)}`)).toBe(false);
    expect(isContractResult("0x")).toBe(true);
    expect(isContractResult(`0x${"00".repeat(1024 * 1024 + 1)}`)).toBe(false);
    expect(isTransactionHash(transactionHash)).toBe(true);
    expect(isTransactionHash("0x1234")).toBe(false);
    expect(isWalletSignature(walletSignature)).toBe(true);
    expect(isWalletSignature(`0x${"a".repeat(128)}`)).toBe(false);
    expect(isUint256Quantity("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"))
      .toBe(true);
    expect(isUint256Quantity(`0x1${"0".repeat(64)}`)).toBe(false);
    expect(toWalletBoundaryError({ code: 4100 }).code).toBe("NOT_CONNECTED");
    expect(toWalletBoundaryError({ code: 4900 }).code).toBe("PROVIDER_DISCONNECTED");
    expect(toWalletBoundaryError({ code: 4901 }).code).toBe("CHAIN_MISMATCH");
  });

  it("builds one bounded EIP-3085 add-chain parameter", () => {
    expect(buildAddEthereumChainParameter({
      chain_id: "11155111",
      chain_name: "Wallet Testnet",
      native_currency: { name: "Test Ether", symbol: "TETH", decimals: 18 },
      rpc_urls: ["https://rpc.example", "http://localhost:8545"],
      block_explorer_urls: ["https://explorer.example"],
      icon_urls: ["https://assets.example/icon.png"],
    })).toEqual({
      chainId: "0xaa36a7",
      chainName: "Wallet Testnet",
      nativeCurrency: { name: "Test Ether", symbol: "TETH", decimals: 18 },
      rpcUrls: ["https://rpc.example", "http://localhost:8545"],
      blockExplorerUrls: ["https://explorer.example"],
      iconUrls: ["https://assets.example/icon.png"],
    });
    for (const rpcURL of [
      "http://rpc.example",
      "http://127.0.0.1:8545",
      "http://reth:8545",
      "ftp://localhost:8545",
      "https://user:secret@rpc.example",
      "https://rpc.example/?key=secret",
      "https://rpc.example/#fragment",
    ]) {
      expect(() => buildAddEthereumChainParameter({
        chain_id: "1",
        chain_name: "Test",
        native_currency: { name: "Test", symbol: "TST", decimals: 18 },
        rpc_urls: [rpcURL],
      })).toThrowError(new WalletBoundaryError("CHAIN_UNAVAILABLE"));
    }
    for (const field of ["block_explorer_urls", "icon_urls"] as const) {
      expect(() => buildAddEthereumChainParameter({
        chain_id: "1",
        chain_name: "Test",
        native_currency: { name: "Test", symbol: "TST", decimals: 18 },
        rpc_urls: ["https://rpc.example"],
        [field]: ["http://localhost:8080"],
      })).toThrowError(new WalletBoundaryError("CHAIN_UNAVAILABLE"));
    }
  });

  it("adds a configured network without requesting accounts or changing the active session", async () => {
    const fake = createFakeProvider({ wallet_addEthereumChain: null });
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Add Test Wallet" }));
    expect(await screen.findByText("added")).toBeVisible();
    expect(fake.request).toHaveBeenCalledWith({
      method: "wallet_addEthereumChain",
      params: [{
        chainId: "0x1",
        chainName: "Wallet Testnet",
        nativeCurrency: { name: "Ether", symbol: "ETH", decimals: 18 },
        rpcUrls: ["http://localhost:8545"],
      }],
    });
    expect(fake.request.mock.calls.some(([request]) => request.method === "eth_requestAccounts"))
      .toBe(false);
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();
  });

  it("fails closed when the configured chain is unavailable, malformed, or mismatched", () => {
    expect(() => assertWalletChain("0x1", undefined)).toThrowError(
      expect.objectContaining<Partial<WalletBoundaryError>>({ code: "CHAIN_UNAVAILABLE" }),
    );
    expect(() => assertWalletChain("1", "1")).toThrowError(
      expect.objectContaining<Partial<WalletBoundaryError>>({
        code: "INVALID_PROVIDER_RESPONSE",
      }),
    );
    expect(() => assertWalletChain("0xa", "1")).toThrowError(
      expect.objectContaining<Partial<WalletBoundaryError>>({ code: "CHAIN_MISMATCH" }),
    );
  });

  it("preserves the first provider for a UUID and ignores colliding announcements", async () => {
    const first = createFakeProvider();
    const collision = createFakeProvider();
    registerProvider(providerDetail(first.provider));
    renderWallet();

    expect(await screen.findByRole("button", { name: "Test Wallet" })).toBeVisible();
    announceProvider(
      providerDetail(collision.provider, {
        name: "Imitation Wallet",
      }),
    );

    expect(screen.queryByRole("button", { name: "Imitation Wallet" })).not.toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "Test Wallet" }));
    expect(first.request).toHaveBeenCalledWith({ method: "eth_requestAccounts" });
    expect(collision.request).not.toHaveBeenCalled();
  });

  it("returns only the bounded connected-wallet identity snapshot", async () => {
    const fake = createFakeProvider();
    registerProvider(providerDetail(fake.provider));
    renderWallet();

    await userEvent.setup().click(
      await screen.findByRole("button", { name: "Test Wallet" }),
    );

    expect(await screen.findByRole("status")).toHaveTextContent(
      JSON.stringify({
        uuid: "00000000-0000-4000-8000-000000000001",
        name: "Test Wallet",
        account: accountA,
        chainID: "1",
        revision: 1,
      }),
    );
  });

  it("bounds discovery to the first 32 valid providers", async () => {
    renderWallet();
    act(() => {
      for (let index = 0; index < 33; index += 1) {
        const fake = createFakeProvider();
        announceProvider(
          providerDetail(fake.provider, {
            uuid: `00000000-0000-4000-8000-${(index + 1).toString(16).padStart(12, "0")}`,
            name: `Wallet ${index}`,
            rdns: `org.etherview.wallet${index}`,
          }),
        );
      }
    });

    await waitFor(() => {
      expect(screen.getAllByRole("button", { name: /^Wallet \d+$/u })).toHaveLength(32);
    });
    expect(screen.queryByRole("button", { name: "Wallet 32" })).not.toBeInTheDocument();
  });

  it("sends exact same-chain reads and writes only through the selected provider", async () => {
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    const fake = createFakeProvider();
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    expect(await screen.findByTestId("active-account")).toHaveTextContent(accountA);

    await user.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByText("0x1234")).toBeVisible();
    expect(fake.request).toHaveBeenCalledWith({
      method: "eth_call",
      params: [
        {
          to: target,
          data: "0x1234",
          from: accountA,
          chainId: "0x1",
        },
        "latest",
      ],
    });

    await user.click(screen.getByRole("button", { name: "Write" }));
    expect(await screen.findByText(transactionHash)).toBeVisible();
    expect(fake.request).toHaveBeenCalledWith({
      method: "eth_sendTransaction",
      params: [
        {
          to: target,
          data: "0x1234",
          from: accountA,
          chainId: "0x1",
          value: "0xf",
        },
      ],
    });
    expect(fake.request.mock.calls.filter(([request]) => request.method === "eth_accounts"))
      .toHaveLength(2);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("signs the exact bounded UTF-8 message and rechecks the same wallet session", async () => {
    const fake = createFakeProvider();
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    await user.click(screen.getByRole("button", { name: "Sign" }));
    expect(await screen.findByText(walletSignature)).toBeVisible();
    const challenge = walletSIWEChallenge();
    expect(fake.request).toHaveBeenCalledWith({
      method: "personal_sign",
      params: [toHex(challenge.message), accountA],
    });
    expect(
      fake.request.mock.calls.filter(([request]) => request.method === "eth_chainId"),
    ).toHaveLength(3);
    expect(
      fake.request.mock.calls.filter(([request]) => request.method === "eth_accounts"),
    ).toHaveLength(2);

    await user.click(screen.getByRole("button", { name: "Sign oversized" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("INVALID_REQUEST");
    expect(
      fake.request.mock.calls.filter(([request]) => request.method === "personal_sign"),
    ).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "Sign with stale identity" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent(
      "SESSION_CHANGED",
    );
    expect(
      fake.request.mock.calls.filter(([request]) => request.method === "personal_sign"),
    ).toHaveLength(1);
  });

  it("discards signatures across ABA revisions and silent account drift", async () => {
    let resolveSignature: ((value: unknown) => void) | undefined;
    const delayedSignature = new Promise<unknown>((resolve) => {
      resolveSignature = resolve;
    });
    const fake = createFakeProvider({ personal_sign: () => delayedSignature });
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    await user.click(screen.getByRole("button", { name: "Sign" }));
    await waitFor(() => {
      expect(
        fake.request.mock.calls.some(([request]) => request.method === "personal_sign"),
      ).toBe(true);
    });
    act(() => {
      fake.emit("accountsChanged", [accountB]);
      fake.emit("accountsChanged", [accountA]);
    });
    await act(async () => resolveSignature?.(walletSignature));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("SESSION_CHANGED");
    expect(screen.queryByText(walletSignature)).not.toBeInTheDocument();

    fake.setResponse("personal_sign", () => {
      fake.setResponse("eth_accounts", [accountB]);
      return walletSignature;
    });
    await user.click(screen.getByRole("button", { name: "Sign" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("ACCOUNT_CHANGED");
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();
  });

  it("rejects malformed signatures and hides provider signing errors", async () => {
    const fake = createFakeProvider({ personal_sign: "0x1234" });
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    await user.click(screen.getByRole("button", { name: "Sign" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent(
      "INVALID_PROVIDER_RESPONSE",
    );

    fake.setResponse("personal_sign", () =>
      Promise.reject({ code: 4001, message: "secret signing rejection" }),
    );
    await user.click(screen.getByRole("button", { name: "Sign" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("USER_REJECTED");
    expect(document.body).not.toHaveTextContent("secret signing rejection");
  });

  it("rejects reads and writes until a discovered wallet is connected", async () => {
    renderWallet();
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("NOT_CONNECTED");
    await user.click(screen.getByRole("button", { name: "Write" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("NOT_CONNECTED");
  });

  it("synchronizes silent chain drift and requires a fresh operation", async () => {
    const mismatch = createFakeProvider();
    registerProvider(providerDetail(mismatch.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    mismatch.setResponse("eth_chainId", "0x2");
    await user.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("CHAIN_MISMATCH");
    expect(screen.getByTestId("active-chain")).toHaveTextContent("2");
    expect(mismatch.request.mock.calls.some(([request]) => request.method === "eth_call")).toBe(
      false,
    );
    expect(
      mismatch.request.mock.calls.some(([request]) => request.method === "eth_sendTransaction"),
    ).toBe(false);

    mismatch.setResponse("eth_chainId", "0x1");
    await user.click(screen.getByRole("button", { name: "Write" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("SESSION_CHANGED");
    expect(screen.getByTestId("active-chain")).toHaveTextContent("1");
    expect(
      mismatch.request.mock.calls.some(([request]) => request.method === "eth_call"),
    ).toBe(false);
    expect(
      mismatch.request.mock.calls.some(([request]) => request.method === "eth_sendTransaction"),
    ).toBe(false);

    await user.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByText("0x1234")).toBeVisible();
  });

  it("fails closed on silent empty or changed accounts before a call", async () => {
    const fake = createFakeProvider();
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    fake.setResponse("eth_accounts", []);
    await user.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("NOT_CONNECTED");
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();
    expect(fake.request.mock.calls.some(([request]) => request.method === "eth_call")).toBe(false);

    fake.setResponse("eth_accounts", [accountA]);
    await user.click(screen.getByRole("button", { name: "Test Wallet" }));
    fake.setResponse("eth_accounts", new Array(257).fill(accountA));
    await user.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent(
      "INVALID_PROVIDER_RESPONSE",
    );
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();

    fake.setResponse("eth_accounts", [accountA]);
    await user.click(screen.getByRole("button", { name: "Test Wallet" }));
    fake.setResponse("eth_accounts", [accountB]);
    await user.click(screen.getByRole("button", { name: "Write" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("ACCOUNT_CHANGED");
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();
    expect(
      fake.request.mock.calls.some(([request]) => request.method === "eth_sendTransaction"),
    ).toBe(false);
  });

  it("fails closed on unauthorized, disconnected, and chain-disconnected requests", async () => {
    const fake = createFakeProvider();
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    fake.setResponse("eth_chainId", () =>
      Promise.reject({ code: 4900, message: "secret disconnected reason" }),
    );
    await user.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent(
      "PROVIDER_DISCONNECTED",
    );
    expect(screen.getByTestId("context-error")).toHaveTextContent("PROVIDER_DISCONNECTED");
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();

    fake.setResponse("eth_chainId", "0x1");
    await user.click(screen.getByRole("button", { name: "Test Wallet" }));
    fake.setResponse("eth_accounts", () =>
      Promise.reject({ code: 4100, message: "secret unauthorized reason" }),
    );
    await user.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("NOT_CONNECTED");
    expect(screen.getByTestId("context-error")).toHaveTextContent("NOT_CONNECTED");
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();

    fake.setResponse("eth_accounts", [accountA]);
    await user.click(screen.getByRole("button", { name: "Test Wallet" }));
    fake.setResponse("eth_call", () =>
      Promise.reject({ code: 4901, message: "secret chain reason" }),
    );
    await user.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent("CHAIN_MISMATCH");
    expect(screen.getByTestId("context-error")).toHaveTextContent("CHAIN_MISMATCH");
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent(/secret .* reason/u);
  });

  it("fences old-provider events and fails closed on disconnect or malformed events", async () => {
    const old = createFakeProvider({}, { ignoreRemoval: true });
    const current = createFakeProvider({
      eth_requestAccounts: [accountB],
      eth_accounts: [accountB],
    });
    registerProvider(providerDetail(old.provider));
    registerProvider(
      providerDetail(current.provider, {
        uuid: secondaryProviderUUID,
        name: "Current Wallet",
        rdns: "org.etherview.current",
      }),
    );
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    await user.click(screen.getByRole("button", { name: "Current Wallet" }));
    expect(await screen.findByTestId("active-account")).toHaveTextContent(accountB);

    act(() => {
      old.emit("accountsChanged", [accountA]);
      old.emit("chainChanged", "0x2");
    });
    expect(screen.getByTestId("active-account")).toHaveTextContent(accountB);
    expect(screen.getByTestId("active-chain")).toHaveTextContent("1");

    act(() => {
      current.emit("accountsChanged", [accountA]);
      current.emit("chainChanged", "0x2");
    });
    expect(screen.getByTestId("active-account")).toHaveTextContent(accountA);
    expect(screen.getByTestId("active-chain")).toHaveTextContent("2");

    act(() => current.emit("disconnect", { code: 4900, message: "secret" }));
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();
    expect(screen.getByTestId("context-error")).toHaveTextContent("PROVIDER_DISCONNECTED");

    await user.click(screen.getByRole("button", { name: "Current Wallet" }));
    act(() => current.emit("accountsChanged", []));
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();
    expect(screen.getByTestId("context-error")).toHaveTextContent("NOT_CONNECTED");

    await user.click(screen.getByRole("button", { name: "Current Wallet" }));
    act(() => current.emit("chainChanged", "not-hex"));
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();
    expect(screen.getByTestId("context-error")).toHaveTextContent(
      "INVALID_PROVIDER_RESPONSE",
    );
  });

  it("does not trust provider array methods or throwing account getters", async () => {
    const hostileAccounts: unknown[] = ["not-an-address"];
    Object.defineProperties(hostileAccounts, {
      every: { value: () => true },
      map: { value: () => ["also-not-an-address"] },
    });
    const fake = createFakeProvider({ eth_requestAccounts: hostileAccounts });
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent(
      "INVALID_PROVIDER_RESPONSE",
    );
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();

    fake.setResponse("eth_requestAccounts", [accountA]);
    await user.click(screen.getByRole("button", { name: "Test Wallet" }));
    const throwingAccounts = new Proxy([accountA], {
      get(target, property, receiver) {
        if (property === "0") throw new Error("hostile account getter");
        return Reflect.get(target, property, receiver);
      },
    });
    act(() => fake.emit("accountsChanged", throwingAccounts));
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();
    expect(screen.getByTestId("context-error")).toHaveTextContent(
      "INVALID_PROVIDER_RESPONSE",
    );

    fake.setResponse("eth_requestAccounts", [accountA]);
    await user.click(screen.getByRole("button", { name: "Test Wallet" }));
    const revokedAccounts = Proxy.revocable([accountA], {});
    revokedAccounts.revoke();
    act(() => fake.emit("accountsChanged", revokedAccounts.proxy));
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();
    expect(screen.getByTestId("context-error")).toHaveTextContent(
      "INVALID_PROVIDER_RESPONSE",
    );
  });

  it("rejects invalid results and never exposes hostile provider error text", async () => {
    const fake = createFakeProvider({
      eth_call: { jsonrpc: "2.0", result: "0x1234" },
      eth_sendTransaction: "0x1234",
    });
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    await user.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent(
      "INVALID_PROVIDER_RESPONSE",
    );
    await user.click(screen.getByRole("button", { name: "Write" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent(
      "TRANSACTION_OUTCOME_UNKNOWN",
    );
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Test Wallet" }));
    fake.setResponse("eth_call", `0x${"00".repeat(1024 * 1024 + 1)}`);
    await user.click(screen.getByRole("button", { name: "Read" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent(
      "INVALID_PROVIDER_RESPONSE",
    );

    fake.setResponse("eth_sendTransaction", () =>
      Promise.reject(new Error("secret uncertain transport failure")),
    );
    await user.click(screen.getByRole("button", { name: "Write" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent(
      "TRANSACTION_OUTCOME_UNKNOWN",
    );
    expect(document.body).not.toHaveTextContent("secret uncertain transport failure");

    await user.click(screen.getByRole("button", { name: "Test Wallet" }));
    fake.setResponse("eth_sendTransaction", () =>
      Promise.reject({ code: 4900, message: "secret post-dispatch disconnect" }),
    );
    await user.click(screen.getByRole("button", { name: "Write" }));
    expect(await screen.findByTestId("operation-error")).toHaveTextContent(
      "TRANSACTION_OUTCOME_UNKNOWN",
    );
    expect(screen.getByTestId("context-error")).toHaveTextContent(
      "PROVIDER_DISCONNECTED",
    );
    expect(screen.queryByTestId("active-account")).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("secret post-dispatch disconnect");

    const rejected = createFakeProvider({
      eth_requestAccounts: () =>
        Promise.reject({ code: 4001, message: "secret-bearing provider message" }),
    });
    const rejectedDetail = providerDetail(rejected.provider, {
        uuid: secondaryProviderUUID,
        name: "Rejecting Wallet",
        rdns: "org.etherview.rejecting",
      });
    registerProvider(rejectedDetail);
    act(() => announceProvider(rejectedDetail));
    await user.click(await screen.findByRole("button", { name: "Rejecting Wallet" }));
    expect(await screen.findByTestId("context-error")).toHaveTextContent("USER_REJECTED");
    expect(document.body).not.toHaveTextContent("secret-bearing provider message");
  });

  it("cancels an operation when a chain event races the provider preflight", async () => {
    let resolveChain: ((value: unknown) => void) | undefined;
    const delayedChain = new Promise<unknown>((resolve) => {
      resolveChain = resolve;
    });
    const fake = createFakeProvider();
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    fake.setResponse("eth_chainId", () => delayedChain);
    await user.click(screen.getByRole("button", { name: "Read" }));
    act(() => fake.emit("chainChanged", "0x2"));
    await act(async () => resolveChain?.("0x1"));

    expect(await screen.findByTestId("operation-error")).toHaveTextContent("CHAIN_MISMATCH");
    expect(fake.request.mock.calls.some(([request]) => request.method === "eth_call")).toBe(
      false,
    );
  });

  it("discards a provider result after disconnect and same-provider reconnection", async () => {
    let resolveCall: ((value: unknown) => void) | undefined;
    const delayedCall = new Promise<unknown>((resolve) => {
      resolveCall = resolve;
    });
    const fake = createFakeProvider({ eth_call: () => delayedCall });
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    await user.click(screen.getByRole("button", { name: "Read" }));
    await waitFor(() => {
      expect(fake.request.mock.calls.some(([request]) => request.method === "eth_call")).toBe(
        true,
      );
    });
    act(() => fake.emit("disconnect", { code: 4900 }));
    await user.click(screen.getByRole("button", { name: "Test Wallet" }));
    expect(await screen.findByTestId("active-account")).toHaveTextContent(accountA);
    await act(async () => resolveCall?.("0x1234"));

    expect(await screen.findByTestId("operation-error")).toHaveTextContent("SESSION_CHANGED");
    expect(screen.queryByText("0x1234")).not.toBeInTheDocument();
  });

  it("rejects a delayed result after an account ABA event sequence", async () => {
    let resolveCall: ((value: unknown) => void) | undefined;
    const delayedCall = new Promise<unknown>((resolve) => {
      resolveCall = resolve;
    });
    const fake = createFakeProvider({ eth_call: () => delayedCall });
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    await user.click(screen.getByRole("button", { name: "Read" }));
    await waitFor(() => {
      expect(fake.request.mock.calls.some(([request]) => request.method === "eth_call")).toBe(
        true,
      );
    });
    act(() => {
      fake.emit("accountsChanged", [accountB]);
      fake.emit("accountsChanged", [accountA]);
    });
    await act(async () => resolveCall?.("0x1234"));

    expect(await screen.findByTestId("operation-error")).toHaveTextContent("SESSION_CHANGED");
    expect(screen.queryByText("0x1234")).not.toBeInTheDocument();
  });

  it("marks a delayed transaction outcome unknown after an account ABA sequence", async () => {
    let resolveTransaction: ((value: unknown) => void) | undefined;
    const delayedTransaction = new Promise<unknown>((resolve) => {
      resolveTransaction = resolve;
    });
    const fake = createFakeProvider({ eth_sendTransaction: () => delayedTransaction });
    registerProvider(providerDetail(fake.provider));
    renderWallet();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Test Wallet" }));
    await user.click(screen.getByRole("button", { name: "Write" }));
    await waitFor(() => {
      expect(
        fake.request.mock.calls.some(
          ([request]) => request.method === "eth_sendTransaction",
        ),
      ).toBe(true);
    });
    act(() => {
      fake.emit("accountsChanged", [accountB]);
      fake.emit("accountsChanged", [accountA]);
    });
    await act(async () => resolveTransaction?.(transactionHash));

    expect(await screen.findByTestId("operation-error")).toHaveTextContent(
      "TRANSACTION_OUTCOME_UNKNOWN",
    );
    expect(screen.getByTestId("context-error")).toHaveTextContent(
      "TRANSACTION_OUTCOME_UNKNOWN",
    );
    expect(screen.getByTestId("active-account")).toHaveTextContent(accountA);
    expect(screen.queryByText(transactionHash)).not.toBeInTheDocument();
  });
});

function WalletHarness() {
  const wallet = useWallet();
  const [result, setResult] = useState<string>();
  const [operationError, setOperationError] = useState<string>();
  return (
    <div>
      {wallet.providers.map((provider) => (
        <div key={provider.uuid}>
          <button
            type="button"
            onClick={() => {
              setOperationError(undefined);
              void wallet.connect(provider.uuid)
                .then((connected) => setResult(JSON.stringify(connected)))
                .catch((cause: unknown) => {
                  setOperationError(
                    cause instanceof WalletBoundaryError ? cause.code : "REQUEST_FAILED",
                  );
                });
            }}
          >
            {provider.name}
          </button>
          <button
            type="button"
            onClick={() => {
              setOperationError(undefined);
              void wallet.addChain(provider.uuid)
                .then(() => setResult("added"))
                .catch((cause: unknown) => {
                  setOperationError(
                    cause instanceof WalletBoundaryError ? cause.code : "REQUEST_FAILED",
                  );
                });
            }}
          >
            Add {provider.name}
          </button>
        </div>
      ))}
      {wallet.active && (
        <>
          <span data-testid="active-account">{wallet.active.account}</span>
          <span data-testid="active-chain">{wallet.active.chainID}</span>
        </>
      )}
      {wallet.error && <span data-testid="context-error">{wallet.error}</span>}
      <button
        type="button"
        onClick={() => {
          setOperationError(undefined);
          void wallet
            .readContract({ to: target, data: "0x1234" }, "1")
            .then(setResult)
            .catch((cause: unknown) => {
              setOperationError(
                cause instanceof WalletBoundaryError ? cause.code : "REQUEST_FAILED",
              );
            });
        }}
      >
        Read
      </button>
      <button
        type="button"
        onClick={() => {
          setOperationError(undefined);
          void wallet
            .sendTransaction({ to: target, data: "0x1234", value: "0xf" }, "1")
            .then(setResult)
            .catch((cause: unknown) => {
              setOperationError(
                cause instanceof WalletBoundaryError ? cause.code : "REQUEST_FAILED",
              );
            });
        }}
      >
        Write
      </button>
      <button
        type="button"
        onClick={() => {
          setOperationError(undefined);
          void wallet
            .signSIWEChallenge(walletSIWEChallenge(), wallet.active!)
            .then(setResult)
            .catch((cause: unknown) => {
              setOperationError(
                cause instanceof WalletBoundaryError ? cause.code : "REQUEST_FAILED",
              );
            });
        }}
      >
        Sign
      </button>
      <button
        type="button"
        onClick={() => {
          setOperationError(undefined);
          const challenge = walletSIWEChallenge();
          void wallet
            .signSIWEChallenge(
              {
                ...challenge,
                message: "x".repeat(MAX_SIGN_MESSAGE_BYTES + 1),
              },
              wallet.active!,
            )
            .then(setResult)
            .catch((cause: unknown) => {
              setOperationError(
                cause instanceof WalletBoundaryError ? cause.code : "REQUEST_FAILED",
              );
            });
        }}
      >
        Sign oversized
      </button>
      <button
        type="button"
        onClick={() => {
          setOperationError(undefined);
          const active = wallet.active;
          if (!active) return;
          void wallet
            .signSIWEChallenge(walletSIWEChallenge(), {
              ...active,
              revision: active.revision + 1,
            })
            .then(setResult)
            .catch((cause: unknown) => {
              setOperationError(
                cause instanceof WalletBoundaryError ? cause.code : "REQUEST_FAILED",
              );
            });
        }}
      >
        Sign with stale identity
      </button>
      {operationError && <span data-testid="operation-error">{operationError}</span>}
      {result && <output role="status">{result}</output>}
    </div>
  );
}

function renderWallet() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  queryClient.setQueryData(["config"], {
    chain_id: "1",
    chain_name: "Wallet Testnet",
    features: { user_auth: true },
    native_decimals: 18,
    native_name: "Ether",
    native_symbol: "ETH",
    wallet_add_chain: {
      chain_id: "1",
      chain_name: "Wallet Testnet",
      native_currency: { name: "Ether", symbol: "ETH", decimals: 18 },
      rpc_urls: ["http://localhost:8545"],
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <WalletProvider>
        <WalletHarness />
      </WalletProvider>
    </QueryClientProvider>,
  );
}

function walletSIWEChallenge(): AuthChallenge {
  const origin = new URL(window.location.origin);
  const expiresAt = "2099-01-01T00:05:00.000Z";
  return {
    challenge_id: walletChallengeID,
    expires_at: expiresAt,
    message:
      `${origin.protocol.slice(0, -1)}://${origin.host} wants you to sign in with your Ethereum account:\n` +
      `${accountA}\n\n\n` +
      `URI: ${origin.origin}\n` +
      "Version: 1\n" +
      "Chain ID: 1\n" +
      "Nonce: abcdefghijklmnopqrstuvwx\n" +
      "Issued At: 2026-01-01T00:00:00.000Z\n" +
      `Expiration Time: ${expiresAt}\n` +
      `Request ID: ${walletChallengeID}`,
  };
}

function providerDetail(
  provider: EIP1193Provider,
  overrides: Partial<EIP6963ProviderDetail["info"]> = {},
): EIP6963ProviderDetail {
  return {
    info: {
      uuid: providerUUID,
      name: "Test Wallet",
      icon: "data:image/png;base64,",
      rdns: "org.etherview.test",
      ...overrides,
    },
    provider,
  };
}

function registerProvider(detail: EIP6963ProviderDetail) {
  const listener: EventListener = () => announceProvider(detail);
  requestListeners.push(listener);
  window.addEventListener(EIP6963_REQUEST_EVENT, listener);
}

function announceProvider(detail: unknown) {
  window.dispatchEvent(new CustomEvent(EIP6963_ANNOUNCE_EVENT, { detail }));
}

type FakeResponse =
  | unknown
  | ((request: EIP1193RequestArguments) => unknown | Promise<unknown>);

function createFakeProvider(
  initial: Record<string, FakeResponse> = {},
  options: { ignoreRemoval?: boolean } = {},
) {
  const responses = new Map<string, FakeResponse>([
    ["eth_requestAccounts", [accountA]],
    ["eth_chainId", "0x1"],
    ["eth_accounts", [accountA]],
    ["eth_call", "0x1234"],
    ["eth_sendTransaction", transactionHash],
    ["personal_sign", walletSignature],
    ...Object.entries(initial),
  ]);
  const listeners = new Map<EIP1193Event, Set<(value: unknown) => void>>();
  const request = vi.fn(async (arguments_: EIP1193RequestArguments): Promise<unknown> => {
    if (!responses.has(arguments_.method)) {
      throw new Error(`unexpected method ${arguments_.method}`);
    }
    const response = responses.get(arguments_.method);
    return typeof response === "function" ? response(arguments_) : response;
  });
  const provider: EIP1193Provider = {
    request: request as EIP1193Provider["request"],
    on(event, listener) {
      const current = listeners.get(event) ?? new Set();
      current.add(listener);
      listeners.set(event, current);
    },
    removeListener(event, listener) {
      if (!options.ignoreRemoval) listeners.get(event)?.delete(listener);
    },
  };

  return {
    provider,
    request,
    setResponse(method: string, response: FakeResponse) {
      responses.set(method, response);
    },
    emit(event: EIP1193Event, value: unknown) {
      for (const listener of listeners.get(event) ?? []) listener(value);
    },
  };
}
