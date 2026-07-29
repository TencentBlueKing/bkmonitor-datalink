# UQ `query_raw` Elasticsearch 按连接攒批需求规范

## 1. 文档状态

| 项目 | 内容 |
| --- | --- |
| 状态 | 请求级 opt-in 契约调整中；尚未提交、部署或执行目标环境验收 |
| 日期 | 2026-07-29 |
| 接口范围 | `/query/ts/raw` |
| 首期存储范围 | UQ 直连 Elasticsearch |
| 不含范围 | `/query/ts/raw_with_scroll`、BKData Elasticsearch 代理、其他 TSDB |
| 取证日期 | 2026-07-29 |
| 证据留存 | 本文只保留脱敏结论，不提交内部环境、连接和部署标识 |

本文的“攒批”采用以下确定解释：

> 在一次 Elasticsearch `_msearch` HTTP 往返中承载多个相互独立的搜索子请求。它减少 UQ 到 ES 的 HTTP 请求数并把子查询并发交给 ES，但不把多个 RT 改写成一个共享 `_search`。

因此，RT 的查询条件、索引目标、返回归属、`total` 和分页状态仍逐项隔离。不同有效过滤条件的成员不得进入同一批次。

需求中的“按 ES 集群”在实现契约中收紧为“按有效连接”：同一物理集群若 endpoint、认证或请求头上下文不同，也必须拆组。目录 slug 保留初始需求用语，不代表只比较 cluster name。

## 2. 背景

APM 跨应用 TraceID 原始数据检索会涉及大量 RT。规划中的入口会把 200+ 应用通过虚拟表收敛成一个 `data_label` 请求 UQ；当前 UQ 在路由展开后仍按 RT 各发一次 Elasticsearch `_search`，并由全局 `QueryMaxRouting` 控制并发。

线上运行基线确认 `http.query.max_routing` 未被部署配置覆盖，实际使用源码默认值 `4`。大量同连接 RT 会在 UQ 侧排队，不能复用 ES 对多个搜索的内部并发能力。

本优化还必须覆盖原始请求本身含多个 `query_list` 项的场景，而不能只覆盖单个 `data_label` 展开多个 RT 的场景。

## 3. 已确认事实

### 3.1 当前源码行为

1. `pkg/unify-query/service/http/query.go` 的 `queryRawWithInstance` 先通过 `ToQueryReference` 展平路由，再按展平后的 `metadata.Query` 逐项执行。
2. 每项查询通过独立的 `prometheus.GetTsDbInstance` 获取实例，并调用一次 `QueryRawData`。
3. 执行池大小是 `QueryMaxRouting`；`pkg/unify-query/service/http/hook.go` 的默认值为 `4`。
4. Elasticsearch `QueryRawData` 当前逐项完成：
   - 生成日期索引或 alias/pattern；
   - `IndexGet`，失败时回退 `GetMapping`；
   - 按该成员的 mapping 生成最终 `SearchSource`；
   - 发起一次 `_search`；
   - 按该成员的 field map 解码、别名回填并生成 `ResultTableOption`。
5. 普通多 RT 返回会按时间全局排序、裁剪；响应 `total` 是各成功成员 ES `total hits` 的和，`result_table_options` 按 `TableUUID` 保存。
6. 当前存在“排序字段在历史空索引缺 mapping”后的成员级窄化重试逻辑。新路径必须保留该逻辑和它的适用边界。

### 3.2 `is_merge_db` 与本需求的关系

`is_merge_db` 不是本需求的开关，也不能直接复用为 ES 攒批开关。

| 对比项 | 现有 `is_merge_db` | 本需求 |
| --- | --- | --- |
| 当前生效存储 | 仅 Doris/BKSQL | Elasticsearch |
| 生效位置 | 单个 `query_list` 项的路由转换内部 | 所有路由展平之后 |
| 语义 | 多 DB 改写为一个代表查询 | 多个独立搜索装入一次 `_msearch` |
| 多个显式 `query_list` 项 | 不覆盖 | 必须覆盖 |
| 成员条件和身份 | 可能被代表项覆盖 | 必须逐项保留 |

