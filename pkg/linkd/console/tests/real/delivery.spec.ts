import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { z } from "zod";
import {
  controlPlaneRuntimeSchema,
  metricsResponseSchema,
  type MetricsResponse,
} from "../../src/shared/contracts";

const fixture = z
  .object({
    backend: z.enum(["elasticsearch", "mysql"]),
    base_url: z
      .string()
      .url()
      .refine((value) => {
        const u = new URL(value);
        return u.hostname === "127.0.0.1" && u.pathname === "/real-console";
      }),
    instance: z.string().min(1),
    alert_id: z.string().min(1),
    action_task: z.string().min(1),
    projection_task: z.string().min(1),
  })
  .parse(
    JSON.parse(
      readFileSync(process.env.LINKD_CONSOLE_DELIVERY_FIXTURE!, "utf8"),
    ),
  );

type Kind = "projection" | "action";
const endpoint = (kind: Kind) => `/local-api/${kind}-metrics`;
async function query(page: Page, kind: Kind, action: () => Promise<void>) {
  const response = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname.endsWith(endpoint(kind)) &&
      response.status() === 200,
    { timeout: 20000 },
  );
  await action();
  return metricsResponseSchema.parse(await (await response).json());
}
function assertLive(data: MetricsResponse, kind: Kind) {
  expect(data.panels).toHaveLength(kind === "action" ? 8 : 7);
  for (const panel of data.panels) {
    // 本用例没有制造未知接收结果；缺时序必须保持缺失，不能伪造零值。
    if (panel.id === "action-unconfirmed") {
      expect(panel.status).toBe("unavailable");
      expect(panel.series).toEqual([]);
      continue;
    }
    expect(panel.status, `${panel.id}: ${panel.message}`).toBe("available");
    expect(
      panel.series.some((series) =>
        series.points.some(
          ([, value]) => value !== null && Number.isFinite(value),
        ),
      ),
    ).toBe(true);
    for (const series of panel.series) {
      expect(series.labels.instance).toBe(fixture.instance);
      expect(series.labels.job).toBe("linkd-delivery-e2e");
      expect(series.labels.linkd_task).toMatch(
        new RegExp(
          `^${kind}-(?:${kind === "action" ? "enqueue" : "producer"}|delivery)$`,
        ),
      );
      expect(series.labels).not.toHaveProperty("bk_tenant_id");
    }
  }
  const runners = data.panels.find((panel) => panel.id === kind + "-runners")!;
  expect(
    runners.series
      .filter((series) => series.labels.linkd_statistic === "运行")
      .every((series) => series.points.some(([, value]) => value === 1)),
  ).toBe(true);
}

