import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { EventSourcesPage } from "./EventSourcesPage";
import { editableSpec, type SourceRecord } from "./event-sources";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function source(id = "source-a", deleted = false): SourceRecord {
  return {
    id,
    revision: 1,
    published: 1,
    deleted,
    spec: {
      ...editableSpec(),
      event_source_id: id,
      storage: {
        type: "kafka",
        kafka: {
          brokers: ["kafka:9092"],
          topic: `events-${id}`,
          consumer_group: "cleaner",
          security: { sasl: { password: "******" } },
        },
      },
      enrich: { processors: [{ type: "fields", config: { rules: [] } }] },
    },
  };
}
const scheduling = {
  workers: {
    w1: {
      id: "w1",
      roles: ["cleaner"],
      labels: { pool: "a" },
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
      role: "cleaner",
      worker: "worker-b",
      version: 1,
      phase: "running",
    },
  },
  statuses: [
    {
      source: "source-a",
      role: "cleaner",
      matching: 8,
      target: 3,
      running: 2,
      reason: "limited by Kafka partitions",
      metadata: { partitions: 3, success: "2026-09-22T00:00:00Z", error: "" },
    },
  ],
};
function setup(initial = [source()]) {
  let records = initial;
  let conflict = false;
  let unavailable = false;
  let unavailableRecords = false;
  let writeGate = Promise.resolve();
  const writes: Array<{
    path: string;
    method: string;
    body: { expected_revision: number; spec?: SourceRecord["spec"] };
  }> = [];
  const json = (value: unknown, status = 200) =>
    new Response(JSON.stringify(value), { status });
  const fetcher = vi.fn(async (input: string, init?: RequestInit) => {
    const path = String(input);
    if (path.includes("/scheduling"))
      return unavailable
        ? json({ error: { message: "暂不可用" } }, 503)
        : json(scheduling);
    if (path.includes("?"))
      return unavailableRecords
        ? json({ error: { message: "读取失败" } }, 503)
        : json(records);
    const id = decodeURIComponent(path.split("/").at(-1)!);
    if (!init?.method || init.method === "GET")
      return json(records.find((s) => s.id === id));
    const body = JSON.parse(String(init.body));
    writes.push({ path, method: init.method, body });
    await writeGate;
    if (conflict)
      return json({ error: { message: "配置版本已变化，请刷新后重试" } }, 409);
    const previous = records.find((s) => s.id === id);
    const record = {
      id,
      revision: body.expected_revision + 1,
      published: body.expected_revision + 1,
      deleted: init.method === "DELETE",
      spec: body.spec ?? previous!.spec,
    };
    records = [...records.filter((s) => s.id !== id), record];
    return json(record, 202);
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  render(
    <QueryClientProvider client={client}>
      <EventSourcesPage />
    </QueryClientProvider>,
  );
  return {
    writes,
    pauseWrites: () => {
      let resume!: () => void;
      writeGate = new Promise<void>((resolve) => {
        resume = resolve;
      });
      return resume;
    },
    client,
    fetcher,
    setRecords: (value: SourceRecord[]) => {
      records = value;
    },
    conflict: () => {
      conflict = true;
    },
    unavailable: () => {
      unavailable = true;
    },
    unavailableRecords: () => {
      unavailableRecords = true;
    },
  };
}
async function open(id = "source-a") {
  fireEvent.click(await screen.findByRole("button", { name: `查看 ${id}` }));
  return screen.getByRole("dialog");
}

it("publishes form changes without losing advanced fields or submitting input Kafka masks", async () => {
  const { writes } = setup();
  await open();
  expect(screen.getByLabelText("来源 ID")).toHaveAttribute("readonly");
  fireEvent.change(screen.getByLabelText("Cleaner 副本数"), {
    target: { value: "0" },
  });
  fireEvent.click(screen.getByRole("tab", { name: "高级 JSON" }));
  const json = JSON.parse(
    (screen.getByLabelText("EventSource 配置") as HTMLTextAreaElement).value,
  );
  expect(json.enrich.processors).toHaveLength(1);
  fireEvent.click(screen.getByRole("tab", { name: "常用表单" }));
  fireEvent.click(screen.getByRole("button", { name: "保存并发布" }));
  await screen.findByText(/配置已发布 · 版本 2/);
  expect(writes).toHaveLength(1);
  expect(writes[0].body.expected_revision).toBe(1);
  expect(writes[0].body.spec?.scheduling.cleaner.replicas).toBe(0);
  expect(writes[0].body.spec?.storage.kafka.security).toBeUndefined();
  expect(writes[0].body.spec?.enrich).toEqual(json.enrich);
});

it("retains invalid JSON and deletes the selected source without parsing the draft", async () => {
  const { writes } = setup();
  await open();
  fireEvent.click(screen.getByRole("tab", { name: "高级 JSON" }));
  fireEvent.change(screen.getByLabelText("EventSource 配置"), {
    target: { value: '{"event_source_id":"other",' },
  });
  fireEvent.click(screen.getByRole("tab", { name: "常用表单" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("JSON 语法错误");
  expect(screen.getByLabelText("EventSource 配置")).toHaveValue(
    '{"event_source_id":"other",',
  );
  fireEvent.click(screen.getByRole("button", { name: "保存并发布" }));
  expect(writes).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "删除来源" }));
  fireEvent.click(screen.getByRole("button", { name: "确认删除" }));
  await screen.findByText(/来源已删除 · 版本 2/);
  expect(writes).toEqual([
    {
      path: "/local-api/event-sources/source-a",
      method: "DELETE",
      body: { expected_revision: 1 },
    },
  ]);
  expect(
    screen.getByRole("button", { name: "恢复并发布" }),
  ).toBeInTheDocument();
});

it("keeps the draft and old revision on refresh and conflicts until an explicit reload", async () => {
  const api = setup();
  await open();
  fireEvent.change(screen.getByLabelText("Cleaner 副本数"), {
    target: { value: "0" },
  });
  api.setRecords([{ ...source(), revision: 4, published: 4 }]);
  await api.client.invalidateQueries({ queryKey: ["event-sources"] });
  await screen.findByText(/来源已有新版本/);
  expect(screen.getByLabelText("Cleaner 副本数")).toHaveValue("0");
  api.conflict();
  fireEvent.click(screen.getByRole("button", { name: "保存并发布" }));
  await screen.findByText(/草稿已保留/);
  expect(api.writes[0].body.expected_revision).toBe(1);
  expect(screen.getByLabelText("Cleaner 副本数")).toHaveValue("0");
  fireEvent.click(screen.getByRole("button", { name: "重新载入" }));
  expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "放弃修改并载入" }));
  await screen.findByText("已载入最新配置");
  expect(screen.getByLabelText("Cleaner 副本数")).toHaveValue("all");
  expect(screen.getByText(/编辑版本 4 \/ 发布版本 4/)).toBeInTheDocument();
  expect(api.writes).toHaveLength(1);
});

