import { useEffect, useRef } from "react";
import { LineChart } from "echarts/charts";
import {
  DataZoomComponent,
  GridComponent,
  TooltipComponent,
} from "echarts/components";
import { init, use, type ECharts, type EChartsCoreOption } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";

import type { ChartPoint } from "@/api/types";
import { useTheme } from "@/theme/ThemeProvider";

use([LineChart, DataZoomComponent, GridComponent, TooltipComponent, CanvasRenderer]);

export function MetricChart({
  data,
  label,
  locale,
  resetKey,
}: {
  data: ChartPoint[];
  label: string;
  locale: string;
  resetKey: number;
}) {
  const { theme } = useTheme();
  const host = useRef<HTMLDivElement>(null);

  useEffect(() => {
    void resetKey;
    if (!host.current || data.length === 0) return;
    let chart: ECharts | undefined;
    try {
      chart = init(host.current, theme === "dark" ? "dark" : undefined, {
        renderer: "canvas",
      });
    } catch {
      // The exact table remains the complete, accessible fallback.
      return;
    }

    const option: EChartsCoreOption = {
      animation: !window.matchMedia?.("(prefers-reduced-motion: reduce)").matches,
      backgroundColor: "transparent",
      grid: { top: 24, right: 24, bottom: 76, left: 44, containLabel: true },
      tooltip: {
        trigger: "axis",
        formatter(parameters: unknown) {
          const items = Array.isArray(parameters) ? parameters : [parameters];
          const first = items[0] as { data?: { exact?: string; timestamp?: string } } | undefined;
          if (!first?.data) return "";
          const timestamp = first.data.timestamp
            ? new Intl.DateTimeFormat(locale, {
                dateStyle: "medium",
                timeStyle: "short",
                timeZone: "UTC",
              }).format(new Date(first.data.timestamp))
            : "";
          return `${timestamp} UTC<br/>${label}: <strong>${first.data.exact ?? "—"}</strong>`;
        },
      },
      xAxis: {
        type: "category",
        boundaryGap: false,
        data: data.map((point) => point.bucket_start),
        axisLabel: {
          formatter(value: string) {
            return new Intl.DateTimeFormat(locale, {
              month: "short",
              day: "numeric",
              timeZone: "UTC",
            }).format(new Date(value));
          },
        },
      },
      yAxis: { type: "value", scale: true },
      dataZoom: [
        { type: "inside", filterMode: "none" },
        { type: "slider", filterMode: "none", bottom: 12, height: 24 },
      ],
      series: [{
        name: label,
        type: "line",
        showSymbol: data.length <= 48,
        symbolSize: 5,
        smooth: false,
        lineStyle: { width: 2 },
        areaStyle: { opacity: 0.08 },
        data: data.map((point) => ({
          value: chartNumber(point.value),
          exact: point.value,
          timestamp: point.bucket_start,
        })),
      }],
    };
    chart.setOption(option);
    const resize = () => chart?.resize();
    const observer = typeof ResizeObserver === "undefined"
      ? undefined
      : new ResizeObserver(resize);
    if (observer) observer.observe(host.current);
    else window.addEventListener("resize", resize);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", resize);
      chart?.dispose();
    };
  }, [data, label, locale, resetKey, theme]);

  return <div aria-hidden="true" className="metric-detail-chart" ref={host} />;
}

function chartNumber(value: string): number | null {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}
