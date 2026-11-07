import "@/i18n/admin";
import { render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { AccountWalletNotice } from "./AccountWalletNotice";

const fixture = vi.hoisted(() => ({
  active: undefined as { account: string; chainID: string } | undefined,
  authenticated: true,
  loading: false,
}));
const address = "0x1111111111111111111111111111111111111111";
vi.mock("@/auth/AuthProvider", () => ({
  useAuth: () => ({
    loading: fixture.loading,
    session: {
      authenticated: fixture.authenticated,
      user: fixture.authenticated ? { address } : undefined,
    },
  }),
}));
vi.mock("@/wallet/WalletProvider", () => ({ useWallet: () => ({ active: fixture.active }) }));
vi.mock("@/api/publicConfig", () => ({ usePublicConfig: () => ({ data: { chain_id: "1" } }) }));
beforeEach(async () => {
  fixture.active = undefined;
  fixture.authenticated = true;
  fixture.loading = false;
  await i18n.changeLanguage("en");
});
it("does not display wallet or user identity for a valid disconnected session", () => {
  const { container } = render(<AccountWalletNotice />);
  expect(container).toBeEmptyDOMElement();
});
it("keeps an actionable wrong-network warning outside Overview before login", () => {
  fixture.active = { account: address, chainID: "0x2" };
  fixture.authenticated = false;
  render(<AccountWalletNotice />);
  expect(screen.getByRole("status")).toHaveTextContent("Switch to configured chain 1");
  expect(screen.queryByRole("region")).not.toBeInTheDocument();
});
it("does not warn about a payment-network switch with a matching valid identity", () => {
  fixture.active = { account: address, chainID: "0x2" };
  render(<AccountWalletNotice />);
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
});
it("reports a connected identity mismatch without showing the identity summary on other tabs", () => {
  fixture.active = { account: "0x2222222222222222222222222222222222222222", chainID: "0x1" };
  render(<AccountWalletNotice />);
  expect(screen.getByRole("status")).toHaveTextContent("differs from your signed-in identity");
  expect(screen.queryByRole("region")).not.toBeInTheDocument();
});
it("suppresses network notices until authentication resolves", () => {
  fixture.authenticated = false;
  fixture.loading = true;
  fixture.active = { account: address, chainID: "0x2" };
  const { container } = render(<AccountWalletNotice />);
  expect(container).toBeEmptyDOMElement();
});
