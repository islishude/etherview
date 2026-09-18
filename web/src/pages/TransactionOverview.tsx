import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import {
  useTransactionCalldata,
  useTransactionFailure,
  useTransactionTokenTransfers,
} from "@/api/hooks";
import type {
  TransactionDetail,
  TransactionCalldata as TransactionCalldataResource,
  TransactionSummary,
} from "@/api/types";
import {
  formatGweiFromWei,
  formatInteger,
  formatNativeAmount,
  formatPercentageRatio,
  formatTimestamp,
} from "@/components/format";
import { CopyableField } from "@/components/CopyButton";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { TransactionStatus, type TransactionVisualStatus } from "@/components/TransactionStatus";
import {
  finalityLabel,
  transactionStatusLabel,
  transactionTypeLabel,
  type Translate,
  yesNo,
} from "./pages";
import { TransactionCalldata, TransactionFailureReason } from "./TransactionCalldata";

function gasUsageValue(gasLimit: string, gasUsed: string | undefined, locale: string): string {
  const quantities = `${formatInteger(gasLimit, locale)} | ${formatInteger(gasUsed, locale)}`;
  const percentage = formatPercentageRatio(gasUsed, gasLimit, locale);
  return percentage ? `${quantities} (${percentage})` : quantities;
}

export function FeeSettings({
  entries,
  locale,
}: {
  entries: ReadonlyArray<{ label: string; value?: string }>;
  locale: string;
}) {
  return (
    <span className="transaction-fee-values">
      {entries.map((entry, index) => (
        <span className="transaction-fee-value" key={entry.label}>
          <span>
            <span className="transaction-fee-label">{entry.label}:</span>{" "}
            {entry.value === undefined ? "—" : `${formatGweiFromWei(entry.value, locale)} Gwei`}
          </span>
          {index < entries.length - 1 ? (
            <span className="transaction-fee-separator" aria-hidden="true">
              |
            </span>
          ) : null}
        </span>
      ))}
    </span>
  );
}

export function MempoolDetailExpired() {
  const { t } = useTranslation();
  return (
    <section className="capability-panel pending-unavailable" role="status">
      <span className="capability-mark" aria-hidden="true">
        !
      </span>
      <div>
        <h2>{t("pending.unavailable")}</h2>
        <p>{t("pending.expiredDetail")}</p>
        <code>snapshot_expired</code>
      </div>
    </section>
  );
}

