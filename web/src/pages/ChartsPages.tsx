import { FormEvent, lazy, Suspense, useEffect, useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { useChartMetric, useChartOverview, usePublicConfig } from "@/api/hooks";
import type {
  ChartInterval,
  ChartMetric,
  ChartMetricSeries,
  ChartOverview,
  ChartPoint,
  ChartPreview,
} from "@/api/types";
import {
  formatGweiFromWei,
  formatInteger,
  formatTimestamp,
} from "@/components/format";
import { QueryNotice } from "@/components/QueryNotice";

const MetricChart = lazy(async () => {
  const module = await import("@/components/MetricChart");
  return { default: module.MetricChart };
});

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

type ChartPreset = "24h" | "7d" | "30d" | "90d" | "1y" | "all" | "custom";

export interface ChartSearch {
  range: ChartPreset;
  from_time?: string;
  to_time?: string;
  interval: ChartInterval;
}

const sections: Array<{ key: string; metrics: ChartMetric[] }> = [
  {
    key: "activity",
    metrics: [
      "transactions", "failed-transactions", "average-tps",
      "erc20-transfers", "nft-transfers", "contract-creations",
    ],
  },
  {
    key: "blocksCapacity",
    metrics: ["blocks", "average-block-time", "gas-used", "gas-utilization"],
  },
  {
    key: "feesBurn",
    metrics: [
      "average-base-fee", "execution-fees", "average-transaction-fee",
      "priority-fees", "burned-fees",
    ],
  },
  {
    key: "blob",
    metrics: ["blob-gas-used", "average-blob-base-fee", "blob-burned-fees"],
  },
];

const overviewMetrics: ChartMetric[] = [
  "transactions", "average-tps", "execution-fees", "gas-utilization",
];

export function isChartMetric(value: string): value is ChartMetric {
  return chartMetrics.includes(value as ChartMetric);
}

export function ChartsPage() {
  const { i18n, t } = useTranslation();
  const overview = useChartOverview();
  const publicConfig = usePublicConfig();
  const locale = i18n.resolvedLanguage ?? "en";
  const previews = new Map(
    overview.data?.metrics.map((preview) => [preview.metric, preview]) ?? [],
  );
  const format = (metric: ChartMetric, value?: string) =>
    formatMetricValue(
      metric,
      value,
      locale,
      publicConfig.data?.native_decimals ?? 18,
      publicConfig.data?.native_symbol ?? "",
    );

  return (
    <ChartsPageFrame title={t("page.charts")} description={t("page.chartsDescription")}>
      <QueryNotice loading={overview.isPending} error={overview.error} />
      {overview.data && (
        <>
          <section className="charts-overview" aria-labelledby="charts-overview-title">
            <div className="charts-section-heading">
              <div>
                <span className="eyebrow">{t("charts.last24Hours")}</span>
                <h2 id="charts-overview-title">{t("charts.overviewStats")}</h2>
              </div>
              <CoverageBadge coverage={overview.data.coverage} />
            </div>
            <div className="charts-overview-grid">
              {overviewMetrics.map((metric) => {
                const preview = previews.get(metric);
                return (
                  <article className="overview-stat-card" key={metric}>
                    <span>{metricLabel(metric, t)}</span>
                    <strong>{format(metric, preview?.current_value)}</strong>
                    <Change value={preview?.change_percent} />
                  </article>
                );
              })}
            </div>
            {overview.data.pending && (
              <div className="analytics-pending" role="status">
                <span className="pulse-dot" aria-hidden="true" />
                <div>
                  <strong>{t("charts.backfillPending")}</strong>
                  <p>{t("charts.backfillPendingDetail", {
                    dirty: overview.data.coverage.dirty_hours,
                  })}</p>
                </div>
              </div>
            )}
          </section>
          {sections.map((section) => (
            <section className="chart-category" key={section.key} aria-labelledby={`chart-${section.key}`}>
              <div className="charts-section-heading">
                <div>
                  <span className="eyebrow">{t(`charts.categories.${section.key}.eyebrow`)}</span>
                  <h2 id={`chart-${section.key}`}>{t(`charts.categories.${section.key}.title`)}</h2>
                </div>
              </div>
              <div className="chart-card-grid">
                {section.metrics.map((metric) => (
                  <PreviewCard
                    format={format}
                    key={metric}
                    metric={metric}
                    preview={previews.get(metric)}
                  />
                ))}
              </div>
            </section>
          ))}
          <p className="charts-snapshot">
            {t("charts.snapshot")}{" "}
            <Link
              to="/blocks/$blockID"
              params={{ blockID: overview.data.snapshot.block_hash }}
            >
              <code>{overview.data.snapshot.block_number}</code>
            </Link>
          </p>
        </>
      )}
    </ChartsPageFrame>
  );
}

export function ChartMetricPage({
  metric,
  search,
  updateSearch,
}: {
  metric: ChartMetric;
  search: ChartSearch;
  updateSearch: (next: ChartSearch) => void;
}) {
  const { i18n, t } = useTranslation();
  const overview = useChartOverview();
  const publicConfig = usePublicConfig();
  const locale = i18n.resolvedLanguage ?? "en";
  const range = useMemo(
    () => resolveRange(search, overview.data?.coverage.available_from),
    [overview.data?.coverage.available_from, search],
  );
  const series = useChartMetric(metric, range.from, range.to, search.interval, range.valid);
  const [fromDate, setFromDate] = useState(range.from.slice(0, 10));
  const [toDate, setToDate] = useState(range.to.slice(0, 10));
  const [resetKey, setResetKey] = useState(0);
  const label = metricLabel(metric, t);
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";
  const display = (value?: string) =>
    formatMetricValue(metric, value, locale, nativeDecimals, nativeSymbol);

  useEffect(() => {
    setFromDate(range.from.slice(0, 10));
    setToDate(range.to.slice(0, 10));
  }, [range.from, range.to]);

  const choosePreset = (preset: Exclude<ChartPreset, "custom">) => {
    updateSearch({ range: preset, interval: search.interval });
  };
  const submitCustom = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const from = utcDateStart(fromDate);
    const to = utcDateEnd(toDate);
    if (!from || !to || from >= to) return;
    updateSearch({
      range: "custom", from_time: from, to_time: to, interval: search.interval,
    });
  };

  return (
    <ChartsPageFrame
      title={label}
      description={t("charts.detailDescription", { metric: label })}
    >
      <nav className="chart-breadcrumb" aria-label={t("charts.breadcrumb")}>
        <Link to="/charts">{t("page.charts")}</Link>
        <span aria-hidden="true">/</span>
        <span aria-current="page">{label}</span>
      </nav>
      <section className="panel chart-controls" aria-label={t("charts.controls")}>
        <div className="chart-presets" role="group" aria-label={t("charts.range")}>
          {(["24h", "7d", "30d", "90d", "1y", "all"] as const).map((preset) => (
            <button
              aria-pressed={search.range === preset}
              className={search.range === preset ? "active" : undefined}
              key={preset}
              onClick={() => choosePreset(preset)}
              type="button"
            >
              {t(`charts.presets.${preset}`)}
            </button>
          ))}
        </div>
        <form className="chart-custom-range" onSubmit={submitCustom}>
          <label>
            <span>{t("charts.fromDate")} · UTC</span>
            <input type="date" value={fromDate} onChange={(event) => setFromDate(event.target.value)} />
          </label>
          <label>
            <span>{t("charts.toDate")} · UTC</span>
            <input type="date" value={toDate} onChange={(event) => setToDate(event.target.value)} />
          </label>
          <button className="button" type="submit">{t("charts.apply")}</button>
        </form>
        <label className="chart-interval">
          <span>{t("charts.intervalControl")}</span>
          <select
            value={search.interval}
            onChange={(event) => updateSearch({
              ...search,
              interval: event.target.value as ChartInterval,
            })}
          >
            {(["auto", "hour", "day", "week", "month"] as const).map((interval) => (
              <option key={interval} value={interval}>{t(`charts.intervals.${interval}`)}</option>
            ))}
          </select>
        </label>
      </section>
      <QueryNotice loading={series.isPending || overview.isPending} error={series.error ?? overview.error} />
      {series.data && (
        <>
          <section className="chart-summary-grid" aria-label={t("charts.summary")}>
            {(["current", "highest", "lowest", metricIsAverage(metric) ? "average" : "total"] as const).map((key) => (
              <article className="chart-summary-card" key={key}>
                <span>{t(`charts.summaryLabels.${key}`)}</span>
                <strong>{display(series.data.summary[key])}</strong>
              </article>
            ))}
          </section>
          <section className="panel chart-detail-panel" aria-labelledby="chart-detail-title">
            <div className="charts-section-heading">
              <div>
                <span className="eyebrow">{t("charts.utcHistory")}</span>
                <h2 id="chart-detail-title">{label}</h2>
              </div>
              <div className="chart-actions">
                <button className="button" onClick={() => setResetKey((value) => value + 1)} type="button">
                  {t("charts.resetZoom")}
                </button>
                <button className="button primary" onClick={() => downloadCSV(series.data)} type="button">
                  {t("charts.downloadCSV")}
                </button>
              </div>
            </div>
            <Suspense fallback={<div className="metric-detail-chart chart-loading" aria-hidden="true" />}>
              <MetricChart
                data={series.data.points}
                label={label}
                locale={locale}
                resetKey={resetKey}
              />
            </Suspense>
            <ExactChartTable
              display={display}
              label={label}
              locale={locale}
              points={series.data.points}
            />
          </section>
          <div className="chart-detail-meta">
            <span>{t("charts.intervalControl")}: <code>{series.data.interval}</code></span>
            <span>{t("charts.points")}: <code>{series.data.points.length}</code></span>
            <span>{t("charts.snapshot")}:{" "}
              <Link to="/blocks/$blockID" params={{ blockID: series.data.snapshot.block_hash }}>
                <code>{series.data.snapshot.block_number}</code>
              </Link>
            </span>
          </div>
        </>
      )}
    </ChartsPageFrame>
  );
}

