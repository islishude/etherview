import { useTranslation } from "react-i18next";
import type { BillingConfig } from "@/api/billing";
import { displayBillingAmount } from "@/billing/amount";

export function BillingPrices({ config }: { config: BillingConfig }) {
  const { t } = useTranslation();
  if (!config.api_billing_enabled) return null;
  return (
    <section className="panel billing-prices" aria-label={t("billing.workspace.prices")}>
      <h2>{t("billing.workspace.prices")}</h2>
      <p>{t("billing.workspace.pricePolicy")}</p>
      <p>
        <code>{config.asset}</code> · {config.network}
      </p>
      <div
        className="table-scroll"
        tabIndex={0}
        role="region"
        aria-label={t("billing.workspace.prices")}
      >
        <table>
          <thead>
            <tr>
              <th>{t("billing.fields.operation")}</th>
              <th>{t("billing.workspace.weight")}</th>
              <th>{t("billing.workspace.tokenAmount")}</th>
            </tr>
          </thead>
          <tbody>
            {config.operations.map((operation) => (
              <tr key={operation.operation}>
                <td>{operation.operation}</td>
                <td>{operation.complexity_weight}</td>
                <td>{displayBillingAmount(operation.amount_atomic, config)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
