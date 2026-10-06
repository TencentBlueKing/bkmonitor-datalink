import { useQuery } from "@tanstack/react-query";
import {
  type KeyboardEvent,
  type RefObject,
  useEffect,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import { useSearchParams } from "react-router-dom";

import type { ElasticsearchTopology, EntityKind } from "../../shared/contracts";
import { getElasticsearchTopology } from "../api";
import { HelpLabel, HelpTableHeader, HelpTip } from "../components/HelpTip";
import { RefreshControls } from "../components/RefreshControls";
import { StatusBadge } from "../components/StatusBadge";
import { useReportPageQueryFailure } from "../navigation";

const entityLabels: Record<EntityKind, string> = {
  events: "Event",
  alerts: "Alert",
  "alert-logs": "AlertLog",
};

const entityKinds: EntityKind[] = ["events", "alerts", "alert-logs"];
const listPageSizes = [10, 25, 50];
const targetDetailPageSize = 8;
type StorageTab = "indices" | "aliases";
type ElasticsearchTarget = ElasticsearchTopology["targets"][number];

export function ElasticsearchPage() {
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [searchParams, setSearchParams] = useSearchParams();
  const activeTab = storageTab(searchParams.get("tab"));
  const indicesTabRef = useRef<HTMLButtonElement>(null);
  const aliasesTabRef = useRef<HTMLButtonElement>(null);
  const [selectedEntity, setSelectedEntity] = useState<EntityKind | "all">(
    "all",
  );
  const [keyword, setKeyword] = useState("");
  const [listPage, setListPage] = useState(1);
  const [listPageSize, setListPageSize] = useState(listPageSizes[0]);
  const topology = useQuery({
    queryKey: ["elasticsearch-topology"],
    queryFn: getElasticsearchTopology,
    refetchInterval: autoRefresh ? 30_000 : false,
  });
  useReportPageQueryFailure(topology.isError);
  const heading = (
    <PageHeading
      status={topology.data?.cluster.status}
      lastSuccessfulAt={topology.dataUpdatedAt || undefined}
      isFetching={topology.isFetching}
      autoRefresh={autoRefresh}
      onRefresh={() => void topology.refetch()}
      onToggleAutoRefresh={() => setAutoRefresh((value) => !value)}
    />
  );

  if (topology.isLoading) {
    return <div className="page-loading">正在读取 Elasticsearch 拓扑…</div>;
  }
  if (topology.isError || !topology.data) {
    return (
      <section>
        {heading}
        <div className="storage-error-state" role="alert">
          <strong>Elasticsearch 拓扑查询失败</strong>
          <span>{topology.error?.message ?? "未返回拓扑数据"}</span>
          <button
            className="primary-button"
            type="button"
            onClick={() => void topology.refetch()}
          >
            重新查询
          </button>
        </div>
      </section>
    );
  }

  const data = topology.data;
  const totalDocs = sum(data.indices.map((index) => index.docsCount));
  const totalBytes = sum(data.indices.map((index) => index.storeBytes));
  const maxDocs = Math.max(...data.indices.map((index) => index.docsCount), 1);
  const normalizedKeyword = keyword.trim().toLowerCase();
  const availableEntities = entityKinds.filter(
    (entity) =>
      data.targets.some((target) => target.entity === entity) ||
      data.indices.some((index) => index.entities.includes(entity)) ||
      data.aliases.some((alias) => alias.entities.includes(entity)),
  );
  const filteredIndices = data.indices.filter(
    (index) =>
      matchesEntity(index.entities, selectedEntity) &&
      matchesKeyword(
        [
          index.name,
          ...index.aliases,
          ...index.entities.flatMap((entity) => [entity, entityLabels[entity]]),
        ],
        normalizedKeyword,
      ),
  );
  const filteredAliases = data.aliases.filter(
    (alias) =>
      matchesEntity(alias.entities, selectedEntity) &&
      matchesKeyword(
        [
          alias.name,
          ...alias.indices,
          alias.writeIndex ?? "",
          ...alias.entities.flatMap((entity) => [entity, entityLabels[entity]]),
        ],
        normalizedKeyword,
      ),
  );
  const hasFilters = selectedEntity !== "all" || normalizedKeyword !== "";
  const indexPage = clampPage(listPage, filteredIndices.length, listPageSize);
  const aliasPage = clampPage(listPage, filteredAliases.length, listPageSize);
  const visibleIndices = pageItems(filteredIndices, indexPage, listPageSize);
  const visibleAliases = pageItems(filteredAliases, aliasPage, listPageSize);

  function resetFilters() {
    setSelectedEntity("all");
    setKeyword("");
    setListPage(1);
  }

  function selectTab(tab: StorageTab) {
    const nextParams = new URLSearchParams(searchParams);
    nextParams.set("tab", tab);
    setSearchParams(nextParams, { replace: true });
    setListPage(1);
  }

  function selectEntity(entity: EntityKind | "all") {
    setSelectedEntity(entity);
    setListPage(1);
  }

  function changeKeyword(value: string) {
    setKeyword(value);
    setListPage(1);
  }

  function changePageSize(value: number) {
    setListPageSize(value);
    setListPage(1);
  }

  function handleTabKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
    let nextTab: StorageTab | undefined;
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      nextTab = activeTab === "indices" ? "aliases" : "indices";
    } else if (event.key === "Home") {
      nextTab = "indices";
    } else if (event.key === "End") {
      nextTab = "aliases";
    }
    if (!nextTab) {
      return;
    }

    event.preventDefault();
    selectTab(nextTab);
    (nextTab === "indices" ? indicesTabRef : aliasesTabRef).current?.focus();
  }

  return (
    <section>
      {heading}

      <div className="es-stat-grid">
        <StorageStat
          label="Cluster"
          value={data.cluster.name}
          help="Elasticsearch API 返回的集群名称。"
        />
        <StorageStat
          label="Version"
          value={data.cluster.version}
          help="当前连接的 Elasticsearch 服务版本。"
        />
        <StorageStat
          label="Nodes"
          value={String(data.cluster.numberOfNodes)}
          help="当前加入 Elasticsearch 集群的节点数。"
        />
        <StorageStat
          label="Indices"
          value={String(data.indices.length)}
          help="配置白名单 target 最终解析出的物理索引数。"
        />
        <StorageStat
          label="Documents"
          value={formatCount(totalDocs)}
          help="当前展示物理索引的文档数总和。"
        />
        <StorageStat
          label="Storage"
          value={formatBytes(totalBytes)}
          help="当前展示物理索引占用存储空间的总和。"
        />
      </div>

      <article className="es-topology-panel">
        <header>
          <div>
            <div className="section-title-with-help">
              <h2>配置目标解析</h2>
              <HelpTip label="配置目标解析">
                将每类实体配置的 index 或 alias 解析为实际物理索引。
              </HelpTip>
            </div>
            <p>实体与配置 target 摘要，展开查看分页解析明细</p>
          </div>
          <span>最多 128 个物理索引</span>
        </header>
        <div className="es-topology-grid">
          {data.targets.map((target) => (
            <TargetCard key={target.entity} target={target} />
          ))}
        </div>
      </article>

      <div className="es-storage-browser">
        <div
          className="es-storage-tabs"
          role="tablist"
          aria-label="Elasticsearch 存储对象"
        >
          <button
            ref={indicesTabRef}
            id="es-indices-tab"
            type="button"
            role="tab"
            aria-selected={activeTab === "indices"}
            aria-controls="es-storage-panel"
            tabIndex={activeTab === "indices" ? 0 : -1}
            onClick={() => selectTab("indices")}
            onKeyDown={handleTabKeyDown}
          >
            物理索引 <span>{data.indices.length}</span>
          </button>
          <button
            ref={aliasesTabRef}
            id="es-aliases-tab"
            type="button"
            role="tab"
            aria-selected={activeTab === "aliases"}
            aria-controls="es-storage-panel"
            tabIndex={activeTab === "aliases" ? 0 : -1}
            onClick={() => selectTab("aliases")}
            onKeyDown={handleTabKeyDown}
          >
            Aliases <span>{data.aliases.length}</span>
          </button>
        </div>

        <article
          id="es-storage-panel"
          className="es-topology-panel es-storage-panel"
          role="tabpanel"
          aria-labelledby={
            activeTab === "indices" ? "es-indices-tab" : "es-aliases-tab"
          }
        >
          <header>
            <div>
              <div className="section-title-with-help">
                <h2>{activeTab === "indices" ? "物理索引" : "Aliases"}</h2>
                <HelpTip
                  label={activeTab === "indices" ? "物理索引" : "Aliases"}
                >
                  {activeTab === "indices"
                    ? "实际保存文档的 Elasticsearch index。"
                    : "指向一个或多个物理索引的逻辑名称；write 标记表示写入目标。"}
                </HelpTip>
              </div>
              <p>
                {activeTab === "indices"
                  ? "文档量、空间、分片和实体归属"
                  : "仅展示配置白名单涉及的 alias"}
              </p>
            </div>
            <span>
              {activeTab === "indices"
                ? filteredCountLabel(
                    filteredIndices.length,
                    data.indices.length,
                    "indices",
                    hasFilters,
                  )
                : filteredCountLabel(
                    filteredAliases.length,
                    data.aliases.length,
                    "aliases",
                    hasFilters,
                  )}
            </span>
          </header>

          <div className="es-storage-filters">
            <label>
              <HelpLabel
                label="ENTITY"
                help="按 Event、Alert 或 AlertLog 的存储归属过滤。"
              />
              <select
                aria-label="按 entity 过滤"
                value={selectedEntity}
                onChange={(event) =>
                  selectEntity(event.target.value as EntityKind | "all")
                }
              >
                <option value="all">全部 entity</option>
                {availableEntities.map((entity) => (
                  <option key={entity} value={entity}>
                    {entityLabels[entity]}
                  </option>
                ))}
              </select>
            </label>
            <label className="es-keyword-filter">
              <HelpLabel
                label="KEYWORD"
                help="匹配 index、alias 或 write index 名称。"
              />
              <input
                type="search"
                aria-label="关键字搜索"
                value={keyword}
                placeholder={
                  activeTab === "indices"
                    ? "搜索 index 或 alias 名称"
                    : "搜索 alias 或 index 名称"
                }
                onChange={(event) => changeKeyword(event.target.value)}
              />
            </label>
            {hasFilters && (
              <button type="button" onClick={resetFilters}>
                清空筛选
              </button>
            )}
          </div>

          {activeTab === "indices" &&
            (filteredIndices.length === 0 ? (
              <StorageEmptyState
                title={
                  hasFilters ? "没有匹配的物理索引" : "当前配置未解析到物理索引"
                }
                filtered={hasFilters}
                onReset={resetFilters}
              />
            ) : (
              <div className="table-scroll">
                <table className="es-index-table">
                  <thead>
                    <tr>
                      <HelpTableHeader
                        label="Index"
                        help="实际保存文档的 Elasticsearch 物理索引名。"
                      />
                      <HelpTableHeader
                        label="Health"
                        help="索引分片健康：green 完整、yellow 副本未完全分配、red 缺主分片。"
                      />
                      <HelpTableHeader
                        label="Entity"
                        help="该索引被 Linkd 哪类实体读写目标引用。"
                      />
                      <HelpTableHeader
                        label="Documents"
                        help="Elasticsearch 统计的索引文档数量。"
                      />
                      <HelpTableHeader
                        label="Storage"
                        help="该物理索引当前占用的存储空间。"
                      />
                      <HelpTableHeader
                        label="Shards"
                        help="主分片数 p / 副本分片配置数 r。"
                      />
                    </tr>
                  </thead>
                  <tbody>
                    {visibleIndices.map((index) => (
                      <tr key={index.name}>
                        <td>
                          <span className="mono id-cell">{index.name}</span>
                          {index.aliases.length > 0 && (
                            <small>{index.aliases.join(", ")}</small>
                          )}
                          {index.mappingFields.length > 0 && (
                            <small>
                              {index.mappingFields.length} mapped fields
                            </small>
                          )}
                        </td>
                        <td>
                          <StatusBadge value={index.health} />
                        </td>
                        <td>{index.entities.map(entityLabel).join(" · ")}</td>
                        <td>
                          <strong>{formatCount(index.docsCount)}</strong>
                          <div className="es-doc-bar">
                            <span
                              style={{
                                width: `${(index.docsCount / maxDocs) * 100}%`,
                              }}
                            />
                          </div>
                        </td>
                        <td>{formatBytes(index.storeBytes)}</td>
                        <td>
                          {index.primaryShards}p / {index.replicaShards}r
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ))}

          {activeTab === "indices" &&
            filteredIndices.length > listPageSizes[0] && (
              <StoragePagination
                label="物理索引分页"
                page={indexPage}
                pageSize={listPageSize}
                total={filteredIndices.length}
                onPageChange={setListPage}
                onPageSizeChange={changePageSize}
              />
            )}

          {activeTab === "aliases" &&
            (filteredAliases.length === 0 ? (
              <StorageEmptyState
                title={
                  hasFilters
                    ? "没有匹配的 alias"
                    : "当前 target 直接指向物理索引"
                }
                filtered={hasFilters}
                onReset={resetFilters}
              />
            ) : (
              <div className="es-alias-list">
                {visibleAliases.map((alias) => (
                  <div key={alias.name}>
                    <code>{alias.name}</code>
                    <span>→</span>
                    <div>
                      {alias.indices.map((index) => (
                        <small key={index}>{index}</small>
                      ))}
                    </div>
                    <em>{alias.entities.map(entityLabel).join(" · ")}</em>
                    {alias.writeIndex && (
                      <strong>write → {alias.writeIndex}</strong>
                    )}
                  </div>
                ))}
              </div>
            ))}

          {activeTab === "aliases" &&
            filteredAliases.length > listPageSizes[0] && (
              <StoragePagination
                label="Alias 分页"
                page={aliasPage}
                pageSize={listPageSize}
                total={filteredAliases.length}
                onPageChange={setListPage}
                onPageSizeChange={changePageSize}
              />
            )}
        </article>
      </div>

      <div className="es-cluster-footnote">
        <HelpLabel
          label={`Active shards ${data.cluster.activeShards}`}
          help="当前已分配且可提供服务的 shard 数。"
        />{" "}
        ·{" "}
        <HelpLabel
          label={`Unassigned shards ${data.cluster.unassignedShards}`}
          help="当前尚未分配到任何节点的 shard 数。"
        />
      </div>
    </section>
  );
}

function TargetCard({ target }: { target: ElasticsearchTarget }) {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const wasOpenRef = useRef(false);
  const shownTargets = target.configuredTargets.slice(0, 2);
  const drawerID = `es-target-${target.entity}-drawer`;

  useEffect(() => {
    if (!drawerOpen) {
      if (wasOpenRef.current) {
        triggerRef.current?.focus();
      }
      wasOpenRef.current = false;
      return;
    }

    wasOpenRef.current = true;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    closeButtonRef.current?.focus();
    const closeOnEscape = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") {
        setDrawerOpen(false);
      }
    };
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("keydown", closeOnEscape);
      document.body.style.overflow = previousOverflow;
    };
  }, [drawerOpen]);

  return (
    <div className="es-target-card">
      <div className="es-target-summary">
        <span className="es-entity-node">
          <span>{entityLabels[target.entity]}</span>
          <small>{target.entity}</small>
        </span>
        <span className="es-target-config">
          <small>
            CONFIGURED TARGET{" "}
            <HelpTip label="Configured target">
              Linkd 配置中允许该实体访问的 index、alias 或 pattern。
            </HelpTip>
          </small>
          <span>
            {shownTargets.length === 0 ? (
              <em>未配置</em>
            ) : (
              shownTargets.map((name) => <code key={name}>{name}</code>)
            )}
            {target.configuredTargets.length > shownTargets.length && (
              <em>+{target.configuredTargets.length - shownTargets.length}</em>
            )}
          </span>
        </span>
        <span className="es-target-counts">
          <span>
            <strong>{target.indices.length}</strong> indices{" "}
            <HelpTip label="Indices count">
              该实体的配置 target 解析出的物理索引数。
            </HelpTip>
          </span>
          <span>
            <strong>{target.aliases.length}</strong> aliases{" "}
            <HelpTip label="Aliases count">
              该实体配置涉及的 Elasticsearch alias 数。
            </HelpTip>
          </span>
        </span>
        <button
          ref={triggerRef}
          className="es-target-open"
          type="button"
          aria-haspopup="dialog"
          aria-expanded={drawerOpen}
          aria-controls={drawerID}
          aria-label={`查看 ${entityLabels[target.entity]} 配置解析`}
          onClick={() => setDrawerOpen(true)}
        >
          查看解析 <span aria-hidden="true">→</span>
        </button>
      </div>

      {drawerOpen && (
        <TargetDrawer
          target={target}
          closeButtonRef={closeButtonRef}
          onClose={() => setDrawerOpen(false)}
        />
      )}
    </div>
  );
}

