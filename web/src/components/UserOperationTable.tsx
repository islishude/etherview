import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { usePublicConfig } from "@/api/hooks";
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
      <table>
        <caption className="sr-only">{t("page.userOperationsDescription")}</caption>
        <thead>
          <tr>
            <th>{t("userOperation.hash")}</th>
            <th>{t("table.status")}</th>
            <th>{t("userOperation.sender")}</th>
            <th>{t("table.block")}</th>
            <th>{t("userOperation.bundler")}</th>
            <th>{t("userOperation.paymaster")}</th>
            <th>{t("userOperation.gasCost")}</th>
            <th>{t("table.finality")}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((operation) => (
            <tr key={operation.hash}>
              <td>
                <Link to="/user-op/$hash" params={{ hash: operation.hash }}>
                  {shorten(operation.hash)}
                </Link>
                <small className="table-secondary">
                  v{operation.entry_point_version} · #{operation.operation_index} · {t("userOperation.eventShort")} #{operation.event_log_index}
                </small>
              </td>
              <td><UserOperationStatus success={operation.success} /></td>
              <td>
                <AddressIdentity address={operation.sender} />
                {operation.participating_roles?.length ? (
                  <small className="table-secondary">
                    {operation.participating_roles.map((role) =>
                      t(`userOperation.role.${role}`)
                    ).join(", ")}
                  </small>
                ) : null}
              </td>
              <td>
                <Link to="/blocks/$blockID" params={{ blockID: operation.block_hash }}>
                  {formatInteger(operation.block_number, locale)}
                </Link>
              </td>
              <td><AddressIdentity address={operation.bundler} /></td>
              <td>{operation.paymaster ? <AddressIdentity address={operation.paymaster} /> : "—"}</td>
              <td><code>{formatNativeAmount(operation.actual_gas_cost, locale, nativeDecimals)}</code></td>
              <td><UserOperationFinality finality={operation.finality} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function UserOperationStatus({ success }: { success: boolean }) {
  const { t } = useTranslation();
  return (
    <span className={success ? "availability yes" : "availability no"}>
      {success ? t("userOperation.success") : t("userOperation.failed")}
    </span>
  );
}

function UserOperationFinality({ finality }: { finality: string }) {
  const { t } = useTranslation();
  return <span className={`finality ${finality}`}>{t(`finality.${finality}`)}</span>;
}
