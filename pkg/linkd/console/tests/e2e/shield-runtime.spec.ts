import {
  runtimeCheck,
  runtimeCheckRequest,
} from "../../src/test-fixtures/shield-checks";
import { expect, test } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import {
  childID,
  runtimeShield,
  runtimeShieldHistory,
  shieldTenant,
} from "../../src/test-fixtures/shield-runtime";

test("shield runtime keeps binding history separate from lifecycle and admission", async ({
  page,
}, info) => {
  const hint = runtimeCheck();
  hint.trigger = "hint";
  for (let i = 0; i < 17; i++)
    hint.report.decision!.steps.push({
      policy: hint.report.decision!.steps[0].policy,
      outcome: "skipped",
      reason_code: `candidate-${i}`,
    });
  let released = false;
  const errors: string[] = [],
    writes: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/local-api/**", async (route) => {
    const request = route.request(),
      u = new URL(request.url());
    if (request.method() !== "GET") writes.push(request.method() + u.pathname);
    if (u.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (u.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    expect(u.searchParams.get("bk_tenant_id")).toBe(shieldTenant);
    if (u.pathname.endsWith("/check"))
      return route.fulfill({
        json: {
          bk_tenant_id: shieldTenant,
          alert_id: childID,
          check: hint,
        },
      });
    if (u.pathname.endsWith("/requests"))
      return route.fulfill({
        json: {
          bk_tenant_id: shieldTenant,
          alert_id: childID,
          items: [],
          next: "",
        },
      });
    if (u.pathname.endsWith("/history"))
      return route.fulfill({
        json: u.searchParams.get("after")
          ? runtimeShieldHistory()
          : { ...runtimeShieldHistory(), items: [], next: "later-history" },
      });
    if (u.pathname.endsWith(childID))
      return route.fulfill({ json: runtimeShield(released) });
    return route.fulfill({
      json: {
        bk_tenant_id: shieldTenant,
        items: released ? [] : [runtimeShield()],
        next: "",
      },
    });
  });
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.goto("/shield-runtime?bk_tenant_id=tenant-a&id=" + childID);
  await expect(
    page.getByRole("heading", { name: "屏蔽运行态", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "main-alert" })).toHaveAttribute(
    "href",
    /detail_tenant=tenant-a/,
  );
  await expect(page.getByText(/本生命周期尚未放行处置/)).toBeVisible();
  await expect(page.getByText(/事件提示复查/)).toBeVisible();
  await expect(page.getByText("1 / 2 · 共 18 条步骤")).toBeVisible();
  await page.getByRole("button", { name: "检查步骤下一页" }).click();
  await expect(page.getByText("candidate-16", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "检查步骤上一页" }).click();
  await page.getByRole("button", { name: "下一页历史" }).click();
  await expect(page.getByText(/解除绑定/)).toBeVisible();
  await page.getByText("查看前后绑定与原因", { exact: true }).click();
  await expect(page.getByText(/before_bindings/)).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await page.screenshot({
    path: info.outputPath("shield-bindings-dark.png"),
    fullPage: true,
  });
  released = true;
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(page.getByText("当前没有活动屏蔽绑定。")).toBeVisible();
  await expect(page.getByText(/本生命周期尚未放行处置/)).toBeVisible();
  await expect(
    page.getByRole("button", { name: "刷新", exact: true }),
  ).toBeEnabled();
  await expect(
    page.getByRole("button", { name: "历史首页", exact: true }),
  ).toBeDisabled();
  await expect(page.getByText(/本页没有屏蔽流水/)).toBeVisible();
  await page.getByRole("button", { name: "下一页历史" }).click();
  await expect(page.getByText(/解除绑定/)).toBeVisible();
  await page.getByText("查看前后绑定与原因", { exact: true }).click();
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 850, height: 1050 });
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await page.screenshot({
    path: info.outputPath("shield-released-light.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  expect(errors).toEqual([]);
  expect(writes).toEqual([]);
});

test("manual shield check retains command after uncertain response and shows safe partial diagnosis", async ({
  page,
}, info) => {
  const writes: unknown[] = [],
    errors: string[] = [];
  let accepted = runtimeCheckRequest();
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
      expect(u.pathname.endsWith("/reconcile")).toBe(true);
      if (writes.length === 1)
        return route.fulfill({
          status: 502,
          json: { error: { message: "提交结果未确认，请重试同一操作。" } },
        });
      accepted = { ...accepted, command: { ...accepted.command, ...body } };
      return route.fulfill({ status: 202, json: accepted });
    }
    expect(u.searchParams.get("bk_tenant_id")).toBe(shieldTenant);
    if (u.pathname.endsWith("/requests/" + accepted.id)) {
      const done = runtimeCheckRequest(true);
      done.command = accepted.command;
      return route.fulfill({ json: done });
    }
    if (u.pathname.endsWith("/check"))
      return route.fulfill({
        json: {
          bk_tenant_id: shieldTenant,
          alert_id: childID,
          check: runtimeCheck(),
        },
      });
    if (u.pathname.endsWith("/requests"))
      return route.fulfill({
        json: {
          bk_tenant_id: shieldTenant,
          alert_id: childID,
          items: [],
          next: "",
        },
      });
    if (u.pathname.endsWith("/history"))
      return route.fulfill({ json: runtimeShieldHistory() });
    if (u.pathname.endsWith(childID))
      return route.fulfill({ json: runtimeShield() });
    return route.fulfill({
      json: { bk_tenant_id: shieldTenant, items: [runtimeShield()], next: "" },
    });
  });
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.goto("/shield-runtime?bk_tenant_id=tenant-a&id=" + childID);
  await expect(
    page.getByText("部分条件未能检查", { exact: true }),
  ).toBeVisible();
  await page.getByLabel("复查原因").fill("依赖服务恢复后复查");
  await page.getByRole("button", { name: "提交复查", exact: true }).click();
  await expect(
    page.getByText("提交结果未确认，请重试同一操作。", { exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("复查原因")).toBeDisabled();
  await page
    .getByRole("button", { name: "重试同一复查操作", exact: true })
    .click();
  await expect(page.getByText("请求已完成", { exact: true })).toBeVisible();
  expect(writes).toHaveLength(2);
  expect(writes[1]).toEqual(writes[0]);
  expect(writes[0]).toMatchObject({
    expected_revision: 2,
    bk_tenant_id: shieldTenant,
  });
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await page.screenshot({
    path: info.outputPath("shield-check-dark.png"),
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await page.setViewportSize({ width: 850, height: 1050 });
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await page.screenshot({
    path: info.outputPath("shield-check-light.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  expect(errors).toEqual([]);
});