function TargetDrawer({
  target,
  closeButtonRef,
  onClose,
}: {
  target: ElasticsearchTarget;
  closeButtonRef: RefObject<HTMLButtonElement | null>;
  onClose: () => void;
}) {
  const titleID = `es-target-${target.entity}-drawer-title`;
  return createPortal(
    <div className="es-drawer-layer">
      <div
        className="es-drawer-backdrop"
        aria-hidden="true"
        onClick={onClose}
      />
      <aside
        id={`es-target-${target.entity}-drawer`}
        className="es-target-drawer"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleID}
        onKeyDown={keepFocusInDrawer}
      >
        <header>
          <div>
            <p>{target.entity}</p>
            <h2 id={titleID}>{entityLabels[target.entity]} 配置解析</h2>
          </div>
          <button
            ref={closeButtonRef}
            type="button"
            aria-label={`关闭 ${entityLabels[target.entity]} 配置解析`}
            onClick={onClose}
          >
            ×
          </button>
        </header>
        <div className="es-drawer-content">
          <section className="es-drawer-configured">
            <header>
              <div className="section-title-with-help">
                <h3>Configured targets</h3>
                <HelpTip label="Configured targets">
                  Linkd 配置中允许该实体访问的 target 白名单。
                </HelpTip>
              </div>
              <span>{target.configuredTargets.length}</span>
            </header>
            {target.configuredTargets.length === 0 ? (
              <div>未配置 target</div>
            ) : (
              <ul>
                {target.configuredTargets.map((name) => (
                  <li key={name}>
                    <code>{name}</code>
                  </li>
                ))}
              </ul>
            )}
          </section>
          <div className="es-drawer-detail-grid">
            <TargetNameList
              entity={target.entity}
              title="物理索引"
              names={target.indices}
              tone="physical"
            />
            <TargetNameList
              entity={target.entity}
              title="Aliases"
              names={target.aliases}
              tone="alias"
            />
          </div>
        </div>
      </aside>
    </div>,
    document.body,
  );
}

