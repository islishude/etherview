import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import {
  usePublicConfig,
  useTransaction,
  useTransactionCalldata,
  useTransactionFailure,
  useTransactionInternalTransactions,
  useTransactionAuthorizations,
  useTransactionLogs,
  useTransactionStateChanges,
  useTransactionTokenTransfers,
  useTransactionTrace,
} from "@/api/hooks";
import type {
  TransactionDetail,
  TransactionCalldata as TransactionCalldataResource,
  TransactionFailure as TransactionFailureResource,
  TransactionSummary,
} from "@/api/types";
import { shorten } from "@/components/format";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { QueryNotice } from "@/components/QueryNotice";
import {
  CapabilityDegraded,
  CursorPagination,
  Page,
  ReorgContext,
  useCursorHistory,
} from "./pages";
import {
  TransactionAccessListPanel,
  TransactionBlobPanel,
  TransactionAuthorizationsPanel,
  TransactionUserOperationsPanel,
  TransactionInternalTransactionsPanel,
  TransactionTokenTransfersPanel,
} from "./TransactionPanels";
import { TransactionLogCard } from "./TransactionLogs";
import { TransactionTracePanel } from "./TransactionTrace";
import {
  MempoolDetailExpired,
  MempoolTransactionOverview,
  resolveTransactionActionEvidence,
  TransactionOverviewPanel,
} from "./TransactionOverview";
export { TransactionStatusBadge, IncludedTransactionStatus } from "./TransactionOverview";

const TRANSACTION_TABS = [
  "overview",
  "access-list",
  "blob",
  "authorizations",
  "user-operations",
  "internal-transactions",
  "token-transfers",
  "logs",
  "trace",
  "state-changes",
] as const;

type TransactionTab = (typeof TRANSACTION_TABS)[number];

function transactionTabsForType(type?: string, userOperations = false): TransactionTab[] {
  const tabs: TransactionTab[] = ["overview"];
  if (type === "1" || type === "2" || type === "3" || type === "4") tabs.push("access-list");
  if (type === "3") tabs.push("blob");
  if (type === "4") tabs.push("authorizations");
  if (userOperations) tabs.push("user-operations");
  tabs.push("internal-transactions", "token-transfers", "logs", "trace", "state-changes");
  return tabs;
}

function transactionActiveTab(tabs: readonly TransactionTab[], requested: string): TransactionTab {
  return tabs.includes(requested as TransactionTab) ? (requested as TransactionTab) : "overview";
}

function matchingBlockIdentity(overviewBlockHash?: string, resourceBlockHash?: string): boolean {
  return !resourceBlockHash || !overviewBlockHash || resourceBlockHash === overviewBlockHash;
}

function transactionMempoolExpiry(detail?: TransactionDetail): number {
  if (detail?.kind !== "pending" && detail?.kind !== "replaced") return Number.NaN;
  return Date.parse(detail.transaction.expires_at);
}

