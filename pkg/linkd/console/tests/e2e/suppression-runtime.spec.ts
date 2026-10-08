import { expect, test } from "@playwright/test";
import {
  clipID,
  aggregationID,
  runtimeClip,
  runtimeAggregation,
  runtimeSuppressionMembers,
  suppressionTenant,
} from "../../src/test-fixtures/suppression-runtime";
import { explorerCapabilities } from "../fixtures/explorer";

test("suppression runtime reads live snapshots with scoped pagination and no writes", async ({
  page,
}, info) => {
  const errors: string[] = [],
    writes: string[] = [],
    queries: string[] = [];
  let zero = false;
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/local-api/**", async (route) => {
    const request = route.request(),
      u = new URL(request.url());
    if (request.method() !== "GET") writes.push(request.method() + u.pathname);
    if (u.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (u.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    queries.push(u.pathname + u.search);
    expect(u.searchParams.get("bk_tenant_id")).toBe(suppressionTenant);
    const v = u.pathname.includes("/aggregation")
      ? runtimeAggregation()
      : runtimeClip(zero ? 0 : 2);
    if (u.pathname.endsWith("/requests"))
      return route.fulfill({
        json: {
          bk_tenant_id: v.bk_tenant_id,
          kind: v.kind,
          window_id: v.id,
          items: [],
          next: "",
        },
      });
    if (u.pathname.endsWith("/members"))
      return route.fulfill({
        json: runtimeSuppressionMembers(
          v,
          u.searchParams.get("after") ? "" : "member-next",
        ),
      });
    if (u.pathname.endsWith("/clip") || u.pathname.endsWith("/aggregation"))
      return route.fulfill({
        json: {
          bk_tenant_id: suppressionTenant,
          kind: v.kind,
          items: u.searchParams.get("after") ? [] : [v],
          next: u.searchParams.get("after") ? "" : "list-next",
        },
      });
    return route.fulfill({ json: v });
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto(
    "/suppression-runtime?bk_tenant_id=tenant-a&kind=clip&id=" + clipID,
  );
  await expect(
    page.getByRole("heading", { name: "防抖详情", exact: true }),
  ).toBeVisible();
  await expect(
    page
      .getByRole("table", { name: "当前抑制窗口" })
      .getByText("2 / 3 次", { exact: true }),
  ).toBeVisible();
  const members = page.getByRole("region", { name: "当前保留成员" });
  await expect(
    members.getByRole("link", { name: "opening-event" }),
  ).toHaveAttribute("href", /detail_tenant=tenant-a/);
  await page.getByRole("button", { name: "下一页", exact: true }).click();
  await expect(page.getByText("本页没有符合条件的窗口。")).toBeVisible();
  await page.getByRole("button", { name: "下一页成员", exact: true }).click();
  await expect(members.getByRole("button", { name: "成员首页" })).toBeEnabled();
  zero = true;
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(
    page
      .getByRole("table", { name: "当前抑制窗口" })
      .getByText("0 / 3 次", { exact: true }),
  ).toBeVisible();
  await expect(
    members.getByRole("button", { name: "成员首页" }),
  ).toBeDisabled();
  await page.screenshot({
    path: info.outputPath("suppression-clip-dark.png"),
    fullPage: true,
  });
  await page.goto(
    "/suppression-runtime?bk_tenant_id=tenant-a&kind=aggregation&id=" +
      aggregationID,
  );
  await expect(
    page.getByRole("heading", { name: "关联聚合详情", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "main-alert" }).first(),
  ).toHaveAttribute("href", /detail_tenant=tenant-a/);
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await page.setViewportSize({ width: 850, height: 1100 });
  await page.screenshot({
    path: info.outputPath("suppression-aggregation-light.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  expect(queries.some((q) => q.includes("after=member-next"))).toBe(true);
  expect(errors).toEqual([]);
  expect(writes).toEqual([]);
});
