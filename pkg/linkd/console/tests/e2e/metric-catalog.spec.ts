import { expect, test } from "@playwright/test";
import { metricCatalogFixture } from "../fixtures/metric-catalog";

test("browses catalog definitions, filters and copies a Prometheus query name", async ({
  page,
}, info) => {
  await page.route("**/local-api/capabilities", (route) =>
    route.fulfill({
      json: {
        version: "test",
        metrics: { configured: false, source: "prometheus" },
        entities: {},
        storage: { elasticsearch: { configured: false } },
        limits: {
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 86400,
          defaultLimit: 50,
          maxLimit: 200,
        },
      },
    }),
  );
  await page.route("**/local-api/metrics/catalog", (route) =>
    route.fulfill({ json: metricCatalogFixture }),
  );
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/metrics/catalog");
  await expect(
    page.getByRole("heading", { name: "指标目录", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "告警丰富", exact: true }).click();
  await expect(page).toHaveURL(/module=enrich/);
  await expect(
    page.getByRole("button", { name: "告警丰富", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await page.getByLabel("指标类型").selectOption("histogram");
  await expect(page).toHaveURL(/module=enrich.*type=histogram/);
  await expect(
    page.getByRole("button", { name: "查看存储逻辑操作数详情" }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "查看告警丰富总耗时详情" }).click();
  await page
    .getByRole("button", {
      name: "复制 linkd_enrich_attempt_duration_seconds_bucket",
      exact: true,
    })
    .click();
  await expect(page.getByRole("status")).toHaveText(
    "已复制 linkd_enrich_attempt_duration_seconds_bucket",
  );
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    "linkd_enrich_attempt_duration_seconds_bucket",
  );
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: info.outputPath("metric-catalog-dark.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "切换为浅色模式" }).click();
  await page.screenshot({
    path: info.outputPath("metric-catalog-light.png"),
    fullPage: true,
  });
  await page.getByLabel("搜索指标").fill("不存在");
  await expect(page.getByText(/没有匹配的指标/)).toBeVisible();
  await page.getByRole("button", { name: "清除筛选" }).click();
  await expect(
    page.getByRole("button", { name: "查看存储逻辑操作数详情" }),
  ).toBeVisible();
});
