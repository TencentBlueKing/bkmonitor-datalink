# UQ `query_raw` Elasticsearch 攒批验证记录

## 1. 状态

| 阶段 | 状态 |
| --- | --- |
| 代码与历史取证 | 已完成 |
| 目标 Trace 归因 | 已完成 |
| 脱敏运行负载取证 | 已完成 |
| 隔离 ES 语义实验 | 已完成 |
| 功能实现 | 请求级控制与 APM Trace 调用已完成，已提交 UQ PR #1424 与 APM PR #11722，未部署 |
| 本地自动化验证 | 请求级契约、批处理、golden、race 和目标包 vet 已验证 |
| 测试环境验收 | 待部署后执行 |
| 目标环境验收 | 待测试环境通过后执行 |
| 生产结论 | 未形成 |

本文件把“实现前已经确认的事实”和“只能在实现后验证的运行结果”分开。内部环境、连接、业务和部署标识不进入公开仓；未上线前不得把预期收益写成生产结论。

实现前运行证据截至 2026-07-29。后续读回必须记录新的证据时间，不能沿用本次状态。

## 2. 实现前基线

### 2.1 UQ 与调用方

只读取证已确认：

- 目标请求命中预期 UQ Deployment；
- 运行源码与本规范检查的调用链一致；
- 当前有效 `QueryMaxRouting` 为 `4`；
- 调用方 timeout 大于 UQ 的 ES 查询 timeout；
- 目标样本使用 UQ 直连 ES；
- 目标连接的 IndexGet 和 GetMapping 均可用。

原始镜像、commit、Pod、环境名、连接标识、endpoint 和认证信息不写入本文件。目标环境验收
时在内部发布记录中保存精确身份，并在本文件仅登记可公开的验证结论。

### 2.2 不同条件负例

已确认：

- APM Event Logs；
- 一次 `query_raw`；
- 显式两个 `query_list`；
- 同一个 ES 连接；
- 条件明显不同；
- 当前执行两个 `_search`。

实现后期望：

- 两个成员在预语义分组阶段分开；
- `_msearch` 不得把二者放入同一 HTTP 请求；
- list、total、result_table_options、result_table_id 和 status 与 `is_es_batch` 缺省/`false`
  时一致。

### 2.3 多路由正向负载

脱敏运行样本确认：

| 项目 | 基线 |
| --- | --- |
| 原始 query_list | 1 |
| 展平路由 | 数十个 |
| ES 成员 | 占绝大多数 |
| ES 连接 | 多个 |
| 最终 body | 同一输入可派生多种 |
| 当前 `_search` | 与 ES 成员逐项对应 |
| 最大成员任务并发 | 4 |

这不是 APM 200+ 应用验收结果，只作为真实执行模型和请求放大基线。

实现后期望：

- 成员条件和 final body 不变；
- 只在连接和 final body 都相同的组内攒批；
- 搜索 HTTP 请求数低于 ES 成员数；
- 任何未攒批成员都能由语义、连接、预算、资格或 fallback 指标解释。

### 2.4 Trace 显式多 RT

源码确认 Trace 全局定位会为多个预计算 RT 构造多个显式 query。脱敏运行元数据确认候选 RT 可以共享同一个 ES 存储。

当前采样窗口没有捕获该接口实际请求，因此实现后必须通过授权测试流量验收，不能用 Event Logs 双 RT 代替。

### 2.5 query golden 证据边界

现有完整 golden 数据集作为 `is_es_batch` 缺省时的回归门禁。实现阶段已把 runner 最小扩展
为可按非空行解析 `_msearch` NDJSON，并新增一个显式开启的双 `query_list` case。该 case
只使用用户提供的生产 Trace 入口形态和当前 APM→UQ 源码调用链作为 input 形态来源；
route、dependencies 和 expected output 均为脱敏固定 fixture，expected 标记为
`outputs_kind=handler_replay` 和 `provisional_output`。

当前仍缺少能够与该生产入口唯一关联的 outbound output，因此该 case 不计入生产 output
采样收敛。后续只有捕获到完整、版本钉住且可关联的 production input/output 后，才可把
output 来源升级为 `production_log`。不得把源码形态、本地 handler 回放或本文件的汇总
描述当作生产出站证据。

