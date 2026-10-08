import { expect, test } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import {
  cleanupFixture,
  cleanupID,
} from "../../src/test-fixtures/suppression-cleanups";
test("cleanup history keeps unavailable distinct from zero without mutations", async ({
  page,
}, info) => {
  const errors: string[] = [],
    writes: string[] = [];
  let row = cleanupFixture();
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/local-api/**", (route) => {
    const r = route.request(),
      u = new URL(r.url());
    if (r.method() !== "GET") writes.push(r.method());
    if (u.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (u.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    expect(u.searchParams.get("bk_tenant_id")).toBe("tenant-a");
    return route.fulfill({
      json: u.pathname.endsWith(cleanupID)
        ? row
        : { bk_tenant_id: "tenant-a", items: [row], next: "" },
    });
  });
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.goto(
    "/suppression-cleanups?bk_tenant_id=tenant-a&id=" + cleanupID,
  );
  await expect(page.getByText("防抖：Redis 不可用，结果未确认")).toBeVisible();
  await page.screenshot({
    path: info.outputPath("cleanup-unconfirmed-dark.png"),
    fullPage: true,
  });
  row = {
    ...row,
    result: {
      clip: { state: "confirmed", removed: 0 },
      aggregation: { state: "not_applicable" },
    },
  };
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(page.getByText("防抖：本轮未发现可清理登记")).toBeVisible();
  await expect(
    page.getByText(
      "此前尝试的清理结果未确认。本轮零删除不代表此前没有副作用。",
    ),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await page.setViewportSize({ width: 850, height: 1050 });
  await page.screenshot({
    path: info.outputPath("cleanup-zero-light.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  row = {
    ...row,
    result: {
      clip: {
        state: "confirmed",
        removed: 1,
        windows: [
          {
            id: "b".repeat(64) + ":" + "c".repeat(64),
            epoch: "first-generation",
          },
        ],
      },
      aggregation: { state: "not_applicable" },
    },
  };
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(
    page.getByText("first-generation", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("cleanup-window-detail-light.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
});