function PreviewCard({
  metric,
  preview,
  format,
}: {
  metric: ChartMetric;
  preview?: ChartPreview;
  format: (metric: ChartMetric, value?: string) => string;
}) {
  const { t } = useTranslation();
  return (
    <article className="chart-preview-card">
      <div className="chart-card-copy">
        <span>{metricLabel(metric, t)}</span>
        <strong>{format(metric, preview?.current_value)}</strong>
        <Change value={preview?.change_percent} />
      </div>
      <Sparkline points={preview?.points ?? []} />
      <Link
        className="chart-card-link"
        to="/charts/$metric"
        params={{ metric }}
        search={{ range: "7d", interval: "auto" }}
      >
        {t("charts.viewChart")} <span aria-hidden="true">→</span>
      </Link>
    </article>
  );
}

function Sparkline({ points }: { points: ChartPoint[] }) {
  const values = points.map((point) => Number(point.value)).filter(Number.isFinite);
  if (values.length < 2) return <div className="sparkline empty" aria-hidden="true" />;
  const minimum = Math.min(...values);
  const maximum = Math.max(...values);
  const spread = maximum - minimum || 1;
  const path = values.map((value, index) => {
    const x = (index / (values.length - 1)) * 100;
    const y = 34 - ((value - minimum) / spread) * 28;
    return `${x},${y}`;
  }).join(" ");
  return (
    <svg className="sparkline" viewBox="0 0 100 40" preserveAspectRatio="none" aria-hidden="true">
      <polyline points={path} />
    </svg>
  );
}

