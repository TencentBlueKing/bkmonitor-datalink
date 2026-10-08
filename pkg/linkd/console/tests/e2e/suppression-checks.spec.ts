import { test, expect } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import {
  runtimeClip,
  runtimeSuppressionMembers,
  clipID,
} from "../../src/test-fixtures/suppression-runtime";
import { suppressionRequestFixture } from "../../src/test-fixtures/suppression-checks";
test("controlled check keeps original command across reload and history after removal", async ({
  page,
}, info) => {
  const errors: string[] = [],
    writes: unknown[] = [];
  let gone = false,
    row = suppressionRequestFixture();
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/local-api/**", async (route) => {
    const request = route.request(),
      u = new URL(request.url());
    if (u.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (u.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    if (request.method() === "POST") {
      const body = request.postDataJSON();
      writes.push(body);
      if (writes.length === 1)
        return route.fulfill({
          status: 502,
          json: { error: { message: "对账提交结果未确认" } },
        });
      row = { ...row, command: { ...row.command, ...body } };
      return route.fulfill({ status: 202, json: row });
    }
    if (u.pathname.endsWith("/requests/" + row.id)) {
      gone = true;
      const done = suppressionRequestFixture(true);
      done.command = row.command;
      done.result = {
        ...done.result!,
        changed: true,
        outcome: "cleared",
        reason: "terminal_owner",
        owner_status: "closed",
      };
      row = done;
      return route.fulfill({ json: done });
    }
    if (u.pathname.endsWith("/requests"))
      return route.fulfill({
        json: {
          bk_tenant_id: "tenant-a",
          kind: "clip",
          window_id: clipID,
          items: [row],
          next: "",
        },
      });
    if (u.pathname.endsWith("/members"))
      return route.fulfill({ json: runtimeSuppressionMembers() });
    if (u.pathname.endsWith("/clip"))
      return route.fulfill({
        json: {
          bk_tenant_id: "tenant-a",
          kind: "clip",
          items: gone ? [] : [runtimeClip()],
          next: "",
        },
      });
    return gone
      ? route.fulfill({
          status: 404,
          json: { error: { message: "当前窗口已不存在" } },
        })
      : route.fulfill({ json: runtimeClip() });
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto(
    "/suppression-runtime?bk_tenant_id=tenant-a&kind=clip&id=" + clipID,
  );
  await page.getByLabel("对账原因").fill("检查原 owner 是否结束");
  await page.getByRole("button", { name: "提交受控对账" }).click();
  await expect(page.getByText("对账提交结果未确认")).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("对账原因")).toBeDisabled();
  await page.getByRole("button", { name: "重试同一对账操作" }).click();
  await expect(page.getByText("原窗口已清理", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("alert").filter({ hasText: "当前窗口已不存在" }),
  ).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "operator-a 检查已完成", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "提交受控对账" }),
  ).toBeDisabled();
  expect(writes).toHaveLength(2);
  expect(writes[1]).toEqual(writes[0]);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: info.outputPath("suppression-reconciled-dark.png"),
    fullPage: true,
  });
  await page.reload();
  await expect(page.getByText("原窗口已清理", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("alert").filter({ hasText: "当前窗口已不存在" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "提交受控对账" }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await page.setViewportSize({ width: 850, height: 1100 });
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: info.outputPath("suppression-reconciled-light.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  expect(errors).toEqual([]);
});
