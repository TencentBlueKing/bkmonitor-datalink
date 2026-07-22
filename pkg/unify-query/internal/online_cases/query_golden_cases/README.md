# UQ 生产查询 golden 数据集

本目录保存从生产历史查询形态中提炼、完成不可逆脱敏的 unify-query（UQ）离线回归数据。测试目标不是下游返回的数据点，而是 UQ 实际生成的全部下游查询请求。

每个 case 固定以下关系：

```text
sanitized request
  + fixed route fixture
  + deterministic dependency responses
  -> real UQ handler / parser / route expansion / query builder
  -> normalized downstream outputs[]
  == expect.outputs.json
```

因此测试不读取实时路由，不连接真实 BKBase、Elasticsearch、InfluxDB 或查询存储，也不依赖实时日志。

## 当前覆盖

数据集当前包含 16 个可执行 case。其中 13 个 case 的 input 与当前 expected outputs 均直接来自可关联的生产历史日志；2 个 TSpider case 的 input 和旧行为来自生产证据，expected outputs 在时间桶分组修复合入后由真实 handler 重新回放；1 个 InfluxDB case 只有生产 input 形态，outputs 由固定 fixture 经当前真实 handler 回放得到，标记为暂定覆盖，不计入生产 output 采样收敛。

| 分类 | Case 数 | 已覆盖形态 | Output 来源 |
| --- | ---: | --- | --- |
| VictoriaMetrics | 3 | 简单 PromQL、复杂聚合/区间/二元表达式、多结果表合并 | 生产日志 |
| Elasticsearch | 4 | aggregate/raw × 有无 query_string | 生产日志 |
| Doris | 3 | aggregate、raw、ES→Doris 时间分段路由 | 生产日志 |
| TSpider | 3 | aggregate、raw、PromQL 8 reference/16 output | 生产日志 + 修复后 handler 回放 |
| HDFS | 2 | aggregate、raw | 生产日志 |
| InfluxDB HTTP | 1 | aggregate、条件、group by time/fill | handler 回放，暂定 |

其中 `doris_es_segmented_multi_output_001` 用一个逻辑查询和固定的 ES→Doris 时间分段路由生成 4 个下游请求：Doris schema、Doris query、ES index/mapping、ES search。控制测试保持 input 不变，只移除 ES 分段后输出变为 2 个 Doris 请求，证明该“一进多出”来自路由展开和各后端的前置查询，而不是解析器把一个 reference 拆成多个逻辑查询。

`vm_multi_result_table_001` 则覆盖另一个边界：多个兼容的 VM 结果表会合并为一个 BKBase VM 请求，而不是每条路由记录各发一个请求。

`tspider_promql_multi_reference_001` 来自后续问题修复：一个 PromQL input 解析出 8 个 reference，固定路由为每个 reference 选择同一个 BKSQL 结果表，每个 reference 再产生 schema + aggregate 两个请求，因此完整 output multiset 为 16 条。该 case 同时锁定 TSpider 时间桶必须按完整表达式分组，不能按 SELECT 别名 `_timestamp_` 分组。

生产采样过程、四个时间窗的形似分布和当前边界见 [SAMPLING.md](SAMPLING.md)。

## Case 协议

正式 case 位于：

```text
testdata/cases/<storage>/<case_id>/
  case.yaml
  request.json
  route.json
  dependencies.json
  expect.outputs.json
```

- `case.yaml`：case ID、存储分类、形似签名、不可逆来源摘要、output 来源、标签和文件引用。`source.outputs_kind=handler_replay` 的 case 必须带 `provisional_output` 标签；问题修复导致 expected output 有意变化时使用 `post_fix_handler_replay`，并带 `post_fix_expected` 标签。
- `request.json`：进入 UQ 的 method、path、保留语义的安全 headers 和 body。
- `route.json`：空间、结果表、data label、存储类型和时间分段路由 fixture。
- `dependencies.json`：构造查询所需的最小稳定响应，例如 BKSQL schema、ES mapping 或 InfluxDB 空结果。
- `expect.outputs.json`：UQ 应生成的全部下游请求。

正式 case 不允许只有 downstream 而没有 input。尚未脱敏或尚不能唯一关联的候选只能放在 `testdata/local_cases/`；该目录已被 `.gitignore` 排除。

## Output 语义

`outputs[]` 记录查询参数，不记录查询结果：

- BKBase VM：`prefer_storage=vm` 和解析后的 query payload。
- BKSQL：schema 查询、Doris/TSpider/HDFS SQL 和必要的 cluster properties。
- Elasticsearch：index/mapping 请求和最终 search DSL。
- InfluxDB：`/query` 的 db、InfluxQL 和稳定控制参数。

比较时会递归解析 JSON，并把并发输出排序为稳定的 multiset：顺序不影响结果，但重复请求的数量必须一致。任何未在 fixture 中声明的外部请求都会使测试失败。

## 路由隔离

回放器只 mock 两类边界：

1. `route.json` 提供路由和结果表元数据；测试不读取线上路由、Consul、Redis 或数据库。
2. `dependencies.json` 为外部 I/O 返回继续构造查询所需的最小响应。

入口 handler、PromQL/结构化查询解析、路由展开和各存储 query builder 均执行真实代码。禁止直接注入最终 `metadata.Query` 绕过这些路径。

## 脱敏门禁

原始生产日志不会进入仓库。正式 case 必须移除或替换：

- token、secret、ticket、cookie、Authorization 及同类鉴权字段。
- 内部 IP、域名、环境名、业务/空间/租户/用户标识。
- 真实集群、结果表、索引、指标、标签值、应用和工作负载名称。

占位符在单 case 内保持一致，使 request、route 和 output 仍可关联。协议测试会扫描结构化 key、嵌套字符串、URL、IP 和禁止的来源元数据字段。

## 运行测试

在 `pkg/unify-query` 下执行：

```bash
go test ./internal/online_cases/query_golden_cases -count=1
go test ./service/http -run 'TestOnlineQueryGoldenCases|TestOnlineQueryGoldenSegmentedRouteControlsFanOut|TestCanonicalOnlineQueryGoldenOutputs' -count=1
```

第一条校验 case 协议、来源摘要、storage/output 对应关系和敏感信息；第二条扫描所有 enabled case，经过真实 UQ 路径回放并比较 `outputs[]`。新增 case 不需要在 Go 代码中登记。

## 采样与扩充

常规扩充按以下循环执行：

1. 按既有分类在一个历史 10 分钟窗口抽取约 20 条候选。
2. 用窄时间窗和 trace 关联入口与完整下游请求序列，歧义候选丢弃。
3. 按会影响下游构造的结构特征去重，不因真实标识或字面值不同新增 case。
4. 切换时间窗继续采样，直到新窗口中的请求均有已有形似 case 覆盖。
5. 脱敏后加入正式目录，并用真实 handler 校准 `expect.outputs.json`。

采样收敛不是永久封口。后续每个查询解析、路由或 query builder 问题的修复，都必须新增能复现该问题的 case；若新日志出现现有形似不能表达的结构，也应继续扩充。

修复类 case 不把线上失败输出固化为正确答案。其 input、旧 output 数量和失败形态仍来自生产证据；修复合入后，用相同 input、固定 route/dependencies 回放得到新的 expected outputs，并标记为 `post_fix_handler_replay`。只有单纯缺少可关联生产 output 的 case 才使用 provisional `handler_replay`。
