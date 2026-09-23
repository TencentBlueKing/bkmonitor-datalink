import { expect, test } from "@playwright/test";
import type { EntityKind } from "../../src/shared/contracts";
import {
  alertFixture,
  eventFixture,
  explorerCapabilities,
  explorerStats,
  logFixture,
} from "../fixtures/explorer";

test("entity investigation, embedded relations, manual closure and light theme", async ({
  page,
}, info) => {
  let closed = false;
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/local-api/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("version"))
      return route.fulfill({ json: { version: "test" } });
    if (url.pathname.endsWith("capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    const kind = url.pathname.split("/")[2] as EntityKind;
    const alert = closed
      ? {
          ...alertFixture,
          payload: {
            ...alertFixture.payload,
            status: "closed",
            end_at: alertFixture.timestamp,
            end_type: "user",
            end_reason: "现场排查完成",
          },
        }
      : alertFixture;
    if (url.pathname.endsWith("/close")) {
      expect(route.request().postDataJSON()).toMatchObject({
        bk_tenant_id: "tenant-a",
        reason: "现场排查完成",
      });
      closed = true;
      return route.fulfill({
        json: {
          alert: { ...alert.payload, status: "closed" },
          already_closed: false,
        },
      });
    }
    if (url.pathname.endsWith("/stats"))
      return route.fulfill({ json: explorerStats(kind) });
    const item =
      kind === "alerts" ? alert : kind === "events" ? eventFixture : logFixture;
    if (url.pathname.split("/").length > 3)
      return route.fulfill({ json: item });
    return route.fulfill({
      json: { source: "elasticsearch", items: [item], warnings: [] },
    });
  });
  await page.setViewportSize({ width: 1512, height: 1100 });
  await page.goto("/explore/alerts?bk_tenant_id=tenant-a");
  await expect(page.getByText("创建", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("button", { name: alertFixture.payload.title as string }),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("alerts-dark.png"),
    fullPage: true,
  });
  await page
    .getByRole("button", { name: alertFixture.payload.title as string })
    .click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog
      .getByRole("region", { name: "关联 Event 列表" })
      .getByRole("button", { name: "CPU 持续异常采样" }),
  ).toBeVisible();
  await expect(
    dialog
      .getByRole("region", { name: "关联 AlertLog 时间线" })
      .getByText("threshold_exceeded"),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("alert-detail.png"),
    fullPage: true,
  });
  await dialog
    .getByRole("region", { name: "告警关联记录", exact: true })
    .screenshot({ path: info.outputPath("alert-related-records.png") });
  await dialog.getByRole("button", { name: "主动关闭", exact: true }).click();
  await dialog.getByLabel("关闭原因").fill("现场排查完成");
  await dialog.getByRole("button", { name: "确认关闭告警" }).click();
  await expect(dialog.getByRole("status")).toContainText("告警已关闭");
  await dialog.getByRole("button", { name: "关闭详情" }).click();
  await page.getByRole("button", { name: "切换为浅色模式" }).click();
  await page.screenshot({
    path: info.outputPath("alerts-light.png"),
    fullPage: true,
  });
  await page.goto("/explore/events?bk_tenant_id=tenant-a");
  await page.getByRole("button", { name: "CPU 持续异常采样" }).click();
  await expect(
    page.getByRole("heading", { name: "Lifecycle 逐级处理结果" }),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("event-detail.png"),
    fullPage: true,
  });
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.goto("/explore/alert-logs?bk_tenant_id=tenant-a");
  await expect(page.getByText("threshold_exceeded")).toBeVisible();
  await page.screenshot({
    path: info.outputPath("alert-logs.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "表格", exact: true }).click();
  await expect(
    page.getByRole("columnheader", { name: "操作 / 流水 ID" }),
  ).toBeVisible();
  await page.setViewportSize({ width: 850, height: 1050 });
  await page.screenshot({
    path: info.outputPath("alert-logs-compact.png"),
    fullPage: true,
  });
  expect(errors).toEqual([]);
});
