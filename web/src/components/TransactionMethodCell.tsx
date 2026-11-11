import { TableCell } from "@/components/ui/table";
export function TransactionMethodCell({
  method,
  signature,
}: {
  method?: string;
  signature?: string;
}) {
  const accessibleName = signature ?? method;
  return (
    <TableCell className="transaction-method-cell">
      <code aria-label={accessibleName} className="transaction-method" title={accessibleName}>
        {method ?? "—"}
      </code>
    </TableCell>
  );
}
