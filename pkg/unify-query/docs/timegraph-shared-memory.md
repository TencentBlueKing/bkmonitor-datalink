# TimeGraph 共享构图与资源边界

`topology` / `topology_range` 将 Matrix 写入请求内共享图。同一关系在 60 个评估点出现时，
只保存一个完整关系身份和一个 uint64 时间位。节点身份与相同属性版本也在查询内共享。
旧 multi_resource 接口的路径求值继续使用逐时间点图。

同一节点的不同属性版本使用不相交的时间位；同一时间点的属性合并遵循确定性规则。
输出拥有独立属性副本，构图状态清理后仍有效。H 跳遍历后返回可达节点的诱导边，
关系方向、类别、指标名和关系类型均参与身份与过滤。

## 配置

配置位于 `cmdb.v1beta3`，默认值如下。实际容量应按部署资源和查询负载校准。

| 配置 | 默认值 | 含义 |
| --- | --- | --- |
| `max_hops` | 2 | topology 省略 H 时的查询深度 |
| `max_allowed_hops` | 6 | topology 显式深度上限；旧兼容查询枚举不受此限 |
| `max_shared_topology_points` | 60 | 网格点数，可调低；非正或大于 60 的配置回退到 60 |
| `max_shared_topology_backend_bytes` | 16777216（16 MiB） | 单次后端响应解压后的字节上限 |
| `max_shared_topology_output_bytes` | 67108864（64 MiB） | HTTP 整批实际编码响应上限 |
| `max_shared_topology_request_bytes` | 1048576（1 MiB） | topology 请求体上限；代理路径按 `data` 内层载荷计 |
| `max_shared_topology_queries` | 16 | 单个 HTTP 批次查询条数 |
| `max_shared_topology_concurrent_requests` | 8 | 进程内执行请求数；FIFO 等待队列容量与之相同 |
| `max_shared_topology_reserved_bytes` | 4294967296（4 GiB） | 进程内所有在途阶段的总预约上限 |
| `shared_topology_memory_headroom_bytes` | 536870912（512 MiB） | 观测可用内存中保留的额外余量 |

资源限制配置非正时使用有限默认值。查询内部期限内置 60 秒，较短的父 context 期限优先。
取消会中止排队、取数和后续构图；后端及 HTTP 期限同样生效。

共享拓扑按同时存活的 Matrix、图、遍历、输出和编码结构预约内存，
不按累计逻辑点数或展开元素数重复收费。旧逐时间点图继续使用节点、边和结果数量限制；
共享图仍保留 `max_graph_node_infos` 属性版本限制。批次串行执行，前项保留的编码字节
计入后项执行期间的总预约。具体计量见[取数与输出](timegraph-output-optimization.md)。

## 并发与生命周期

每个请求按计划顺序取数并立即写图，只保留一份可复用 Matrix；接收下一份响应前释放旧份。
跨请求并发由上表的请求准入限制，默认最多 8 个执行请求，每个请求同时至多一次关系取数。
接收和驻留 Matrix 继续计入进程内总预约，不再维护独立 worker 池或固定取数槽。

执行槽满时，FIFO 队列最多接纳一波等待请求。等待计入同一查询期限；队列满明确拒绝。
等待请求只读取并预约有界 body，不取数或构图。执行中的内存增长不足直接失败。

每个 HTTP 批次只获取一次准入，内部模型调用复用。直连和代理都在响应写完或失败清理后
释放；直接模型调用在返回时释放。成功、失败和取消路径均恢复活动计数和预约。

## 预算错误与质量

DTO 在分配前预约容器、属性副本和保留字符串；编码前预约 scratch 与缓存，逐块累计实际
JSON 字节。JSON 保守上界只用于内存预约，不单独否决字节上限内的响应。

请求体、批次数量或整批响应超限使 HTTP 批次失败；单项模型预算失败通过该项 code/message
返回。后端字节拒绝沿聚合子查询传播，不能被转成成功空图。普通后端 partial 保留在结果中。

## 观测

`build-time-graph-from-relations` 的关键 trace 属性：

- `graph-storage-mode=shared`、`graph-legacy-timepoint-count=0`：采用共享构图。
- `graph-shared-edge-count`：唯一关系身份数。
- `graph-attribute-version-count`：保存的属性版本数。
- `graph-timepoint-count`：有节点观测的时间点数。

`graph-edge-count` 和 `graph-node-info-count` 是逻辑出现次数。
Matrix 物理调用数、复用次数与逻辑调用数分别记录；构图阶段累计串行取数耗时。
指标口径见[观测说明](timegraph-relation-observability.md)。