历史提交曾启用 ES 多 DB 合并，后来因不同 RT mapping/字段不一致而关闭。可复用的是 `DBs`、多索引客户端能力和相关测试经验；不能复用“选择一个代表查询并把 DB 合起来”的语义。

### 3.3 真实负例：同连接但条件不同

用户提供的生产 Trace 已直接归因到 APM Event Logs 请求：

- 一次 `/query/ts/raw`；
- 原始 `query_list` 长度为 `2`；
- 两项都是 Elasticsearch，并使用同一个实际连接；
- 一项查询系统事件并排除恢复事件；
- 另一项查询 Kubernetes Event，包含多组资源类型和字段条件；
- UQ 实际发出了两个并行 `_search`。

这是“同连接但不得攒批”的确定负例。分组不能只看存储标识、连接或索引，必须识别最终有效查询语义。

### 3.4 真实正向负载：虚拟表展开

另一个脱敏生产 `query_raw` 样本中：

- 原始 `query_list` 长度为 `1`；
- 一个 `data_label` 展开为数十个路由；
- 绝大部分成员是 Elasticsearch，分布在多个实际连接；
- 至少一个连接内包含十个以上成员；
- 当前仍按 ES 成员逐项产生 `_search`；
- 开启高亮后每成员会重复取得字段 mapping；
- Trace 计算得到同时运行的成员任务上限为 `4`。

同一输入语义因 mapping/字段可用性生成了多种最终查询体。按“实际连接 + 最终查询体”分组后，仍有多个成员组可以减少 HTTP 请求。

这个样本是日志虚拟表，不等同于 APM 200+ 应用场景；它证明的是 UQ 当前路由展开和逐 RT 执行形态。

### 3.5 Trace 显式多 `query_list` 场景

bk-monitor 源码已确认 Trace 全局定位存在显式多 RT 用法：

- `TencentBlueKing/bk-monitor` 仓库 `bkmonitor/apm/resources.py` 的 `QueryTraceByIdsResource`；
- 同仓库 `bkmonitor/apm/core/handlers/query/trace_query.py` 按预计算 RT 逐项构造查询；
- 每项使用相同 TraceID 条件、字段、时间字段、排序和 limit；
- 最终发起一个含多个显式 `query_list` 项的 UQ `query_raw` 请求。

脱敏运行元数据确认相关候选 RT 均为 Elasticsearch 且共享同一注册存储，具备形成候选批次的部署条件。最近采样窗口没有观察到该接口调用，因此这里的“调用形态”是源码事实，“当前请求频率”不是线上已确认事实。

用户提供的 Event Logs Trace 不是该全局定位入口；它是另一个真实的显式双 RT 负例。

### 3.6 实现前测试覆盖与本次新增覆盖

实现前已有测试覆盖：

- 单个 `data_label` 展开多个 ES RT 后仍分别查询；
- 两个 ES 路由的全局排序和裁剪；
- 多路由部分成功；
- 多 `query_list` 的 from、multi-from、search-after 和 scroll 形态。

但实现前的多 `query_list` 用例中的成员通常不是同一个实际连接，无法验证本需求。当前 query golden
数据集缺少以下查询构造形态：

- 同连接、同语义、可攒批的多 RT；
- TraceID 精确检索；
- 显式多 `query_list` 的 ES 攒批。

本次新增的单元和 httptest 集成测试已经覆盖：

- 单个 `data_label` 展开两个同连接、同条件 RT 后形成一个 `_msearch`；
- 两个显式 `query_list` 项以相同 TraceID 条件形成一个 `_msearch`；
- 两个显式 `query_list` 项条件不同时保持两个 `_search`；
- 固定 `/_msearch` RequestURI、原始 NDJSON、末尾换行和超过 4096 字节的 NDJSON header；
- child 部分失败、整批 transport 失败不展开单查、最终 body 拆组、成员/body 预算尾单；
- 高亮 mapping 复用、成员级缺 mapping retry、成员归属、`total`、options 和 routeInfo。
- 同一 fixture 在 `is_es_batch` 缺省/`false` 和 `true` 时的 list、`total`、options、result table ID 与 status 精确差分；
- 200 成员有界 dispatcher、context 取消，以及 preparation/direct single/batch 共享
  `QueryMaxRouting`；
