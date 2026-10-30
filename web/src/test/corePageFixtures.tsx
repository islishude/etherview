import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";

import { makeRouter } from "@/router";
import { AuthProvider } from "@/auth/AuthProvider";
import { ThemeProvider } from "@/theme/ThemeProvider";
import { WalletProvider } from "@/wallet/WalletProvider";

export function configResponse() {
  return envelope({
    chain_id: "1",
    chain_name: "Core Testnet",
    native_symbol: "ETH",
    native_name: "Ether",
    native_decimals: 18,
    features: {},
  });
}

export function completeness() {
  return {
    core: "complete",
    trace: "unavailable",
    metadata: "pending",
    state: "complete",
    user_operations: "unavailable",
  };
}

export function envelope(data: unknown, meta: Record<string, unknown> = {}) {
  const responseData = includedTransactionFixture(data)
    ? { kind: "included", transaction: data }
    : data;
  return Response.json({
    data: responseData,
    meta: { request_id: "core-pages-test", chain_id: "1", ...meta },
  });
}

function includedTransactionFixture(data: unknown): data is Record<string, unknown> {
  if (!data || Array.isArray(data) || typeof data !== "object") return false;
  const candidate = data as Record<string, unknown>;
  return (
    typeof candidate.hash === "string" &&
    typeof candidate.from === "string" &&
    typeof candidate.nonce === "string" &&
    typeof candidate.gas === "string" &&
    typeof candidate.input === "string" &&
    typeof candidate.canonical === "boolean" &&
    typeof candidate.finality === "string"
  );
}

export function notFound() {
  return Response.json(
    {
      error: { code: "not_found", message: "not found", request_id: "core-pages-test" },
    },
    { status: 404 },
  );
}

export function requestURL(input: RequestInfo | URL) {
  return new URL(String(input), "http://etherview.test");
}

export function renderExplorer(path: string, children?: ReactNode) {
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
  });
  const view = render(
    <QueryClientProvider client={queryClient}>
      {children}
      <ThemeProvider>
        <WalletProvider>
          <AuthProvider>
            <RouterProvider router={router} />
          </AuthProvider>
        </WalletProvider>
      </ThemeProvider>
    </QueryClientProvider>,
  );
  return { ...view, router };
}
