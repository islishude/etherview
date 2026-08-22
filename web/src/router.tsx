import {
  createRootRoute,
  createRoute,
  createRouter,
  type RouterHistory,
} from "@tanstack/react-router";
import { getAddress, isAddress } from "viem";

import { AppShell } from "@/components/AppShell";
import {
  BlocksPage,
  GenesisPage,
  HomePage,
  NotFoundPage,
  SearchPage,
  StatusPage,
  TokensPage,
  TransactionsPage,
} from "@/pages/pages";
import { EntityPage } from "@/pages/EntityPage";
import { VerifyPage } from "@/pages/VerifyPage";
import {
  ChartMetricPage,
  ChartsPage,
  isChartMetric,
  type ChartSearch,
} from "@/pages/ChartsPages";
import { PendingPage } from "@/pages/PendingPage";
import { AccountPage, AdminUsersPage } from "@/pages/AuthPages";
import { AdminBillingPage } from "@/pages/BillingPages";

const rootRoute = createRootRoute({
  component: AppShell,
  notFoundComponent: NotFoundPage,
});

const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: HomePage });
const blocksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/blocks",
  component: BlocksPage,
});
const blockRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/blocks/$blockID",
  validateSearch: (search: Record<string, unknown>): { tab?: "overview" | "transactions" | "withdrawals" } => ({
    tab: typeof search.tab === "string" && ["overview", "transactions", "withdrawals"].includes(search.tab)
      ? search.tab as "overview" | "transactions" | "withdrawals"
      : undefined,
  }),
  component: BlockRoutePage,
});
const genesisRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/genesis",
  component: GenesisPage,
});
const transactionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/transactions",
  component: TransactionsPage,
});
const transactionRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tx/$hash",
  validateSearch: (search: Record<string, unknown>) => {
    const tab = typeof search.tab === "string" ? search.tab : "overview";
    return {
      tab: ["overview", "access-list", "blob", "authorizations", "internal-transactions", "token-transfers", "logs", "trace", "state-changes"].includes(tab)
        ? tab
        : "overview",
    };
  },
  component: TransactionRoutePage,
});
const addressRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/address/$address",
  validateSearch: (search: Record<string, unknown>): { tab?: string } => {
    const tab = typeof search.tab === "string" ? search.tab : "transactions";
    if (tab === "transactions") return {};
    return [
      "internal-transactions",
      "withdrawals",
      "erc20-transfers",
      "nft-transfers",
      "assets",
      "delegation",
    ].includes(tab)
      ? { tab }
      : {};
  },
  component: AddressRoutePage,
});
const tokensRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tokens",
  component: TokensPage,
});
const tokenRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/token/$address",
  component: TokenRoutePage,
});
const nftRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/nft/$address/$tokenID",
  component: NFTRoutePage,
});
const verifyRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/verify",
  validateSearch: (search: Record<string, unknown>): { address?: string } => ({
    address: typeof search.address === "string" && isAddress(search.address)
      ? getAddress(search.address)
      : undefined,
  }),
  component: VerifyRoutePage,
});
const chartsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/charts",
  component: ChartsPage,
});
const chartMetricRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/charts/$metric",
  validateSearch: (search: Record<string, unknown>): ChartSearch => {
    const ranges = ["24h", "7d", "30d", "90d", "1y", "all", "custom"];
    const intervals = ["auto", "hour", "day", "week", "month"];
    const range = typeof search.range === "string" && ranges.includes(search.range)
      ? search.range as ChartSearch["range"]
      : "7d";
    const interval = typeof search.interval === "string" && intervals.includes(search.interval)
      ? search.interval as ChartSearch["interval"]
      : "auto";
    return {
      range,
      interval,
      from_time: typeof search.from_time === "string" ? search.from_time : undefined,
      to_time: typeof search.to_time === "string" ? search.to_time : undefined,
    };
  },
  component: ChartMetricRoutePage,
});
const pendingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/pending",
  component: PendingPage,
});
const statusRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/status",
  component: StatusPage,
});
const accountRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/account",
  validateSearch: (search: Record<string, unknown>): { tab?: "overview" | "api-keys" | "billing" } => ({
    tab: search.tab === "api-keys" || search.tab === "billing" ? search.tab : undefined,
  }),
  component: AccountRoutePage,
});
const adminUsersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/admin/users",
  component: AdminUsersPage,
});
const adminBillingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/admin/billing",
  component: AdminBillingPage,
});
const searchRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/search",
  validateSearch: (search: Record<string, unknown>) => ({
    q: typeof search.q === "string" ? search.q : "",
  }),
  component: SearchRoutePage,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  blocksRoute,
  blockRoute,
  genesisRoute,
  transactionsRoute,
  transactionRoute,
  addressRoute,
  tokensRoute,
  tokenRoute,
  nftRoute,
  verifyRoute,
  chartsRoute,
  chartMetricRoute,
  pendingRoute,
  statusRoute,
  accountRoute,
  adminUsersRoute,
  adminBillingRoute,
  searchRoute,
]);

function BlockRoutePage() {
  const { blockID } = blockRoute.useParams();
  const { tab } = blockRoute.useSearch();
  return <EntityPage kind="block" identifier={blockID} blockTab={tab} />;
}

function TransactionRoutePage() {
  const { hash } = transactionRoute.useParams();
  const { tab } = transactionRoute.useSearch();
  return <EntityPage kind="transaction" identifier={hash} transactionTab={tab} />;
}

function AddressRoutePage() {
  const { address } = addressRoute.useParams();
  const { tab } = addressRoute.useSearch();
  return <EntityPage kind="address" identifier={address} addressTab={tab ?? "transactions"} />;
}

function TokenRoutePage() {
  const { address } = tokenRoute.useParams();
  return <EntityPage kind="token" identifier={address} />;
}

function NFTRoutePage() {
  const { address, tokenID } = nftRoute.useParams();
  return <EntityPage kind="nft" identifier={address} secondary={tokenID} />;
}

function AccountRoutePage() {
  const { tab } = accountRoute.useSearch();
  return <AccountPage tab={tab ?? "overview"} />;
}

function VerifyRoutePage() {
  const { address } = verifyRoute.useSearch();
  return <VerifyPage initialAddress={address} />;
}

function SearchRoutePage() {
  const { q } = searchRoute.useSearch();
  return <SearchPage query={q} />;
}

function ChartMetricRoutePage() {
  const { metric } = chartMetricRoute.useParams();
  const search = chartMetricRoute.useSearch();
  const navigate = chartMetricRoute.useNavigate();
  if (!isChartMetric(metric)) return <NotFoundPage />;
  return (
    <ChartMetricPage
      metric={metric}
      search={search}
      updateSearch={(next) => void navigate({ search: next })}
    />
  );
}

export function makeRouter(history?: RouterHistory) {
  return createRouter({
    routeTree,
    history,
    defaultPreload: "intent",
    scrollRestoration: true,
  });
}

export const router = makeRouter();

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
