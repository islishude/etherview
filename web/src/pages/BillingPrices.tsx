import { Card } from "@/components/ui/card";
import {
  Table,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from "@/components/ui/table";
import { useTranslation } from "react-i18next";
import type { BillingConfig } from "@/api/billing";
import { displayBillingAmount } from "@/billing/amount";

export function BillingPrices({ config }: { config: BillingConfig }) {
  const { t } = useTranslation();
  if (!config.api_billing_enabled) return null;
  return (
    <Card asChild>
      <section className="panel billing-prices" aria-label={t("billing.workspace.prices")}>
        <h2>{t("billing.workspace.prices")}</h2>
        <p>{t("billing.workspace.pricePolicy")}</p>
        <p>{t("billing.workspace.creditUnit")}</p>
        <div
          className="table-scroll"
          tabIndex={0}
          role="region"
          aria-label={t("billing.workspace.prices")}
        >
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("billing.fields.operation")}</TableHead>
                <TableHead>{t("billing.workspace.weight")}</TableHead>
                <TableHead>{t("billing.workspace.creditUnit")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {config.operations.map((operation) => (
                <TableRow key={operation.operation}>
                  <TableCell>{operation.operation}</TableCell>
                  <TableCell>{operation.complexity_weight}</TableCell>
                  <TableCell>{displayBillingAmount(operation.amount_atomic, config)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </section>
    </Card>
  );
}
