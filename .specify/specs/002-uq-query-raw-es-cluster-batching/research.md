# UQ `query_raw` Elasticsearch 攒批取证与方案研究

## 1. 研究结论

证据时间为 2026-07-29。源码与隔离实验结论对应文档基线；运行事实只代表该取证窗口，精确环境和时间范围保存在受控内部记录中，不作为长期当前状态提交。

本需求与历史 ES 多 DB 合并不重合。历史能力可以提供多索引客户端和测试经验，但不能恢复原算法。

推荐实现：

> 路由展平后，对 UQ 直连 ES 的成员按“有效连接 + 等价最终查询体”分组，再用一次 `_msearch` HTTP 请求承载多个独立搜索。

不推荐：

- 普通 multi-index `_search`；
- `filters` aggregation + `top_hits`；
- 重新打开 ES `is_merge_db`；
- 仅在单个 `query_list` 项内部合并。

选择 `_msearch` 的关键原因是：

1. 一个 HTTP 往返可以复用 ES 内部并发；
2. 每个 RT 仍有自己的索引、查询、total、命中、分页状态和错误；
3. 索引目标位于 NDJSON body，不进入 HTTP request line；
4. 重复 alias 和同物理索引不会被全局查询去重；
5. ES mapping 差异不再被强行放进一个搜索上下文。

## 2. 代码事实

### 2.1 路由与执行

| 事实 | 代码位置 | 结论 |
| --- | --- | --- |
| 请求先转为 `QueryReference` | `pkg/unify-query/service/http/query.go` | 攒批应位于展平之后 |
| 展平成员逐项提交到 ants pool | 同上 | 当前并发单元是单 RT |
| pool 大小为 `QueryMaxRouting` | 同上 | 线上当前实际为 4 |
| `ResultTableOptions` 按 `TableUUID` 写入 | 同上 | 批次返回必须逐成员解复用 |
| 多路由后由上层全局排序、裁剪 | 同上 | ES 层不能改成一个全局 hits 列表 |

原始请求含多个 `query_list` 项时，每项的 `ToQueryMetric` 会分别生成路由，但最终都进入同一个 `QueryReference`。因此，在 `QueryReference` 之后规划可以天然覆盖：

- 单 `data_label` 展开；
- 显式多 `query_list`；
- 两者混合。

### 2.2 Elasticsearch 单成员生命周期

`pkg/unify-query/tsdb/elasticsearch/instance.go` 的普通原始查询依次完成：

1. `getAlias`：按 DB 和时间范围生成 alias/pattern；
2. `fieldMapWithPhysicalIndexes`：IndexGet 或 GetMapping；
3. 构造该成员的 `FormatFactory`；
4. `buildESQuerySource`：条件、时间、query string、排序、字段投影、from/size、collapse；
5. 如有 search-after，再将其加到 `SearchSource`；
6. `_search`；
7. 逐 hit 解码、字段别名回填、时间字段归一；
8. 提取 `total hits` 和最后一条 sort；
9. 必要时执行缺 mapping 空索引重试。

这决定了正确的批处理不能只合并输入 `metadata.Query`。mapping 会影响最终字段类型、排序和查询体；最终分组门禁必须在每个成员完成准备之后再次判断。

### 2.3 `is_merge_db`

`pkg/unify-query/metadata/struct.go` 的 `GetMergeDBStatus` 明确只在以下情况返回 true：

- `IsMergeDB` 为 true；
- 存储是 BKSQL；
- measurement 是 Doris。

ES 分支固定返回 false，并有注释说明 mapping/字段不同会导致问题。

历史演进：

| 提交 | 行为 |
| --- | --- |
| `f2c8e82a` | 增加 ES/Doris 多 DB 合并 |
| `39f86f75` | 引入 `is_merge_db`；当时 ES 仍自动合并 |
| `e06ed571` | 关闭 ES 自动合并，原因是 mapping/字段差异 |

旧算法在一个 `query_list` 项的 `ToQueryMetric` 内选择代表查询并合并 DB。直接恢复会造成：

- 显式多个 `query_list` 项仍无法覆盖；
- 不同条件可能被代表项覆盖；
- 成员 TableUUID、options 和错误无法独立；
- 重复 alias 的逻辑成员被折叠。

## 3. 运行时证据

### 3.1 目标 Event Logs Trace

来源是脱敏生产 APM Event Logs，完整链路为：

`EventLogsResource` → `bk.unify_query.api=query_raw` → UQ `/query/ts/raw`。

一次请求内有两个显式 `query_list` 项：

