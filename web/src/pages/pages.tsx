import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableCaption,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from "@/components/ui/table";
import { Alert } from "@/components/ui/alert";
import { Pagination } from "@/components/ui/pagination";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useBrowsingInvalidated } from "@/api/browsingHooks";
import { expirationText } from "@/ens/nameDetails";
import { useNameFreshness } from "@/ens/useNameFreshness";
import { BrowsingScope, BrowsingEmpty } from "@/components/BrowsingScope";
import { ArrowRight, CircleAlert, History } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { useBrowsingCursorHistory, useCursorHistory } from "@/api/pagination";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { usePublicConfig } from "@/api/publicConfig";
import {
  useBlocks,
  useGenesisAccounts,
  useSearchResults,
  useTokens,
  useTransactions,
} from "@/api/hooks";
import type { SearchResult, VerificationJob } from "@/api/types";
import {
  formatInteger,
  formatNativeAmount,
  formatTokenAmount,
  formatTimestamp,
  shorten,
} from "@/components/format";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { BrowsingRefreshControl } from "@/components/BrowsingRefreshControl";
import { PageHeading } from "@/components/PageHeading";
import { QueryNotice } from "@/components/QueryNotice";
import { TransactionMethodCell } from "./AddressPage";
import { IncludedTransactionStatus } from "./TransactionPage";

export const CORE_PAGE_SIZE = 25;
const SEARCH_PAGE_SIZE = 20;

export function formatTokenEventAmount(
  event: { amount?: string; decimals?: number; standard: string },
  locale: string,
): string {
  if (event.amount === undefined) return "—";
  return event.standard === "erc20"
    ? formatTokenAmount(event.amount, event.decimals, locale)
    : formatInteger(event.amount, locale);
}

export function isNFTStandard(standard: string): boolean {
  return standard === "erc721" || standard === "erc1155";
}

export function NFTTokenIDLink({
  address,
  compact = false,
  prefix = false,
  tokenID,
}: {
  address: string;
  compact?: boolean;
  prefix?: boolean;
  tokenID: string;
}) {
  return (
    <Link
      to="/nft/$address/$tokenID"
      params={{ address, tokenID }}
      title={compact ? tokenID : undefined}
    >
      <code>{`${prefix ? "#" : ""}${compact ? shorten(tokenID) : tokenID}`}</code>
    </Link>
  );
}

export function FinalityBadge({ finality }: { finality: string }) {
  const { t } = useTranslation();
  return (
    <Badge variant="status" className={`finality-badge ${finality}`}>
      {finalityLabel(finality, t)}
    </Badge>
  );
}

