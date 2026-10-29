import {
  createRootRoute,
  createRoute,
  createRouter,
  redirect,
  type RouterHistory,
} from "@tanstack/react-router";
import { getAddress, isAddress } from "viem";

import { lazyRouteComponent } from "@/lazyRouteComponent";

import { AppShell } from "@/components/AppShell";
import { isChartMetric, type ChartSearch } from "@/pages/chartRoute";

const NamePage = lazyRouteComponent(() => import("@/pages/NamePage"), "NamePage");
const BlobPage = lazyRouteComponent(() => import("@/pages/BlobPage"), "BlobPage");
const HomePage = lazyRouteComponent(() => import("@/pages/HomePage"), "HomePage");
const BlocksPage = lazyRouteComponent(() => import("@/pages/pages"), "BlocksPage");
const GenesisPage = lazyRouteComponent(() => import("@/pages/pages"), "GenesisPage");
const NotFoundPage = lazyRouteComponent(() => import("@/pages/pages"), "NotFoundPage");
const SearchPage = lazyRouteComponent(() => import("@/pages/pages"), "SearchPage");
const StatusPage = lazyRouteComponent(() => import("@/pages/pages"), "StatusPage");
const TokensPage = lazyRouteComponent(() => import("@/pages/pages"), "TokensPage");
const TransactionsPage = lazyRouteComponent(() => import("@/pages/pages"), "TransactionsPage");
const EntityPage = lazyRouteComponent(() => import("@/pages/EntityPage"), "EntityPage");
const VerifyPage = lazyRouteComponent(() => import("@/pages/VerifyPage"), "VerifyPage");
const ChartsPage = lazyRouteComponent(() => import("@/pages/ChartsPages"), "ChartsPage");
const ChartMetricPage = lazyRouteComponent(() => import("@/pages/ChartsPages"), "ChartMetricPage");
const PendingPage = lazyRouteComponent(() => import("@/pages/PendingPage"), "PendingPage");
const UserOperationsPage = lazyRouteComponent(
  () => import("@/pages/UserOperationPages"),
  "UserOperationsPage",
);
const UserOperationDetailPage = lazyRouteComponent(
  () => import("@/pages/UserOperationPages"),
  "UserOperationDetailPage",
);
const AccountPage = lazyRouteComponent(() => import("@/pages/AuthPages"), "AccountPage");
const AdminUsersPage = lazyRouteComponent(() => import("@/pages/AuthPages"), "AdminUsersPage");
const AdminBillingPage = lazyRouteComponent(
  () => import("@/pages/BillingPages"),
  "AdminBillingPage",
);

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
  validateSearch: (
    search: Record<string, unknown>,
  ): {
    tab?: "overview" | "transactions" | "withdrawals" | "consensus" | "requests" | "blobs";
  } => ({
    tab:
      typeof search.tab === "string" &&
      ["overview", "transactions", "withdrawals", "consensus", "requests", "blobs"].includes(
        search.tab,
      )
        ? (search.tab as
            | "overview"
            | "transactions"
            | "withdrawals"
            | "consensus"
            | "requests"
            | "blobs")
        : undefined,
  }),
  component: BlockRoutePage,
});
const blockAliasRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/block/$blockID",
  validateSearch: blockRoute.options.validateSearch,
  beforeLoad: ({ params, search, location }) => {
    throw redirect({
      to: "/blocks/$blockID",
      params,
      search,
      hash: location.hash,
      replace: true,
    });
  },
});
const blobRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/blocks/$blockHash/blobs/$index",
  component: BlobRoutePage,
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
const userOperationsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/user-operations",
  component: UserOperationsPage,
});
const userOperationRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/user-op/$hash",
  component: UserOperationRoutePage,
});
const transactionRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tx/$hash",
  validateSearch: (search: Record<string, unknown>) => {
    const tab = typeof search.tab === "string" ? search.tab : "overview";
    return {
      tab: [
        "overview",
        "access-list",
        "blob",
        "authorizations",
        "user-operations",
        "native-transfers",
        "internal-transactions",
        "token-transfers",
        "logs",
        "trace",
        "state-changes",
      ].includes(tab)
        ? tab
        : "overview",
    };
  },
  component: TransactionRoutePage,
});
const nameRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/name/$name",
  component: NameRoutePage,
});
const addressRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/address/$address",
  validateSearch: (search: Record<string, unknown>): { tab?: string } => {
    const tab = typeof search.tab === "string" ? search.tab : "transactions";
    if (tab === "transactions") return {};
    return [
      "native-transfers",
      "internal-transactions",
      "withdrawals",
      "erc20-transfers",
      "nft-transfers",
      "assets",
      "user-operations",
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
  validateSearch: (search: Record<string, unknown>): { address?: string; job?: string } => ({
    job:
      typeof search.job === "string" &&
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/iu.test(search.job)
        ? search.job.toLowerCase()
        : undefined,
    address:
      typeof search.address === "string" && isAddress(search.address)
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
    const range =
      typeof search.range === "string" && ranges.includes(search.range)
        ? (search.range as ChartSearch["range"])
        : "7d";
    const interval =
      typeof search.interval === "string" && intervals.includes(search.interval)
        ? (search.interval as ChartSearch["interval"])
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
  validateSearch: (
    search: Record<string, unknown>,
  ): { tab?: "overview" | "api-keys" | "billing" | "watchlist" } => ({
    tab:
      search.tab === "watchlist" || search.tab === "api-keys" || search.tab === "billing"
        ? search.tab
        : undefined,
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
  blockAliasRoute,
  blobRoute,
  genesisRoute,
  transactionsRoute,
  transactionRoute,
  userOperationsRoute,
  userOperationRoute,
  addressRoute,
  nameRoute,
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

function BlobRoutePage() {
  const { blockHash, index } = blobRoute.useParams();
  if (!/^0x[0-9a-fA-F]{64}$/.test(blockHash) || !/^(0|[1-9][0-9]{0,19})$/.test(index))
    return <NotFoundPage />;
  return <BlobPage key={`${blockHash}:${index}`} hash={blockHash} index={index} />;
}

function TransactionRoutePage() {
  const { hash } = transactionRoute.useParams();
  const { tab } = transactionRoute.useSearch();
  return <EntityPage kind="transaction" identifier={hash} transactionTab={tab} />;
}

function UserOperationRoutePage() {
  const { hash } = userOperationRoute.useParams();
  return <UserOperationDetailPage hash={hash} />;
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
  const { address, job } = verifyRoute.useSearch();
  const navigate = verifyRoute.useNavigate();
  return (
    <VerifyPage
      initialAddress={address}
      initialJob={job}
      onJobChange={(id) => void navigate({ search: { address, job: id || undefined } })}
    />
  );
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

function NameRoutePage() {
  const { name } = nameRoute.useParams();
  return <NamePage name={name} />;
}