| 项目 | 系统事件 RT | Kubernetes Event RT |
| --- | --- | --- |
| 存储 | Elasticsearch | Elasticsearch |
| 注册 ES 连接 | 相同 | 相同 |
| 实际连接 | 相同 | 相同 |
| 条件 | 一组事件状态条件 | 多组 Kubernetes 资源条件 |
| 当前请求 | 独立 `_search` | 独立 `_search` |

结论：连接相同不是充分条件。用户指出的“过滤条件不一样不能合并”已经由运行时请求体直接确认。

### 3.2 多路由样本

另一个真实 `query_raw` 请求由单个日志 `data_label` 展开：

| 指标 | 值 |
| --- | ---: |
| 展平路由 | 数十个 |
| ES 路由 | 占绝大多数 |
| ES 实际连接 | 多个 |
| 最大同时运行成员任务 | 4 |
| ES `_search` 次数 | 与 ES 成员数相同 |
| 高亮下 IndexGet | 每 ES 成员重复执行 |

至少一个连接包含十个以上成员。由于 mapping/字段差异，同一输入语义仍会生成多种最终 ES body。

这说明：

- 仅按连接分组不正确；
- 增加最终 body 门禁后，仍存在可减少搜索 HTTP 请求的多成员组；
- 首期保留逐成员 mapping 仍可覆盖主要 search 耗时；
- 后续若要继续优化高亮 mapping，应该另立需求。

### 3.3 重复 alias

样本中多个逻辑成员使用相同 alias/body。当前路径会分别查询并把命中分别归属于成员；响应 total 也逐项累加。

普通 multi-index `_search` 会把相同物理文档只返回一次，无法还原当前重复成员语义。`_msearch` 会保留两条独立子请求，符合现有行为。

### 3.4 Trace 显式多 RT

Trace 全局定位调用路径：

1. `TencentBlueKing/bk-monitor` 仓库 `bkmonitor/apm/resources.py`：
   `QueryTraceByIdsResource`；
2. 同仓库 `bkmonitor/apm/core/handlers/query/proxy.py`：
   读取预计算 RT 并调用 `TraceQuery`；
3. 同仓库 `bkmonitor/apm/core/handlers/query/trace_query.py`：
   每个预计算 RT 生成一个显式 query；
4. 所有 query 组成一次 UQ `query_raw`。

各项模板相同：

- TraceID `eq`；
- 同一字段投影；
- `min_start_time` 时间字段；
- 相同倒序；
- 相同 limit。

脱敏运行元数据可见相关候选 RT 均路由到同一个注册 ES 存储。当前只读模型不能确定运行时究竟启用了哪一组 RT，最近采样窗口也没有该接口请求，所以不能把“线上每次实际 query_list 数量”写成已观察事实。

仍可得出两个确定结论：

- Trace 代码确实存在显式多 `query_list` 场景；
- 该模板是本特性必须覆盖的正向测试形态。

## 4. 部署与兼容性证据

### 4.1 UQ 基线

只读取证已确认目标请求命中预期 UQ Deployment，运行源码与本地检查的调用链一致。内部环境名、镜像、Pod 和 commit 对应关系不写入公开仓。

运行配置：

| 配置 | 实际值 | 依据 |
| --- | ---: | --- |
| `http.query.max_routing` | 4 | 源码默认与目标运行 Trace 一致 |
| ES 查询 timeout | 已核对 | 原始值不进入公开规范 |
| 调用方 timeout | 已核对且大于 ES timeout | 原始值不进入公开规范 |

### 4.2 目标 ES 链路

目标 Event 两项使用同一个注册 ES 连接：

- UQ 连接地址和注册集群元数据一致；
- path 为 `/`；
- 未观察到会改写该请求的 sidecar；
- IndexGet 成功；
- GetMapping 只读探针成功；

因此，该目标是 UQ 到登记 ES endpoint 的直连链路，没有发现会额外限制 request line 的 sidecar 或 K8s HTTP gateway。

这不能证明服务端所有设置。当前只读能力无法读取：

- `http.max_initial_line_length`；
- `http.max_content_length`；
- `search.max_buckets`；
- `index.max_inner_result_window`；
- alias filter 和 search routing。

最近 7 天 UQ 日志没有匹配到 `too_long_frame_exception`，只能说明窗口内未观察到，不能证明阈值不存在。

### 4.3 ES 版本范围

目标部署包含多个 ES 代际和连接形态。`_msearch` 是拟支持 ES 代际已有的接口，但仍应通过
APM Trace 请求显式传入 `is_es_batch=true`，验证 UQ 客户端、认证层和特殊 endpoint 的实际
兼容性，不能把单一目标集群结果外推到全部连接。请求缺省或 `false` 时保持原逐 RT 路径。

