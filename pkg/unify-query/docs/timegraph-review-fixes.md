# TimeGraph Review 修复流程

## 入口与配置

默认继续使用有限资源保护。服务加载 `cmdb.v1beta3.yolo_mode` 后，
`settings.go` 的有效预算函数在普通模式返回配置值或默认值，在 YOLO 模式返回 0。
所有使用这些预算的判断仅在上限大于 0 时拒绝请求。
HTTP 的请求体和批次条数也使用同一模式判断，不再存在固定的 1 MiB / 16 项例外。
普通模式新增可配置的进程并发与内存预留保护，YOLO 绕过这些预算；输入校验、最大跳数配置、取消和超时保持原行为。
取数复用、紧凑输出与准入默认值见 [输出与资源优化](timegraph-output-optimization.md)。
完整默认值与 YAML 示例见 [共享构图与资源边界](timegraph-shared-memory.md)。

## 旧接口路径选择

`QueryResourceMatcher` 和 `QueryResourceMatcherRange` 通过
`buildTimeGraphRequest` 得到候选关系路径，先按跳数排序，再按资源、关系、类别、方向和指标名
稳定排序。`queryFirstTimeGraphPath` 逐条执行：

1. 检查请求取消状态。
2. 只将当前候选路径交给 TimeGraph 取数、构图和遍历，保留关系身份与 source_expand_info。
3. 当前路径有结果则立即返回，不执行后续路径。
4. 空结果或普通后端错误继续尝试下一条路径。
5. 没有任何命中时，只要存在失败路径就返回聚合错误；只有全部路径成功且均无数据才返回空结果。
6. 取消或 deadline 错误直接终止，不进入后续路径。

例如同时存在 A -> C 与 A -> B -> C 时，优先执行直连；直连失败后才执行两跳路径。
备用路径的错误不再影响已成功的直连结果。多路径 API 和完整拓扑 API 不套用此首路径短路。
这里没有添加跨请求缓存，也不会复用其他请求的图状态。

## Range 时间桶

旧 `QueryResourceMatcherRange` 始终以 step 作为每个桶的关系存活窗口。
原实现的 look_back_delta 影响整段原始数据的检索范围，不扩大或缩小单个桶；
现在后端直接评估每个桶，因此显式 look_back_delta 仍校验，但不传作单桶窗口。
`path_resources` 和 topology 的独立 lookback 语义不变。

旧 range 通过 `WithExactTimeGrid` 保留调用者的起点，源信息、关系、目标信息查询均不向下对齐；
VM 禁用会重对齐的结果缓存，并校验返回样本的网格。不能靠结果阶段重新分桶修复评估窗口的偏移。

旧 range 的每个桶为 `(timestamp-step, timestamp]`。VM 原生采用左开窗口；
内嵌旧 PromQL 引擎是双闭窗口，因此只在旧 range 上启用 `WithLeftOpenTimeWindow`，
由本地 Prometheus 适配器在查询 AST 中将 range selector 缩短 1ms（其样本精度为毫秒）。
普通查询和发送给 VM 的表达式不修改；start=end 的单桶请求也启用此处理。
VM 边界实现参考 [rollup.go](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/master/app/vmselect/promql/rollup.go)。

当前 QueryTs 和 VM 网关以整数秒传输时间，TimeGraph 在取数前拒绝非整秒时间戳或 step，
不再静默截断。整秒的毫秒格式仍受支持；此校验在 YOLO 下也生效。

## 显式自环

类型路径中的一次 hop 可以消费真实的 `a -> a` 自环，不能把它当成零跳根节点结果过滤。
遍历仍按显式路径长度逐层推进，并保留结果预算、方向和关系身份匹配；
经过不同节点再回到之前节点的普通回溯环仍被排除，不进行无界环路扩展。

## 有向指标名

`v1beta3SchemaProviderAdapter.ListRelationSchemas` 对未配置 MetricName 的有向关系，
调用生产端同一规则 `RelationDefinition.GetRelationName` 补全物理指标名。
名称在路径方向转换之前确定，因此 inbound 遍历也读取原始 from/to 对应的指标。
显式配置名称优先；无向关系继续使用已有的排序后 *_with_*_relation 回退。

## 扩展时间位图

`timeBitmap` 将前 64 位内联保存，更多位放入额外的 uint64 数组。
普通 60 点场景不为位数组分配内存；YOLO 查询的第 65 点使用第二个字的第 0 位，
第 129 点使用第三个字的第 0 位，不再发生移位后归零。

