import type { FastifyInstance } from "fastify";
import { z } from "zod";
import {
  deliveryMetricQuery,
  type DeliveryMetricQuery,
  type DeliveryMetricKind,
} from "../shared/delivery-metrics.js";
import type { MetricPanel, MetricsResponse } from "../shared/contracts.js";
import type { ConsoleConfig } from "./config.js";
import { boundedJSON } from "./http-response.js";
import { prometheusAuthHeaders } from "./prometheus.js";
import {
  actionDefinitions,
  actionOutcomeNames,
} from "./action-metric-definitions.js";
import { projectionDefinitions } from "./projection-metric-definitions.js";

export type Definition = Pick<
  MetricPanel,
  "id" | "title" | "unit" | "description"
> & {
  query: (s: string, w: string) => string;
};
interface Family {
  kind: DeliveryMetricKind;
  name: string;
  definitions: Definition[];
  phases: readonly [string, ...string[]];
  outcomes: readonly [string, ...string[]];
  phaseNames: Record<string, string>;
  outcomeName: (outcome: string, phase: string) => string;
}
const families: Family[] = [
  {
    kind: "action",
    name: "动作",
    definitions: actionDefinitions,
    phases: ["action-enqueue", "action-delivery"],
    outcomes: [
      "succeeded",
      "failed",
      "cancelled",
      "enqueued",
      "accepted",
      "skipped",
      "waiting_projection",
      "blocked",
      "retrying",
      "deferred",
      "capacity",
      "unstarted",
    ],
    phaseNames: { "action-enqueue": "意图补扫", "action-delivery": "动作发送" },
    outcomeName: (outcome) => actionOutcomeNames[outcome],
  },
  {
    kind: "projection",
    name: "投影",
    definitions: projectionDefinitions,
    phases: ["projection-producer", "projection-delivery"],
    outcomes: [
      "succeeded",
      "failed",
      "cancelled",
      "advanced",
      "deferred",
      "capacity",
    ],
    phaseNames: {
      "projection-producer": "投影生产",
      "projection-delivery": "投影投递",
    },
    outcomeName: (outcome, phase) =>
      outcome === "advanced"
        ? phase === "projection-producer"
          ? "已建或复用任务"
          : "已有本地 ACK"
        : actionOutcomeNames[outcome],
  },
];
function matrixSchema(family: Family) {
  const labels = z
    .object({
      job: z.string().max(512).optional(),
      instance: z.string().max(512).optional(),
      linkd_task: z.enum(family.phases),
      linkd_outcome: z.enum(family.outcomes).optional(),
      linkd_statistic: z.enum(["运行", "执行中"]).optional(),
    })
    .strict();
  return z.object({
    status: z.literal("success"),
    warnings: z.array(z.string()).max(0).optional(),
    data: z.object({
      resultType: z.literal("matrix"),
      result: z
        .array(
          z.object({
            metric: labels,
            values: z
              .array(z.tuple([z.number().finite(), z.string().max(64)]))
              .max(481),
          }),
        )
        .max(64),
    }),
  });
}
function unavailable(d: Definition, message: string): MetricPanel {
  const { query: _, ...metadata } = d;
  void _;
  return {
    ...metadata,
    kind: "line",
    status: "unavailable",
    message,
    series: [],
  };
}
// 投影与动作共用两个浏览器请求额度，每页最多四路 Prometheus；输入、响应和时间均有硬上限。
export function registerDeliveryMetricRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
) {
  let active = 0;
  for (const family of families) {
    const definitions = family.definitions,
      matrix = matrixSchema(family);
    app.get(`/local-api/${family.kind}-metrics`, async (request, reply) => {
      reply.header("Cache-Control", "no-store");
      const parsed = deliveryMetricQuery.safeParse(request.query);
      if (
        !parsed.success ||
        (Date.parse(parsed.data.to) - Date.parse(parsed.data.from)) / 1000 >
          config.query.maxRangeSeconds
      )
        return reply.code(400).send({
          error: { message: `${family.name}指标查询范围或参数不合法` },
        });
      if (active >= 2)
        return reply.code(429).send({
          error: { message: `${family.name}指标查询繁忙，请稍后重试` },
        });
      active++;
      const abort = new AbortController(),
        disconnected = () => {
          if (!reply.raw.writableEnded) abort.abort();
        },
        canceled = () => abort.abort();
      request.raw.once("aborted", canceled);
      reply.raw.once("close", disconnected);
      const signal = AbortSignal.any([
        abort.signal,
        AbortSignal.timeout(Math.min(10000, config.query.timeoutMilliseconds)),
      ]);
      try {
        const q = parsed.data,
          panels: Array<MetricPanel> = new Array(definitions.length);
        let next = 0;
        await Promise.all(
          Array.from({ length: 4 }, async () => {
            while (next < definitions.length) {
              const i = next++;
              panels[i] = await readPanel(
                config,
                q,
                definitions[i],
                signal,
                family,
                matrix,
              );
            }
          }),
        );
        return {
          from: q.from,
          to: q.to,
          step: q.step,
          panels,
        } satisfies MetricsResponse;
      } finally {
        active--;
        request.raw.off("aborted", canceled);
        reply.raw.off("close", disconnected);
      }
    });
  }
}
async function readPanel(
  config: ConsoleConfig,
  q: DeliveryMetricQuery,
  d: Definition,
  signal: AbortSignal,
  family: Family,
  matrix: ReturnType<typeof matrixSchema>,
): Promise<MetricPanel> {
  if (!config.prometheus) return unavailable(d, "Prometheus 未配置");
  try {
    signal.throwIfAborted();
    const scope = `{linkd_task=~"${family.phases.join("|")}"${q.instance ? ",instance=" + JSON.stringify(q.instance) : ""}}`;
    const url = new URL(
      config.prometheus.baseUrl.replace(/\/$/, "") + "/api/v1/query_range",
    );
    url.searchParams.set(
      "query",
      d.query(scope, `${q.calculation_window_seconds}s`),
    );
    const from = Date.parse(q.from) / 1000,
      to = Date.parse(q.to) / 1000;
    url.searchParams.set("start", String(from));
    url.searchParams.set("end", String(to));
    url.searchParams.set("step", String(q.step));
    const response = await fetch(url, {
      headers: {
        accept: "application/json",
        ...prometheusAuthHeaders(config.prometheus.auth),
      },
      signal,
      redirect: "error",
    });
    if (!response.ok) {
      await response.body?.cancel();
      throw new Error("status");
    }
    const data = matrix.parse(await boundedJSON(response, 1 << 20));
    const end = from + Math.floor((to - from) / q.step) * q.step;
    const series = data.data.result.map((item) => {
      if (q.instance && item.metric.instance !== q.instance)
        throw new Error("scope");
      let previous = -Infinity;
      const points = item.values.map(([at, raw]): [number, number | null] => {
        if (at < from - 0.001 || at > end + 0.001 || at <= previous)
          throw new Error("sample time");
        previous = at;
        if (["NaN", "+Inf", "-Inf"].includes(raw)) return [at, null];
        if (
          !/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(raw) ||
          !Number.isFinite(Number(raw))
        )
          throw new Error("sample value");
        return [at, Number(raw)];
      });
      if (points.length && points.at(-1)![0] < end - 0.001) {
        if (points.length >= 481) throw new Error("sample budget");
        points.push([end, null]);
      }
      return {
        name: [
          item.metric.job,
          item.metric.instance,
          family.phaseNames[item.metric.linkd_task],
          item.metric.linkd_statistic,
          item.metric.linkd_outcome
            ? family.outcomeName(
                item.metric.linkd_outcome,
                item.metric.linkd_task,
              )
            : undefined,
        ]
          .filter(Boolean)
          .join(" · "),
        labels: item.metric,
        points,
      };
    });
    if (!series.length)
      return unavailable(
        d,
        `查询范围内没有${family.name}运行时序；不能据此判定任务已完成`,
      );
    return {
      ...unavailable(d, ""),
      status: "available",
      message: undefined,
      series,
    };
  } catch {
    return unavailable(
      d,
      `${family.name}指标查询失败、结果不完整或超过显示预算，请缩小范围或指定进程`,
    );
  }
}
