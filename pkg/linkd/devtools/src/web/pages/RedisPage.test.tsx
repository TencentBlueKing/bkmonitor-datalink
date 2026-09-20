import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RedisPage } from "./RedisPage";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("RedisPage", () => {
  it("separates Redis purposes and preserves unknown lag", async () => {
    stubRedisAPI();
    const { container } = renderPage();

    expect(await screen.findByRole("heading", { name: "Redis" })).toBeVisible();
    expect(screen.getByRole("button", { name: "自动 15s" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getAllByRole("tab")).toHaveLength(4);
    fireEvent.click(screen.getByRole("tab", { name: /信号队列/ }));

    expect(
      await screen.findByRole("heading", { name: "Lifecycle 信号队列" }),
    ).toBeVisible();
    expect(screen.getAllByText("linkd-lifecycle").length).toBeGreaterThan(0);
    expect(screen.getAllByText("未知").length).toBeGreaterThan(0);
    expect(screen.getByText(/Idle 是距最近一次尝试交互/)).toBeVisible();

    const priority = container.querySelector<HTMLElement>(
      ".redis-signal-priority-grid",
    );
    const background = container.querySelector<HTMLElement>(
      ".redis-stream-background",
    );
    expect(priority).not.toBeNull();
    expect(background).not.toBeNull();
    expect(within(priority!).getByText("Group Lag")).toBeVisible();
    expect(within(priority!).getByText("PEL Pending")).toBeVisible();
    expect(within(priority!).queryByText("累计写入")).toBeNull();
    expect(within(background!).getByText("累计写入")).toBeVisible();
  });

  it("filters Mailboxes and exposes a structured detail entry", async () => {
    stubRedisAPI();
    renderPage();
    await screen.findByRole("heading", { name: "Redis" });
    fireEvent.click(screen.getByRole("tab", { name: /Mailbox 调度/ }));

    expect(await screen.findByText("mailbox-a")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "查看详情 →" }));
    expect(screen.getByText("MAILBOX DETAIL")).toBeVisible();
    expect(screen.getByRole("link", { name: "event-a" })).toHaveAttribute(
      "href",
      "/explore/events?id=event-a",
    );
  });

  it("initializes from a deep link and keeps the selected tab on reload", async () => {
    stubRedisAPI();
    renderPage("/infrastructure/redis?tab=signal");

    expect(
      await screen.findByRole("heading", { name: "Lifecycle 信号队列" }),
    ).toBeVisible();
    expect(screen.getByRole("tab", { name: /信号队列/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByTestId("location")).toHaveTextContent("tab=signal");
  });

  it("falls back from an invalid tab and replaces the URL", async () => {
    stubRedisAPI();
    renderPage("/infrastructure/redis?scope=kept&tab=unknown");

    expect(
      await screen.findByRole("heading", { name: "实例运行与数据安全" }),
    ).toBeVisible();
    expect(screen.getByRole("tab", { name: /实例总览/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByTestId("location")).toHaveTextContent(
      "scope=kept&tab=overview",
    );
  });

  it("updates the tab parameter while preserving other query parameters", async () => {
    stubRedisAPI();
    renderPage("/infrastructure/redis?scope=kept&tab=overview");
    await screen.findByRole("heading", { name: "实例运行与数据安全" });

    fireEvent.click(screen.getByRole("tab", { name: /Lease \/ Lock/ }));

    expect(
      await screen.findByRole("heading", { name: "Lease / Lock" }),
    ).toBeVisible();
    expect(screen.getByTestId("location")).toHaveTextContent(
      "scope=kept&tab=lease",
    );
  });
});

function renderPage(path = "/infrastructure/redis") {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={[path]}>
        <RedisPage />
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function LocationProbe() {
  const location = useLocation();
  return (
    <output data-testid="location">
      {location.pathname}
      {location.search}
    </output>
  );
}

function stubRedisAPI() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/pending")) {
        return response({
          status: "empty",
          message: "当前没有已投递但未确认的 Signal",
          snapshotAt: "2026-09-02T01:00:00.000Z",
          group: "linkd-lifecycle",
          total: 0,
          smallestId: null,
          greatestId: null,
          claimMinIdleMilliseconds: 300_000,
          items: [],
          truncated: false,
        });
      }
      if (url.includes("/mailboxes")) {
        return response({
          status: "available",
          snapshotAt: "2026-09-02T01:00:00.000Z",
          scanned: 1,
          scanTruncated: false,
          items: [
            {
              mailboxId: "mailbox-a",
              eventCount: 2,
              headEventId: "event-a",
            },
          ],
        });
      }
      if (url.includes("/leases")) {
        return response({
          status: "empty",
          snapshotAt: "2026-09-02T01:00:00.000Z",
          scanned: 0,
          scanTruncated: false,
          items: [],
        });
      }
      return response({
        status: "available",
        snapshotAt: "2026-09-02T01:00:00.000Z",
        connection: {
          status: "available",
          address: "127.0.0.1:6379",
          database: 0,
          ping: "PONG",
        },
        instance: {
          status: "available",
          version: "7.2.16",
          usedMemoryBytes: 4096,
        },
        signalQueue: {
          status: "available",
          streamKey: "linkd:lifecycle:signals",
          expectedGroup: "linkd-lifecycle",
          claimMinIdleSeconds: 300,
          stream: {
            exists: true,
            length: 12,
            entriesAdded: 12,
            memoryBytes: 2048,
            firstEntryId: "1788352938006-0",
            lastGeneratedId: "1788357791745-1",
            oldestEntryAgeSeconds: 120,
            groupsCount: 1,
            maxEntries: 100_000,
            entriesAboveMax: 0,
          },
          groups: [
            {
              name: "linkd-lifecycle",
              expected: true,
              consumersCount: 1,
              pending: 0,
              lastDeliveredId: "1788357791745-1",
              entriesRead: 12,
              lag: null,
              consumersStatus: "available",
              consumers: [
                {
                  name: "consumer-a",
                  pending: 0,
                  idleMilliseconds: 500,
                  inactiveMilliseconds: 1200,
                },
              ],
            },
          ],
        },
        mailbox: {
          status: "available",
          activeMailboxes: 1,
          scanTruncated: false,
          maxPendingPerMailbox: 128,
          maxDrainEvents: 512,
        },
        leases: {
          status: "empty",
          activeLeases: 0,
          scanTruncated: false,
          ttlSeconds: 60,
          renewIntervalSeconds: 20,
        },
      });
    }),
  );
}

function response(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
}