function TransactionDetailRow({
  label,
  children,
  wide,
}: {
  label: string;
  children: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <div
      className={
        wide ? "transaction-detail-row detail-item wide" : "transaction-detail-row detail-item"
      }
    >
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

export function MempoolTransactionOverview({
  detail,
  locale,
  nativeDecimals,
  nativeSymbol,
}: {
  detail: Exclude<TransactionDetail, { kind: "included" }>;
  locale: string;
  nativeDecimals: number;
  nativeSymbol: string;
}) {
  const { t } = useTranslation();
  const transaction = detail.transaction;
  const pending = detail.kind === "pending";
  return (
    <div className="transaction-overview mempool-transaction-overview">
      <section
        className={`panel mempool-transaction-state ${pending ? "pending" : "replaced"}`}
        role="status"
      >
        <TransactionStatus
          label={t(pending ? "transactionStatus.pending" : "transactionStatus.replaced")}
          status={pending ? "pending" : "replaced"}
        />
        <div>
          <h2>{t(pending ? "detail.pendingTitle" : "detail.replacedTitle")}</h2>
          <p>{t(pending ? "detail.pendingDetail" : "detail.replacedDetail")}</p>
        </div>
      </section>
      <section
        className="panel transaction-detail-card"
        aria-label={t("detail.transactionSummary")}
      >
        <h2 className="sr-only">{t("detail.transactionSummary")}</h2>
        <dl className="transaction-detail-list">
          <TransactionDetailRow label={t("table.hash")}>
            <CopyableField value={transaction.hash}>
              <code>{transaction.hash}</code>
            </CopyableField>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("table.status")}>
            <TransactionStatus
              label={t(pending ? "transactionStatus.pending" : "transactionStatus.replaced")}
              status={pending ? "pending" : "replaced"}
            />
          </TransactionDetailRow>
          {!pending ? (
            <TransactionDetailRow label={t("detail.replacementHash")}>
              <CopyableField value={detail.replacement_hash}>
                <Link
                  to="/tx/$hash"
                  params={{ hash: detail.replacement_hash }}
                  search={{ tab: "overview" }}
                >
                  <code>{detail.replacement_hash}</code>
                </Link>
              </CopyableField>
            </TransactionDetailRow>
          ) : null}
          {transaction.replaces_hash ? (
            <TransactionDetailRow label={t("detail.replacesHash")}>
              <CopyableField value={transaction.replaces_hash}>
                <Link
                  to="/tx/$hash"
                  params={{ hash: transaction.replaces_hash }}
                  search={{ tab: "overview" }}
                >
                  <code>{transaction.replaces_hash}</code>
                </Link>
              </CopyableField>
            </TransactionDetailRow>
          ) : null}
          <TransactionDetailRow label={t("table.from")}>
            <AddressIdentity address={transaction.from} activity compact={false} copy />
          </TransactionDetailRow>
          <TransactionDetailRow label={t("table.to")}>
            {transaction.to ? (
              <AddressIdentity address={transaction.to} activity compact={false} copy />
            ) : (
              t("common.contractCreation")
            )}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("table.value", { symbol: nativeSymbol })}>
            <strong>
              {formatNativeAmount(transaction.value, locale, nativeDecimals)} {nativeSymbol}
            </strong>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.nonce")}>
            {formatInteger(transaction.nonce, locale)}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.type")}>
            {transactionTypeLabel(transaction.type, t)}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.gasLimit")}>
            {formatInteger(transaction.gas, locale)}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.gasFees")}>
            <FeeSettings
              locale={locale}
              entries={[
                { label: t("pending.gasPrice"), value: transaction.gas_price },
                { label: t("detail.feeMax"), value: transaction.max_fee_per_gas },
                { label: t("detail.feeMaxPriority"), value: transaction.max_priority_fee_per_gas },
              ]}
            />
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.firstSeen")}>
            <time dateTime={transaction.first_seen_at}>
              {formatTimestamp(transaction.first_seen_at, locale)}
            </time>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.lastSeen")}>
            <time dateTime={transaction.last_seen_at}>
              {formatTimestamp(transaction.last_seen_at, locale)}
            </time>
          </TransactionDetailRow>
          {!pending ? (
            <TransactionDetailRow label={t("detail.replacedAt")}>
              <time dateTime={detail.replaced_at}>
                {formatTimestamp(detail.replaced_at, locale)}
              </time>
            </TransactionDetailRow>
          ) : null}
          <TransactionDetailRow label={t("detail.expiresAt")}>
            <time dateTime={transaction.expires_at}>
              {formatTimestamp(transaction.expires_at, locale)}
            </time>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.endpoint")}>
            <code>{transaction.endpoint}</code>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.input")} wide>
            <textarea
              aria-label={t("detail.rawCalldataValue", { mode: t("detail.rawHex") })}
              className="transaction-calldata-raw-value transaction-data"
              readOnly
              rows={4}
              spellCheck={false}
              value={transaction.input}
              wrap="soft"
            />
          </TransactionDetailRow>
        </dl>
      </section>
    </div>
  );
}

export function TransactionStatusBadge({
  transaction,
  showFinality = true,
}: {
  transaction: TransactionSummary;
  showFinality?: boolean;
}) {
  const { t } = useTranslation();
  const state = !transaction.canonical
    ? "orphan"
    : transaction.status === "success"
      ? "success"
      : transaction.status === "failed"
        ? "failed"
        : "unknown";
  return (
    <span className="transaction-status-group">
      <TransactionStatus
        label={
          state === "orphan" ? t("detail.orphaned") : transactionStatusLabel(transaction.status, t)
        }
        status={state}
      />
      {showFinality && transaction.canonical && transaction.finality === "finalized" ? (
        <span className="finality-badge finalized">{finalityLabel(transaction.finality, t)}</span>
      ) : null}
    </span>
  );
}

export function IncludedTransactionStatus({ transaction }: { transaction: TransactionSummary }) {
  const { t } = useTranslation();
  const state: TransactionVisualStatus = !transaction.canonical
    ? "orphan"
    : transaction.status === "success"
      ? "success"
      : transaction.status === "failed"
        ? "failed"
        : transaction.status === "pending"
          ? "pending"
          : "unknown";
  return (
    <TransactionStatus
      label={
        state === "orphan" ? t("detail.orphaned") : transactionStatusLabel(transaction.status, t)
      }
      status={state}
    />
  );
}

type TransactionActionEvidence =
  | { state: "loading" }
  | { state: "unavailable" }
  | {
      state: "current";
      resolution: TransactionCalldataResource["execution"]["resolution"];
      decoded: boolean;
    };

