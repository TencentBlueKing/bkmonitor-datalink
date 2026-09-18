# Legacy VM relation observability

这份文档对应 legacy VM 关系接口：

- `POST /api/v1/relation/multi_resource`
- `POST /api/v1/relation/multi_resource_range`

SurrealDB/v1beta3 不使用这里的 `vm_legacy` 指标。

## Trace 观测点

请求级 span：`handler-api-relation-multi-resource` 或
`handler-api-relation-multi-resource-range`。

请求内单项 span：`handler-api-relation-multi-resource-item` 或
`handler-api-relation-multi-resource-range-item`。

重点属性：

- `query-count`：本次请求中的 `query_list` 数量；
- `all-path-query-count`：开启 `return_all_paths` 的查询数量；
- `failed-query-count` / `partial-failure`：批量项失败情况；
- `query-index`：批量项下标，用于定位某条请求；
- `executed-path-count` / `target-count`：单项路径和目标数量；
- `query-execution-mode`：`first_path` 或 `all_paths`；
- `query-candidate-path-count` / `query-path-error-count`：候选路径和路径失败数；
- `query-bk-biz-id`：最终解析到的业务 ID；
- `query-promql`、`query-path`、`query-result-series`、`query-result-points`：VM 查询细节。

当前 BKOP trace 已能看到 `build-metadata-query` 中的 `bk_biz_id` 和最终
VM 条件。新增属性用于区分批量项和多路径放大后的单次查询成本。

## 指标

固定 route 为 `vm_legacy`，避免和 v1beta3 的 VM 优先路由混在一起。

### 请求结果

```promql
sum by (query_mode, result) (
  rate(unify_query_cmdb_relation_route_total{route="vm_legacy"}[5m])
)
```

### 失败率

```promql
sum(rate(unify_query_cmdb_relation_route_total{route="vm_legacy",result="failed"}[5m]))
/
sum(rate(unify_query_cmdb_relation_route_total{route="vm_legacy",result=~"success|empty|partial|failed"}[5m]))
```

### P95 查询耗时

```promql
histogram_quantile(
  0.95,
  sum by (le, query_mode) (
    rate(unify_query_cmdb_relation_route_seconds_bucket{route="vm_legacy"}[5m])
  )
)
```

### 批量大小和候选路径数

```promql
histogram_quantile(0.95, sum by (le, query_mode) (
  rate(unify_query_cmdb_relation_query_list_size_bucket{route="vm_legacy"}[5m])
))

histogram_quantile(0.95, sum by (le, query_mode, execution_mode) (
  rate(unify_query_cmdb_relation_candidate_path_count_bucket{route="vm_legacy"}[5m])
))
```

### 路径失败和空结果

```promql
sum by (query_mode, result) (
  rate(unify_query_cmdb_relation_path_result_total{route="vm_legacy"}[5m])
)
```

### 目标数量 P95

```promql
histogram_quantile(0.95, sum by (le, query_mode, execution_mode) (
  rate(unify_query_cmdb_relation_target_count_bucket{route="vm_legacy"}[5m])
))
```

## Dashboard

已在业务 2 创建 `unify-query / Legacy VM Relation` 仪表盘：

- UID：`uq-legacy-vm-rel`
- 地址：https://bkmonitor.bkop.woa.com/?bizId=2#/grafana/d/uq-legacy-vm-rel
- 标签：`unify-query`、`cmdb`、`relation`、`vm_legacy`

当前包含以下面板：

1. 请求结果：`success`、`empty`、`partial`、`failed`；
2. 请求失败率；
3. instant/range P95 延迟；
4. `first_path/all_paths` 候选路径数 P95；
5. 路径结果：`success`、`empty`、`failed`；
6. 目标数量 P95；
7. 批量 `query_list` 大小 P95；
8. 观测口径和 Trace 排查说明，按接口 span 名称过滤
   `handler-api-relation-*`。

仪表盘通过 BKMonitor Dashboard as Code 创建，并已回读确认包含 8 个面板；7 条
PromQL 在业务 2 最近一小时均返回 HTTP 200。
