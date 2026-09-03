import { expect, test } from "@playwright/test";

test("shows the overview and storage navigation", async ({ page }) => {
  for (const endpoint of ["processes", "cleaner", "lifecycle"]) {
    await page.route(`**/local-api/runtime/${endpoint}`, async (route) => {
      await route.fulfill({
        json: { status: "available", items: [], eventSources: [] },
      });
    });
  }
  await page.route("**/local-api/capabilities", async (route) => {
    await route.fulfill({
      json: {
        version: "0.1.0",
        metrics: { configured: true, source: "prometheus" },
        entities: {
          events: { source: "mysql", filters: ["state"] },
          alerts: { source: "mysql", filters: ["status"] },
          "alert-logs": { source: "mysql", filters: ["alertId"] },
        },
        storage: { elasticsearch: { configured: false } },
        limits: {
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 604800,
          defaultLimit: 50,
          maxLimit: 200,
        },
      },
    });
  });
  await page.route("**/local-api/metrics**", async (route) => {
    await route.fulfill({
      json: {
        from: "2026-08-30T00:00:00Z",
        to: "2026-08-30T01:00:00Z",
        step: 15,
        panels: [
          {
            id: "retry-rate",
            title: "重试速率",
            unit: "retry/s",
            kind: "line",
            status: "unavailable",
            message: "查询范围内没有对应时序",
            series: [],
          },
        ],
      },
    });
  });
  await page.goto("/overview");
  await expect(page.getByRole("heading", { name: "处理状态" })).toBeVisible();
  await expect(page.getByText("READ ONLY")).toBeVisible();
  await expect(page.getByRole("link", { name: /Events/ })).toBeVisible();
  await expect(page.getByRole("link", { name: "C Cleaner" })).toBeVisible();
  await expect(page.getByText("EventSource / Kafka")).toBeVisible();

  const metricTitle = page.getByRole("heading", { name: "重试速率" });
  const metricUnit = page.getByText("retry/s");
  const titleBox = await metricTitle.boundingBox();
  const unitBox = await metricUnit.boundingBox();
  expect(titleBox).not.toBeNull();
  expect(unitBox).not.toBeNull();
  expect(unitBox!.y).toBeGreaterThanOrEqual(titleBox!.y + titleBox!.height);

  await page.getByLabel("重试速率说明").hover();
  await expect(page.getByRole("tooltip")).toContainText("可恢复失败");
});

test("can leave a failed query page through the sidebar", async ({ page }) => {
  await page.route("**/local-api/capabilities", async (route) => {
    await route.fulfill({
      json: {
        version: "0.1.0",
        metrics: { configured: true, source: "prometheus" },
        entities: {
          events: { source: "elasticsearch", filters: ["state"] },
          alerts: { source: "elasticsearch", filters: ["status"] },
          "alert-logs": {
            source: "elasticsearch",
            filters: ["alertId"],
          },
        },
        storage: { elasticsearch: { configured: true } },
        limits: {
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 604800,
          defaultLimit: 50,
          maxLimit: 200,
        },
      },
    });
  });
  await page.route("**/local-api/alerts**", async (route) => {
    await route.fulfill({
      status: 502,
      json: {
        error: {
          code: "data_source_error",
          message: "测试数据源不可用",
          requestId: "request-a",
        },
      },
    });
  });
  await page.route("**/local-api/events**", async (route) => {
    await route.fulfill({
      json: { items: [], source: "elasticsearch", warnings: [] },
    });
  });

  await page.goto("/explore/alerts");
  await expect(page.getByText("查询失败：测试数据源不可用")).toBeVisible();
  await expect(page.getByRole("link", { name: /Events/ })).toHaveAttribute(
    "data-reload-document",
    "true",
  );
  await page.getByRole("link", { name: /Events/ }).click();

  await expect(page).toHaveURL(/\/explore\/events$/);
  await expect(
    page.getByRole("heading", { name: "Event Explorer" }),
  ).toBeVisible();
});

