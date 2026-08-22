import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { hexToBytes, type Hex } from "viem";

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
  TransactionLog,
  TransactionCalldata as TransactionCalldataResource,
  TransactionFailure as TransactionFailureResource,
  TransactionSummary,
} from "@/api/types";
import {
  formatGweiFromWei,
  formatInteger,
  formatNativeAmount,
  formatPercentageRatio,
  formatTimestamp,
  shorten,
} from "@/components/format";
import { CopyButton, CopyableField } from "@/components/CopyButton";
import { AddressIdentity } from "@/ens/AddressIdentity";
import {
  TransactionStatus,
  type TransactionVisualStatus,
} from "@/components/TransactionStatus";
import { QueryNotice } from "@/components/QueryNotice";
import { flattenFailureArguments } from "@/components/failureFormat";
import {
  flattenLogArgument,
  formatTopicValue,
  isAnonymousDecodedLog,
  type LogArgumentRow,
  type TopicDisplayMode,
} from "@/components/logFormat";
import {
  formatTransactionCalldataInputs,
  type FormattedAbiField,
  type FormattedAbiOutput,
  type FormattedAbiValue,
} from "@/contracts/abi";
import {
  CapabilityDegraded,
  CursorPagination,
  Detail,
  DetailList,
  NFTTokenIDLink,
  Page,
  ReorgContext,
  abiSourceKindLabel,
  attributionLabel,
  confidenceLabel,
  finalityLabel,
  formatLogArgument,
  formatTokenEventAmount,
  isNFTStandard,
  logDecodingKey,
  traceDecodingKey,
  traceOutputStatusKey,
  transactionStatusLabel,
  transactionTypeLabel,
  type Translate,
  useCursorHistory,
  yesNo,
} from "./pages";

const TRANSACTION_TABS = ["overview", "access-list", "blob", "authorizations", "internal-transactions", "token-transfers", "logs", "trace", "state-changes"] as const;
type TransactionTab = typeof TRANSACTION_TABS[number];

function transactionTabsForType(type?: string): TransactionTab[] {
  const tabs: TransactionTab[] = ["overview"];
  if (type === "1" || type === "2" || type === "3" || type === "4") tabs.push("access-list");
  if (type === "3") tabs.push("blob");
  if (type === "4") tabs.push("authorizations");
  tabs.push("internal-transactions", "token-transfers", "logs", "trace", "state-changes");
  return tabs;
}

function gasUsageValue(gasLimit: string, gasUsed: string | undefined, locale: string): string {
  const quantities = `${formatInteger(gasLimit, locale)} | ${formatInteger(gasUsed, locale)}`;
  const percentage = formatPercentageRatio(gasUsed, gasLimit, locale);
  return percentage ? `${quantities} (${percentage})` : quantities;
}

function FeeSettings({ entries, locale }: {
  entries: ReadonlyArray<{ label: string; value?: string }>;
  locale: string;
}) {
  return <span className="transaction-fee-values">
    {entries.map((entry, index) => (
      <span className="transaction-fee-value" key={entry.label}>
        <span><span className="transaction-fee-label">{entry.label}:</span>{" "}
          {entry.value === undefined ? "—" : `${formatGweiFromWei(entry.value, locale)} Gwei`}
        </span>
        {index < entries.length - 1 ? <span className="transaction-fee-separator" aria-hidden="true">|</span> : null}
      </span>
    ))}
  </span>;
}

function calldataStructName(internalType: string | undefined): string | undefined {
  if (!internalType?.startsWith("struct ")) return undefined;
  const name = internalType.slice("struct ".length).replace(/\[[0-9]*\]/gu, "");
  return name.split(".").at(-1) || undefined;
}

function calldataDisplayType(
  type: string,
  internalType: string | undefined,
  kind: FormattedAbiValue["kind"],
): string {
  const structName = calldataStructName(internalType);
  if (kind === "tuple" && structName) return structName;
  if (kind === "array" && structName) return `${structName}${type.slice("tuple".length)}`;
  return type;
}

function calldataScalarValue(value: string, type: string, locale: string): string {
  const baseType = type.replace(/\[[0-9]*\]/gu, "");
  if (typeof value === "string" && /^(?:u?int)(?:[0-9]*)$/u.test(baseType)) {
    return formatInteger(value, locale);
  }
  return value;
}

function CalldataValueTree({
  signature,
  args,
  locale,
  columnLabels,
  itemCountLabel,
}: {
  signature: string;
  args: readonly FormattedAbiOutput[];
  locale: string;
  columnLabels: Readonly<{ index: string; params: string; type: string; data: string }>;
  itemCountLabel: (count: number) => string;
}) {
  return (
    <div className="calldata-value-tree">
      <div className="calldata-table-scroll">
        <div className="calldata-table" role="group" aria-label={signature}>
          <div className="calldata-table-row calldata-table-header">
            <span>{columnLabels.index}</span>
            <span>{columnLabels.params}</span>
            <span>{columnLabels.type}</span>
            <span>{columnLabels.data}</span>
          </div>
        {args.map((argument) => (
          <CalldataField
            field={argument}
            key={`${argument.index}:${argument.name}:${argument.type}`}
            locale={locale}
            depth={0}
            rowIndex={String(argument.index + 1)}
            itemCountLabel={itemCountLabel}
          />
        ))}
        </div>
      </div>
    </div>
  );
}

