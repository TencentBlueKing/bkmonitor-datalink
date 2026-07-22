# UQ 查询 golden 数据集采样记录

## 口径

本记录只保存不可逆的结构统计，不保存原始日志、trace ID、内部路由值或真实查询内容。

- 采样来源：目标生产环境的历史 UQ 日志。
- 时间范围：4 个互不重叠的历史 10 分钟窗口，记为 W1～W4。
- 样本规模：对能够稳定关联 input/output 的分类，每个窗口最终取 20 条通过结构判定的可用样本；宽检索误命中只留在本地候选池。InfluxDB 的例外单列在后文，不计入该收敛口径。
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

## 修复驱动扩充

四窗收敛后又从一条生产问题请求中确认了新的组合形态：PromQL range input 包含 8 个 range selector，经解析形成 8 个 reference；每个 reference 只有 1 条 BKSQL 路由，并分别执行 schema 和 aggregate 查询，最终产生 16 个下游请求。该形态新增 `tspider_promql_multi_reference_001`，不改变前述四窗收敛统计。

这条链路中 BKBase 执行端记录为 HDFS，但 UQ 路由的 `measurement` 为空，实际选择的是 TSpider SQL 表达式；因此 case 按 UQ 待测 builder 归入 TSpider，而不是按 BKBase 内部执行设备归入 HDFS。问题发生时的 aggregate SQL 使用 `_timestamp_` 别名分组；修复合入后，golden expected 改为 `MAX(时间桶表达式)` 并按完整时间桶表达式分组，来源标记为 `post_fix_handler_replay`，不把失败 SQL 当作正确基线。

## InfluxDB 边界

SLI 显示 InfluxDB HTTP 路径仍有流量，但选定 UQ 日志链路中没有形成可稳定关联的 outbound GET 样本。源码会在 InfluxDB 查询 span 中记录查询参数，但对多个历史窗口的定向检索也没有拿到可用 span，因此不宣称其“生产 output 四窗收敛”。当前数据集保留一条来自生产入口形态的 InfluxDB aggregate case，并通过固定 route fixture 和真实 UQ handler 生成、截获 InfluxQL 请求；其 `source.outputs_kind` 明确标为 `handler_replay`。

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

- VM、ES、Doris、TSpider、HDFS：W4 新增未覆盖形似均为 0，本轮采集收尾。
- InfluxDB：离线 case 已覆盖，直接生产 output 采样证据尚未达到四窗收敛，边界如上。
- 后续问题修复：不受本轮收尾状态限制，必须新增对应回归 case。