## 3. 已完成的隔离实验

### 3.1 结果语义

| 方案 | 成员 total | 成员分页 | 重复 alias | 子项错误 | 结论 |
| --- | --- | --- | --- | --- | --- |
| 普通 multi-index `_search` | 无法逐成员保留 | 无法逐成员保留 | 会折叠物理命中 | 难隔离 | 拒绝 |
| `filters + top_hits` | 与当前阈值语义不同 | 受 inner window 限制 | alias 并集风险 | 分片错误复杂 | 拒绝 |
| `_msearch` | 保留 | 保留 | 保留独立 child | 保留 | 采用 |

### 3.2 URL 边界

隔离环境把 initial line 限制为 4096 后，单 URL 索引列表在数百个短索引附近失败，具体边界随 API、base path 和编码变化。

`GET /_msearch` 的 RequestURI 固定；实验中 NDJSON header 超过 4096 字节仍成功，因为索引位于请求体。

### 3.3 容量边界

`top_hits` 的宽查询在合成实验中表现出明显响应放大。这不是生产性能数字，只证明“一次全局聚合”不是安全替代。

`_msearch` 仍需限制：

- members；
- request body bytes；
- ES internal concurrency；
- UQ 外部 batch task concurrency；
- APM Trace 请求的显式 opt-in 范围。

## 4. 实现前不能直接读取的 ES 设置

现有只读能力不能取得：

- `http.max_initial_line_length`；
- `http.max_content_length`；
- `search.max_buckets`；
- `index.max_inner_result_window`；
- alias filter 和 search routing。

方案不依赖 `search.max_buckets` 或 inner window；索引不进入新批量请求 URL；初始 body
预算为 1 MiB；功能由顶层请求参数 `is_es_batch` 控制。不能直接读取的配置按保守预算和
运行验证处理，不是继续向需求方索取凭据或阻塞编码的理由。

## 5. 本地实现与自动化验证

截至 2026-07-30，本地工作树已把 `_msearch` 路径调整为请求级 `is_es_batch` 控制，并由
APM `TraceQuery.query_by_trace_ids` 显式启用；代码已提交 UQ PR #1424 与 APM PR #11722，
尚未部署或执行目标环境流量。
下列证据只能证明本地代码契约，不能外推为目标 ES 集群兼容性或性能收益。

### 5.1 已通过门禁

- [x] 单个 `data_label` 展开两个同连接、同条件 RT 后形成一个 `_msearch`；
- [x] 两个显式 `query_list` 项使用相同 TraceID 条件时形成一个 `_msearch`；
- [x] 两个显式 `query_list` 项条件不同时保持两个独立 `_search`；
- [x] 固定 `GET /_msearch`、NDJSON content type、原始 body、末尾换行和索引不进入 RequestURI；
- [x] 最终 body 不同、尾部单成员和单成员超过 body 预算时走 prepared single；
- [x] child 部分失败、整批 transport 失败不展开单查、缺 mapping 只重试失败 child；
- [x] `total`、成员归属、options、routeInfo、高亮和低基数观测回归；
- [x] 同一 fixture 在原逐 RT 路径的两个 `_search` 与一个 `_msearch` 下，严格排序并裁剪后的 list、`total`、options、result table ID 和 status 精确等价；
- [x] 200 个任务在 `QueryMaxRouting=4` 时使用有界 dispatcher，最大外部 I/O 为 4，取消后不保留 O(N) 等待 goroutine；
- [x] preparation、普通 single 和 batch 混合执行时共享同一个 `QueryMaxRouting` 上限；
- [x] 每个 ES 预分组独立协调准备和执行；阻塞预分组不会阻塞健康预分组发出 `_msearch`；
- [x] `QueryMaxRouting=1` 时 missing-mapping 的首次 child、空检查和单成员 retry 串行完成，最大 inflight 为 1；
- [x] 请求 context 取消会释放阻塞中的 batch 请求并关闭生产者通道；
- [x] batching 功能调整前，现有完整 query golden 数据集在原路径下通过；
- [x] batching 相关目标测试在 race 模式通过；
- [x] `metric`、`tsdb/elasticsearch`、`service/http` 目标包 `go vet` 通过。
- [x] `is_es_batch` 缺省/`false` 不调用 planner，`true` 进入 planner；
- [x] APM `TraceQuery.query_by_trace_ids` 只在目标 Trace 查询按需传 `true`；
- [x] 请求级调整后的目标测试、golden、race 和 vet 已重跑。