it("confirms abandoning changes and restores focus when the drawer closes", async () => {
  setup();
  const trigger = await screen.findByRole("button", { name: "查看 source-a" });
  trigger.focus();
  fireEvent.click(trigger);
  fireEvent.change(screen.getByLabelText("Cleaner 副本数"), {
    target: { value: "2" },
  });
  fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
  expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "继续编辑" }));
  expect(screen.getByLabelText("Cleaner 副本数")).toHaveValue("2");
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  fireEvent.click(screen.getByRole("button", { name: "放弃修改" }));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
});

it("isolates runtime by source and distinguishes missing data from zero", async () => {
  setup([source(), source("source-b")]);
  const dialog = await open();
  fireEvent.click(screen.getByRole("tab", { name: "运行情况" }));
  expect(within(dialog).getByText("worker-a")).toBeInTheDocument();
  expect(within(dialog).queryByText("worker-b")).not.toBeInTheDocument();
  expect(
    within(dialog).getByText("limited by Kafka partitions"),
  ).toBeInTheDocument();
  expect(within(dialog).getAllByText("未知").length).toBeGreaterThan(0);
});

it("filters, sorts, paginates and restores deleted records", async () => {
  const api = setup([
    ...Array.from({ length: 22 }, (_, i) =>
      source(`s-${String(21 - i).padStart(2, "0")}`),
    ),
    source("deleted", true),
  ]);
  await screen.findByRole("button", { name: "查看 s-00" });
  expect(
    screen.queryByRole("button", { name: "查看 s-20" }),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  expect(screen.getByRole("button", { name: "查看 s-20" })).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("搜索来源 ID / Topic"), {
    target: { value: "events-s-03" },
  });
  expect(screen.getByRole("button", { name: "查看 s-03" })).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
  fireEvent.change(screen.getByLabelText("来源状态"), {
    target: { value: "deleted" },
  });
  await open("deleted");
  fireEvent.click(screen.getByRole("button", { name: "恢复并发布" }));
  await screen.findByText(/配置已发布 · 版本 2/);
  expect(api.writes[0]).toMatchObject({
    method: "PUT",
    body: { expected_revision: 1, spec: { event_source_id: "deleted" } },
  });
});

