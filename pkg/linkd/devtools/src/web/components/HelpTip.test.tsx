import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { HelpLabel } from "./HelpTip";
import { MetricPanelCard } from "./MetricPanelCard";

afterEach(cleanup);

describe("HelpTip", () => {
  it("shows concise help on hover and keyboard focus", () => {
    render(<HelpLabel label="Lag" help="尚未被消费组处理的消息数。" />);

    const trigger = screen.getByLabelText("Lag说明");
    expect(screen.queryByRole("tooltip")).toBeNull();

    fireEvent.mouseEnter(trigger);
    expect(screen.getByRole("tooltip")).toHaveTextContent(
      "尚未被消费组处理的消息数。",
    );

    fireEvent.mouseLeave(trigger);
    expect(screen.queryByRole("tooltip")).toBeNull();

    fireEvent.focus(trigger);
    expect(screen.getByRole("tooltip")).toBeVisible();
  });

  it("keeps the metric unit on a separate visual row", () => {
    render(
      <MetricPanelCard
        panel={{
          id: "retry-rate",
          title: "重试速率",
          unit: "retry/s",
          kind: "line",
          status: "unavailable",
          series: [],
        }}
      />,
    );

    const title = screen.getByRole("heading", { name: "重试速率" });
    const unit = screen.getByText("retry/s");
    expect(title.parentElement).toHaveClass("metric-panel-title");
    expect(unit).toHaveClass("metric-panel-unit");
    expect(title.parentElement).not.toContainElement(unit);
  });
});
