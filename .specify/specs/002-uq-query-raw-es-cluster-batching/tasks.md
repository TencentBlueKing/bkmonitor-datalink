# UQ `query_raw` Elasticsearch 按连接攒批任务清单

## 1. 执行纪律

1. 按依赖图实施：T001 先冻结基线；T002 与 T003 可并行；T004 与 T005 在共同前置完成后可并行，其余按依赖顺序推进；未通过前置任务验证不得接入后续任务。
2. 每项先写失败测试，再写最小实现。
3. `is_es_batch` 缺省/`false` 始终走原逐 RT 路径；只有调用方显式传 `true` 才进入 planner。
4. 不修改 `/query/ts/raw_with_scroll` 和 BKData ES 代理。
5. 不复用或改变 `is_merge_db`。
6. 不在新日志、span、metric 中写连接地址、凭据、headers、索引列表、TraceID 或完整查询体。
7. 每个 diff 行都应能追溯到 `spec.md` 的功能需求或验收场景。

## 2. 完成定义

- `spec.md` 的 AC-01 至 AC-15 均有自动化覆盖；
- `is_es_batch` 缺省/`false` 时与当前路径差分等价；
- `is_es_batch=true` 后同连接同语义成员使用固定 `/_msearch`；
- 不同条件、不同连接、BKData、scroll 均不进入同一批次；
- total、list、result_table_options、result_table_id、partial status 等价；
- 目标包、query golden、race、全量测试和 vet 完成，或对与本变更无关的基线失败给出未改基线复现证据；
- 参数缺省/`false` 与 `true` 的目标环境对比按 `validation.md` 留档；
- 代码提交状态和运行验证状态分别报告。

### 2.1 当前执行状态

| 任务 | 状态 | 边界 |
| --- | --- | --- |
| T001–T008 | 本地实现和目标自动化已完成 | 尚未提交、部署 |
| T009 | 部分完成 | 现有 query golden、httptest 集成和 5/20/50/200 装箱微基准已完成；正式 production golden、目标 ES 版本兼容与全链路性能待运行证据 |
| T010 本地门禁 | 已完成并记录基线边界 | 目标测试、定向 race、目标包 vet 和全量测试已执行；全量测试、全量 race、全量 vet 的既有失败均已在未改基线复现 |
| T010 目标环境验收与留档 | 未开始 | 必须先部署 UQ，再由 APM Trace 请求按需传 `is_es_batch=true` |

当前工作树没有提交。不得把“T001–T008 本地完成”表述为测试环境或生产验收完成。

## T001：冻结当前 `query_raw` 契约

### 目标

在重构前为当前行为建立差分基线。

### 测试

- 单 ES 成员：
  - list；
  - ES total hits；
  - result table option；
  - 最后 sort/search-after；
  - field alias 和时间字段；
- 多成员：
  - 全局排序；
  - from/limit 裁剪；
  - multi-from；
  - result_table_id 及其内部 routeInfo 来源；
  - 部分成功和全失败；
- 重复 alias 的两个逻辑成员；
- 缺 mapping 空索引 fallback；
- highLight 字段映射。
- 两路全失败；
- 空结果或全局裁剪后 `result_table_id` 仍来自完整 routeInfo；
- highLight 首次字段映射预取失败、正式准备成功时仍返回成功。

同时运行现有完整 query golden，保持 `is_es_batch` 缺省，防止新参数改变回放结果。

### 文件

- `pkg/unify-query/tsdb/elasticsearch/instance_test.go`
- `pkg/unify-query/tsdb/elasticsearch/instance_missing_mapping_fallback_test.go`
- `pkg/unify-query/service/http/query_test.go`

### 验证

```bash
go test ./tsdb/elasticsearch ./service/http -count=1
go test ./internal/online_cases/query_golden_cases -count=1
go test ./service/http -run 'TestOnlineQueryGoldenCases' -count=1
```

### 完成条件

测试在未实现批处理时通过，能够捕获 list、total、options 或 partial 契约变化。