export function resolveTransactionActionEvidence(
  hasRecipient: boolean,
  pending: boolean,
  fetching: boolean,
  error: unknown,
  resource: TransactionCalldataResource | undefined,
  identityCurrent: boolean,
  retryPending: boolean,
): TransactionActionEvidence {
  if (!hasRecipient) return { state: "unavailable" };
  if (pending || (!identityCurrent && (fetching || retryPending))) return { state: "loading" };
  if (error !== null || resource === undefined || !identityCurrent) return { state: "unavailable" };
  return {
    state: "current",
    resolution: resource.execution.resolution,
    decoded: resource.decoding.status === "decoded",
  };
}

function transactionActionLabel(
  transaction: TransactionSummary,
  evidence: TransactionActionEvidence,
  t: Translate,
): string {
  if (!transaction.to) return t("detail.actionContractCreation");
  if (evidence.state === "loading") return t("detail.actionDetermining");
  if (evidence.state === "unavailable") return t("detail.actionUnavailable");
  if (evidence.decoded) return t("detail.actionContractCall");
  if (evidence.resolution === "direct" || evidence.resolution === "eip7702_delegate") {
    return t("detail.actionContractCall");
  }
  if (evidence.resolution === "empty") {
    return transaction.input === "0x"
      ? t("detail.actionNativeTransfer")
      : t("detail.actionEOATransaction");
  }
  return t("detail.actionUnavailable");
}

