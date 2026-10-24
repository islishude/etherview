import * as sdk from "../api/heyapi/sdk.gen";
import { useCursorHistory } from "@/api/pagination";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { apiClient, requireEnvelope } from "@/api/client";
import { chainQueryMeta } from "@/api/chainEvents";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { QueryNotice } from "@/components/QueryNotice";
import { formatInteger, shorten } from "@/components/format";
import { CORE_PAGE_SIZE, CursorPagination } from "./pages";

export function NativeTransfersPanel({
  kind,
  identity,
}: {
  kind: "address" | "transaction";
  identity: string;
}) {
  const { t, i18n } = useTranslation();
  const pager = useCursorHistory(`native-transfers:${kind}:${identity}`);
  const locale = i18n.resolvedLanguage ?? "en";
  const transfers = useQuery({
    queryKey: [
      "native-transfers",
      kind,
      identity,
      pager.request.cursor,
      pager.request.refreshGeneration,
    ],
    meta: chainQueryMeta,
    retry: false,
    queryFn: async ({ signal }) => {
      const query = { cursor: pager.request.cursor, limit: CORE_PAGE_SIZE };
      return kind === "address"
        ? requireEnvelope(
            await sdk.listAddressNativeTransfers({
              client: apiClient,
              signal,
              path: { address: identity },
              query,
            }),
          )
        : requireEnvelope(
            await sdk.listTransactionNativeTransfers({
              client: apiClient,
              signal,
              path: { hash: identity },
              query,
            }),
          );
    },
  });
  return (
    <section className="panel transaction-tab-panel" aria-label={t("nativeTransfer.title")}>
      <h2>{t("nativeTransfer.title")}</h2>
      <p className="context-note">{t("nativeTransfer.source")}</p>
      <QueryNotice loading={transfers.isPending} error={transfers.error} onReset={pager.reset} />
      {transfers.data && !transfers.error ? (
        <>
          {!transfers.data.applicable ? (
            <p role="status">{t("nativeTransfer.unavailable")}</p>
          ) : (
            <>
              <p className="context-note">
                {t("nativeTransfer.coverage", {
                  start: transfers.data.meta.coverage_start,
                  end: transfers.data.meta.coverage_end,
                })}
              </p>
              {transfers.data.data.length === 0 ? (
                <p role="status">{t("nativeTransfer.empty")}</p>
              ) : (
                <div className="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>{t("table.hash")}</th>
                        <th>{t("table.block")}</th>
                        <th>{t("table.from")}</th>
                        <th>{t("table.to")}</th>
                        <th>{t("nativeTransfer.amount")}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {transfers.data.data.map((item) => (
                        <tr key={`${item.block_hash}:${item.log_index}`}>
                          <td>
                            <Link
                              to="/tx/$hash"
                              params={{ hash: item.transaction_hash }}
                              search={{ tab: "native-transfers" }}
                            >
                              {shorten(item.transaction_hash)}
                            </Link>
                          </td>
                          <td>
                            <Link to="/blocks/$blockID" params={{ blockID: item.block_hash }}>
                              {formatInteger(item.block_number, locale)}
                            </Link>
                          </td>
                          <td>
                            <AddressIdentity address={item.from} />
                          </td>
                          <td>
                            <AddressIdentity address={item.to} />
                          </td>
                          <td>
                            <code>{formatInteger(item.amount, locale)}</code>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <CursorPagination
                busy={transfers.isFetching}
                hasNext={Boolean(transfers.data.meta.next_cursor)}
                hasPrevious={pager.hasPrevious}
                label={t("nativeTransfer.title")}
                onNext={() => pager.next(transfers.data?.meta.next_cursor)}
                onPrevious={pager.previous}
                page={pager.page}
              />
            </>
          )}
        </>
      ) : null}
    </section>
  );
}
