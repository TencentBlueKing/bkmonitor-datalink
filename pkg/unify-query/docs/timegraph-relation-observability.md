# v1beta3 TimeGraph 关系查询观测

v1beta3 关系查询使用 TimeGraph。旧 `multi_resource` 模型指标使用
`route="timegraph"`；完整共享拓扑使用独立的 `cmdb_topology_*` 指标。
路径查询和拓扑查询共用 `cmdb_timegraph_*` 构图与取数指标。仪表盘 UID 为
`uq-timegraph-v1beta3`，业务为 2：

<https://bkmonitor.bkop.woa.com/?bizId=2#/grafana/d/uq-timegraph-v1beta3>

实现边界：TimeGraph 的图存储、TSDB 关系物化和路径查询位于
`cmdb/v1beta3`；版本无关的关系路径转换和排序逻辑位于 `cmdb` 公共包。
v1beta1 不再承载 TimeGraph 实现，HTTP path-resources 接口也直接使用 v1beta3 Model。

## 指标

共享拓扑时间网格保留请求起点，不按 step 向下对齐；源信息、关系边和目标信息使用
同一网格，VM 查询禁用可能移动采样点的缓存。当前 VM 协议使用整数秒，时间参数可用
秒或整秒对应的毫秒格式，step 必须为正整秒；非整秒精度在取数前明确拒绝。
后端样本偏离网格时返回错误，不转换成完整空图；instant 和 range 均保留 backend partial。

同类型多跳或 H 跳内可能回到起点类型时，关系取数取消种子下推，以保留后续边和
边界节点间的诱导边。路径查询的 `max_graph_results` 同时限制每层展开状态和最终累计
结果；中间层超限也会拒绝，即使后续过滤可能减少最终结果。显式路径仍受静态关系方向约束。

- `unify_query_api_request_total`：v1beta3 HTTP 请求量，按接口和状态区分；
- `unify_query_cmdb_relation_route_total`：TimeGraph 查询结果，`result` 为
  `started`、`success`、`empty` 或 `failed`；
- `unify_query_cmdb_relation_route_seconds`：TimeGraph 关系查询耗时；
- `unify_query_cmdb_relation_path_result_total`：候选路径整体结果；
- `unify_query_cmdb_relation_candidate_path_count`：候选路径数量；
- `unify_query_cmdb_relation_target_count`：最终目标数量；
- `unify_query_cmdb_relation_timegraph_result_count`：TimeGraph 返回的原始路径资源数量；
- `unify_query_cmdb_relation_timegraph_bucket_count`：range 查询产生的非空时间桶数量；
- `unify_query_tsdb_request_seconds`：底层 VictoriaMetrics 请求耗时。

共享拓扑和共用阶段的指标如下，均以 `unify_query_` 为前缀：

| 指标 | 标签与口径 |
| --- | --- |
| `cmdb_topology_operations_total` | `scope=request/query`、`query_mode=instant/range`、`result`；每个请求或子查询只记录一次终态 |
| `cmdb_topology_operation_seconds` | `scope`、`query_mode`；包含失败、拒绝、取消和超时的耗时 |
| `cmdb_topology_inflight` | `query_mode`；正在执行的模型查询数，所有返回路径均减一 |
| `cmdb_topology_size` | `query_mode`、`kind=points/nodes/edges/partial_snapshots`；返回结果的评估点数、所有快照节点/边的数量之和、不完整快照数 |
| `cmdb_topology_rejections_total` | `query_mode`、`reason`；固定的参数或容量拒绝原因 |
| `cmdb_timegraph_stage_seconds` | `stage`、`result`；构图、源信息/关系边/目标信息取数、拓扑遍历各阶段耗时，`_count` 同时表示阶段执行次数 |
| `cmdb_timegraph_storage_size` | `storage=shared/time-buckets`、`kind`、`result`；构图退出时的存储对象及逻辑时间记录数量，失败也记录 |
| `cmdb_timegraph_build_phase_seconds` | `storage`、`phase=matrix/local`、`result`；每次构图累计 Matrix 调用耗时与余下本地墙钟时间 |
| `cmdb_topology_admission_active` | 无业务标签；进程内活跃拓扑请求数，含代理响应写出期间；仅观测，不限制并发，普通/YOLO 模式相同 |
| `cmdb_topology_payload_bytes` | `stage=request-body/backend-response/response-item`、`result`；实际读取或单项编码字节 |
| `cmdb_timegraph_size` | `stage`、`kind`；构图节点、边、节点属性、时间桶数量，以及 Matrix 序列数、样本点数 |