export function TransactionDetailPage({ hash, tab }: { hash: string; tab: string }) {
  const { i18n, t } = useTranslation();
  const navigate = useNavigate();
  const transactionDetail = useTransaction(hash);
  const observedDetail = transactionDetail.data;
  const observedExpiry = transactionMempoolExpiry(observedDetail);
  const detailExpired = useMempoolDetailExpiry(observedExpiry);
  const detail = transactionDetail.error || detailExpired ? undefined : observedDetail;
  const included = detail?.kind === "included";
  const transaction =
    detail?.kind === "included"
      ? { ...transactionDetail, data: detail.transaction }
      : { ...transactionDetail, data: undefined };
  const publicConfig = usePublicConfig();
  const userOperationsEnabled = publicConfig.data?.features.user_operations === true;
  const transactionTabs = transactionTabsForType(transaction.data?.type, userOperationsEnabled);
  const activeTab = transactionActiveTab(transactionTabs, tab);
  const calldataEnabled = included && activeTab === "overview" && Boolean(transaction.data?.to);
  const calldata = useTransactionCalldata(hash, calldataEnabled);
  const failureEnabled =
    included && activeTab === "overview" && transaction.data?.status === "failed";
  const failure = useTransactionFailure(hash, failureEnabled);
  const internalPager = useCursorHistory(`transaction-internal-transactions:${hash}`);
  const tokenPager = useCursorHistory(`transaction-token-transfers:${hash}`);
  const logPager = useCursorHistory(`transaction-logs:${hash}`);
  const statePager = useCursorHistory(`transaction-state-changes:${hash}`);
  const authorizationPager = useCursorHistory(`transaction-authorizations:${hash}`);
  const tokenTransfers = useTransactionTokenTransfers(
    hash,
    tokenPager.cursor,
    included && (activeTab === "overview" || activeTab === "token-transfers"),
  );
  const internalTransactions = useTransactionInternalTransactions(
    hash,
    internalPager.cursor,
    included && activeTab === "internal-transactions",
  );
  const logs = useTransactionLogs(hash, logPager.cursor, included && activeTab === "logs");
  const trace = useTransactionTrace(hash, included && activeTab === "trace");
  const authorizations = useTransactionAuthorizations(
    hash,
    authorizationPager.cursor,
    included && activeTab === "authorizations",
  );
  const stateChanges = useTransactionStateChanges(
    hash,
    statePager.cursor,
    included && activeTab === "state-changes",
  );
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";
  const locale = i18n.resolvedLanguage ?? "en";
  const lastIdentityRetry = useRef("");
  const tokenIdentityCurrent = matchingBlockIdentity(
    transaction.data?.block_hash,
    tokenTransfers.data?.block_hash,
  );
  const internalIdentityCurrent = matchingBlockIdentity(
    transaction.data?.block_hash,
    internalTransactions.data?.block_hash,
  );
  const calldataIdentityCurrent =
    calldata.data === undefined ||
    transaction.data === undefined ||
    transactionCalldataIdentityMatches(transaction.data, calldata.data);
  const calldataIdentityRetryKey =
    !calldataIdentityCurrent && transaction.data && calldata.data
      ? transactionCalldataRetryKey(transaction.data, calldata.data)
      : undefined;
  const calldataIdentityRetryPending =
    calldataIdentityRetryKey !== undefined &&
    lastIdentityRetry.current !== calldataIdentityRetryKey;
  const failureIdentityCurrent =
    failure.data === undefined ||
    transaction.data === undefined ||
    transactionFailureIdentityMatches(transaction.data, failure.data);
  const failureIdentityRetryKey =
    !failureIdentityCurrent && transaction.data && failure.data
      ? transactionFailureRetryKey(transaction.data, failure.data)
      : undefined;
  const failureIdentityRetryPending =
    failureIdentityRetryKey !== undefined && lastIdentityRetry.current !== failureIdentityRetryKey;
  const transactionActionEvidence = resolveTransactionActionEvidence(
    Boolean(transaction.data?.to),
    calldata.isPending,
    calldata.isFetching,
    calldata.error,
    calldata.data,
    calldataIdentityCurrent,
    calldataIdentityRetryPending,
  );
  const logIdentityCurrent = matchingBlockIdentity(
    transaction.data?.block_hash,
    logs.data?.block_hash,
  );
  const traceIdentityCurrent = matchingBlockIdentity(
    transaction.data?.block_hash,
    trace.data?.block_hash,
  );
  const stateIdentityCurrent = matchingBlockIdentity(
    transaction.data?.block_hash,
    stateChanges.data?.block_hash,
  );
  const authorizationIdentityCurrent = matchingBlockIdentity(
    transaction.data?.block_hash,
    authorizations.data?.block_hash,
  );
  useTransactionResourceIdentityRefresh(
    activeTab,
    lastIdentityRetry,
    transaction,
    tokenTransfers,
    internalTransactions,
    logs,
    trace,
    authorizations,
    stateChanges,
  );
  usePairedIdentityRefresh(
    calldataEnabled,
    calldataIdentityCurrent,
    calldataIdentityRetryKey,
    lastIdentityRetry,
    transaction.refetch,
    calldata.refetch,
  );
  usePairedIdentityRefresh(
    failureEnabled,
    failureIdentityCurrent,
    failureIdentityRetryKey,
    lastIdentityRetry,
    transaction.refetch,
    failure.refetch,
  );
  const stateGroups = useMemo(() => {
    const groups = new Map<string, NonNullable<typeof stateChanges.data>["items"]>();
    for (const change of stateChanges.data?.items ?? []) {
      const group = groups.get(change.address) ?? [];
      group.push(change);
      groups.set(change.address, group);
    }
    return [...groups.entries()];
  }, [stateChanges.data]);

  if (detail?.kind === "pending" || detail?.kind === "replaced") {
    return (
      <Page title={t("page.transactionDetails")} description={hash} mono>
        <QueryNotice loading={transactionDetail.isPending} error={transactionDetail.error} />
        <MempoolTransactionOverview
          detail={detail}
          locale={locale}
          nativeDecimals={nativeDecimals}
          nativeSymbol={nativeSymbol}
        />
      </Page>
    );
  }

  return (
    <Page title={t("page.transactionDetails")} description={hash} mono>
      <QueryNotice loading={transactionDetail.isPending} error={transactionDetail.error} />
      {detailExpired && !transactionDetail.error ? <MempoolDetailExpired /> : null}
      {transaction.data && (
        <>
          {!transaction.data.canonical && (
            <ReorgContext kind="transaction" hash={transaction.data.hash} />
          )}
          <nav
            className="transaction-tabs"
            role="tablist"
            aria-label={t("detail.transactionSections")}
          >
            {transactionTabs.map((tabID) => (
              <Link
                aria-selected={activeTab === tabID}
                className={activeTab === tabID ? "transaction-tab active" : "transaction-tab"}
                key={tabID}
                onKeyDown={(event) => {
                  if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
                  event.preventDefault();
                  const currentIndex = transactionTabs.indexOf(tabID);
                  const nextIndex =
                    event.key === "Home"
                      ? 0
                      : event.key === "End"
                        ? transactionTabs.length - 1
                        : event.key === "ArrowLeft"
                          ? (currentIndex - 1 + transactionTabs.length) % transactionTabs.length
                          : (currentIndex + 1) % transactionTabs.length;
                  const nextTab = transactionTabs[nextIndex];
                  if (!nextTab) return;
                  const tabList = event.currentTarget.parentElement;
                  void navigate({
                    to: "/tx/$hash",
                    params: { hash },
                    search: { tab: nextTab },
                  }).then(() => {
                    const tabs = tabList?.querySelectorAll<HTMLElement>('[role="tab"]');
                    tabs?.[nextIndex]?.focus();
                  });
                }}
                params={{ hash }}
                role="tab"
                search={{ tab: tabID }}
                tabIndex={activeTab === tabID ? 0 : -1}
                to="/tx/$hash"
              >
                {t(`transactionTabs.${tabID}`)}
              </Link>
            ))}
          </nav>

          {activeTab === "overview" && (
            <TransactionOverviewPanel
              transaction={{ data: transaction.data }}
              transactionActionEvidence={transactionActionEvidence}
              tokenIdentityCurrent={tokenIdentityCurrent}
              tokenTransfers={tokenTransfers}
              failure={failure}
              failureIdentityCurrent={failureIdentityCurrent}
              failureIdentityRetryPending={failureIdentityRetryPending}
              calldata={calldata}
              calldataIdentityCurrent={calldataIdentityCurrent}
              locale={locale}
              nativeDecimals={nativeDecimals}
              nativeSymbol={nativeSymbol}
              t={t}
            />
          )}

          {activeTab === "internal-transactions" && (
            <TransactionInternalTransactionsPanel
              identityCurrent={internalIdentityCurrent}
              locale={locale}
              nativeDecimals={nativeDecimals}
              nativeSymbol={nativeSymbol}
              pager={internalPager}
              resource={internalTransactions}
              t={t}
            />
          )}

          {activeTab === "access-list" && (
            <TransactionAccessListPanel transaction={transaction.data} t={t} />
          )}

          {activeTab === "blob" && (
            <TransactionBlobPanel transaction={transaction.data} locale={locale} t={t} />
          )}

          {activeTab === "authorizations" && (
            <TransactionAuthorizationsPanel
              authorizations={authorizations}
              identityCurrent={authorizationIdentityCurrent}
              pager={authorizationPager}
              t={t}
            />
          )}

          {activeTab === "user-operations" && (
            <TransactionUserOperationsPanel blockHash={transaction.data.block_hash} hash={hash} />
          )}

          {activeTab === "token-transfers" && (
            <TransactionTokenTransfersPanel
              tokenTransfers={tokenTransfers}
              tokenIdentityCurrent={tokenIdentityCurrent}
              tokenPager={tokenPager}
              locale={locale}
              t={t}
            />
          )}

          {activeTab === "logs" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <QueryNotice loading={logs.isPending} error={logs.error} />
              {logs.data && !logIdentityCurrent && (
                <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
              )}
              {logIdentityCurrent && logs.data?.items.length === 0 && (
                <p className="empty-result">{t("state.noTransactionLogs")}</p>
              )}
              {logIdentityCurrent && logs.data && logs.data.items.length > 0 && (
                <div className="transaction-log-list">
                  {logs.data.items.map((log) => (
                    <TransactionLogCard key={log.log_index} log={log} locale={locale} />
                  ))}
                </div>
              )}
              {logIdentityCurrent && logs.data && (
                <CursorPagination
                  busy={logs.isFetching}
                  hasNext={Boolean(logs.data.next_cursor)}
                  hasPrevious={logPager.hasPrevious}
                  label={t("transactionTabs.logs")}
                  onNext={() => logPager.next(logs.data?.next_cursor)}
                  onPrevious={logPager.previous}
                  page={logPager.page}
                />
              )}
            </section>
          )}

          {activeTab === "trace" && (
            <TransactionTracePanel
              trace={trace}
              traceIdentityCurrent={traceIdentityCurrent}
              locale={locale}
              nativeDecimals={nativeDecimals}
              nativeSymbol={nativeSymbol}
              t={t}
            />
          )}

          {activeTab === "state-changes" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <QueryNotice loading={stateChanges.isPending} error={stateChanges.error} />
              {stateChanges.data && !stateIdentityCurrent && (
                <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
              )}
              {stateIdentityCurrent &&
                stateChanges.data?.state !== "complete" &&
                stateChanges.data && (
                  <CapabilityDegraded stage="state_diff" state={stateChanges.data.state} />
                )}
              {stateIdentityCurrent &&
                stateChanges.data?.state === "complete" &&
                stateGroups.length === 0 && (
                  <p className="empty-result">{t("state.noTransactionStateChanges")}</p>
                )}
              {stateIdentityCurrent &&
                stateChanges.data?.state === "complete" &&
                stateGroups.map(([address, changes]) => (
                  <article className="transaction-state-account" key={address}>
                    <header>
                      <AddressIdentity address={address} compact={false} />
                    </header>
                    <dl>
                      {changes.map((change) => (
                        <div key={`${change.kind}:${change.storage_key ?? ""}`}>
                          <dt>
                            {change.kind}
                            {change.storage_key ? <code>{shorten(change.storage_key)}</code> : null}
                          </dt>
                          <dd>
                            <code>{change.before ?? "∅"}</code>
                            <span aria-hidden="true">→</span>
                            <code>{change.after ?? "∅"}</code>
                          </dd>
                        </div>
                      ))}
                    </dl>
                  </article>
                ))}
              {stateIdentityCurrent && stateChanges.data && (
                <CursorPagination
                  busy={stateChanges.isFetching}
                  hasNext={Boolean(stateChanges.data.next_cursor)}
                  hasPrevious={statePager.hasPrevious}
                  label={t("transactionTabs.state-changes")}
                  onNext={() => statePager.next(stateChanges.data?.next_cursor)}
                  onPrevious={statePager.previous}
                  page={statePager.page}
                />
              )}
            </section>
          )}
        </>
      )}
    </Page>
  );
}