test("opens a fixed history drawer from a Cleaner node", async ({ page }) => {
  await page.route("**/local-api/capabilities", async (route) => {
    await route.fulfill({
      json: {
        version: "0.1.0",
        metrics: { configured: true, source: "prometheus" },
        entities: {},
        storage: { elasticsearch: { configured: false } },
        limits: {
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 604800,
          defaultLimit: 50,
          maxLimit: 200,
        },
      },
    });
  });
  await page.route("**/local-api/runtime/cleaner", async (route) => {
    await route.fulfill({
      json: {
        status: "available",
        eventSources: [
          {
            eventSourceId: "source-a",
            enabled: true,
            cleanerType: "mapping",
            runtime: { workers: 2, batchSize: 100 },
            kafka: {
              brokers: ["kafka:9092"],
              topic: "events",
              consumerGroup: "cleaner",
            },
          },
        ],
        kafka: {
          status: "available",
          resources: [
            {
              eventSourceId: "source-a",
              status: "available",
              topic: "events",
              consumerGroup: "cleaner",
              partitions: [],
            },
          ],
        },
      },
    });
  });
  await page.route("**/local-api/metrics**", async (route) => {
    await route.fulfill({
      json: {
        from: "2026-08-30T00:00:00Z",
        to: "2026-08-30T01:00:00Z",
        step: 15,
        panels: [],
      },
    });
  });

  await page.goto("/cleaner");
  await page.getByRole("button", { name: /transform/ }).click();
  await expect(
    page.getByRole("dialog", { name: "transform 节点详情" }),
  ).toBeVisible();
  await expect(
    page.getByRole("region", { name: "transform 步骤说明" }),
  ).toContainText("纯计算");
  await expect(page.getByText("当前关键状态与固定历史指标")).toBeVisible();
});

test("shows control-plane task dependencies and an independent backlog failure", async ({
  page,
}) => {
  await page.route("**/local-api/capabilities", async (route) => {
    await route.fulfill({
      json: {
        version: "0.2.0",
        metrics: { configured: true, source: "prometheus" },
        entities: {
          events: { source: "elasticsearch", filters: [] },
          alerts: { source: "elasticsearch", filters: [] },
          "alert-logs": { source: "elasticsearch", filters: [] },
        },
        storage: { elasticsearch: { configured: true } },
        limits: {
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 604800,
          defaultLimit: 50,
          maxLimit: 200,
        },
      },
    });
  });
  await page.route("**/local-api/runtime/control-plane**", async (route) => {
    const now = Date.now() / 1000;
    const ids = [
      "elasticsearch-schema-and-active-reconciler",
      "elasticsearch-bucket-manager",
      "elasticsearch-alert-archiver",
      "redis-stream-manager",
    ];
    await route.fulfill({
      json: {
        status: "partial",
        snapshotAt: new Date().toISOString(),
        tasks: ids.map((id, index) => ({
          id,
          enabled: index < 3,
          dependsOn: index === 1 ? [ids[0]] : index === 2 ? [ids[1]] : [],
          intervalSeconds: [3600, 21600, 30, 60][index],
          configSource: index < 3 ? "default" : "disabled",
          settings:
            index === 2
              ? { archiveBatchSize: 1000, archiveWorkerCount: 4 }
              : index === 1
                ? {
                    eventBucketDays: 7,
                    alertHistoryBucketDays: 7,
                    alertLogBucketDays: 7,
                    precreatePastBuckets: 1,
                    precreateFutureBuckets: 1,
                    maxBucketsPerEntity: 512,
                  }
                : {},
        })),
        processes: {
          status: "available",
          items: [
            {
              instance: "linkd:9464",
              job: "linkd",
              serviceInstanceId: "linkd-1",
              role: "control-plane",
              version: "dev",
              up: true,
            },
          ],
        },
        metrics: {
          status: "available",
          series: {
            active: ids.slice(0, 3).map((id) => ({
              labels: { instance: "linkd:9464", linkd_task: id },
              value: 1,
              timestamp: now,
            })),
            lastSuccess: ids.slice(0, 3).map((id) => ({
              labels: { instance: "linkd:9464", linkd_task: id },
              value: now - 5,
              timestamp: now,
            })),
            runCount: [],
            averageDuration: [],
            p95Duration: [],
            archiveLastScanned: [],
            archiveLastBatch: [],
            archiveLastFailed: [],
            trimRequired: [],
            trimSafe: [],
            trimLastEntries: [],
            oldestPendingAge: [],
          },
        },
        archive: {
          status: "unavailable",
          message: "待归档 Alert 查询失败",
          backlog: null,
        },
        redis: {
          status: "unavailable",
          message: "Redis Stream 管理任务未启用",
          streamExists: null,
          expectedGroupPresent: null,
          entries: null,
          maxEntries: null,
          entriesAboveMax: null,
          pending: null,
          maxLag: null,
        },
      },
    });
  });
  await page.route("**/local-api/metrics**", async (route) => {
    await route.fulfill({
      json: {
        from: "2026-09-02T23:00:00Z",
        to: "2026-09-03T00:00:00Z",
        step: 15,
        panels: [],
      },
    });
  });

  await page.goto("/control-plane");
  await expect(
    page.getByRole("heading", { name: "Control Plane" }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "任务依赖与故障边界" }),
  ).toBeVisible();
  await expect(page.getByText("待归档 Alert 查询失败")).toBeVisible();
  await expect(page.getByText("Repository 默认启用").first()).toBeVisible();
});
