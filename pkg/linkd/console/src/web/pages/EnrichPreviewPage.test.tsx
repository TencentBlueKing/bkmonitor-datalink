import {
  cleanup,
  render,
  screen,
  fireEvent,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { EnrichPreviewPage } from "./EnrichPreviewPage";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("previews JSON and ID without saving and loads config without connections", async () => {
  const calls: Array<{ path: string; body?: Record<string, unknown> }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      const body = init?.body
        ? (JSON.parse(String(init.body)) as Record<string, unknown>)
        : undefined;
      calls.push({ path, body });
      return new Response(
        JSON.stringify(
          path.includes("/config/")
            ? { enrich: { processors: [] } }
            : {
                event_source_version: 7,
                config_digest: "abcdef",
                enrich_status: "succeeded",
                original: { title: "raw" },
                effective_alert: { title: "new" },
                enrich: { processors: [] },
                changes: [{ path: "$.title", before: "raw", after: "new" }],
                previous_changes: [],
                trace: [],
              },
        ),
      );
    }),
  );
  render(
    <MemoryRouter
      initialEntries={["/enrich-preview?bk_tenant_id=t&event_source_id=host"]}
    >
      <EnrichPreviewPage />
    </MemoryRouter>,
  );
  fireEvent.click(screen.getByRole("button", { name: "执行预览" }));
  await screen.findByText("执行结果 · succeeded");
  expect(calls[0].body?.input).toHaveProperty("alert");
  expect(calls[0].body).not.toHaveProperty("enrich");
  fireEvent.change(screen.getByLabelText("输入方式"), {
    target: { value: "id" },
  });
  fireEvent.change(screen.getByLabelText("Alert ID"), {
    target: { value: "a" },
  });
  fireEvent.click(screen.getByRole("button", { name: "加载已发布配置" }));
  await waitFor(() =>
    expect(screen.getByLabelText("丰富配置")).toHaveValue(
      '{\n  "processors": []\n}',
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "执行预览" }));
  await waitFor(() => expect(calls).toHaveLength(3));
  expect(calls[2].body?.input).toEqual({ alert_id: "a" });
  expect(calls[2].body?.enrich).toEqual({ processors: [] });
  expect(calls.every((c) => c.path.includes("/enrich/"))).toBe(true);
});
it("shows malformed JSON locally", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  render(
    <MemoryRouter initialEntries={["/?bk_tenant_id=t&event_source_id=host"]}>
      <EnrichPreviewPage />
    </MemoryRouter>,
  );
  fireEvent.change(screen.getByLabelText("Alert JSON"), {
    target: { value: "{" },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行预览" }));
  expect(await screen.findByRole("alert")).toBeInTheDocument();
  expect(fetcher).not.toHaveBeenCalled();
});
