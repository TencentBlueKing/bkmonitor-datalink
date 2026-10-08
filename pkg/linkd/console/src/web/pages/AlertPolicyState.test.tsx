import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { MemoryRouter } from "react-router-dom";
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
it("separates active lifecycle, blocking policies, historical admission and per-target watermarks", () => {
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
    projection: {
      targets: {
        kac: {
          source_version: 3,
          required_revision: 8,
          synced_revision: 7,
          synced_at: at,
        },
        archive: {
          source_version: 3,
          required_revision: 8,
          synced_revision: 8,
          synced_at: at,
        },
      },
    },
  });
  expect(screen.getByText("屏蔽中")).toBeInTheDocument();
  expect(screen.getByText("等待合并裁决")).toBeInTheDocument();
  expect(screen.getByText("放行级别：warning")).toBeInTheDocument();
  expect(screen.getByText(/最近放行仅记录历史动作/)).toBeInTheDocument();
  const table = screen.getByRole("table", { name: "投影目标水位" });
  expect(within(table).getByText("kac").closest("tr")).toHaveTextContent(
    "待同步",
  );
  expect(within(table).getByText("archive").closest("tr")).toHaveTextContent(
    "已确认",
  );
  expect(
    screen.queryByRole("button", { name: /重试|解除|放行|关闭告警/ }),
  ).not.toBeInTheDocument();
});
it("retains terminal projection pending and shows no immediate action after relation release", () => {
  setup({
    status: "recovered",
    revision: 9,
    shield: { active: false },
    merge: { role: "original", state: "released" },
    admission: {},
    projection: {
      targets: {
        kac: { source_version: 3, required_revision: 9, synced_revision: 0 },
      },
    },
  });
  expect(screen.getByText("未屏蔽")).toBeInTheDocument();
  expect(screen.getByText("已释放")).toBeInTheDocument();
  expect(screen.getByText("尚无放行记录")).toBeInTheDocument();
  expect(screen.getByText("待同步")).toBeInTheDocument();
});
it("distinguishes absent data, no target and invalid or future watermarks", () => {
  const { rerender } = render(<AlertPolicyState payload={{}} />);
  expect(screen.getByText("未提供有效版本")).toBeInTheDocument();
  expect(screen.getByText("未提供投影水位")).toBeInTheDocument();
  expect(screen.queryByText("已确认")).not.toBeInTheDocument();
  rerender(<AlertPolicyState payload={{ projection: {} }} />);
  expect(screen.getByText("未绑定投影目标")).toBeInTheDocument();
  rerender(
    <AlertPolicyState
      payload={{
        revision: 1,
        projection: {
          targets: {
            future: {
              source_version: 1,
              required_revision: 1,
              synced_revision: 2,
              synced_at: at,
            },
            absent: { source_version: 1, required_revision: 1 },
            invalid_time: {
              source_version: 1,
              required_revision: 1,
              synced_revision: 1,
              synced_at: "bad",
            },
          },
        },
      }}
    />,
  );
  expect(screen.getAllByText("水位异常")).toHaveLength(3);
  expect(screen.queryByText("已确认")).not.toBeInTheDocument();
});

it("shows pending action enqueue independently and links only the current tenant", () => {
  render(
    <MemoryRouter>
      <AlertPolicyState
        payload={{
          bk_tenant_id: "tenant-a",
          alert_id: "alert-a",
          revision: 2,
          action_pending: {
            revision: 2,
            action: "firing",
            cause_type: "source_event",
            cause_id: "original",
            targets: { kac: 3 },
          },
          projection: {
            targets: {
              kac: {
                source_version: 3,
                action_enabled: true,
                required_revision: 2,
                synced_revision: 0,
              },
            },
          },
        }}
      />
    </MemoryRouter>,
  );
  expect(screen.getByText(/原获准动作尚待确认全部目标入队/)).toBeVisible();
  expect(
    screen.getByRole("link", { name: "查看此告警的动作投递" }),
  ).toHaveAttribute(
    "href",
    "/action-deliveries?bk_tenant_id=tenant-a&alert_id=alert-a",
  );
  expect(screen.getByText("启用")).toBeVisible();
});