type RefetchableBlockResource = {
  data?: { block_hash?: string };
  refetch: () => Promise<unknown>;
};

function useMempoolDetailExpiry(observedExpiry: number): boolean {
  const [expired, setExpired] = useState(false);
  useEffect(() => {
    if (!Number.isFinite(observedExpiry)) {
      setExpired(false);
      return;
    }
    if (observedExpiry <= Date.now()) {
      setExpired(true);
      return;
    }
    setExpired(false);
    const timer = window.setTimeout(
      () => setExpired(true),
      Math.min(observedExpiry - Date.now(), 2_147_483_647),
    );
    return () => window.clearTimeout(timer);
  }, [observedExpiry]);
  return expired || (Number.isFinite(observedExpiry) && observedExpiry <= Date.now());
}

function useTransactionResourceIdentityRefresh(
  activeTab: TransactionTab,
  lastRetry: { current: string },
  transaction: RefetchableBlockResource,
  tokenTransfers: RefetchableBlockResource,
  internalTransactions: RefetchableBlockResource,
  logs: RefetchableBlockResource,
  trace: RefetchableBlockResource,
  authorizations: RefetchableBlockResource,
  stateChanges: RefetchableBlockResource,
): void {
  useEffect(() => {
    if (activeTab === "access-list" || activeTab === "blob") return;
    const resource =
      activeTab === "token-transfers" || activeTab === "overview"
        ? tokenTransfers
        : activeTab === "internal-transactions"
          ? internalTransactions
          : activeTab === "logs"
            ? logs
            : activeTab === "trace"
              ? trace
              : activeTab === "authorizations"
                ? authorizations
                : stateChanges;
    const resourceBlockHash = resource.data?.block_hash;
    const overviewBlockHash = transaction.data?.block_hash;
    if (!resourceBlockHash || !overviewBlockHash || resourceBlockHash === overviewBlockHash) return;
    const retryKey = `${activeTab}:${overviewBlockHash}:${resourceBlockHash}`;
    if (lastRetry.current === retryKey) return;
    lastRetry.current = retryKey;
    void Promise.all([transaction.refetch(), resource.refetch()]);
  }, [
    activeTab,
    authorizations,
    internalTransactions,
    lastRetry,
    logs,
    stateChanges,
    tokenTransfers,
    trace,
    transaction,
  ]);
}

