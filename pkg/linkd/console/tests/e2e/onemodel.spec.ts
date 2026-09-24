import { expect, test } from "@playwright/test";

test("queries pages and relationships and releases the snapshot", async ({
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
  const calls: Array<{ operation: string; body: Record<string, unknown> }> = [];
  await page.route("**/local-api/onemodel/*", (route) => {
    const operation = route.request().url().split("/").at(-1)!;
    const body = route.request().postDataJSON() as Record<string, unknown>;
    calls.push({ operation, body });
    const id = body.cursor ? "102" : "101";
    return route.fulfill({
      json:
        operation === "close"
          ? { closed: true }
          : {
              items: [
                {
                  bk_tenant_id: "system",
                  model_id: "cw-Host",
                  model_inst_id: id,
                  entity_uid: `cw-Host|${id}`,
                  display_name: "production-host",
                  attributes: { bk_host_innerip: "10.0.0.1", owner: "ops" },
                },
              ],
              next_cursor: operation === "search" ? "next-page" : undefined,
              elapsed_milliseconds: 8,
            },
    });
  });
  await page.goto("/onemodel?bk_tenant_id=system&model_id=cw-Host");
  await page.getByRole("button", { name: "执行查询" }).click();
  await expect(
    page.getByText("production-host", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "下一页" }).click();
  await expect(
    page.getByRole("button", { name: "102", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "102", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "实例详情 · 102" }),
  ).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: info.outputPath("onemodel-desktop.png"),
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await page.screenshot({
    path: info.outputPath("onemodel-light.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "查询关联" }).click();
  await expect
    .poll(() => calls.some((call) => call.operation === "close"))
    .toBe(true);
  await expect(page.getByLabel("起点实例 ID", { exact: true })).toHaveValue(
    "102",
  );
  await page.getByLabel("目标模型", { exact: true }).fill("cw-Biz");
  await page.getByLabel("关系", { exact: true }).fill("belongs");
  await page.getByLabel("方向", { exact: true }).selectOption("in");
  await page.getByRole("button", { name: "执行查询" }).click();
  await expect
    .poll(() => calls.some((call) => call.operation === "related"))
    .toBe(true);
  await expect(page.getByRole("button", { name: "下一页" })).toBeDisabled();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: info.outputPath("onemodel-mobile.png"),
    fullPage: true,
  });
  expect(
    calls.find((call) => call.operation === "related")?.body,
  ).toMatchObject({
    bk_tenant_id: "system",
    direction: "in",
    roots: [{ model_id: "cw-Host", model_inst_id: "102" }],
  });
});
