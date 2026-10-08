import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { I18nextProvider } from "react-i18next";
import { AddressIdentity } from "./AddressIdentity";
import { AddressNamesProvider } from "./AddressNamesProvider";

const address = "0x1111111111111111111111111111111111111111";
const secondAddress = "0x2222222222222222222222222222222222222222";

afterEach(() => {
  vi.unstubAllGlobals();
});

it("renders a snapshot-resolved custom primary name while retaining the exact address", async () => {
  const requestedSnapshots: Array<string | null> = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/config") {
      return jsonResponse({
        data: {
          chain_id: "1",
          chain_name: "Ethereum",
          native_symbol: "ETH",
          native_name: "Ether",
          native_decimals: 18,
          features: { ens: true },
        },
        meta: { request_id: "config", chain_id: "1" },
      });
    }
    if (url.pathname === "/api/v1/address-names") {
      requestedSnapshots.push(url.searchParams.get("snapshot"));
      const requested = (url.searchParams.get("addresses") ?? "").split(",");
      return jsonResponse({
        data: {
          snapshot: "ens-snapshot",
          items: requested.map((value) =>
            value === address.toLowerCase()
              ? {
                  address,
                  state: "resolved",
                  primary_name: { name: "alice.custom", source: "custom_ens" },
                }
              : { address: value, state: "not_found" },
          ),
        },
        meta: { request_id: "names", chain_id: "1" },
      });
    }
    return jsonResponse(
      { error: { code: "not_found", message: "missing", request_id: "missing" } },
      404,
    );
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <AddressNamesProvider>
        <DynamicAddresses />
      </AddressNamesProvider>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("alice.custom")).toHaveAttribute("title", "alice.custom");
  expect(screen.getByText("Custom ENS")).toBeVisible();
  expect(screen.getByTitle(address)).toBeVisible();
  await userEvent.setup().click(screen.getByRole("button", { name: "Show second" }));
  expect(await screen.findByTitle(secondAddress)).toBeVisible();
  await vi.waitFor(() => {
    expect(requestedSnapshots).toEqual([null, "ens-snapshot"]);
  });
});

function DynamicAddresses() {
  const [second, setSecond] = useState(false);
  return (
    <>
      <AddressIdentity address={address} link={false} />
      <button onClick={() => setSecond(true)} type="button">
        Show second
      </button>
      {second ? <AddressIdentity address={secondAddress} link={false} /> : null}
    </>
  );
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

it.each(["en", "zh"])(
  "labels the exact system address in %s and copies the address",
  async (language) => {
    await i18n.changeLanguage(language);
    const user = userEvent.setup();
    const system = "0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFE";
    const { rerender } = render(
      <I18nextProvider i18n={i18n}>
        <AddressIdentity address={system} link={false} copy compact={false} />
      </I18nextProvider>,
    );
    expect(screen.getByText(language === "en" ? "System Address" : "系统地址")).toBeVisible();
    expect(screen.getByTitle(system)).toBeVisible();
    await user.click(screen.getByRole("button"));
    expect(await navigator.clipboard.readText()).toBe(system);
    rerender(
      <I18nextProvider i18n={i18n}>
        <AddressIdentity address="0xffffffffffffffffffffffffffffffffffffffff" link={false} />
      </I18nextProvider>,
    );
    expect(screen.queryByText(language === "en" ? "System Address" : "系统地址")).toBeNull();
    await i18n.changeLanguage("en");
  },
);