export function BlocksPage() {
  const { i18n, t } = useTranslation();
  const pager = useCursorHistory("blocks");
  const blocks = useBlocks(pager.request, CORE_PAGE_SIZE);
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <Page title={t("page.blocks")} description={t("page.blocksDescription")}>
      <QueryNotice loading={blocks.isPending} error={blocks.error} onReset={pager.reset} />
      {blocks.data?.items.length === 0 && (
        <p className="empty-result" role="status">
          {t("state.noBlocks")}
        </p>
      )}
      {blocks.data && blocks.data.items.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("page.blocks")}>
          <Table className="blocks-table">
            <TableCaption className="sr-only">{t("context.canonicalBlocksOnly")}</TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead>{t("table.block")}</TableHead>
                <TableHead>{t("table.age")}</TableHead>
                <TableHead className="numeric">{t("table.transactions")}</TableHead>
                <TableHead className="numeric">{t("table.gas")}</TableHead>
                <TableHead className="text-right">{t("table.finality")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {blocks.data.items.map((block) => (
                <TableRow key={block.hash}>
                  <TableCell>
                    <Link to="/blocks/$blockID" params={{ blockID: block.hash }}>
                      {formatInteger(block.number, locale)}
                    </Link>
                    <code className="table-secondary" title={block.hash}>
                      {shorten(block.hash)}
                    </code>
                  </TableCell>
                  <TableCell>{formatTimestamp(block.timestamp, locale)}</TableCell>
                  <TableCell className="numeric">
                    {formatInteger(block.transaction_count, locale)}
                  </TableCell>
                  <TableCell className="numeric">{formatInteger(block.gas_used, locale)}</TableCell>
                  <TableCell className="text-right">
                    <FinalityBadge finality={block.finality} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {blocks.data && (
        <CursorPagination
          itemCount={blocks.data?.items.length ?? 0}
          onReset={pager.reset}
          busy={blocks.isFetching}
          hasNext={Boolean(blocks.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("pagination.blocks")}
          onNext={() => pager.next(blocks.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      )}
    </Page>
  );
}

export function GenesisPage() {
  const { i18n, t } = useTranslation();
  const publicConfig = usePublicConfig();
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";
  const pager = useCursorHistory("genesis");
  const accounts = useGenesisAccounts(pager.request, CORE_PAGE_SIZE);
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <Page title={t("page.genesis")} description={t("page.genesisDescription")}>
      <Alert asChild>
        <p className="context-note" role="note">
          {t("context.genesisAuthenticated")}
        </p>
      </Alert>
      <QueryNotice loading={accounts.isPending} error={accounts.error} onReset={pager.reset} />
      {accounts.data?.items.length === 0 && (
        <p className="empty-result" role="status">
          {t("state.noGenesisAccounts")}
        </p>
      )}
      {accounts.data && accounts.data.items.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("page.genesis")}>
          <Table>
            <TableCaption className="sr-only">{t("context.genesisAuthenticated")}</TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead>{t("table.address")}</TableHead>
                <TableHead>{t("detail.type")}</TableHead>
                <TableHead className="numeric">
                  {t("table.balance", { symbol: nativeSymbol })}
                </TableHead>
                <TableHead className="numeric">{t("detail.nonce")}</TableHead>
                <TableHead>{t("detail.codeHash")}</TableHead>
                <TableHead>{t("detail.storageRoot")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {accounts.data.items.map((account) => (
                <TableRow key={account.address}>
                  <TableCell>
                    <AddressIdentity address={account.address} />
                  </TableCell>
                  <TableCell>{t(`accountType.${account.type}`)}</TableCell>
                  <TableCell className="numeric">
                    {formatNativeAmount(account.balance, locale, nativeDecimals)}
                  </TableCell>
                  <TableCell className="numeric">{formatInteger(account.nonce, locale)}</TableCell>
                  <TableCell>
                    <code title={account.code_hash}>{shorten(account.code_hash)}</code>
                  </TableCell>
                  <TableCell>
                    <code title={account.storage_root}>{shorten(account.storage_root)}</code>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {accounts.data && (
        <CursorPagination
          itemCount={accounts.data?.items.length ?? 0}
          onReset={pager.reset}
          busy={accounts.isFetching}
          hasNext={Boolean(accounts.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("pagination.genesis")}
          onNext={() => pager.next(accounts.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      )}
    </Page>
  );
}

export function TransactionsPage() {
  const { i18n, t } = useTranslation();
  const publicConfig = usePublicConfig();
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";
  const pager = useCursorHistory("transactions");
  const transactions = useTransactions(pager.request, CORE_PAGE_SIZE);
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <Page title={t("page.transactions")} description={t("page.transactionsDescription")}>
      <QueryNotice
        loading={transactions.isPending}
        error={transactions.error}
        onReset={pager.reset}
      />
      {transactions.data?.items.length === 0 && (
        <p className="empty-result" role="status">
          {t("state.noTransactions")}
        </p>
      )}
      {transactions.data && transactions.data.items.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("page.transactions")}>
          <Table>
            <TableCaption className="sr-only">
              {t("context.canonicalTransactionsOnly")}
            </TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead>{t("table.hash")}</TableHead>
                <TableHead>{t("table.method")}</TableHead>
                <TableHead className="numeric">{t("table.block")}</TableHead>
                <TableHead>{t("table.status")}</TableHead>
                <TableHead>{t("table.from")}</TableHead>
                <TableHead>{t("table.to")}</TableHead>
                <TableHead className="numeric">
                  {t("table.value", { symbol: nativeSymbol })}
                </TableHead>
                <TableHead>{t("table.finality")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {transactions.data.items.map((transaction) => (
                <TableRow key={transaction.hash}>
                  <TableCell>
                    <Link
                      to="/tx/$hash"
                      params={{ hash: transaction.hash }}
                      search={{ tab: "overview" }}
                    >
                      {shorten(transaction.hash)}
                    </Link>
                  </TableCell>
                  <TransactionMethodCell
                    method={transaction.method}
                    signature={transaction.method_signature}
                  />
                  <TableCell className="numeric">
                    {transaction.block_hash ? (
                      <Link to="/blocks/$blockID" params={{ blockID: transaction.block_hash }}>
                        {formatInteger(transaction.block_number, locale)}
                      </Link>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                  <TableCell>
                    <IncludedTransactionStatus transaction={transaction} />
                  </TableCell>
                  <TableCell>
                    <AddressIdentity nameOnly address={transaction.from} />
                  </TableCell>
                  <TableCell>
                    {transaction.to ? (
                      <AddressIdentity nameOnly address={transaction.to} />
                    ) : (
                      t("common.contractCreation")
                    )}
                  </TableCell>
                  <TableCell className="numeric">
                    <code>{formatNativeAmount(transaction.value, locale, nativeDecimals)}</code>
                  </TableCell>
                  <TableCell>
                    <FinalityBadge finality={transaction.finality} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {transactions.data && (
        <CursorPagination
          itemCount={transactions.data?.items.length ?? 0}
          onReset={pager.reset}
          busy={transactions.isFetching}
          hasNext={Boolean(transactions.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("pagination.transactions")}
          onNext={() => pager.next(transactions.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      )}
    </Page>
  );
}

export function TokensPage() {
  const { i18n, t } = useTranslation();
  const pager = useBrowsingCursorHistory("tokens");
  const tokens = useTokens(pager.request, CORE_PAGE_SIZE);
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <Page title={t("page.tokens")} description={t("page.tokensDescription")}>
      <BrowsingScope status={tokens.data?.meta.browsing}>
        <QueryNotice loading={tokens.isPending} error={tokens.error} onReset={pager.reset} />
        {tokens.data && tokens.data.items.length === 0 && (
          <p className="empty-result" role="status">
            <BrowsingEmpty>{t("state.noTokens")}</BrowsingEmpty>
          </p>
        )}
        {tokens.data && tokens.data.items.length > 0 && (
          <div className="table-scroll" tabIndex={0} aria-label={t("page.tokens")}>
            <Table>
              <TableCaption className="sr-only">{t("page.tokensDescription")}</TableCaption>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("table.token")}</TableHead>
                  <TableHead>{t("table.standard")}</TableHead>
                  <TableHead>{t("table.confidence")}</TableHead>
                  <TableHead className="numeric">{t("table.supply")}</TableHead>
                  <TableHead>{t("table.metadata")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tokens.data.items.map((token) => (
                  <TableRow key={token.address}>
                    <TableCell>
                      <span className="table-primary">
                        <Link to="/token/$address" params={{ address: token.address }}>
                          {token.name ?? token.symbol ?? shorten(token.address)}
                        </Link>
                        <Link
                          to="/token/$address"
                          params={{ address: token.address }}
                          title={token.address}
                        >
                          <code>{shorten(token.address)}</code>
                        </Link>
                      </span>
                    </TableCell>
                    <TableCell>
                      <Badge variant="status" className="result-kind">
                        {tokenStandardLabel(token.standard, t)}
                      </Badge>
                    </TableCell>
                    <TableCell>{confidenceLabel(token.confidence, t)}</TableCell>
                    <TableCell className="numeric">
                      <code>{formatInteger(token.total_supply, locale)}</code>
                    </TableCell>
                    <TableCell>{stageStateLabel(token.metadata_state, t)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
        {tokens.data && (
          <CursorPagination
            itemCount={tokens.data?.items.length ?? 0}
            onReset={pager.reset}
            busy={tokens.isFetching}
            hasNext={Boolean(tokens.data.next_cursor)}
            hasPrevious={pager.hasPrevious}
            label={t("pagination.tokens")}
            onNext={() => pager.next(tokens.data?.next_cursor)}
            onPrevious={pager.previous}
            page={pager.page}
          />
        )}
      </BrowsingScope>
    </Page>
  );
}

export function ReorgContext({ kind, hash }: { kind: "block" | "transaction"; hash: string }) {
  const { t } = useTranslation();
  return (
    <section className="reorg-context" role="status" aria-labelledby="reorg-context-title">
      <span className="reorg-mark" aria-hidden="true">
        <UIIcon icon={History} size={24} />
      </span>
      <div>
        <h2 id="reorg-context-title">
          {kind === "block" ? t("context.orphanBlock") : t("context.orphanTransaction")}
        </h2>
        <p>{t("context.orphanDetail")}</p>
        <code>{hash}</code>
      </div>
    </section>
  );
}

export function CursorPagination({
  itemCount,
  onReset,
  busy,
  hasNext,
  hasPrevious,
  label,
  onNext,
  onPrevious,
  page,
}: {
  itemCount: number;
  onReset: () => void;
  busy: boolean;
  hasNext: boolean;
  hasPrevious: boolean;
  label: string;
  onNext: () => void;
  onPrevious: () => void;
  page: number;
}) {
  const invalid = useBrowsingInvalidated();
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <Pagination
      itemCount={itemCount}
      onReset={onReset}
      hasPrevious={hasPrevious}
      busy={busy}
      className="cursor-pagination"
      aria-busy={busy}
      aria-label={label}
    >
      <Button
        variant="outline"
        className="button secondary"
        disabled={!hasPrevious || busy || invalid}
        onClick={onPrevious}
        type="button"
      >
        {t("pagination.previous")}
      </Button>
      <span aria-live="polite">{t("pagination.page", { page: formatInteger(page, locale) })}</span>
      <Button
        variant="outline"
        className="button secondary"
        disabled={!hasNext || busy || invalid}
        onClick={onNext}
        type="button"
      >
        {t("pagination.next")}
      </Button>
    </Pagination>
  );
}

export function DetailList({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Card asChild>
      <section className="panel detail-card" aria-label={label}>
        <h2>{label}</h2>
        <dl className="detail-grid">{children}</dl>
      </section>
    </Card>
  );
}

export function Detail({
  label,
  value,
  mono,
  wide,
}: {
  label: string;
  value?: React.ReactNode;
  mono?: boolean;
  wide?: boolean;
}) {
  return (
    <div className={wide ? "detail-item wide" : "detail-item"}>
      <dt>{label}</dt>
      <dd className={mono ? "mono-wrap" : undefined}>{value ?? "—"}</dd>
    </div>
  );
}

export function CapabilityDegraded({ stage, state }: { stage: string; state: string }) {
  const { t } = useTranslation();
  return (
    <Alert asChild>
      <div className="query-notice degraded" role="status">
        <UIIcon icon={CircleAlert} size={14} className="status-icon warning" />
        <span>
          <strong>{t("state.stageUnavailable", { stage: stageLabel(stage, t) })}</strong>
          <small>
            {t("state.stageUnavailableDetail", { state: stageStateLabel(state, t), block: "" })}
          </small>
        </span>
      </div>
    </Alert>
  );
}

export type Translate = ReturnType<typeof useTranslation>["t"];

export function finalityLabel(value: string, t: Translate): string {
  switch (value) {
    case "pending":
      return t("finality.pending");
    case "latest":
      return t("finality.latest");
    case "safe":
      return t("finality.safe");
    case "finalized":
      return t("finality.finalized");
    case "orphan":
      return t("finality.orphan");
    default:
      return value;
  }
}

export function transactionStatusLabel(value: string | undefined, t: Translate): string {
  switch (value) {
    case "pending":
      return t("transactionStatus.pending");
    case "success":
      return t("transactionStatus.success");
    case "failed":
      return t("transactionStatus.failed");
    case "unknown":
      return t("transactionStatus.unknown");
    default:
      return t("common.indexed");
  }
}

export function transactionTypeLabel(value: string | undefined, t: Translate): string {
  if (!value) return "—";
  const normalized = value.trim().toLowerCase();
  const parsed = normalized.startsWith("0x")
    ? Number.parseInt(normalized.slice(2), 16)
    : Number.parseInt(normalized, 10);
  if (!Number.isNaN(parsed)) {
    switch (String(parsed)) {
      case "0":
        return t("transactionType.legacy");
      case "1":
        return t("transactionType.accessList");
      case "2":
        return t("transactionType.dynamicFee");
      case "3":
        return t("transactionType.blob");
      case "4":
        return t("transactionType.eip7702");
    }
  }
  return value;
}

export function accountTypeLabel(value: string, t: Translate): string {
  switch (value) {
    case "eoa":
      return t("accountType.eoa");
    case "contract":
      return t("accountType.contract");
    case "delegated_eoa":
      return t("accountType.delegatedEoa");
    case "unknown":
      return t("accountType.unknown");
    default:
      return t("accountType.unknown");
  }
}

export function stageLabel(value: string, t: Translate): string {
  switch (value) {
    case "core":
      return t("stage.core");
    case "token":
      return t("stage.token");
    case "stats":
    case "statistics":
      return t("stage.stats");
    case "trace":
      return t("stage.trace");
    case "metadata":
      return t("stage.metadata");
    case "state":
      return t("stage.state");
    case "user_operations":
      return t("feature.userOperations");
    default:
      return value;
  }
}

export function stageStateLabel(value: string, t: Translate): string {
  switch (value) {
    case "complete":
      return t("stageState.complete");
    case "pending":
      return t("stageState.pending");
    case "unavailable":
      return t("stageState.unavailable");
    case "failed":
      return t("stageState.failed");
    default:
      return value;
  }
}

export function tokenStandardLabel(value: string, t: Translate): string {
  switch (value) {
    case "erc20":
      return t("tokenStandard.erc20");
    case "erc721":
      return t("tokenStandard.erc721");
    case "erc1155":
      return t("tokenStandard.erc1155");
    default:
      return t("tokenStandard.unknown");
  }
}

export function confidenceLabel(value: string, t: Translate): string {
  switch (value) {
    case "verified":
      return t("confidence.verified");
    case "high":
      return t("confidence.high");
    case "inferred":
      return t("confidence.inferred");
    case "guess":
      return t("confidence.guess");
    case "rpc_exact":
      return t("confidence.rpcExact");
    default:
      return value;
  }
}

export function tokenEventKindLabel(value: string, t: Translate): string {
  switch (value) {
    case "transfer":
      return t("tokenEvent.transfer");
    case "mint":
      return t("tokenEvent.mint");
    case "burn":
      return t("tokenEvent.burn");
    case "approval":
      return t("tokenEvent.approval");
    case "approval_for_all":
      return t("tokenEvent.approvalForAll");
    default:
      return value;
  }
}

export function featureLabel(value: string, t: Translate): string {
  switch (value) {
    case "trace":
      return t("feature.trace");
    case "mempool":
      return t("feature.mempool");
    case "historical_state":
      return t("feature.historicalState");
    case "verification":
      return t("feature.verification");
    case "sourcify":
      return t("feature.sourcify");
    case "nft_metadata":
      return t("feature.nftMetadata");
    case "pricing":
      return t("feature.pricing");
    case "user_operations":
      return t("feature.userOperations");
    default:
      return value;
  }
}

export function verificationJobStatusLabel(value: VerificationJob["status"], t: Translate): string {
  switch (value) {
    case "queued":
      return t("verificationStatus.queued");
    case "running":
      return t("verificationStatus.running");
    case "succeeded":
      return t("verificationStatus.succeeded");
    case "failed":
      return t("verificationStatus.failed");
    case "cancelled":
      return t("verificationStatus.cancelled");
  }
}

export function verificationMatchLabel(value: string | undefined, t: Translate): string {
  switch (value) {
    case "full":
      return t("verificationMatch.full");
    case "partial":
      return t("verificationMatch.partial");
    default:
      return "—";
  }
}

export function verificationLanguageLabel(value: string, t: Translate): string {
  switch (value) {
    case "solidity":
      return t("verificationLanguage.solidity");
    case "yul":
      return t("verificationLanguage.yul");
    case "geas":
      return t("verificationLanguage.geas");
    case "vyper":
      return t("verificationLanguage.vyper");
    default:
      return value;
  }
}

export function searchKindLabel(value: SearchResult["kind"], t: Translate): string {
  switch (value) {
    case "name":
      return t("names.name");
    case "block":
      return t("searchKind.block");
    case "transaction":
      return t("searchKind.transaction");
    case "user_operation":
      return t("searchKind.user_operation");
    case "address":
      return t("searchKind.address");
    case "contract":
      return t("searchKind.contract");
    case "token":
      return t("searchKind.token");
    case "nft":
      return t("searchKind.nft");
    case "label":
      return t("searchKind.label");
  }
}

export function yesNo(value: boolean, t: ReturnType<typeof useTranslation>["t"]): string {
  return value ? t("common.yes") : t("common.no");
}

export function SearchPage({ query }: { query: string }) {
  const { t } = useTranslation();
  const normalizedQuery = query.trim();
  const pager = useCursorHistory(`search:${normalizedQuery}`);
  const search = useSearchResults(normalizedQuery, pager.request, SEARCH_PAGE_SIZE);
  const names = useNameFreshness(search.data?.items, [
    "search",
    normalizedQuery,
    pager.request.cursor ?? null,
    SEARCH_PAGE_SIZE,
    pager.request.refreshGeneration,
  ]);
  return (
    <Page title={t("page.search")} description={query} mono>
      {normalizedQuery.length === 0 && (
        <Alert asChild>
          <p className="context-note">{t("search.prompt")}</p>
        </Alert>
      )}
      <QueryNotice
        loading={search.isPending && normalizedQuery.length > 0}
        error={search.error}
        onReset={pager.reset}
      />
      {names.withdrawn && (
        <Alert asChild>
          <p className="context-note" role="status">
            {t("state.nameExpired")}{" "}
            <Button variant="outline" type="button" onClick={pager.reset}>
              {t("actions.restartPagination")}
            </Button>
          </p>
        </Alert>
      )}
      {!search.error && search.data && search.data.items.length === 0 && (
        <p className="empty-result" role="status">
          {t("state.noResults")}
        </p>
      )}
      <div className="search-results">
        {!search.error &&
          names.items?.map((result) => (
            <SearchResultLink key={`${result.kind}:${result.key}`} result={result} />
          ))}
      </div>
      {search.data && !search.error && (
        <CursorPagination
          itemCount={names.items?.length ?? 0}
          onReset={pager.reset}
          busy={search.isFetching}
          hasNext={Boolean(search.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("pagination.search")}
          onNext={() => pager.next(search.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      )}
    </Page>
  );
}

function SearchResultLink({ result }: { result: SearchResult }) {
  const { t, i18n } = useTranslation();
  const content = (
    <>
      <Badge variant="status" className="result-kind">
        {searchKindLabel(result.kind, t)}
      </Badge>
      <span>
        <strong>
          <bdi>{result.label}</bdi>
        </strong>
        <small>{result.key}</small>
      </span>
      <span className="search-result-tail">
        {result.canonical !== undefined && (
          <Badge
            variant="status"
            className={result.canonical ? "availability yes" : "orphan-label"}
          >
            {result.canonical ? t("common.canonical") : t("common.orphan")}
          </Badge>
        )}
        <UIIcon icon={ArrowRight} />
      </span>
    </>
  );

  if (result.kind === "name")
    return (
      <div className="search-result search-name-result">
        <Link to="/name/$name" params={{ name: result.key }}>
          {content}
        </Link>
        {result.name_details && (
          <div className="search-name-summary">
            <span>{t(`names.${result.name_details.registration_state}`)}</span>
            {result.name_details.expiration_time.state === "value" && (
              <small>
                {t("names.expiration")}:{" "}
                {expirationText(result.name_details.expiration_time.value, i18n.language)}
              </small>
            )}
            {result.name_details.resolved_address.state === "value" &&
              result.name_details.resolved_address.value && (
                <AddressIdentity address={result.name_details.resolved_address.value} copy />
              )}
          </div>
        )}
      </div>
    );
  switch (result.kind) {
    case "block":
      return (
        <Link className="search-result" to="/blocks/$blockID" params={{ blockID: result.key }}>
          {content}
        </Link>
      );
    case "transaction":
      return (
        <Link
          className="search-result"
          to="/tx/$hash"
          params={{ hash: result.key }}
          search={{ tab: "overview" }}
        >
          {content}
        </Link>
      );
    case "user_operation":
      return (
        <Link className="search-result" to="/user-op/$hash" params={{ hash: result.key }}>
          {content}
        </Link>
      );
    case "address":
      return (
        <Link className="search-result" to="/address/$address" params={{ address: result.key }}>
          {content}
        </Link>
      );
    case "contract":
      return (
        <Link
          className="search-result"
          hash="code"
          params={{ address: result.key }}
          search={{}}
          to="/address/$address"
        >
          {content}
        </Link>
      );
    case "token":
      return (
        <Link className="search-result" to="/token/$address" params={{ address: result.key }}>
          {content}
        </Link>
      );
    default:
      return (
        <a className="search-result" href={`/search?q=${encodeURIComponent(result.key)}`}>
          {content}
        </a>
      );
  }
}

export function TextArtifact({ title, value }: { title: string; value: unknown }) {
  return (
    <section className="artifact-panel">
      <h3>{title}</h3>
      <pre tabIndex={0}>{JSON.stringify(value, null, 2)}</pre>
    </section>
  );
}

export function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

export class DuplicateJSONKeyError extends Error {}
export class JSONStructureLimitError extends Error {}
export class UnsafeJSONNumberError extends Error {}

export function assertNoDuplicateJSONKeys(source: string): void {
  let offset = 0;
  const maximumDepth = 256;
  const whitespace = /\s/;

  const skipWhitespace = () => {
    while (offset < source.length && whitespace.test(source[offset] ?? "")) offset += 1;
  };

  const parseString = (): string => {
    if (source[offset] !== '"') throw new SyntaxError("expected JSON string");
    const start = offset;
    offset += 1;
    while (offset < source.length) {
      const character = source[offset];
      if (character === "\\") {
        offset += 2;
        continue;
      }
      offset += 1;
      if (character === '"') {
        return JSON.parse(source.slice(start, offset)) as string;
      }
    }
    throw new SyntaxError("unterminated JSON string");
  };

  const parseValue = (depth: number): void => {
    if (depth > maximumDepth) throw new JSONStructureLimitError();
    skipWhitespace();
    const character = source[offset];
    if (character === "{") {
      offset += 1;
      skipWhitespace();
      const keys = new Set<string>();
      if (source[offset] === "}") {
        offset += 1;
        return;
      }
      while (offset < source.length) {
        const key = parseString();
        if (keys.has(key)) throw new DuplicateJSONKeyError();
        keys.add(key);
        skipWhitespace();
        if (source[offset] !== ":") throw new SyntaxError("expected JSON colon");
        offset += 1;
        parseValue(depth + 1);
        skipWhitespace();
        if (source[offset] === "}") {
          offset += 1;
          return;
        }
        if (source[offset] !== ",") throw new SyntaxError("expected JSON comma");
        offset += 1;
        skipWhitespace();
      }
      throw new SyntaxError("unterminated JSON object");
    }
    if (character === "[") {
      offset += 1;
      skipWhitespace();
      if (source[offset] === "]") {
        offset += 1;
        return;
      }
      while (offset < source.length) {
        parseValue(depth + 1);
        skipWhitespace();
        if (source[offset] === "]") {
          offset += 1;
          return;
        }
        if (source[offset] !== ",") throw new SyntaxError("expected JSON comma");
        offset += 1;
      }
      throw new SyntaxError("unterminated JSON array");
    }
    if (character === '"') {
      parseString();
      return;
    }
    const start = offset;
    while (
      offset < source.length &&
      !whitespace.test(source[offset] ?? "") &&
      !",]}".includes(source[offset] ?? "")
    ) {
      offset += 1;
    }
    if (offset === start) throw new SyntaxError("expected JSON value");
    const primitive = source.slice(start, offset);
    if (/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(primitive)) {
      const parsed = Number(primitive);
      if (
        !Number.isSafeInteger(parsed) ||
        !/^-?(?:0|[1-9]\d*)$/.test(primitive) ||
        String(parsed) !== primitive
      ) {
        throw new UnsafeJSONNumberError();
      }
    }
  };

  parseValue(0);
  skipWhitespace();
  if (offset !== source.length) throw new SyntaxError("unexpected JSON suffix");
}

export function logDecodingKey(status: string) {
  switch (status) {
    case "decoded":
      return "detail.logDecoded" as const;
    case "ambiguous":
      return "detail.logAmbiguous" as const;
    case "unknown":
      return "detail.logUnknown" as const;
    case "malformed":
      return "detail.logMalformed" as const;
    default:
      return "detail.logUnavailable" as const;
  }
}

export function abiSourceKindLabel(value: string, t: Translate): string {
  switch (value) {
    case "exact_address":
      return t("detail.abiSourceKinds.exactAddress");
    case "code_hash":
      return t("detail.abiSourceKinds.codeHash");
    case "proxy_implementation":
      return t("detail.abiSourceKinds.proxyImplementation");
    case "signature_database":
      return t("detail.abiSourceKinds.signatureDatabase");
    case "builtin":
      return t("detail.abiSourceKinds.builtin");
    default:
      return value;
  }
}

export function attributionLabel(value: string, t: Translate): string {
  return value === "exact_trace"
    ? t("detail.attributionKinds.exactTrace")
    : t("detail.attributionKinds.addressFallback");
}

export function traceDecodingKey(status: string) {
  switch (status) {
    case "decoded":
      return "detail.traceDecoded" as const;
    case "ambiguous":
      return "detail.traceAmbiguous" as const;
    case "unknown":
      return "detail.traceUnknown" as const;
    case "malformed":
      return "detail.traceMalformed" as const;
    case "not_applicable":
      return "detail.traceNotApplicable" as const;
    default:
      return "detail.traceUnavailable" as const;
  }
}

export function traceOutputStatusKey(status: string) {
  switch (status) {
    case "decoded":
      return "detail.outputDecoded" as const;
    case "empty":
      return "detail.outputEmpty" as const;
    case "unknown":
      return "detail.outputUnknown" as const;
    case "malformed":
      return "detail.outputMalformed" as const;
    case "not_applicable":
      return "detail.outputNotApplicable" as const;
    default:
      return "detail.outputUnavailable" as const;
  }
}

export function formatLogArgument(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "boolean" || typeof value === "number" || value === null) {
    return String(value);
  }
  try {
    return JSON.stringify(value) ?? "[unavailable]";
  } catch {
    return "[unavailable]";
  }
}

export function NotFoundPage() {
  const { t } = useTranslation();
  return (
    <Page title="404 ·" description={t("page.notFound")}>
      <p>{t("page.notFoundDescription")}</p>
      <Button asChild variant="default">
        <Link className="button primary inline-button" to="/">
          {t("nav.home")}
        </Link>
      </Button>
    </Page>
  );
}

export function Page({
  title,
  description,
  children,
  mono,
}: {
  title: string;
  description: React.ReactNode;
  children: React.ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="page-stack inner-page">
      <PageHeading
        actions={<BrowsingRefreshControl />}
        title={title}
        description={description}
        mono={mono}
      />
      {children}
    </div>
  );
}