## 5. 隔离语义实验

实验使用 ES 7.8 OSS 和仓库当前 `github.com/olivere/elastic/v7 v7.0.32`，不代表生产性能，只用于确认协议和结果语义。

### 5.1 普通 multi-index `_search`

优点：

- 只有一个逻辑搜索；
- URL 可通过 body 或特殊路径设计规避长度。

无法满足：

- 无法稳定判断 hit 属于哪个逻辑 RT；
- 相同 alias/物理索引会去重；
- 每个 RT 的 total、search-after 和错误不再独立；
- 不同 alias filter/routing 可能改变语义；
- 代表查询会覆盖成员条件。

结论：拒绝。

### 5.2 `filters` + `top_hits`

实验确认它能为 bucket 返回各自 hits，但存在不可接受差异：

1. 当前独立搜索在默认阈值下，两个各匹配 15000 的成员返回 total 值各 10000；UQ 累加为 20000。bucket 统计会返回精确 15000 + 15000，改变公共 `total`。
2. `top_hits` 受 `index.max_inner_result_window` 约束；本地阈值为 100，`from + size = 101` 失败。
3. top-level `track_total_hits` 不改变各 `top_hits` 的 total 行为。
4. 两个 alias 指向同一物理索引时，聚合搜索看到的是 alias 目标并集，无法保持每个 alias 的 filter/routing 隔离。
5. 200 个 bucket、每个 100 hits 的响应在单分片小数据实验中已约 3 MiB；不能直接外推生产，但证明响应会按成员窗口线性放大。
6. ES 可能 HTTP 200 同时带分片失败，错误隔离更复杂。

结论：拒绝。

### 5.3 `_msearch`

实验结果：

| 验证项 | 结果 |
| --- | --- |
| 请求路径 | 固定 `GET /_msearch` |
| 索引位置 | NDJSON header body |
| 不同条件 | 每个子请求独立 |
| total | 与各独立 `_search` 一致 |
| list/sort | 与各独立 `_search` 一致 |
| search-after | 每项独立保留 |
| 空结果 | 每项独立保留 |
| 子项错误 | 一个 404 子响应不影响其他子项 |
| 请求顺序 | responses 与 requests 同序 |

一次实验中：

- 7 个搜索的 NDJSON body 为 5221 字节；
- 其中一个 header 行为 4213 字节；
- HTTP request line 仍只有固定路径；
- ES 成功返回。

结论：符合“一次 ES HTTP 请求、多个独立 RT 搜索”的需求。

### 5.4 4096 request-line 边界

隔离 ES 配置 4096 字节 initial line 后，使用长索引列表测得：

| API | 最后成功规模 | 失败规模 |
| --- | ---: | ---: |
| IndexGet | 583 个短索引 | 584 |
| GetMapping | 582 | 583 |
| Search | 582 | 583 |

边界会受 base path、编码和具体索引长度影响，不能把“索引数量”作为通用预算。`_msearch` 把成员索引移入 body，直接消除新增批处理对 request line 的线性增长。

首期仍保留逐成员 IndexGet/GetMapping，因此单个 RT 自身若展开出超长索引列表，仍属于已有风险；本需求不声称修复所有 ES API 的 URL 长度问题。

## 6. 设计推论

### 6.1 为什么仍要求相同条件

协议上 `_msearch` 可以承载不同条件，但需求明确规定不同 RT 条件不同不能合并。首期采用更保守策略：

- 条件或其他有效执行语义不同，拆为不同 HTTP 请求；
- 相同条件但最终 mapping 派生 body 不同，也拆开；
- 只有连接和最终 body 都相同才一起发送。

这会少获得一部分理论收益，但把行为边界保持得可解释、可测试。

### 6.2 为什么不需要索引所有权门禁

普通 multi-index 搜索需要证明每个物理文档只属于一个逻辑成员；否则无法解复用。

`_msearch` 的每个 child 本来就是独立搜索，因此：

- alias 重叠允许；
- 物理索引重叠允许；
- 重复 alias 允许；
- 每项的 alias filter/routing 由 ES 独立应用。

这消除了旧方案最难证明的所有权前提。

### 6.3 为什么 mapping 仍逐成员准备

mapping 会影响：

- 查询字段类型；
- 排序 `unmapped_type`；
- 字段投影和别名；
- hit 解码；
- 缺 mapping fallback。

首期逐成员准备并使用各自 formatter，可以避免把 mapping 差异重新变成跨 RT 共享状态。线上样本显示 search 占主要工作量，因此即使不合并 mapping，请求数和排队仍有优化空间。

