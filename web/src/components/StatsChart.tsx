import { useEffect, useRef } from "react";
import { LineChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import { init, use, type ECharts, type EChartsCoreOption } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { useTranslation } from "react-i18next";

import type { BlockStat } from "@/api/types";
import { useTheme } from "@/theme/ThemeProvider";

use([LineChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer]);

export function StatsChart({ data }: { data: BlockStat[] }) {
  const { t } = useTranslation();
  const { theme } = useTheme();
  const host = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!host.current || data.length === 0) return;

    let chart: ECharts | undefined;
    try {
      chart = init(host.current, theme === "dark" ? "dark" : undefined, {
        renderer: "canvas",
      });
    } catch {
      // The exact-value table remains usable if this browser cannot create a canvas.
      return;
    }

    const option: EChartsCoreOption = {
      animation: !window.matchMedia?.("(prefers-reduced-motion: reduce)").matches,
      backgroundColor: "transparent",
      tooltip: { trigger: "axis" },
      legend: { top: 0 },
      grid: { top: 52, right: 24, bottom: 42, left: 72, containLabel: true },
      xAxis: {
        type: "category",
        boundaryGap: false,
        data: data.map((item) => item.block_number),
      },
      yAxis: { type: "value", scale: true },
      series: [
        lineSeries(t("charts.transactions"), data.map((item) => item.transaction_count)),
        lineSeries(t("charts.gasUsed"), data.map((item) => item.gas_used)),
        lineSeries(t("charts.baseFee"), data.map((item) => item.base_fee_per_gas)),
        lineSeries(t("charts.burned"), data.map((item) => item.burned_wei)),
      ],
    };
    chart.setOption(option);

    const resize = () => chart?.resize();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(resize);
    if (observer) observer.observe(host.current);
    else window.addEventListener("resize", resize);

    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", resize);
      chart?.dispose();
    };
  }, [data, t, theme]);

  return <div aria-hidden="true" className="stats-chart" ref={host} />;
}

function lineSeries(name: string, values: Array<string | undefined>) {
  return {
    name,
    type: "line" as const,
    data: values.map(chartNumber),
    connectNulls: false,
    showSymbol: values.length <= 40,
    symbolSize: 5,
    smooth: false,
  };
}

function chartNumber(value: string | undefined): number | null {
  if (value === undefined) return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}
