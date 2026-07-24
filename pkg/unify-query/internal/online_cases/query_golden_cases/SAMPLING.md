# UQ 查询 golden 数据集采样记录

## 口径

本记录只保存不可逆的结构统计，不保存原始日志、trace ID、内部路由值或真实查询内容。

- 采样来源：目标生产环境的历史 UQ 日志。
- 时间范围：原有 4 个互不重叠的历史 10 分钟窗口记为 W1～W4；2026-07-23 继续采集 W5（13:25～13:35 UTC）和 W6（13:35～13:45 UTC），2026-07-24 采集 W7（02:50～03:00 UTC）；三个窗口均已结束且互不重叠。
- 样本规模：对能够稳定关联 input/output 的分类，每个窗口最终取 20 条通过结构判定的可用样本；VM 因 W5 首次出现 instant，range/instant 分层各取 20 条。宽检索误命中只留在本地候选池。InfluxDB 的例外单列在后文，不计入该收敛口径。
- 去重单位：会改变 UQ 下游请求构造的形似特征，而不是指标名、表名、标签名或字面值。
- 收尾条件：最后一个窗口中的可用样本全部能由正式 case 的形似覆盖。

最近 10 分钟 SLI 只用于判断分类优先级和控制采样量级，不作为 golden 输入。观察到的下游量级约为：VictoriaMetrics 201 万、Elasticsearch 11.8 万、BKSQL 4.9 万、InfluxDB HTTP 0.32 万；因此采用分层小样本而不是按流量比例采集。

## 形似定义

本轮按影响 query builder 的正交特征归并：

- VM：single/multi result table、简单/复杂 PromQL、reference 数量、聚合/函数/区间/二元表达式、matcher 类型。
- ES：aggregate/raw、有无 query_string；index/mapping 与 search 均属于 output 序列。
- BKSQL：schema、aggregate、raw，并按 Doris、TSpider、HDFS 分开。
- 路由：单路由、数据标签多 RT 合并、时间分段跨存储展开。
- InfluxDB：aggregate/raw、条件、group by time、控制参数。

形似按特征覆盖，不枚举所有特征的笛卡尔积。完整 `expect.outputs.json` 仍做逐字段比较，形似签名不替代 golden 比较。

## 四窗结果

### VictoriaMetrics

| 窗口 | single RT | multi RT | 可用样本 | 新增未覆盖形似 |
| --- | ---: | ---: | ---: | ---: |
| W1 | 14 | 6 | 20 | 1（multi RT） |
| W2 | 14 | 6 | 20 | 0 |
| W3 | 18 | 2 | 20 | 0 |
| W4 | 15 | 5 | 20 | 0 |

入口 PromQL 样本均为 range 查询。除简单 selector 外，还观察到聚合、函数、range selector、二元表达式、正则/否定 matcher 和多 reference；这些正交特征由 `vm_promql_complex_binary_001` 覆盖，多 RT 合并由 `vm_multi_result_table_001` 覆盖。

### Elasticsearch

| 窗口 | aggregate/no query_string | aggregate/query_string | raw/no query_string | raw/query_string | 可用样本 | 新增未覆盖形似 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| W1 | 14 | 5 | 1 | 0 | 20 | 3 |
| W2 | 17 | 2 | 1 | 0 | 20 | 0 |
| W3 | 16 | 2 | 0 | 2 | 20 | 1（raw/query_string） |
| W4 | 17 | 0 | 0 | 3 | 20 | 0 |

最终 4 个 ES case 与四种形似一一对应。

### Doris

| 窗口 | schema | aggregate | raw | 可用样本 | 新增未覆盖形似 |
| --- | ---: | ---: | ---: | ---: | ---: |
| W1 | 0 | 9 | 11 | 20 | 1（raw） |
| W2 | 0 | 0 | 20 | 20 | 0 |
| W3 | 0 | 2 | 18 | 20 | 0 |
| W4 | 9 | 0 | 11 | 20 | 0 |

`schema` 是 aggregate/raw 查询构造中的依赖阶段，已包含在两类 case 的 output 序列中。另有一个固定 ES→Doris 时间分段 case 覆盖跨存储展开。

### TSpider

