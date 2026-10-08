import { expect, test } from "@playwright/test";
import { explorerCapabilities } from "../fixtures/explorer";
import { policyRecord, policyRelease } from "../../src/test-fixtures/policies";
import { policyLinkQuerySchema } from "../../src/shared/policy-links";
import {
  loadPolicyLinks,
  resolvePolicyLink,
} from "../../src/server/policy-links";
import type { PolicyKind } from "../../src/shared/policies";

test("KAC policy links preserve explicit tenant mapping, open current config and retain historical inspection", async ({
  page,
  context,
}, info) => {
  const errors: string[] = [],
    writes: string[] = [];
  const links = loadPolicyLinks(
    JSON.stringify({
      "tenant-a": {
        suppression:
          "https://kac.example/kingeye/#/kac/editAlarmRestrain?mode=edit&id={policy_id}",
        shield:
          "https://kac.example/kingeye/#/kac/editAlarmShield?mode=edit&id={policy_id}",
        merge:
          "https://kac.example/kingeye/#/kac/alarmMerge/edit?id={policy_id}",
      },
    }),
  );
  page.on("pageerror", (e) => errors.push(e.message));
  await context.route("https://kac.example/**", (route) =>
    route.fulfill({
      contentType: "text/html; charset=utf-8",
      body: "<!doctype html><title>KAC navigation fixture</title><h1>KAC 配置入口测试页</h1>",
    }),
  );
  await page.route("**/local-api/**", async (route) => {
    const req = route.request(),
      url = new URL(req.url());
    if (req.method() !== "GET") writes.push(url.pathname);
    if (url.pathname.endsWith("/capabilities"))
      return route.fulfill({ json: explorerCapabilities });
    if (url.pathname.endsWith("/version"))
      return route.fulfill({ json: { version: "test" } });
    if (url.pathname === "/local-api/policy-links")
      return route.fulfill({
        json: resolvePolicyLink(
          links,
          policyLinkQuerySchema.parse(Object.fromEntries(url.searchParams)),
        ),
      });
    const kind = (url.searchParams.get("type") ??
      url.pathname.split("/")[3]) as PolicyKind;
    const tenant = url.searchParams.get("bk_tenant_id")!;
    const record = policyRecord(kind);
    record.bk_tenant_id = tenant;
    if (record.pending) record.pending.bk_tenant_id = tenant;
    if (url.pathname === "/local-api/policies")
      return route.fulfill({ json: { items: [record], next: "" } });
    const version = /releases\/(\d+)/.exec(url.pathname);
    if (version)
      return route.fulfill({
        json: {
          ...policyRelease(Number(version[1]), kind),
          bk_tenant_id: tenant,
        },
      });
    return route.fulfill({ json: record });
  });
  await page.setViewportSize({ width: 1512, height: 1100 });
  for (const [type, expected] of [
    ["suppression", "/kac/editAlarmRestrain?mode=edit&id=db-noise"],
    ["shield", "/kac/editAlarmShield?mode=edit&id=db-noise"],
    ["merge", "/kac/alarmMerge/edit?id=db-noise"],
  ]) {
    await page.goto(
      "/policies?bk_tenant_id=tenant-a&type=" + type + "&id=db-noise&version=1",
    );
    await expect(
      page.getByRole("heading", { name: "发布 v1", exact: true }),
    ).toBeVisible();
    const link = page.getByRole("link", { name: /在 KAC 管理/ });
    await expect(link).toHaveAttribute(
      "href",
      "https://kac.example/kingeye/#" + expected,
    );
    const popupEvent = page.waitForEvent("popup");
    await link.click();
    const popup = await popupEvent;
    await expect(
      popup.getByRole("heading", { name: "KAC 配置入口测试页" }),
    ).toBeVisible();
    expect(
      await popup.evaluate(() => ({
        referrer: document.referrer,
        opener: window.opener === null,
      })),
    ).toEqual({ referrer: "", opener: true });
    expect(popup.url()).toBe("https://kac.example/kingeye/#" + expected);
    await popup.close();
    await expect(
      page.getByRole("heading", { name: "发布 v1", exact: true }),
    ).toBeVisible();
    if (type === "shield") {
      await expect(page.getByText(/目标范围仅限制主告警/)).toBeVisible();
      await page.screenshot({
        path: info.outputPath("kac-policy-link-dark.png"),
        fullPage: true,
      });
      await page.getByRole("button", { name: "切换为浅色模式" }).click();
      await page.setViewportSize({ width: 850, height: 1100 });
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBeLessThanOrEqual(850);
      await page.screenshot({
        path: info.outputPath("kac-policy-link-light.png"),
        fullPage: true,
      });
    }
  }
  links["tenant-a"].merge =
    "https://kac.example/kingeye/#/kac/alarmMerge?from=updated";
  await page.getByRole("button", { name: "刷新策略", exact: true }).click();
  await expect(page.getByRole("link", { name: /在 KAC 管理/ })).toHaveAttribute(
    "href",
    links["tenant-a"].merge,
  );
  await page.goto("/policies?bk_tenant_id=tenant-b&type=shield&id=db-noise");
  await expect(
    page.getByText("当前租户尚未配置此类策略的 KAC 入口。"),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: /在 KAC 管理/ })).toHaveCount(0);
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
});