子查询 `result` 为 `success/empty/partial/failed/rejected/canceled/timeout`。
有任意不完整快照时优先记 `partial`，即使节点为空也不记成 `empty`。
HTTP 请求 span 与 `scope=request` 耗时均覆盖实际响应写出；通过代理时延迟到代理完成写出及活动计数清理后结束。写出失败单独记录为错误，业务失败与编码/传输失败在不同 span 可区分。
HTTP `scope=request` 的空批次记 `empty`，所有子查询失败记 `failed`，
混合成功/失败或有不完整结果记 `partial`；非法 JSON 记 `rejected`。
外层 HTTP 200 不能代表子查询成功。HTTP 时间模式校验失败未进入模型时，也记录一次
`scope=query` 拒绝；进入模型后由模型统一记录，避免重复计数。

`reason` 只允许 `invalid_request/max_shared_topology_points/max_graph_nodes/`
`max_graph_edges/max_graph_node_infos/max_graph_results/max_targets/`
`max_response_bytes/max_topology_matrix_points/max_topology_matrix_series/`
`max_topology_output_elements/max_topology_output_bytes/`
`max_topology_queries/max_topology_request_bytes/other`。
共用阶段 `stage` 允许 `build/source-info/relation-edge/target-info/topology-traversal/`
`topology-state/topology-materialize/topology-convert/topology-encode/`
`apply-relation/apply-source-info/apply-target-info/matrix-validation/`
`topology-propagation/cleanup/admission/admission-release`。
错误原文、租户、资源类型、关系名称、matcher、trace ID 不作为指标标签；trace exemplar
仍按现有机制关联。构图规模在失败时也记录；响应和 Matrix 规模只记录成功返回的数据，
不会用失败时的零值污染结果规模分布。响应节点/边总数不是跨时间去重数量，也不是堆内存。

新指标不会合并到旧 `route_total` 中；旧 route、path result、target 和 bucket 指标
仍描述 `multi_resource` 适配路径，不能用它们的存在证明 topology 已被监控。

拓扑入口现已直接共享构图。时间点及边计数保留逻辑预算口径，不能把它们解释为
图对象数量或内存字节；构图模式、默认资源保护及完整流水线探针见
[共享构图与资源边界](timegraph-shared-memory.md)。

`candidate_path_count` 和路径结果使用 `execution_mode="all_paths"`，因为 TimeGraph
会在一次查询窗口内物化候选关系并统一遍历，不再按 SurrealDB path 逐条执行。

## 直接共享构图的观测口径

