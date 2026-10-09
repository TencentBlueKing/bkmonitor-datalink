import { expect, test } from "@playwright/test";
import {
  explorerCapabilities,
  explorerStats,
  kacAlarmFixture,
} from "../fixtures/explorer";

test("KAC document query, raw details and read-only controls", async ({
  page,
}, info) => {
  const calls: URL[] = [];
  await page.route("**/local-api/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    if (url.pathname.includes("kac-alarms")) {
      calls.push(url);
      expect(url.searchParams.get("bk_tenant_id")).toBe("tenant-a");
      if (url.pathname.endsWith("stats"))
        return route.fulfill({ json: explorerStats("kac-alarms") });
      if (url.pathname.endsWith("legacy-alarm-001"))
        return route.fulfill({ json: kacAlarmFixture });
      return route.fulfill({
        json: {
          source: "elasticsearch",
          items: [kacAlarmFixture],
          warnings: [],
        },
      });
    }
    return route.fulfill({ json: {} });
  });
  await page.setViewportSize({ width: 1512, height: 1100 });
  await page.goto("/explore/kac-alarms");
  await expect(page.getByText("请填写租户并执行查询。")).toBeVisible();
  expect(calls).toHaveLength(0);
  await page.getByLabel("租户", { exact: true }).fill("tenant-a");
  await page.getByLabel("KAC 级别").fill("critical");
  await page.getByRole("button", { name: "执行查询" }).click();
  const title = page.getByRole("button", { name: "KAC CPU 告警", exact: true });
  await expect(title).toBeVisible();
  await page.screenshot({
    path: info.outputPath("kac-alarms-dark.png"),
    fullPage: true,
  });
  await title.click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByText("notify_status", { exact: true }),
  ).toBeVisible();
  await expect(
    dialog.getByRole("button", { name: "主动关闭", exact: true }),
  ).toHaveCount(0);
  await dialog.getByRole("tab", { name: "字段与扩展数据" }).click();
  await expect(
    dialog.getByRole("heading", { name: "field_extra_info", exact: true }),
  ).toBeVisible();
  await dialog.getByRole("tab", { name: "完整 JSON" }).click();
  await expect(dialog.getByText(/2026-10-09 09:00:00/)).toBeVisible();
  await page.screenshot({
    path: info.outputPath("kac-alarm-detail.png"),
    fullPage: true,
  });
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "切换为浅色模式" }).click();
  await page.screenshot({
    path: info.outputPath("kac-alarms-light.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 1100, height: 1050 });
  await page.screenshot({
    path: info.outputPath("kac-alarms-compact.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  expect(
    calls
      .filter((url) => !url.pathname.endsWith("legacy-alarm-001"))
      .every((url) => url.searchParams.get("level") === "critical"),
  ).toBe(true);
});

test("KAC connection missing is explicit without querying Linkd", async ({
  page,
}) => {
  const capabilities = structuredClone(explorerCapabilities);
  delete capabilities.entities["kac-alarms"];
  const queries: string[] = [];
  await page.route("**/local-api/**", (route) => {
    if (route.request().url().endsWith("capabilities"))
      return route.fulfill({ json: capabilities });
    queries.push(route.request().url());
    return route.fulfill({ json: {} });
  });
  await page.goto("/explore/kac-alarms?bk_tenant_id=tenant-a");
  await expect(page.getByText("请先配置 KAC 查询连接。")).toBeVisible();
  expect(queries).toHaveLength(0);
});
