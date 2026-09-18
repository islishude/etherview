import { useEffect, useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { hexToBytes, type Hex } from "viem";
import type {
  TransactionCalldata as TransactionCalldataResource,
  TransactionFailure as TransactionFailureResource,
  TransactionSummary,
} from "@/api/types";
import { formatInteger } from "@/components/format";
import { CopyButton, CopyableField } from "@/components/CopyButton";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { QueryNotice } from "@/components/QueryNotice";
import { flattenFailureArguments } from "@/components/failureFormat";
import {
  formatTransactionCalldataInputs,
  type FormattedAbiField,
  type FormattedAbiOutput,
  type FormattedAbiValue,
} from "@/contracts/abi";

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
      label={
        label ??
        (prefix ? `${prefix}.${field.name || `#${field.index}`}` : field.name || `#${field.index}`)
      }
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

  const displayType = calldataDisplayType(
    value.type,
    value.internalType ?? internalType,
    value.kind,
  );
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

export function TransactionCalldata({
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
  const resourceCurrent =
    identityCurrent &&
    resource !== undefined &&
    resource.input.toLowerCase() === input.toLowerCase() &&
    resource.execution.context_address.toLowerCase() === targetAddress.toLowerCase();
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
      {enabled && (
        <section className="transaction-calldata-decoded" aria-labelledby={decodedHeadingID}>
          <h3 className="transaction-calldata-heading" id={decodedHeadingID}>
            {t("detail.calldataDecoded")}
            {enabled && !loadingABI && decoding?.status === "decoded" && decoding.signature && (
              <>
                {" "}
                · <code>{decoding.signature}</code>
              </>
            )}
          </h3>
          {enabled && loadingABI && (
            <p className="quiet" role="status">
              {t("detail.calldataDecodeLoading")}
            </p>
          )}
          {enabled &&
            !loadingABI &&
            decoding?.status === "decoded" &&
            decoding.signature &&
            resource && (
              <>
                <div className="calldata-abi-sources" aria-label={t("detail.calldataAbiEvidence")}>
                  <div className="calldata-abi-source">
                    <span>{t("detail.calldataExecutionEvidence")}</span>
                    <span aria-hidden="true">·</span>
                    <span>{t(`detail.executionResolution.${resource.execution.resolution}`)}</span>
                    {resource.execution.address && (
                      <>
                        <span aria-hidden="true">·</span>
                        <AddressIdentity
                          address={resource.execution.address}
                          compact={false}
                          copy
                        />
                      </>
                    )}
                  </div>
                  {decoding.abi_source?.address && (
                    <div className="calldata-abi-source">
                      <span>
                        {t("detail.calldataAbiSource", { kind: decoding.abi_source.kind })}
                      </span>
                      <span aria-hidden="true">·</span>
                      <AddressIdentity address={decoding.abi_source.address} compact={false} copy />
                    </div>
                  )}
                </div>
                {decodedArgs === null ? (
                  <p className="capability-panel" role="status">
                    {t("detail.calldataStructureUnavailable")}
                  </p>
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
                ) : (
                  <p className="quiet">{t("detail.calldataNoParameters")}</p>
                )}
              </>
            )}
          {enabled && !loadingABI && decoding?.status !== "decoded" && (
            <p className="capability-panel" role="status">
              {t(
                !identityCurrent || (resource !== undefined && !resourceCurrent)
                  ? "state.transactionIdentityChanged"
                  : decoding?.status === "not_applicable"
                    ? "detail.calldataNoExecutionCode"
                    : decoding?.status === "unknown"
                      ? "detail.calldataUnknownSelector"
                      : decoding?.status === "malformed"
                        ? "detail.calldataMalformed"
                        : decoding?.status === "ambiguous"
                          ? "detail.calldataAmbiguous"
                          : "detail.calldataUnavailable",
              )}
              {decoding?.status === "ambiguous" && decoding.candidates.length > 0 ? (
                <>
                  {" "}
                  · <code>{decoding.candidates.join(" · ")}</code>
                </>
              ) : null}
            </p>
          )}
        </section>
      )}
      <section className="transaction-calldata-raw" aria-labelledby={rawHeadingID}>
        <header className="transaction-calldata-raw-header">
          <h3 className="transaction-calldata-heading" id={rawHeadingID}>
            {t("detail.rawCalldata")}
          </h3>
          <div className="transaction-calldata-raw-actions">
            <button
              className="transaction-calldata-mode-link"
              onClick={toggleRawMode}
              type="button"
            >
              {t(rawMode === "hex" ? "detail.rawViewAsUtf8" : "detail.rawViewAsHex")}
            </button>
            <CopyButton value={input} />
          </div>
        </header>
        {utf8Unavailable && (
          <p className="quiet transaction-calldata-raw-status" role="status">
            {t("detail.rawUtf8Unavailable")}
          </p>
        )}
        <textarea
          aria-label={t("detail.rawCalldataValue", {
            mode: t(rawMode === "hex" ? "detail.rawHex" : "detail.rawUtf8"),
          })}
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

export function TransactionFailureReason({
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
  const builtin =
    resource?.decoding.status === "decoded" && resource.decoding.abi_source?.kind === "builtin";
  const arguments_ = useMemo(() => {
    if (
      !resource ||
      resource.decoding.status !== "decoded" ||
      resource.decoding.abi_source?.kind === "builtin"
    )
      return undefined;
    try {
      return flattenFailureArguments(formatTransactionCalldataInputs(resource.decoding.arguments));
    } catch {
      return null;
    }
  }, [resource]);

  if (loading) return <QueryNotice compact loading />;
  if (error) return <QueryNotice compact error={error} />;
  if (!resource || !identityCurrent) {
    return (
      <p className="capability-panel" role="status">
        {t("state.transactionIdentityChanged")}
      </p>
    );
  }

  const decoded = resource.decoding.status === "decoded" && arguments_ !== null;
  const directError = builtin ? (resource.decoding.reason ?? resource.error) : undefined;
  return (
    <section className="transaction-failure" aria-label={t("detail.failureReason")}>
      {directError !== undefined ? (
        <CopyableField value={directError}>
          <code>{directError}</code>
        </CopyableField>
      ) : decoded && resource.decoding.signature ? (
        <strong className="transaction-failure-signature">
          <code>{resource.decoding.signature}</code>
        </strong>
      ) : (
        <CopyableField value={resource.error}>
          <code>{resource.error}</code>
        </CopyableField>
      )}
      {arguments_ && arguments_.rows.length > 0 && (
        <div className="transaction-failure-table-scroll" tabIndex={0}>
          <div
            className="transaction-failure-table"
            role="table"
            aria-label={t("detail.failureArguments")}
          >
            <div className="transaction-failure-row transaction-failure-header" role="row">
              <span role="columnheader">{t("detail.argumentName")}</span>
              <span role="columnheader">{t("detail.argumentType")}</span>
              <span role="columnheader">{t("detail.argumentData")}</span>
            </div>
            {arguments_.rows.map((row, index) => (
              <div className="transaction-failure-row" role="row" key={`${row.path}:${index}`}>
                <code className="transaction-failure-name" role="cell">
                  {row.path}
                </code>
                <code className="transaction-failure-type" role="cell">
                  {row.type}
                </code>
                <span className="transaction-failure-data" role="cell">
                  <CopyableField value={row.data}>
                    <code>{row.data}</code>
                  </CopyableField>
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
      {arguments_?.truncated && (
        <p className="quiet transaction-failure-warning" role="status">
          {t("detail.failureArgumentsTruncated")}
        </p>
      )}
      {arguments_ === null && (
        <p className="capability-panel" role="status">
          {t("detail.failureStructureUnavailable")}
        </p>
      )}
      {resource.decoding.warning && (
        <p className="quiet transaction-failure-warning">{resource.decoding.warning}</p>
      )}
      {!builtin && resource.revert_data !== undefined && (
        <details className="transaction-more-details transaction-failure-raw">
          <summary>{t("detail.revertData")}</summary>
          <CopyableField value={resource.revert_data}>
            <code>{resource.revert_data}</code>
          </CopyableField>
        </details>
      )}
    </section>
  );
}
