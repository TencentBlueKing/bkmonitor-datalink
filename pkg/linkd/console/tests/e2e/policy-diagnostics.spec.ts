import { expect, test } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import { policyRecord, policyRelease } from "../../src/test-fixtures/policies";
import {
  projectionFixture,
  projectionID,
  projectionTenant,
} from "../../src/test-fixtures/projection-tasks";

test("simulation timeline, scoped statistics and optional KAC navigation", async ({
  page,
}, info) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  let configured = false;
  const writes: string[] = [];
  await page.route("**/local-api/**", async (route) => {
    const req = route.request(),
      url = new URL(req.url());
    if (req.method() !== "GET") writes.push(url.pathname);
    if (url.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (url.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    if (url.pathname === "/local-api/policy-links")
      return route.fulfill({
        json: {
          bk_tenant_id: "tenant-a",
          type: "suppression",
          url: null,
          reason: "not_configured",
        },
      });
    if (url.pathname === "/local-api/kac-alert-link")
      return route.fulfill({
        json: {
          bk_tenant_id: projectionTenant,
          alarm_id: projectionFixture().alarm_id,
          url: configured
            ? "https://kac.example/#/kac/alarmDetail?id=" +
              projectionFixture().alarm_id
            : null,
        },
      });
    if (url.pathname === "/local-api/policies/statistics")
      return route.fulfill({
        json: {
          bk_tenant_id: "tenant-a",
          type: "suppression",
          hours: Number(url.searchParams.get("hours")),
          mode: "execution_observations",
          from: "2026-10-07T01:00:00Z",
          to: "2026-10-08T00:00:00Z",
          items: [
            {
              id: "db-noise",
              matched: 12,
              not_matched: 4,
              unavailable: 1,
              execution_skipped: 2,
            },
          ],
        },
      });
    if (url.pathname === "/local-api/policies/simulate") {
      const q = req.postDataJSON() as {
        steps: Array<{ at: string; event_id?: string }>;
      };
      return route.fulfill({
        json: {
          bk_tenant_id: "tenant-a",
          type: "suppression",
          id: "db-noise",
          version: 2,
          compiled: policyRelease().compiled,
          mode: "state_simulation",
          steps: q.steps.map((s) => ({
            ...s,
            replayed: false,
            outcome: s.event_id ? "alert_suppressed" : "time_advanced",
            decision: s.event_id
              ? {
                  suppression: {
                    evaluations: [
                      {
                        severity: "warning",
                        suppressed: true,
                        steps: [
                          {
                            scheme: "clip",
                            outcome: "suppressed",
                            count: 1,
                            threshold: 3,
                          },
                        ],
                      },
                    ],
                  },
                }
              : undefined,
            alerts: [],
            windows: [],
          })),
        },
      });
    }
    if (url.pathname.startsWith("/local-api/projection-tasks"))
      return route.fulfill({
        json: url.pathname.includes(projectionID)
          ? projectionFixture("delivered")
          : {
              bk_tenant_id: projectionTenant,
              items: [projectionFixture("delivered")],
              next: "",
            },
      });
    if (url.pathname === "/local-api/policies")
      return route.fulfill({ json: { items: [policyRecord()], next: "" } });
    if (url.pathname.includes("/releases/"))
      return route.fulfill({ json: policyRelease() });
    if (url.pathname.endsWith("/db-noise"))
      return route.fulfill({ json: policyRecord() });
    return route.fulfill({
      status: 503,
      json: { error: { message: "not configured" } },
    });
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(
    "/policies?bk_tenant_id=tenant-a&type=suppression&id=db-noise",
  );
  await expect(
    page.getByRole("heading", { name: "本页策略执行观察" }),
  ).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "12", exact: true }),
  ).toBeVisible();
  await page.getByLabel("模拟事件 ID").fill("event-a");
  await page.getByLabel("模拟判定时间").fill("2026-10-08T00:00:00Z");
  await page.getByRole("button", { name: "添加事件步骤" }).click();
  await page.getByRole("button", { name: "推进虚拟时间" }).click();
  await page.getByRole("button", { name: "运行隔离模拟" }).click();
  await expect(page.getByLabel("模拟轨迹")).toBeVisible();
  await page.getByText("计数、策略诊断和状态快照").first().click();
  await page
    .getByRole("region", { name: "策略状态模拟" })
    .screenshot({ path: info.outputPath("simulation-dark.png") });
  await page.setViewportSize({ width: 850, height: 1050 });
  await page.getByRole("button", { name: "切换为浅色模式" }).click();
  await page.screenshot({
    path: info.outputPath("policy-diagnostics-light.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.goto(
    `/projection-tasks?bk_tenant_id=${projectionTenant}&id=${projectionID}`,
  );
  await expect(
    page.getByRole("link", { name: "查看当前 Alert" }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: /查看 KAC/ })).toHaveCount(0);
  configured = true;
  await page.reload();
  const link = page.getByRole("link", { name: /查看 KAC/ });
  await expect(link).toHaveAttribute("href", /https:\/\/kac\.example/);
  await expect(link).toHaveAttribute("rel", "noopener noreferrer");
  expect(writes).toEqual(["/local-api/policies/simulate"]);
  expect(errors).toEqual([]);
});
