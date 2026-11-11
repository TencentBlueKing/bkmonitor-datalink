# v1beta3 TimeGraph 关系查询观测

共享拓扑使用 `cmdb_topology_*` 指标，兼容 `multi_resource` 查询沿用
`cmdb_relation_*` 的 `route="timegraph"`。两者共用阶段、输入和构图指标。
导入唯一的[TimeGraph 仪表盘](timegraph-relation-dashboard.json)，UID 为
`uq-timegraph-v1beta3`；按目标数据源选择对应实例和时间窗。

## 请求结果与输出

以下指标均以 `unify_query_` 为前缀。

| 指标 | 标签与口径 |
| --- | --- |
| `cmdb_topology_operations_total` | `scope=request/query`、`query_mode=instant/range`、`result`；每个 HTTP 请求或模型查询记录一次终态 |
| `cmdb_topology_operation_seconds` | `scope`、`query_mode`；所有终态的耗时，HTTP 请求覆盖响应写出 |
| `cmdb_topology_rejections_total` | `query_mode`、`reason`；参数、原始输入规格或无法拆分的后端响应拒绝 |
| `cmdb_topology_size` | `query_mode`、`kind=points/nodes/edges/partial_snapshots`；成功响应的评估点数、节点/边出现次数和不完整快照数 |
| `cmdb_topology_payload_bytes` | `stage=request-body/backend-response/response-item`、`result`；读取的请求/后端字节或单项编码字节 |
| `cmdb_relation_route_total` / `cmdb_relation_route_seconds` | `route=timegraph`、`query_mode`；兼容关系查询的终态及耗时；计数器另有 `started` |
| `cmdb_relation_quality_total` | `query_mode`、`quality`、`reason`；兼容查询与 topology 子查询中的 partial、截断和拒绝原因，每个查询按固定原因去重；topology 的缺路由计为 `relation_no_physical_route`，与 `backend_partial` 分开 |
| `cmdb_relation_target_count` | `route`、`query_mode`；兼容查询最终返回的目标数量 |
| `cmdb_relation_query_list_size` | `route`、`query_mode`；批量 HTTP 请求的子查询数量 |

拓扑子查询终态为 `success/empty/partial/failed/rejected/canceled/timeout`。
任意快照不完整时记 `partial`，包括节点为空的情况。HTTP 空批次记 `empty`，
所有子查询失败记 `failed`，混合成功/失败或包含不完整结果记 `partial`。
校验失败在进入模型前记录一次查询拒绝，进入模型后由模型记录，避免重复计数。
业务结果以子查询终态核实；外层 HTTP 状态用于定位入口和传输错误。

`reason` 固定为 `invalid_request/max_shared_topology_points/max_response_bytes/`
`max_topology_queries/max_topology_request_bytes/other`。构图规模、目标数、编码字节、
内存估算和排队人数不是拒绝理由，不再有对应原因。

`cmdb_relation_quality_total` 的 `reason` 固定为 `backend_partial/vm_storage_coverage_mismatch/`
`relation_no_physical_route/source_info_no_route/target_info_failed/max_response_bytes/`
`max_shared_topology_points/other`。

快照和 compact 输出都按展开后的节点/边出现次数记录，便于比较相同请求；
数量不是跨时间去重值。compact 的字节收益查看 `response-item`。
超限读取只保留 limit+1 字节，因此 `request-body/backend-response` 可能是截断下界；
`response-item` 是单项的实际编码字节。这些值不等于最终整批 wire bytes 或堆内存。
整批编码字节查看 HTTP 根 span 的 `response-encoded-bytes`，写出结果查看 HTTP span。

## 阶段、输入与存储

| 指标 | 标签与口径 |
| --- | --- |
| `cmdb_timegraph_stage_seconds` | `stage`、`result`；阶段耗时，`_count` 同时表示执行次数 |
| `cmdb_timegraph_load_operations_total` | `stage`、`result`；实际取回 Matrix 的物理取数尝试，包含失败、取消和拒绝 |
| `cmdb_timegraph_load_seconds_total` | `stage`、`result`；上述调用的累计墙钟耗时，含准备、取数、转换和校验 |
| `cmdb_timegraph_load_size_total` | `stage`、`kind=series/points/label_bytes`；后端已返回的输入，即使随后校验拒绝仍计入 |
| `cmdb_timegraph_size` | `stage`、`kind=series/points`；通过校验的逻辑 Matrix 规模分布，包含请求内复用 |
| `cmdb_timegraph_storage_size` | `storage=shared/time-buckets`、`kind`、`result`；构图退出时的存储对象和逻辑时间记录，失败也记录 |
| `cmdb_timegraph_build_phase_seconds` | `storage`、`phase=matrix/local`、`result`；顺序 Matrix 调用的累计耗时与其余本地构图墙钟 |
| `cmdb_timegraph_matrix_queries_per_build` | `storage`、`kind=logical/physical/reused`、`result`；每次构图的逻辑查询、物理取数和请求内复用次数 |