## T002：增加请求级控制与容量配置

### 目标

增加：

```text
is_es_batch
http.query.raw.es_batch.max_members
http.query.raw.es_batch.max_body_bytes
http.query.raw.es_batch.max_concurrent_searches
```

### 测试

- `is_es_batch` 缺省/`false` 不调用 planner，`true` 才允许进入；
- 参数只在 `/query/ts/raw` 生效；
- `true` 仍受连接、conditions、最终 DSL 和容量门禁约束；
- 旧 UQ 忽略未知参数时保持旧路径；
- 非法成员数、body bytes、并发值回到安全默认；
- 配置不改变 `is_merge_db`。

### 文件

- `pkg/unify-query/service/http/settings.go`
- `pkg/unify-query/service/http/hook.go`
- `pkg/unify-query/unify-query.yaml`
- `pkg/unify-query/query/structured/query_ts.go`
- 对应测试文件

### 验证

```bash
go test ./service/http -run 'Test.*Settings|Test.*Hook|Test.*ESBatch' -count=1
```

## T003：无行为变化地拆分 ES 准备、执行和解码

### 目标

从 `QueryRawData` 抽取：

- `PrepareRawFieldMetadata`；
- `PrepareRawQuery`；
- prepared single executor；
- 包内 result decoder。

现有 `QueryRawData` 改为组合这些步骤，仍只发 `_search`。
不修改公共 `tsdb.Instance`；HTTP 层通过 Elasticsearch 的窄可选能力接口使用 prepared
路径。prepared 对象保持 opaque 和成员私有，字段元数据使用显式完整性标志，不能以空
mapping 或空 physical indexes 推断“不完整”。

### 测试

- 新旧最终 SearchSource 完全相同；
- search-after 在最终序列化前加入；
- formatter/field map 不跨成员共享；
- size cap、option from、field alias、time 和 total 不变；
- highLight 候选预取同时保存 alias/index targets、physical indexes 和 field map；只返回 field map 或不完整结果不得跳过正式准备；
- 完整预取成功时正式准备只在本地重新派生 alias 校验身份，不重复远端 `IndexGet`/`GetMapping`；
- highLight 预取失败、正式 alias/mapping 成功时请求仍成功；两次都失败时只产生一次成员错误且不做第三次尝试；
- 预取得到的 physical indexes 能继续支持缺 mapping retry，且成员间不共享可变元数据；
- 当前 fallback 测试全部保持通过。

### 文件

- `pkg/unify-query/tsdb/elasticsearch/instance.go`
- 可新建 `pkg/unify-query/tsdb/elasticsearch/raw_query.go`
- 对应测试文件

### 验证

```bash
go test ./tsdb/elasticsearch -count=1
go test -race ./tsdb/elasticsearch -count=1
```

### 完成条件

尚未接入 `_msearch`，T001 全部通过。

## T004：实现安全预分组

### 目标

在 HTTP 层实现：

- 直连 ES 资格判断；
- 不可输出的有效连接身份；
- 显式预语义投影和稳定指纹；
- 单元素组直接走现有路径。

### 先写的失败测试

1. 同连接、相同语义形成候选组；
2. 用户 Event Logs 形态的不同 conditions 不成组；
3. query string、time field、sort、from/size、search-after、collapse 任一不同均不成组；
4. 不同认证上下文不成组；
5. BKData 代理不成组；
6. `is_es_batch` 缺省/`false` 不成组；
7. 两个显式 `query_list` 与一个 data_label 展开使用同一入口；
8. 预分组单元素不等待准备屏障；
9. `QueryReference` 以不同 map 插入顺序构造时，成员 ordinal 和分组结果一致；
10. 准备前指纹不确定走旧单路径，已准备后的指纹不确定走 prepared single，mapping/DSL 准备错误只生成一次成员错误。

### 文件