function CalldataField({
  field,
  locale,
  depth,
  label,
  prefix,
  rowIndex,
  itemCountLabel,
}: {
  field: FormattedAbiField;
  locale: string;
  depth: number;
  label?: string;
  prefix?: string;
  rowIndex?: string;
  itemCountLabel: (count: number) => string;
}) {
  return (
    <CalldataValueNode
      label={label ?? (prefix ? `${prefix}.${field.name || `#${field.index}`}` : field.name || `#${field.index}`)}
      locale={locale}
      depth={depth}
      type={field.type}
      internalType={field.internalType}
      value={field.value}
      rowIndex={rowIndex}
      itemCountLabel={itemCountLabel}
    />
  );
}

function CalldataValueNode({
  label,
  locale,
  depth,
  type,
  internalType,
  value,
  rowIndex,
  itemCountLabel,
}: {
  label: string;
  locale: string;
  depth: number;
  type: string;
  internalType?: string;
  value: FormattedAbiValue;
  rowIndex?: string;
  itemCountLabel: (count: number) => string;
}) {
  if (value.kind === "scalar") {
    return (
      <div className={`calldata-table-row calldata-scalar calldata-depth-${Math.min(depth, 6)}`}>
        <span className="calldata-row-index">{rowIndex}</span>
        <span className="calldata-row-name">{label}</span>
        <small className="calldata-row-type">{type}</small>
        <code className="calldata-row-data">{calldataScalarValue(value.text, type, locale)}</code>
      </div>
    );
  }

  const displayType = calldataDisplayType(value.type, value.internalType ?? internalType, value.kind);
  const itemCount = value.kind === "array" ? value.items.length : value.fields.length;
  return (
    <details
      className={`calldata-composite calldata-${value.kind} calldata-depth-${Math.min(depth, 6)}`}
      open={depth < 3}
    >
      <summary
        className="calldata-table-row calldata-node-summary"
        onKeyDown={(event) => {
          if (event.key !== "Enter" && event.key !== " ") return;
          event.preventDefault();
          const details = event.currentTarget.parentElement;
          if (details instanceof HTMLDetailsElement) details.open = !details.open;
        }}
      >
        <span className="calldata-row-index">{rowIndex}</span>
        <span className="calldata-row-name">{label}</span>
        <small className="calldata-row-type">{displayType}</small>
        <span className="calldata-row-data calldata-item-count">
          {value.kind === "array" ? itemCountLabel(itemCount) : ""}
        </span>
      </summary>
      <div className="calldata-tree-children">
        {value.kind === "array"
          ? value.items.map((item, index) => (
              <CalldataValueNode
                key={`${label}:${index}`}
                label={`#${index}`}
                locale={locale}
                depth={depth + 1}
                type={item.type}
                internalType={item.internalType}
                value={item}
                rowIndex=""
                itemCountLabel={itemCountLabel}
              />
            ))
          : value.fields.map((child) => (
              <CalldataField
                field={child}
                key={`${label}:${child.index}:${child.name}:${child.type}`}
                locale={locale}
                depth={depth + 1}
                prefix={depth === 0 ? label : undefined}
                rowIndex=""
                itemCountLabel={itemCountLabel}
              />
            ))}
      </div>
    </details>
  );
}

function TransactionCalldata({
  transaction,
  resource,
  loading,
  identityCurrent,
}: {
  transaction: TransactionSummary;
  resource?: TransactionCalldataResource;
  loading: boolean;
  identityCurrent: boolean;
}) {
  const { i18n, t } = useTranslation();
  const decodedHeadingID = useId();
  const rawHeadingID = useId();
  const input = transaction.input;
  const targetAddress = transaction.to ?? "";
  const hasCalldata = input.length > 2;
  const enabled = hasCalldata && targetAddress.length > 0;
  const resourceCurrent = identityCurrent && resource !== undefined
    && resource.input.toLowerCase() === input.toLowerCase()
    && resource.execution.context_address.toLowerCase() === targetAddress.toLowerCase();
  const decoding = resourceCurrent ? resource.decoding : undefined;
  const decodedArgs = useMemo(() => {
    if (decoding?.status !== "decoded") return undefined;
    try {
      return formatTransactionCalldataInputs(decoding.inputs);
    } catch {
      return null;
    }
  }, [decoding]);
  const [rawMode, setRawMode] = useState<"hex" | "utf8">("hex");
  const [utf8Unavailable, setUtf8Unavailable] = useState(false);
  useEffect(() => {
    void input;
    setRawMode("hex");
    setUtf8Unavailable(false);
  }, [input]);
  const utf8 = useMemo(() => {
    if (!/^0x(?:[0-9a-f]{2})*$/iu.test(input)) return undefined;
    try {
      return new TextDecoder("utf-8", { fatal: true }).decode(hexToBytes(input as Hex));
    } catch {
      return undefined;
    }
  }, [input]);
  const loadingABI = enabled && loading;
  const displayValue = rawMode === "utf8" && utf8 !== undefined ? utf8 : input;
  const toggleRawMode = () => {
    if (rawMode === "utf8") {
      setRawMode("hex");
      setUtf8Unavailable(false);
      return;
    }
    if (utf8 === undefined) {
      setUtf8Unavailable(true);
      return;
    }
    setRawMode("utf8");
    setUtf8Unavailable(false);
  };

  return (
    <div className="transaction-calldata">
      {enabled && <section className="transaction-calldata-decoded" aria-labelledby={decodedHeadingID}>
        <h3 className="transaction-calldata-heading" id={decodedHeadingID}>
          {t("detail.calldataDecoded")}
          {enabled && !loadingABI && decoding?.status === "decoded" && decoding.signature && (
            <> · <code>{decoding.signature}</code></>
          )}
        </h3>
        {enabled && loadingABI && <p className="quiet" role="status">{t("detail.calldataDecodeLoading")}</p>}
        {enabled && !loadingABI && decoding?.status === "decoded" && decoding.signature && resource && (
          <>
            <div className="calldata-abi-sources" aria-label={t("detail.calldataAbiEvidence")}>
              <div className="calldata-abi-source">
                <span>{t("detail.calldataExecutionEvidence")}</span>
                <span aria-hidden="true">·</span>
                <span>{t(`detail.executionResolution.${resource.execution.resolution}`)}</span>
                {resource.execution.address && <>
                  <span aria-hidden="true">·</span>
                  <AddressIdentity address={resource.execution.address} compact={false} copy />
                </>}
              </div>
              {decoding.abi_source?.address && (
                <div className="calldata-abi-source">
                  <span>{t("detail.calldataAbiSource", { kind: decoding.abi_source.kind })}</span>
                  <span aria-hidden="true">·</span>
                  <AddressIdentity address={decoding.abi_source.address} compact={false} copy />
                </div>
              )}
            </div>
            {decodedArgs === null ? (
              <p className="capability-panel" role="status">{t("detail.calldataStructureUnavailable")}</p>
            ) : decodedArgs && decodedArgs.length > 0 ? (
              <CalldataValueTree
                args={decodedArgs}
                columnLabels={{
                  index: t("detail.calldataIndex"),
                  params: t("detail.calldataParams"),
                  type: t("detail.calldataType"),
                  data: t("detail.calldataData"),
                }}
                itemCountLabel={(count) => t("detail.calldataArrayItems", { count })}
                locale={i18n.resolvedLanguage ?? "en"}
                signature={decoding.signature}
              />
            ) : <p className="quiet">{t("detail.calldataNoParameters")}</p>}
          </>
        )}
        {enabled && !loadingABI && decoding?.status !== "decoded" && (
          <p className="capability-panel" role="status">
            {t(!identityCurrent || resource !== undefined && !resourceCurrent
              ? "state.transactionIdentityChanged"
              : decoding?.status === "not_applicable"
                ? "detail.calldataNoExecutionCode"
                : decoding?.status === "unknown"
                  ? "detail.calldataUnknownSelector"
                  : decoding?.status === "malformed"
                    ? "detail.calldataMalformed"
                    : decoding?.status === "ambiguous"
                      ? "detail.calldataAmbiguous"
                      : "detail.calldataUnavailable")}
            {decoding?.status === "ambiguous" && decoding.candidates.length > 0
              ? <> · <code>{decoding.candidates.join(" · ")}</code></>
              : null}
          </p>
        )}
      </section>}
      <section className="transaction-calldata-raw" aria-labelledby={rawHeadingID}>
        <header className="transaction-calldata-raw-header">
          <h3 className="transaction-calldata-heading" id={rawHeadingID}>{t("detail.rawCalldata")}</h3>
          <div className="transaction-calldata-raw-actions">
            <button
              className="transaction-calldata-mode-link"
              onClick={toggleRawMode}
              type="button"
            >{t(rawMode === "hex" ? "detail.rawViewAsUtf8" : "detail.rawViewAsHex")}</button>
            <CopyButton value={input} />
          </div>
        </header>
        {utf8Unavailable && (
          <p className="quiet transaction-calldata-raw-status" role="status">{t("detail.rawUtf8Unavailable")}</p>
        )}
        <textarea
          aria-label={t("detail.rawCalldataValue", { mode: t(rawMode === "hex" ? "detail.rawHex" : "detail.rawUtf8") })}
          className="transaction-calldata-raw-value transaction-data"
          readOnly
          rows={4}
          spellCheck={false}
          value={displayValue}
          wrap="soft"
        />
      </section>
    </div>
  );
}

function TransactionFailureReason({
  resource,
  loading,
  error,
  identityCurrent,
}: {
  resource?: TransactionFailureResource;
  loading: boolean;
  error?: unknown;
  identityCurrent: boolean;
}) {
  const { t } = useTranslation();
  const builtin = resource?.decoding.status === "decoded"
    && resource.decoding.abi_source?.kind === "builtin";
  const arguments_ = useMemo(() => {
    if (!resource || resource.decoding.status !== "decoded"
      || resource.decoding.abi_source?.kind === "builtin") return undefined;
    try {
      return flattenFailureArguments(formatTransactionCalldataInputs(resource.decoding.arguments));
    } catch {
      return null;
    }
  }, [resource]);

  if (loading) return <QueryNotice compact loading />;
  if (error) return <QueryNotice compact error={error} />;
  if (!resource || !identityCurrent) {
    return <p className="capability-panel" role="status">{t("state.transactionIdentityChanged")}</p>;
  }

  const decoded = resource.decoding.status === "decoded" && arguments_ !== null;
  const directError = builtin ? resource.decoding.reason ?? resource.error : undefined;
  return (
    <section className="transaction-failure" aria-label={t("detail.failureReason")}>
      {directError !== undefined ? (
        <CopyableField value={directError}><code>{directError}</code></CopyableField>
      ) : decoded && resource.decoding.signature ? (
        <strong className="transaction-failure-signature"><code>{resource.decoding.signature}</code></strong>
      ) : (
        <CopyableField value={resource.error}><code>{resource.error}</code></CopyableField>
      )}
      {arguments_ && arguments_.rows.length > 0 && (
        <div className="transaction-failure-table-scroll" tabIndex={0}>
          <div className="transaction-failure-table" role="table" aria-label={t("detail.failureArguments")}>
            <div className="transaction-failure-row transaction-failure-header" role="row">
              <span role="columnheader">{t("detail.argumentName")}</span>
              <span role="columnheader">{t("detail.argumentType")}</span>
              <span role="columnheader">{t("detail.argumentData")}</span>
            </div>
            {arguments_.rows.map((row, index) => (
              <div className="transaction-failure-row" role="row" key={`${row.path}:${index}`}>
                <code className="transaction-failure-name" role="cell">{row.path}</code>
                <code className="transaction-failure-type" role="cell">{row.type}</code>
                <span className="transaction-failure-data" role="cell">
                  <CopyableField value={row.data}><code>{row.data}</code></CopyableField>
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
      {arguments_?.truncated && (
        <p className="quiet transaction-failure-warning" role="status">{t("detail.failureArgumentsTruncated")}</p>
      )}
      {arguments_ === null && (
        <p className="capability-panel" role="status">{t("detail.failureStructureUnavailable")}</p>
      )}
      {resource.decoding.warning && (
        <p className="quiet transaction-failure-warning">{resource.decoding.warning}</p>
      )}
      {!builtin && resource.revert_data !== undefined && (
        <details className="transaction-more-details transaction-failure-raw">
          <summary>{t("detail.revertData")}</summary>
          <CopyableField value={resource.revert_data}><code>{resource.revert_data}</code></CopyableField>
        </details>
      )}
    </section>
  );
}

function TransactionAccessListPanel({
  transaction,
  t,
}: {
  transaction: TransactionSummary;
  t: Translate;
}) {
  return (
    <section className="panel transaction-tab-panel" role="tabpanel">
      <h2>{t("detail.accessList")}</h2>
      {transaction.access_list?.length ? (
        <div className="transaction-log-list">
          {transaction.access_list.map((entry) => (
            <article className="transaction-log" key={entry.address}>
              <header><strong><AddressIdentity address={entry.address} compact={false} /></strong></header>
              {entry.storage_keys.length > 0 ? (
                <dl>
                  {entry.storage_keys.map((key) => (
                    <div key={key}><dt>{t("detail.storageKey")}</dt><dd><code>{key}</code></dd></div>
                  ))}
                </dl>
              ) : <p className="quiet">{t("state.noStorageKeys")}</p>}
            </article>
          ))}
        </div>
      ) : <p className="empty-result">{t("state.emptyAccessList")}</p>}
    </section>
  );
}

function TransactionBlobPanel({
  transaction,
  locale,
  t,
}: {
  transaction: TransactionSummary;
  locale: string;
  t: Translate;
}) {
  return (
    <section className="panel transaction-tab-panel" role="tabpanel">
      <h2>{t("detail.blobData")}</h2>
      <DetailList label={t("detail.blobData")}>
        <Detail label={t("detail.blobGasFees")} value={(
          <FeeSettings locale={locale} entries={[
            { label: t("detail.blobBaseFee"), value: transaction.blob_base_fee_per_gas },
            { label: t("detail.feeMax"), value: transaction.max_fee_per_blob_gas },
          ]} />
        )} />
        <Detail label={t("detail.blobCount")} value={formatInteger(transaction.blob_versioned_hashes?.length ?? 0, locale)} />
      </DetailList>
      {transaction.blob_versioned_hashes?.length ? (
        <div className="transaction-log-list">
          {transaction.blob_versioned_hashes.map((hash, index) => (
            <article className="transaction-log" key={hash}>
              <header><strong>{t("detail.blobIndex", { index })}</strong></header>
              <code className="mono-wrap">{hash}</code>
            </article>
          ))}
        </div>
      ) : <p className="empty-result">{t("state.noBlobHashes")}</p>}
    </section>
  );
}

function TransactionAuthorizationsPanel({
  authorizations,
  identityCurrent,
  pager,
  t,
}: {
  authorizations: ReturnType<typeof useTransactionAuthorizations>;
  identityCurrent: boolean;
  pager: ReturnType<typeof useCursorHistory>;
  t: Translate;
}) {
  return (
    <section className="panel transaction-tab-panel" role="tabpanel">
      <QueryNotice loading={authorizations.isPending} error={authorizations.error} />
      {authorizations.data && !identityCurrent ? (
        <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
      ) : null}
      {identityCurrent && authorizations.data?.state !== "complete" && authorizations.data ? (
        <CapabilityDegraded stage="state_diff" state={authorizations.data.state} />
      ) : null}
      {identityCurrent && authorizations.data?.state === "complete" && authorizations.data.items.length === 0 ? (
        <p className="empty-result">{t("state.noAuthorizations")}</p>
      ) : null}
      {identityCurrent && authorizations.data?.state === "complete" && authorizations.data.items.length > 0 ? (
        <div className="transaction-log-list">
          {authorizations.data.items.map((authorization) => (
            <article className="transaction-log" key={authorization.index}>
              <header>
                <strong>{t("detail.authorizationIndex", { index: authorization.index })}</strong>
                <span>{authorization.application_status}</span>
              </header>
              <dl>
                <div><dt>{t("delegation.authority")}</dt><dd><code>{authorization.authority ?? "—"}</code></dd></div>
                <div><dt>{t("delegation.delegate")}</dt><dd><code>{authorization.delegate}</code></dd></div>
                <div><dt>{t("detail.chainID")}</dt><dd><code>{authorization.chain_id}</code></dd></div>
                <div><dt>{t("detail.nonce")}</dt><dd><code>{authorization.nonce}</code></dd></div>
                <div><dt>{t("detail.signatureStatus")}</dt><dd>{authorization.signature_status}</dd></div>
                <div><dt>{t("detail.skipReason")}</dt><dd>{authorization.skip_reason ?? "—"}</dd></div>
              </dl>
              <details className="transaction-more-details">
                <summary>{t("detail.rawAuthorization")}</summary>
                <dl>
                  <div><dt>yParity</dt><dd><code>{authorization.y_parity}</code></dd></div>
                  <div><dt>r</dt><dd><code>{authorization.r}</code></dd></div>
                  <div><dt>s</dt><dd><code>{authorization.s}</code></dd></div>
                </dl>
              </details>
            </article>
          ))}
        </div>
      ) : null}
      {identityCurrent && authorizations.data ? (
        <CursorPagination
          busy={authorizations.isFetching}
          hasNext={Boolean(authorizations.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("transactionTabs.authorizations")}
          onNext={() => pager.next(authorizations.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      ) : null}
    </section>
  );
}

export function TransactionDetailPage({ hash, tab }: { hash: string; tab: string }) {
  const { i18n, t } = useTranslation();
  const navigate = useNavigate();
  const transactionDetail = useTransaction(hash);
  const [mempoolDetailExpired, setMempoolDetailExpired] = useState(false);
  const observedDetail = transactionDetail.data;
  const observedExpiry = observedDetail?.kind === "pending" || observedDetail?.kind === "replaced"
    ? Date.parse(observedDetail.transaction.expires_at)
    : Number.NaN;
  const detailExpired = mempoolDetailExpired
    || (Number.isFinite(observedExpiry) && observedExpiry <= Date.now());
  const detail = transactionDetail.error || detailExpired ? undefined : observedDetail;
  const included = detail?.kind === "included";
  const transaction = detail?.kind === "included"
    ? { ...transactionDetail, data: detail.transaction }
    : { ...transactionDetail, data: undefined };
  const transactionTabs = transactionTabsForType(transaction.data?.type);
  const activeTab: TransactionTab = transactionTabs.includes(tab as TransactionTab)
    ? tab as TransactionTab
    : "overview";
  const calldataEnabled = included && activeTab === "overview"
    && Boolean(transaction.data?.to);
  const calldata = useTransactionCalldata(hash, calldataEnabled);
  const failureEnabled = included && activeTab === "overview"
    && transaction.data?.status === "failed";
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
  const publicConfig = usePublicConfig();
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";
  const locale = i18n.resolvedLanguage ?? "en";
  const lastIdentityRetry = useRef("");
  const identityMatches = (blockHash?: string) =>
    !blockHash || !transaction.data?.block_hash || blockHash === transaction.data.block_hash;
  const tokenIdentityCurrent = identityMatches(tokenTransfers.data?.block_hash);
  const internalIdentityCurrent = identityMatches(internalTransactions.data?.block_hash);
  const calldataIdentityCurrent = calldata.data === undefined || transaction.data === undefined
    || transactionCalldataIdentityMatches(transaction.data, calldata.data);
  const calldataIdentityRetryKey = !calldataIdentityCurrent && transaction.data && calldata.data
    ? transactionCalldataRetryKey(transaction.data, calldata.data)
    : undefined;
  const calldataIdentityRetryPending = calldataIdentityRetryKey !== undefined
    && lastIdentityRetry.current !== calldataIdentityRetryKey;
  const failureIdentityCurrent = failure.data === undefined || transaction.data === undefined
    || transactionFailureIdentityMatches(transaction.data, failure.data);
  const failureIdentityRetryKey = !failureIdentityCurrent && transaction.data && failure.data
    ? transactionFailureRetryKey(transaction.data, failure.data)
    : undefined;
  const failureIdentityRetryPending = failureIdentityRetryKey !== undefined
    && lastIdentityRetry.current !== failureIdentityRetryKey;
  const transactionActionEvidence: TransactionActionEvidence = !transaction.data?.to
    ? { state: "unavailable" }
    : calldata.isPending || !calldataIdentityCurrent
      && (calldata.isFetching || calldataIdentityRetryPending)
      ? { state: "loading" }
      : calldata.error !== null || calldata.data === undefined || !calldataIdentityCurrent
        ? { state: "unavailable" }
        : {
            state: "current",
            resolution: calldata.data.execution.resolution,
            decoded: calldata.data.decoding.status === "decoded",
          };
  const logIdentityCurrent = identityMatches(logs.data?.block_hash);
  const traceIdentityCurrent = identityMatches(trace.data?.block_hash);
  const stateIdentityCurrent = identityMatches(stateChanges.data?.block_hash);
  const authorizationIdentityCurrent = identityMatches(authorizations.data?.block_hash);
  useEffect(() => {
    if (activeTab === "access-list" || activeTab === "blob") return;
    const resource = activeTab === "token-transfers" || activeTab === "overview"
      ? tokenTransfers
      : activeTab === "internal-transactions" ? internalTransactions
      : activeTab === "logs" ? logs
      : activeTab === "trace" ? trace
      : activeTab === "authorizations" ? authorizations
      : stateChanges;
    const resourceBlockHash = resource.data?.block_hash;
    const overviewBlockHash = transaction.data?.block_hash;
    if (!resourceBlockHash || !overviewBlockHash || resourceBlockHash === overviewBlockHash) return;
    const retryKey = `${activeTab}:${overviewBlockHash}:${resourceBlockHash}`;
    if (lastIdentityRetry.current === retryKey) return;
    lastIdentityRetry.current = retryKey;
    void Promise.all([transaction.refetch(), resource.refetch()]);
  }, [
    activeTab,
    authorizations,
    internalTransactions,
    logs,
    stateChanges,
    tokenTransfers,
    trace,
    transaction,
  ]);
  useEffect(() => {
    if (!calldataEnabled || calldataIdentityCurrent) return;
    if (calldataIdentityRetryKey === undefined
      || lastIdentityRetry.current === calldataIdentityRetryKey) return;
    lastIdentityRetry.current = calldataIdentityRetryKey;
    void Promise.all([transaction.refetch(), calldata.refetch()]);
  }, [
    calldata,
    calldataEnabled,
    calldataIdentityCurrent,
    calldataIdentityRetryKey,
    transaction,
  ]);
  useEffect(() => {
    if (!failureEnabled || failureIdentityCurrent || failureIdentityRetryKey === undefined ||
      lastIdentityRetry.current === failureIdentityRetryKey) return;
    lastIdentityRetry.current = failureIdentityRetryKey;
    void Promise.all([transaction.refetch(), failure.refetch()]);
  }, [
    failure,
    failureEnabled,
    failureIdentityCurrent,
    failureIdentityRetryKey,
    transaction,
  ]);
  const stateGroups = useMemo(() => {
    const groups = new Map<string, NonNullable<typeof stateChanges.data>["items"]>();
    for (const change of stateChanges.data?.items ?? []) {
      const group = groups.get(change.address) ?? [];
      group.push(change);
      groups.set(change.address, group);
    }
    return [...groups.entries()];
  }, [stateChanges.data]);

  useEffect(() => {
    if (!Number.isFinite(observedExpiry)) {
      setMempoolDetailExpired(false);
      return;
    }
    if (observedExpiry <= Date.now()) {
      setMempoolDetailExpired(true);
      return;
    }
    setMempoolDetailExpired(false);
    const maximumDelay = 2_147_483_647;
    const timer = window.setTimeout(
      () => setMempoolDetailExpired(true),
      Math.min(observedExpiry - Date.now(), maximumDelay),
    );
    return () => window.clearTimeout(timer);
  }, [observedExpiry]);

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
          <nav className="transaction-tabs" role="tablist" aria-label={t("detail.transactionSections")}>
            {transactionTabs.map((tabID) => (
              <Link
                aria-selected={activeTab === tabID}
                className={activeTab === tabID ? "transaction-tab active" : "transaction-tab"}
                key={tabID}
                onKeyDown={(event) => {
                  if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
                  event.preventDefault();
                  const currentIndex = transactionTabs.indexOf(tabID);
                  const nextIndex = event.key === "Home" ? 0
                    : event.key === "End" ? transactionTabs.length - 1
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
            <div className="transaction-overview" role="tabpanel">
              <section className="panel transaction-action-card" aria-labelledby="transaction-action-title">
                <div className="transaction-action-icon" aria-hidden="true">↗</div>
                <div>
                  <span id="transaction-action-title">{t("detail.transactionAction")}</span>
                  <strong aria-live="polite">
                    {transactionActionLabel(transaction.data, transactionActionEvidence, t)}
                  </strong>
                  <p>
                    {formatNativeAmount(transaction.data.value, locale, nativeDecimals)} {nativeSymbol}
                    {transaction.data.to ? (
                      <> · <AddressIdentity address={transaction.data.to} /></>
                    ) : null}
                    {tokenIdentityCurrent && (tokenTransfers.data?.items.length ?? 0) > 0
                      ? <> · {t("detail.actionTokenEvents", {
                          count: tokenTransfers.data?.items.length ?? 0,
                        })}</>
                      : null}
                  </p>
                </div>
              </section>

              <section className="panel transaction-detail-card" aria-label={t("detail.transactionSummary")}>
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
                        loading={failure.isPending || !failureIdentityCurrent &&
                          (failure.isFetching || failureIdentityRetryPending)}
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
                    ) : "—"}
                  </TransactionDetailRow>
                  <TransactionDetailRow label={t("detail.blockTimestamp")}>
                    {transaction.data.block_timestamp
                      ? <time dateTime={transaction.data.block_timestamp}>
                          {formatTimestamp(transaction.data.block_timestamp, locale)}
                        </time>
                      : "—"}
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
                        <span className="transaction-creation-label">
                          {t("common.contractCreation")}
                        </span>
                      </span>
                    ) : t("common.contractCreation")}
                  </TransactionDetailRow>
                  <TransactionDetailRow label={t("table.value", { symbol: nativeSymbol })}>
                    <strong>{formatNativeAmount(transaction.data.value, locale, nativeDecimals)} {nativeSymbol}</strong>
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
                      <FeeSettings locale={locale} entries={[
                        { label: t("detail.feeBase"), value: transaction.data.base_fee_per_gas },
                        { label: t("detail.feeMax"), value: transaction.data.max_fee_per_gas ?? transaction.data.gas_price },
                        { label: t("detail.feeMaxPriority"), value: transaction.data.max_priority_fee_per_gas ?? transaction.data.gas_price },
                      ]} />
                    </TransactionDetailRow>
                    {transaction.data.type === "3" ? (
                      <TransactionDetailRow label={t("detail.blobGasFees")}>
                        <FeeSettings locale={locale} entries={[
                          { label: t("detail.blobBaseFee"), value: transaction.data.blob_base_fee_per_gas },
                          { label: t("detail.feeMax"), value: transaction.data.max_fee_per_blob_gas },
                        ]} />
                      </TransactionDetailRow>
                    ) : null}
                    <TransactionDetailRow label={t("detail.effectiveGasPrice")}>
                      {formatGweiFromWei(transaction.data.effective_gas_price, locale)}
                    </TransactionDetailRow>
                    <TransactionDetailRow label={t("detail.burned")}>
                      {formatNativeAmount(transaction.data.burned_wei, locale, nativeDecimals)} {nativeSymbol}
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
          )}

          {activeTab === "internal-transactions" && (
            <section
              className="panel transaction-tab-panel"
              role="tabpanel"
              aria-labelledby="transaction-internal-transactions-title"
            >
                <h2 id="transaction-internal-transactions-title">
                  {t("addressTab.internalTransactions")}
                </h2>
                <QueryNotice
                  loading={internalTransactions.isPending}
                  error={internalTransactions.error}
                />
                {internalTransactions.data && !internalIdentityCurrent ? (
                  <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
                ) : null}
                {internalIdentityCurrent && internalTransactions.data?.state !== "complete"
                  && internalTransactions.data ? (
                    <CapabilityDegraded stage="trace" state={internalTransactions.data.state} />
                  ) : null}
                {internalIdentityCurrent && internalTransactions.data?.state === "complete"
                  && internalTransactions.data.items.length === 0 ? (
                    <p className="empty-result">{t("state.noTransactionInternalTransactions")}</p>
                  ) : null}
                {internalIdentityCurrent && internalTransactions.data?.state === "complete"
                  && internalTransactions.data.items.length > 0 ? (
                    <div
                      className="table-scroll"
                      tabIndex={0}
                      aria-label={t("addressTab.internalTransactions")}
                    >
                      <table>
                        <caption className="sr-only">{t("addressTab.internalTransactions")}</caption>
                        <thead>
                          <tr>
                            <th>{t("detail.callType")}</th>
                            <th>{t("table.from")}</th>
                            <th>{t("table.to")}</th>
                            <th>{t("table.value", { symbol: nativeSymbol })}</th>
                          </tr>
                        </thead>
                        <tbody>
                          {internalTransactions.data.items.map((item) => {
                            const destination = item.created_address ?? item.to;
                            return (
                              <tr key={item.path.join(".")}>
                                <td><span className="transaction-trace-kind">{item.call_type}</span></td>
                                <td>
                                  <AddressIdentity address={item.from} copy />
                                </td>
                                <td>
                                  {destination ? (
                                    <span className="table-primary">
                                      <AddressIdentity address={destination} copy />
                                      {item.created_address ? <small>{t("activity.created")}</small> : null}
                                    </span>
                                  ) : "—"}
                                </td>
                                <td>
                                  <code>{formatNativeAmount(item.value, locale, nativeDecimals)}</code>
                                  {nativeSymbol ? ` ${nativeSymbol}` : ""}
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>
                  ) : null}
                {internalIdentityCurrent && internalTransactions.data ? (
                  <CursorPagination
                    busy={internalTransactions.isFetching}
                    hasNext={Boolean(internalTransactions.data.next_cursor)}
                    hasPrevious={internalPager.hasPrevious}
                    label={t("addressTab.internalTransactions")}
                    onNext={() => internalPager.next(internalTransactions.data?.next_cursor)}
                    onPrevious={internalPager.previous}
                    page={internalPager.page}
                  />
                ) : null}
            </section>
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

          {activeTab === "token-transfers" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <QueryNotice loading={tokenTransfers.isPending} error={tokenTransfers.error} />
              {tokenTransfers.data && !tokenIdentityCurrent && (
                <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
              )}
              {tokenIdentityCurrent && tokenTransfers.data?.state !== "complete" && tokenTransfers.data && (
                <CapabilityDegraded stage="token" state={tokenTransfers.data.state} />
              )}
              {tokenIdentityCurrent && tokenTransfers.data?.state === "complete" && tokenTransfers.data.items.length === 0 && (
                <p className="empty-result">{t("state.noTransactionTokenTransfers")}</p>
              )}
              {tokenIdentityCurrent && tokenTransfers.data?.state === "complete" && tokenTransfers.data.items.length > 0 && (
                <div className="table-scroll" tabIndex={0}>
                  <table>
                    <thead><tr>
                      <th>{t("table.token")}</th><th>{t("detail.event")}</th>
                      <th>{t("table.from")}</th><th>{t("table.to")}</th>
                      <th>{t("detail.amountOrTokenID")}</th>
                    </tr></thead>
                    <tbody>{tokenTransfers.data.items.map((event) => (
                      <tr key={`${event.log_index}:${event.sub_index}`}>
                        <td><Link to="/token/$address" params={{ address: event.token_address }}>
                          <code>{shorten(event.token_address)}</code>
                        </Link><small className="table-secondary">{event.standard}</small></td>
                        <td>{event.kind}</td>
                        <td><code>{event.from ? shorten(event.from) : "—"}</code></td>
                        <td><code>{event.to ? shorten(event.to) : "—"}</code></td>
                        <td>
                          <span className="table-primary">
                            {event.amount !== undefined ? (
                              <code>{formatTokenEventAmount(event, locale)}</code>
                            ) : event.token_id !== undefined && isNFTStandard(event.standard) ? (
                              <NFTTokenIDLink address={event.token_address} tokenID={event.token_id} />
                            ) : (
                              <code>{formatInteger(event.token_id, locale)}</code>
                            )}
                            {event.amount !== undefined && event.token_id !== undefined && isNFTStandard(event.standard) ? (
                              <small><NFTTokenIDLink address={event.token_address} prefix tokenID={event.token_id} /></small>
                            ) : null}
                          </span>
                        </td>
                      </tr>
                    ))}</tbody>
                  </table>
                </div>
              )}
              {tokenIdentityCurrent && tokenTransfers.data && (
                <CursorPagination
                  busy={tokenTransfers.isFetching}
                  hasNext={Boolean(tokenTransfers.data.next_cursor)}
                  hasPrevious={tokenPager.hasPrevious}
                  label={t("transactionTabs.token-transfers")}
                  onNext={() => tokenPager.next(tokenTransfers.data?.next_cursor)}
                  onPrevious={tokenPager.previous}
                  page={tokenPager.page}
                />
              )}
            </section>
          )}

          {activeTab === "logs" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <QueryNotice loading={logs.isPending} error={logs.error} />
              {logs.data && !logIdentityCurrent && (
                <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
              )}
              {logIdentityCurrent && logs.data?.items.length === 0 && <p className="empty-result">{t("state.noTransactionLogs")}</p>}
              {logIdentityCurrent && logs.data && logs.data.items.length > 0 && (
                <div className="transaction-log-list">
                  {logs.data.items.map((log) => (
                    <TransactionLogCard key={log.log_index} log={log} locale={locale} />
                  ))}
                </div>
              )}
              {logIdentityCurrent && logs.data && (
                <CursorPagination
                  busy={logs.isFetching} hasNext={Boolean(logs.data.next_cursor)}
                  hasPrevious={logPager.hasPrevious} label={t("transactionTabs.logs")}
                  onNext={() => logPager.next(logs.data?.next_cursor)}
                  onPrevious={logPager.previous} page={logPager.page}
                />
              )}
            </section>
          )}

          {activeTab === "trace" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <QueryNotice loading={trace.isPending} error={trace.error} />
              {trace.data && !traceIdentityCurrent && (
                <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
              )}
              {traceIdentityCurrent && trace.data && trace.data.state !== "complete" && (
                <CapabilityDegraded stage="trace" state={trace.data.state} />
              )}
              {traceIdentityCurrent && trace.data?.state === "complete" && trace.data.frames.length === 0 && (
                <p className="empty-result">{t("state.noTraceFrames")}</p>
              )}
              {traceIdentityCurrent && trace.data?.state === "complete" && trace.data.frames.length > 0 && (
                <div className="transaction-trace-list">
                  {trace.data.frames.map((frame) => (
                    <article
                      className={frame.reverted ? "transaction-trace-frame reverted" : "transaction-trace-frame"}
                      key={frame.path.join(".") || "root"}
                      style={{ "--trace-depth": frame.depth } as React.CSSProperties}
                    >
                      <div className="transaction-trace-summary">
                        <span className="transaction-trace-kind">{frame.call_type}</span>
                        <div>
                          <strong><code>{frame.decoding?.signature ?? t(traceDecodingKey(frame.decoding?.status ?? "unavailable"))}</code></strong>
                          <code>{frame.from ? shorten(frame.from) : "—"} → {frame.to
                            ? shorten(frame.to)
                            : frame.created_address ? shorten(frame.created_address) : "—"}</code>
                          <small className="table-secondary">
                            {t("detail.executionContext", { address: frame.execution.context_address })}
                            {frame.execution.address
                              ? ` · ${t("detail.executionCode", { address: frame.execution.address })}`
                              : ` · ${t(`detail.executionResolution.${frame.execution.resolution}`)}`}
                          </small>
                        </div>
                        <span>{formatNativeAmount(frame.value, locale, nativeDecimals)} {nativeSymbol}</span>
                        <span>{frame.direct_reverted
                          ? frame.error ?? t("detail.directReverted")
                          : frame.reverted ? t("detail.ancestorReverted") : t("detail.succeeded")}</span>
                      </div>
                      <details className="transaction-more-details transaction-trace-details">
                        <summary>{t("detail.traceDetails")}</summary>
                        {frame.decoding && (
                          <>
                            <p className="quiet">{t(traceDecodingKey(frame.decoding.status))}
                              {frame.decoding.abi_source?.address
                                ? ` · ${t("detail.abiSource", { kind: frame.decoding.abi_source.kind, address: frame.decoding.abi_source.address })}`
                                : ""}
                            </p>
                            <dl className="transaction-trace-decode-status">
                              <div><dt>{t("detail.callInputs")}</dt><dd>{t(traceDecodingKey(frame.decoding.status))}</dd></div>
                              <div><dt>{t("detail.callOutputs")}</dt><dd>{t(traceOutputStatusKey(frame.decoding.output_status))}</dd></div>
                              {frame.decoding.revert && (
                                <div><dt>{t("detail.revertData")}</dt><dd>{t(traceDecodingKey(frame.decoding.revert.status))}</dd></div>
                              )}
                            </dl>
                            {frame.decoding.inputs.length > 0 && (
                              <TraceABIValues title={t("detail.callInputs")} values={frame.decoding.inputs} />
                            )}
                            {frame.decoding.outputs.length > 0 && (
                              <TraceABIValues title={t("detail.callOutputs")} values={frame.decoding.outputs} />
                            )}
                            {frame.decoding.revert && frame.decoding.revert.arguments.length > 0 && (
                              <TraceABIValues
                                title={frame.decoding.revert.signature ?? t("detail.revertData")}
                                values={frame.decoding.revert.arguments}
                              />
                            )}
                            {(frame.decoding.warning || frame.decoding.revert?.warning) && (
                              <p className="quiet">{frame.decoding.warning ?? frame.decoding.revert?.warning}</p>
                            )}
                          </>
                        )}
                        <dl className="transaction-trace-raw">
                          <div><dt>{t("detail.executionContextLabel")}</dt><dd><code>{frame.execution.context_address}</code></dd></div>
                          <div><dt>{t("detail.executionCodeLabel")}</dt><dd><code>{frame.execution.address ?? "—"}</code></dd></div>
                          <div><dt>{t("detail.codeHash")}</dt><dd><code>{frame.execution.code_hash ?? "—"}</code></dd></div>
                          <div><dt>{t("detail.input")}</dt><dd><code>{frame.input ?? "0x"}</code></dd></div>
                          <div><dt>{t("detail.output")}</dt><dd><code>{frame.output ?? "0x"}</code></dd></div>
                        </dl>
                      </details>
                    </article>
                  ))}
                </div>
              )}
            </section>
          )}

          {activeTab === "state-changes" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <QueryNotice loading={stateChanges.isPending} error={stateChanges.error} />
              {stateChanges.data && !stateIdentityCurrent && (
                <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
              )}
              {stateIdentityCurrent && stateChanges.data?.state !== "complete" && stateChanges.data && (
                <CapabilityDegraded stage="state_diff" state={stateChanges.data.state} />
              )}
              {stateIdentityCurrent && stateChanges.data?.state === "complete" && stateGroups.length === 0 && (
                <p className="empty-result">{t("state.noTransactionStateChanges")}</p>
              )}
              {stateIdentityCurrent && stateChanges.data?.state === "complete" && stateGroups.map(([address, changes]) => (
                <article className="transaction-state-account" key={address}>
                  <header><AddressIdentity address={address} compact={false} /></header>
                  <dl>{changes.map((change) => (
                    <div key={`${change.kind}:${change.storage_key ?? ""}`}>
                      <dt>{change.kind}{change.storage_key ? <code>{shorten(change.storage_key)}</code> : null}</dt>
                      <dd><code>{change.before ?? "∅"}</code><span aria-hidden="true">→</span><code>{change.after ?? "∅"}</code></dd>
                    </div>
                  ))}</dl>
                </article>
              ))}
              {stateIdentityCurrent && stateChanges.data && (
                <CursorPagination
                  busy={stateChanges.isFetching} hasNext={Boolean(stateChanges.data.next_cursor)}
                  hasPrevious={statePager.hasPrevious} label={t("transactionTabs.state-changes")}
                  onNext={() => statePager.next(stateChanges.data?.next_cursor)}
                  onPrevious={statePager.previous} page={statePager.page}
                />
              )}
            </section>
          )}
        </>
      )}
    </Page>
  );
}

function MempoolDetailExpired() {
  const { t } = useTranslation();
  return (
    <section className="capability-panel pending-unavailable" role="status">
      <span className="capability-mark" aria-hidden="true">!</span>
      <div>
        <h2>{t("pending.unavailable")}</h2>
        <p>{t("pending.expiredDetail")}</p>
        <code>snapshot_expired</code>
      </div>
    </section>
  );
}

function TransactionLogCard({
  log,
  locale,
}: {
  log: TransactionLog;
  locale: string;
}) {
  const { t } = useTranslation();
  const [topicModes, setTopicModes] = useState<Record<string, TopicDisplayMode>>({});
  const signature = log.decoding.signature ?? log.decoding.event_name
    ?? t("detail.logIndex", { index: formatInteger(log.log_index, locale) });
  const decoded = log.decoding.status === "decoded";
  const anonymous = isAnonymousDecodedLog(log.decoding.status, log.topics.length, log.decoding.arguments);
  const setTopicMode = (index: number, mode: TopicDisplayMode) => {
    setTopicModes((current) => ({ ...current, [String(index)]: mode }));
  };

  return (
    <article className="transaction-log">
      <header className="transaction-log-header">
        <div className="transaction-log-heading">
          <strong>{signature}</strong>
          {!decoded && <small className="transaction-log-status">{t(logDecodingKey(log.decoding.status))}</small>}
        </div>
        <span className="transaction-log-index">
          <span className="sr-only">{t("detail.logIndex", { index: formatInteger(log.log_index, locale) })}</span>
          {formatInteger(log.log_index, locale)}
        </span>
      </header>

      <div className="transaction-log-address">
        <span className="transaction-log-label">{t("detail.address")}</span>
        <CopyableField value={log.address}>
          <AddressIdentity address={log.address} compact={false} />
        </CopyableField>
      </div>

      {log.decoding.arguments.length > 0 && (
        <TransactionLogArgumentsTable arguments={log.decoding.arguments} />
      )}

      {log.decoding.warning && <p className="quiet transaction-log-warning">{log.decoding.warning}</p>}

      <details className="transaction-more-details transaction-log-details">
        <summary>{t("detail.moreDetails")}</summary>
        <div className="transaction-log-details-content">
          <TransactionLogProvenance log={log} />
          <TransactionLogTopicsAndData
            allowTopicZeroConversion={anonymous}
            data={log.data}
            logIndex={log.log_index}
            onTopicModeChange={setTopicMode}
            topicModes={topicModes}
            topics={log.topics}
          />
        </div>
      </details>
    </article>
  );
}

function TransactionLogArgumentsTable({
  arguments: values,
}: {
  arguments: TransactionLog["decoding"]["arguments"];
}) {
  const { t } = useTranslation();
  const rows = values.flatMap((argument, index) => flattenLogArgument(argument, index));

  return (
    <div className="transaction-log-arguments-scroll" tabIndex={0}>
      <div className="transaction-log-arguments-table" role="table" aria-label={t("detail.eventArguments")}>
        <div className="transaction-log-arguments-row transaction-log-arguments-header" role="row">
          <span role="columnheader">{t("detail.argumentName")}</span>
          <span role="columnheader">{t("detail.argumentType")}</span>
          <span role="columnheader">{t("detail.argumentIndexed")}</span>
          <span role="columnheader">{t("detail.argumentData")}</span>
        </div>
        {rows.map((row, index) => (
          <TransactionLogArgumentRow key={`${row.path}:${index}`} row={row} />
        ))}
      </div>
    </div>
  );
}

function TransactionLogArgumentRow({ row }: { row: LogArgumentRow }) {
  const { t } = useTranslation();
  const value = formatLogArgument(row.value);
  return (
    <div
      className={`transaction-log-arguments-row transaction-log-argument-depth-${Math.min(row.depth, 6)}${row.composite ? " transaction-log-argument-composite" : ""}`}
      role="row"
    >
      <span className="transaction-log-argument-name" role="cell">{row.path}</span>
      <code className="transaction-log-argument-type" role="cell">{row.type || "—"}</code>
      <span className="transaction-log-argument-indexed" role="cell">
        {row.indexed === undefined ? "—" : yesNo(row.indexed, t)}
      </span>
      <span className="transaction-log-argument-data" role="cell">
        <CopyableField value={value}><code>{value}</code></CopyableField>
      </span>
    </div>
  );
}

function TransactionLogProvenance({ log }: { log: TransactionLog }) {
  const { t } = useTranslation();
  const source = log.decoding.abi_source;
  const attribution = log.decoding.attribution;
  return (
    <section className="transaction-log-provenance" aria-labelledby={`log-provenance-${log.log_index}`}>
      <h3 id={`log-provenance-${log.log_index}`}>{t("detail.abiProvenance")}</h3>
      <div className="transaction-log-provenance-grid">
        {source && (
          <div className="transaction-log-provenance-card">
            <h4>{t("detail.abiSourceLabel")}</h4>
            <dl>
              <div>
                <dt>{t("detail.sourceKind")}</dt>
                <dd><span className="transaction-provenance-badge">{abiSourceKindLabel(source.kind, t)}</span></dd>
              </div>
              {source.address && (
                <div>
                  <dt>{t("detail.sourceAddress")}</dt>
                  <dd><AddressIdentity address={source.address} compact={false} copy /></dd>
                </div>
              )}
              {source.code_hash && (
                <div>
                  <dt>{t("detail.sourceCodeHash")}</dt>
                  <dd><CopyableField value={source.code_hash}><code>{source.code_hash}</code></CopyableField></dd>
                </div>
              )}
              {log.decoding.confidence && (
                <div>
                  <dt>{t("detail.confidence")}</dt>
                  <dd><span className="transaction-provenance-badge">{confidenceLabel(log.decoding.confidence, t)}</span></dd>
                </div>
              )}
            </dl>
          </div>
        )}

        <div className="transaction-log-provenance-card">
          <h4>{t("detail.executionProvenance")}</h4>
          <dl>
            <div>
              <dt>{t("detail.emitterAddress")}</dt>
              <dd><AddressIdentity address={log.address} compact={false} copy /></dd>
            </div>
            {attribution.execution_address && (
              <div>
                <dt>{t("detail.executionAddress")}</dt>
                <dd><AddressIdentity address={attribution.execution_address} compact={false} copy /></dd>
              </div>
            )}
            <div>
              <dt>{t("detail.attribution")}</dt>
              <dd><span className="transaction-provenance-badge">{attributionLabel(attribution.mode, t)}</span></dd>
            </div>
            <div>
              <dt>{t("detail.tracePath")}</dt>
              <dd><code>[{attribution.trace_path.join(", ")}]</code></dd>
            </div>
          </dl>
        </div>
      </div>
    </section>
  );
}

function TransactionLogTopicsAndData({
  allowTopicZeroConversion,
  data,
  logIndex,
  onTopicModeChange,
  topicModes,
  topics,
}: {
  allowTopicZeroConversion: boolean;
  data: string;
  logIndex: string;
  onTopicModeChange: (index: number, mode: TopicDisplayMode) => void;
  topicModes: Record<string, TopicDisplayMode>;
  topics: readonly string[];
}) {
  const { t } = useTranslation();
  return (
    <>
      <section className="transaction-log-topics" aria-labelledby={`log-topics-${logIndex}`}>
        <h3 id={`log-topics-${logIndex}`}>{t("detail.topics")}</h3>
        <div className="transaction-topic-list">
          {topics.map((topic, index) => {
            const convertible = index > 0 || allowTopicZeroConversion;
            const mode = topicModes[String(index)] ?? "hex";
            const value = formatTopicValue(topic, mode);
            const displayValue = value ?? t("detail.topicUnavailable");
            return (
              <div
                className={`transaction-topic${convertible ? " transaction-topic-convertible" : ""}`}
                key={`${topic}:${index}`}
              >
                <span className="transaction-topic-index" aria-hidden="true">{index}</span>
                {!convertible ? (
                  <CopyableField value={topic}><code>{topic}</code></CopyableField>
                ) : (
                  <>
                    <label className="sr-only" htmlFor={`topic-mode-${logIndex}-${index}`}>
                      {t("detail.topicMode", { index })}
                    </label>
                    <select
                      aria-label={t("detail.topicMode", { index })}
                      className="topic-mode-select"
                      id={`topic-mode-${logIndex}-${index}`}
                      onChange={(event) => onTopicModeChange(index, event.target.value as TopicDisplayMode)}
                      value={mode}
                    >
                      <option value="hex">{t("detail.topicModes.hex")}</option>
                      <option value="address">{t("detail.topicModes.address")}</option>
                      <option value="text">{t("detail.topicModes.text")}</option>
                      <option value="number">{t("detail.topicModes.number")}</option>
                    </select>
                    <CopyableField value={value ?? topic}><code>{displayValue}</code></CopyableField>
                  </>
                )}
              </div>
            );
          })}
        </div>
      </section>
      <section className="transaction-log-data" aria-labelledby={`log-data-${logIndex}`}>
        <h3 id={`log-data-${logIndex}`}>{t("detail.data")}</h3>
        <div className="transaction-log-data-value">
          <CopyableField value={data}><code>{data}</code></CopyableField>
        </div>
      </section>
    </>
  );
}

function TraceABIValues({ title, values }: {
  title: string;
  values: Array<{ name: string; type: string; value: unknown }>;
}) {
  return (
    <section className="transaction-trace-values">
      <h4>{title}</h4>
      <dl>{values.map((value, index) => (
        <div key={`${value.name}:${value.type}:${index}`}>
          <dt>{value.name || `${index}`} <code>{value.type}</code></dt>
          <dd><code>{formatLogArgument(value.value)}</code></dd>
        </div>
      ))}</dl>
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
  return <div className={wide ? "transaction-detail-row detail-item wide" : "transaction-detail-row detail-item"}>
    <dt>{label}</dt><dd>{children}</dd>
  </div>;
}

function MempoolTransactionOverview({
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
      <section className={`panel mempool-transaction-state ${pending ? "pending" : "replaced"}`} role="status">
        <TransactionStatus
          label={t(pending ? "transactionStatus.pending" : "transactionStatus.replaced")}
          status={pending ? "pending" : "replaced"}
        />
        <div>
          <h2>{t(pending ? "detail.pendingTitle" : "detail.replacedTitle")}</h2>
          <p>{t(pending ? "detail.pendingDetail" : "detail.replacedDetail")}</p>
        </div>
      </section>
      <section className="panel transaction-detail-card" aria-label={t("detail.transactionSummary")}>
        <h2 className="sr-only">{t("detail.transactionSummary")}</h2>
        <dl className="transaction-detail-list">
          <TransactionDetailRow label={t("table.hash")}>
            <CopyableField value={transaction.hash}><code>{transaction.hash}</code></CopyableField>
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
                <Link to="/tx/$hash" params={{ hash: detail.replacement_hash }} search={{ tab: "overview" }}>
                  <code>{detail.replacement_hash}</code>
                </Link>
              </CopyableField>
            </TransactionDetailRow>
          ) : null}
          {transaction.replaces_hash ? (
            <TransactionDetailRow label={t("detail.replacesHash")}>
              <CopyableField value={transaction.replaces_hash}>
                <Link to="/tx/$hash" params={{ hash: transaction.replaces_hash }} search={{ tab: "overview" }}>
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
            ) : t("common.contractCreation")}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("table.value", { symbol: nativeSymbol })}>
            <strong>{formatNativeAmount(transaction.value, locale, nativeDecimals)} {nativeSymbol}</strong>
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
            <FeeSettings locale={locale} entries={[
              { label: t("pending.gasPrice"), value: transaction.gas_price },
              { label: t("detail.feeMax"), value: transaction.max_fee_per_gas },
              { label: t("detail.feeMaxPriority"), value: transaction.max_priority_fee_per_gas },
            ]} />
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.firstSeen")}>
            <time dateTime={transaction.first_seen_at}>{formatTimestamp(transaction.first_seen_at, locale)}</time>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.lastSeen")}>
            <time dateTime={transaction.last_seen_at}>{formatTimestamp(transaction.last_seen_at, locale)}</time>
          </TransactionDetailRow>
          {!pending ? (
            <TransactionDetailRow label={t("detail.replacedAt")}>
              <time dateTime={detail.replaced_at}>{formatTimestamp(detail.replaced_at, locale)}</time>
            </TransactionDetailRow>
          ) : null}
          <TransactionDetailRow label={t("detail.expiresAt")}>
            <time dateTime={transaction.expires_at}>{formatTimestamp(transaction.expires_at, locale)}</time>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.endpoint")}><code>{transaction.endpoint}</code></TransactionDetailRow>
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
    : transaction.status === "success" ? "success"
    : transaction.status === "failed" ? "failed"
    : "unknown";
  return <span className="transaction-status-group">
    <TransactionStatus
      label={state === "orphan" ? t("detail.orphaned") : transactionStatusLabel(transaction.status, t)}
      status={state}
    />
    {showFinality && transaction.canonical && transaction.finality === "finalized"
      ? <span className="finality-badge finalized">{finalityLabel(transaction.finality, t)}</span>
      : null}
  </span>;
}

export function IncludedTransactionStatus({ transaction }: { transaction: TransactionSummary }) {
  const { t } = useTranslation();
  const state: TransactionVisualStatus = !transaction.canonical
    ? "orphan"
    : transaction.status === "success" ? "success"
    : transaction.status === "failed" ? "failed"
    : transaction.status === "pending" ? "pending"
    : "unknown";
  return (
    <TransactionStatus
      label={state === "orphan" ? t("detail.orphaned") : transactionStatusLabel(transaction.status, t)}
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

function transactionCalldataIdentityMatches(
  transaction: TransactionSummary,
  resource: TransactionCalldataResource,
): boolean {
  return resource.state === "complete"
    && Boolean(transaction.to)
    && Boolean(transaction.block_hash)
    && resource.transaction_hash.toLowerCase() === transaction.hash.toLowerCase()
    && resource.block_hash.toLowerCase() === transaction.block_hash?.toLowerCase()
    && resource.block_number === transaction.block_number
    && resource.transaction_index === String(transaction.transaction_index)
    && resource.input.toLowerCase() === transaction.input.toLowerCase()
    && resource.execution.context_address.toLowerCase() === transaction.to?.toLowerCase();
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
  ].join(":").toLowerCase();
}

function transactionFailureIdentityMatches(
  transaction: TransactionSummary,
  resource: TransactionFailureResource,
): boolean {
  return resource.state === "complete"
    && Boolean(transaction.block_hash)
    && resource.transaction_hash.toLowerCase() === transaction.hash.toLowerCase()
    && resource.block_hash.toLowerCase() === transaction.block_hash?.toLowerCase()
    && resource.block_number === transaction.block_number
    && resource.transaction_index === String(transaction.transaction_index);
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
  ].join(":").toLowerCase();
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
