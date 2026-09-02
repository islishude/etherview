import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { usePublicConfig, useUserOperation, useUserOperations } from "@/api/hooks";
import type { UserOperationDetail } from "@/api/types";
import { CopyableField } from "@/components/CopyButton";
import {
  formatInteger,
  formatNativeAmount,
  formatTimestamp,
  shorten,
} from "@/components/format";
import { QueryNotice } from "@/components/QueryNotice";
import { UserOperationStatus, UserOperationTable } from "@/components/UserOperationTable";
import { AddressIdentity } from "@/ens/AddressIdentity";
import {
  CORE_PAGE_SIZE,
  CursorPagination,
  Detail,
  DetailList,
  FinalityBadge,
  Page,
  useCursorHistory,
} from "./pages";

export function UserOperationsPage() {
  const { i18n, t } = useTranslation();
  const pager = useCursorHistory("user-operations");
  const operations = useUserOperations(CORE_PAGE_SIZE, pager.cursor, pager.refreshGeneration);
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <Page title={t("page.userOperations")} description={t("page.userOperationsDescription")}>
      <QueryNotice loading={operations.isPending} error={operations.error} onReset={pager.reset} />
      {operations.data?.meta.coverage_start && operations.data.meta.coverage_end ? (
        <p className="context-note" role="note">
          {t("userOperation.coverage", {
            start: formatInteger(operations.data.meta.coverage_start, locale),
            end: formatInteger(operations.data.meta.coverage_end, locale),
          })}
        </p>
      ) : null}
      {operations.data?.items.length === 0 ? (
        <p className="empty-result" role="status">{t("state.noUserOperations")}</p>
      ) : null}
      {operations.data && operations.data.items.length > 0 ? (
        <UserOperationTable items={operations.data.items} />
      ) : null}
      {operations.data ? (
        <CursorPagination
          busy={operations.isFetching}
          hasNext={Boolean(operations.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("pagination.userOperations")}
          onNext={() => pager.next(operations.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      ) : null}
    </Page>
  );
}

export function UserOperationDetailPage({ hash }: { hash: string }) {
  const { i18n, t } = useTranslation();
  const operation = useUserOperation(hash);
  const config = usePublicConfig();
  const locale = i18n.resolvedLanguage ?? "en";
  const nativeDecimals = config.data?.native_decimals ?? 18;
  const nativeSymbol = config.data?.native_symbol ?? "";
  return (
    <Page title={t("page.userOperation")} description={hash} mono>
      <QueryNotice loading={operation.isPending} error={operation.error} />
      {operation.data ? (
        <div className="page-stack user-operation-detail">
          {!operation.data.success ? <UserOperationFailure operation={operation.data} /> : null}
          <DetailList label={t("page.userOperation")}>
            <Detail label={t("userOperation.hash")} mono wide value={<HashValue value={operation.data.hash} />} />
            <Detail label={t("table.status")} value={<UserOperationStatus success={operation.data.success} />} />
            <Detail label={t("userOperation.version")} value={`v${operation.data.entry_point_version}`} />
            <Detail label={t("userOperation.entryPoint")} value={<AddressIdentity address={operation.data.entry_point} />} />
            <Detail label={t("userOperation.sender")} value={<AddressIdentity address={operation.data.sender} />} />
            <Detail label={t("userOperation.nonce")} value={formatInteger(operation.data.nonce, locale)} />
            <Detail label={t("userOperation.nonceKey")} value={formatInteger(operation.data.nonce_key, locale)} />
            <Detail label={t("userOperation.nonceSequence")} value={formatInteger(operation.data.nonce_sequence, locale)} />
            <Detail label={t("userOperation.bundle")} mono value={(
              <Link to="/tx/$hash" params={{ hash: operation.data.transaction_hash }} search={{ tab: "user-operations" }}>
                {shorten(operation.data.transaction_hash)}
              </Link>
            )} />
            <Detail label={t("userOperation.operationIndex")} value={formatInteger(operation.data.operation_index, locale)} />
            <Detail label={t("userOperation.eventLogIndex")} value={formatInteger(operation.data.event_log_index, locale)} />
            <Detail label={t("table.block")} value={(
              <Link to="/blocks/$blockID" params={{ blockID: operation.data.block_hash }}>
                {formatInteger(operation.data.block_number, locale)}
              </Link>
            )} />
            <Detail label={t("detail.timestamp")} value={formatTimestamp(operation.data.block_timestamp, locale)} />
            <Detail label={t("table.finality")} value={<FinalityBadge finality={operation.data.finality} />} />
            <Detail label={t("userOperation.bundler")} value={<AddressIdentity address={operation.data.bundler} />} />
            <Detail label={t("userOperation.beneficiary")} value={<AddressIdentity address={operation.data.beneficiary} />} />
            <Detail label={t("userOperation.initialization")} value={t(`userOperation.initKind.${operation.data.init_kind}`)} />
            <Detail label={t("userOperation.factory")} value={operation.data.factory ? <AddressIdentity address={operation.data.factory} /> : "—"} />
            <Detail label={t("userOperation.paymaster")} value={operation.data.paymaster ? <AddressIdentity address={operation.data.paymaster} /> : "—"} />
            <Detail label={t("userOperation.aggregator")} value={operation.data.aggregator ? <AddressIdentity address={operation.data.aggregator} /> : "—"} />
            <Detail label={t("userOperation.gasUsed")} value={formatInteger(operation.data.actual_gas_used, locale)} />
            <Detail
              label={`${t("userOperation.gasCost")} (${nativeSymbol})`}
              value={<code>{formatNativeAmount(operation.data.actual_gas_cost, locale, nativeDecimals)}</code>}
            />
          </DetailList>
          <UserOperationRequestPanel operation={operation.data} />
          <UserOperationEvents operation={operation.data} />
        </div>
      ) : null}
    </Page>
  );
}

function UserOperationFailure({ operation }: { operation: UserOperationDetail }) {
  const { t } = useTranslation();
  const failure = operation.events.find((event) =>
    ["execution_revert", "post_op_revert", "prefund_too_low"].includes(event.kind)
  );
  return (
    <section className="reorg-context user-operation-failure" role="status">
      <span className="reorg-mark" aria-hidden="true">!</span>
      <div>
        <h2>{failure ? t(`userOperation.eventKind.${failure.kind}`) : t("userOperation.failed")}</h2>
        {failure?.reason ? <p>{failure.reason}</p> : null}
        {failure?.panic_code ? <code>Panic({failure.panic_code})</code> : null}
      </div>
    </section>
  );
}

function UserOperationRequestPanel({ operation }: { operation: UserOperationDetail }) {
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  const request = operation.request;
  return (
    <DetailList label={t("userOperation.request")}>
      <Detail label={t("userOperation.callGasLimit")} value={formatInteger(request.call_gas_limit, locale)} />
      <Detail label={t("userOperation.verificationGasLimit")} value={formatInteger(request.verification_gas_limit, locale)} />
      <Detail label={t("userOperation.preVerificationGas")} value={formatInteger(request.pre_verification_gas, locale)} />
      <Detail label={t("userOperation.maxFeePerGas")} value={formatInteger(request.max_fee_per_gas, locale)} />
      <Detail label={t("userOperation.maxPriorityFeePerGas")} value={formatInteger(request.max_priority_fee_per_gas, locale)} />
      <Detail label={t("userOperation.paymasterVerificationGasLimit")} value={formatInteger(request.paymaster_verification_gas_limit, locale)} />
      <Detail label={t("userOperation.paymasterPostOpGasLimit")} value={formatInteger(request.paymaster_post_op_gas_limit, locale)} />
      {request.account_gas_limits ? <RawDetail label={t("userOperation.accountGasLimits")} value={request.account_gas_limits} /> : null}
      {request.gas_fees ? <RawDetail label={t("userOperation.gasFees")} value={request.gas_fees} /> : null}
      <RawDetail label={t("userOperation.initCode")} value={request.init_code} />
      <RawDetail label={t("userOperation.factoryData")} value={request.factory_data} />
      <RawDetail label={t("userOperation.callData")} value={request.call_data} />
      <RawDetail label={t("userOperation.paymasterAndData")} value={request.paymaster_and_data} />
      <RawDetail label={t("userOperation.paymasterData")} value={request.paymaster_data} />
      <RawDetail label={t("userOperation.paymasterSignature")} value={request.paymaster_signature} />
      <RawDetail label={t("userOperation.signature")} value={request.signature} />
      <RawDetail label={t("userOperation.aggregatedSignature")} value={request.aggregated_signature} />
    </DetailList>
  );
}

function UserOperationEvents({ operation }: { operation: UserOperationDetail }) {
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <section className="panel detail-card" aria-labelledby="user-operation-events-title">
      <h2 id="user-operation-events-title">{t("userOperation.events")}</h2>
      {operation.events.length === 0 ? <p className="empty-result">{t("userOperation.noEvents")}</p> : null}
      <div className="transaction-event-list">
        {operation.events.map((event) => (
          <article className="transaction-log" key={`${event.log_index}:${event.kind}`}>
            <h3>{t(`userOperation.eventKind.${event.kind}`)}</h3>
            <dl className="detail-grid compact-grid">
              <Detail label={t("detail.logIndex")} value={formatInteger(event.log_index, locale)} />
              <Detail label={t("userOperation.sender")} value={<AddressIdentity address={event.sender} />} />
              <Detail label={t("userOperation.nonce")} value={formatInteger(event.nonce, locale)} />
              <Detail label={t("detail.address")} value={event.related_address ? <AddressIdentity address={event.related_address} /> : "—"} />
              <Detail label={t("userOperation.paymaster")} value={event.paymaster ? <AddressIdentity address={event.paymaster} /> : "—"} />
              {event.reason ? <Detail label={t("detail.reason")} value={event.reason} wide /> : null}
              <RawDetail label={t("detail.rawData")} value={event.raw_data} />
            </dl>
          </article>
        ))}
      </div>
    </section>
  );
}

function RawDetail({ label, value }: { label: string; value: string }) {
  return <Detail label={label} mono wide value={<HashValue value={value} />} />;
}

function HashValue({ value }: { value: string }) {
  return (
    <CopyableField value={value}>
      <code className="mono-wrap">{value}</code>
    </CopyableField>
  );
}
