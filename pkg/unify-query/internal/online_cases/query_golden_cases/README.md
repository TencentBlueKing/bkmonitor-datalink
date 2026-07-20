# UQ 线上查询构造 golden 测试集

本目录用于沉淀 unify-query 线上查询样例的“查询构造 golden”。第一阶段只兜 UQ 生成下游查询的行为，不保存最终响应；真实路径测试会用本地 mock 截获下游请求，只校验 UQ 实际生成的 query payload。

目标链路是：

```text
UQ 入口请求
  -> router / query builder / RT 展开 / filter 拼接
  -> ES DSL / VM query_sync payload / Doris SQL
  -> 和本地 expect 做 golden 对比
```

它和完整 replay 的区别是：

- 查询构造 golden：只比较“生成的下游查询”，轻量，适合 ES / VM / Doris 大批量样例。
- 完整 replay：还要保存最终 response 和下游 mock，成本高，后续只适合少量端到端 smoke。

## 目录结构

正式兜底 case 放在：

```text
testdata/cases/
  es/
  vm/
  doris/
```

本地临时采样 case 放在：

```text
testdata/local_cases/
```

`local_cases` 已加入 `.gitignore`，用于保存还没有脱敏、归一化或确认稳定的本地样例，不进入仓库。

单个 case 目录形态：

```text
testdata/cases/vm/vm_query_builder_real_001/
  case.yaml
  request.json
  expect.downstream.json
```

## 文件职责

`case.yaml`

描述 case 的来源、状态、分类和期望文件位置。

`request.json`

进入 UQ 的入口请求。只有 `golden_status: golden_ready` 的 case 才必须提供；如果采样时只能拿到下游 query_sync payload，可以先不放 request，并把 case 标记为 `golden_status: captured_downstream`。

`expect.downstream.json`

采样阶段捕获到的下游查询期望。不同存储对应不同形态：

- VM：BKBase query_sync 请求里的 `prefer_storage=vm` 和 `sql` JSON payload。
- ES：最终发给 ES 的 index / DSL。
- Doris：最终发给 BKBase / Doris 的 SQL。

## case 状态

`golden_status: captured_downstream`

表示这个 case 已经有真实线上下游查询样本，但还没有补齐 UQ 入口请求。它可以用于沉淀数据和校验 fixture 结构，暂时不能跑真实 UQ query builder golden。

`golden_status: golden_ready`

表示这个 case 同时具备入口 `request.json` 和 `expect.downstream.json`，可以接入真实 UQ 代码路径做 golden 对比。

已提交的 `golden_ready` case 会固定采样时的时间窗口。后续测试复放这条请求时，不使用当前时间，而是验证同一份入口请求是否仍然生成同一份下游查询。

## 当前测试覆盖

当前有两层测试。

第一层是 fixture 协议校验：

```bash
go test ./internal/online_cases/query_golden_cases -run TestQueryGoldenCasesDataset -count=1
```

它会检查：

- case ID、storage、golden_status、tags、source 是否完整。
- `expect.downstream.json` 是否存在且是合法 JSON。
- `golden_ready` case 必须提供合法 `request.json`。
- VM case 的 expect 中必须包含 `prefer_storage=vm`、`api_type`、PromQL、`result_table_list`。
- fixture 里没有 token、cookie、authorization、内网 IP 等敏感片段。

第二层是真实 UQ 代码路径 golden：

```bash
go test ./service/http -run TestOnlineVMQueryGoldenCaseRealPath -count=1
```

当前 `vm_query_builder_real_001` 已经接入这层测试。测试会：

```text
读取 vm_query_builder_real_001/request.json
  -> 设置该线上样例对应的本地 router fixture
  -> 调用真实 /query/ts/promql handler
  -> 让 promQLToStruct / queryTsToInstanceAndStmt / victoriaMetrics.DirectQueryRange 正常执行
  -> 用 httpmock 截获发往 BKBase query_sync 的请求
  -> 取出 prefer_storage 和 sql
  -> 归一化动态字段
  -> 对比 expect.downstream.json
```

这条真实路径可以兜住：

- VM 路由变化。
- result_table_list 展开变化。
- VM PromQL / metric_filter_condition 生成变化。
- cluster_name、start/end/step/nocache 等下游参数变化。

VM 真实路径测试目前是 table-driven 形式。继续补 VM case 时，优先新增：

```text
testdata/cases/vm/<case_name>/
  case.yaml
  request.json
  expect.downstream.json
```

然后在 `service/http/online_query_golden_test.go` 的 `cases` 列表里补一项该 case 对应的本地 router fixture 信息，不需要复制新的测试函数。

后续 ES / Doris case 也按同样模式接入真实路径测试；到那时可以把 `service/http` 里的单 case 测试抽成扫描式 `TestOnlineQueryBuilderGoldenCases`。

## case 转正标准

本地 case 进入 `golden_ready` 前需要满足：

1. 有真实 UQ 入口请求 `request.json`。
2. 有真实或人工确认稳定的 `expect.downstream.json`。
3. 已脱敏：不包含 token、cookie、authorization、真实用户敏感信息、内网 IP。
4. 已归一化：时间、trace_id、request_id、耗时等易变字段固定或忽略。
5. 有代表性：能说明一个查询场景，例如 `query_range`、`prefer_storage_vm`、`result_table_list`、`group_by`、`filter`。