- `QueryMaxRouting=1` 时 missing-mapping 首次 child、空检查和单成员 retry 串行完成，且不重放成功 sibling。

这些 wire 和故障粒度主要由单元/httptest 集成测试承担；query golden 负责冻结可稳定重放的
下游请求构造。

本变更把 golden runner 最小扩展为可逐行解析 `_msearch` NDJSON，并新增
`is_es_batch=true` 的显式双 `query_list` case。该 case 保留用户提供的生产 Trace 入口
形态，route、dependencies 和 expected output 使用脱敏固定 fixture；expected 来源标记为
`outputs_kind=handler_replay` 和 `provisional_output`。既有未携带该参数的完整 golden
数据集继续作为“请求参数缺省时查询构造不变”的回归门禁。

当前仍缺少能够与该生产入口唯一关联的 outbound output，因此新增 case 不计入 production
output 采样收敛。后续捕获到版本钉住且可关联的 production input/output 后，再按 query
golden 采样流程判断是否升级来源；不得把本地 handler 回放写成 production output。

## 4. 目标

1. 对 `/query/ts/raw` 展平后的 ES 成员按实际连接和等价查询语义攒批。
2. 同时覆盖：
   - 一个 `data_label` 展开多个 RT；
   - 原始 `query_list > 1`；
   - 两者混合。
3. 用固定路径 `/_msearch` 将每个成员的索引目标放入 NDJSON 请求体，避免批量索引进入 URL。
4. 保持成功响应、成员归属、全局排序、裁剪、`total`、分页 option 和部分成功契约。
5. 提供请求级显式控制，并保留服务端容量上限、低基数观测和即时回退能力。

## 5. 非目标

1. 不重新启用历史 ES `is_merge_db`。
2. 不把多个 RT 改写成一个普通 multi-index `_search`。
3. 不在首期改造 scroll/slice 下载路径。
4. 不在首期支持 BKData Elasticsearch 代理；其 GET-with-body 和 `_msearch` 代理契约需单独验证。
5. 不在首期合并 IndexGet/GetMapping 请求；mapping 查询仍逐成员执行。
6. 不修改 UQ 响应字段；请求仅新增顶层可选参数 `is_es_batch`。
7. 不调整全局 `QueryMaxRouting` 的默认值。
8. 不顺带修复现有日志中记录连接、请求头或完整查询体的问题；新代码不得继续扩大该暴露面。
9. 不在本变更增加 `query_list` 总量上限或修复 nil query 输入；它们是已识别的独立输入治理问题。

## 6. 功能需求

### FR-01：在路由展平后规划

攒批输入必须是 `ToQueryReference` 展平、空 ES 索引前缀过滤、search-after 已完成 RT 跳过和 multi-RT from/size 重写之后的成员。

不得在单个 `query_list` 项的 `ToQueryMetric` 内完成攒批，否则无法覆盖显式多 `query_list`。

### FR-02：首期资格

成员同时满足以下条件才可进入候选集合：

1. 接口是普通 `/query/ts/raw`；
2. `StorageType` 为 Elasticsearch；
3. `SourceType` 不是 BKData 代理；
4. 不是 scroll/slice 请求；
5. 顶层请求参数 `is_es_batch=true`；
6. 同一候选键至少有两个成员。

不满足条件的成员继续走现有单查询路径。

`is_es_batch` 缺省或为 `false` 时不得调用 batch planner，完整走原逐 RT 路径。请求 opt-in
只允许进入规划器，不会跳过实际连接、预分组指纹、最终 DSL 指纹、成员数或 body 预算等
资格门禁。

### FR-03：实际连接隔离

同一批次的成员必须使用完全相同的有效连接和认证上下文。分组身份至少包含：

- ES 地址；
- 用户名和凭据身份；
- 有效请求头身份；
- 会影响客户端行为的必要连接配置。

