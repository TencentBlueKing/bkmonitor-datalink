import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RefreshControls } from "./RefreshControls";

afterEach(cleanup);

describe("RefreshControls", () => {
  it("uses one interaction model for manual and automatic refresh", () => {
    const onRefresh = vi.fn();
    const onToggleAutoRefresh = vi.fn();
    const { rerender } = render(
      <RefreshControls
        status="available"
        lastSuccessfulAt={Date.parse("2026-09-03T00:00:00.000Z")}
        isFetching={false}
        autoRefresh
        intervalSeconds={15}
        onRefresh={onRefresh}
        onToggleAutoRefresh={onToggleAutoRefresh}
      />,
    );

    expect(screen.getByText(/最后成功：/)).not.toHaveTextContent("尚未成功");
    const automatic = screen.getByRole("button", { name: "自动 15s" });
    expect(automatic).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(screen.getByRole("button", { name: "立即刷新" }));
    fireEvent.click(automatic);
    expect(onRefresh).toHaveBeenCalledOnce();
    expect(onToggleAutoRefresh).toHaveBeenCalledOnce();

    rerender(
      <RefreshControls
        isFetching
        autoRefresh={false}
        intervalSeconds={15}
        onRefresh={onRefresh}
        onToggleAutoRefresh={onToggleAutoRefresh}
      />,
    );
    expect(screen.getByRole("button", { name: "刷新中…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "已暂停" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });
});
