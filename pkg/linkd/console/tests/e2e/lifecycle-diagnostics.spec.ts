import { expect, test } from "@playwright/test";

test("shows Event diagnostics and physical Bulk counts on desktop and mobile", async ({
  page,
}, info) => {
  await page.route("**/local-api/capabilities", (route) =>
    route.fulfill({
      json: {
        version: "0.1.0",
        metrics: { configured: true, source: "prometheus" },
        entities: {},
        storage: { elasticsearch: { configured: true } },
        limits: {
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 604800,
          defaultLimit: 50,
          maxLimit: 200,
        },
      },
    }),
  );
  await page.route("**/local-api/runtime/lifecycle", (route) =>
    route.fulfill({
      json: {
        status: "available",
        config: {
          concurrency: 256,
          elasticsearchWriteBatch: {
            enabled: true,
            max_operations: 100,
            wait_milliseconds: 100,
            max_concurrent_batches: 32,
            max_bytes: 4194304,
          },
        },
      },
    }),
  );
  const points = (n: number): [number, number][] =>
    Array.from({ length: 12 }, (_, i) => [
      1788630000 + i * 15,
      n * (0.7 + i / 30),
    ]);
  const line = (
    id: string,
    title: string,
    unit: string,
    values: Record<string, number>,
  ) => ({
    id,
    title,
    unit,
    kind: "line",
    status: "available",
    series: Object.entries(values).map(([name, n]) => ({
      name,
      labels: { linkd_statistic: name, linkd_stage: "lifecycle" },
      points: points(n),
    })),
  });
  const count = (id: string, values: Record<string, number>) => ({
    id,
    title: id,
    unit: "operation",
    kind: "stat",
    status: "available",
    series: Object.entries(values).map(([name, n]) => ({
      name,
      labels: { linkd_outcome: name },
      points: [[1788630165, n]],
    })),
  });
  await page.route("**/local-api/metrics**", (route) =>
    route.fulfill({
      json: {
        from: "2026-09-06T00:00:00Z",
        to: "2026-09-06T01:00:00Z",
        step: 15,
        panels: [
          count("lifecycle-batch-executions", {
            succeeded: 7100,
            partial_failed: 12,
            failed: 2,
          }),
          count("lifecycle-batch-items", { succeeded: 564500, failed: 22 }),
          line("pipeline-completed", "阶段完成速率", "event/s", { 完成: 937 }),
          line("signal-backlog", "Signal 近似积压", "signal", { 积压: 128 }),
          line("pipeline-average", "单 Event 平均耗时", "s", { 平均: 0.09 }),
          line("pipeline-p99", "单 Event P99", "s", { P99: 0.39 }),
          line("lifecycle-batch-rate", "Bulk 执行速率", "batch/s", {
            succeeded: 39,
          }),
          line("lifecycle-batch-write-rate", "写操作结果速率", "operation/s", {
            succeeded: 3136,
            failed: 0.2,
          }),
          line("lifecycle-batch-size", "实际每批操作数", "operation/batch", {
            平均: 79,
            P95: 98,
            P99: 100,
          }),
          line("lifecycle-batch-bytes", "平均批次字节数", "MiB", {
            平均: 0.34,
          }),
          line("lifecycle-batch-queue", "批次首项排队耗时", "ms", {
            平均: 25,
            P95: 72,
            P99: 100,
          }),
          line("lifecycle-batch-duration", "Bulk 请求执行耗时", "ms", {
            平均: 7.7,
            P95: 15,
            P99: 25,
          }),
        ],
      },
    }),
  );
  await page.goto("/lifecycle");
  await expect(
    page.getByRole("heading", { name: "ES 分批写入", exact: true }),
  ).toBeVisible();
  const bulk = page.getByRole("region", { name: "ES 分批写入", exact: true });
  await expect(bulk.getByText("7,114", { exact: true })).toBeVisible();
  await expect(bulk.getByText("564,522", { exact: true })).toBeVisible();
  await expect(bulk.getByText("564,500", { exact: true })).toBeVisible();
  await expect(bulk.getByText("22", { exact: true })).toBeVisible();
  await expect(bulk.locator("canvas")).toHaveCount(6);
  await bulk.scrollIntoViewIfNeeded();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: info.outputPath("lifecycle-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("heading", { name: "ES 分批写入", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("lifecycle-mobile.png"),
    fullPage: true,
  });
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    )
    .toBe(true);
});
