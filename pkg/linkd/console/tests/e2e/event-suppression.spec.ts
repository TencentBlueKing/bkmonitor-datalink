import { expect, test } from "@playwright/test";
import { suppressionFixture } from "../../src/test-fixtures/suppression";
import {
  eventFixture,
  explorerCapabilities,
  explorerStats,
} from "../fixtures/explorer";

test("Event suppression history is readable and refresh never writes counters", async ({
  page,
}, info) => {
  const errors: string[] = [],
    writes: string[] = [];
  let bypass = false;
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/local-api/**", async (route) => {
    const request = route.request(),
      url = new URL(request.url());
    if (request.method() !== "GET") writes.push(url.pathname);
    if (url.pathname.endsWith("version"))
      return route.fulfill({ json: { version: "test" } });
    if (url.pathname.endsWith("capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    if (url.pathname.endsWith("/stats"))
      return route.fulfill({ json: explorerStats("events") });
    const item = {
      ...eventFixture,
      payload: {
        ...eventFixture.payload,
        _processing: {
          state: "suppressed",
          policy_decision: {
            suppression: bypass
              ? {
                  bypass_reason: "active_alert",
                  active_alert_id: "active-alert",
                }
              : suppressionFixture,
          },
        },
      },
    };
    if (url.pathname.startsWith("/local-api/events/"))
      return route.fulfill({ json: item });
    return route.fulfill({
      json: { source: "elasticsearch", items: [item], warnings: [] },
    });
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto("/explore/events?bk_tenant_id=tenant-a");
  await page
    .getByRole("button", { name: eventFixture.payload.title as string })
    .click();
  const panel = page.getByRole("region", { name: "事件抑制记录" });
  await expect(panel.getByText(/2 \/ 3 次/)).toBeVisible();
  await expect(panel.getByText("固定截止：", { exact: false })).toBeVisible();
  await expect(
    panel.getByRole("link", { name: "clip-policy / 2 ↗" }),
  ).toHaveAttribute("href", /bk_tenant_id=tenant-a.*version=2/);
  await panel.screenshot({
    path: info.outputPath("event-suppression-dark.png"),
  });
  await page.setViewportSize({ width: 780, height: 1000 });
  await panel.screenshot({
    path: info.outputPath("event-suppression-compact.png"),
  });
  bypass = true;
  await page.getByRole("button", { name: "刷新详情" }).click();
  await expect(panel.getByText(/绕过防抖和关联聚合/)).toBeVisible();
  await expect(panel.getByRole("table")).toHaveCount(0);
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
});