| 窗口 | schema | aggregate | raw | 可用样本 | 新增未覆盖形似 |
| --- | ---: | ---: | ---: | ---: | ---: |
| W1 | 7 | 13 | 0 | 20 | 0 |
| W2 | 11 | 9 | 0 | 20 | 0 |
| W3 | 9 | 11 | 0 | 20 | 0 |
| W4 | 12 | 8 | 0 | 20 | 0 |

宽采样中的 raw 占比很低；额外的稀有形似定向样本确认了 raw SQL，因此补充 `tspider_raw_001`。schema、aggregate、raw 均已覆盖。

### HDFS

| 窗口 | schema | aggregate | raw | 可用样本 | 新增未覆盖形似 |
| --- | ---: | ---: | ---: | ---: | ---: |
| W1 | 11 | 6 | 3 | 20 | 1（raw） |
| W2 | 11 | 6 | 3 | 20 | 0 |
| W3 | 12 | 5 | 3 | 20 | 0 |
| W4 | 11 | 4 | 5 | 20 | 0 |

HDFS 查询日志容易被同名指标误命中，因此先扩大本地候选池，再按 SQL 目标结构固定取 20 条。误命中不计入表中。

## W5～W7 增量采样

本轮以数据集首次合入提交 `73273428d556b521571df28cd9840d70a4210314` 为 Git 证据下界。运行实例可确认使用不可变镜像摘要，但当前证据无法把该摘要精确映射到 Git commit，因此不把运行时行为声明为某个未验证源码版本；正式 fixture 以已关联生产 input/output 的结构为依据，并在上述 Git 基线执行真实 handler 回放。

| 分类 | W5 候选 | W5 可用 | W5 选取 | W5 新增形似 | W6 候选 | W6 可用 | W6 选取 | W6 新增形似 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| VM（range + instant 分层） | 60 | 50（28 + 22） | 40 | 2 | 60 | 49（28 + 21） | 40 | 0 |
| Elasticsearch | 30 | 25 | 20 | 1 | 30 | 27 | 20 | 0 |
| Doris | 30 | 21 | 20 | 1 | 30 | 21 | 20 | 0 |
| TSpider | 30 | 30 | 20 | 0 | 30 | 28 | 20 | 0 |
| HDFS | 30 | 30 | 20 | 0 | 30 | 30 | 20 | 0 |

W5 新增并物化了四个生产形似：

- VM：PromQL instant 与结构化 instant，分别由 `vm_promql_instant_001`、`vm_structured_instant_001` 覆盖。结构化 range 使用同一 QueryTs 解析路径，range VM builder 已由既有 PromQL range case 约束；按正交特征覆盖，不再复制其笛卡尔组合。
- ES：`/query/ts/reference` 的 index/mapping + search 两阶段，由 `es_reference_001` 覆盖。
- Doris：一个 reference 经七条同存储 data-label 路由生成七条 schema 和七条 query，由 `doris_multi_route_fanout_001` 覆盖。
- TSpider、HDFS：观察到的 1/2/3/8 reference 放大仍是既有 parser reference、单路由 builder 和 schema/query 两阶段的组合，没有新增构造因素。

W6 再次按相同口径取得每层 20 条可用样本，全部能由正式 case 的正交形似覆盖。W6 还观察到 ES 的 3/5 路由 output 放大、TSpider/HDFS 的多 reference 放大；前者由 ES mapping/search 阶段 case 与同存储 data-label 多路由 case 共同约束，后者由对应单路由 builder 与多 reference case 共同约束。遵循“不枚举全部特征笛卡尔积”的口径，本轮不为这些组合重复增加 fixture。

候选 gate 要求一个入口、一个 handler、入口 2xx、无 error span、output payload 完整。W5/W6 的候选数高于最终选取数；被拒绝的多入口 trace、非 2xx、error span 或 payload 不完整样本只保留在本地缓存，不进入正式目录。

### W7 复验

W7 以包含上述四个新增 case 的 34 case 数据集作为覆盖基线，再取一个不重叠的历史 10 分钟窗口。生产 trace 未提供可用的服务版本字段，当前只读运行时证据也无法确认部署 commit，因此不把生产行为声明为某个源码版本；形似判断只使用已关联的生产 input/output 结构，离线回放以本次数据集 checkout 为 Git 基线。

