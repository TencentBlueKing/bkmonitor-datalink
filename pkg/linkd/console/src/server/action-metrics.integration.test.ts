import Fastify from "fastify";
import { expect, it, vi } from "vitest";
import { registerDeliveryMetricRoutes } from "./delivery-metrics.js";
import type { ConsoleConfig } from "./config.js";
// 显式启用，只对已有 Prometheus 发只读查询；不安装规则、不修改抓取或写入时序。
it.skipIf(!process.env.LINKD_TEST_PROMETHEUS_URL).each([
  ["action", 8],
  ["projection", 7],
] as const)(
  "executes all fixed %s queries against a real Prometheus engine",
  async (kind, count) => {
    const endpoint = process.env.LINKD_TEST_PROMETHEUS_URL!;
    const original = globalThis.fetch;
    const engineResults: unknown[] = [];
    const spy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (input, init) => {
        const response = await original(input, init);
        engineResults.push(await response.clone().json());
        return response;
      });
    const app = Fastify();
    registerDeliveryMetricRoutes(app, {
      prometheus: { baseUrl: endpoint, auth: {} },
      query: { maxRangeSeconds: 604800, timeoutMilliseconds: 5000 },
    } as ConsoleConfig);
    try {
      const to = new Date(),
        from = new Date(to.getTime() - 300000);
      const result = await app.inject(
        `/local-api/${kind}-metrics?` +
          new URLSearchParams({
            from: from.toISOString(),
            to: to.toISOString(),
            step: "15",
          }),
      );
      expect(result.statusCode).toBe(200);
      expect(engineResults).toHaveLength(count);
      for (const value of engineResults)
        expect(value).toMatchObject({
          status: "success",
          data: { resultType: "matrix" },
        });
      expect(result.json().panels).toHaveLength(count);
      expect(result.body).not.toContain(
        kind === "action" ? "动作指标查询失败" : "投影指标查询失败",
      );
    } finally {
      await app.close();
      spy.mockRestore();
    }
  },
);
