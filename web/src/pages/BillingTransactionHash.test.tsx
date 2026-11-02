import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { shorten } from "@/components/format";
import { BillingTransactionHash } from "./BillingTransactionHash";

const config = vi.hoisted(() => ({ chainID: "1" as string | undefined }));
vi.mock("@/api/publicConfig", () => ({
  usePublicConfig: () => ({ data: config.chainID ? { chain_id: config.chainID } : undefined }),
}));
const hash = `0x${"12345678".repeat(8)}`;

beforeEach(async () => {
  config.chainID = "1";
  await i18n.changeLanguage("en");
});

async function setup(network?: string, value: string | undefined = hash) {
  const user = userEvent.setup();
  const route = createRootRoute({
    component: () => <BillingTransactionHash hash={value} network={network} />,
  });
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  render(<RouterProvider router={router} />);
  await waitFor(() => expect(screen.getByText(value ? shorten(value) : "—")).toBeVisible());
  return user;
}

it.each(["en", "zh"])(
  "links same-chain hashes and copies the complete value in %s",
  async (language) => {
    await i18n.changeLanguage(language);
    const user = await setup("eip155:1");
    const link = screen.getByRole("link", { name: shorten(hash) });
    expect(link).toHaveAttribute("href", `/tx/${hash}?tab=overview`);
    expect(screen.getByTitle(hash)).toHaveTextContent(shorten(hash));
    await user.tab();
    expect(link).toHaveFocus();
    await user.tab();
    expect(screen.getByRole("button")).toHaveFocus();
    const copy = vi.spyOn(navigator.clipboard, "writeText");
    await user.keyboard("{Enter}");
    expect(copy).toHaveBeenCalledWith(hash);
  },
);

it.each(["eip155:2", undefined, "unknown:1"])("does not link network %s", async (network) => {
  await setup(network);
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
});

it("waits for known site chain identity before linking", async () => {
  config.chainID = undefined;
  await setup("eip155:1");
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
});

it("preserves the missing hash placeholder without a copy button", async () => {
  await setup("eip155:1", "");
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  expect(screen.queryByRole("button")).not.toBeInTheDocument();
});
