import { expect, test } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import {
  actionFixture,
  actionID,
  actionTenant,
  actionOrderFixture,
} from "../../src/test-fixtures/action-deliveries";
import { actionMetricsFixture } from "../../src/test-fixtures/action-metrics";

test("action runtime charts keep process scope, missing-data meaning and safe log navigation", async ({
  page,
}, info) => {
  const calls: URL[] = [],
    writes: string[] = [],
    errors: string[] = [];
  let empty = false;
  const row = actionFixture("succeeded");
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/local-api/**", async (route) => {
    const r = route.request(),
      u = new URL(r.url());
    if (r.method() !== "GET") writes.push(r.method());
    if (u.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (u.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    if (u.pathname.endsWith("/action-metrics")) {
      calls.push(u);
      expect(u.searchParams.has("bk_tenant_id")).toBe(false);
      expect(u.searchParams.has("alert_id")).toBe(false);
      return route.fulfill({
        json: actionMetricsFixture(u.searchParams, empty),
      });
    }
    expect(u.searchParams.get("bk_tenant_id")).toBe(actionTenant);
    if (u.pathname.endsWith("/order"))
      return route.fulfill({ json: actionOrderFixture(row) });
    return route.fulfill({
      json: u.pathname.endsWith(actionID)
        ? row
        : { bk_tenant_id: actionTenant, items: [row], next: "" },
    });
  });
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.goto(
    `/action-deliveries?bk_tenant_id=${actionTenant}&id=${actionID}`,
  );
  await expect(
    page.getByRole("heading", { name: "动作受理确认" }),
  ).toBeVisible();
  expect(calls).toHaveLength(0);
  await page.getByRole("button", { name: "查看动作运行观测" }).click();
  await expect(
    page.getByRole("heading", { name: "最近页观察距今", exact: true }),
  ).toBeVisible();
  await expect(
    page.locator(".delivery-metrics .panel-state.available"),
  ).toHaveCount(8);
  await expect(page.locator(".delivery-metrics canvas")).toHaveCount(8);
  await page.getByLabel("进程 instance").fill("worker-b");
  await page.getByRole("button", { name: "应用进程筛选" }).click();
  await expect
    .poll(() => calls.at(-1)?.searchParams.get("instance"))
    .toBe("worker-b");
  await page.getByLabel("指标计算窗口").selectOption("300");
  await expect
    .poll(() => calls.at(-1)?.searchParams.get("calculation_window_seconds"))
    .toBe("300");
  await page.getByRole("button", { name: "15m", exact: true }).click();
  await expect
    .poll(
      () =>
        Date.parse(calls.at(-1)!.searchParams.get("to")!) -
        Date.parse(calls.at(-1)!.searchParams.get("from")!),
    )
    .toBe(900000);
  await page.screenshot({
    path: info.outputPath("action-metrics-dark.png"),
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await page.setViewportSize({ width: 850, height: 1050 });
  await page.screenshot({
    path: info.outputPath("action-metrics-light.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  empty = true;
  await page.getByRole("button", { name: "刷新动作指标" }).click();
  await expect(
    page.locator(".delivery-metrics .panel-state.unavailable"),
  ).toHaveCount(8);
  await expect(
    page.getByText("查询范围内没有动作运行时序；不能据此判定任务已完成", {
      exact: true,
    }),
  ).toHaveCount(8);
  await page.getByRole("button", { name: "收起动作运行观测" }).click();
  await expect(page.getByRole("region", { name: "动作运行观测" })).toHaveCount(
    0,
  );
  await expect(page.getByLabel("发送日志定位字段")).toContainText(
    `"action_task_id": "${actionID}"`,
  );
  const log = page.getByRole("link", {
    name: "查看 Alert 操作流水（任务更新时间前 1 小时）",
  });
  await expect(log).toHaveAttribute(
    "href",
    /bk_tenant_id=tenant-a&alert_id=opening-alert/,
  );
  await page
    .getByRole("region", { name: "动作日志定位" })
    .screenshot({ path: info.outputPath("action-log-locator-light.png") });
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
});