- `storage_size.kind` 只允许 `nodes/relations/attribute_versions/legacy_graphs/edge_time_entries/node_time_entries`。`relations` 是去重关系身份，`legacy_graphs` 才是逐桶图对象数量；共享模式应为 0。`edge_time_entries` 保持端点对×有效时间预算计数，多关系同端点时不能与 `relations` 直接当作压缩率。
- `attribute_versions` 在共享模式为保存的属性版本数，旧模式为节点×时间属性记录数。所有规模在构图退出时记录；`result` 是构图执行结果，`success` 不表示后端数据完整，partial 仍查看模型终态。
- Matrix 调用目前串行。`phase=matrix` 累计查询准备、后端读取、解码、Matrix 转换及校验耗时；`phase=local` 是整个 build 的剩余墙钟时间，包含配置、表达式构造、属性和目标索引处理。它不是纯 CPU 或纯 map 插入时间。不能相减两个 P95 来计算本地耗时。
- `topology-traversal` 保留原来的总阶段口径，含 state 准备与快照物化。新增 `topology-state`、`topology-materialize` 为其中的子阶段；`topology-convert` 为目标过滤与公共结果转换，`topology-encode` 为 HTTP 子项预算检查所用的一次 JSON 编码。嵌套阶段不能相加，也不代表完整 socket 写出耗时。
- `admission_active` 与已有 `inflight` 含义不同：前者按 HTTP 整批请求计一次直到响应写完，直接模型调用独立计数；后者计模型调用，嵌套不会给前者重复加一。活动计数不用于限流或排队，普通/YOLO 模式均不限制请求并发；取消/失败后清理，重复 release 不重复扣减。瞬时抓取可能错过短峰值。旧 `admission_limit` 指标和 `max_topology_concurrency` 拒绝原因已移除。
- `payload_bytes` 按阶段记录已读取/已编码字节。请求体或后端超限时只读到 limit+1，是截断下界，不是完整响应大小；后端为解压后数据，只记录进入 body 读取的调用。response-item 记录预算检查编码的单项，即使整批随后被拒绝，也已经产生了这些字节；其 `success` 只表示编码成功。均不等于线上整批最终 wire bytes 或堆内存。
- 非法 JSON 和超大请求体分别记 `invalid_request`、`max_topology_request_bytes` 一次拒绝；模型拒绝仍由模型记录，HTTP 不重复累计。

不要在每个请求执行 `runtime.ReadMemStats`、强制 GC 或用进程堆差值估计单请求内存。
容量验收仍需结合进程 RSS/Go heap、GC 和隔离探针。上述新增指标描述存储与工作量，
没有声称测出精确请求峰值。指标不包含租户、请求 ID、节点、关系名或错误全文标签。

## Trace

关键 span 及属性：

