import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { TimeContext } from "../time";
import { AlertPolicyState } from "./AlertPolicyState";

afterEach(cleanup);
const at = "2026-10-05T01:02:03Z";
function setup(payload: Record<string, unknown>) {
  render(
    <TimeContext.Provider value="utc">
      <AlertPolicyState payload={payload} />
    </TimeContext.Provider>,
  );
}
it("shows blocking policies and historical admission", () => {
  setup({
    status: "active",
    revision: 8,
    shield: {
      active: true,
      next_check_at: at,
      bindings: [{ policy: { id: "maint" } }],
    },
    merge: {
      role: "original",
      state: "pending",
      pending: [
        {
          window_id: "window-a",
          policy: { id: "merge-a", version: 2 },
          deadline: at,
        },
      ],
      relation_ids: ["parent-relation"],
    },
    admission: { admitted_at: at, severity: "warning" },
  });
  expect(screen.getByText("屏蔽中")).toBeInTheDocument();
  expect(screen.getByText("等待合并裁决")).toBeInTheDocument();
  expect(screen.getByText("放行级别：warning")).toBeInTheDocument();
  expect(screen.getByText(/最近放行仅记录历史动作/)).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /重试|解除|放行|关闭告警/ }),
  ).not.toBeInTheDocument();
});
it("shows no immediate action after relation release", () => {
  setup({
    status: "recovered",
    revision: 9,
    shield: { active: false },
    merge: { role: "original", state: "released" },
    admission: {},
  });
  expect(screen.getByText("未屏蔽")).toBeInTheDocument();
  expect(screen.getByText("已释放")).toBeInTheDocument();
  expect(screen.getByText("尚无放行记录")).toBeInTheDocument();
});
it("shows absent business state explicitly", () => {
  setup({});
  expect(screen.getByText("未提供有效版本")).toBeVisible();
  expect(screen.getByText("未提供屏蔽状态")).toBeVisible();
});