function usePairedIdentityRefresh(
  enabled: boolean,
  identityCurrent: boolean,
  retryKey: string | undefined,
  lastRetry: { current: string },
  refetchOverview: () => Promise<unknown>,
  refetchResource: () => Promise<unknown>,
): void {
  useEffect(() => {
    if (!enabled || identityCurrent || retryKey === undefined || lastRetry.current === retryKey)
      return;
    lastRetry.current = retryKey;
    void Promise.all([refetchOverview(), refetchResource()]);
  }, [enabled, identityCurrent, lastRetry, refetchOverview, refetchResource, retryKey]);
}

function transactionCalldataIdentityMatches(
  transaction: TransactionSummary,
  resource: TransactionCalldataResource,
): boolean {
  return (
    resource.state === "complete" &&
    Boolean(transaction.to) &&
    Boolean(transaction.block_hash) &&
    resource.transaction_hash.toLowerCase() === transaction.hash.toLowerCase() &&
    resource.block_hash.toLowerCase() === transaction.block_hash?.toLowerCase() &&
    resource.block_number === transaction.block_number &&
    resource.transaction_index === String(transaction.transaction_index) &&
    resource.input.toLowerCase() === transaction.input.toLowerCase() &&
    resource.execution.context_address.toLowerCase() === transaction.to?.toLowerCase()
  );
}