// 真实 Console -> 真实临时 Prometheus -> 正式 Linkd exporter；不使用 route/fulfill 或合成时序。
test("real delivery task state and process metrics stay distinct from business completion", async ({
  page,
}, info) => {
  const errors: string[] = [],
    writes: string[] = [],
    queries: URL[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("response", (response) => {
    const u = new URL(response.url());
    if (u.pathname.includes("/local-api/") && response.status() >= 400)
      errors.push(`${response.status()} ${u.pathname}`);
  });
  page.on("request", (request) => {
    const u = new URL(request.url());
    if (u.pathname.includes("/local-api/") && request.method() !== "GET")
      writes.push(request.method() + " " + u.pathname);
    if (
      u.pathname.endsWith(endpoint("projection")) ||
      u.pathname.endsWith(endpoint("action"))
    )
      queries.push(u);
  });
  const controlResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname.endsWith(
        "/local-api/runtime/control-plane",
      ) && response.status() === 200,
  );
  await page.goto(fixture.base_url + "/control-plane");
  const control = controlPlaneRuntimeSchema.parse(
    await (await controlResponse).json(),
  );
  for (const phase of [
    "projection-producer",
    "projection-delivery",
    "action-enqueue",
    "action-delivery",
  ]) {
    const task = control.tasks.find((task) => task.id === phase);
    expect(task?.enabled).toBe(true);
    expect(task?.active).toBe(true);
    expect(task!.execution.succeeded).toBeGreaterThan(0);
  }

  await page.getByLabel("搜索任务").fill("KAC");
  for (const label of [
    "KAC 投影补扫",
    "KAC 投影投递",
    "KAC 动作入队",
    "KAC 动作投递",
  ])
    await expect(
      page.getByRole("button", { name: new RegExp(label) }),
    ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("delivery-control-plane.png"),
    fullPage: true,
  });

  for (const kind of ["projection", "action"] as const) {
    const subject = kind === "action" ? "动作" : "投影";
    const path = kind === "action" ? "action-deliveries" : "projection-tasks";
    const id =
      kind === "action" ? fixture.action_task : fixture.projection_task;
    await page.goto(
      fixture.base_url +
        "/" +
        path +
        "?" +
        new URLSearchParams({ bk_tenant_id: "delivery", id }),
    );
    await expect(
      page.getByRole("heading", {
        name: kind === "action" ? "动作受理确认" : "远端确认",
        exact: true,
      }),
    ).toBeVisible();
    const initial = await query(page, kind, () =>
      page.getByRole("button", { name: `查看${subject}运行观测` }).click(),
    );
    assertLive(initial, kind);
    const scoped = await query(page, kind, async () => {
      await page.getByLabel("进程 instance").fill(fixture.instance);
      await page.getByRole("button", { name: "应用进程筛选" }).click();
    });
    assertLive(scoped, kind);
    const ranged = await query(page, kind, () =>
      page.getByRole("button", { name: "15m", exact: true }).click(),
    );
    assertLive(ranged, kind);
    expect(Date.parse(ranged.to) - Date.parse(ranged.from)).toBe(900000);
    await expect(page.locator(".delivery-metrics canvas")).toHaveCount(7);
    // 等待 MetricChart 的 240ms 入场动画结束，再检查可分享截图。
    await page.waitForTimeout(300);
    await page.screenshot({
      path: info.outputPath(kind + "-live-dark.png"),
      fullPage: true,
    });
    await page
      .locator(".delivery-metrics article.panel")
      .filter({
        has: page.getByRole("heading", {
          name: "工作观察结果速率",
          exact: true,
        }),
      })
      .screenshot({ path: info.outputPath(kind + "-live-work.png") });
    const empty = await query(page, kind, async () => {
      await page.getByLabel("进程 instance").fill("missing-e2e-instance");
      await page.getByRole("button", { name: "应用进程筛选" }).click();
    });
    expect(
      empty.panels.every(
        (panel) => panel.status === "unavailable" && panel.series.length === 0,
      ),
    ).toBe(true);
    // 五秒缓存可能直接恢复此前查询；只有显式刷新才要求新网络响应。
    await page.getByLabel("进程 instance").fill(fixture.instance);
    await page.getByRole("button", { name: "应用进程筛选" }).click();
    assertLive(
      await query(page, kind, () =>
        page.getByRole("button", { name: `刷新${subject}指标` }).click(),
      ),
      kind,
    );
    await page
      .getByRole("button", { name: "切换为浅色模式", exact: true })
      .click();
    await page.setViewportSize({ width: 850, height: 1050 });
    await page.waitForTimeout(300);
    await page.screenshot({
      path: info.outputPath(kind + "-live-light.png"),
      fullPage: true,
    });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page
      .getByRole("button", { name: "切换为深色模式", exact: true })
      .click();
    await page.setViewportSize({ width: 1440, height: 1050 });
    await page.getByRole("button", { name: `收起${subject}运行观测` }).click();
    await expect(
      page.getByRole("region", { name: `${subject}运行观测` }),
    ).toHaveCount(0);
  }
  expect(queries.length).toBeGreaterThanOrEqual(10);
  expect(
    queries.every(
      (q) =>
        !q.searchParams.has("bk_tenant_id") && !q.searchParams.has("alert_id"),
    ),
  ).toBe(true);
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
});