| 分类 | 候选 | 可用 | 选取 | 新增未覆盖形似 |
| --- | ---: | ---: | ---: | ---: |
| VM range | 30 | 27 | 20 | 0 |
| VM instant | 30 | 20 | 20 | 0 |
| Elasticsearch | 30 | 24 | 20 | 0 |
| Doris | 60 | 27 | 20 | 0 |
| TSpider | 30 | 30 | 20 | 0 |
| HDFS | 30 | 30 | 20 | 0 |

Doris 首批 30 个候选只有 14 条通过 gate，因此继续分页并按 trace 去重，将候选扩大到 60 条后取得 27 条可用请求。最终每类固定选取 20 条进行形似判断。

W7 的 VM range 选取样本包含结构化和 PromQL 入口，VM instant 选取样本包含 5 条结构化入口和 15 条 PromQL 入口；两类的 handler、instant/range 和 VM query/query_range 构造均已有 case 约束。ES 观察到一条单 reference 经 51 条 ES 路由生成 51 组 mapping/search 请求；这是 W6 已见同后端多路由机制的数量扩大，不增加新的路由或 backend 阶段。Doris 仍为单/双 reference、单路由、同存储多路由和跨存储展开的既有组合；TSpider、HDFS 仍为已有 reference 放大与 schema/query 两阶段组合。按正交因素覆盖且不枚举特征笛卡尔积的口径，六层选取样本的新增未覆盖形似均为 0。

## 修复驱动扩充

四窗收敛后又从一条生产问题请求中确认了新的组合形态：PromQL range input 包含 8 个 range selector，经解析形成 8 个 reference；每个 reference 只有 1 条 BKSQL 路由，并分别执行 schema 和 aggregate 查询，最终产生 16 个下游请求。该形态新增 `tspider_promql_multi_reference_001`，不改变前述四窗收敛统计。

这条链路中 BKBase 执行端记录为 HDFS，但 UQ 路由的 `measurement` 为空，实际选择的是 TSpider SQL 表达式；因此 case 按 UQ 待测 builder 归入 TSpider，而不是按 BKBase 内部执行设备归入 HDFS。问题发生时的 aggregate SQL 使用 `_timestamp_` 别名分组；修复合入后，golden expected 改为 `MAX(时间桶表达式)` 并按完整时间桶表达式分组，来源标记为 `post_fix_handler_replay`，不把失败 SQL 当作正确基线。

### 最近 90 天已合并 UQ PR 回溯

2026-07-23 按 `mergedAt >= 2026-04-24` 且改动 `pkg/unify-query/` 的口径，从仓库同期 87 个已合并 PR 中筛出 41 个 UQ PR，并逐个检查问题语义、代码路径、原有测试和 golden 覆盖。19 个 PR 涉及 parser、route expansion、query builder 或稳定的下游请求构造，其中 8 个由上一轮 case 覆盖，2 个由已有 case 精确覆盖，本轮为其余 9 个补充 case；另外 22 个只影响响应处理、并发安全、观测、性能、错误契约或测试，不适合用正向 downstream-output golden 表达。

