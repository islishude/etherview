import {
  Table,
  TableCaption,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { usePublicConfig } from "@/api/publicConfig";
import type { UserOperationSummary } from "@/api/types";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { formatInteger, formatNativeAmount, shorten } from "./format";

export function UserOperationTable({ items }: { items: UserOperationSummary[] }) {
  const { i18n, t } = useTranslation();
  const config = usePublicConfig();
  const locale = i18n.resolvedLanguage ?? "en";
  const nativeDecimals = config.data?.native_decimals ?? 18;
  return (
    <div className="table-scroll" tabIndex={0} aria-label={t("page.userOperations")}>
      <Table>
        <TableCaption className="sr-only">{t("page.userOperationsDescription")}</TableCaption>
        <TableHeader>
          <TableRow>
            <TableHead>{t("userOperation.hash")}</TableHead>
            <TableHead>{t("table.status")}</TableHead>
            <TableHead>{t("userOperation.sender")}</TableHead>
            <TableHead className="numeric">{t("table.block")}</TableHead>
            <TableHead>{t("userOperation.bundler")}</TableHead>
            <TableHead>{t("userOperation.paymaster")}</TableHead>
            <TableHead className="numeric">{t("userOperation.gasCost")}</TableHead>
            <TableHead>{t("table.finality")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((operation) => (
            <TableRow key={operation.hash}>
              <TableCell>
                <Link to="/user-op/$hash" params={{ hash: operation.hash }}>
                  {shorten(operation.hash)}
                </Link>
                <small className="table-secondary">
                  v{operation.entry_point_version} · #{operation.operation_index} ·{" "}
                  {t("userOperation.eventShort")} #{operation.event_log_index}
                </small>
              </TableCell>
              <TableCell>
                <UserOperationStatus success={operation.success} />
              </TableCell>
              <TableCell>
                <AddressIdentity address={operation.sender} />
                {operation.participating_roles?.length ? (
                  <small className="table-secondary">
                    {operation.participating_roles
                      .map((role) => t(`userOperation.role.${role}`))
                      .join(", ")}
                  </small>
                ) : null}
              </TableCell>
              <TableCell className="numeric">
                <Link to="/blocks/$blockID" params={{ blockID: operation.block_hash }}>
                  {formatInteger(operation.block_number, locale)}
                </Link>
              </TableCell>
              <TableCell>
                <AddressIdentity address={operation.bundler} />
              </TableCell>
              <TableCell>
                {operation.paymaster ? <AddressIdentity address={operation.paymaster} /> : "—"}
              </TableCell>
              <TableCell className="numeric">
                <code>{formatNativeAmount(operation.actual_gas_cost, locale, nativeDecimals)}</code>
              </TableCell>
              <TableCell>
                <UserOperationFinality finality={operation.finality} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

export function UserOperationStatus({ success }: { success: boolean }) {
  const { t } = useTranslation();
  return (
    <Badge variant="status" className={success ? "availability yes" : "availability no"}>
      {success ? t("userOperation.success") : t("userOperation.failed")}
    </Badge>
  );
}

function UserOperationFinality({ finality }: { finality: string }) {
  const { t } = useTranslation();
  return <span className={`finality ${finality}`}>{t(`finality.${finality}`)}</span>;
}
