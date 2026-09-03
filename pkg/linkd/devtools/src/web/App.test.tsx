import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("App", () => {
  it("renders the operations overview and read-only boundary", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        const body = url.includes("capabilities")
          ? {
              version: "0.1.0",
              metrics: { configured: true, source: "prometheus" },
              entities: {
                events: { source: "mysql", filters: [] },
                alerts: { source: "mysql", filters: [] },
                "alert-logs": { source: "mysql", filters: [] },
              },
              storage: { elasticsearch: { configured: false } },
              limits: {
                defaultRangeSeconds: 3600,
                maxRangeSeconds: 604800,
                defaultLimit: 50,
                maxLimit: 200,
              },
            }
          : {
              from: "2026-08-30T00:00:00.000Z",
              to: "2026-08-30T01:00:00.000Z",
              step: 15,
              panels: [],
            };
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
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
    expect(screen.getByText("READ ONLY")).toBeInTheDocument();
    expect(
      await screen.findByRole("heading", { name: "处理状态" }),
    ).toBeInTheDocument();
  });

  it("keeps sidebar navigation usable after an entity query fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("capabilities")) {
          return response({
            version: "0.1.0",
            metrics: { configured: true, source: "prometheus" },
            entities: {
              events: { source: "elasticsearch", filters: ["state"] },
              alerts: { source: "elasticsearch", filters: ["status"] },
              "alert-logs": {
                source: "elasticsearch",
                filters: ["alertId"],
              },
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
        if (url.includes("/alerts")) {
          return response(
            {
              error: {
                code: "data_source_error",
                message: "测试查询失败",
                requestId: "request-a",
              },
            },
            502,
          );
        }
        if (url.includes("/events")) {
          return response({
            items: [],
            source: "elasticsearch",
            warnings: [],
          });
        }
        throw new Error(`unexpected fetch ${url}`);
      }),
    );
    renderApp("/explore/alerts");

    expect(
      await screen.findByText("查询失败：测试查询失败"),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Events/ })).toHaveAttribute(
      "data-reload-document",
      "true",
    );
  });

  it("renders configured Elasticsearch topology", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("capabilities")) {
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
        if (url.includes("elasticsearch/topology")) {
          return response({
            cluster: {
              name: "linkd-e2e",
              version: "7.17.7",
              status: "yellow",
              numberOfNodes: 1,
              activeShards: 3,
              unassignedShards: 3,
            },
            targets: [
              {
                entity: "events",
                configuredTargets: ["linkd-events-*"],
                indices: ["linkd-events"],
                aliases: ["linkd-events-read"],
              },
            ],
            indices: [
              {
                name: "linkd-events",
                health: "yellow",
                status: "open",
                primaryShards: 1,
                replicaShards: 1,
                docsCount: 7,
                storeBytes: 4096,
                aliases: ["linkd-events-read"],
                entities: ["events"],
              },
            ],
            aliases: [
              {
                name: "linkd-events-read",
                indices: ["linkd-events"],
                entities: ["events"],
              },
            ],
          });
        }
        throw new Error(`unexpected fetch ${url}`);
      }),
    );
    renderApp("/storage/elasticsearch");

    expect(
      await screen.findByRole("heading", { name: "Elasticsearch Storage" }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("linkd-events").length).toBeGreaterThan(0);
    expect(screen.getAllByText("linkd-events-read").length).toBeGreaterThan(0);
  });
});

function renderApp(path: string) {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={[path]}>
        <App />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function response(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json" },
  });
}