| PR | 查询处理影响 | Golden 处理 |
| --- | --- | --- |
| #1413 | TSpider 时间桶错误按 `_timestamp_` 别名分组 | 既有 `tspider_promql_multi_reference_001` 精确覆盖 |
| #1400 | Doris 对象叶子大小写与 `DATETIMEV2` 精度 | 既有 `doris_union_object_leaf_precision_case_001` 精确覆盖 |
| #1401 | TSpider FieldsMap 缺失及错误使用 Doris 时间字段 | 既有 `tspider_promql_multi_reference_001` 精确覆盖 |
| #1399 | Doris 多表 `SELECT *` 的公共字段和安全类型交集 | 既有 `doris_union_select_all_type_intersection_001` 精确覆盖 |
| #1393 | ES query_string 方括号短语被 regexp contains 逻辑补宽 | 既有 `es_query_string_regexp_bracket_phrase_001` 精确覆盖 |
| #1397 | Doris 多表字段漂移时的显式 UNION 投影 | 既有 `doris_union_explicit_projection_001` 精确覆盖 |
| #1384 | 大小写不敏感 ES 字段仍使用大写 regexp | 既有 `es_query_string_regexp_case_insensitive_001` 精确覆盖 |
| #1372 | 只修复 BKSQL 单测自身的 nil 断言 | 范围外；没有运行时下游构造变化 |
| #1371 | 只补请求完成后的 route validation span 属性 | 范围外；没有下游构造变化 |
| #1333 | ES→Doris 时间分段路由及 BKSQL `cluster_name` | 既有 `doris_es_segmented_multi_output_001` 精确覆盖 |
| #1363 | 显式 table/data label 选表时因字段元数据尚未同步而误删候选 RT | 新增 `es_explicit_table_field_missing_fallback_001` |
| #1364 | mapping 为大小写敏感 analyzer 时 wildcard 被错误转为小写 | 新增 `es_query_string_wildcard_case_sensitive_mapping_001` |
| #1361 | stat 接口 JSON 响应序列化 | 范围外；属于响应输出 |
| #1360 | 查询引擎初始化 nil 防护 | 范围外；没有稳定的下游请求差异 |
| #1356 | 否定分支的 query_string 枚举值错误进入聚合 `terms.include` | 新增 `es_aggregate_query_string_global_include_001` |
| #1354 | 显式 AND 链中的隐式词项错误落入 `should` | 新增 `es_query_string_explicit_and_implicit_terms_001` |
| #1346 | 响应中的 route_info 展示 | 范围外；属于响应输出 |
| #1335 | ES/Doris 分段查询和结果加权合并 | 请求构造由 `doris_es_segmented_multi_output_001` 覆盖；结果加权属于响应处理 |
| #1355 | ES regexp 大小写规范化 | 既有 `es_query_string_regexp_case_insensitive_001` 精确覆盖，修复前回放得到不同 DSL |
| #1343 | 独立 ES 查询服务注册及 alias 刷新并发修复 | 范围外；不是结构化 query handler 的下游构造协议 |
| #1344 | 共享查询对象的并发修改隔离 | 范围外；单次固定回放没有确定的 downstream 差异，保留原有并发测试 |
| #1345 | 多子查询复用对象导致状态污染 | 范围外；单次固定回放无法稳定表达，保留原有单测 |
| #1338 | CMDB relation path 的确定性排序 | 范围外；golden 使用固定 route fixture，刻意绕开图路径选择 |
| #1340 | ES shard 遥测 | 范围外；只影响观测 |
| #1337 | query 对象复用时的并发状态隔离 | 范围外；由原有并发/竞态测试兜底 |
| #1336 | 查询 goroutine 泄漏 | 范围外；属于生命周期与资源回收 |
| #1328 | 无指标名的非法 PromQL panic | 范围外；当前协议只比较成功下游请求，负向错误契约由原有单测兜底 |
| #1320 | ES highlight 结果处理 | 范围外；属于查询结果后处理 |
| #1321 | raw scroll 的 DeepCopy 崩溃与并发安全 | 范围外；没有稳定的成功请求形似差异 |
| #1319 | 路由同步指标 | 范围外；只影响观测 |
| #1314 | 多 RT 路由中 db/dbs 为空的无效 ES 子任务阻断有效查询 | 新增 `es_multi_route_empty_index_skipped_001` |
| #1315 | Check 接口响应补充子查询路由与结果表 | 范围外；属于响应输出 |
| #1316 | SaaS data source 枚举未归一化为 UQ 内部枚举 | 新增 `es_data_source_alias_bk_log_search_001` |
| #1299 | trace 埋点精简 | 范围外；只影响观测 |
| #1310 | ES 查询结果 flatten | 范围外；属于查询结果后处理 |
| #1311 | GetStorage 性能和指标 | 范围外；不改变下游请求 |
| #1307 | Doris 缺失字段的 contains 条件生成不支持的 `NULL MATCH_PHRASE` | 新增 `doris_missing_field_contains_001` |
| #1297 | field_map 参数解析丢失 `table_id_conditions` | 新增 `es_info_field_map_table_conditions_001`，并让回放器执行真实 field_map handler |
| #1300 | raw 多路查询部分成功响应策略 | 范围外；不改变各子查询的构造 |
| #1298 | BKSQL 结果格式化 | 范围外；属于查询结果后处理 |
| #1296 | 显式 `table_id_conditions` 被 K8s split-measurement 默认规则误过滤 | 新增 `es_table_id_conditions_k8s_non_split_001` |

