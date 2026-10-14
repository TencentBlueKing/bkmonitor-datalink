import { expect, test } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import { actionMetricsFixture } from "../../src/test-fixtures/action-metrics";
import { projectionMetricsFixture } from "../../src/test-fixtures/projection-metrics";
test("KAC output charts preserve process scope and missing-data state", async ({
  page,
}) => {
  let empty = false;
  const calls: URL[] = [];
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/local-api/**", async (route) => {
    const u = new URL(route.request().url());
    expect(route.request().method()).toBe("GET");
    if (u.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (u.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    calls.push(u);
    expect(u.searchParams.has("bk_tenant_id")).toBe(false);
    return route.fulfill({
      json: (u.pathname.endsWith("/projection-metrics")
        ? projectionMetricsFixture
        : actionMetricsFixture)(u.searchParams, empty),
    });
  });
  await page.goto("/kac-output");
  await expect(
    page.getByRole("heading", { name: "KAC 输出", exact: true }),
  ).toBeVisible();
  await expect(
    page.locator(".delivery-metrics .panel-state.available"),
  ).toHaveCount(8);
  const projection = page.getByRole("region", { name: "投影运行观测" });
  await projection.getByLabel("进程 instance").fill("control-b");
  await projection.getByRole("button", { name: "应用进程筛选" }).click();
  await expect
    .poll(() => calls.at(-1)?.searchParams.get("instance"))
    .toBe("control-b");
  empty = true;
  await projection.getByRole("button", { name: "刷新投影指标" }).click();
  await expect(projection.locator(".panel-state.unavailable")).toHaveCount(4);
  expect(errors).toEqual([]);
});