export function TransactionOverviewPanel({
  transaction,
  transactionActionEvidence,
  tokenIdentityCurrent,
  tokenTransfers,
  failure,
  failureIdentityCurrent,
  failureIdentityRetryPending,
  calldata,
  calldataIdentityCurrent,
  locale,
  nativeDecimals,
  nativeSymbol,
  t,
}: {
  transaction: { data: Extract<TransactionDetail, { kind: "included" }>["transaction"] };
  transactionActionEvidence: TransactionActionEvidence;
  tokenIdentityCurrent: boolean;
  tokenTransfers: ReturnType<typeof useTransactionTokenTransfers>;
  failure: ReturnType<typeof useTransactionFailure>;
  failureIdentityCurrent: boolean;
  failureIdentityRetryPending: boolean;
  calldata: ReturnType<typeof useTransactionCalldata>;
  calldataIdentityCurrent: boolean;
  locale: string;
  nativeDecimals: number;
  nativeSymbol: string;
  t: Translate;
}) {
  return (
    <div className="transaction-overview" role="tabpanel">
      <section className="panel transaction-action-card" aria-labelledby="transaction-action-title">
        <div className="transaction-action-icon" aria-hidden="true">
          ↗
        </div>
        <div>
          <span id="transaction-action-title">{t("detail.transactionAction")}</span>
          <strong aria-live="polite">
            {transactionActionLabel(transaction.data, transactionActionEvidence, t)}
          </strong>
          <p>
            {formatNativeAmount(transaction.data.value, locale, nativeDecimals)} {nativeSymbol}
            {transaction.data.to ? (
              <>
                {" "}
                · <AddressIdentity address={transaction.data.to} />
              </>
            ) : null}
            {tokenIdentityCurrent && (tokenTransfers.data?.items.length ?? 0) > 0 ? (
              <>
                {" "}
                ·{" "}
                {t("detail.actionTokenEvents", {
                  count: tokenTransfers.data?.items.length ?? 0,
                })}
              </>
            ) : null}
          </p>
        </div>
      </section>

      <section
        className="panel transaction-detail-card"
        aria-label={t("detail.transactionSummary")}
      >
        <h2 className="sr-only">{t("detail.transactionSummary")}</h2>
        <dl className="transaction-detail-list">
          <TransactionDetailRow label={t("table.hash")}>
            <CopyableField value={transaction.data.hash}>
              <code>{transaction.data.hash}</code>
            </CopyableField>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("table.status")}>
            <TransactionStatusBadge transaction={transaction.data} />
          </TransactionDetailRow>
          {transaction.data.status === "failed" && (
            <TransactionDetailRow label={t("detail.failureReason")} wide>
              <TransactionFailureReason
                error={failure.error}
                identityCurrent={failureIdentityCurrent}
                loading={
                  failure.isPending ||
                  (!failureIdentityCurrent && (failure.isFetching || failureIdentityRetryPending))
                }
                resource={failure.data}
              />
            </TransactionDetailRow>
          )}
          <TransactionDetailRow label={t("table.block")}>
            {transaction.data.block_hash ? (
              <span className="transaction-inline-values">
                <Link to="/blocks/$blockID" params={{ blockID: transaction.data.block_hash }}>
                  {formatInteger(transaction.data.block_number, locale)}
                </Link>
                {transaction.data.confirmations && (
                  <span className="transaction-confirmations">
                    {t("detail.confirmationCount", {
                      count: formatInteger(transaction.data.confirmations, locale),
                    })}
                  </span>
                )}
              </span>
            ) : (
              "—"
            )}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.blockTimestamp")}>
            {transaction.data.block_timestamp ? (
              <time dateTime={transaction.data.block_timestamp}>
                {formatTimestamp(transaction.data.block_timestamp, locale)}
              </time>
            ) : (
              "—"
            )}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("table.from")}>
            <AddressIdentity address={transaction.data.from} compact={false} copy />
          </TransactionDetailRow>
          <TransactionDetailRow label={t("table.to")}>
            {transaction.data.to ? (
              <AddressIdentity address={transaction.data.to} compact={false} copy />
            ) : transaction.data.contract_address ? (
              <span className="transaction-inline-values">
                <AddressIdentity address={transaction.data.contract_address} compact={false} copy />
                <span className="transaction-creation-label">{t("common.contractCreation")}</span>
              </span>
            ) : (
              t("common.contractCreation")
            )}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("table.value", { symbol: nativeSymbol })}>
            <strong>
              {formatNativeAmount(transaction.data.value, locale, nativeDecimals)} {nativeSymbol}
            </strong>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.transactionFee")}>
            {formatNativeAmount(transaction.data.tx_fee_wei, locale, nativeDecimals)} {nativeSymbol}
          </TransactionDetailRow>
        </dl>
        <details className="transaction-more-details">
          <summary>{t("detail.moreDetails")}</summary>
          <dl className="transaction-detail-list advanced">
            <TransactionDetailRow label={t("detail.nonce")}>
              {formatInteger(transaction.data.nonce, locale)}
            </TransactionDetailRow>
            <TransactionDetailRow label={t("detail.type")}>
              {transactionTypeLabel(transaction.data.type, t)}
            </TransactionDetailRow>
            <TransactionDetailRow label={t("detail.gasLimitAndUsage")}>
              {gasUsageValue(transaction.data.gas, transaction.data.gas_used, locale)}
            </TransactionDetailRow>
            <TransactionDetailRow label={t("detail.gasFees")}>
              <FeeSettings
                locale={locale}
                entries={[
                  { label: t("detail.feeBase"), value: transaction.data.base_fee_per_gas },
                  {
                    label: t("detail.feeMax"),
                    value: transaction.data.max_fee_per_gas ?? transaction.data.gas_price,
                  },
                  {
                    label: t("detail.feeMaxPriority"),
                    value: transaction.data.max_priority_fee_per_gas ?? transaction.data.gas_price,
                  },
                ]}
              />
            </TransactionDetailRow>
            {transaction.data.type === "3" ? (
              <TransactionDetailRow label={t("detail.blobGasFees")}>
                <FeeSettings
                  locale={locale}
                  entries={[
                    {
                      label: t("detail.blobBaseFee"),
                      value: transaction.data.blob_base_fee_per_gas,
                    },
                    { label: t("detail.feeMax"), value: transaction.data.max_fee_per_blob_gas },
                  ]}
                />
              </TransactionDetailRow>
            ) : null}
            <TransactionDetailRow label={t("detail.effectiveGasPrice")}>
              {formatGweiFromWei(transaction.data.effective_gas_price, locale)}
            </TransactionDetailRow>
            <TransactionDetailRow label={t("detail.burned")}>
              {formatNativeAmount(transaction.data.burned_wei, locale, nativeDecimals)}{" "}
              {nativeSymbol}
            </TransactionDetailRow>
            <TransactionDetailRow label={t("detail.canonical")}>
              {yesNo(transaction.data.canonical, t)}
            </TransactionDetailRow>
            <TransactionDetailRow label={t("table.finality")}>
              {finalityLabel(transaction.data.finality, t)}
            </TransactionDetailRow>
            <TransactionDetailRow label={t("detail.input")} wide>
              <TransactionCalldata
                identityCurrent={calldataIdentityCurrent}
                loading={calldata.isPending}
                resource={calldata.data}
                transaction={transaction.data}
              />
            </TransactionDetailRow>
          </dl>
        </details>
      </section>
    </div>
  );
}
