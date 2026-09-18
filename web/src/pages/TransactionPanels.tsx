import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import {
  useTransactionInternalTransactions,
  useTransactionAuthorizations,
  useTransactionTokenTransfers,
  useTransactionUserOperations,
} from "@/api/hooks";
import type { TransactionSummary } from "@/api/types";
import { formatInteger, formatNativeAmount, shorten } from "@/components/format";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { QueryNotice } from "@/components/QueryNotice";
import {
  CapabilityDegraded,
  CursorPagination,
  Detail,
  DetailList,
  NFTTokenIDLink,
  formatTokenEventAmount,
  isNFTStandard,
  type Translate,
  useCursorHistory,
} from "./pages";
import { UserOperationTable } from "@/components/UserOperationTable";
import { FeeSettings } from "./TransactionOverview";

export function TransactionAccessListPanel({
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
              <header>
                <strong>
                  <AddressIdentity address={entry.address} compact={false} />
                </strong>
              </header>
              {entry.storage_keys.length > 0 ? (
                <dl>
                  {entry.storage_keys.map((key) => (
                    <div key={key}>
                      <dt>{t("detail.storageKey")}</dt>
                      <dd>
                        <code>{key}</code>
                      </dd>
                    </div>
                  ))}
                </dl>
              ) : (
                <p className="quiet">{t("state.noStorageKeys")}</p>
              )}
            </article>
          ))}
        </div>
      ) : (
        <p className="empty-result">{t("state.emptyAccessList")}</p>
      )}
    </section>
  );
}

export function TransactionBlobPanel({
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
        <Detail
          label={t("detail.blobGasFees")}
          value={
            <FeeSettings
              locale={locale}
              entries={[
                { label: t("detail.blobBaseFee"), value: transaction.blob_base_fee_per_gas },
                { label: t("detail.feeMax"), value: transaction.max_fee_per_blob_gas },
              ]}
            />
          }
        />
        <Detail
          label={t("detail.blobCount")}
          value={formatInteger(transaction.blob_versioned_hashes?.length ?? 0, locale)}
        />
      </DetailList>
      {transaction.blob_versioned_hashes?.length ? (
        <div className="transaction-log-list">
          {transaction.blob_versioned_hashes.map((hash, index) => (
            <article className="transaction-log" key={hash}>
              <header>
                <strong>{t("detail.blobIndex", { index })}</strong>
              </header>
              <code className="mono-wrap">{hash}</code>
            </article>
          ))}
        </div>
      ) : (
        <p className="empty-result">{t("state.noBlobHashes")}</p>
      )}
    </section>
  );
}