执行过的核心命令：

```bash
go test ./query/structured ./metric ./tsdb/elasticsearch -count=1
go test ./service/http -run 'RawESBatch|QueryRawESBatch|QueryRawCharacterization|TestExecuteQueryRawWithESBatch' -count=1
go test ./internal/online_cases/query_golden_cases -count=1
go test ./service/http -run 'TestOnlineQueryGoldenCases|TestOnlineQueryGoldenSegmentedRouteControlsFanOut|TestCanonicalOnlineQueryGoldenOutputs' -count=1
go test -race ./tsdb/elasticsearch -run 'RawBatch|RawQuery|MissingMapping' -count=1
go test -race ./service/http -run 'RawESBatch|QueryRawESBatch|QueryRawCharacterization|TestExecuteQueryRawWithESBatch' -count=1
go vet ./service/http ./tsdb/elasticsearch ./metric ./query/structured
```

上述 UQ 目标命令均通过。APM 定向 pytest 共 13 个用例通过，覆盖参数缺省时不发送、
显式开启时发送、Serializer 无隐式默认值，以及
`TraceQuery.query_by_trace_ids` 的请求体 opt-in 和 `QueryHelper` 的中间转发；相关 6 个
Python 文件的 Ruff check 和 format check 均通过。

完整 `service/http` 包仍有一个 BKData PromQL mock 失败：
`TestPromQLQueryHandler/test_promql_by_bkdata_with_long_dim`。已在独立临时 worktree 对基线提交
`af832dbd7576d2c971cd1df084b6d5d89ed539b1` 执行同一子用例并得到相同失败：
生成 SQL 含 `MAX(FLOOR(...))`，现有 mock 无匹配数据，期望一条 series、实际为空。
因此该项是已隔离的基线失败；不能据此写“`service/http` 全量通过”，也不能把它计为本次回归。

`go test ./... -count=1` 的本次全量运行也只因上述 `service/http` 子用例退出失败，其余包通过。
`go vet ./...` 仍会报告仓库已有的 unreachable code、`context.WithTimeout` cancel、
`Seek` 签名和其他生成代码问题；已在同一基线提交的隔离 worktree 复现。本文只据此声明
“本次目标包 vet 通过，仓库全量 vet 被基线问题阻断”，不声明全量 vet 通过，也不在本变更中
修改这些无关文件。

完整 `service/http` race 还有两层既有阻塞，均已在同一基线提交复现：

1. Go 1.26.2 的 checkptr 在测试初始化 bbolt v1.3.3 时触发
   `converted pointer straddles multiple allocations`，尚未进入本次 batch 路径；
2. 关闭 checkptr 后，现有
   `TestAPIHandler/test_tag_values_in_prometheus_direct` 在
   `service/http/api.go:275` 暴露并发写，调用链位于 tag-values/规则解析路径，也不经过
   `query_raw` batch。

因此只声明本次 batch 目标测试重复 5 次 race 通过，不声明整个 `service/http` 包 race
通过。

### 5.2 本地装箱微基准

命令：

```bash
go test ./tsdb/elasticsearch -run '^$' -bench '^BenchmarkRawBatchPack$' -benchtime=100x -count=1
```

| 成员数 | 时间 | 分配字节 | 分配次数 |
| ---: | ---: | ---: | ---: |
| 5 | 2.176 µs/op | 3,027 B/op | 28 allocs/op |
| 20 | 8.207 µs/op | 10,859 B/op | 96 allocs/op |
| 50 | 16.557 µs/op | 27,451 B/op | 232 allocs/op |
| 200 | 83.854 µs/op | 110,584 B/op | 909 allocs/op |

这是本机 NDJSON 装箱微基准，不包含 mapping、网络、ES 搜索、响应解码和 UQ 全链路聚合，不能作为容量或延迟验收结果。

## 6. 测试环境验收

### 6.1 环境记录模板