- `handler-api-relation-multi-resource-v1beta3` / `...range-v1beta3`：HTTP 批量请求；
- `handler-api-relation-path-resources` / `...range`：path-resources HTTP 入口；每个子查询记录 source、target、路径、matcher、step、结果数和失败数；
- `handler-api-relation-v1beta3-topology` / `...-range` 及对应 item span：共享拓扑 HTTP 入口；记录请求点数、时间范围、跳数、过滤条件、快照节点/边数和 partial 数；
- `timegraph-query-path-resources`、`timegraph-query-relation-path-resources` 及 range 变体：模型入口的参数校验、路径解析和输出结果；
- `timegraph-normalize-topology-grid`：共享拓扑时间网格规范化，记录原始范围、step、点数和可配置上限；`timegraph-normalize-lookback` 记录回溯窗口解析；
- `timegraph-resolve-topology-relations`：按 namespace、类别、关系类型和方向筛选关系；
- `timegraph-resolve-and-query` / `timegraph-resolve-paths`：路径来源、请求路径数、解析路径数和最终结果数；
- `build-time-graph-from-relations`：含失败路径的图规模、存储模式、旧图数、唯一关系、属性版本、累计 Matrix 点数与次数；`matrix-query-duration-seconds` 和 `local-build-duration-seconds` 拆分墙钟时间；
- `timegraph-build-config`：记录 namespace 级 schema 配置快照及节点、边、结果、节点属性上限；
- `timegraph-build-resource-info-query` / `timegraph-build-relation-query`：记录 source-info、relation-edge、target-info 查询构造及失败点；
- `timegraph-query-matrix`：每个 VM 或测试查询记录 `query-stage`、查询范围、series、point、partial 和错误；生产阶段值为 `source-info`、`relation-edge`、`target-info`；
- `timegraph-prepare-vm-query`：记录 QueryTs、PromQL 表达式长度和查询参数准备错误；
- `timegraph-resolve-querier`、`timegraph-adapt-instant-result` / `...range`：记录后端解析、TimeGraph 结果适配、候选路径、桶和目标数量；
- `timegraph-query-relation`：关系指标物化与路径遍历，记录 path、relation、原始结果、时间点、目标类型和路径节点数量；
- `timegraph-find-shortest-path` / `timegraph-find-relation-paths`：记录图时间点、候选节点、已见路径和结果数；
- `timegraph-find-shared-topology`：共享拓扑遍历和输出，记录图状态、源节点匹配、可达节点、跳数、快照节点/边和 partial 数；
- `topology-read-request-body` / `topology-decode-request` / `topology-get-model`：HTTP 读取、解码、模型获取；代理外层还有 `proxy-decode-request`；
- `timegraph-admission` / `timegraph-release-admission`：保留历史 span 名称，记录活动计数是否复用、当前活动数及清理；不做并发准入检查；
- `timegraph-create-storage` / `timegraph-plan-topology-fetch`：存储初始化和根关系过滤规划；
- `http-curl-response-headers` / `http-curl-read-body` / `http-curl-json-decode`：真实 HTTP 响应头、body 读取和 JSON 解码；自定义 decoder 用 `http-curl-decode-body`，不能再分出内部读取/解码；
- `victoria-metrics-vectorFormat` / `victoria-metrics-matrixFormat`：VM 对象转换为 Vector/Matrix，记录输出序列数；
- `timegraph-validate-matrix`：样本时间网格与累计预算校验；`timegraph-apply-source-info-matrix` / `timegraph-apply-relation-matrix` / `timegraph-apply-target-info-matrix`：按 Matrix 批次写图和属性/目标索引；
- `timegraph-build-shared-topology-state`：时间网格节点位图和过滤后边状态；
- `timegraph-propagate-topology`：种子匹配与 H 跳传播，记录种子数、`frontier-nodes-by-level`、`completed-levels` 和可达节点数，取消时保留已完成层统计；frontier 数组最多 64 项（含种子层），超出时标记 `frontier-levels-truncated`，总完成层数仍完整；
- `timegraph-materialize-topology`：快照生成与排序；记录 `output-budget-elements-used/limit`、`output-budget-bytes-used/limit`（保守估计，不是实际编码字节）；
- `timegraph-convert-topology`：目标过滤与公共结果转换；`timegraph-encode-topology-item`：子项 JSON 编码及 `encoded-item-bytes`；
- `http-curl`：已有响应头、body 读取、JSON 解码耗时和 body 字节；新增 `response-body-byte-limit`，保留失败前已读取字节；
- `timegraph-clean`：真实图清理，记录清理前规模及父 context 是否取消；构图失败而未返回图对象时仍交由 Go 回收，不伪造一次 Clean；
- `http-response-encode-write`：直接/代理最终 JSON 编码与 ResponseWriter 写出，记录状态、writer 报告的 body 字节和写出错误。这个字节数不是客户端实际收包确认，也不包含网络栈排空时间；
- 预算拒绝所在的模型、构图、Matrix、物化和 HTTP span 记录 `limit-reason/count/maximum`。HTTP 根记录 `request-body-bytes` 和 `response-budget-bytes-used/limit`，后者含保守 envelope 预留，不是实际完整响应长度。

代理创建的 context 传入内部 handler；父 span 在响应写出及 cleanup 回调之后结束。阶段回归检查 started/ended 数量相等、同 trace、父子 ID 与时间包围关系，并覆盖直接/代理的写出失败。

所有阶段 span 都通过 `span.End(&err)` 记录错误，包括参数校验、VM 查询、图构建、路径遍历和 context cancellation。热点节点、边循环不创建逐元素 span，只在阶段 span 中记录聚合计数，避免 trace 数量随图规模线性膨胀。

无采样不能等价为零请求或零结果；需要结合 `route_total`、`api_request_total` 和 trace
一起判断服务是否有流量。

仪表盘配置以 [timegraph-relation-dashboard.json](timegraph-relation-dashboard.json)
形式保存在仓库中，可通过 Dashboard as Code 重新发布。
