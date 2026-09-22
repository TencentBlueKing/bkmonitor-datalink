import { expect, test, type Page } from "@playwright/test";

function record(id = "source-a", deleted = false) {
  return {
    id,
    revision: 1,
    published: 1,
    deleted,
    spec: {
      event_source_id: id,
      enabled: !deleted,
      cleaner: { type: "standard" },
      scheduling: {
        cleaner: {
          replicas: "all" as string | number,
          selector: { pool: "a" },
        },
        lifecycle: { replicas: 2, selector: {} },
      },
      enrich: { processors: [{ type: "fields", config: { rules: [] } }] },
      hooks: [
        {
          name: "output",
          type: "kafka",
          config: {
            brokers: ["kafka:9092"],
            topic: "out",
            security: {
              protocol: "sasl_plaintext",
              sasl: { username: "demo", password: "******" },
            },
          },
        },
      ],
      storage: {
        type: "kafka",
        kafka: {
          brokers: ["kafka:9092"],
          topic: `events-${id}`,
          consumer_group: "cleaner",
          security: {
            protocol: "sasl_plaintext",
            sasl: { password: "******" },
          },
        },
      },
    },
  };
}
async function fixture(
  page: Page,
  initial = [record(), record("source-b"), record("removed", true)],
) {
  const state = {
    records: initial,
    fail: false,
    conflict: false,
    writes: [] as Array<{
      id: string;
      method: string;
      body: Record<string, unknown>;
    }>,
  };
  await page.route("**/local-api/capabilities", (route) =>
    route.fulfill({
      json: {
        version: "test",
        metrics: { configured: false, source: "prometheus" },
        entities: {},
        storage: { elasticsearch: { configured: false } },
        limits: {
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 604800,
          defaultLimit: 50,
          maxLimit: 200,
        },
      },
    }),
  );
  await page.route("**/local-api/event-sources?**", (route) =>
    state.fail
      ? route.fulfill({
          status: 503,
          json: { error: { message: "来源连接失败" } },
        })
      : route.fulfill({ json: state.records }),
  );
  await page.route("**/local-api/event-sources/*", async (route) => {
    const id = decodeURIComponent(
      new URL(route.request().url()).pathname.split("/").at(-1)!,
    );
    const previous = state.records.find((s) => s.id === id);
    const method = route.request().method();
    if (method === "GET") return route.fulfill({ json: previous });
    const body = route.request().postDataJSON();
    state.writes.push({ id, method, body });
    if (state.conflict)
      return route.fulfill({
        status: 409,
        json: { error: { message: "配置版本已变化，请刷新后重试" } },
      });
    const updated = {
      id,
      revision: body.expected_revision + 1,
      published: body.expected_revision + 1,
      deleted: method === "DELETE",
      spec: body.spec ?? { ...previous!.spec, enabled: false },
    };
    state.records = [...state.records.filter((s) => s.id !== id), updated];
    return route.fulfill({ status: 202, json: updated });
  });
  await page.route("**/local-api/scheduling", (route) =>
    state.fail
      ? route.fulfill({
          status: 503,
          json: { error: { message: "调度连接失败" } },
        })
      : route.fulfill({
          json: {
            workers: {
              w1: {
                id: "w1",
                roles: ["cleaner", "lifecycle"],
                labels: { pool: "a", zone: "shanghai" },
                require_explicit_selector: true,
              },
            },
            tasks: {
              a: {
                id: "a",
                source: "source-a",
                role: "cleaner",
                worker: "worker-a",
                version: 1,
                phase: "running",
                partitions: ["events/0"],
              },
              b: {
                id: "b",
                source: "source-b",
                role: "lifecycle",
                worker: "worker-b",
                version: 1,
                phase: "starting",
              },
            },
            statuses: [
              {
                source: "source-a",
                role: "cleaner",
                matching: 8,
                target: 3,
                running: 3,
                reason: "limited by Kafka partitions",
                metadata: {
                  partitions: 3,
                  success: "2026-09-22T00:00:00Z",
                  error: "",
                },
              },
            ],
          },
        }),
  );
  return state;
}

