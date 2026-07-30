import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { usePublicConfig } from "@/api/hooks";
import i18n from "@/i18n";
import { WalletBoundaryError } from "@/wallet/eip6963";
import { useWallet } from "@/wallet/WalletProvider";
import { AddNetworkControl } from "./AddNetworkControl";

vi.mock("@/api/hooks", () => ({
  usePublicConfig: vi.fn(),
}));

vi.mock("@/wallet/WalletProvider", () => ({
  useWallet: vi.fn(),
}));

type WalletState = ReturnType<typeof useWallet>;

const providerA = {
  uuid: "00000000-0000-4000-8000-000000000001",
  name: "Alpha Wallet",
  rdns: "org.etherview.alpha",
};
const providerB = {
  uuid: "00000000-0000-4000-8000-000000000002",
  name: "Beta Wallet",
  rdns: "org.etherview.beta",
};

describe("AddNetworkControl", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    await i18n.changeLanguage("en");
    mockConfig(true);
    vi.mocked(useWallet).mockReturnValue(walletState());
  });

  it("renders only for configured wallet metadata and disables without a provider", () => {
    mockConfig(false);
    const { rerender } = render(<AddNetworkControl menuOpen />);
    expect(screen.queryByRole("region", { name: "Chain" })).not.toBeInTheDocument();

    mockConfig(true);
    rerender(<AddNetworkControl menuOpen />);
    expect(screen.getByRole("button", { name: "Add Wallet Testnet network" }))
      .toBeDisabled();
    expect(screen.queryByText("Install or unlock a browser wallet, then refresh discovery."))
      .not.toBeInTheDocument();

    vi.mocked(useWallet).mockReturnValue(walletState({
      addingChain: true,
      providers: [providerA],
    }));
    rerender(<AddNetworkControl menuOpen />);
    expect(screen.getByRole("button", { name: "Add Wallet Testnet network" }))
      .toBeDisabled();
  });

  it("adds through the sole discovered provider without connecting an account", async () => {
    const addChain = vi.fn(async (_uuid: string) => {});
    const connect = vi.fn(async (_uuid: string) => activeWallet());
    vi.mocked(useWallet).mockReturnValue(walletState({
      addChain,
      connect,
      providers: [providerA],
    }));

    render(<AddNetworkControl menuOpen />);
    await userEvent.setup().click(
      screen.getByRole("button", { name: "Add Wallet Testnet network" }),
    );

    await waitFor(() => expect(addChain).toHaveBeenCalledWith(providerA.uuid));
    expect(connect).not.toHaveBeenCalled();
    expect(await screen.findByText("Wallet Testnet was added to the wallet."))
      .toBeVisible();
  });

  it("prefers the active wallet when several providers are available", async () => {
    const addChain = vi.fn(async (_uuid: string) => {});
    vi.mocked(useWallet).mockReturnValue(walletState({
      active: {
        uuid: providerB.uuid,
        name: providerB.name,
        account: "0x1111111111111111111111111111111111111111",
        chainID: "1",
        revision: 1,
      },
      addChain,
      providers: [providerA, providerB],
    }));

    render(<AddNetworkControl menuOpen />);
    await userEvent.setup().click(
      screen.getByRole("button", { name: "Add Wallet Testnet network" }),
    );

    await waitFor(() => expect(addChain).toHaveBeenCalledWith(providerB.uuid));
    expect(
      screen.queryByRole("group", {
        name: "Choose a wallet to add Wallet Testnet",
      }),
    ).not.toBeInTheDocument();
  });

  it("chooses among multiple disconnected wallets inline and resets when the menu closes", async () => {
    const addChain = vi.fn(async (_uuid: string) => {});
    vi.mocked(useWallet).mockReturnValue(walletState({
      addChain,
      providers: [providerA, providerB],
    }));

    const { rerender } = render(<AddNetworkControl menuOpen />);
    const user = userEvent.setup();
    await user.click(
      screen.getByRole("button", { name: "Add Wallet Testnet network" }),
    );
    const chooser = screen.getByRole("group", {
      name: "Choose a wallet to add Wallet Testnet",
    });
    expect(chooser).toBeVisible();
    expect(addChain).not.toHaveBeenCalled();

    rerender(<AddNetworkControl menuOpen={false} />);
    await waitFor(() => expect(chooser).not.toBeInTheDocument());
    rerender(<AddNetworkControl menuOpen />);
    expect(
      screen.queryByRole("group", {
        name: "Choose a wallet to add Wallet Testnet",
      }),
    ).not.toBeInTheDocument();

    await user.click(
      screen.getByRole("button", { name: "Add Wallet Testnet network" }),
    );
    await user.click(screen.getByRole("button", { name: /Beta Wallet/u }));
    await waitFor(() => expect(addChain).toHaveBeenCalledWith(providerB.uuid));
  });

  it("shows bounded wallet errors locally and clears them when the menu closes", async () => {
    const addChain = vi.fn(async (_uuid: string) => {
      throw new WalletBoundaryError("USER_REJECTED");
    });
    vi.mocked(useWallet).mockReturnValue(walletState({
      addChain,
      providers: [providerA],
    }));

    const { rerender } = render(<AddNetworkControl menuOpen />);
    await userEvent.setup().click(
      screen.getByRole("button", { name: "Add Wallet Testnet network" }),
    );
    expect(await screen.findByText("The wallet request was rejected.")).toBeVisible();

    rerender(<AddNetworkControl menuOpen={false} />);
    await waitFor(() => {
      expect(screen.queryByText("The wallet request was rejected."))
        .not.toBeInTheDocument();
    });
  });
});

function mockConfig(configured: boolean) {
  vi.mocked(usePublicConfig).mockReturnValue({
    data: {
      chain_id: "1",
      chain_name: "Wallet Testnet",
      features: {},
      native_decimals: 18,
      native_name: "Ether",
      native_symbol: "ETH",
      wallet_add_chain: configured
        ? {
            chain_id: "1",
            chain_name: "Wallet Testnet",
            native_currency: {
              decimals: 18,
              name: "Ether",
              symbol: "ETH",
            },
            rpc_urls: ["http://localhost:8545"],
          }
        : undefined,
    },
    isPending: false,
  } as ReturnType<typeof usePublicConfig>);
}

function walletState(overrides: Partial<WalletState> = {}): WalletState {
  return {
    active: undefined,
    addChain: vi.fn(async (_uuid: string) => {}),
    addingChain: false,
    connect: vi.fn(async (_uuid: string) => activeWallet()),
    connecting: false,
    discover: vi.fn(),
    disconnect: vi.fn(),
    error: undefined,
    isActiveWallet: vi.fn(() => false),
    providers: [],
    readContract: vi.fn(async () => "0x" as const),
    sendTransaction: vi.fn(async () => "0x" as const),
    signSIWEChallenge: vi.fn(async () => "0x" as const),
    ...overrides,
  };
}

function activeWallet() {
  return {
    uuid: providerA.uuid,
    name: providerA.name,
    account: "0x1111111111111111111111111111111111111111" as const,
    chainID: "1",
    revision: 1,
  };
}