- 新建 `pkg/unify-query/service/http/query_raw_es_batch.go`
- 新建 `pkg/unify-query/service/http/query_raw_es_batch_test.go`
- `pkg/unify-query/tsdb/elasticsearch/instance.go`

### 验证

```bash
go test ./service/http -run 'Test.*Raw.*ESBatch.*Group' -count=1
```

## T005：实现最终指纹、NDJSON 编码和装箱

### 目标

实现：

- `connectionKey + final body fingerprint`；
- 每成员独立 index header；
- 稳定 NDJSON；
- max members/body bytes 双预算；
- 固定 path。

### 先写的失败测试

1. mapping 派生不同 body 必须拆组；
2. 相同 body、不同 index 可以同组；
3. conditions 不同即使协议允许也不进入同一组；
4. 组内顺序稳定；
5. body 末尾换行；
6. 16/17 成员边界；
7. body 恰好等于/超过预算；
8. 单成员超过预算回退；
9. `RequestURI` 不含 index；
10. 一个 NDJSON header 超过 4096 字节仍不增长 request line；
11. `PerformRequest` 收到 string body，httptest 捕获原始 NDJSON 而不是 base64，content type 为 `application/x-ndjson`。
12. 单成员超过 body 预算时使用 prepared single，alias/mapping 只准备一次且搜索只执行一次。

### 文件

- 新建 `pkg/unify-query/tsdb/elasticsearch/raw_batch.go`
- 新建 `pkg/unify-query/tsdb/elasticsearch/raw_batch_test.go`

### 验证

```bash
go test ./tsdb/elasticsearch -run 'Test.*RawBatch.*Encode|Test.*RawBatch.*Pack|Test.*MSearch.*URI' -count=1
```

## T006：实现 `_msearch` 执行和成员解复用

### 目标

一次发送一个装箱结果，按 response ordinal 返回逐成员结果。

### 先写的失败测试

- 两个成功 child 的 list/total/option 分别正确；
- 空 child 保留空结果；
- 重复 alias 仍返回两个独立结果；
- search-after 各自独立；
- responses 数量不匹配为 batch transport error；
- nil child 或缺失 response 不得使后续成员错位；
- 一个 child 404，另一个成功；
- 失败 child 对 rows/total/option/successedPaths 的贡献为零；
- 零命中 child 仍增加 successedPaths 并保存 option；
- batch HTTP 429/5xx/timeout；
- endpoint/media type 不支持时整批失败，不做 prepared single 自动回退；
- transport 错误不释放 claim 后展开单查；
- `max_concurrent_searches` 只在有效时发送；
- batch/member/request/body/error/fallback 指标正确计数；
- 指标标签保持低基数；
- URL/index/credentials/headers/query sentinel 不进入返回错误、日志、span 或 metric。

### 约束

- 一般 transport error 不展开为 N 次 `_search`；
- child error 复用 `handleESError` 的分类语义，但使用不含 URL/index 的 batch 专用渲染；
- `successedPaths` 的粒度仍是成员，不是 batch。

### 验证

```bash
go test ./tsdb/elasticsearch -run 'Test.*MSearch|Test.*RawBatch.*Execute' -count=1
```

## T007：保留缺 mapping fallback

### 目标

复用现有成员级 fallback：

- 单查询继续单成员 retry；
- batch 逐 child 判断，只有失败成员执行空检查和单成员 retry；
- 多个失败 child 在同一 batch worker 内依次处理；
- 已成功 child 不重复执行。

### 先写的失败测试

1. 一个 child 触发，另一个成功；
2. 两个 child 触发时依次 retry，成功 sibling 不重放；
3. 失败索引非空时不 retry；
4. 非 missing-mapping 错误不 retry；
5. retry child 错误只影响对应成员；
6. retry URL 不包含多 RT 索引；
7. retry 响应仍有 shard failure 时状态为 failed，不能误计 recovered；
8. fallback attempted/recovered/failed 只使用固定低基数状态；
9. 当前所有 `instance_missing_mapping_fallback_test.go` 用例保持通过。
10. batch fallback 成功和失败时，索引、retry body、endpoint、凭据和原始错误 sentinel 均不进入新增日志、span、metric 或公共错误。

