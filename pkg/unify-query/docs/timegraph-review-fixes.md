# TimeGraph Review 修复流程

## 入口与配置

默认继续使用有限资源保护。服务加载 `cmdb.v1beta3.yolo_mode` 后，
`settings.go` 的有效预算函数在普通模式返回配置值或默认值，在 YOLO 模式返回 0。
所有使用这些预算的判断仅在上限大于 0 时拒绝请求。
HTTP 的请求体和批次条数也使用同一模式判断，不再存在固定的 1 MiB / 16 项例外。
并发活动计数不限制请求数量；输入校验、最大跳数配置、取消和超时保持原行为。
完整默认值与 YAML 示例见 [共享构图与资源边界](timegraph-shared-memory.md)。

## 旧接口路径选择

`QueryResourceMatcher` 和 `QueryResourceMatcherRange` 通过
`buildTimeGraphRequest` 得到候选关系路径，先按跳数排序，再按资源、关系、类别、方向和指标名
稳定排序。`queryFirstTimeGraphPath` 逐条执行：

1. 检查请求取消状态。
2. 只将当前候选路径交给 TimeGraph 取数、构图和遍历，保留关系身份与 source_expand_info。
3. 当前路径有结果则立即返回，不执行后续路径。
4. 空结果或普通后端错误继续尝试下一条路径。
5. 全部路径报错时返回聚合错误；至少一条成功但均无数据时返回空结果。
6. 取消或 deadline 错误直接终止，不进入后续路径。

例如同时存在 A -> C 与 A -> B -> C 时，优先执行直连；直连失败后才执行两跳路径。
备用路径的错误不再影响已成功的直连结果。多路径 API 和完整拓扑 API 不套用此首路径短路。
这里没有添加跨请求缓存，也不会复用其他请求的图状态。

## Range 时间桶

旧 `QueryResourceMatcherRange` 在未传 look_back_delta 时，将已解析的 step 作为回溯窗口，
然后交给底层生成 count_over_time。step=1m 会生成一分钟窗口，而不是默认一天。
显式指定 look_back_delta 时继续遵循现有显式窗口；新 topology 的独立 lookback 语义不变。
因此只有一小时前样本的关系不会再出现在当前一分钟桶中。

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

## 回归验证

- `timegraph_review_regression_test.go`：候选失败隔离、短路与取消、短路径优先、缺省有向指标、真实 PromQL range 窗口、65 点公开接口。
- `timegraph_bitmap_test.go`：字边界和随机布尔集合对照，覆盖至 11001 点。
- `timegraph_shared_test.go`：共享图与逐时间点图的差分，包含 64、65、129、257 点、历史属性和 partial。
- `timegraph_yolo_test.go`：普通模式拒绝、YOLO 放行、关闭后恢复，及后端响应预算传播。
- `service/http/api/topology_budget_test.go`：直连/代理请求体、批量条数、响应预算和开关恢复。

这些测试验证本地行为，不替代真实 VM 数据回放或生产容量测试。

2026-09-28 本地验证通过，在 `pkg/unify-query` 目录执行：

```bash
GOTOOLCHAIN=go1.24.4 make test
GOTOOLCHAIN=go1.24.4 go vet -tags=jsonsonic ./cmdb/... ./service/http/api ./service/http/proxy ./metric ./curl ./metadata ./tsdb/victoriaMetrics
GOTOOLCHAIN=go1.24.4 go test -race -tags=jsonsonic ./cmdb/v1beta3 ./service/http/api
```

`git diff --check` 通过。本次没有修改线上配置或部署服务。
