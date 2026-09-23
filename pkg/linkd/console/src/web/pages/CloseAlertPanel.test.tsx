import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { alertFixture } from "../../../tests/fixtures/explorer";
import { CloseAlertPanel } from "./CloseAlertPanel";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  sessionStorage.clear();
});
it("preserves one close command after an uncertain response and reopening, including after state became terminal", async () => {
  const onClosed = vi.fn();
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(
      Response.json({ error: { message: "结果尚未确认" } }, { status: 502 }),
    )
    .mockResolvedValueOnce(
      Response.json({ alert: { ...alertFixture.payload, status: "closed" } }),
    );
  vi.stubGlobal("fetch", fetcher);
  const first = render(
    <CloseAlertPanel item={alertFixture} onClosed={onClosed} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "主动关闭" }));
  fireEvent.change(screen.getByLabelText("关闭原因"), {
    target: { value: "问题已确认" },
  });
  fireEvent.click(screen.getByRole("button", { name: "确认关闭告警" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("结果尚未确认");
  first.unmount();
  render(
    <CloseAlertPanel
      item={{
        ...alertFixture,
        payload: { ...alertFixture.payload, status: "closed" },
      }}
      onClosed={onClosed}
    />,
  );
  expect(screen.getByLabelText("关闭原因")).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "重试同一关闭操作" }));
  expect(await screen.findByRole("status")).toHaveTextContent("告警已关闭");
  expect(fetcher.mock.calls[0][1].body).toBe(fetcher.mock.calls[1][1].body);
  expect(onClosed).toHaveBeenCalledOnce();
  expect(sessionStorage.length).toBe(0);
});
it("rejects oversized UTF-8 reasons without sending or offers no new close for terminal alerts", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const view = render(
    <CloseAlertPanel item={alertFixture} onClosed={vi.fn()} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "主动关闭" }));
  fireEvent.change(screen.getByLabelText("关闭原因"), {
    target: { value: "中".repeat(86) },
  });
  fireEvent.click(screen.getByRole("button", { name: "确认关闭告警" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("最多 256 字节");
  expect(fetcher).not.toHaveBeenCalled();
  view.unmount();
  render(
    <CloseAlertPanel
      item={{ ...alertFixture, payload: { status: "recovered" } }}
      onClosed={vi.fn()}
    />,
  );
  expect(
    screen.queryByRole("button", { name: "主动关闭" }),
  ).not.toBeInTheDocument();
});