精确值保存在受控内部发布记录；公开规范只记录验证结果。

| 项目 | 结果 |
| --- | --- |
| UQ image/commit 已读回 | 待填：是/否 |
| ES version 已确认 | 待填：是/否 |
| 直连或代理形态已确认 | 待填 |
| APM image/commit 已读回 | 待填：是/否 |
| `is_es_batch` 缺省/false | 待填：原逐 RT 路径 |
| `is_es_batch=true` | 待填：目标 Trace 请求 |
| max members | 待填 |
| max body bytes | 待填 |
| max concurrent searches | 待填 |
| 流量窗口和样本量 | 待填 |

### 6.2 协议门禁

- [ ] 参数缺省/`false` 不调用 planner，保持逐 RT `_search`；
- [ ] 参数 `true` 只允许进入 planner，不跳过连接、conditions、最终 DSL 和容量门禁；
- [ ] UQ 实际 client 能向目标 endpoint 发送 GET-with-body；
- [ ] NDJSON content type 被接受；
- [ ] body 最后换行；
- [ ] RequestURI 固定为 `/_msearch`；
- [ ] 目标 Trace 请求涉及的 endpoint 已验证支持 `_msearch`；不兼容时整批失败，不自动展开 prepared single；
- [ ] 初始 body 预算可被链路接受；
- [ ] 代表性受支持 ES 版本验证；
- [ ] BKData ES 代理保持不进入批次；
- [ ] `/query/ts/raw_with_scroll`、BKData 和其他接口不受参数影响；
- [ ] UQ 先于 APM 部署；旧 UQ 忽略未知参数时仍保持原路径。

### 6.3 正确性门禁

- [ ] 同连接同语义双 RT 形成一个 batch；
- [ ] 不同条件双 RT 不同 batch；
- [ ] 相同 alias 的两个成员保持两份逻辑结果；
- [ ] 只有比较器形成严格全序时才逐项比较 list；任一比较器等价的并列组按组内成员有效载荷多重集比较；无排序时按完整有效载荷多重集比较；
- [ ] total 与参数缺省/`false` 差分一致；
- [ ] result_table_options 与参数缺省/`false` 差分一致；
- [ ] result_table_id/status 与参数缺省/`false` 差分一致；
- [ ] search-after 逐成员一致；
- [ ] highLight 一致；
- [ ] highLight 候选预取同时保留 alias/index targets、physical indexes 和 field map；预取失败后仍保留一次正式准备机会，完整预取成功时只在本地重新派生 alias 校验身份，不重复远端 `IndexGet`/`GetMapping`；
- [ ] 一个 child 失败时其他 child 成功；
- [ ] 缺 mapping retry 只作用于失败 child；
- [ ] 缺 mapping 的多个失败 child 在当前 batch worker 中依次执行空检查和单成员 `_search` retry，不重放成功 sibling，也不进行二次组装；
- [ ] batch fallback 成功和失败分支的新增观测及公共错误均不包含索引、retry body、endpoint、凭据或原始错误；
- [ ] 失败 child 对 list、total、result_table_options 和成功计数贡献为零；
- [ ] 零命中 child 仍计为成功并保存 option；
- [ ] responses 缺项或 nil child 不会错配后续成员；
- [ ] batch transport 失败不展开 N 路请求。

### 6.4 并发与资源门禁

- [ ] 准备 + 单查询 + batch 的 UQ 外部 I/O 同时数不超过 `QueryMaxRouting`；
- [ ] 单 batch 内 ES 并发不超过配置；
- [ ] 无 goroutine 泄漏；
- [ ] race 检查通过；
- [ ] 5/20/50/200 成员 benchmark 完成；
- [ ] 最大 body 和最大 response 有记录；
- [ ] UQ CPU/内存峰值有记录；
- [ ] ES search rejected、queue、timeout 有记录。

## 7. 部署与目标环境验收步骤

### 阶段 0：先部署 UQ

目标：

- 确认版本身份；
- 确认容量配置可读；
- 确认新增指标不含高基数或敏感信息；
- 所有现有请求保持不传 `is_es_batch`，行为与基线一致。

退出条件：参数缺省/`false` 与基线无回归。

### 阶段 1：测试请求显式启用、最多 5 成员

