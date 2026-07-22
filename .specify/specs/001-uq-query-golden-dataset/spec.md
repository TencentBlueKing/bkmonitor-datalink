# UQ 生产查询 golden 数据集规范

## 概述

为 unify-query（UQ）的查询解析、路由展开和下游查询构造建立一套离线回归数据集。
数据集样本来自生产历史日志，但提交内容必须完成不可逆脱敏。测试只比较 UQ 生成的下游请求，不访问真实路由、存储或网络。

PR #1411 仅作为最终代码和数据集的承载位置，不作为采样、数据协议或 mock 设计的前提。

## 目标

每条可执行 case 都表达以下确定性关系：

```text
sanitized input
  + fixed route fixture
  + deterministic dependency responses
  -> real UQ query path
  -> normalized downstream outputs[]
  == golden expected outputs[]
```

数据集用于兜底 UQ 查询处理行为：

1. UQ 查询处理改动必须通过数据集回归。
2. 后续每个查询构造问题的修复必须新增或扩充 case。
3. 数据集不依赖实时日志，也不追求全量收集。
4. 数据集不能受运行时查询路由、存储状态或外部服务响应影响。

## 生产采样原则

### 分类

首期按近期 SLI 中实际活跃的下游类型采样：

- VictoriaMetrics（通过 BKBase `query_sync`）。
- Elasticsearch。
- BKSQL，并继续按执行形态区分 Doris、TSpider、HDFS。
- InfluxDB HTTP。

入口优先覆盖 `/query/ts/promql` 与 `/query/ts`。低量入口在出现独立查询构造形态或问题修复时追加。

### 采样与收敛

1. 在固定 UQ 基线版本内，每个分类从一个历史时间窗抽取约 20 条请求。
2. 只接收能在窄时间窗中唯一关联到入口请求和完整下游请求序列的候选。
3. 对同类候选计算结构形似签名，剔除仅真实标识或字面值不同的重合样本。
4. 切换到不同历史时间窗重复采样。
5. 当一个新增时间窗的样本全部被已有形似签名覆盖时，该分类本轮采集收敛。
6. 线上修复不受收敛状态限制，必须将新的问题形态加入数据集。

`trace_id` 不能单独作为关联依据。候选还必须满足窄时间窗、单入口、同一请求下游序列完整等条件；歧义候选直接丢弃。

## Case 协议

每个正式 case 必须包含：

- `case.yaml`：ID、分类、形似签名、脱敏采样证明、input/output 来源和文件引用；普通非直接观测 output 必须显式标记为 provisional，已验证问题修复后的 expected output 必须标记为 post-fix handler replay。
- `request.json`：HTTP method、path、语义请求头和请求 body。
- `route.json`：本 case 所需的空间路由、结果表详情、数据标签映射和存储类型。
- `dependencies.json`：为完成查询构造而返回的最小确定性下游响应，例如 Doris/HDFS/TSpider 字段表或 ES mapping。
- `expect.outputs.json`：UQ 实际生成的全部下游请求列表。

正式目录不允许仅保存 downstream 而缺少 input；不可重放候选只能留在本地采样缓存，不能进入仓库。

## Output 定义

`outputs[]` 是 UQ 生成的下游请求，不是存储查询结果。至少支持：

- BKBase VM：`prefer_storage` 与解析后的 query payload。
- BKSQL：字段表查询和最终 Doris/TSpider/HDFS SQL，以及必要的 cluster properties。
- ES：索引/mapping 请求和最终 index + DSL。
- InfluxDB：HTTP query 参数中的 db、InfluxQL 和稳定控制参数。

每条正式 case 的同一 input 必须产生 1 个或多个 output。比较时允许并发导致的无语义顺序变化，但必须保留重复请求的数量。

修复类 case 的线上失败 output 只作为问题证据，不能作为正确 golden。修复合入后必须使用相同脱敏 input 和固定 fixture 重新回放 expected outputs，并以独立的修复单测证明该 expected 不是对当前实现的无条件追认。

## 路由与依赖隔离

1. route fixture 必须由 case 自带，不读取线上路由、Consul、Redis 或数据库。
2. runner 必须走真实 UQ handler、解析器、路由展开和 query builder 路径。
3. runner 只能 mock 路由数据和外部 I/O，不能直接注入最终 `metadata.Query` 绕过待测逻辑。
4. dependency response 只提供继续构造查询所需的字段/mapping/空查询结果，不参与 golden output。
5. 所有未声明的外部请求必须使测试失败。

## 归一化

比较前必须：

- 解析嵌套 JSON，避免字符串格式差异。
- 移除鉴权字段、cookie、trace/request ID、真实 host 和耗时。
- 统一 URL 到 backend + method + path + query/body。
- 对 JSON object 使用稳定键序。
- 对并发 outputs 使用稳定排序；不得用 set 去重。
- 保留 SQL、PromQL、InfluxQL、ES DSL 中会改变查询语义的结构。

## 形似签名

形似签名用于采样去重，不替代完整 golden 比较。签名至少包含：

- 入口 endpoint、instant/range、分页/scroll/merge_db 等模式。
- query/reference 数量及表达式关系。
- PromQL 节点类型、函数、聚合、二元操作、matcher 操作符和窗口结构。
- 结构化查询的函数、条件树、维度、query_string、SQL 等形态。
- 路由展开数量、单表/多表/数据标签/分段存储形态。
- output backend、请求阶段和 output 数量。

真实 metric、table、index、label 名称和字面值不应单独造成新签名。

## 脱敏与安全

原始日志可能包含有效鉴权信息，因此脱敏必须发生在进入正式 case 之前。

正式数据禁止包含：

- token、secret、ticket、cookie、Authorization 等鉴权字段和值。
- 内网 IP、内部域名、真实环境名、业务 ID、空间 ID、租户或用户标识。
- 真实集群、结果表、索引、指标、标签值、应用或工作负载名称。

替换值必须格式保持且在单 case 内一致，使路由与查询语义仍可重放。安全校验需要同时检查结构化 key、嵌套日志字符串、IP/域名模式和禁止的来源元数据字段。

## 非目标

- 不保存真实下游查询结果作为 golden。
- 不从日志全量或实时生产数据集。
- 不在生产代码中新增日志或采样旁路。
- 不以当前 PR 中的手写 fixture 作为数据协议。
- 不保证零流量后端在首期拥有生产 case；必须在采样报告中明确未覆盖项。

## 验收标准

- [x] 正式 case 均具备 input、route、dependencies 和 outputs，可完全离线运行。
- [x] runner 扫描数据集执行，无需在 Go 代码中逐条登记 case。
- [x] 支持一进多出、稳定归一化和重复请求保留。
- [x] 至少覆盖近期活跃的 VM、ES、BKSQL 和 InfluxDB 查询构造路径；BKSQL 覆盖已采到的执行形态。
- [x] 路由变化不会影响相同 fixture 的测试结果。
- [x] 敏感信息门禁可拦截结构化字段、嵌套字符串、内部地址和来源元数据泄漏。
- [x] 提交一份脱敏的采样收敛记录，记录每类每窗样本数、新增签名数、output 来源和未覆盖项。
- [x] 数据集校验和真实路径 golden 测试通过；与 PR 原始 head 对照后，相关 UQ 整包测试无新增失败。
