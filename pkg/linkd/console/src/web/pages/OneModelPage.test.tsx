import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { OneModelPage } from "./OneModelPage";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
const item = (id: string) => ({
  bk_tenant_id: "t",
  model_id: "host",
  model_inst_id: id,
  entity_uid: `host|${id}`,
  display_name: `host-${id}`,
  attributes: { owner: "alice" },
});
const response = (items: unknown[], next_cursor?: string) =>
  new Response(JSON.stringify({ items, next_cursor, elapsed_milliseconds: 3 }));
function show() {
  return render(
    <MemoryRouter initialEntries={["/onemodel?bk_tenant_id=t&model_id=host"]}>
      <OneModelPage />
    </MemoryRouter>,
  );
}
it("pages with a fixed query, shows detail and switches to relations", async () => {
  const calls: Array<{ path: string; body: Record<string, unknown> }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as Record<string, unknown>;
      calls.push({ path, body });
      if (path.endsWith("close")) return new Response("{}");
      return response(
        [item(body.cursor ? "2" : "1")],
        body.cursor ? undefined : "cursor-1",
      );
    }),
  );
  show();
  fireEvent.change(screen.getByLabelText("属性名 1"), {
    target: { value: "enabled" },
  });
  fireEvent.change(screen.getByLabelText("属性类型 1"), {
    target: { value: "boolean" },
  });
  fireEvent.change(screen.getByLabelText("属性值 1"), {
    target: { value: "false" },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行查询" }));
  await screen.findByText("host-1");
  expect(calls[0].body.where).toEqual({
    field: "attributes.enabled",
    type: "boolean",
    operator: "eq",
    value: false,
  });
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByText("host-2");
  expect(calls[1].body.cursor).toBe("cursor-1");
  expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "2" }));
  expect(screen.getByRole("button", { name: "复制 JSON" })).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "查询关联" }));
  expect(screen.getByLabelText("起点实例 ID")).toHaveValue("2");
  fireEvent.change(screen.getByLabelText("目标模型"), {
    target: { value: "biz" },
  });
  fireEvent.change(screen.getByLabelText("关系"), {
    target: { value: "belongs" },
  });
  fireEvent.change(screen.getByLabelText("方向"), { target: { value: "in" } });
  fireEvent.click(screen.getByRole("button", { name: "执行查询" }));
  await waitFor(() => expect(calls.at(-1)?.path).toMatch(/related$/));
  expect(calls.at(-1)?.body).toMatchObject({
    bk_tenant_id: "t",
    roots: [{ model_id: "host", model_inst_id: "2" }],
    relation: "belongs",
    direction: "in",
    query: { model_id: "biz", limit: 1024 },
  });
});
it("releases a snapshot on editing and unmount and never applies stale results", async () => {
  let complete: ((r: Response) => void) | undefined;
  const fetcher = vi.fn((path: string) =>
    path.endsWith("close")
      ? Promise.resolve(new Response("{}"))
      : new Promise<Response>((resolve) => {
          complete = resolve;
        }),
  );
  vi.stubGlobal("fetch", fetcher);
  const view = show();
  fireEvent.click(screen.getByRole("button", { name: "执行查询" }));
  fireEvent.change(screen.getByLabelText("模型"), { target: { value: "biz" } });
  complete?.(response([item("old")], "old-cursor"));
  await waitFor(() =>
    expect(fetcher).toHaveBeenCalledWith(
      expect.stringMatching(/close$/),
      expect.objectContaining({
        body: JSON.stringify({ bk_tenant_id: "t", cursor: "old-cursor" }),
      }),
    ),
  );
  expect(screen.queryByText("host-old")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "执行查询" }));
  complete?.(response([item("new")], "new-cursor"));
  await screen.findByText("host-new");
  view.unmount();
  await waitFor(() =>
    expect(fetcher).toHaveBeenLastCalledWith(
      expect.stringMatching(/close$/),
      expect.objectContaining({
        body: JSON.stringify({ bk_tenant_id: "t", cursor: "new-cursor" }),
      }),
    ),
  );
});
it("validates JSON before sending and shows empty and expired results distinctly", async () => {
  const fetcher = vi.fn(async () => response([]));
  vi.stubGlobal("fetch", fetcher);
  show();
  fireEvent.click(screen.getByLabelText("高级 JSON"));
  fireEvent.change(screen.getByLabelText("过滤 JSON"), {
    target: { value: "{" },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行查询" }));
  expect(await screen.findByRole("alert")).toBeInTheDocument();
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("过滤 JSON"), {
    target: {
      value:
        '{"any":[{"field":"model_inst_id","type":"keyword","operator":"eq","value":"1"}]}',
    },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行查询" }));
  await screen.findByText("没有匹配的实例。");
  fetcher.mockResolvedValue(
    new Response(
      JSON.stringify({ error: { message: "查询快照已过期，请重新查询" } }),
      { status: 410 },
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "重新查询" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("快照已过期");
  expect(screen.queryByText("没有匹配的实例。")).not.toBeInTheDocument();
});

it("uses the configured deployment subpath for queries and snapshot cleanup", async () => {
  const base = document.createElement("base");
  base.href = "/apps/linkd/";
  document.head.append(base);
  const fetcher = vi.fn(async (path: string) =>
    path.endsWith("close")
      ? new Response("{}")
      : response([item("1")], "cursor"),
  );
  vi.stubGlobal("fetch", fetcher);
  try {
    const view = show();
    fireEvent.click(screen.getByRole("button", { name: "执行查询" }));
    await screen.findByText("host-1");
    expect(fetcher).toHaveBeenCalledWith(
      "/apps/linkd/local-api/onemodel/search",
      expect.anything(),
    );
    fireEvent.change(screen.getByLabelText("模型"), {
      target: { value: "biz" },
    });
    await waitFor(() =>
      expect(fetcher).toHaveBeenCalledWith(
        "/apps/linkd/local-api/onemodel/close",
        expect.anything(),
      ),
    );
    view.unmount();
  } finally {
    base.remove();
  }
});
