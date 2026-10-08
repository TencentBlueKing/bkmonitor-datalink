import { expect, test } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import {
  projectionFixture,
  projectionID,
  projectionTenant,
  recoveredProjection,
} from "../../src/test-fixtures/projection-tasks";
test("projection retry preserves uncertain command and frozen snapshot", async ({
  page,
}, info) => {
  let row = projectionFixture();
  const writes: Record<string, unknown>[] = [],
    errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/local-api/**", async (route) => {
    const r = route.request(),
      u = new URL(r.url());
    if (u.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (u.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    if (r.method() === "POST") {
      const body = r.postDataJSON() as Record<string, unknown>;
      writes.push(body);
      expect(u.pathname.endsWith("/retry")).toBe(true);
      if (writes.length === 1)
        return route.fulfill({
          status: 502,
          json: { error: { message: "恢复结果未确认" } },
        });
      row = recoveredProjection(body);
      return route.fulfill({ status: 202, json: row });
    }
    expect(u.searchParams.get("bk_tenant_id")).toBe(projectionTenant);
    if (u.pathname.endsWith("/snapshot"))
      return route.fulfill({
        json: {
          bk_tenant_id: projectionTenant,
          id: projectionID,
          revision: row.revision,
          content_hash: row.content_hash,
          alert: {
            bk_tenant_id: projectionTenant,
            alert_id: row.alert_id,
            revision: row.revision,
            title: "冻结时的标题",
            enrich: { object: { a: 1, b: false } },
          },
        },
      });
    return route.fulfill({
      json: u.pathname.endsWith(projectionID)
        ? row
        : { bk_tenant_id: projectionTenant, items: [row], next: "" },
    });
  });
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.goto(
    "/projection-tasks?bk_tenant_id=" +
      projectionTenant +
      "&id=" +
      projectionID,
  );
  await expect(
    page.getByRole("heading", { name: "告警投影任务", exact: true }),
  ).toBeVisible();
  await page.getByLabel("恢复原因").fill("鉴权恢复后重试");
  await page.getByRole("button", { name: "恢复原任务" }).click();
  await expect(page.getByText("恢复结果未确认")).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("恢复原因")).toBeDisabled();
  await page.getByRole("button", { name: "重试同一恢复操作" }).click();
  await expect(page.getByText(/恢复操作已被接受/)).toBeVisible();
  expect(writes).toHaveLength(2);
  expect(writes[0]).toEqual(writes[1]);
  expect(writes[0]).toMatchObject({
    expected_version: "opaque-cas-1",
    bk_tenant_id: projectionTenant,
  });
  await page.getByRole("button", { name: "查看冻结快照" }).click();
  await expect(page.getByText(/冻结时的标题/)).toBeVisible();
  await page.screenshot({
    path: info.outputPath("projection-pending-dark.png"),
    fullPage: true,
  });
  row = {
    ...projectionFixture("delivered"),
    progress: {
      ...projectionFixture("delivered").progress,
      generation: 2,
      total_attempts: 2,
      last_retry: row.progress.last_retry,
    },
  };
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(
    page.getByText("远端已确认，本地水位尚待完成；后续执行只补 ACK。"),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await page.setViewportSize({ width: 850, height: 1050 });
  await page.screenshot({
    path: info.outputPath("projection-delivered-light.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  expect(errors).toEqual([]);
});
