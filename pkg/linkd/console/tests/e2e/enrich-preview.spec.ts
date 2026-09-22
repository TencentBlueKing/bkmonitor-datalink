import { expect, test } from "@playwright/test";
test("previews JSON and ID and shows patches without save actions", async ({
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
  const inputs: unknown[] = [];
  await page.route("**/local-api/enrich/preview", (route) => {
    const request = route.request().postDataJSON();
    inputs.push(request.input);
    return route.fulfill({
      json: {
        event_source_version: 7,
        config_digest: "a1b2c3d4e5f6",
        enrich_status: "succeeded",
        original: { title: "CPU 告警" },
        effective_alert: {
          title: "CPU 告警",
          labels: { environment: "production" },
        },
        enrich: {
          processors: [
            {
              fields: {
                status: "succeeded",
                patches: [
                  {
                    op: "set",
                    path: "$.labels.environment",
                    value: "production",
                    rule_id: "business_labels",
                    operation_id: "assign_labels",
                  },
                ],
              },
            },
          ],
        },
        changes: [
          {
            path: "$.labels.environment",
            before: null,
            before_exists: false,
            after: "production",
            after_exists: true,
          },
        ],
        previous_changes: [],
        trace: [
          {
            processor: "fields",
            status: "succeeded",
            rules: [
              {
                rule_id: "business_labels",
                operation_id: "assign_labels",
                status: "succeeded",
              },
            ],
          },
        ],
      },
    });
  });
  await page.goto("/enrich-preview?bk_tenant_id=tenant-a&event_source_id=host");
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "执行预览" }).click();
  await expect(page.getByText("执行结果 · succeeded")).toBeVisible();
  expect(inputs[0]).toHaveProperty("alert");
  await page.getByRole("tab", { name: "处理器补丁" }).click();
  await expect(page.getByRole("tabpanel")).toContainText("production");
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: info.outputPath("enrich-preview-desktop.png"),
    fullPage: true,
  });
  await page.getByLabel("输入方式").selectOption("id");
  await page.getByLabel("Alert ID", { exact: true }).fill("alert-1");
  await page.getByRole("button", { name: "执行预览" }).click();
  await expect.poll(() => inputs.length).toBe(2);
  expect(inputs[1]).toEqual({ alert_id: "alert-1" });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("button", { name: "执行预览" })).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: info.outputPath("enrich-preview-mobile.png"),
    fullPage: true,
  });
  expect(
    await page.getByRole("button", { name: /^(保存|发布)$/ }).count(),
  ).toBe(0);
});