分组身份只能在进程内比较；日志、Trace 和指标只记录不可逆、低基数的连接标识，不能记录地址、凭据、请求头或可还原散列输入。

### FR-04：不同查询语义不得同批

规划器采用两层门禁：

1. 预分组指纹：在 mapping I/O 前比较条件、query string、时间字段、字段投影、排序、from/size、search-after、collapse 等执行语义；明显不同者直接分开，避免无意义等待。
2. 最终指纹：每个成员独立完成 mapping 和 `SearchSource` 构造，并在应用 search-after 后对最终请求体做稳定序列化；只有最终请求体完全相同者才能同批。

索引目标和输出身份不进入最终请求体指纹，因为它们由每个 `_msearch` 子请求头和成员上下文独立保存。

任何指纹计算不确定、序列化失败或字段无法确认的成员都不得攒批：尚未准备时走现有单查询路径，已经成功准备时走 prepared single executor，避免重复 mapping I/O。mapping、DSL 构造等查询准备本身失败时，按当前契约记录该成员错误，不得以“回退”为名重复查询。

高亮阶段的 `QueryFieldMap` 是当前 HTTP 层的可选预取，不是上述正式准备。候选成员不能只缓存它返回的 field map，而应通过 ES 内部窄接口预取成员私有的完整字段元数据，包括 alias/index targets、physical indexes 和 field map：完整预取成功时供后续 `PrepareRawQuery` 复用；`PrepareRawQuery` 会重新在本地派生 alias 以校验复用身份，但不得重复远端 `IndexGet`/`GetMapping`。预取失败时必须保留现有行为，由正式准备再完整执行一次 alias/mapping 获取。只有正式准备失败才生成成员错误，不得因开启攒批把“预取失败、正式准备成功”改成失败。

### FR-05：每个 RT 保持独立子查询

同一批次内，每个成员对应一个 `_msearch` header/body 对：

- header 使用该成员原始 alias/pattern 列表；
- body 使用该成员已构造的 `SearchSource`；
- 响应按请求顺序与成员一一对应；
- 重复 alias 的两个逻辑成员仍产生两个子请求，不能去重；
- 每项使用自己的 field map、formatter、TableUUID 和结果 option 解码。

### FR-06：返回契约等价

启用和关闭攒批时，成功场景必须满足：

1. 每个成员的命中集合和最后一条 `sort` 值等价；只有比较器形成严格全序时才逐项验证有序列表，任一比较器等价的并列组按组内成员有效载荷多重集比较，无排序时按完整有效载荷多重集比较，不把当前并发完成顺序误认为稳定 API 契约；
2. 响应 `total` 仍按当前规则累加每个成员的 ES `total hits`；
3. `result_table_options` 仍按原成员 `TableUUID` 写入；
4. 全局排序、from/limit 裁剪、multi-from 和高亮处理仍由现有上层逻辑完成；
5. `result_table_id` 及其内部 `routeInfo` 来源、状态码和公共响应结构不变；
6. ES `total hits` 的现有阈值行为不被改成精确计数。

### FR-07：成员级错误和重试

1. `_msearch` HTTP 成功但某个子响应失败时，只把对应成员写入现有错误通道，其他子响应继续解码。
2. 至少一个成员成功时，继续使用现有 `QueryRawPartial` 部分成功契约。
3. 全部成员失败时，继续返回查询失败。
4. 失败 child 对 list、total、result_table_options 和成功成员计数的贡献均为零；成功粒度始终是成员，不是 batch。
5. 零命中的 child 仍是成功成员，成功计数加一，并按当前行为保存该成员 option。
6. responses 数量不匹配或 nil child 不能通过位置偏移归给其他成员；相关成员按 batch 响应协议错误处理。
7. 整个 `_msearch` 的 timeout、429、5xx 或连接错误不得立即展开成 N 次 `_search`，避免故障时放大流量；该批成员统一失败，其他批次仍可成功。
8. 首期不做 endpoint/media-type 自动兼容回退。任何 `_msearch` transport 错误均按该批失败处理；由调用方按请求选择是否启用，避免 claim 所有权转移、负缓存和故障时 N 路单查放大。
9. 现有缺 mapping 空索引重试必须按成员判断。命中该窄化条件时，只对失败 child 依次执行现有空检查和单成员 retry；不能因一个成员触发整批重试，不能重放成功 sibling，也不能把多个 RT 的索引拼进同一个 retry URL。该稀有路径以串行延迟换取状态机简单和现有语义复用。

