import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  defaultMetricCalculationWindowSeconds,
  defaultMetricRangeSeconds,
  metricStep,
} from "../metricRange";
import { MetricQueryControls } from "./MetricQueryControls";

afterEach(cleanup);

describe("MetricQueryControls", () => {
  it("changes calculation window and chart range independently", () => {
    const onRangeChange = vi.fn();
    const onCalculationWindowChange = vi.fn();
    render(
      <MetricQueryControls
        rangeSeconds={defaultMetricRangeSeconds}
        calculationWindowSeconds={defaultMetricCalculationWindowSeconds}
        onRangeChange={onRangeChange}
        onCalculationWindowChange={onCalculationWindowChange}
      />,
    );

    expect(screen.queryByRole("button", { name: "1m" })).toBeNull();
    expect(screen.getByRole("button", { name: "1h" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("combobox", { name: "指标计算窗口" })).toHaveValue(
      "60",
    );

    fireEvent.change(screen.getByRole("combobox", { name: "指标计算窗口" }), {
      target: { value: "300" },
    });
    fireEvent.click(screen.getByRole("button", { name: "15m" }));

    expect(onCalculationWindowChange).toHaveBeenCalledWith(300);
    expect(onRangeChange).toHaveBeenCalledWith(900);
  });

  it("bounds short-range sampling while limiting long-range points", () => {
    expect(metricStep(900)).toBe(15);
    expect(metricStep(604800)).toBe(2520);
  });
});