### 6.4 为什么不在未知失败时展开重试

一个 429、5xx 或 timeout 后立即改发 N 个 `_search` 会在 ES 已不稳定时放大请求。更安全的规则是：

- child 失败按成员处理；
- batch transport 失败按批处理；
- 首期 endpoint/media type 不支持时整批失败，不做自动单查回退；
- APM 去掉 `is_es_batch` 或传 `false` 是恢复原路径的手段。

### 6.5 为什么使用请求级 opt-in

功能启用使用 `/query/ts/raw` 顶层可选参数 `is_es_batch`，而不是 UQ 服务端功能开关或连接
范围配置。缺省/`false` 保持原逐 RT 路径，`true` 只允许进入 planner；不同连接、
conditions、最终 DSL 和装箱预算仍会拆组。APM 仅在
`TraceQuery.query_by_trace_ids` 的跨 RT TraceID 查询按需传 `true`。

兼容顺序是 UQ 先、APM 后。新 UQ 对未 opt-in 请求保持旧行为；旧 UQ 按现有 JSON 解码
行为忽略未知字段并继续旧路径。

### 6.6 已发现但不混入本功能的现存风险

1. 普通 `query_raw` 没有与 `query/ts` 相同的请求级 `query_list` 数量上限。批次的 `max_members` 只限制单次 `_msearch`，不限制请求总路由数。由于本需求明确要支持 200+ RT，不能在本变更中随意增加较小上限；应以 body、批次、共享并发和压测控制容量。
2. Handler 的数据源校验会跳过 nil query，但 `ToQueryReference` 随后直接解引用；`query_list:[null]` 存在 panic 风险。这是现有输入校验缺陷，应独立修复，不与 batch planner 混在同一个变更中。
3. 单个 RT 自身的 IndexGet/GetMapping URL 仍可能因日期 alias 数量过多而超长。本功能消除的是“跨 RT 攒批新增的搜索 URL 增长”，不是所有 ES metadata URL 风险。
4. 当前若干 span/log 会记录完整连接、headers 或查询体；本功能只保证新增观测不继续扩大暴露面，存量日志治理应独立实施。
5. 静态部署 values 不等于运行生效值。运行验收必须读取有效配置，不能仅凭 values 推断并发基线。

### 6.7 query golden 适配结论

现有 query golden 数据集继续冻结 `is_es_batch` 缺省时的 handler、路由展开和普通 ES
查询构造。本变更已经最小扩展 runner：`/_msearch` body 按非空 NDJSON 行解析，GET
`/_msearch` 使用 search fixture，并由请求 body 的顶层 `is_es_batch=true` 逐 case 开启。

新增 `es_raw_explicit_multi_rt_batch_001` 保留用户提供的生产 Trace 入口形态，使用脱敏固定
route/dependencies 经真实 handler 回放 expected output。由于缺少能与该入口唯一关联的
production outbound output，case 标记为 `outputs_kind=handler_replay` 和
`provisional_output`，不计入 production output 采样收敛。

据此采用两层门禁：

1. 运行现有完整 golden 数据集，证明参数缺省时没有查询构造回归；
2. 运行显式开启的 provisional golden，冻结双显式 RT 的 NDJSON `/_msearch` 构造；
3. 不同条件、child partial、响应解复用和故障粒度继续使用 httptest 集成 fixture。

后续捕获到完整、版本钉住且可关联的 production input/output 后，再按
`unify-query-golden-sampling` 的去重和来源规则判断是否升级来源。不得把 handler replay
或源码推导写成 production output。

## 7. 剩余边界分类

### 7.1 已形成并用于本地实现的结论

- 正负场景和分组语义已明确；
- 当前返回契约已定位；
- 方案协议语义已实验；
- 目标部署和源码一致性已确认；
- URL 风险有直接复现；
- 测试缺口已盘点。

### 7.2 部署后运行门禁

以下必须验证，但不需要继续向需求方索要材料：

1. UQ 实际客户端到目标 ES 的 GET-with-body/NDJSON；
2. 1 MiB 初始 body 预算在目标链路的余量；
3. `max_concurrent_searches` 对 ES rejected 和查询延迟的影响；
4. 200 成员下 UQ 内存和响应体；
5. ES 5.x/6.x/7.x 代表连接的兼容性；
6. 全批错误时的告警，以及去掉请求参数后恢复原路径的效果。

这些检查在 `validation.md` 中作为目标环境验收门禁执行。部署顺序必须是 UQ 先、APM 后；
旧 UQ 会按现有 JSON 解码行为忽略未知的 `is_es_batch` 并保持旧路径。