function keepFocusInDrawer(event: KeyboardEvent<HTMLElement>) {
  if (event.key !== "Tab") {
    return;
  }
  const focusable = Array.from(
    event.currentTarget.querySelectorAll<HTMLElement>(
      'button:not([disabled]), select:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  );
  const first = focusable[0];
  const last = focusable.at(-1);
  if (!first || !last) {
    return;
  }
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

function TargetNameList({
  entity,
  title,
  names,
  tone,
}: {
  entity: EntityKind;
  title: string;
  names: string[];
  tone: "physical" | "alias";
}) {
  const [page, setPage] = useState(1);
  const currentPage = clampPage(page, names.length, targetDetailPageSize);
  const visibleNames = pageItems(names, currentPage, targetDetailPageSize);
  const totalPages = pageCount(names.length, targetDetailPageSize);

  return (
    <section
      className={`es-target-detail-list ${tone}`}
      aria-label={`${entityLabels[entity]} ${title}明细`}
    >
      <header>
        <div className="section-title-with-help">
          <h3>{title}</h3>
          <HelpTip label={title}>
            {tone === "physical"
              ? "配置 target 最终解析出的实际 Elasticsearch index。"
              : "配置 target 涉及的逻辑 alias 名称。"}
          </HelpTip>
        </div>
        <span>{names.length}</span>
      </header>
      {visibleNames.length === 0 ? (
        <div className="es-target-detail-empty">无解析结果</div>
      ) : (
        <ul>
          {visibleNames.map((name) => (
            <li key={name}>
              <code>{name}</code>
            </li>
          ))}
        </ul>
      )}
      {names.length > targetDetailPageSize && (
        <div className="es-target-pagination" aria-label={`${title}明细分页`}>
          <span>
            {rangeLabel(currentPage, targetDetailPageSize, names.length)}
          </span>
          <button
            type="button"
            aria-label={`${title}明细上一页`}
            disabled={currentPage === 1}
            onClick={() => setPage(currentPage - 1)}
          >
            ←
          </button>
          <span>
            {currentPage} / {totalPages}
          </span>
          <button
            type="button"
            aria-label={`${title}明细下一页`}
            disabled={currentPage === totalPages}
            onClick={() => setPage(currentPage + 1)}
          >
            →
          </button>
        </div>
      )}
    </section>
  );
}

function StoragePagination({
  label,
  page,
  pageSize,
  total,
  onPageChange,
  onPageSizeChange,
}: {
  label: string;
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
}) {
  const totalPages = pageCount(total, pageSize);
  return (
    <div className="es-storage-pagination" aria-label={label}>
      <label>
        <HelpLabel label="每页" help="每页最多展示的存储对象数量。" />
        <select
          aria-label="每页显示数量"
          value={pageSize}
          onChange={(event) => onPageSizeChange(Number(event.target.value))}
        >
          {listPageSizes.map((size) => (
            <option key={size} value={size}>
              {size}
            </option>
          ))}
        </select>
      </label>
      <span className="es-pagination-range" aria-live="polite">
        显示 {rangeLabel(page, pageSize, total)}
      </span>
      <div>
        <button
          type="button"
          aria-label="上一页"
          disabled={page === 1}
          onClick={() => onPageChange(page - 1)}
        >
          上一页
        </button>
        <span>
          第 {page} / {totalPages} 页
        </span>
        <button
          type="button"
          aria-label="下一页"
          disabled={page === totalPages}
          onClick={() => onPageChange(page + 1)}
        >
          下一页
        </button>
      </div>
    </div>
  );
}

function PageHeading({
  status,
  lastSuccessfulAt,
  isFetching,
  autoRefresh,
  onRefresh,
  onToggleAutoRefresh,
}: {
  status?: string;
  lastSuccessfulAt?: number;
  isFetching: boolean;
  autoRefresh: boolean;
  onRefresh: () => void;
  onToggleAutoRefresh: () => void;
}) {
  return (
    <div className="page-heading explorer-heading">
      <div>
        <p className="eyebrow">STORAGE TOPOLOGY</p>
        <h1>Elasticsearch Storage</h1>
        <p>查看实体读目标、alias 解析、物理索引容量与集群健康状态。</p>
      </div>
      <RefreshControls
        status={status}
        lastSuccessfulAt={lastSuccessfulAt}
        isFetching={isFetching}
        autoRefresh={autoRefresh}
        intervalSeconds={30}
        onRefresh={onRefresh}
        onToggleAutoRefresh={onToggleAutoRefresh}
      />
    </div>
  );
}

function StorageStat({
  label,
  value,
  help,
}: {
  label: string;
  value: string;
  help: string;
}) {
  return (
    <article className="storage-stat">
      <HelpLabel label={label} help={help} />
      <strong>{value}</strong>
    </article>
  );
}

function StorageEmptyState({
  title,
  filtered,
  onReset,
}: {
  title: string;
  filtered: boolean;
  onReset: () => void;
}) {
  return (
    <div className="es-storage-empty" role="status">
      <strong>{title}</strong>
      {filtered && (
        <>
          <span>请调整 entity 或关键字后重试。</span>
          <button type="button" onClick={onReset}>
            清空筛选
          </button>
        </>
      )}
    </div>
  );
}

function entityLabel(entity: EntityKind): string {
  return entityLabels[entity];
}

function storageTab(value: string | null): StorageTab {
  return value === "aliases" ? "aliases" : "indices";
}

function matchesEntity(
  entities: EntityKind[],
  selected: EntityKind | "all",
): boolean {
  return selected === "all" || entities.includes(selected);
}

function matchesKeyword(values: string[], keyword: string): boolean {
  return (
    keyword === "" ||
    values.some((value) => value.toLowerCase().includes(keyword))
  );
}

function filteredCountLabel(
  filtered: number,
  total: number,
  unit: string,
  hasFilters: boolean,
): string {
  return hasFilters ? `${filtered} / ${total} ${unit}` : `${total} ${unit}`;
}

function pageCount(total: number, pageSize: number): number {
  return Math.max(1, Math.ceil(total / pageSize));
}

function clampPage(page: number, total: number, pageSize: number): number {
  return Math.min(Math.max(1, page), pageCount(total, pageSize));
}

function pageItems<T>(items: T[], page: number, pageSize: number): T[] {
  const start = (page - 1) * pageSize;
  return items.slice(start, start + pageSize);
}

function rangeLabel(page: number, pageSize: number, total: number): string {
  const start = (page - 1) * pageSize + 1;
  const end = Math.min(page * pageSize, total);
  return `${start}–${end} / 共 ${total} 项`;
}

function sum(values: number[]): number {
  return values.reduce((total, value) => total + value, 0);
}

function formatCount(value: number): string {
  return new Intl.NumberFormat("en-US").format(value);
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let size = value;
  let unit = -1;
  do {
    size /= 1024;
    unit += 1;
  } while (size >= 1024 && unit < units.length - 1);
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[unit]}`;
}
