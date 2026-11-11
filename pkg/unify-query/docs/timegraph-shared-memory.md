# TimeGraph 共享构图与资源边界

`topology` / `topology_range` 将 Matrix 写入请求内共享图。同一关系在 60 个评估点出现时，
只保存一个完整关系身份和一个 uint64 时间位。节点身份与相同属性版本也在查询内共享。
旧 multi_resource 接口的路径求值继续使用逐时间点图。

同一节点的不同属性版本使用不相交的时间位；同一时间点的属性合并遵循确定性规则。
输出拥有独立属性副本，构图状态清理后仍有效。H 跳遍历后返回可达节点的诱导边，
关系方向、类别、指标名和关系类型均参与身份与过滤。

## 配置

配置位于 `cmdb.v1beta3`，默认值如下。配置只约束请求的原始输入和部署参数；
构图、取数和输出的内部规模不是拒绝理由，实例资源按部署规格配置。

| 配置 | 默认值 | 含义 |
| --- | --- | --- |
| `max_hops` | 2 | topology 省略 H 时的查询深度 |
| `max_allowed_hops` | 6 | topology 显式深度上限；旧兼容查询枚举不受此限 |
| `max_shared_topology_points` | 60 | 网格点数，可调低；非正或大于 60 的配置回退到 60 |
| `max_shared_topology_request_bytes` | 1048576（1 MiB） | topology 请求体上限；代理路径按 `data` 内层载荷计 |
| `max_shared_topology_queries` | 16 | 单个 HTTP 批次查询条数 |
| `max_range_points` | 11000 | 兼容区间入口 `multi_resource_range` 的评估点数 |
| `max_relation_request_bytes` | 16777216（16 MiB） | 两个兼容入口的请求体上限；代理路径按 `data` 内层载荷计 |
| `max_relation_queries` | 10000 | 两个兼容入口单个 HTTP 批次的查询条数 |
| `max_shared_topology_concurrent_requests` | 8 | 进程内同时执行的请求数；其余请求排队等待，不设队列长度 |

上限类配置非正时使用内置默认值。查询内部期限内置 60 秒，较短的父 context 期限优先。
取消会中止排队、取数和后续构图；后端及 HTTP 期限同样生效。

单次后端响应以 16 MiB 为分批边界（程序常量，不是配置项），topology 与兼容入口相同：按身份
分批的读取超过时对半拆分重读；根读取、整类关系的宽读取、单个身份的读取和 info 读取超过时把
这次读取在精确网格上按时间对半重读，两半不重叠，直到单个评估点。单个评估点仍超过时返回
`max_response_bytes`，这是终态错误，兼容入口也不再改用下一条候选路径。构图规模（节点、关系身份、属性版本）、目标数和编码字节都不设上限。
具体取数和编码见[取数与输出](timegraph-output-optimization.md)。

## 并发与生命周期

每个请求按计划顺序取数并立即写图，只保留一份可复用 Matrix；接收下一份响应前释放旧份。
跨请求并发由上表的执行槽限制，默认最多 8 个执行请求，每个请求同时至多一次关系取数，
不再维护独立 worker 池或固定取数槽。

执行槽满时，请求按到达顺序等待，队列不设长度，只受本请求期限和取消约束；不因排队人数
或内存估算拒绝。等待计入同一查询期限。等待请求只读取有界 body，不取数或构图。

每个 HTTP 批次只获取一次执行槽，内部模型调用复用。直连和代理都在响应写完或失败清理后
释放；直接模型调用在返回时释放。成功、失败和取消路径均恢复活动计数。

## 错误与质量

响应逐项编码，编码完一项立即写出并释放，批次只保留正在编码的一项；snapshots 由共享图逐帧
构造并编码，不同时物化全部帧。累计字节只用于 trace 和负载指标。

请求体或批次数量超限使 HTTP 批次失败（topology 原因为 `max_topology_request_bytes`、
`max_topology_queries`，兼容入口为 `max_relation_request_bytes`、`max_relation_queries`）；单项模型失败通过该项 code/message 返回。单个评估点
的后端响应仍超过分批边界时返回 `max_response_bytes`，沿聚合子查询传播，不能被转成成功空图。
普通后端 partial 保留在结果中。

## 观测

`build-time-graph-from-relations` 的关键 trace 属性：

- `graph-storage-mode=shared`、`graph-legacy-timepoint-count=0`：采用共享构图。
- `graph-shared-edge-count`：唯一关系身份数。
- `graph-attribute-version-count`：保存的属性版本数。
- `graph-timepoint-count`：有节点观测的时间点数。

`graph-edge-count` 和 `graph-node-info-count` 是逻辑出现次数。
Matrix 物理调用数、复用次数与逻辑调用数分别记录；构图阶段累计串行取数耗时。
指标口径见[观测说明](timegraph-relation-observability.md)。
