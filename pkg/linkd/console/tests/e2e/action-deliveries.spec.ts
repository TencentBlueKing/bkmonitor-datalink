import { expect, test } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import {
  actionFixture,
  actionID,
  actionTenant,
  recoveredAction,
  actionOrderFixture,
  actionSnapshotFixture,
} from "../../src/test-fixtures/action-deliveries";
test("action retry keeps original command and separates visibility, local skip and prior uncertainty", async ({
  page,
}, info) => {
  let row = actionFixture();
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
      row = recoveredAction(body);
      return route.fulfill({ status: 202, json: row });
    }
    expect(u.searchParams.get("bk_tenant_id")).toBe(actionTenant);
    if (u.pathname.endsWith("/snapshot"))
      return route.fulfill({ json: actionSnapshotFixture(row) });
    if (u.pathname.endsWith("/order"))
      return route.fulfill({ json: actionOrderFixture(row) });
    return route.fulfill({
      json: u.pathname.endsWith(actionID)
        ? row
        : { bk_tenant_id: actionTenant, items: [row], next: "" },
    });
  });
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.goto(
    "/action-deliveries?bk_tenant_id=" + actionTenant + "&id=" + actionID,
  );
  await expect(
    page.getByRole("heading", { name: "告警动作投递", exact: true }),
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
    bk_tenant_id: actionTenant,
  });
  await page.getByRole("button", { name: "查看冻结快照" }).click();
  await expect(page.getByText(/冻结时的标题/)).toBeVisible();
  await expect(
    page.locator(".projection-snapshot .json-viewer pre"),
  ).toContainText("linkd.kac-action.v2");
  const audit = row.progress.last_retry;
  row = {
    ...actionFixture("waiting_projection"),
    progress: {
      ...actionFixture("waiting_projection").progress,
      generation: 2,
      total_attempts: 1,
      last_retry: audit,
    },
  };
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(
    page
      .locator(".merge-detail-summary")
      .getByText("等待投影可见", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("action-waiting-dark.png"),
    fullPage: true,
  });
  row = {
    ...actionFixture("skipped"),
    progress: {
      ...actionFixture("skipped").progress,
      generation: 2,
      total_attempts: 2,
      last_retry: audit,
      previous_unconfirmed: true,
    },
  };
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(page.getByText(/此前尝试结果未确认/)).toBeVisible();
  await expect(page.getByText(/本次依据较新终态投影在本地跳过/)).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Celery 投递确认" }),
  ).toHaveCount(0);
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await page.setViewportSize({ width: 850, height: 1050 });
  await page.screenshot({
    path: info.outputPath("action-skipped-light.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  expect(errors).toEqual([]);
});
