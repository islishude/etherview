import type { ChartInterval, ChartMetric } from "@/api/types";

export const chartMetrics = [
  "transactions",
  "failed-transactions",
  "average-tps",
  "erc20-transfers",
  "nft-transfers",
  "contract-creations",
  "blocks",
  "average-block-time",
  "gas-used",
  "gas-utilization",
  "average-base-fee",
  "execution-fees",
  "average-transaction-fee",
  "priority-fees",
  "burned-fees",
  "blob-gas-used",
  "average-blob-base-fee",
  "blob-burned-fees",
] as const satisfies readonly ChartMetric[];

export type ChartPreset = "24h" | "7d" | "30d" | "90d" | "1y" | "all" | "custom";

export interface ChartSearch {
  range: ChartPreset;
  from_time?: string;
  to_time?: string;
  interval: ChartInterval;
}

export function isChartMetric(value: string): value is ChartMetric {
  return chartMetrics.includes(value as ChartMetric);
}