输入计数仅按 `source-info/relation-edge/target-info` 阶段聚合。指标不携带资源名、
关系指标名、租户、请求 ID、matcher 或错误原文；具体请求和指标名通过 trace 定位。
复用的 Matrix 仍计入逻辑阶段与校验，物理输入计数不会重复累计。
`label_bytes` 为每条序列的标签名和值字符串长度之和，不包含容器或对象开销。
后端失败且未形成可信 Matrix 时只记录次数和耗时。

阶段覆盖 `build`、三类取数、`matrix-validation`、`apply-relation/`
`apply-source-info/apply-target-info`、`topology-state/topology-propagation/`
`topology-materialize/topology-convert/topology-encode/topology-traversal`、
`admission/admission-release/cleanup`。遍历总阶段包含 state 和物化等子阶段，
嵌套阶段不相加。`phase=local` 为构图墙钟减 Matrix 累计耗时后的非负余量，
包含配置、表达式和索引处理；它不是纯 CPU 时间，也不能由两个 P95 相减得到。

`storage_size.kind` 包含 `nodes/relations/attribute_versions/legacy_graphs/`
`edge_time_entries/node_time_entries`。`relations` 仅记录共享图的唯一关系身份；
`legacy_graphs` 是逐桶图对象数，共享模式为 0。`edge_time_entries` 是端点对与
有效时间的组合数，多关系同端点时不能直接用它除以关系数计算压缩率。
共享模式的 `attribute_versions` 是实际保存版本数，逐桶模式为节点×时间记录数。
存储指标的 `result` 描述构图结果，后端完整性仍由模型的 `partial` 终态表达。

## 请求资源与验收

- `cmdb_topology_admission_active`：活跃请求数，包含 HTTP 批次响应写出期间；嵌套模型调用不重复计数。
- `cmdb_topology_inflight{query_mode}`：正在执行的模型查询数。
- `cmdb_topology_admission_waiters`：等待执行槽的 FIFO 项数，不设上限；等待计入原请求期限。

取消和失败后释放执行槽，重复 release 不重复扣减。内存按进程 RSS、Go heap 和 GC 从
进程监控核实，结合 `cmdb_topology_size`、`cmdb_timegraph_storage_size` 和
`cmdb_topology_payload_bytes` 的请求规模校准实例规格；瞬时抓取可能错过短峰值。
资源保护机制见[共享构图与资源边界](timegraph-shared-memory.md)和
[取数与输出](timegraph-output-optimization.md)。

验收固定版本、窗口、step、lookBack、种子、H 和并发，再比较：

1. 查询终态、partial、拒绝原因及兼容接口返回目标。
2. 物理输入量、Matrix 复用和本地阶段耗时。
3. 共享存储对象数、展开输出量与编码字节。
4. 活动请求和等待项的释放，以及进程峰值和普通查询影响。

看板保留这些核心量，不按指标名拆分成本。首次抓取没有计数器基线，
`rate/increase` 无法还原抓取前的第一笔调用；无样本也不补为零。

## Trace

- HTTP 根和 item span：批量状态、参数、单项结果、响应编码和写出错误。
- `build-time-graph-from-relations`：存储模式、唯一节点/关系、属性版本、逻辑时间记录、Matrix 次数与构图耗时。
- `timegraph-query-matrix`：`query-stage`、`metric-name`、`matrix-returned`、返回序列/点/标签字节、复用、partial 和错误。
- `timegraph-validate-matrix` 与三类 `timegraph-apply-*-matrix`：网格校验、分批边界拒收和写图成本。
- `timegraph-build-shared-topology-state`、`timegraph-propagate-topology`：种子、已完成层和可达节点。
- `timegraph-materialize-topology` / `timegraph-compact-topology`、`timegraph-convert-topology`、`timegraph-encode-topology-item`：快照或 compact 物化、转换和单项编码字节。
- `timegraph-admission`、`timegraph-release-admission`、`timegraph-clean`：等待执行槽、释放及清理。
- `http-curl` 与 VM 转换 span：后端读取、解码、转换和失败位置。
- `http-response-encode` / `http-response-write`：最终 envelope 编码和 ResponseWriter 写出。

规格拒绝记录 `limit-reason/count/maximum`。阶段 span 记录聚合计数，
热点节点和边循环不创建逐元素 span。trace exemplar 可关联抽样请求；
持续流量与失败率以聚合指标核实。
