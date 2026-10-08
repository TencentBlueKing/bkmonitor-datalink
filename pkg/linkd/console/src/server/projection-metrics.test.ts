import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import { registerDeliveryMetricRoutes } from "./delivery-metrics.js";
import type { ConsoleConfig } from "./config.js";
import { deliveryMetricIDs } from "../shared/delivery-metrics.js";

afterEach(() => vi.unstubAllGlobals());
const from = "2026-10-06T01:00:00.000Z",
  to = "2026-10-06T02:00:00.000Z",
  start = Date.parse(from) / 1000;
const base =
  "/local-api/projection-metrics?" +
  new URLSearchParams({
    from,
    to,
    step: "15",
    instance: "worker",
    calculation_window_seconds: "300",
  });
function appFor(configured = true) {
  const app = Fastify();
  registerDeliveryMetricRoutes(app, {
    query: { timeoutMilliseconds: 1000, maxRangeSeconds: 604800 },
    ...(configured
      ? {
          prometheus: {
            baseUrl: "http://prometheus",
            auth: { apiKey: "private" },
          },
        }
      : {}),
  } as ConsoleConfig);
  return app;
}
function response(phase = "projection-producer", outcome = "advanced") {
  return {
    status: "success",
    data: {
      resultType: "matrix",
      result: [
        {
          metric: {
            job: "control",
            instance: "worker",
            linkd_task: phase,
            linkd_outcome: outcome,
          },
          values: [
            [start, "0"],
            [start + 15, "NaN"],
          ],
        },
      ],
    },
  };
}
it("queries exactly seven projection panels with independent phases and honest work labels", async () => {
  const queries: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: URL, init: RequestInit) => {
      const query = new URL(input).searchParams.get("query")!;
      queries.push(query);
      expect(init.redirect).toBe("error");
      expect((init.headers as Record<string, string>).authorization).toBe(
        "Bearer private",
      );
      expect(init.signal).toBeInstanceOf(AbortSignal);
      return Response.json(response());
    }),
  );
  const app = appFor();
  try {
    const result = await app.inject(base);
    expect(result.statusCode).toBe(200);
    expect(result.headers["cache-control"]).toBe("no-store");
    expect(result.json().panels.map((p: { id: string }) => p.id)).toEqual(
      deliveryMetricIDs.projection,
    );
    expect(queries).toHaveLength(7);
    for (const query of queries) {
      expect(query).toContain(
        'linkd_task=~"projection-producer|projection-delivery"',
      );
      expect(query).toContain('instance="worker"');
      expect(query).not.toMatch(
        /action_|tenant|alert_id|vector\(0\)|clamp_min|unconfirmed/,
      );
    }
    expect(
      queries.find((query) => query.includes("observations_total")),
    ).toContain("[300s]");
    const panel = result.json().panels[3];
    expect(panel.series[0].name).toContain("投影生产 · 已建或复用任务");
    expect(panel.series[0].points).toEqual([
      [start, 0],
      [start + 15, null],
      [Date.parse(to) / 1000, null],
    ]);
    expect(panel.description).toContain("不是 HTTP 请求数");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json(response("projection-delivery"))),
    );
    const delivered = await app.inject(base);
    expect(delivered.json().panels[3].series[0].name).toContain(
      "投影投递 · 已有本地 ACK",
    );
  } finally {
    await app.close();
  }
});

it("rejects action series and tenant inputs and keeps failure or absence separate from zero", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const app = appFor();
  try {
    for (const suffix of [
      "&bk_tenant_id=tenant",
      "&alert_id=alert",
      "&type=action",
      "&query=up",
    ])
      expect((await app.inject(base + suffix)).statusCode).toBe(400);
    expect(fetcher).not.toHaveBeenCalled();
    for (const value of [
      response("action-delivery"),
      response("projection-delivery", "accepted"),
    ]) {
      fetcher.mockImplementation(async () => Response.json(value));
      expect(
        (await app.inject(base))
          .json()
          .panels.every((p: { status: string }) => p.status === "unavailable"),
      ).toBe(true);
    }
    fetcher.mockImplementation(async (input: URL) =>
      new URL(input).searchParams.get("query")!.includes("duration")
        ? Response.json({ error: "private upstream" }, { status: 503 })
        : Response.json(response()),
    );
    const partial = await app.inject(base);
    expect(
      partial
        .json()
        .panels.filter((p: { status: string }) => p.status === "available"),
    ).toHaveLength(6);
    expect(partial.body).not.toContain("private upstream");
    fetcher.mockImplementation(async () =>
      Response.json({
        status: "success",
        data: { resultType: "matrix", result: [] },
      }),
    );
    expect((await app.inject(base)).json().panels[0]).toMatchObject({
      status: "unavailable",
      series: [],
      message: "查询范围内没有投影运行时序；不能据此判定任务已完成",
    });
  } finally {
    await app.close();
  }
  const absent = appFor(false);
  try {
    expect((await absent.inject(base)).json().panels[0].message).toBe(
      "Prometheus 未配置",
    );
  } finally {
    await absent.close();
  }
});
