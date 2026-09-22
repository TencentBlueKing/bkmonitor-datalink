# TimeGraph 共享构图与资源边界

`topology` / `topology_range` 从 Matrix 直接写入共享节点、关系和时间位。
同一条关系在 60 个评估点出现时，只保留一个关系身份及一个 uint64 状态，
不创建 60 个 `graph.Graph`，也不先建立每个节点/关系的 timestamp map。
已有 path / multi_resource 接口继续使用逐时间点图。

属性不能只保存最后一次值。同一节点的相同属性版本共享一份 matcher，
不同版本用不相交的时间位表示；同一时间点的属性合并继续采用原有确定性规则。
输出快照持有自己的属性副本，清理构图状态或修改其他快照不会改写结果。
关系方向、类别、指标名及关系类型仍参与身份和过滤，H 跳之后补齐诱导边。

## 默认保护

下列配置位于 `cmdb.v1beta3`。这些是有限资源保护的默认值，不是吞吐或延迟 SLO。
除点数上限外，非正配置回退到表中的默认值；提高限制前需要重新做容量验证。

| 配置 | 默认值 | 生效位置 |
| --- | --- | --- |
| `max_shared_topology_points` | 60 | 实际网格；可调低，不能通过配置放宽到 61–64 点，超出范围的配置回退 60 |
| `max_shared_topology_concurrency` | 2 | 进程内拓扑请求准入；超限立即拒绝，普通指标查询不占这些名额 |
| `max_shared_topology_backend_bytes` | 16777216（16 MiB） | 单次后端 HTTP 响应的解压后大小；VM 调用显式传入，解码前拒绝 |
| `max_shared_topology_matrix_points` | 1000000 | 一个拓扑查询所有取数阶段的 Matrix 点数总和 |
| `max_shared_topology_output_elements` | 200000 | 物化的全部快照节点与边之和，包含之后可能被目标类型过滤的候选输出 |
| `max_shared_topology_output_bytes` | 67108864（64 MiB） | 快照物化前的 JSON 大小保守上界，以及 HTTP 整批响应大小 |

既有 `max_graph_nodes`、`max_graph_edges`、`max_graph_node_infos` 继续约束构图。
Matrix 单次序列数也受 `max_graph_nodes` 限制。边预算保留“端点对 × 有效时间点”
口径，节点属性预算保留“节点 × 有效时间点”口径，避免改为共享存储时意外放宽取数范围。
这些逻辑计数不能直接换算为堆大小；多关系身份仍分别保留在共享关系表中。

HTTP 请求体最多 1 MiB，`query_list` 最多 16 项。批次串行执行，响应累计也受预算限制，
不能用更多子查询放大单请求输出上限。通过代理调用时，准入名额持有到代理完成响应写出。
模型内部直接调用同样受准入限制，取消和所有返回路径释放名额。

JSON 物化预算采用包括转义开销的保守估计，可能在实际编码字节数达到上限前拒绝。
这是为了在创建大快照前终止。内部共享不消除 60 份快照本身的输出量。
HTTP 批次超限整体失败；单个查询触发模型预算时，沿用批次中的失败项语义。
外层 HTTP 200 仍不表示每个子查询成功。

后端字节拒绝状态沿派生 context 共享，即使聚合后端把该路由错误转换为 partial，
拓扑入口也会明确返回容量错误。取消不伪装为空图；普通后端 partial 继续保留在快照中。

## 观测

在 `build-time-graph-from-relations` 中检查：

- `graph-storage-mode=shared`，`graph-legacy-timepoint-count=0`。
- `graph-shared-edge-count` 是唯一关系身份数。
- `graph-attribute-version-count` 是实际保存的节点属性版本数。
- `graph-timepoint-count` 表示有节点观测的时间点数，不再等于内存中的图对象个数。

`graph-edge-count` 和 `graph-node-info-count` 仍是上述逻辑预算计数。
`build` span 包含后端取数，不能把它全部解释为内存图分配时间。
响应字节、Matrix 点数、输出元素/字节及并发拒绝均有固定 reason 指标；
见 [指标说明](timegraph-relation-observability.md)。

## 同口径测量

`TestSharedTopologyPipelineProbe` 为显式启用的本地测量入口。它从文件流提供固定
VM 响应，经过真实 HttpCurl、JSON 解码、VM Matrix 转换、构图、遍历、公共结果
转换、图清理和 JSON 编码。`legacy` 与 `shared` 只切换构图存储方式。
路由解析、真实存储以及公网 HTTP handler 不在这个隔离探针范围内。

探针为比较相同大响应，把两个模式的输出预算都临时设为 100 万元素 / 256 MiB；
这只作用于测试进程。并发档位比较核心流水线成本，不经过 HTTP 准入，不能据此
宣称默认服务允许四路请求。真实准入、批次及代理生命周期由另外的回归测试验证。

从仓库根目录执行：

```bash
cd pkg/unify-query
GOTOOLCHAIN=go1.24.4 GOMAXPROCS=4 go test -tags=jsonsonic -c -o /tmp/timegraph-pipeline.test ./cmdb/v1beta3
TG_PIPELINE_MODE=legacy TG_EDGES=1000 TG_POINTS=60 TG_CONCURRENCY=1 GOMAXPROCS=4 GOMEMLIMIT=768MiB /tmp/timegraph-pipeline.test -test.run '^TestSharedTopologyPipelineProbe$' -test.v -test.timeout=60s
TG_PIPELINE_MODE=shared TG_EDGES=1000 TG_POINTS=60 TG_CONCURRENCY=1 GOMAXPROCS=4 GOMEMLIMIT=768MiB /tmp/timegraph-pipeline.test -test.run '^TestSharedTopologyPipelineProbe$' -test.v -test.timeout=60s
```

每个模式使用新进程，外部约每 20 ms 采样 RSS；测试内部约每 5 ms 采样 HeapAlloc。
短峰值可能漏采。fixture 生成不在请求耗时内，RSS 采样则覆盖整个进程。
结果日志同时记录累计分配、GC、保留输出时的存活堆、清理后的堆和输出摘要。
累计分配量、GC 后存活堆与峰值 RSS 是不同指标，不应相互替代。

正式容量验收仍需固定真实业务画像和延迟/内存目标，并验证持续负载对普通查询的影响。
逐层过滤、候选批读与混合取数会改变进入系统的数据量，应在保持相同结果语义的前提下
独立比较；本次直接共享构图沿用已有候选取数策略。