容量配置：

```yaml
max_members: 5
max_body_bytes: 1048576
max_concurrent_searches: 4
```

验证：

- 对同一 Trace 显式多 RT 请求分别传缺省/`false` 与 `true`；
- 一个 Event Logs 条件不同负向请求；
- batch request/body/member/error 指标；
- ES rejected 和 p95。

### 阶段 2：部署 APM 调用方

前置：

- 阶段 1 正确性完全通过；
- child/transport error 无异常上升；
- UQ/ES 资源有余量。

UQ 先、APM 后。仅由 `TraceQuery.query_by_trace_ids` 按需传 `is_es_batch=true`，其他 APM
查询不被隐式启用。先保持 `max_members=5`；确认调用范围和回退生效后再提高到 16。

### 阶段 3：200 RT

只在隔离压测或受控流量执行。记录：

- 展平成员；
- 预分组/最终分组；
- batch 数；
- 请求和响应 bytes；
- UQ p50/p95/p99；
- ES took、queue、rejected；
- 总体成功/部分成功/失败。

不得仅以“HTTP 请求数下降”判定成功。

## 8. 前后对比模板

| 指标 | 参数缺省/false | 参数 true | 结论 |
| --- | ---: | ---: | --- |
| 展平 ES 成员数 | 待填 | 待填 | |
| `_search` HTTP 数 | 待填 | 待填 | |
| `_msearch` HTTP 数 | 0 | 待填 | |
| 总 ES search HTTP 数 | 待填 | 待填 | |
| UQ p50 | 待填 | 待填 | |
| UQ p95 | 待填 | 待填 | |
| UQ p99 | 待填 | 待填 | |
| UQ CPU | 待填 | 待填 | |
| UQ memory | 待填 | 待填 | |
| child error | 待填 | 待填 | |
| batch transport error | 0 | 待填 | |
| ES rejected | 待填 | 待填 | |
| ES timeout | 待填 | 待填 | |
| 最大 request body | 不适用 | 待填 | |
| 最大 response body | 待填 | 待填 | |

要求使用同环境、相近流量、相同时间范围和相同调用方比较；不能把不同业务窗口直接做因果对比。

## 9. 回退验收

- [ ] APM 去掉 `is_es_batch` 或传 `false` 后，新请求不再出现 `_msearch`；
- [ ] 参数缺省/`false` 的同一请求恢复逐 RT `_search`；
- [ ] 服务端容量配置不被误用为功能开关；
- [ ] `is_merge_db` 行为不变；
- [ ] 当前单查询 partial/fallback 恢复；
- [ ] 请求参数回退后错误和延迟恢复；
- [ ] 回退动作、生效时间和 RTO 记录在内部发布记录。

## 10. 失败判定

出现任一项即停止扩大请求启用范围，并由 APM 去掉参数或传 `false`：

- list/total/options/result_table_id/status 任一差分不符合规范；
- 不同 conditions 被装入同一 batch；
- child 响应错配成员；
- batch transport 错误触发 N 路放大；
- ES rejected、timeout 或 UQ p95 持续明显恶化；
- request/response body 超过链路限制；
- 新观测泄漏敏感信息；
- 非 Trace 调用被意外启用；
- 无法通过请求参数恢复旧路径。

## 11. 敏感信息门禁

公开规范、测试数据和新增观测均不得包含：

- 内部环境名；
- 实际 endpoint、连接标识或索引名；
- 业务、应用、TraceID；
- 镜像、Pod 和部署拓扑；
- 用户名、密码、Authorization 或其他认证头；
- 完整 query body。

精确发布身份和运行证据只能保存在受控内部记录中。任何凭据处置不进入公开代码仓。

## 12. 上线后沉淀要求

完成线上验证后更新本文件中的公开结论，并在受控内部发布记录中保存精确证据。

公开结论至少包括：

- 受支持的连接形态；
- 参数缺省/`false` 与 `true` 的验收是否通过；
- 请求数和延迟趋势；
- ES 资源是否出现回归；
- 是否发生回退；
- 是否扩大到 200+ RT；
- 尚存限制和下一步。

若最终未启用，也要记录未启用原因类别，避免后续重复试验。