### FR-08：按成员数和请求体字节拆批

`/_msearch` 的路径固定，不再按索引 URL 长度装箱。批次仍必须同时受以下上限约束：

- 最大成员数；
- NDJSON 编码后的最大请求体字节数；
- ES 内部 `max_concurrent_searches`。

装箱前必须把 `QueryReference` 的 Go map 转成规范化成员序列，不能依赖 `Range` 的 map 遍历顺序。装箱必须确定性执行并保留该稳定成员顺序。单个成员超过请求体预算时使用已经完成准备的 prepared single executor 并记录原因，不得重新执行 alias/mapping 准备；不得静默截断查询体或索引。

首期保守默认建议：

| 配置 | 默认值 |
| --- | --- |
| `http.query.raw.es_batch.max_members` | `16` |
| `http.query.raw.es_batch.max_body_bytes` | `1048576` |
| `http.query.raw.es_batch.max_concurrent_searches` | `4` |

功能启用不再读取服务端开关或连接允许列表。`max_members`、`max_body_bytes` 和
`max_concurrent_searches` 只负责服务端容量保护，不能替代请求级 opt-in，也不能放宽
FR-02 至 FR-04 的资格门禁。

### FR-09：并发边界

1. mapping/DSL 准备阶段继续受 `QueryMaxRouting` 限制。
2. 最终执行任务以“批次或单成员”为并发单元，继续受同一个 `QueryMaxRouting` 限制。
3. 单个 `_msearch` 通过 `max_concurrent_searches` 限制 ES 内部并发。
4. context 取消、超时、panic 恢复和 channel 关闭必须覆盖批次路径，不得泄漏 goroutine。
5. 预分组后不足两个成员的集合应保持现有流水路径，避免负例因等待批次准备而产生额外延迟。

### FR-10：4096 字节请求行约束

批量搜索请求必须使用固定 `/_msearch` 路径，索引目标只能出现在 NDJSON header 中。不得使用：

- `/{index1,index2,...}/_search`；
- `/{index1,index2,...}/_msearch`；
- 把多个 RT 索引拼入 query parameter。

请求体必须以换行结尾，并使用 ES 接受的 NDJSON content type。URL 安全测试要直接断言 `RequestURI` 不含成员索引。

### FR-11：请求控制、观测和脱敏

至少提供：

- batch 候选数、实际批次数、批大小和请求体字节分布；
- 单查询数与 `_msearch` 请求数；
- 子项成功、子项错误、整批错误和回退原因；
- 准备耗时、ES 执行耗时和端到端耗时；
- 按存储类型、结果和区间化批大小聚合的低基数指标。

不得记录：

- 完整 ES 地址；
- 用户名、密码、Authorization 或自定义认证头；
- 完整索引列表；
- 完整查询条件、TraceID 或查询体。

### FR-12：请求参数与兼容顺序

本功能使用 `/query/ts/raw` 顶层可选参数 `is_es_batch`，不改变 `is_merge_db` 的含义。
`is_es_batch` 只在该接口生效，`/query/ts/raw_with_scroll` 和其他查询接口不读取它。APM
`TraceQuery.query_by_trace_ids` 在需要跨 RT TraceID 检索时显式传入
`is_es_batch=true`；其他调用方和其他 APM 查询不被隐式启用。

部署顺序必须是 UQ 先、APM 后。新 UQ 对缺省或 `false` 保持原逐 RT 路径；旧 UQ 会按现有
JSON 解码行为忽略未知的 `is_es_batch` 字段并继续原路径，因此 APM 后续启用不会把旧 UQ
变成错误请求。

