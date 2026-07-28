import {
  createRootRoute,
  createRoute,
  createRouter,
  type RouterHistory,
} from "@tanstack/react-router";

import { AppShell } from "@/components/AppShell";
import {
  BlocksPage,
  ChartsPage,
  ContractPage,
  ContractsPage,
  EntityPage,
  GenesisPage,
  HomePage,
  NotFoundPage,
  SearchPage,
  StatusPage,
  TokensPage,
  TransactionsPage,
  VerifyPage,
} from "@/pages/pages";
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
      tab: ["overview", "token-transfers", "logs", "trace", "state-changes"].includes(tab)
        ? tab
        : "overview",
    };
  },
  component: TransactionRoutePage,
});
const addressRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/address/$address",
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
const contractsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/contracts",
  component: ContractsPage,
});
const contractRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/contract/$address",
  validateSearch: (search: Record<string, unknown>) => ({
    code_hash: typeof search.code_hash === "string" ? search.code_hash : "",
  }),
  component: ContractRoutePage,
});
const verifyRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/verify",
  component: VerifyPage,
});
const chartsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/charts",
  component: ChartsPage,
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
  component: AccountPage,
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
  contractsRoute,
  contractRoute,
  verifyRoute,
  chartsRoute,
  pendingRoute,
  statusRoute,
  accountRoute,
  adminUsersRoute,
  adminBillingRoute,
  searchRoute,
]);

function BlockRoutePage() {
  const { blockID } = blockRoute.useParams();
  return <EntityPage kind="block" identifier={blockID} />;
}

function TransactionRoutePage() {
  const { hash } = transactionRoute.useParams();
  const { tab } = transactionRoute.useSearch();
  return <EntityPage kind="transaction" identifier={hash} transactionTab={tab} />;
}

function AddressRoutePage() {
  const { address } = addressRoute.useParams();
  return <EntityPage kind="address" identifier={address} />;
}

function TokenRoutePage() {
  const { address } = tokenRoute.useParams();
  return <EntityPage kind="token" identifier={address} />;
}

function NFTRoutePage() {
  const { address, tokenID } = nftRoute.useParams();
  return <EntityPage kind="nft" identifier={address} secondary={tokenID} />;
}

function ContractRoutePage() {
  const { address } = contractRoute.useParams();
  const { code_hash: codeHash } = contractRoute.useSearch();
  return <ContractPage address={address} codeHash={codeHash} />;
}

function SearchRoutePage() {
  const { q } = searchRoute.useSearch();
  return <SearchPage query={q} />;
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