function Change({ value }: { value?: string }) {
  const { t } = useTranslation();
  if (value === undefined) return <small className="chart-change neutral">{t("charts.noComparison")}</small>;
  const negative = value.startsWith("-");
  return (
    <small className={`chart-change ${negative ? "negative" : "positive"}`}>
      <span aria-hidden="true">{negative ? "↓" : "↑"}</span>{" "}
      {value.replace("-", "")}% {t("charts.vsPrevious")}
    </small>
  );
}

function CoverageBadge({ coverage }: { coverage: ChartOverview["coverage"] }) {
  const { t } = useTranslation();
  return (
    <span className={`coverage-badge ${coverage.complete ? "complete" : "pending"}`}>
      <span className="status-dot" aria-hidden="true" />
      {coverage.complete
        ? t("charts.backfillComplete")
        : `${t("charts.backfillInProgress")} · ${coverage.backfill_progress}%`}
    </span>
  );
}

function ExactChartTable({
  points,
  label,
  locale,
  display,
}: {
  points: ChartPoint[];
  label: string;
  locale: string;
  display: (value?: string) => string;
}) {
  const { t } = useTranslation();
  return (
    <div className="table-scroll chart-exact-table" tabIndex={0} aria-label={t("charts.exactTable")}>
      <table>
        <caption>{t("charts.exactTableCaption")}</caption>
        <thead>
          <tr>
            <th>{t("charts.bucketStart")}</th>
            <th>{t("charts.bucketEnd")}</th>
            <th>{label}</th>
            <th>{t("charts.exactValue")}</th>
            <th>{t("charts.partial")}</th>
            <th>{t("charts.blockRange")}</th>
          </tr>
        </thead>
        <tbody>
          {points.map((point) => (
            <tr key={point.bucket_start}>
              <td><time dateTime={point.bucket_start}>{formatTimestamp(point.bucket_start, locale)}</time></td>
              <td><time dateTime={point.bucket_end}>{formatTimestamp(point.bucket_end, locale)}</time></td>
              <td>{display(point.value)}</td>
              <td><code>{point.value}</code></td>
              <td>{point.partial ? t("common.yes") : t("common.no")}</td>
              <td><code>{point.from_block} – {point.to_block}</code></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ChartsPageFrame({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <div className="page-stack inner-page charts-page">
      <header className="page-header charts-page-header">
        <span className="eyebrow">Etherview Analytics</span>
        <h1>{title}</h1>
        <p>{description}</p>
      </header>
      {children}
    </div>
  );
}

function resolveRange(search: ChartSearch, availableFrom?: string) {
  if (search.range === "custom" && search.from_time && search.to_time) {
    return { from: search.from_time, to: search.to_time, valid: search.from_time < search.to_time };
  }
  const now = new Date();
  const to = now.toISOString();
  if (search.range === "all" && availableFrom) {
    return { from: availableFrom, to, valid: availableFrom < to };
  }
  const milliseconds: Record<Exclude<ChartPreset, "all" | "custom">, number> = {
    "24h": 24 * 60 * 60 * 1_000,
    "7d": 7 * 24 * 60 * 60 * 1_000,
    "30d": 30 * 24 * 60 * 60 * 1_000,
    "90d": 90 * 24 * 60 * 60 * 1_000,
    "1y": 365 * 24 * 60 * 60 * 1_000,
  };
  const effectiveRange = search.range === "all" || search.range === "custom"
    ? "7d"
    : search.range;
  const duration = milliseconds[effectiveRange];
  return { from: new Date(now.getTime() - duration).toISOString(), to, valid: true };
}

function utcDateStart(value: string): string | undefined {
  const parsed = new Date(`${value}T00:00:00.000Z`);
  return Number.isNaN(parsed.getTime()) ? undefined : parsed.toISOString();
}

function utcDateEnd(value: string): string | undefined {
  const parsed = new Date(`${value}T00:00:00.000Z`);
  if (Number.isNaN(parsed.getTime())) return undefined;
  parsed.setUTCDate(parsed.getUTCDate() + 1);
  const now = new Date();
  return (parsed > now ? now : parsed).toISOString();
}

function metricLabel(metric: ChartMetric, t: ReturnType<typeof useTranslation>["t"]): string {
  return t(`charts.metrics.${metric}`);
}

function metricIsAverage(metric: ChartMetric) {
  return [
    "average-tps", "average-block-time", "gas-utilization",
    "average-base-fee", "average-transaction-fee", "average-blob-base-fee",
  ].includes(metric);
}

function formatMetricValue(
  metric: ChartMetric,
  value: string | undefined,
  locale: string,
  nativeDecimals: number,
  nativeSymbol: string,
): string {
  if (value === undefined) return "—";
  if (["execution-fees", "average-transaction-fee", "priority-fees", "burned-fees", "blob-burned-fees"].includes(metric)) {
    const formatted = formatScaledDecimal(value, nativeDecimals, locale);
    return `${formatted} ${nativeSymbol}`.trim();
  }
  if (metric === "average-base-fee" || metric === "average-blob-base-fee") {
    return `${formatGweiFromWei(value, locale)} Gwei`;
  }
  if (metric === "gas-utilization") return `${value}%`;
  if (metric === "average-block-time") return `${value} s`;
  if (metric === "average-tps") return value;
  return formatInteger(value, locale);
}

function formatScaledDecimal(value: string, decimals: number, locale: string): string {
  if (!/^(0|[1-9][0-9]*)(\.[0-9]+)?$/.test(value) || decimals < 0) return value;
  const [whole, fraction = ""] = value.split(".");
  const digits = `${whole}${fraction}`.replace(/^0+(?=\d)/, "");
  const sourceScale = fraction.length;
  const targetScale = sourceScale + decimals;
  const padded = digits.padStart(targetScale + 1, "0");
  const integer = padded.slice(0, -targetScale) || "0";
  const decimal = targetScale === 0 ? "" : padded.slice(-targetScale).replace(/0+$/, "");
  const grouped = formatInteger(integer, locale);
  return decimal ? `${grouped}.${decimal.slice(0, 18)}` : grouped;
}

function downloadCSV(series: ChartMetricSeries) {
  const header = "bucket_start,bucket_end,value,partial,from_block,to_block";
  const rows = series.points.map((point) => [
    point.bucket_start,
    point.bucket_end,
    point.value,
    point.partial ? "true" : "false",
    point.from_block,
    point.to_block,
  ].join(","));
  const blob = new Blob([[header, ...rows].join("\n") + "\n"], {
    type: "text/csv;charset=utf-8",
  });
  const link = document.createElement("a");
  const url = URL.createObjectURL(blob);
  link.href = url;
  link.download = [
    series.metric,
    series.from_time.slice(0, 10),
    series.to_time.slice(0, 10),
    series.interval,
  ].join("_") + ".csv";
  link.click();
  URL.revokeObjectURL(url);
}
