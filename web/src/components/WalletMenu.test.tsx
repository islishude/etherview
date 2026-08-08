import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { useAuth } from "@/auth/AuthProvider";
import { useWallet } from "@/wallet/WalletProvider";
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

describe("WalletMenu", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    await i18n.changeLanguage("en");
    vi.mocked(useWallet).mockReturnValue({
      providers: [{
        uuid: "00000000-0000-4000-8000-000000000001",
        name: "Test Wallet",
        rdns: "org.etherview.test",
      }],
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
    const summary = screen.getByText("Connect wallet", { selector: "summary" });
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
    const summary = screen.getByText("Connect wallet", { selector: "summary" });
    const menu = summary.parentElement as HTMLDetailsElement;

    await user.click(summary);
    await user.click(screen.getByRole("button", { name: /Test Wallet/u }));

    expect(menu).toHaveAttribute("open", "");
  });
});