构图时，时间戳映射到整数索引，节点、边和属性版本分别保存对应的位图。
union、intersect、subtract 返回独立结果，不修改已有版本的底层数组。
遍历通过时间位相交传播可达节点；物化阶段按位读取各快照并复制属性。
因此时间点扩展同时覆盖边、可达性、历史属性、逻辑预算计数和最终响应，而非只修改网格校验。

## Matrix 累计计数

`timeGraphMatrixLoader.validateMatrix` 对每次校验遍历到的 Matrix 点累计计数，
与是否启用 topology 预算或 YOLO 无关。只有预算拒绝判断受模式控制；
空结果、已取消请求和已有后端错误不会增加计数。这样 legacy 和 YOLO 的
`matrix-cumulative-point-count` 不再错误地保持为 0，默认模式仍按跨次查询的总量拒绝超限。
该值是校验过程中已计数的点数，不替代按指标记录的完整后端返回规模。

`TestTimeGraphCumulativePointsIndependentOfBudgetMode` 覆盖 topology/legacy 与
默认/YOLO 四种组合、两次累计取数、默认累计预算拒绝、空结果、取消和后端错误。

## 回归验证

- `timegraph_review_regression_test.go`：候选失败隔离、短路与取消、短路径优先、缺省有向指标、真实 PromQL range 窗口、65 点公开接口。
- `timegraph_legacy_contract_test.go`：真实引擎验证桶边界、非整步起点和末桶、显式宽/窄 lookback、混合失败与空路径、全部加载阶段的网格和精度校验。
- `timegraph_self_relation_test.go` / `timegraph_differential_test.go`：真实自环、多次显式自环、普通环路保护、方向和随机路径独立枚举。
- `tsdb/prometheus/time_window_test.go`：左开窗口仅按需启用，覆盖 instant/range，原有默认行为不变。
- `timegraph_bitmap_test.go`：字边界和随机布尔集合对照，覆盖至 11001 点。
- `timegraph_shared_test.go`：共享图与逐时间点图的差分，包含 64、65、129、257 点、历史属性和 partial。
- `timegraph_yolo_test.go`：普通模式拒绝、YOLO 放行、关闭后恢复，及后端响应预算传播。
- `service/http/api/topology_budget_test.go`：直连/代理请求体、批量条数、响应预算和开关恢复。

这些测试验证本地行为，不替代真实 VM 数据回放或生产容量测试。

2026-09-28 本地验证通过，在 `pkg/unify-query` 目录执行：

```bash
GOTOOLCHAIN=go1.24.4 GOFLAGS=-count=1 make test
GOTOOLCHAIN=go1.24.4 go vet -stdmethods=false -tags=jsonsonic ./cmdb/... ./service/http/api ./service/http/proxy ./metric ./curl ./metadata ./tsdb/prometheus ./tsdb/victoriaMetrics
GOTOOLCHAIN=go1.24.4 go test -race -tags=jsonsonic -count=1 ./cmdb ./cmdb/v1beta3 ./service/http/api ./service/http/proxy ./curl ./metric ./metadata ./tsdb/prometheus ./tsdb/victoriaMetrics
```

`pkg/utils/relation` 测试、Linux amd64 无 CGO 发布构建、增量 golangci-lint v1.64.8、
`git diff --check`、两个 dashboard 的 PromQL/面板 ID 校验、Swagger JSON/YAML 一致性校验通过。
构建使用 `make -o tidy build`，避免非必要的依赖文件改写。

默认 `go vet` 在未修改的 `tsdb/prometheus/selector_cache.go` 及其测试中仍有两条
`stdmethods` 告警：Prometheus 迭代器的 `Seek(int64) chunkenc.ValueType` 不符合标准库
`io.Seeker` 签名。上述命令只关闭这一项，其余分析器正常执行。race 验证范围不包含
旧 bbolt 依赖的 v1beta1 包；其常规测试包含在全量测试中。

## 环境验收

本次没有修改线上配置或部署服务。共享 test 正被其他人使用，待确认可用后再发布。
本地验证不替代以下验收：

- 用真实 VM 数据核对非整步起点、末桶、桶边界、自关联和首条命中路径。
- 默认模式核对批次、时间点和数据预算；在隔离 Pod 开启 `cmdb.v1beta3.yolo_mode` 后核对超预算查询，再关闭确认保护恢复。
- 对照仪表盘检查结果完整性、超时、内存和响应大小；YOLO 不解除下游自身限制，不作为生产容量保证。
