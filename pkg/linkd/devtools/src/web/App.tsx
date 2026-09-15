import { useQuery } from "@tanstack/react-query";
import { Component, lazy, type ReactNode, Suspense, useState } from "react";
import {
  NavLink,
  Navigate,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";

import { getCapabilities } from "./api";
import { PageQueryFailureContext } from "./navigation";
import { ExplorerPage } from "./pages/ExplorerPage";
import { TimeContext, type TimeMode } from "./time";

const OverviewPage = lazy(() =>
  import("./pages/OverviewPage").then((module) => ({
    default: module.OverviewPage,
  })),
);
const ElasticsearchPage = lazy(() =>
  import("./pages/ElasticsearchPage").then((module) => ({
    default: module.ElasticsearchPage,
  })),
);
const CleanerPage = lazy(() =>
  import("./pages/CleanerPage").then((module) => ({
    default: module.CleanerPage,
  })),
);
const LifecyclePage = lazy(() =>
  import("./pages/LifecyclePage").then((module) => ({
    default: module.LifecyclePage,
  })),
);
const ControlPlanePage = lazy(() =>
  import("./pages/ControlPlanePage").then((module) => ({
    default: module.ControlPlanePage,
  })),
);
const KafkaPage = lazy(() =>
  import("./pages/KafkaPage").then((module) => ({
    default: module.KafkaPage,
  })),
);
const RedisPage = lazy(() =>
  import("./pages/RedisPage").then((module) => ({
    default: module.RedisPage,
  })),
);
const ConfigPage = lazy(() =>
  import("./pages/ConfigPage").then((module) => ({
    default: module.ConfigPage,
  })),
);

type NavigationItem = {
  to: string;
  label: string;
  glyph: string;
  requiresElasticsearch?: boolean;
};

const navigationGroups: Array<{
  label: string;
  items: NavigationItem[];
}> = [
  {
    label: "总览",
    items: [{ to: "/overview", label: "处理状态", glyph: "◫" }],
  },
  {
    label: "模块",
    items: [
      { to: "/cleaner", label: "Cleaner", glyph: "C" },
      { to: "/lifecycle", label: "Lifecycle", glyph: "Y" },
      { to: "/control-plane", label: "Control Plane", glyph: "P" },
    ],
  },
  {
    label: "核心数据",
    items: [
      { to: "/explore/events", label: "Events", glyph: "E" },
      { to: "/explore/alerts", label: "Alerts", glyph: "A" },
      { to: "/explore/alert-logs", label: "Alert Logs", glyph: "L" },
    ],
  },
  {
    label: "存储",
    items: [
      {
        to: "/storage/elasticsearch",
        label: "ES Storage",
        glyph: "S",
        requiresElasticsearch: true,
      },
      { to: "/infrastructure/kafka", label: "Kafka", glyph: "K" },
      { to: "/infrastructure/redis", label: "Redis", glyph: "R" },
    ],
  },
  {
    label: "系统",
    items: [{ to: "/config", label: "Configuration", glyph: "⚙" }],
  },
];

export function App() {
  const [timeMode, setTimeMode] = useState<TimeMode>("local");
  const [pageQueryFailed, setPageQueryFailed] = useState(false);
  const capabilities = useQuery({
    queryKey: ["capabilities"],
    queryFn: getCapabilities,
  });

  return (
    <PageQueryFailureContext.Provider value={setPageQueryFailed}>
      <TimeContext.Provider value={timeMode}>
        <div className="app-shell">
          <aside className="sidebar">
            <div className="brand">
              <span className="brand-mark">L</span>
              <div>
                <strong>Linkd</strong>
                <small>DEVTOOLS</small>
              </div>
            </div>
            <nav aria-label="主要导航">
              {navigationGroups.map((group) => (
                <section className="nav-group" key={group.label}>
                  <h2 className="nav-group-title">{group.label}</h2>
                  <div className="nav-group-items">
                    {group.items
                      .filter(
                        (item) =>
                          !item.requiresElasticsearch ||
                          capabilities.data?.storage.elasticsearch.configured,
                      )
                      .map((item) => (
                        <NavLink
                          key={item.to}
                          to={item.to}
                          reloadDocument={pageQueryFailed}
                          data-reload-document={
                            pageQueryFailed ? "true" : undefined
                          }
                          className={({ isActive }) =>
                            isActive ? "active" : ""
                          }
                        >
                          <span className="nav-glyph">{item.glyph}</span>
                          {item.label}
                        </NavLink>
                      ))}
                  </div>
                </section>
              ))}
            </nav>
            <div className="sidebar-footer">
              <span
                className={`source-dot ${capabilities.isSuccess ? "online" : ""}`}
              />
              {capabilities.isSuccess
                ? "本机连接层已就绪"
                : capabilities.isError
                  ? "连接层不可用"
                  : "正在检查连接层"}
            </div>
          </aside>
          <main className="workspace">
            <header className="topbar">
              <div>
                <span className="environment-label">
                  LOCAL OPERATIONS CONSOLE
                </span>
              </div>
              <div className="topbar-actions">
                <span className="readonly-pill">READ ONLY</span>
                <button
                  className="timezone-button"
                  type="button"
                  onClick={() =>
                    setTimeMode((current) =>
                      current === "local" ? "utc" : "local",
                    )
                  }
                >
                  {timeMode === "local" ? "本地时间" : "UTC"}
                </button>
              </div>
            </header>
            <div className="page-container">
              <PageRoutes />
            </div>
          </main>
        </div>
      </TimeContext.Provider>
    </PageQueryFailureContext.Provider>
  );
}

function PageRoutes() {
  const location = useLocation();
  const resetKey = `${location.pathname}${location.search}`;
  return (
    <PageErrorBoundary resetKey={resetKey}>
      <Routes key={location.pathname} location={location}>
        <Route
          path="/overview"
          element={
            <Suspense
              fallback={<div className="page-loading">正在加载模块…</div>}
            >
              <OverviewPage />
            </Suspense>
          }
        />
        <Route
          path="/cleaner"
          element={
            <Suspense
              fallback={<div className="page-loading">正在加载 Cleaner…</div>}
            >
              <CleanerPage />
            </Suspense>
          }
        />
        <Route
          path="/lifecycle"
          element={
            <Suspense
              fallback={<div className="page-loading">正在加载 Lifecycle…</div>}
            >
              <LifecyclePage />
            </Suspense>
          }
        />
        <Route
          path="/control-plane"
          element={
            <Suspense
              fallback={
                <div className="page-loading">正在加载 Control Plane…</div>
              }
            >
              <ControlPlanePage />
            </Suspense>
          }
        />
        <Route
          path="/explore/events"
          element={<ExplorerPage key="events" entity="events" />}
        />
        <Route
          path="/explore/alerts"
          element={<ExplorerPage key="alerts" entity="alerts" />}
        />
        <Route
          path="/explore/alert-logs"
          element={<ExplorerPage key="alert-logs" entity="alert-logs" />}
        />
        <Route
          path="/storage/elasticsearch"
          element={
            <Suspense
              fallback={<div className="page-loading">正在加载模块…</div>}
            >
              <ElasticsearchPage />
            </Suspense>
          }
        />
        <Route
          path="/infrastructure/kafka"
          element={
            <Suspense
              fallback={<div className="page-loading">正在加载 Kafka…</div>}
            >
              <KafkaPage />
            </Suspense>
          }
        />
        <Route
          path="/infrastructure/redis"
          element={
            <Suspense
              fallback={<div className="page-loading">正在加载 Redis…</div>}
            >
              <RedisPage />
            </Suspense>
          }
        />
        <Route
          path="/config"
          element={
            <Suspense
              fallback={<div className="page-loading">正在加载配置…</div>}
            >
              <ConfigPage />
            </Suspense>
          }
        />
        <Route path="*" element={<Navigate to="/overview" replace />} />
      </Routes>
    </PageErrorBoundary>
  );
}

class PageErrorBoundary extends Component<
  { children: ReactNode; resetKey: string },
  { error?: Error }
> {
  state: { error?: Error } = {};

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidUpdate(previous: Readonly<{ resetKey: string }>) {
    if (previous.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: undefined });
    }
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <section className="page-error-state" role="alert">
        <p className="eyebrow">PAGE RECOVERY</p>
        <h1>当前页面加载失败</h1>
        <p>侧边栏仍可正常切换；你也可以重试当前页面。</p>
        <button
          className="primary-button"
          type="button"
          onClick={() => this.setState({ error: undefined })}
        >
          重试当前页面
        </button>
      </section>
    );
  }
}