function transactionCalldataRetryKey(
  transaction: TransactionSummary,
  resource: TransactionCalldataResource,
): string | undefined {
  if (!transaction.to || !transaction.block_hash) return undefined;
  return [
    "calldata",
    transaction.hash,
    resource.transaction_hash,
    transaction.block_hash,
    resource.block_hash,
    transaction.block_number,
    resource.block_number,
    String(transaction.transaction_index),
    resource.transaction_index,
    transaction.input,
    resource.input,
    transaction.to,
    resource.execution.context_address,
  ]
    .join(":")
    .toLowerCase();
}

function transactionFailureIdentityMatches(
  transaction: TransactionSummary,
  resource: TransactionFailureResource,
): boolean {
  return (
    resource.state === "complete" &&
    Boolean(transaction.block_hash) &&
    resource.transaction_hash.toLowerCase() === transaction.hash.toLowerCase() &&
    resource.block_hash.toLowerCase() === transaction.block_hash?.toLowerCase() &&
    resource.block_number === transaction.block_number &&
    resource.transaction_index === String(transaction.transaction_index)
  );
}

function transactionFailureRetryKey(
  transaction: TransactionSummary,
  resource: TransactionFailureResource,
): string | undefined {
  if (!transaction.block_hash) return undefined;
  return [
    "failure",
    transaction.hash,
    resource.transaction_hash,
    transaction.block_hash,
    resource.block_hash,
    transaction.block_number,
    resource.block_number,
    String(transaction.transaction_index),
    resource.transaction_index,
  ]
    .join(":")
    .toLowerCase();
}