export function TransactionAuthorizationsPanel({
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
      {identityCurrent &&
      authorizations.data?.state === "complete" &&
      authorizations.data.items.length === 0 ? (
        <p className="empty-result">{t("state.noAuthorizations")}</p>
      ) : null}
      {identityCurrent &&
      authorizations.data?.state === "complete" &&
      authorizations.data.items.length > 0 ? (
        <div className="transaction-log-list">
          {authorizations.data.items.map((authorization) => (
            <article className="transaction-log" key={authorization.index}>
              <header>
                <strong>{t("detail.authorizationIndex", { index: authorization.index })}</strong>
                <span>{authorization.application_status}</span>
              </header>
              <dl>
                <div>
                  <dt>{t("delegation.authority")}</dt>
                  <dd>
                    <code>{authorization.authority ?? "—"}</code>
                  </dd>
                </div>
                <div>
                  <dt>{t("delegation.delegate")}</dt>
                  <dd>
                    <code>{authorization.delegate}</code>
                  </dd>
                </div>
                <div>
                  <dt>{t("detail.chainID")}</dt>
                  <dd>
                    <code>{authorization.chain_id}</code>
                  </dd>
                </div>
                <div>
                  <dt>{t("detail.nonce")}</dt>
                  <dd>
                    <code>{authorization.nonce}</code>
                  </dd>
                </div>
                <div>
                  <dt>{t("detail.signatureStatus")}</dt>
                  <dd>{authorization.signature_status}</dd>
                </div>
                <div>
                  <dt>{t("detail.skipReason")}</dt>
                  <dd>{authorization.skip_reason ?? "—"}</dd>
                </div>
              </dl>
              <details className="transaction-more-details">
                <summary>{t("detail.rawAuthorization")}</summary>
                <dl>
                  <div>
                    <dt>yParity</dt>
                    <dd>
                      <code>{authorization.y_parity}</code>
                    </dd>
                  </div>
                  <div>
                    <dt>r</dt>
                    <dd>
                      <code>{authorization.r}</code>
                    </dd>
                  </div>
                  <div>
                    <dt>s</dt>
                    <dd>
                      <code>{authorization.s}</code>
                    </dd>
                  </div>
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

export function TransactionUserOperationsPanel({
  blockHash,
  hash,
}: {
  blockHash?: string;
  hash: string;
}) {
  const { t } = useTranslation();
  const pager = useCursorHistory(`transaction-user-operations:${hash}`);
  const operations = useTransactionUserOperations(hash, pager.cursor);
  const identityCurrent =
    !blockHash ||
    (operations.data?.items ?? []).every((operation) => operation.block_hash === blockHash);
  return (
    <section className="panel transaction-tab-panel" role="tabpanel">
      <QueryNotice loading={operations.isPending} error={operations.error} onReset={pager.reset} />
      {operations.data && !identityCurrent ? (
        <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
      ) : null}
      {identityCurrent && operations.data?.items.length === 0 ? (
        <p className="empty-result">{t("state.noTransactionUserOperations")}</p>
      ) : null}
      {identityCurrent && operations.data && operations.data.items.length > 0 ? (
        <UserOperationTable items={operations.data.items} />
      ) : null}
      {identityCurrent && operations.data ? (
        <CursorPagination
          busy={operations.isFetching}
          hasNext={Boolean(operations.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("transactionTabs.user-operations")}
          onNext={() => pager.next(operations.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      ) : null}
    </section>
  );
}

type InternalTransactionsResource = ReturnType<typeof useTransactionInternalTransactions>;

type CursorHistory = ReturnType<typeof useCursorHistory>;

export function TransactionInternalTransactionsPanel({
  identityCurrent,
  locale,
  nativeDecimals,
  nativeSymbol,
  pager,
  resource,
  t,
}: {
  identityCurrent: boolean;
  locale: string;
  nativeDecimals: number;
  nativeSymbol: string;
  pager: CursorHistory;
  resource: InternalTransactionsResource;
  t: Translate;
}) {
  return (
    <section
      className="panel transaction-tab-panel"
      role="tabpanel"
      aria-labelledby="transaction-internal-transactions-title"
    >
      <h2 id="transaction-internal-transactions-title">{t("addressTab.internalTransactions")}</h2>
      <QueryNotice loading={resource.isPending} error={resource.error} />
      {resource.data && !identityCurrent ? (
        <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
      ) : null}
      {identityCurrent && resource.data?.state !== "complete" && resource.data ? (
        <CapabilityDegraded stage="trace" state={resource.data.state} />
      ) : null}
      {identityCurrent &&
      resource.data?.state === "complete" &&
      resource.data.items.length === 0 ? (
        <p className="empty-result">{t("state.noTransactionInternalTransactions")}</p>
      ) : null}
      {identityCurrent && resource.data?.state === "complete" && resource.data.items.length > 0 ? (
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
              {resource.data.items.map((item) => {
                const destination = item.created_address ?? item.to;
                return (
                  <tr key={item.path.join(".")}>
                    <td>
                      <span className="transaction-trace-kind">{item.call_type}</span>
                    </td>
                    <td>
                      <AddressIdentity address={item.from} copy />
                    </td>
                    <td>
                      {destination ? (
                        <span className="table-primary">
                          <AddressIdentity address={destination} copy />
                          {item.created_address ? <small>{t("activity.created")}</small> : null}
                        </span>
                      ) : (
                        "—"
                      )}
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
      {identityCurrent && resource.data ? (
        <CursorPagination
          busy={resource.isFetching}
          hasNext={Boolean(resource.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("addressTab.internalTransactions")}
          onNext={() => pager.next(resource.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      ) : null}
    </section>
  );
}

export function TransactionTokenTransfersPanel({
  tokenTransfers,
  tokenIdentityCurrent,
  tokenPager,
  locale,
  t,
}: {
  tokenTransfers: ReturnType<typeof useTransactionTokenTransfers>;
  tokenIdentityCurrent: boolean;
  tokenPager: ReturnType<typeof useCursorHistory>;
  locale: string;
  t: Translate;
}) {
  return (
    <section className="panel transaction-tab-panel" role="tabpanel">
      <QueryNotice loading={tokenTransfers.isPending} error={tokenTransfers.error} />
      {tokenTransfers.data && !tokenIdentityCurrent && (
        <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
      )}
      {tokenIdentityCurrent && tokenTransfers.data?.state !== "complete" && tokenTransfers.data && (
        <CapabilityDegraded stage="token" state={tokenTransfers.data.state} />
      )}
      {tokenIdentityCurrent &&
        tokenTransfers.data?.state === "complete" &&
        tokenTransfers.data.items.length === 0 && (
          <p className="empty-result">{t("state.noTransactionTokenTransfers")}</p>
        )}
      {tokenIdentityCurrent &&
        tokenTransfers.data?.state === "complete" &&
        tokenTransfers.data.items.length > 0 && (
          <div className="table-scroll" tabIndex={0}>
            <table>
              <thead>
                <tr>
                  <th>{t("table.token")}</th>
                  <th>{t("detail.event")}</th>
                  <th>{t("table.from")}</th>
                  <th>{t("table.to")}</th>
                  <th>{t("detail.amountOrTokenID")}</th>
                </tr>
              </thead>
              <tbody>
                {tokenTransfers.data.items.map((event) => (
                  <tr key={`${event.log_index}:${event.sub_index}`}>
                    <td>
                      <Link to="/token/$address" params={{ address: event.token_address }}>
                        <code>{shorten(event.token_address)}</code>
                      </Link>
                      <small className="table-secondary">{event.standard}</small>
                    </td>
                    <td>{event.kind}</td>
                    <td>
                      <code>{event.from ? shorten(event.from) : "—"}</code>
                    </td>
                    <td>
                      <code>{event.to ? shorten(event.to) : "—"}</code>
                    </td>
                    <td>
                      <span className="table-primary">
                        {event.amount !== undefined ? (
                          <code>{formatTokenEventAmount(event, locale)}</code>
                        ) : event.token_id !== undefined && isNFTStandard(event.standard) ? (
                          <NFTTokenIDLink address={event.token_address} tokenID={event.token_id} />
                        ) : (
                          <code>{formatInteger(event.token_id, locale)}</code>
                        )}
                        {event.amount !== undefined &&
                        event.token_id !== undefined &&
                        isNFTStandard(event.standard) ? (
                          <small>
                            <NFTTokenIDLink
                              address={event.token_address}
                              prefix
                              tokenID={event.token_id}
                            />
                          </small>
                        ) : null}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
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
  );
}
