import { mergePointFixture } from "../../src/test-fixtures/merge-retries";
import { expect, test } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import {
  decisionID,
  mergeTenant,
  runtimeDecision,
  runtimeMembers,
  runtimeRelation,
  runtimeWindow,
  runtimeSnapshot,
} from "../../src/test-fixtures/merge-runtime";

test("merge runtime separates transient windows, durable decisions and ended relationships", async ({
  page,
}, info) => {
  const errors: string[] = [],
    writes: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/local-api/**", async (route) => {
    const r = route.request(),
      u = new URL(r.url());
    if (r.method() !== "GET") writes.push(r.method() + u.pathname);
    if (u.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (u.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    if (u.pathname.includes("/policy-runtime/")) {
      const kind = u.pathname.includes("/relations/")
        ? "relations"
        : "decisions";
      if (u.pathname.endsWith("/control"))
        return route.fulfill({ json: mergePointFixture(kind, true) });
      if (u.pathname.endsWith("/requests"))
        return route.fulfill({
          json: {
            bk_tenant_id: mergeTenant,
            kind,
            target_id: decisionID,
            items: [],
            next: "",
          },
        });

      if (u.pathname.includes("/members/"))
        return route.fulfill({ json: runtimeSnapshot() });
      expect(u.searchParams.get("bk_tenant_id")).toBe(mergeTenant);
      if (u.pathname.endsWith("/members"))
        return route.fulfill({ json: runtimeMembers() });
      if (u.pathname.includes("/windows/"))
        return route.fulfill({ json: runtimeWindow() });
      if (u.pathname.includes("/relations/"))
        return route.fulfill({ json: runtimeRelation() });
      if (u.pathname.includes("/decisions/"))
        return route.fulfill({ json: runtimeDecision() });
      const resource = u.searchParams.get("resource");
      return route.fulfill({
        json: {
          bk_tenant_id: mergeTenant,
          items: [
            resource === "windows"
              ? runtimeWindow()
              : resource === "relations"
                ? runtimeRelation()
                : runtimeDecision(),
          ],
          next: "",
        },
      });
    }
    return route.fulfill({ json: {} });
  });
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.goto(
    "/merge-runtime?bk_tenant_id=tenant-a&resource=decisions&id=" + decisionID,
  );
  await expect(
    page.getByRole("heading", { name: "合并运行态", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("table", { name: "固定成员快照" })).toBeVisible();
  await page.getByRole("button", { name: "查看冻结快照" }).click();
  await expect(page.getByText(/opening content/)).toBeVisible();
  await page.getByRole("button", { name: "关闭快照" }).click();
  await expect(page.getByRole("link", { name: "child-a" })).toHaveAttribute(
    "href",
    /detail_tenant=tenant-a/,
  );
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await page.screenshot({
    path: info.outputPath("merge-decision-dark.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "查看临时窗口" }).click();
  await expect(page.getByRole("table", { name: "窗口成员" })).toBeVisible();
  await expect(page.getByText("尚未确认", { exact: true })).toBeVisible();
  await expect(page.getByText("条件组 2：0 个首次命中成员")).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await page.screenshot({
    path: info.outputPath("merge-window-dark.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "持久化裁决", exact: true }).click();
  await page.getByRole("button", { name: "查看详情" }).click();
  await page.getByRole("button", { name: "查看父子关系" }).click();
  await expect(page.getByRole("table", { name: "关系成员" })).toBeVisible();
  await expect(page.getByText(/父人工关闭只解除关系/)).toBeVisible();
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 850, height: 1050 });
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await page.screenshot({
    path: info.outputPath("merge-relation-light.png"),
    fullPage: true,
    animations: "disabled",
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  expect(errors).toEqual([]);
  expect(writes).toEqual([]);
});