本轮 9 个新增 case 的问题形态来自已合并 PR 的问题描述、回归测试和代码修复，不保留原始 trace ID。每个 case 均使用同一份脱敏 request、固定 route/dependencies，在对应修复 PR 的父 commit 先得到 RED，再在当前代码得到 GREEN；当前 expected 由真实 handler 生成并标记为 `post_fix_handler_replay`。它们的 `source.kind` 均为 `merged_pr`，不声称来自已关联的生产日志或 trace，也不计入 W1～W4 分类采样的 production output 收敛统计。

历史 RED 的判定点如下：

| PR | 修复前可观察差异 |
| --- | --- |
| #1364 | wildcard 值 `ERROR`、`Traceback` 被转成小写 |
| #1363 | handler 以 `SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS` 结束，没有生成 ES 请求 |
| #1356 | 仅出现在否定分支的枚举值仍进入 `terms.include` |
| #1354 | AND 链中的两个隐式词项落入 `should`，没有全部进入 `must` |
| #1314 | 空索引 RT 触发 `QUERY_RAW_PARTIAL`，有效 RT 也无法继续 |
| #1316 | 未归一化别名导致字段选表失败，没有生成 ES 请求 |
| #1307 | SQL 生成 `NULL MATCH_PHRASE 'demo'`，而不是修复后的 `NULL = 'demo'` |
| #1297 | handler 入参中的 `table_id_conditions` 丢失，没有发起 index/mapping 请求 |
| #1296 | K8s 默认过滤误删显式条件命中的 RT，handler 以选表失败结束 |

## InfluxDB 边界

SLI 显示 InfluxDB HTTP 路径仍有流量，但选定 UQ 日志链路中没有形成可稳定关联的 outbound GET 样本。源码会在 InfluxDB 查询 span 中记录查询参数，但对多个历史窗口的定向检索也没有拿到可用 span，因此不宣称其“生产 output 四窗收敛”。当前数据集保留一条来自生产入口形态的 InfluxDB aggregate case，并通过固定 route fixture 和真实 UQ handler 生成、截获 InfluxQL 请求；其 `source.outputs_kind` 明确标为 `handler_replay`。

W5、W6 和 W7 又分别定向检索 `influxdb-client-query`、`influxdb-query-select`、`query-info-influxdb-query-select`、`raw-query` 四类 span，仍均为 0。该结果只说明当前 trace 证据链不可用，不解释为无流量或已收敛。

这不影响离线回放能力，但属于采样证据边界：后续一旦日志或 trace 能稳定拿到 InfluxDB outbound 请求，应按同一流程补做 aggregate/raw 四窗核验，若出现新形似则新增 case。

## 一进多出结论

已关联的代表请求只有一个入口和一个逻辑 reference。固定 route fixture 为同一结果表提供 ES→Doris 的时间分段记录后，真实 UQ 路径生成 4 个下游请求：

```text
one logical request
  -> route time segmentation
  -> ES segment + Doris segment
  -> ES index/mapping + ES search + Doris schema + Doris query
```

控制测试保持 input 不变，只从 fixture 删除 ES 分段，实际输出随即只剩 Doris schema + query 两条。因此该“一进多出”由路由展开和每个后端的前置查询共同导致，不是解析器重复解析。将这份路由固化在 `route.json` 后，线上路由变化不会改变回放结果。

新增的 PromQL 多 reference case 是另一种一进多出：`1 input × 8 parser references × 1 fixed route × 2 BKSQL stages = 16 outputs`。这里 reference 数由 input 解析结构决定，schema/query 两阶段由 BKSQL builder 决定；固定 route 只有一条，不存在路由分段放大。两类 case 共同把解析展开与路由展开分开验证。

## 收尾状态

- VM、ES、Doris、TSpider、HDFS：W5 新增形似已物化，W6 和 W7 每层选取的 20 条可用样本新增未覆盖形似均为 0，本轮采集收尾。
- InfluxDB：离线 case 已覆盖，直接生产 output 采样证据尚未达到收敛，边界如上。
- 后续问题修复：不受本轮收尾状态限制，必须新增对应回归 case。
