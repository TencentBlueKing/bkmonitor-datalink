import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { MemoryRouter, useLocation, useNavigationType } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ElasticsearchTopology } from "../../shared/contracts";
import { ElasticsearchPage } from "./ElasticsearchPage";

const topology: ElasticsearchTopology = {
  cluster: {
    name: "linkd-e2e",
    version: "7.17.7",
    status: "yellow",
    numberOfNodes: 1,
    activeShards: 4,
    unassignedShards: 4,
  },
  targets: [
    {
      entity: "events",
      configuredTargets: ["linkd-events"],
      indices: ["linkd-events-2026.09.02"],
      aliases: ["linkd-events"],
    },
    {
      entity: "alerts",
      configuredTargets: ["linkd-alerts-active"],
      indices: ["linkd-alerts-active-000001"],
      aliases: ["linkd-alerts-active"],
    },
    {
      entity: "alert-logs",
      configuredTargets: ["linkd-alert-logs"],
      indices: [],
      aliases: [],
    },
  ],
  indices: [
    {
      name: "linkd-events-2026.09.02",
      health: "yellow",
      status: "open",
      primaryShards: 1,
      replicaShards: 1,
      docsCount: 7,
      storeBytes: 4096,
      aliases: ["linkd-events"],
      entities: ["events"],
      mappingFields: ["event_id"],
    },
    {
      name: "linkd-alerts-active-000001",
      health: "green",
      status: "open",
      primaryShards: 1,
      replicaShards: 0,
      docsCount: 3,
      storeBytes: 2048,
      aliases: ["linkd-alerts-active"],
      entities: ["alerts"],
      mappingFields: ["alert_id"],
    },
  ],
  aliases: [
    {
      name: "linkd-events",
      indices: ["linkd-events-2026.09.02"],
      entities: ["events"],
      writeIndex: "linkd-events-2026.09.02",
    },
    {
      name: "linkd-alerts-active",
      indices: ["linkd-alerts-active-000001"],
      entities: ["alerts"],
      writeIndex: "linkd-alerts-active-000001",
    },
  ],
  templates: [
    {
      name: "linkd-events-template",
      indexPatterns: ["linkd-events-*"],
      schema: "event",
    },
  ],
};

