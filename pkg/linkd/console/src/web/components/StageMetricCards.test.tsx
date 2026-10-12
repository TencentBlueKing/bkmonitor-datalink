import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { StageMetricCards } from "./StageMetricCards";
afterEach(cleanup);
it("labels rolling average with its actual calculation window", () => {
  const { rerender } = render(
    <StageMetricCards
      panels={[]}
      stage="lifecycle"
      stageLabel="Lifecycle"
      calculationWindowSeconds={60}
    />,
  );
  expect(screen.getByText("近 1 分钟平均总耗时")).toBeVisible();
  expect(screen.getByText(/下方分布按完整所选范围统计/)).toBeVisible();
  rerender(
    <StageMetricCards
      panels={[]}
      stage="lifecycle"
      stageLabel="Lifecycle"
      calculationWindowSeconds={30}
    />,
  );
  expect(screen.getByText("近 30 秒平均总耗时")).toBeVisible();
});
