import { readFileSync } from "node:fs";
import { expect, test } from "@playwright/test";
import { z } from "zod";

const id = z.string().min(1);
const fixture = z
  .object({
    backend: z.enum(["elasticsearch", "mysql"]),
    base_url: z
      .string()
      .url()
      .refine((v) => {
        const u = new URL(v);
        return u.hostname === "127.0.0.1" && u.pathname === "/real-console";
      }),
    clip_window: id,
    clip_event: id,
    aggregation_window: id,
    aggregation_event: id,
    aggregation_owner: id,
    shield_alert: id,
    shield_revision: z.number().int().positive(),
    merge_decision: id,
    merge_parent: id,
    merge_children: z.array(id).length(2),
    merge_window: id,
    merge_waiting: id,
    action_task: id,
    action_version: id,
    action_hash: id,
    projection_task: id,
    projection_version: id,
    projection_hash: id,
  })
  .parse(
    JSON.parse(readFileSync(process.env.LINKD_CONSOLE_REAL_FIXTURE!, "utf8")),
  );

// 此用例不使用 route/fulfill；所有页面、详情和控制命令均访问真实 Console/Linkd。
test("real Event enrichment and policy runtime remain coherent through browser controls", async ({
  page,
}, info) => {
  const failures: string[] = [],
    writes: string[] = [];
  page.on("pageerror", (e) => failures.push(e.message));
  page.on("response", (response) => {
    const u = new URL(response.url());
    if (u.pathname.includes("/local-api/") && response.status() >= 400)
      failures.push(`${response.status()} ${u.pathname}`);
  });
  page.on("request", (request) => {
    const u = new URL(request.url());
    if (u.pathname.includes("/local-api/") && request.method() !== "GET")
      writes.push(request.method() + " " + u.pathname);
  });
  const url = (path: string, params: Record<string, string>) =>
    fixture.base_url + path + "?" + new URLSearchParams(params);
  const screenshot = async (name: string) => {
    await page.evaluate(() => window.scrollTo(0, 0));
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
    await page.screenshot({
      path: info.outputPath(name + ".png"),
      fullPage: true,
    });
  };

  await page.goto(
    url("/policies", {
      bk_tenant_id: "clip",
      type: "suppression",
      id: "enabled-e2e",
    }),
  );
  await expect(
    page.getByRole("heading", { name: "发布 v1", exact: true }),
  ).toBeVisible();
  await page.getByLabel("预览输入 ID").fill(fixture.clip_event);
  await page.getByRole("button", { name: "执行只读匹配", exact: true }).click();
  await expect(page.getByRole("region", { name: "匹配结果" })).toBeVisible();
  await screenshot("policy-preview");

  await page.goto(
    url("/explore/events", {
      bk_tenant_id: "clip",
      detail_tenant: "clip",
      detail: fixture.clip_event,
    }),
  );
  const event = page.getByRole("dialog");
  await expect(
    event.getByRole("region", { name: "事件抑制记录" }).getByText(/2 \/ 3 次/),
  ).toBeVisible();
  await event.getByRole("tab", { name: "完整 JSON", exact: true }).click();
  await expect(event.locator(".json-viewer pre")).toContainText("policy-e2e");
  const payload = JSON.parse(
    await event.locator(".json-viewer pre").innerText(),
  ) as { enrich?: unknown; content?: string; _processing?: { state?: string } };
  expect(payload._processing?.state).toBe("suppressed");
  expect(JSON.stringify(payload.enrich)).toContain("enrich_marker");
  expect(payload.content).toBe("content-browser-clip-2");
  await event.screenshot({ path: info.outputPath("event-enrichment.png") });

  await page.goto(
    url("/suppression-runtime", {
      bk_tenant_id: "clip",
      kind: "clip",
      id: fixture.clip_window,
    }),
  );
  await expect(
    page.getByRole("heading", { name: "防抖详情", exact: true }),
  ).toBeVisible();
  await expect(
    page
      .getByRole("table", { name: "当前抑制窗口" })
      .getByText("2 / 3 次", { exact: true }),
  ).toBeVisible();
  await expect(
    page
      .getByRole("region", { name: "当前保留成员" })
      .getByRole("link", { name: fixture.clip_event, exact: true }),
  ).toHaveAttribute("href", /detail_tenant=clip/);
  await page.getByLabel("对账原因").fill("真实 Console 复核当前计数");
  await page.getByRole("button", { name: "提交受控对账", exact: true }).click();
  await expect(page.getByText("窗口保留", { exact: true })).toBeVisible();
  await expect(
    page.getByText("unbound_counter", { exact: true }),
  ).toBeVisible();
  await screenshot("clip-runtime");

  await page.goto(
    url("/suppression-runtime", {
      bk_tenant_id: "aggregation",
      kind: "aggregation",
      id: fixture.aggregation_window,
    }),
  );
  await expect(
    page.getByRole("heading", { name: "关联聚合详情", exact: true }),
  ).toBeVisible();
  await expect(
    page
      .getByRole("link", { name: fixture.aggregation_owner, exact: true })
      .first(),
  ).toHaveAttribute("href", /detail_tenant=aggregation/);
  await expect(
    page
      .getByRole("region", { name: "当前保留成员" })
      .getByRole("link", { name: fixture.aggregation_event, exact: true }),
  ).toBeVisible();
  await screenshot("aggregation-runtime");

  await page.goto(
    url("/shield-runtime", {
      bk_tenant_id: "shield",
      id: fixture.shield_alert,
    }),
  );
  await expect(
    page.getByRole("heading", { name: "当前绑定", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("时间屏蔽", { exact: true }).last(),
  ).toBeVisible();
  await page.getByLabel("复查原因").fill("真实浏览器验证屏蔽条件");
  await page.getByRole("button", { name: "提交复查", exact: true }).click();
  await expect(page.getByText("请求已完成", { exact: true })).toBeVisible({
    timeout: 45000,
  });
  await expect(
    page
      .getByRole("region", { name: "屏蔽复查" })
      .getByText("保留屏蔽", { exact: true })
      .first(),
  ).toBeVisible();
  await expect(page.getByText(/本生命周期尚未放行处置/)).toBeVisible();
  await screenshot("shield-request");

  await page.goto(
    url("/merge-runtime", {
      bk_tenant_id: "merge",
      resource: "windows",
      id: fixture.merge_window,
    }),
  );
  await expect(
    page
      .getByRole("table", { name: "窗口成员" })
      .getByRole("link", { name: fixture.merge_waiting, exact: true }),
  ).toBeVisible();
  await screenshot("merge-window");
  await page.goto(
    url("/merge-runtime", {
      bk_tenant_id: "merge",
      resource: "decisions",
      id: fixture.merge_decision,
    }),
  );
  await expect(
    page.getByRole("table", { name: "固定成员快照" }).getByRole("row"),
  ).toHaveCount(3);
  await page
    .getByRole("button", { name: "查看冻结快照", exact: true })
    .first()
    .click();
  await expect(page.getByText(/content-browser-merge-/)).toBeVisible();
  await page.getByRole("button", { name: "关闭快照", exact: true }).click();
  await screenshot("merge-decision");
  await page.goto(
    url("/merge-runtime", {
      bk_tenant_id: "merge",
      resource: "relations",
      alert_id: fixture.merge_parent,
      id: fixture.merge_decision,
    }),
  );
  await page.getByLabel("操作原因").fill("真实 Console 复核活动合并关系");
  await page.getByRole("button", { name: "请求检查关系", exact: true }).click();
  await expect(page.getByText("本次无需推进", { exact: true })).toBeVisible({
    timeout: 45000,
  });
  await expect(page.getByText("no_progress", { exact: true })).toBeVisible();
  await screenshot("merge-request");

  await page.goto(
    url("/explore/alerts", {
      bk_tenant_id: "merge",
      detail_tenant: "merge",
      detail: fixture.merge_parent,
    }),
  );
  const parent = page.getByRole("dialog");
  await expect(
    parent
      .getByRole("region", { name: "策略与投影状态" })
      .getByText("合并主告警 · 关系已就绪", { exact: true }),
  ).toBeVisible();
  await parent.getByRole("button", { name: "主动关闭", exact: true }).click();
  await parent.getByLabel("关闭原因").fill("真实浏览器验证父关闭只解除关系");
  await parent
    .getByRole("button", { name: "确认关闭告警", exact: true })
    .click();
  await expect(parent.getByText(/告警已关闭/)).toBeVisible({ timeout: 30000 });
  await parent.screenshot({ path: info.outputPath("parent-closed.png") });

  await page.goto(
    url("/merge-runtime", {
      bk_tenant_id: "merge",
      resource: "relations",
      alert_id: fixture.merge_parent,
      id: fixture.merge_decision,
    }),
  );
  await expect
    .poll(
      async () => {
        await expect(
          page.getByRole("button", { name: "刷新", exact: true }),
        ).toBeEnabled();
        await page.getByRole("button", { name: "刷新", exact: true }).click();
        return page.getByText("关系已结束", { exact: true }).count();
      },
      { timeout: 75000, intervals: [1000, 2000, 5000] },
    )
    .toBeGreaterThan(0);
  await page
    .getByRole("button", { name: "切换为浅色模式", exact: true })
    .click();
  await page.setViewportSize({ width: 850, height: 1050 });
  await screenshot("merge-ended-light");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);

  await page.goto(
    url("/explore/alerts", {
      bk_tenant_id: "merge",
      detail_tenant: "merge",
      detail: fixture.merge_children[0],
    }),
  );
  const child = page.getByRole("dialog");
  await child.getByRole("tab", { name: "完整 JSON", exact: true }).click();
  const current = JSON.parse(
    await child.locator(".json-viewer pre").innerText(),
  ) as {
    status?: string;
    admission?: { admitted_at?: string };
    content?: string;
  };
  expect(current.status).toBe("active");
  expect(current.admission?.admitted_at).toBeUndefined();
  expect(current.content).toBe("content-browser-merge-a");
  await page.goto(
    url("/projection-tasks", {
      bk_tenant_id: "shield",
      id: fixture.projection_task,
    }),
  );
  await expect(
    page.getByRole("heading", { name: "告警投影任务", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(fixture.projection_hash, { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "查看冻结快照" }).click();
  await expect(
    page.locator(".projection-snapshot .json-viewer pre"),
  ).toContainText("content-browser-shield");
  await page.getByLabel("恢复原因").fill("真实 Console 验证恢复原任务");
  await page.getByRole("button", { name: "恢复原任务" }).click();
  await expect(page.getByText(/恢复操作已被接受/)).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "最近一次人工恢复" }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "最近一次人工恢复" }),
  ).toBeVisible();
  await expect(page.getByText("console-local", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "恢复原任务" })).toHaveCount(0);
  await screenshot("projection-retry-light");
  await page.goto(
    url("/action-deliveries", {
      bk_tenant_id: "aggregation",
      id: fixture.action_task,
    }),
  );
  await expect(
    page.getByRole("heading", { name: "告警动作投递", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(fixture.action_hash, { exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/当前任务是可见队首/)).toBeVisible();
  await page.getByRole("button", { name: "查看冻结快照" }).click();
  await expect(
    page.locator(".projection-snapshot .json-viewer pre"),
  ).toContainText("content-browser-agg-main");
  await page.getByLabel("恢复原因").fill("真实 Console 恢复原动作");
  await page.getByRole("button", { name: "恢复原任务" }).click();
  await expect(page.getByText(/恢复操作已被接受/)).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "最近一次人工恢复" }),
  ).toBeVisible();
  await expect(page.getByText("console-local", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "恢复原任务" })).toHaveCount(0);
  await screenshot("action-retry-light");

  await page.goto(
    url("/suppression-cleanups", {
      bk_tenant_id: "merge",
      alert_id: fixture.merge_parent,
    }),
  );
  await expect(
    page.getByRole("heading", { name: "抑制清理历史", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: fixture.merge_parent, exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "查看记录", exact: true }).click();
  await expect(page.getByText("诊断已保存，请分别查看两类结果")).toBeVisible();
  await expect(page.getByText("防抖：本轮未发现可清理登记")).toBeVisible();
  await screenshot("suppression-cleanup-light");
  expect(writes).toEqual([
    "POST /real-console/local-api/policies/preview",
    `POST /real-console/local-api/policy-runtime/suppression/clip/${encodeURIComponent(fixture.clip_window)}/reconcile`,
    `POST /real-console/local-api/policy-runtime/shield/alerts/${fixture.shield_alert}/reconcile`,
    `POST /real-console/local-api/policy-runtime/merge/relations/${fixture.merge_decision}/requests`,
    `POST /real-console/local-api/alerts/${fixture.merge_parent}/close`,
    `POST /real-console/local-api/projection-tasks/${fixture.projection_task}/retry`,
    `POST /real-console/local-api/action-deliveries/${fixture.action_task}/retry`,
  ]);
  expect(failures).toEqual([]);
});