const largeTopology = buildLargeTopology(28, 27);

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("ElasticsearchPage", () => {
  it("opens the aliases tab from a deep link", async () => {
    renderPage(topology, "/storage/elasticsearch?tab=aliases&source=deep-link");

    await screen.findByRole("heading", { name: "Elasticsearch Storage" });
    expect(screen.getByRole("button", { name: "自动 30s" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("tab", { name: /Aliases/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(
      within(screen.getByRole("tabpanel")).getByText(
        "write → linkd-events-2026.09.02",
      ),
    ).toBeInTheDocument();
  });

  it("updates the tab query with replace while preserving other params", async () => {
    renderPage(
      topology,
      "/storage/elasticsearch?scope=all&tab=indices&view=compact",
    );
    await screen.findByRole("heading", { name: "Elasticsearch Storage" });

    fireEvent.click(screen.getByRole("tab", { name: /Aliases/ }));

    expect(screen.getByLabelText("当前 URL 查询参数")).toHaveTextContent(
      "?scope=all&tab=aliases&view=compact",
    );
    expect(screen.getByLabelText("当前导航类型")).toHaveTextContent("REPLACE");
  });

  it.each([
    ["missing", "/storage/elasticsearch?scope=all"],
    ["invalid", "/storage/elasticsearch?scope=all&tab=unknown"],
  ])("falls back to indices for a %s tab query", async (_case, path) => {
    renderPage(topology, path);
    await screen.findByRole("heading", { name: "Elasticsearch Storage" });

    expect(screen.getByRole("tab", { name: /物理索引/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(
      within(screen.getByRole("tabpanel")).getByRole("table"),
    ).toBeInTheDocument();
  });

  it("separates indices and aliases into tabs and omits templates", async () => {
    renderPage(topology);

    await screen.findByRole("heading", { name: "Elasticsearch Storage" });
    const indicesTab = screen.getByRole("tab", { name: /物理索引/ });
    const aliasesTab = screen.getByRole("tab", { name: /Aliases/ });
    expect(indicesTab).toHaveAttribute("aria-selected", "true");
    expect(
      within(screen.getByRole("tabpanel")).getByRole("table"),
    ).toBeInTheDocument();
    expect(screen.queryByText("Index Templates")).not.toBeInTheDocument();
    expect(screen.queryByText("linkd-events-template")).not.toBeInTheDocument();

    fireEvent.click(aliasesTab);

    expect(aliasesTab).toHaveAttribute("aria-selected", "true");
    expect(indicesTab).toHaveAttribute("aria-selected", "false");
    const aliasPanel = screen.getByRole("tabpanel");
    expect(within(aliasPanel).queryByRole("table")).not.toBeInTheDocument();
    expect(
      within(aliasPanel).getByText("write → linkd-events-2026.09.02"),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Alias 分页")).not.toBeInTheDocument();
  });

  it("supports keyboard navigation between storage tabs", async () => {
    renderPage(topology);
    await screen.findByRole("heading", { name: "Elasticsearch Storage" });

    const indicesTab = screen.getByRole("tab", { name: /物理索引/ });
    const aliasesTab = screen.getByRole("tab", { name: /Aliases/ });
    indicesTab.focus();
    fireEvent.keyDown(indicesTab, { key: "ArrowRight" });

    expect(aliasesTab).toHaveFocus();
    expect(aliasesTab).toHaveAttribute("aria-selected", "true");
    expect(aliasesTab).toHaveAttribute("tabindex", "0");
    expect(indicesTab).toHaveAttribute("tabindex", "-1");

    fireEvent.keyDown(aliasesTab, { key: "Home" });
    expect(indicesTab).toHaveFocus();
    expect(indicesTab).toHaveAttribute("aria-selected", "true");
  });

  it("filters the active tab by topology entity and visible names", async () => {
    renderPage(topology);
    await screen.findByRole("heading", { name: "Elasticsearch Storage" });

    const entityFilter = screen.getByRole("combobox", {
      name: "按 entity 过滤",
    });
    expect(
      within(entityFilter).getByRole("option", { name: "AlertLog" }),
    ).toBeInTheDocument();
    fireEvent.change(entityFilter, { target: { value: "alerts" } });

    let panel = screen.getByRole("tabpanel");
    expect(
      within(panel).getByText("linkd-alerts-active-000001"),
    ).toBeInTheDocument();
    expect(
      within(panel).queryByText("linkd-events-2026.09.02"),
    ).not.toBeInTheDocument();
    expect(within(panel).getByText("1 / 2 indices")).toBeInTheDocument();

    fireEvent.change(screen.getByRole("searchbox", { name: "关键字搜索" }), {
      target: { value: "missing-index" },
    });
    expect(screen.getByText("没有匹配的物理索引")).toBeInTheDocument();
    expect(
      screen.getByText("请调整 entity 或关键字后重试。"),
    ).toBeInTheDocument();

    fireEvent.change(entityFilter, { target: { value: "all" } });
    fireEvent.change(screen.getByRole("searchbox", { name: "关键字搜索" }), {
      target: { value: "events-2026.09.02" },
    });
    fireEvent.click(screen.getByRole("tab", { name: /Aliases/ }));

    panel = screen.getByRole("tabpanel");
    expect(within(panel).getByText("linkd-events")).toBeInTheDocument();
    expect(
      within(panel).queryByText("linkd-alerts-active"),
    ).not.toBeInTheDocument();
    expect(within(panel).getByText("1 / 2 aliases")).toBeInTheDocument();
  });

  it("paginates large index and alias results and keeps the page valid", async () => {
    renderPage(largeTopology);
    await screen.findByRole("heading", { name: "Elasticsearch Storage" });

    expect(screen.getByText("显示 1–10 / 共 28 项")).toBeInTheDocument();
    let panel = screen.getByRole("tabpanel");
    expect(within(panel).getByText("linkd-index-001")).toBeInTheDocument();
    expect(
      within(panel).queryByText("linkd-index-011"),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    expect(screen.getByText("显示 21–28 / 共 28 项")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();

    fireEvent.change(screen.getByRole("searchbox", { name: "关键字搜索" }), {
      target: { value: "index-005" },
    });
    expect(screen.queryByLabelText("物理索引分页")).not.toBeInTheDocument();
    expect(within(panel).getByText("linkd-index-005")).toBeInTheDocument();

    fireEvent.change(screen.getByRole("searchbox", { name: "关键字搜索" }), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    fireEvent.change(screen.getByRole("combobox", { name: "每页显示数量" }), {
      target: { value: "25" },
    });
    expect(screen.getByText("显示 1–25 / 共 28 项")).toBeInTheDocument();
    expect(within(panel).getByText("linkd-index-001")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: /Aliases/ }));
    panel = screen.getByRole("tabpanel");
    expect(screen.getByText("显示 1–25 / 共 27 项")).toBeInTheDocument();
    expect(within(panel).getByText("linkd-alias-001")).toBeInTheDocument();
    expect(screen.getByText("第 1 / 2 页")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    expect(screen.getByText("显示 26–27 / 共 27 项")).toBeInTheDocument();
  });

  it("opens paginated target details in a dismissible drawer", async () => {
    renderPage(largeTopology);
    await screen.findByRole("heading", { name: "Elasticsearch Storage" });

    expect(
      screen.queryByRole("region", { name: "Event 物理索引明细" }),
    ).not.toBeInTheDocument();
    const targetToggle = screen.getByRole("button", {
      name: "查看 Event 配置解析",
    });
    expect(targetToggle).toHaveAttribute("aria-expanded", "false");

    fireEvent.click(targetToggle);

    expect(targetToggle).toHaveAttribute("aria-expanded", "true");
    let drawer = screen.getByRole("dialog", { name: "Event 配置解析" });
    expect(drawer).toHaveAttribute("aria-modal", "true");
    const closeButton = within(drawer).getByRole("button", {
      name: "关闭 Event 配置解析",
    });
    expect(closeButton).toHaveFocus();
    expect(document.body.style.overflow).toBe("hidden");
    const indexDetails = within(drawer).getByRole("region", {
      name: "Event 物理索引明细",
    });
    expect(within(indexDetails).getAllByRole("listitem")).toHaveLength(8);
    expect(
      within(indexDetails).getByText("linkd-index-001"),
    ).toBeInTheDocument();
    expect(
      within(indexDetails).queryByText("linkd-index-009"),
    ).not.toBeInTheDocument();
    expect(
      within(indexDetails).getByText("1–8 / 共 28 项"),
    ).toBeInTheDocument();

    fireEvent.click(
      within(indexDetails).getByRole("button", { name: "物理索引明细下一页" }),
    );
    expect(
      within(indexDetails).getByText("linkd-index-009"),
    ).toBeInTheDocument();
    expect(
      within(indexDetails).getByText("9–16 / 共 28 项"),
    ).toBeInTheDocument();

    const aliasDetails = within(drawer).getByRole("region", {
      name: "Event Aliases明细",
    });
    expect(within(aliasDetails).getAllByRole("listitem")).toHaveLength(8);
    expect(
      within(aliasDetails).getByText("1–8 / 共 27 项"),
    ).toBeInTheDocument();
    const lastDrawerControl = within(aliasDetails).getByRole("button", {
      name: "Aliases明细下一页",
    });
    closeButton.focus();
    fireEvent.keyDown(closeButton, { key: "Tab", shiftKey: true });
    expect(lastDrawerControl).toHaveFocus();
    fireEvent.keyDown(lastDrawerControl, { key: "Tab" });
    expect(closeButton).toHaveFocus();

    fireEvent.click(closeButton);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(targetToggle).toHaveFocus();
    expect(document.body.style.overflow).toBe("");

    fireEvent.click(targetToggle);
    drawer = screen.getByRole("dialog", { name: "Event 配置解析" });
    fireEvent.keyDown(document, { key: "Escape" });
    expect(drawer).not.toBeInTheDocument();
    expect(targetToggle).toHaveFocus();

    fireEvent.click(targetToggle);
    const backdrop = document.querySelector(".es-drawer-backdrop");
    expect(backdrop).not.toBeNull();
    fireEvent.click(backdrop as Element);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(targetToggle).toHaveFocus();
  });

  it("explains when no aliases are configured", async () => {
    renderPage({ ...topology, aliases: [] });
    await screen.findByRole("heading", { name: "Elasticsearch Storage" });

    fireEvent.click(screen.getByRole("tab", { name: /Aliases/ }));

    expect(
      screen.getByText("当前 target 直接指向物理索引"),
    ).toBeInTheDocument();
  });
});

function renderPage(
  value: ElasticsearchTopology,
  path = "/storage/elasticsearch",
) {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify(value), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
    ),
  );
  return render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: { queries: { retry: false, gcTime: 0 } },
        })
      }
    >
      <MemoryRouter initialEntries={[path]}>
        <ElasticsearchPage />
        <LocationSearch />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function LocationSearch() {
  const location = useLocation();
  const navigationType = useNavigationType();
  return (
    <>
      <output aria-label="当前 URL 查询参数">{location.search}</output>
      <output aria-label="当前导航类型">{navigationType}</output>
    </>
  );
}

function buildLargeTopology(
  indexCount: number,
  aliasCount: number,
): ElasticsearchTopology {
  const indexNames = Array.from(
    { length: indexCount },
    (_, index) => `linkd-index-${String(index + 1).padStart(3, "0")}`,
  );
  const aliasNames = Array.from(
    { length: aliasCount },
    (_, index) => `linkd-alias-${String(index + 1).padStart(3, "0")}`,
  );
  return {
    cluster: topology.cluster,
    targets: [
      {
        entity: "events",
        configuredTargets: ["linkd-events"],
        indices: indexNames,
        aliases: aliasNames,
      },
    ],
    indices: indexNames.map((name, index) => ({
      name,
      health: "green",
      status: "open",
      primaryShards: 1,
      replicaShards: 0,
      docsCount: index + 1,
      storeBytes: (index + 1) * 1024,
      aliases: index < aliasCount ? [aliasNames[index]] : [],
      entities: ["events"],
      mappingFields: ["event_id"],
    })),
    aliases: aliasNames.map((name, index) => ({
      name,
      indices: [indexNames[index]],
      entities: ["events"],
      writeIndex: indexNames[index],
    })),
    templates: [],
  };
}