### 验证

```bash
go test ./tsdb/elasticsearch -run 'Test.*MissingMapping|Test.*RawBatch.*Retry' -count=1
```

## T008：接入共享限流和现有聚合器

### 目标

在 `queryRawWithInstance` 接入：

- 一个容量为 `QueryMaxRouting` 的共享 I/O 限流器；
- 非候选单查询；
- 候选组准备 coordinator；
- batch/single prepared execution；
- 现有 dataCh、errCh、total、options、label/highlight 汇总；
- 请求级和 batch 级低基数 span/metric。

### 先写的失败测试

1. 同时执行的准备、单查询和 batch 总数不超过限制；
2. 不在持有令牌时等待同限流器子任务；
3. context cancel 后无 goroutine 泄漏；
4. panic 仍转换为成员错误；
5. dataCh/errCh 只关闭一次；
6. highLight 复用 prepared field map，结果等价；
7. Event Logs 不同条件保持两个独立执行任务；
8. 脱敏多路由模型按连接和最终 body 形成预期组数；
9. total、global sort/crop、result_table_id 和 status 与参数缺省/`false` 等价；
10. `raw_with_scroll`、普通 raw 中带 scroll/slice 的成员不进入 planner；
11. 候选数、批次数、批大小、body bytes、child/transport error、fallback 和耗时指标正确；
12. table/index/endpoint/fingerprint/原始错误不能成为 metric label；
13. `QueryMaxRouting=0` 和负值保持 unlimited/no-op limiter 语义，不死锁；
14. `QueryMaxRouting=1` 时 missing-mapping 的首次请求、空检查和 retry 依次完成，最大 inflight 为 1；
15. 正数限制使用有界 worker/dispatcher，200 成员不会产生 O(N) 个等待令牌的 goroutine。

### 文件

- `pkg/unify-query/service/http/query.go`
- `pkg/unify-query/service/http/query_raw_es_batch.go`
- 对应测试

### 验证

```bash
go test ./service/http -count=1
go test -race ./service/http -count=1
```

## T009：运行 query golden 回归、补充集成和基准

### query golden

- 每个阶段运行现有完整数据集，保持 `is_es_batch` 缺省；
- 最小扩展 runner，使 `/_msearch` body 按非空 NDJSON 行解析，并消费 search fixture；
- 新增顶层 `is_es_batch=true` 的显式双 `query_list` provisional case，保留生产 Trace
  入口形态，route/dependencies 使用脱敏固定 fixture；
- expected 使用真实 handler 回放并标记 `outputs_kind=handler_replay`、
  `provisional_output`，不计入 production output 采样收敛；
- 后续捕获到完整、版本钉住且可关联的 production input/output 时，再按
  `unify-query-golden-sampling` 判断是否升级来源。

### 集成测试

- httptest wire/response fixture：同语义批次、child partial、search-after、重复 alias；
- 按拟支持 ES 响应形态提供 5.x/6.x/7.x 协议 fixture；
- GET-with-body/NDJSON；
- 4096 request-line；
- 1 MiB body 边界。

真实 ES 版本兼容性属于 `validation.md` 的运行门禁；仓库自动化不假定本地存在 ES 5.x/6.x 服务。

### benchmark

数据规模：

- 5；
- 20；
- 50；
- 200。

比较：

- HTTP 请求数；
- allocations；
- UQ CPU/内存；
- wall time；
- body/response bytes；
- child/batch error；
- `is_es_batch` 缺省/`false` 基线。

### 验证

```bash
go test ./internal/online_cases/query_golden_cases -count=1
go test ./service/http ./tsdb/elasticsearch -count=1
go test ./tsdb/elasticsearch -run '^$' -bench 'RawBatch' -benchmem
```

## T010：全量验证、目标环境验收和留档

### 本地门禁

