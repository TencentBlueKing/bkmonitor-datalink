import { expect, test } from "@playwright/test";
import {
  alertFixture,
  explorerCapabilities,
  explorerStats,
} from "../fixtures/explorer";

test("Alert policy dimensions and projection confirmation stay independent through refresh", async ({
  page,
}, info) => {
  const errors: string[] = [];
  const writes: string[] = [];
  let confirmed = false;
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/local-api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() !== "GET") writes.push(url.pathname);
    if (url.pathname.endsWith("version"))
      return route.fulfill({ json: { version: "test" } });
    if (url.pathname.endsWith("capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    const item = {
      ...alertFixture,
      payload: {
        ...alertFixture.payload,
        revision: 8,
        shield: {
          active: true,
          next_check_at: alertFixture.timestamp,
          bindings: [
            {
              binding_id: "shield-binding",
              policy: { id: "maintenance", version: 2 },
              type: "time",
            },
          ],
        },
        merge: {
          role: "original",
          state: "pending",
          pending: [
            {
              window_id: "a".repeat(64),
              policy: { id: "merge-policy", version: 4 },
              deadline: alertFixture.timestamp,
            },
          ],
          relation_ids: [],
        },
        admission: { admitted_at: alertFixture.timestamp, severity: "warning" },
        projection: {
          targets: {
            kac: {
              source_version: 5,
              required_revision: 8,
              synced_revision: confirmed ? 8 : 7,
              synced_at: alertFixture.timestamp,
            },
            audit: {
              source_version: 5,
              required_revision: 8,
              synced_revision: 8,
              synced_at: alertFixture.timestamp,
            },
          },
        },
      },
    };
    if (url.pathname.endsWith("/stats"))
      return route.fulfill({ json: explorerStats("alerts") });
    if (url.pathname.startsWith("/local-api/alerts/"))
      return route.fulfill({ json: item });
    return route.fulfill({
      json: {
        source: "elasticsearch",
        items: url.pathname === "/local-api/alerts" ? [item] : [],
        warnings: [],
      },
    });
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto("/explore/alerts?bk_tenant_id=tenant-a");
  await page
    .getByRole("button", { name: alertFixture.payload.title as string })
    .click();
  const policy = page.getByRole("region", { name: "策略与投影状态" });
  await expect(policy.getByText("屏蔽中", { exact: true })).toBeVisible();
  await expect(policy.getByText("等待合并裁决", { exact: true })).toBeVisible();
  await expect(policy.getByText("放行级别：warning")).toBeVisible();
  const table = policy.getByRole("table", { name: "投影目标水位" });
  await expect(table.getByRole("row").filter({ hasText: "kac" })).toContainText(
    "待同步",
  );
  await expect(
    table.getByRole("row").filter({ hasText: "audit" }),
  ).toContainText("已确认");
  await policy.getByText("合并等待窗口（1）", { exact: true }).click();
  await expect(policy.getByText("merge-policy / 4")).toBeVisible();
  await policy.screenshot({ path: info.outputPath("alert-policy-dark.png") });
  confirmed = true;
  await page.getByRole("button", { name: "刷新详情" }).click();
  await expect(table.getByRole("row").filter({ hasText: "kac" })).toContainText(
    "已确认",
  );
  await expect(policy.getByText("屏蔽中", { exact: true })).toBeVisible();
  await expect(policy.getByText("等待合并裁决", { exact: true })).toBeVisible();
  await page.setViewportSize({ width: 780, height: 1000 });
  await policy.screenshot({
    path: info.outputPath("alert-policy-compact.png"),
  });
  await expect(policy.getByRole("button")).toHaveCount(0);
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
});
