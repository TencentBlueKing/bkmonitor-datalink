import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("sidebar navigation", () => {
  it("groups every route without changing its order", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input).includes("capabilities")) {
          return response({
            version: "0.1.0",
            metrics: { configured: true, source: "prometheus" },
            entities: {
              events: { source: "elasticsearch", filters: [] },
              alerts: { source: "elasticsearch", filters: [] },
              "alert-logs": { source: "elasticsearch", filters: [] },
            },
            storage: { elasticsearch: { configured: true } },
            limits: {
              defaultRangeSeconds: 3600,
              maxRangeSeconds: 604800,
              defaultLimit: 50,
              maxLimit: 200,
            },
          });
        }
        return response({
          from: "2026-08-30T00:00:00.000Z",
          to: "2026-08-30T01:00:00.000Z",
          step: 15,
          panels: [],
        });
      }),
    );

    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <MemoryRouter initialEntries={["/overview"]}>
          <App />
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await screen.findByRole("link", { name: /ES Storage/ });
    const navigation = screen.getByRole("navigation", {
      name: "主要导航",
    });
    const expectedGroups = [
      ["总览", ["/overview"]],
      ["模块", ["/cleaner", "/lifecycle", "/control-plane"]],
      [
        "核心数据",
        ["/explore/events", "/explore/alerts", "/explore/alert-logs"],
      ],
      [
        "存储",
        [
          "/storage/elasticsearch",
          "/infrastructure/kafka",
          "/infrastructure/redis",
        ],
      ],
      ["系统", ["/config"]],
    ] as const;

    for (const [label, routes] of expectedGroups) {
      const heading = within(navigation).getByRole("heading", { name: label });
      const group = heading.closest(".nav-group");
      expect(group).not.toBeNull();
      expect(
        within(group as HTMLElement)
          .getAllByRole("link")
          .map((link) => link.getAttribute("href")),
      ).toEqual(routes);
    }
  });
});

function response(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
}
