import { expect, test } from "@playwright/test";
import { controlPlaneFixture } from "../../src/test-fixtures/control-plane";

test("control plane exposes all tasks, details, unified refresh and themes", async ({
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
  const data = controlPlaneFixture();
  let reads = 0,
    dynamicReads = 0,
    fail = false;
  await page.route("**/local-api/runtime/control-plane?*", (route) => {
    reads++;
    return route.fulfill({
      status: fail ? 502 : 200,
      json: fail ? { error: { message: "控制面暂不可用" } } : data,
    });
  });
  await page.route("**/local-api/dynamic-config", (route) => {
    dynamicReads++;
    return route.fulfill({
      json: {
        config: { enabled: false, origin: "yaml", sync_state: "disabled" },
        workers: {},
      },
    });
  });
  await page.goto("/control-plane");
  await expect(
    page.getByRole("heading", { name: "Control Plane", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".cp-task-row")).toHaveCount(8);
  await page.getByRole("button", { name: /Redis Stream Manager/ }).click();
  await expect(
    page.getByRole("heading", { name: "Redis Stream Manager", exact: true }),
  ).toBeVisible();
  await page.getByRole("tab", { name: "生效配置" }).click();
  await expect(page.getByRole("button", { name: "复制 JSON" })).toBeVisible();
  await page.getByRole("tab", { name: "执行情况" }).click();
  await page.getByLabel("历史时间范围").selectOption("900");
  await expect.poll(() => reads).toBeGreaterThanOrEqual(2);
  await page.getByRole("tab", { name: "运行详情" }).click();
  await page.screenshot({
    path: info.outputPath("control-plane-dark.png"),
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await page.screenshot({
    path: info.outputPath("control-plane-light.png"),
    fullPage: true,
  });
  const oldDynamic = dynamicReads;
  fail = true;
  await page.getByRole("button", { name: "立即刷新", exact: true }).click();
  await expect.poll(() => dynamicReads).toBeGreaterThan(oldDynamic);
  await expect(page.getByRole("alert")).toContainText("旧快照");
  await expect(page.locator(".cp-task-row")).toHaveCount(8);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: info.outputPath("control-plane-mobile.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
});
