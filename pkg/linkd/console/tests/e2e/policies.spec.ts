import { expect, test } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import {
  policyRecord,
  policyRelease,
  policyPreview,
} from "../../src/test-fixtures/policies";
import type { PolicyKind } from "../../src/shared/policies";

test.use({ timezoneId: "Asia/Shanghai" });

test("tenant policy inspection, immutable version comparison and read-only matching", async ({
  page,
}, info) => {
  const errors: string[] = [];
  const previews: Record<string, unknown>[] = [];
  const writes: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/local-api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() !== "GET") writes.push(url.pathname);
    if (url.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (url.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    if (url.pathname === "/local-api/policy-links")
      return route.fulfill({
        json: {
          bk_tenant_id: "tenant-a",
          type: url.searchParams.get("type"),
          url: null,
          reason: "not_configured",
        },
      });
    if (url.pathname.endsWith("/preview")) {
      const body = request.postDataJSON() as Record<string, unknown>;
      previews.push(body);
      const result = policyPreview(Number(body.version ?? 2));
      if (body.spec) {
        result.id = "preview";
        result.version = 0;
      }
      return route.fulfill({ json: result });
    }
    if (url.pathname === "/local-api/policies") {
      expect(url.searchParams.get("bk_tenant_id")).toBe("tenant-a");
      const kind = url.searchParams.get("type") as PolicyKind;
      return route.fulfill({ json: { items: [policyRecord(kind)], next: "" } });
    }
    const kind = url.pathname.split("/")[3] as PolicyKind;
    const version = /releases\/(\d+)/.exec(url.pathname);
    return route.fulfill({
      json: version
        ? policyRelease(Number(version[1]), kind)
        : policyRecord(kind),
    });
  });
  await page.setViewportSize({ width: 1512, height: 1100 });
  await page.goto(
    "/policies?bk_tenant_id=tenant-a&type=suppression&id=db-noise",
  );
  await expect(
    page.getByRole("heading", { name: "告警策略", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "发布 v2", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("待完成", { exact: true })).toBeVisible();
  await page.screenshot({
    path: info.outputPath("policies-dark.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "对比配置" }).click();
  await expect(page.getByText("变化字段：scheme")).toBeVisible();
  await page.getByLabel("预览判定时间").fill("2026-10-05T08:02");
  await page.getByRole("button", { name: "本地时间", exact: true }).click();
  await expect(page.getByLabel("预览判定时间")).toHaveValue("2026-10-05T00:02");
  await page.getByLabel("预览输入 ID").fill("event-001");
  await page.getByRole("button", { name: "执行只读匹配" }).click();
  const result = page.getByRole("region", { name: "匹配结果" });
  await expect(
    result.getByRole("heading", { name: /critical.*无法判定/ }),
  ).toBeVisible();
  await expect(result.getByText("field_unavailable")).toBeVisible();
  expect(previews[0]).toMatchObject({
    bk_tenant_id: "tenant-a",
    type: "suppression",
    id: "db-noise",
    version: 2,
    event_id: "event-001",
    at: "2026-10-05T00:02:00.000Z",
  });
  await page.setViewportSize({ width: 1512, height: 1500 });
  await page
    .getByRole("region", { name: "策略匹配预览" })
    .screenshot({ path: info.outputPath("policy-preview-dark.png") });
  await page.getByLabel("查看版本").fill("1");
  await page.getByRole("button", { name: "读取版本" }).click();
  await expect(
    page.getByRole("heading", { name: "发布 v1", exact: true }),
  ).toBeVisible();
  await expect(result).toHaveCount(0);
  await page.getByRole("button", { name: "切换为浅色模式" }).click();
  await page.setViewportSize({ width: 850, height: 1100 });
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(850);
  await page.screenshot({
    path: info.outputPath("policies-light-compact.png"),
    fullPage: true,
  });
  await page.getByLabel("策略类型").selectOption("shield");
  await page.getByRole("button", { name: "查询策略" }).click();
  await page.getByRole("button", { name: /数据库告警降噪/ }).click();
  await expect(
    page.getByRole("heading", { name: "发布 v2", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("checkbox", { name: "匹配被屏蔽条件（rely_policy）" })
    .check();
  await page.getByLabel("依赖主 Alert ID").fill("main-alert");
  await page.getByLabel("预览输入 ID").fill("child-event");
  await page.getByRole("button", { name: "执行只读匹配" }).click();
  await expect(result).toBeVisible();
  expect(previews[1]).toMatchObject({
    type: "shield",
    rely: true,
    origin_alert_id: "main-alert",
    event_id: "child-event",
  });
  expect(writes).toEqual([
    "/local-api/policies/preview",
    "/local-api/policies/preview",
  ]);
  expect(errors).toEqual([]);
});