it("keeps a stale snapshot after a refresh failure and allows manual refresh while paused", async () => {
  const api = setup();
  await screen.findByRole("button", { name: "查看 source-a" });
  api.unavailable();
  api.unavailableRecords();
  fireEvent.click(screen.getByRole("button", { name: "自动 5s" }));
  fireEvent.click(screen.getByRole("button", { name: "立即刷新" }));
  await waitFor(() => expect(screen.getAllByRole("alert")).toHaveLength(2));
  expect(
    screen.getByRole("button", { name: "查看 source-a" }),
  ).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "已暂停" })).toHaveAttribute(
    "aria-pressed",
    "false",
  );
  expect(screen.getByText(/以下保留上次成功的快照/)).toBeInTheDocument();
});

it("blocks duplicate submissions and closing while a write is in flight", async () => {
  const api = setup();
  await open();
  const resume = api.pauseWrites();
  fireEvent.change(screen.getByLabelText("Cleaner 副本数"), {
    target: { value: "2" },
  });
  const save = screen.getByRole("button", { name: "保存并发布" });
  fireEvent.click(save);
  fireEvent.click(save);
  expect(screen.getByRole("button", { name: "处理中…" })).toBeDisabled();
  expect(screen.getByLabelText("Cleaner 副本数")).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "关闭来源详情" }));
  expect(screen.getByRole("dialog")).toBeInTheDocument();
  expect(api.writes).toHaveLength(1);
  resume();
  await screen.findByText(/配置已发布 · 版本 2/);
});

it("distinguishes initial loading, no sources and no search results", async () => {
  setup([]);
  expect(screen.getAllByText("正在加载来源…").length).toBeGreaterThan(0);
  await screen.findByText("暂无事件来源，点击新增来源开始配置");
  cleanup();
  setup();
  await screen.findByRole("button", { name: "查看 source-a" });
  fireEvent.change(screen.getByLabelText("搜索来源 ID / Topic"), {
    target: { value: "missing" },
  });
  expect(screen.getByText("没有符合条件的来源")).toBeInTheDocument();
});

it("shows unavailable runtime as unknown after an initial failure and can retry", async () => {
  let failed = true;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async (path: string) =>
        new Response(
          JSON.stringify(
            failed
              ? { error: { message: "测试连接失败" } }
              : String(path).includes("scheduling")
                ? { workers: {}, tasks: {}, statuses: null }
                : [source()],
          ),
          { status: failed ? 503 : 200 },
        ),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  render(
    <QueryClientProvider client={client}>
      <EventSourcesPage />
    </QueryClientProvider>,
  );
  await screen.findByText("无法加载来源，请点击立即刷新重试");
  expect(screen.getAllByRole("alert")).toHaveLength(2);
  failed = false;
  fireEvent.click(screen.getByRole("button", { name: "立即刷新" }));
  const dialog = await open();
  fireEvent.click(screen.getByRole("tab", { name: "运行情况" }));
  expect(within(dialog).getByText("该来源暂无任务")).toBeInTheDocument();
  expect(within(dialog).getAllByText("未知").length).toBeGreaterThan(0);
});
