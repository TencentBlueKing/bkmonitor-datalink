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
  let audit: unknown = null;
  await page.route("**/local-api/strategy-index/audits", (route) => {
    if (route.request().method() === "POST") {
      expect(route.request().postDataJSON()).toEqual({
        event_source_id: "host",
        hook_name: "active-by-strategy",
      });
      audit = {
        id: "audit-test",
        target,
        status: "completed",
        phase: "done",
        startedAt: "2026-09-22T00:00:00Z",
        finishedAt: "2026-09-22T00:00:01Z",
        scannedAlerts: 1000,
        skippedAlerts: 0,
        invalidAlerts: 0,
        checkedStrategies: 30,
        discoveredStrategies: 30,
        incompleteStrategies: 0,
        redisMembers: 1000,
        matched: 999,
        missing: 1,
        extra: 1,
        samplesTruncated: false,
        warnings: ["双向扫描不是同一时刻的事务快照。"],
        differences: [
          {
            tenantId: "system",
            strategyId: "123",
            fingerprint: "missing-fp",
            status: "missing_redis",
          },
          {
            tenantId: "other",
            strategyId: "456",
            fingerprint: "extra-fp",
            status: "redis_only",
          },
        ],
      };
    }
    return route.fulfill({ json: audit });
  });
  const visitedCursors: string[] = [];
  await page.route("**/local-api/strategy-index/browse?*", (route) => {
    const query = new URL(route.request().url()).searchParams;
    const second = query.get("cursor") === "next-batch";
    visitedCursors.push(query.get("cursor") ?? "start");
    expect(query.get("count")).toBe("50");
    return route.fulfill({
      json: {
        target,
        scannedAt: "2026-09-22T00:00:01Z",
        nextCursor: second ? null : "next-batch",
        phase: second ? "pending" : "sets",
        warnings: ["列表按扫描批次展示，并发变更可能导致跨批重复。"],
        health: {
          lastSuccess: "2026-09-22T00:00:00Z",
          lastAttempt: "2026-09-22T00:00:00Z",
          error: null,
          pendingCount: 1,
          oldestDueAt: "2026-09-22T00:00:00Z",
        },
        rows: second
          ? [
              {
                tenantId: "tenant-b",
                strategyId: "789",
                key: "alarmd:open_alerts:tenant-b:789",
                members: 0,
                pending: true,
                lastSuccess: null,
                lastAttempt: null,
                error: null,
              },
            ]
          : [
              {
                tenantId: "system",
                strategyId: "123",
                key: "alarmd:open_alerts:system:123",
                members: 2,
                pending: false,
                lastSuccess: "2026-09-21T23:59:01Z",
                lastAttempt: "2026-09-22T00:00:00Z",
                error: null,
              },
              {
                tenantId: "another",
                strategyId: "456",
                key: "alarmd:open_alerts:another:456",
                members: 20,
                pending: false,
                lastSuccess: null,
                lastAttempt: null,
                error: null,
              },
            ],
      },
    });
  });
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
  await expect(
    page.getByRole("heading", { name: "租户与策略组合" }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "缓存维护任务" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "对账租户 system 策略 123" }),
  ).toBeVisible();
  await expect(page.getByLabel("租户 ID", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "开始整体对账" }).click();
  await expect(
    page.getByRole("heading", { name: "发现不一致", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("missing-fp", { exact: true })).toBeVisible();
  await expect(page.getByText("extra-fp", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "下一批", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "对账租户 tenant-b 策略 789" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "下一批", exact: true }),
  ).toBeDisabled();
  expect(visitedCursors).toContain("next-batch");
  await page.getByRole("button", { name: "上一批", exact: true }).click();
  await page.getByRole("button", { name: "对账租户 system 策略 123" }).click();
  await expect(
    page.getByRole("heading", { name: "已读完当前查询范围" }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "host / alert-1" }),
  ).toHaveAttribute("href", "/explore/alerts?bk_tenant_id=system&id=alert-1");
  await expect(
    page
      .getByLabel("索引成员对账", { exact: true })
      .getByRole("table")
      .getByRole("row"),
  ).toHaveCount(4);
  await expect(page.getByText(/控制面投影：刷新失败/)).toBeVisible();
  await expect(page.getByText(/距查询结束 60 秒/)).toBeVisible();
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
  await page.screenshot({
    path: info.outputPath("strategy-desktop.png"),
    fullPage: true,
  });
  await page.getByLabel("对账结果").selectOption("missing_redis");
  await expect(
    page
      .getByLabel("索引成员对账", { exact: true })
      .getByRole("table")
      .getByRole("row"),
  ).toHaveCount(2);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
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
