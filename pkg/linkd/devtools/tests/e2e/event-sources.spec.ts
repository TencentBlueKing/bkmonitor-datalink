import { expect, test } from "@playwright/test";

test("edits replicas through the API and displays the Kafka scheduling cap", async ({
  page,
}) => {
  await page.route("**/local-api/capabilities", (route) =>
    route.fulfill({
      json: {
        version: "test",
        metrics: { configured: false, source: "prometheus" },
        entities: {
          events: { source: "mysql", filters: [] },
          alerts: { source: "mysql", filters: [] },
          "alert-logs": { source: "mysql", filters: [] },
        },
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
  const spec = {
    event_source_id: "source-a",
    enabled: true,
    cleaner: { type: "standard" },
    scheduling: {
      cleaner: { replicas: "all", selector: { pool: "a" } },
      lifecycle: { replicas: 2, selector: {} },
    },
    storage: {
      type: "kafka",
      kafka: {
        brokers: ["kafka:9092"],
        topic: "events",
        consumer_group: "cleaner",
        security: {
          protocol: "sasl_plaintext",
          sasl: { password: "[redacted]" },
        },
      },
    },
  };
  let revision = 1;
  let saved: unknown;
  await page.route("**/local-api/event-sources?**", (route) =>
    route.fulfill({
      json: [
        { id: "source-a", revision, published: revision, deleted: false, spec },
      ],
    }),
  );
  await page.route("**/local-api/event-sources/source-a", async (route) => {
    saved = route.request().postDataJSON();
    revision++;
    await route.fulfill({
      status: 202,
      json: {
        id: "source-a",
        revision,
        published: revision,
        deleted: false,
        spec,
      },
    });
  });
  await page.route("**/local-api/scheduling", (route) =>
    route.fulfill({
      json: {
        workers: {
          w1: {
            id: "w1",
            roles: ["cleaner", "lifecycle"],
            labels: { pool: "a" },
            require_explicit_selector: true,
          },
        },
        tasks: {
          task1: {
            id: "task1",
            source: "source-a",
            role: "cleaner",
            worker: "w1",
            version: 1,
            phase: "running",
            partitions: ["events/0"],
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
              success: "2026-09-07T00:00:00Z",
              error: "",
            },
          },
        ],
      },
    }),
  );
  await page.goto("/event-sources");
  await expect(
    page.getByRole("heading", { name: "事件来源与任务调度" }),
  ).toBeVisible();
  await expect(page.getByText("limited by Kafka partitions")).toBeVisible();
  await page.getByRole("button", { name: "编辑", exact: true }).click();
  const editor = page.getByLabel("EventSource 配置");
  const value = JSON.parse(await editor.inputValue());
  expect(value.storage.kafka.security).toBeUndefined();
  value.scheduling.cleaner.replicas = 0;
  await editor.fill(JSON.stringify(value));
  await page.getByRole("button", { name: "保存并发布" }).click();
  await expect
    .poll(() => saved)
    .toMatchObject({
      expected_revision: 1,
      spec: { scheduling: { cleaner: { replicas: 0 } } },
    });
  await expect(editor).not.toContainText("[redacted]");
});
