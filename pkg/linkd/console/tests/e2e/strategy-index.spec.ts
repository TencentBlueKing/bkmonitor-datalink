import { expect, test } from "@playwright/test";

test("queries and reconciles strategy membership on desktop and mobile", async ({
  page,
}, info) => {
  const target = {
    eventSourceId: "host",
    hookName: "active-by-strategy",
    keyPrefix: "alarmd:open_alerts",
    address: "redis.example:6379",
    database: 8,
    sources: ["host", "network"],
  };
  await page.route("**/local-api/capabilities", (route) =>
    route.fulfill({
      json: {
        version: "test",
        metrics: { configured: false, source: "prometheus" },
        entities: {},
        storage: { elasticsearch: { configured: true } },
        limits: {
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 86400,
          defaultLimit: 50,
          maxLimit: 200,
        },
      },
    }),
  );
  await page.route("**/local-api/strategy-index/targets", (route) =>
    route.fulfill({ json: [target] }),
  );
  await page.route("**/local-api/strategy-index/reconcile?*", (route) => {
    const query = new URL(route.request().url()).searchParams;
    expect(query.get("bk_tenant_id")).toBe("system");
    expect(query.get("strategy_id")).toBe("123");
    return route.fulfill({
      json: {
        target,
        tenantId: "system",
        strategyId: "123",
        key: "alarmd:open_alerts:system:123",
        startedAt: "2026-09-22T00:00:00Z",
        finishedAt: "2026-09-22T00:00:01Z",
        complete: true,
        warnings: [
          "两侧读取不是原子快照；告警变更期间可能存在短暂差异，请复查。",
          "控制面按共享来源的活跃告警并集维护成员。",
        ],
        redis: {
          complete: true,
          total: 2,
          scanned: 2,
          projection: {
            lastSuccess: "2026-09-21T23:59:01Z",
            lastAttempt: "2026-09-22T00:00:00Z",
            error: "read_failed",
            discoverySuccess: "2026-09-21T23:59:00Z",
            discoveryError: null,
            pending: true,
          },
        },
        alerts: { complete: true, scanned: 3, matched: 2 },
        rows: [
          {
            fingerprint: "host-10.0.0.1-cpu",
            status: "matched",
            alerts: [
              {
                alertId: "alert-1",
                eventSourceId: "host",
                fingerprint: "host-10.0.0.1-cpu",
              },
            ],
          },
          {
            fingerprint: "network-router-link-down",
            status: "missing_redis",
            alerts: [
              {
                alertId: "alert-2",
                eventSourceId: "network",
                fingerprint: "network-router-link-down",
              },
            ],
          },
          { fingerprint: "old-alert-member", status: "redis_only", alerts: [] },
        ],
      },
    });
  });
  await page.goto("/strategy-index");
  await expect(
    page.getByRole("heading", { name: "策略活跃索引" }),
  ).toBeVisible();
  await page.getByLabel("租户 ID").fill("system");
  await page.getByLabel("策略 ID").fill("123");
  await page.getByRole("button", { name: "查询并对账" }).click();
  await expect(
    page.getByRole("heading", { name: "已读完当前查询范围" }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "host / alert-1" }),
  ).toHaveAttribute("href", "/explore/alerts?bk_tenant_id=system&id=alert-1");
  await expect(page.getByRole("table").getByRole("row")).toHaveCount(4);
  await expect(page.getByText(/控制面投影：刷新失败/)).toBeVisible();
  await expect(page.getByText(/距查询结束 60 秒/)).toBeVisible();
  await page.screenshot({
    path: info.outputPath("strategy-desktop.png"),
    fullPage: true,
  });
  await page.getByLabel("对账结果").selectOption("missing_redis");
  await expect(page.getByRole("table").getByRole("row")).toHaveCount(2);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: info.outputPath("strategy-mobile.png"),
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