```bash
go test -race ./tsdb/elasticsearch ./service/http -count=1
go test ./... -count=1
go vet ./...
```

并检查：

- `git diff --check`；
- 无敏感信息；
- 公共请求只增加顶层可选参数 `is_es_batch`；
- 无 scroll/BKData 越界修改；
- 不存在功能启用配置或连接允许列表；
- 文档与实现一致。

### 目标环境验收

1. 先部署 UQ，现有请求保持不传 `is_es_batch`；
2. 测试 UQ 对同一请求比较参数缺省/`false` 与 `true`；
3. UQ 通过后再部署 APM，由 `TraceQuery.query_by_trace_ids` 按需传 `true`；
4. 验证 Trace 多 RT 正向场景；
5. 验证 Event Logs 不同 conditions 负向场景；
6. 从 `max_members <= 5` 开始，验证后再提高至 16，并完成 200 RT 压测；
7. 任一正确性或稳定性异常由 APM 去掉参数或传 `false` 恢复原路径。

### 留档

在受控内部发布记录写入精确镜像、commit、环境、流量窗口、请求参数和生效容量配置；在
`validation.md` 只写公开安全的结论，包括前后趋势、正负场景和回退结果。

## 3. 需求追踪

| 需求 | 任务 |
| --- | --- |
| FR-01 路由展平后规划 | T004、T008 |
| FR-02 首期资格 | T002、T004 |
| FR-03 实际连接隔离 | T004 |
| FR-04 不同语义不成批 | T004、T005 |
| FR-05 RT 独立子查询 | T005、T006 |
| FR-06 返回等价 | T001、T003、T006、T008 |
| FR-07 错误和重试 | T006、T007 |
| FR-08 双预算拆批 | T002、T005、T006 |
| FR-09 并发边界 | T008 |
| FR-10 4096 请求行 | T005、T009 |
| FR-11 请求控制、观测与脱敏 | T002、T006、T008、T010 |
| FR-12 请求参数与兼容顺序 | T002、T010 |

## 4. 验收场景追踪

| 场景 | 任务与自动化测试 |
| --- | --- |
| AC-01 data_label 展开同连接同语义 | T004 分组测试、T008 HTTP 集成测试 |
| AC-02 显式多 query_list 同连接同语义 | T004 分组测试、T008 HTTP 集成测试 |
| AC-03 同连接不同条件 | T004 Event Logs 形态测试、T008 回归 |
| AC-04 mapping 派生 body 不同 | T003 prepare fixture、T005 最终指纹测试 |
| AC-05 重复 alias | T001 基线、T006 child 解复用测试 |
| AC-06 不同连接或认证 | T004 连接矩阵测试 |
| AC-07 单 child 缺索引 | T006 partial 测试 |
| AC-08 整批 timeout/429/5xx | T006 transport 故障注入测试 |
| AC-09 search-after | T001 基线、T003 body、T006 child option 测试 |
| AC-10 highLight | T001 基线、T003 预取容错测试、T008 prepared field map 差分测试 |
| AC-11 成员/body 超限 | T005 边界装箱测试 |
| AC-12 参数缺省/false、scroll、BKData | T002 请求参数、T004 资格、T008 scroll 集成测试 |
| AC-13 Trace 多 RT | T008 Trace 模板集成 fixture；T009 等待完整生产样本后评估 golden |
| AC-14 URL 安全 | T005 RequestURI 测试、T009 4096 集成测试 |
| AC-15 缺 mapping retry | T007 member-only retry 测试 |

## 5. 依赖关系

```text
T001
  ├─ T002
  └─ T003
      ├─ T004（同时依赖 T002）
      │   └─ T008
      └─ T005（同时依赖 T002）
          └─ T006
              └─ T007
                  └─ T008
                      └─ T009
                          └─ T010
```

T002 和 T003 在 T001 完成后可并行；T004 和 T005 同时依赖二者，完成后可并行开发，
但接入和共享状态由 T008 统一处理。
