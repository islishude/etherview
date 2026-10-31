import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import "@/i18n/resources/billingPrices";
import { BillingWalletConnect } from "./BillingWalletConnect";

const wallet = vi.hoisted(() => ({
  providers: [] as { uuid: string; name: string }[],
  connect: vi.fn(),
  discover: vi.fn(),
  connecting: false,
  error: undefined as string | undefined,
}));
vi.mock("@/wallet/WalletProvider", () => ({ useWallet: () => wallet }));
beforeEach(async () => {
  vi.resetAllMocks();
  wallet.providers = [];
  wallet.connecting = false;
  wallet.error = undefined;
  wallet.connect.mockResolvedValue(undefined);
  await i18n.changeLanguage("en");
});
it("lets users choose a provider explicitly", async () => {
  wallet.providers = [
    { uuid: "one", name: "First wallet" },
    { uuid: "two", name: "Second wallet" },
  ];
  render(<BillingWalletConnect />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Connect wallet" }));
  expect(wallet.connect).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "Second wallet" }));
  expect(wallet.connect).toHaveBeenCalledExactlyOnceWith("two");
});
it("offers discovery and installation guidance when no provider is detected", async () => {
  render(<BillingWalletConnect />);
  await userEvent.click(screen.getByRole("button", { name: "Connect wallet" }));
  expect(wallet.discover).toHaveBeenCalledOnce();
  expect(screen.getByText(i18n.t("wallet.installHint"))).toBeVisible();
  expect(wallet.connect).not.toHaveBeenCalled();
});
it("keeps rejected connections retryable and uses sanitized provider errors", async () => {
  wallet.providers = [{ uuid: "one", name: "First wallet" }];
  wallet.connect.mockRejectedValue(new Error("private provider error"));
  const { rerender } = render(<BillingWalletConnect />);
  await userEvent.click(screen.getByRole("button", { name: "Connect wallet" }));
  wallet.error = "USER_REJECTED";
  rerender(<BillingWalletConnect />);
  expect(screen.getByRole("alert")).toBeVisible();
  expect(document.body).not.toHaveTextContent("private provider error");
  expect(screen.getByRole("button", { name: "Connect wallet" })).toBeEnabled();
});