## 7. 验收场景

| 编号 | 场景 | 预期 |
| --- | --- | --- |
| AC-01 | 一个 `data_label` 展开多个同连接、同最终语义 ES RT | 按预算形成一个或多个 `_msearch` |
| AC-02 | 原始 `query_list > 1`，成员同连接、同最终语义 | 与 AC-01 使用同一规划器 |
| AC-03 | 同连接但条件不同的 Event Logs 双 RT | 即使 `is_es_batch=true` 也不进入同一批次，结果与原路径一致 |
| AC-04 | 同输入但 mapping 导致最终查询体不同 | 按最终指纹拆开 |
| AC-05 | 两个逻辑成员使用同一 alias | 保留两个子请求和当前重复归属语义 |
| AC-06 | 相同语义但不同连接或认证上下文 | 分开执行 |
| AC-07 | 子项缺索引，其他子项成功 | 仅该成员失败，响应为部分成功 |
| AC-08 | 整批 timeout/429/5xx | 不做 N 路放大重试；该批失败 |
| AC-09 | search-after | 每个成员保持自己的 search-after 和最后 sort |
| AC-10 | highLight 开启 | 字段映射、高亮结果和当前路径等价 |
| AC-11 | 成员数或 body bytes 超限 | 稳定拆批，所有成员恰好执行一次 |
| AC-12 | `is_es_batch` 缺省/`false`、scroll、BKData 代理 | 完整走现有路径 |
| AC-13 | Trace 全局定位多 RT 请求 | 相同模板的同连接成员可攒批，返回按 RT 解复用 |
| AC-14 | URL 安全 | 请求行固定为 `/_msearch`，索引仅在 body |
| AC-15 | 缺 mapping 空索引重试 | 只重试对应成员，其他成员不重复查询 |

## 8. 成功标准

### 8.1 正确性

- 所有验收场景都有自动化测试；
- `is_es_batch` 缺省/`false` 的差分结果与当前基线一致；
- `is_es_batch=true` 后，成功请求的 list、total、options、status 和 result_table_id 满足本规范；
- race、单元测试、query golden 和现有 query_raw 回归通过。

### 8.2 性能与容量

在代表性 `5 / 20 / 50 / 200` RT 数据集上：

- ES 搜索 HTTP 请求数接近“最终批次数 + 不可批成员数”，而不是 RT 数；
- UQ 端到端 p95 不劣于基线，目标场景有明确下降；
- UQ CPU、内存、goroutine、响应体和 ES rejected/timeout 没有不可接受回归；
- 不再因批量索引目标触发 4096 字节请求行异常。

具体百分比不在脱离生产数据时预设；目标环境验证前必须先记录基线，再以同流量窗口比较。

## 9. 已关闭与仍需运行验证的门禁

### 9.1 已关闭并已用于本地实现

- 请求入口和目标 Trace 归因；
- 同连接不同条件负例；
- 虚拟表展开的真实并发瓶颈；
- Trace 显式多 `query_list` 源码路径；
- `is_merge_db` 的历史和当前边界；
- 当前测试集缺口；
- 线上 UQ 版本、默认并发和目标 ES 版本；
- 目标 UQ 到 ES 为直连、IndexGet/GetMapping 可用；
- multi-index、`top_hits` 和 `_msearch` 的隔离语义实验；
- 4096 字节请求行的本地边界复现。

当前没有需要需求方补材料才能继续本地验证的门禁。

### 9.2 只能在部署后验证，不是本地实现前置卡点

- 目标连接对 UQ 客户端实际 GET-with-body/NDJSON 的完整兼容性；
- 目标 ES 的实际 HTTP body 上限；
- 不同 ES 版本和少量特殊部署的 `_msearch` 兼容性；
- 200 成员下真实 body/response 大小、ES 线程池和 rejected 情况；
- 请求启用流量下 p95、部分错误率和资源变化。

这些边界由“请求缺省关闭 + 保守预算 + APM 按需启用 + 去掉参数即可回退”控制，验证方法见
`validation.md`。
