import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { useAuth } from "@/auth/AuthProvider";
import { useWallet } from "@/wallet/WalletProvider";
import { usePrimaryName } from "@/ens/AddressNamesProvider";
import { WalletMenu } from "./WalletMenu";

vi.mock("@/auth/AuthProvider", () => ({
  useAuth: vi.fn(),
}));

vi.mock("@/wallet/WalletProvider", () => ({
  useWallet: vi.fn(),
}));

vi.mock("./AddNetworkControl", () => ({
  AddNetworkControl: () => null,
}));

vi.mock("./SIWELoginControl", () => ({
  SIWELoginControl: () => null,
}));

vi.mock("@/ens/AddressNamesProvider", () => ({ usePrimaryName: vi.fn() }));

describe("WalletMenu", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    await i18n.changeLanguage("en");
    vi.mocked(useWallet).mockReturnValue({
      providers: [
        {
          uuid: "00000000-0000-4000-8000-000000000001",
          name: "Test Wallet",
          rdns: "org.etherview.test",
        },
      ],
      prepareLogin: vi.fn(async (selected) => selected),
      connecting: false,
      addingChain: false,
      discover: vi.fn(),
      connect: vi.fn(async () => {
        throw new Error("test connection failure");
      }),
      addChain: vi.fn(),
      disconnect: vi.fn(),
      getActiveWallet: vi.fn(),
      isActiveWallet: vi.fn(),
      readContract: vi.fn(),
      sendTransaction: vi.fn(),
      signSIWEChallenge: vi.fn(),
    });
    vi.mocked(useAuth).mockReturnValue({
      enabled: false,
      loading: false,
      pending: false,
      session: { authenticated: false },
      refresh: vi.fn(),
      login: vi.fn(),
      logout: vi.fn(),
      updateDisplayName: vi.fn(),
      updateUser: vi.fn(),
      revokeSessions: vi.fn(),
      clearError: vi.fn(),
    });
  });

  it("closes when a pointer is pressed outside the menu", async () => {
    render(
      <>
        <WalletMenu />
        <button type="button">Outside</button>
      </>,
    );
    const user = userEvent.setup();
    const summary = screen.getByLabelText("Connect wallet", { selector: "summary" });
    const menu = summary.parentElement as HTMLDetailsElement;

    await user.click(summary);
    expect(menu).toHaveAttribute("open", "");
    expect(screen.getByText("Injected wallet")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Outside" }));
    expect(menu).not.toHaveAttribute("open");
    expect(screen.getByText("Injected wallet")).not.toBeVisible();
  });

  it("keeps the popover open for interactions inside the menu", async () => {
    render(<WalletMenu />);
    const user = userEvent.setup();
    const summary = screen.getByLabelText("Connect wallet", { selector: "summary" });
    const menu = summary.parentElement as HTMLDetailsElement;

    await user.click(summary);
    await user.click(screen.getByRole("button", { name: /Test Wallet/u }));

    expect(menu).toHaveAttribute("open", "");
  });

  it.each([
    { displayName: "Alice", ens: "alice.eth", label: "Alice" },
    { displayName: null, ens: "alice.eth", label: "alice.eth" },
    { displayName: null, ens: undefined, label: "0x2222…2222" },
  ])("links each identity to its own address with $label", async ({ displayName, ens, label }) => {
    const walletAddress = "0x1111111111111111111111111111111111111111";
    const userAddress = "0x2222222222222222222222222222222222222222";
    vi.mocked(useWallet).mockReturnValue({
      ...useWallet(),
      active: {
        account: walletAddress,
        chainID: "1",
        uuid: "test",
        revision: 1,
        name: "Test Wallet",
      },
    });
    vi.mocked(useAuth).mockReturnValue({
      ...useAuth(),
      enabled: true,
      session: {
        authenticated: true,
        user: {
          id: "test",
          address: userAddress,
          chain_id: "1",
          role: "user",
          status: "active",
          display_name: displayName,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        },
      },
    });
    vi.mocked(usePrimaryName).mockImplementation((address) =>
      address === userAddress && ens
        ? {
            name: ens,
            publication: "1",
            observed_block_number: "1",
            observed_block_hash: `0x${"a".repeat(64)}`,
            valid_until: "2099-01-01T00:00:00Z",
          }
        : undefined,
    );
    const root = createRootRoute({ component: WalletMenu });
    const router = createRouter({
      routeTree: root.addChildren([
        createRoute({ getParentRoute: () => root, path: "/" }),
        createRoute({ getParentRoute: () => root, path: "/address/$address" }),
      ]),
      history: createMemoryHistory({ initialEntries: ["/"] }),
    });
    render(<RouterProvider router={router} />);
    const summary = await screen.findByLabelText(/Test Wallet/u, { selector: "summary" });
    const menu = summary.parentElement as HTMLDetailsElement;
    const user = userEvent.setup();
    await user.click(summary);
    const walletLink = screen.getByRole("link", { name: walletAddress });
    expect(walletLink).toHaveAttribute("href", `/address/${walletAddress}`);
    const identityLink = screen.getByRole("link", {
      name: new RegExp(label.replace(".", "\\."), "u"),
    });
    expect(identityLink).toHaveAttribute("href", `/address/${userAddress}`);
    identityLink.focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(router.state.location.pathname).toBe(`/address/${userAddress}`));
    expect(menu).not.toHaveAttribute("open");
    await user.click(summary);
    await user.click(screen.getByRole("link", { name: walletAddress }));
    await waitFor(() => expect(router.state.location.pathname).toBe(`/address/${walletAddress}`));
    expect(menu).not.toHaveAttribute("open");
  });
});
