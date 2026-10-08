import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it } from "vitest";
import { suppressionFixture } from "../../test-fixtures/suppression";
import { TimeContext } from "../time";
import { EventSuppressionState } from "./EventSuppressionState";

afterEach(cleanup);
function setup(suppression?: unknown) {
  return render(
    <MemoryRouter>
      <TimeContext.Provider value="utc">
        <EventSuppressionState
          tenant="tenant &/a"
          payload={{ _processing: { policy_decision: { suppression } } }}
        />
      </TimeContext.Provider>
    </MemoryRouter>,
  );
}
it("shows frozen counts, window and tenant-scoped links to exact policy releases", () => {
  setup(suppressionFixture);
  expect(screen.getByText(/不是当前 Redis 状态/)).toBeInTheDocument();
  expect(screen.getByText(/2 \/ 3 次/)).toBeInTheDocument();
  const link = screen.getByRole("link", { name: "clip-policy / 2 ↗" });
  const url = new URL(link.getAttribute("href")!, "http://localhost");
  expect(Object.fromEntries(url.searchParams)).toEqual({
    bk_tenant_id: "tenant &/a",
    type: "suppression",
    id: "clip-policy",
    version: "2",
  });
  const group = screen.getByRole("table", { name: "抑制步骤 warning" });
  expect(within(group).getByText(/固定截止/)).toBeInTheDocument();
  const main = within(group).getByRole("link", { name: "main-alert ↗" });
  expect(
    new URL(main.getAttribute("href")!, "http://localhost").searchParams.get(
      "bk_tenant_id",
    ),
  ).toBe("tenant &/a");
});
it("explains active-Alert bypass without showing synthetic counts", () => {
  setup({ bypass_reason: "active_alert", active_alert_id: "active-id" });
  expect(screen.getByText(/绕过防抖和关联聚合/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "active-id ↗" })).toBeInTheDocument();
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
});
it("keeps absent data and invalid results distinct from passing the threshold", () => {
  setup({
    evaluations: [
      {
        severity: "warning",
        steps: [
          {
            policy: {},
            scheme: "clip",
            outcome: "new-value",
            count: 0,
            threshold: 3,
          },
        ],
      },
    ],
  });
  expect(screen.getByText("warning · 结果未知")).toBeInTheDocument();
  expect(screen.getByText("未知结果：new-value")).toBeInTheDocument();
  expect(screen.getByText("未提供有效计数")).toBeInTheDocument();
  expect(screen.queryByText("通过门槛")).not.toBeInTheDocument();
  cleanup();
  setup();
  expect(screen.getByText("尚无已保存的抑制裁决。")).toBeInTheDocument();
});
it("does not describe reserved aggregation candidates as admitted owners", () => {
  const candidate = structuredClone(suppressionFixture.evaluations[1]);
  candidate.suppressed = false;
  candidate.reason_code = "";
  candidate.related_alert_id = "";
  candidate.steps[0].outcome = "reserved";
  setup({ evaluations: [candidate] });
  expect(screen.getByText("warning · 未被抑制")).toBeInTheDocument();
  expect(screen.getByText("候选占位，待放行确认")).toBeInTheDocument();
  expect(screen.getByText(/候选：/)).toBeInTheDocument();
});
it("paginates recorded steps locally without hiding that more steps exist", () => {
  setup({
    evaluations: [
      {
        severity: "warning",
        suppressed: false,
        steps: Array.from({ length: 18 }, (_, i) => ({
          policy: { id: `policy-${i}`, version: 1 },
          scheme: "clip",
          outcome: "not_matched",
        })),
      },
    ],
  });
  expect(screen.getAllByRole("row")).toHaveLength(17);
  expect(screen.getByText("1–16 / 18")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "下页步骤" }));
  expect(screen.getAllByRole("row")).toHaveLength(3);
  expect(screen.getByText("17–18 / 18")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "下页步骤" })).toBeDisabled();
});