test("edits forms and JSON, preserves credentials and isolates source runtime", async ({
  page,
}) => {
  const state = await fixture(page);
  await page.goto("/event-sources");
  await page.getByRole("button", { name: "查看 source-a" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("来源 ID", { exact: true })).toHaveAttribute(
    "readonly",
    "",
  );
  await dialog.getByLabel("Cleaner 副本数").fill("0");
  await dialog.getByRole("tab", { name: "高级 JSON" }).click();
  const editor = dialog.getByLabel("EventSource 配置");
  const spec = JSON.parse(await editor.inputValue());
  expect(spec.storage.kafka.security).toBeUndefined();
  expect(spec.hooks[0].config.security.sasl.password).toBe("******");
  expect(spec.enrich.processors).toHaveLength(1);
  await dialog.getByRole("tab", { name: "常用表单" }).click();
  await dialog.getByRole("button", { name: "保存并发布" }).click();
  await expect(dialog.getByRole("status")).toContainText("配置已发布 · 版本 2");
  expect(state.writes[0]).toMatchObject({
    id: "source-a",
    body: {
      expected_revision: 1,
      spec: { scheduling: { cleaner: { replicas: 0 } } },
    },
  });
  await dialog.getByRole("tab", { name: "运行情况" }).click();
  await expect(dialog.getByText("limited by Kafka partitions")).toBeVisible();
  await expect(dialog.getByText("worker-a", { exact: true })).toBeVisible();
  await expect(dialog.getByText("worker-b", { exact: true })).toHaveCount(0);
  await expect(dialog.getByText("未知", { exact: true }).first()).toBeVisible();
});

test("creates, deletes an invalid draft, and restores a source", async ({
  page,
}) => {
  const state = await fixture(page);
  await page.goto("/event-sources");
  await page.getByRole("button", { name: "新增来源", exact: true }).click();
  let dialog = page.getByRole("dialog");
  await dialog.getByLabel("来源 ID", { exact: true }).fill("new-source");
  await dialog.getByLabel("关联租户", { exact: true }).fill("tenant-a");
  await dialog.getByLabel("Brokers（每行一个）").fill("kafka:9092");
  await dialog.getByLabel("Topic", { exact: true }).fill("new-events");
  await dialog
    .getByLabel("Consumer group", { exact: true })
    .fill("new-cleaner");
  await dialog.getByRole("button", { name: "保存并发布" }).click();
  await expect(dialog.getByRole("status")).toContainText("版本 1");
  await dialog.getByRole("tab", { name: "高级 JSON" }).click();
  await dialog
    .getByLabel("EventSource 配置")
    .fill('{"event_source_id":"wrong",');
  await dialog.getByRole("tab", { name: "常用表单" }).click();
  await expect(dialog.getByRole("alert")).toContainText("JSON 语法错误");
  await dialog.getByRole("button", { name: "删除来源" }).click();
  await page
    .getByRole("alertdialog")
    .getByRole("button", { name: "确认删除", exact: true })
    .click();
  await expect(dialog.getByRole("status")).toContainText("来源已删除");
  expect(state.writes[1]).toEqual({
    id: "new-source",
    method: "DELETE",
    body: { expected_revision: 1 },
  });
  await dialog.getByRole("button", { name: "关闭来源详情" }).click();
  await page.getByLabel("来源状态").selectOption("deleted");
  await page.getByRole("button", { name: "查看 new-source" }).click();
  dialog = page.getByRole("dialog");
  await dialog.getByLabel("启用来源", { exact: true }).check();
  await dialog.getByRole("button", { name: "恢复并发布" }).click();
  await expect(dialog.getByRole("status")).toContainText("配置已发布 · 版本 3");
  expect(state.writes[2].method).toBe("PUT");
});

test("keeps drafts on conflicts and traps and restores keyboard focus", async ({
  page,
}) => {
  const state = await fixture(page);
  await page.goto("/event-sources");
  const trigger = page.getByRole("button", { name: "查看 source-a" });
  await trigger.click();
  const dialog = page.getByRole("dialog");
  const close = dialog.getByRole("button", { name: "关闭来源详情" });
  await expect(close).toBeFocused();
  await dialog.getByLabel("Cleaner 副本数").fill("0");
  await close.focus();
  await page.keyboard.press("Shift+Tab");
  await expect(
    dialog.getByRole("button", { name: "保存并发布" }),
  ).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(close).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("alertdialog")).toBeVisible();
  await page.getByRole("button", { name: "继续编辑" }).click();
  state.conflict = true;
  state.records[0].revision = 4;
  state.records[0].published = 4;
  await dialog.getByRole("button", { name: "保存并发布" }).click();
  await expect(dialog.getByRole("alert")).toContainText("草稿已保留");
  await expect(dialog.getByLabel("Cleaner 副本数")).toHaveValue("0");
  await dialog.getByRole("button", { name: "重新载入", exact: true }).click();
  await page
    .getByRole("alertdialog")
    .getByRole("button", { name: "放弃修改并载入" })
    .click();
  await expect(dialog.getByLabel("Cleaner 副本数")).toHaveValue("all");
  await close.click();
  await expect(trigger).toBeFocused();
  expect(state.writes).toHaveLength(1);
});

