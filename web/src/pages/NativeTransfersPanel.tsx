import { Card } from "@/components/ui/card";
import { Alert } from "@/components/ui/alert";
import {
  Table,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from "@/components/ui/table";
import { BrowsingScope, BrowsingEmpty } from "@/components/BrowsingScope";
import * as sdk from "../api/heyapi/sdk.gen";
import { useBrowsingCursorHistory } from "@/api/pagination";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { apiClient, requireEnvelope } from "@/api/client";
import { statusQueryMeta } from "@/api/chainEvents";
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
  const pager = useBrowsingCursorHistory(`native-transfers:${kind}:${identity}`);
  const locale = i18n.resolvedLanguage ?? "en";
  const transfers = useQuery({
    queryKey: [
      "native-transfers",
      kind,
      identity,
      pager.request.cursor,
      pager.request.refreshGeneration,
    ],
    meta: {
      ...statusQueryMeta,
      pagination: pager.request.identity,
    },
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
    <Card asChild>
      <section className="panel transaction-tab-panel" aria-label={t("nativeTransfer.title")}>
        <h2>{t("nativeTransfer.title")}</h2>
        <BrowsingScope status={transfers.data?.meta.browsing}>
          <QueryNotice
            loading={transfers.isPending}
            error={transfers.error}
            onReset={pager.reset}
          />
          {transfers.data && !transfers.error ? (
            <>
              {!transfers.data.applicable ? (
                <p role="status">{t("nativeTransfer.unavailable")}</p>
              ) : (
                <>
                  {transfers.data.meta.coverage_start !== undefined && (
                    <Alert asChild>
                      <p className="context-note">
                        {t("nativeTransfer.coverage", {
                          start: transfers.data.meta.coverage_start,
                          end: transfers.data.meta.coverage_end,
                        })}
                      </p>
                    </Alert>
                  )}
                  {transfers.data.data.length === 0 ? (
                    <p role="status">
                      <BrowsingEmpty>{t("nativeTransfer.empty")}</BrowsingEmpty>
                    </p>
                  ) : (
                    <div className="table-scroll">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead>{t("table.hash")}</TableHead>
                            <TableHead>{t("table.block")}</TableHead>
                            <TableHead>{t("table.from")}</TableHead>
                            <TableHead>{t("table.to")}</TableHead>
                            <TableHead>{t("nativeTransfer.amount")}</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {transfers.data.data.map((item) => (
                            <TableRow key={`${item.block_hash}:${item.log_index}`}>
                              <TableCell>
                                <Link
                                  to="/tx/$hash"
                                  params={{ hash: item.transaction_hash }}
                                  search={{ tab: "native-transfers" }}
                                >
                                  {shorten(item.transaction_hash)}
                                </Link>
                              </TableCell>
                              <TableCell>
                                <Link to="/blocks/$blockID" params={{ blockID: item.block_hash }}>
                                  {formatInteger(item.block_number, locale)}
                                </Link>
                              </TableCell>
                              <TableCell>
                                <AddressIdentity
                                  nameOnly={kind === "transaction"}
                                  address={item.from}
                                />
                              </TableCell>
                              <TableCell>
                                <AddressIdentity
                                  nameOnly={kind === "transaction"}
                                  address={item.to}
                                />
                              </TableCell>
                              <TableCell>
                                <code>{formatInteger(item.amount, locale)}</code>
                              </TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
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
        </BrowsingScope>
      </section>
    </Card>
  );
}
