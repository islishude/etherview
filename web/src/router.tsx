import {
  createRootRoute,
  createRoute,
  createRouter,
  defaultParseSearch,
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
const TokensPage = lazyRouteComponent(() => import("@/pages/pages"), "TokensPage");
const TransactionsPage = lazyRouteComponent(() => import("@/pages/pages"), "TransactionsPage");
const BlockDetailPage = lazyRouteComponent(() => import("@/pages/BlockPage"), "BlockDetailPage");
const TransactionDetailPage = lazyRouteComponent(
  () => import("@/pages/TransactionPage"),
  "TransactionDetailPage",
);
const AddressDetailPage = lazyRouteComponent(
  () => import("@/pages/AddressPage"),
  "AddressDetailPage",
);
const TokenDetailPage = lazyRouteComponent(() => import("@/pages/TokenPage"), "TokenDetailPage");
const NFTDetailPage = lazyRouteComponent(() => import("@/pages/NFTPage"), "NFTDetailPage");
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
const AddressExportPage = lazyRouteComponent(
  () => import("@/pages/AddressActionPages"),
  "AddressExportPage",
);
const AccountPage = lazyRouteComponent(() => import("@/pages/AuthPages"), "AccountPage");

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
  validateSearch: (
    search: Record<string, unknown>,
  ): {
    tab?: string;
    asset?: "tokens" | "nfts" | "defi";
    log_mode?: "name" | "topic";
    event_signature?: string;
    event_id?: string;
    topic0?: string;
    topic1?: string;
    topic2?: string;
    topic3?: string;
    from_block?: string;
    to_block?: string;
  } => {
    const tab = typeof search.tab === "string" ? search.tab : "transactions";
    if (tab === "logs") {
      const log_mode = search.log_mode === "topic" ? "topic" : "name";
      return {
        tab,
        log_mode,
        from_block:
          typeof search.from_block === "string"
            ? search.from_block.slice(0, 79)
            : search.from_block === undefined
              ? undefined
              : String(search.from_block),
        to_block:
          typeof search.to_block === "string"
            ? search.to_block.slice(0, 79)
            : search.to_block === undefined
              ? undefined
              : String(search.to_block),
        event_signature:
          log_mode === "name" && typeof search.event_signature === "string"
            ? search.event_signature.slice(0, 4097)
            : undefined,
        event_id:
          log_mode === "name" && search.event_id !== undefined
            ? String(search.event_id).slice(0, 67)
            : undefined,
        ...Object.fromEntries(
          ["topic0", "topic1", "topic2", "topic3"].map((key) => [
            key,
            search[key] === undefined ? undefined : String(search[key]).slice(0, 67),
          ]),
        ),
      };
    }
    if (tab === "transactions") return {};
    if (tab === "assets") {
      return {
        tab,
        asset: search.asset === "defi" ? "defi" : search.asset === "nfts" ? "nfts" : "tokens",
      };
    }
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
const addressExportRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/address/$address/export",
  component: AddressExportRoutePage,
});
const tokensRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tokens",
  component: TokensPage,
});
const tokenRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/token/$address",
  validateSearch: (
    search: Record<string, unknown>,
  ): { tab?: "transfers" | "holders" | "details" } => ({
    tab:
      search.tab === "holders" || search.tab === "details" || search.tab === "transfers"
        ? search.tab
        : undefined,
  }),
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
  beforeLoad: () => {
    throw redirect({ to: "/account", search: { tab: "admin", section: "status" }, replace: true });
  },
});
const accountRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/account",
  validateSearch: (
    search: Record<string, unknown>,
  ): {
    tab?: "overview" | "api-keys" | "billing" | "watchlist" | "admin";
    section?: "overview" | "users" | "billing" | "status";
  } => ({
    tab:
      search.tab === "watchlist" ||
      search.tab === "api-keys" ||
      search.tab === "billing" ||
      search.tab === "admin"
        ? search.tab
        : undefined,
    section:
      search.tab === "admin"
        ? search.section === "users" || search.section === "billing" || search.section === "status"
          ? search.section
          : "overview"
        : undefined,
  }),
  component: AccountRoutePage,
});
const adminUsersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/admin/users",
  beforeLoad: () => {
    throw redirect({ to: "/account", search: { tab: "admin", section: "users" }, replace: true });
  },
});
const adminBillingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/admin/billing",
  beforeLoad: () => {
    throw redirect({ to: "/account", search: { tab: "admin", section: "billing" }, replace: true });
  },
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
  addressExportRoute,
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
  return <BlockDetailPage identifier={blockID} tab={tab ?? "overview"} />;
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
  return <TransactionDetailPage hash={hash} tab={tab ?? "overview"} />;
}

function UserOperationRoutePage() {
  const { hash } = userOperationRoute.useParams();
  return <UserOperationDetailPage hash={hash} />;
}

function AddressRoutePage() {
  const { address } = addressRoute.useParams();
  const { tab, asset } = addressRoute.useSearch();
  return <AddressDetailPage address={address} tab={tab ?? "transactions"} asset={asset} />;
}

function TokenRoutePage() {
  const { address } = tokenRoute.useParams();
  const { tab } = tokenRoute.useSearch();
  return <TokenDetailPage address={address} tab={tab} />;
}

function NFTRoutePage() {
  const { address, tokenID } = nftRoute.useParams();
  return <NFTDetailPage address={address} tokenID={tokenID} />;
}

function AccountRoutePage() {
  const { tab, section } = accountRoute.useSearch();
  return <AccountPage tab={tab ?? "overview"} section={section ?? "users"} />;
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
    parseSearch: (search) => {
      const parsed = defaultParseSearch(search) as Record<string, unknown>;
      const params = new URLSearchParams(search);
      // Unquoted decimal query values must not pass through JSON's Number parser.
      for (const bound of ["from_block", "to_block"]) {
        if (params.has(bound) && typeof parsed[bound] !== "string")
          parsed[bound] = params.get(bound);
      }
      return parsed;
    },
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

function AddressExportRoutePage() {
  const { address } = addressExportRoute.useParams();
  if (!isAddress(address)) return <NotFoundPage />;
  return <AddressExportPage address={getAddress(address)} />;
}