test("searches and paginates, shows Workers and retains stale data while paused", async ({
  page,
}) => {
  const state = await fixture(
    page,
    Array.from({ length: 25 }, (_, i) =>
      record(`source-${String(i).padStart(2, "0")}`),
    ),
  );
  await page.goto("/event-sources");
  await expect(
    page.getByRole("button", { name: "查看 source-00" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "查看 source-20" }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "下一页" }).click();
  await expect(
    page.getByRole("button", { name: "查看 source-20" }),
  ).toBeVisible();
  await page.getByLabel("搜索来源 ID / Topic").fill("events-source-03");
  await expect(
    page.getByRole("button", { name: "查看 source-03" }),
  ).toBeVisible();
  await page.getByRole("tab", { name: /Workers/ }).click();
  await expect(page.getByText("必须显式匹配")).toBeVisible();
  await page.getByText("2 个标签").click();
  await expect(page.getByText("zone=shanghai")).toBeVisible();
  await page.getByRole("tab", { name: /事件来源/ }).click();
  await page.getByRole("button", { name: "自动 5s" }).click();
  state.fail = true;
  await page.getByRole("button", { name: "立即刷新" }).click();
  await expect(page.getByText(/以下保留上次成功的快照/)).toBeVisible({
    timeout: 15000,
  });
  await expect(
    page.getByRole("button", { name: "查看 source-03" }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "已暂停" })).toHaveAttribute(
    "aria-pressed",
    "false",
  );
});

for (const width of [1280, 1440]) {
  test(`dark and light controls and drawers at ${width}px`, async ({
    page,
  }, info) => {
    await page.setViewportSize({ width, height: 960 });
    await fixture(page);
    await page.goto("/event-sources");
    await expect(
      page.getByRole("button", { name: "查看 source-a" }),
    ).toBeVisible();
    for (const theme of ["dark", "light"]) {
      if (theme === "light")
        await page.getByRole("button", { name: "切换为浅色模式" }).click();
      await page.screenshot({
        path: info.outputPath(`event-sources-${theme}-${width}.png`),
        fullPage: true,
      });
      await page.getByRole("button", { name: "查看 source-a" }).click();
      const dialog = page.getByRole("dialog");
      await expect(dialog).toBeVisible();
      await expect(
        dialog.getByRole("button", { name: "保存并发布" }),
      ).toBeDisabled();
      const dimensions = await dialog.evaluate((element) => ({
        client: element.clientWidth,
        scroll: element.scrollWidth,
        right: element.getBoundingClientRect().right,
      }));
      expect(dimensions.scroll).toBe(dimensions.client);
      expect(dimensions.right).toBeLessThanOrEqual(width);
      await page.screenshot({
        path: info.outputPath(`event-source-form-${theme}-${width}.png`),
      });
      await dialog.getByRole("tab", { name: "高级 JSON" }).click();
      await expect(dialog.getByLabel("EventSource 配置")).toHaveCSS(
        "background-color",
        theme === "dark" ? "rgb(9, 13, 19)" : "rgb(244, 247, 250)",
      );
      await expect(
        dialog.getByRole("button", { name: "格式化 JSON" }),
      ).toHaveCSS(
        "background-color",
        theme === "dark" ? "rgb(17, 27, 38)" : "rgb(238, 243, 247)",
      );
      await page.screenshot({
        path: info.outputPath(`event-source-json-${theme}-${width}.png`),
      });
      await dialog.getByRole("tab", { name: "高级 JSON" }).hover();
      await expect(dialog.getByRole("tab", { name: "高级 JSON" })).toHaveCSS(
        "color",
        theme === "dark" ? "rgb(111, 227, 193)" : "rgb(8, 123, 92)",
      );
      await dialog.getByRole("button", { name: "格式化 JSON" }).hover();
      await expect(
        dialog.getByRole("button", { name: "格式化 JSON" }),
      ).toHaveCSS(
        "color",
        theme === "dark" ? "rgb(220, 231, 242)" : "rgb(38, 54, 72)",
      );
      await dialog.getByRole("button", { name: "删除来源" }).hover();
      await expect(dialog.getByRole("button", { name: "删除来源" })).toHaveCSS(
        "color",
        theme === "dark" ? "rgb(255, 113, 143)" : "rgb(180, 35, 67)",
      );
      await dialog.getByRole("button", { name: "删除来源" }).click();
      await expect(page.getByRole("alertdialog")).toBeVisible();
      await page.screenshot({
        path: info.outputPath(`event-source-confirm-${theme}-${width}.png`),
      });
      await page.getByRole("button", { name: "继续编辑" }).click();
      await dialog.getByRole("button", { name: "关闭来源详情" }).click();
    }
  });
}
