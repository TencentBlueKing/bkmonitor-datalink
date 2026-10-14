import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import { registerDeliveryMetricRoutes } from "./delivery-metrics.js";
import type { ConsoleConfig } from "./config.js";
const config = {
  query: { timeoutMilliseconds: 5000, maxRangeSeconds: 604800 },
  prometheus: {
    baseUrl: "http://prometheus",
    auth: { apiKey: "private-test-token" },
  },
} as ConsoleConfig;
const from = "2026-10-06T01:00:00.000Z",
  to = "2026-10-06T02:00:00.000Z",
  start = Date.parse(from) / 1000,
  end = Date.parse(to) / 1000;
const base =
  "/local-api/action-metrics?" +
  new URLSearchParams({
    from,
    to,
    step: "15",
    calculation_window_seconds: "300",
  });
const item = () => ({
  metric: { job: "control", instance: "worker", linkd_stage: "action" },
  values: [
    [start, "0"],
    [start + 15, "NaN"],
  ],
});
const response = (result: unknown[] = [item()], extra = {}) => ({
  status: "success",
  data: { resultType: "matrix", result },
  ...extra,
});
afterEach(() => vi.unstubAllGlobals());
function appFor(c = config) {
  const app = Fastify();
  registerDeliveryMetricRoutes(app, c);
  return app;
}
it("queries throughput, duration and queue metrics with honest missing samples", async () => {
  const queries: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: URL) => {
      queries.push(new URL(input).searchParams.get("query")!);
      return Response.json(response());
    }),
  );
  const app = appFor();
  try {
    const result = await app.inject(base);
    expect(result.statusCode).toBe(200);
    expect(result.json().panels).toHaveLength(4);
    expect(queries).toHaveLength(4);
    expect(queries.some((q) => q.includes("linkd_kac_items_total"))).toBe(true);
    expect(queries.some((q) => q.includes("linkd_kac_queue_messages"))).toBe(
      true,
    );
    expect(
      queries
        .filter((q) => q.includes("queue_"))
        .every((q) => !q.includes("linkd_stage=")),
    ).toBe(true);
    expect(result.json().panels[0].series[0].points[0][1]).toBe(0);
    expect(result.json().panels[0].series[0].points[1][1]).toBeNull();
    expect(result.body).not.toContain("private-test-token");
  } finally {
    await app.close();
  }
});

it("rejects unsupported scope and range/point budgets before touching Prometheus", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const app = appFor();
  try {
    for (const suffix of [
      "&bk_tenant_id=tenant",
      "&event_source_id=source",
      "&alert_id=alert",
      "&url=http://other",
      "&instance=",
      "&step=1",
      "&from=" + to,
      "&calculation_window_seconds=0",
    ]) {
      expect((await app.inject(base + suffix)).statusCode).toBe(400);
    }
    expect(fetcher).not.toHaveBeenCalled();
  } finally {
    await app.close();
  }
});
it("reports configured absence and preserves per-panel partial availability", async () => {
  const absent = appFor({ ...config, prometheus: undefined });
  try {
    const r = await absent.inject(base);
    expect(r.statusCode).toBe(200);
    expect(
      r
        .json()
        .panels.every(
          (p: { message: string }) => p.message === "Prometheus 未配置",
        ),
    ).toBe(true);
  } finally {
    await absent.close();
  }
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: URL) =>
      new URL(input).searchParams.get("query")!.includes("duration")
        ? Response.json({ status: "error", error: "private upstream" })
        : Response.json(response()),
    ),
  );
  const app = appFor();
  try {
    const r = await app.inject(base);
    expect(
      r
        .json()
        .panels.filter((p: { status: string }) => p.status === "available"),
    ).toHaveLength(3);
    expect(r.body).not.toContain("private upstream");
  } finally {
    await app.close();
  }
});
it("rejects incomplete, foreign, unordered, over-budget and sensitive-label series", async () => {
  const app = appFor();
  try {
    const bad = [
      response([], { warnings: ["partial private"] }),
      response(Array(65).fill(item())),
      response([
        { ...item(), metric: { ...item().metric, instance: "other" } },
      ]),
      response([
        { ...item(), metric: { ...item().metric, bk_tenant_id: "private" } },
      ]),
      response([
        {
          ...item(),
          values: [
            [start + 15, "1"],
            [start, "2"],
          ],
        },
      ]),
      response([{ ...item(), values: [[end + 1, "1"]] }]),
      response([{ ...item(), values: [[start, ""]] }]),
      response([{ ...item(), values: Array(482).fill([start, "1"]) }]),
      response([
        {
          ...item(),
          values: Array.from({ length: 481 }, (_, i) => [start + i / 10, "1"]),
        },
      ]),
      { status: "success", data: { resultType: "vector", result: [] } },
    ];
    for (const value of bad) {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => Response.json(value)),
      );
      const r = await app.inject(base + "&instance=worker");
      expect(
        r
          .json()
          .panels.every((p: { status: string }) => p.status === "unavailable"),
      ).toBe(true);
      expect(r.body).not.toContain("partial private");
    }
    const canceled = vi.fn();
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(
            new ReadableStream({
              start(c) {
                c.enqueue(new Uint8Array((1 << 20) + 1));
              },
              cancel: canceled,
            }),
          ),
      ),
    );
    expect(
      (await app.inject(base))
        .json()
        .panels.every((p: { status: string }) => p.status === "unavailable"),
    ).toBe(true);
    expect(canceled).toHaveBeenCalledTimes(4);
  } finally {
    await app.close();
  }
});
it("limits two page requests and four queries per page, and releases permits after timeout", async () => {
  let inflight = 0,
    peak = 0;
  const requests: AbortSignal[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (_input: URL, init: RequestInit) =>
        new Promise((_resolve, reject) => {
          const signal = init.signal! as AbortSignal;
          requests.push(signal);
          inflight++;
          peak = Math.max(peak, inflight);
          signal.addEventListener(
            "abort",
            () => {
              inflight--;
              reject(new Error("private timeout"));
            },
            { once: true },
          );
        }),
    ),
  );
  const app = appFor({
    ...config,
    query: { ...config.query, timeoutMilliseconds: 100 },
  });
  try {
    const one = app.inject(base).then((r) => r),
      two = app
        .inject(base.replace("action-metrics", "projection-metrics"))
        .then((r) => r);
    await vi.waitFor(() => expect(requests).toHaveLength(8));
    expect((await app.inject(base)).statusCode).toBe(429);
    expect(
      (await app.inject(base.replace("action-metrics", "projection-metrics")))
        .statusCode,
    ).toBe(429);
    const results = await Promise.all([one, two]);
    expect(peak).toBe(8);
    expect(inflight).toBe(0);
    expect(requests.every((s) => s.aborted)).toBe(true);
    expect(
      results.every(
        (r) => r.statusCode === 200 && !r.body.includes("private timeout"),
      ),
    ).toBe(true);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json(response([]))),
    );
    expect((await app.inject(base)).statusCode).toBe(200);
  } finally {
    await app.close();
  }
});
