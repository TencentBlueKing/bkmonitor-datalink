import { expect, it } from "vitest";
import { alertFixture, logFixture } from "../../../tests/fixtures/explorer";
import { relationLinks, searchValues } from "./explorer";

it("links a Hook source_event cause to its exact Event within the same tenant", () => {
  const links = relationLinks(
    "alert-logs",
    {
      ...logFixture,
      tenantId: "tenant & other",
      payload: {
        ...logFixture.payload,
        params: { cause_type: "source_event", cause_id: "event / id" },
      },
    },
    3600,
  );
  const event = new URL(
    links.find((link) => link.label === "触发事件")!.to,
    "http://local",
  );
  expect(event.searchParams.get("bk_tenant_id")).toBe("tenant & other");
  expect(event.searchParams.get("id")).toBe("event / id");
  expect(event.searchParams.has("from")).toBe(false);
  expect(
    relationLinks(
      "alert-logs",
      {
        ...logFixture,
        payload: {
          params: { cause_type: "user_operation", cause_id: "operation" },
        },
      },
      3600,
    ),
  ).toEqual([]);
});
it("keeps historical Alert collection links around its update and strips presentation state from API filters", () => {
  const links = relationLinks("alerts", alertFixture, 3600);
  for (const link of links.filter((l) => l.label.includes("窗口"))) {
    const url = new URL(link.to, "http://local");
    expect(url.searchParams.get("bk_tenant_id")).toBe(alertFixture.tenantId);
    expect(
      Date.parse(url.searchParams.get("to")!) -
        Date.parse(url.searchParams.get("from")!),
    ).toBe(3600000);
  }
  const values = searchValues(
    "alerts",
    new URLSearchParams(
      "id=alert-a&detail=alert-b&detail_tenant=other&view=table&range=exact&arbitrary=ignored",
    ),
    Date.now(),
    3600,
  );
  expect(values).toEqual({ id: "alert-a" });
});
