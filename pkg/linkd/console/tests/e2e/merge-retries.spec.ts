import { test, expect } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import {
  mergePointFixture,
  mergeRetryFixture,
} from "../../src/test-fixtures/merge-retries";
import {
  decisionID,
  runtimeDecision,
  runtimeMembers,
} from "../../src/test-fixtures/merge-runtime";
test("merge retry retains original token across uncertainty while business progress advances", async ({
  page,
}, info) => {
  const errors: string[] = [],
    writes: unknown[] = [];
  let point = mergePointFixture(),
    row = mergeRetryFixture();
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/local-api/**", async (route) => {
    const req = route.request(),
      u = new URL(req.url());
    if (u.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (u.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    if (req.method() === "POST") {
      const body = req.postDataJSON();
      writes.push(body);
      row = { ...row, command: { ...row.command, ...body } };
      point = mergeRetryFixture(true).result!.after!;
      if (writes.length === 1)
        return route.fulfill({
          status: 502,
          json: { error: { message: "合并请求结果未确认" } },
        });
      return route.fulfill({ status: 202, json: row });
    }
    if (u.pathname.endsWith("/control")) return route.fulfill({ json: point });
    if (u.pathname.endsWith("/requests/" + row.id)) {
      row = { ...mergeRetryFixture(true), command: row.command };
      return route.fulfill({ json: row });
    }
    if (u.pathname.endsWith("/requests"))
      return route.fulfill({
        json: {
          bk_tenant_id: "tenant-a",
          kind: "decisions",
          target_id: decisionID,
          items: writes.length ? [row] : [],
          next: "",
        },
      });
    if (u.pathname.endsWith("/members"))
      return route.fulfill({ json: runtimeMembers() });
    const d = runtimeDecision();
    d.phase = point.phase as "prepared" | "waiting_parent";
    d.member_offset = 0;
    d.window_finished = false;
    delete d.parent_alert_id;
    if (u.pathname.endsWith(decisionID)) return route.fulfill({ json: d });
    return route.fulfill({
      json: { bk_tenant_id: "tenant-a", items: [d], next: "" },
    });
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto(
    "/merge-runtime?bk_tenant_id=tenant-a&resource=decisions&id=" + decisionID,
  );
  await page.getByLabel("操作原因").fill("接续已准备的父 Event");
  await page.getByRole("button", { name: "请求接续裁决", exact: true }).click();
  await expect(
    page.getByText("合并请求结果未确认", { exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("操作原因")).toBeDisabled();
  await page.getByRole("button", { name: "重试同一操作", exact: true }).click();
  await expect(page.getByText("原进度已推进", { exact: true })).toBeVisible();
  await expect(page.getByText(/执行后阶段：waiting_parent/)).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "operator-a 本次操作已完成", exact: true }),
  ).toBeVisible();
  expect(writes).toHaveLength(2);
  expect(writes[1]).toEqual(writes[0]);
  expect(writes[1]).toMatchObject({
    expected_token: mergePointFixture().token,
  });
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await page.screenshot({
    path: info.outputPath("merge-retry-dark.png"),
    fullPage: true,
  });
  await page.reload();
  await expect(page.getByText("原进度已推进", { exact: true })).toBeVisible();
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await page.setViewportSize({ width: 850, height: 1100 });
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await page.screenshot({
    path: info.outputPath("merge-retry-light.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  expect(errors).toEqual([]);
});
