# TimeGraph 关系查询接入指南

本页、[OpenAPI](../swagger.yaml)、[默认资源目录](timegraph-schema.md)和[可运行示例](../examples/topology-client.mjs)随同一产品提交交付。默认值指本版本内置值；实际部署可以覆盖配置。示例全部使用合成的 `example-cluster` / `demo` 数据，不是可直接在任意环境命中的对象。

## 选择接口

以下全部为 POST，JSON 请求均为 `{"query_list":[...]}`。响应按输入顺序对应逐项结果。直接接口成功外层为 `{"trace_id":"...","data":[...]}`；HTTP 200 后仍须检查每个 `data[i].code`，不能只检查第一个结果。

| 精确路径 | 适用目的 | 请求 DTO | 成功项主要字段 |
| --- | --- | --- | --- |
| `/api/v1/relation/multi_resource` | 保持已有单点目标查询 | RelationMultiResourceRequest | target_list、path，按请求可有 paths |
| `/api/v1/relation/multi_resource_range` | 保持已有分桶目标查询 | RelationMultiResourceRangeRequest | 按时间的 target_list、path/paths |
| `/api/v1/relation/v1beta3/multi_resource` | TimeGraph 实现的旧单点目标合同 | RelationMultiResourceRequest | 同上，不是图 DTO |
| `/api/v1/relation/v1beta3/multi_resource_range` | TimeGraph 实现的旧范围目标合同 | RelationMultiResourceRangeRequest | 同上，不是完整逐帧图 |
| `/api/v1/relation/path_resources` | 查询一组显式或推导路径，保留沿途资源 | RelationPathResourcesRequest | results：timestamp、target_type、path |
| `/api/v1/relation/path_resources_range` | 查询范围内的完整路径资源 | RelationPathResourcesRangeRequest | 同上；只返回命中的路径 |
| `/api/v1/relation/v1beta3/topology` | 指定时刻的完整局部拓扑 | SharedTopologyRequest | snapshots 或 compact-v1 |
| `/api/v1/relation/v1beta3/topology_range` | 指定统一时间网格的完整局部拓扑 | SharedTopologyRequest | snapshots 或 compact-v1，保留空帧 |

找一个目标、告警补全或原 APM 路径需求可继续使用目标/路径接口，不必下载完整图。查询中的 `target_type`、路径接口的 `target_types` 与 topology 的输出类型过滤不是同一语义。

### 鉴权、空间与封装

- 发送 `Content-Type: application/json` 和调用者有权限的 `X-Bk-Scope-Space-Uid`。空间从请求头写入请求上下文，不从 topology 单项的 JSON `space_uid` 读取；没有内置的 `bkcc__2` 请求头默认值。
- 公共 HTTP 路由使用 JWT 中间件。`jwt.enabled=true` 时需合法的网关签名 `X-Bkapi-Jwt`，并按服务配置校验应用/空间授权；失败为 HTTP 401。关闭 JWT 校验不代表外部网关也免鉴权。这里的关系 handler 不执行 BasicAuth；OpenAPI 全局保留的 BasicAuth 定义不是这些接口的实际鉴权保证。
- `traceparent` 可选，用于串联调用链。请从部署入口获取凭据，不在请求文件、示例或日志中保存 token。
- 本页示例调用直接关系路径。若使用部署配置的代理路径，需按其 `path/data` 入参及 `result/data/message` 外层包装取出上述响应，不能把代理的 `result=true` 当成所有 query item 成功。取消、序列化和最终写出都属于请求生命周期。

## Topology 参数及图语义

| 字段 | 必填、默认与组合规则 |
| --- | --- |
| query_list | 有序数组；省略或空数组得到空 data。普通模式内置最大 16 项，YOLO 跳过该数量限制 |
| source_type | 两种 topology 接口均必填，使用实际 Schema 的资源类型 |
| source_info | 字符串维度映射。完整主键定位一个身份；部分或空身份可能匹配多个根。身份字段见资源目录 |
| source_expand_info | 显式根属性条件，按帧筛根；只接受 Schema 已定义字段。可用主键补全身份，与 source_info 同键不同值会失败；非主键不会覆盖身份，也不限制邻域属性 |
| timestamp | topology 必填、非零；不得同时传 start_time/end_time |
| start_time、end_time | topology_range 必须同时提供且非零；不得传 timestamp；结束点包含在内 |
| step | range 正整秒 Go duration，省略为 `1m`；end-start 必须整除 step。单点接口不使用该字段，响应 step 为 `0s` |
| look_back_delta | 正 Go duration，省略用配置值（内置 `24h`）。与 step 独立 |
| max_hops | 省略或 0 使用配置默认值（内置 2），负数拒绝。普通模式配置上限内置 5；YOLO 跳过该上限，不静默缩小 H |
| allowed_categories | 关系类别白名单：通常 static、dynamic。空为不限，未知值匹配不到关系 |
| allowed_relation_types | 精确关系名白名单，与类别范围取交集；空为不限，未知名匹配不到关系，不自动转换别名 |
| dynamic_relation_direction | both（默认）、outbound、inbound。控制定向关系遍历；非定向静态关系仍可双向遍历 |
| target_types | **遍历后**保留这些类型及所有可达的 source_type 节点。删除其他类型及其关联边；不会把中间节点折叠为新边。空为保留全部可达类型 |
| response_format | 单项选择 snapshots（省略/空时默认）或 compact-v1；同批次允许混用 |

时间输入小于 `1e12` 按 Unix 秒解释，否则按 Unix 毫秒解释；必须对应整秒，不接受负数或亚秒精度。所有图响应 start_time/end_time/timestamp/timestamps 均为**毫秒**。range 点数为 `(end-start)/step+1`；普通模式上限内置 60，配置可降低，YOLO 不受此应用上限约束。超限拒绝，不更改 step 或丢帧。

关系按每个评估点的 `count_over_time` 回看窗口观测判活。窗口内看见多个关系不等价于证明它们在某一真实时刻同时存在。输出为在每帧内 H 跳可达节点的诱导边；不因跨 namespace 而隐式删边。

根必须出现在关系端点中；只有 info、没有关系的孤立实体不会凭空进入拓扑。默认图仅保证已观测关系及其现有属性，**不能把缺少可选属性解释为属性值为空，也不保证总是额外读取 info 指标**。显式属性选根或按需详情应明确查询 info 条件；本页详情样例重复一个相同的主键条件以触发 source info 读取。邻居不会被根的 version 条件过滤。历史属性是所选窗口的观测值，外部 CMDB 当前详情不可冒充历史值。

### 普通模式和 YOLO

普通模式按实际活跃结构预约内存，内置共享预约总额 4 GiB、内存余量 512 MiB、执行槽 8、等待队列容量同执行槽上限；请求体内置 1 MiB、编码输出 64 MiB、后端字节上限 16 MiB。配置键和阶段计量见[资源与输出说明](../timegraph-output-optimization.md)。排队开始即使用同一个内部期限（内置 60 秒），较短的父期限优先；运行中资源增长失败立即拒绝，不等待内存而占住执行槽。

YOLO 通过 `cmdb.v1beta3.yolo_mode` 显式启用，默认关闭。启用时绕过上述 TimeGraph 应用预算、H/K/批次上限及内部期限，保持完整结果；仍响应父 context 的取消/期限。HTTP socket、网关、客户端、容器资源仍是独立运行条件。此页不更改部署配置、不宣称任意规模都能在固定时间内完成。

## 按场景运行

### 先离线查看，再连接自己选定的环境

需要 Node.js 22+（无第三方依赖）。在 `pkg/unify-query` 下运行：

```sh
node docs/examples/topology-run.mjs known-node
node docs/examples/topology-run.mjs attribute-roots
node --test docs/examples/topology-client.test.mjs
```

默认只解码随代码提交的[实际本地 HTTP 请求/响应](../examples/topology-responses.json)，不联网。完整[场景请求](../examples/topology-scenarios.json)包括：

| 场景名 | 意图及合成数据预期 |
| --- | --- |
| known-node | node-a 一跳到 pod-a，三帧均 2 节点/2 条静态方向边 |
| known-pod | pod-a 一跳到 node-a、app-a，三帧均 3 节点/4 边 |
| deployment-workload | web 经 web-rs 到两个 Pod，H2 三帧均 4 节点/6 边 |
| attribute-roots | container 的 version=v2；首帧只有 app-b，后两帧 app-a/app-b 均命中，共 2/4/4 节点 |
| historical-detail | 完整身份 app-a 加 source_expand_info.container=app-a，按需读 source info；version 为 v1/v2/v2 |
| output-filter | deployment-workload 的 target_types=[pod]，保留 Deployment 和两个 Pod，3 节点但 0 边；被过滤的 ReplicaSet 不被折叠 |
| empty-roots | 不存在的 Node，保留三帧空图且 partial=false |
| legacy-target | 继续用旧目标 DTO，得到三桶 pod-a 及 node→pod 路径 |
| explicit-path | path_resources_range 显式 node→pod 路径，得到三个时间点的完整路径 |

应用可复用轻量封装：

```js
import { queryRelations, readTopologyItem } from './docs/examples/topology-client.mjs';
const abortController = new AbortController();
const response = await queryRelations({
  baseURL: process.env.UQ_URL,
  spaceUID: process.env.UQ_SPACE_UID,
  path: 'v1beta3/topology_range',
  headers: process.env.UQ_JWT ? { 'X-Bkapi-Jwt': process.env.UQ_JWT } : {},
  signal: abortController.signal,
  queryList: [{
    start_time: 1700000000, end_time: 1700000120, step: '1m',
    source_type: 'container',
    source_info: { bcs_cluster_id: 'example-cluster', namespace: 'demo' },
    source_expand_info: { version: 'v2' },
    max_hops: 1, look_back_delta: '5m',
    allowed_relation_types: ['container_with_pod'], response_format: 'compact-v1'
  }]
});
for (const item of response.data) {
  const reader = readTopologyItem(item);
  if (!reader.ok) { console.error(reader.code, reader.message); continue; }
  for (let i = 0; i < reader.pointCount; i++) {
    const frame = reader.frame(i); // Check frame.partial and partial_reason before use.
    console.log(frame.timestamp, frame.nodes.length, frame.edges.length, frame.partial);
  }
}
```

以上时间和身份仅对应合成夹具；连接环境前替换为有权限且已写入的实际输入。`abortController` 由调用者创建和取消。可将实际 `{"query_list":[...]}` 存入 request.json，再显式执行：

```sh
# Set UQ_URL and UQ_SPACE_UID in your environment; UQ_JWT is optional as configured.
node docs/examples/topology-run.mjs --live request.json v1beta3/topology_range
```

wrapper 完整序列化 query_list，不白名单丢弃 source_expand_info，不调整 H、step 或返回范围，也不自动重试。接入自己的 serializer 时须重放相同验收；现有页面/CLI 的上线是消费方独立工作。

## 输出、解码与身份

snapshots 单项返回 start_time/end_time/step/point_count/snapshots；成功空图仍有对应时间帧、nodes=[]、edges=[]。失败项 snapshots=[]、point_count=0，须先看 code。compact-v1 单项带 response_format 和 compact，失败时 compact=null；不能把它当成成功空图。

compact 版本为 `version:1`；timestamps 是整份网格，节点/边按属性版本附带 mask。每个 mask word 是 16 位十六进制 uint64，最低位对应这一 word 的首帧；第 64 帧进入第二个 word。节点维度变化产生不重叠的版本，边身份包括完整两端和关系类型、指标、类别、方向。partial 数组用帧索引和原因表示质量。完整字段解释见[输出协议](../timegraph-output-optimization.md#response-formats)。

[compact 解码器](../examples/topology-compact.mjs)返回按帧独立对象，检查位图、版本和端点引用。新增 HTTP wrapper 复用它。snapshots 的 uint64 ID 是 JSON 数字，JavaScript 普通 JSON.parse 可能丢精度；本例利用 JSON.parse 的原始 token 恢复 ID 为十进制字符串，不经过舍入。compact ID 本身已是字符串。

**NodeID 只用于本次响应的边引用，不能作为跨请求实体身份。** 使用 `resource_type + 全部主键字段和值`；按字段名排序，忽略 version 等可选展示属性。调用者缓存同时区分空间和 Schema 版本。`entityKey` 遇到缺主键会失败，不能静默退化为部分身份。`alignFrame` 把边端点换成完整实体主键，再带上 relation_type/metric_name/category/direction 比较；相同端点不同关系必须保留。不要直接哈希完整 dimensions，因为可选属性变化会误造新实体。多请求比较需固定范围、Schema 和时间语义，partial 图的缺边不能直接断言资源已删除。

## 空、partial 与失败

本节列直接关系 endpoint 的真实层次；代理/网关可再包装 HTTP 错误。固定合成[响应夹具](../examples/topology-responses.json)及普通模式[错误夹具](../examples/topology-errors.json)来自同版本 handler，错误文字和数量可随配置变化；调用者主要按 HTTP 状态、逐项 code、partial 判断，不把完整 message 当稳定枚举。

| 情况 | HTTP / 单项状态 | 可使用的数据及处理 |
| --- | --- | --- |
| 无匹配根/无关系观测 | 200 / 200 | 保留空时间帧，partial=false；不要当执行失败 |
| info 有路由但属性条件无命中 | 200 / 200 | 完整空帧，partial=false；不能借别的根补答案 |
| 显式根属性查询缺 info 路由 | 200 / 200 | 空帧且 partial=true、source_info_no_route；不能当确认无实体 |
| 所需关系缺物理路由 | 200 / 400 | 此项无可用图；message 标识 required relation has no physical query route，其他成功项仍可用 |
| 后端 partial | 200 / 200 | 保留已有图并标记对应帧 backend_partial；不宣称完整 |
| 参数非法或单项后端执行失败 | 200 / 400 | 此项无可用图，保留其他成功项；失败 compact=null 或 snapshots=[] |
| JSON 错误、请求体/批次数量超限 | 400 / 无 data 项 | 整个请求失败，直接响应为 {error:...} |
| 准入/编码资源拒绝 | 400 / 无 data 项 | 整个请求失败；可能含 max_topology_reserved_bytes、max_topology_memory_headroom、max_response_bytes 等原因 |
| 模型内单项资源拒绝 | 200 / 400 | 按单项失败处理。相同资源原因可出现于不同阶段，因此不能只用原因字符串判断外层状态 |
| 执行槽满、队列尚有空间 | 暂无响应 | 普通模式 FIFO 等待，可取消，期限不重新计时；不是立即拒绝 |
| 等待队列满 | 400 / 无 data 项 | max_topology_admission_waiters，属于硬拒绝；不在此层返回 429 |
| 排队期限到达 | 400（仍可写时） | {"error":"context deadline exceeded"}；整个请求无可用结果，不返回部分批次 |
| 客户端取消/断连、写出失败 | 不保证有完整 HTTP 响应 | 服务端释放请求资源；丢弃不完整 JSON，不把收到的前缀当完整成功。已取消但传输仍可写时可见 {error:"context canceled"} |
| 启用 JWT 后鉴权失败 | 401 | handler 不执行；按部署要求处理凭据和权限 |

两个典型响应，完整请求与响应见夹具：

```json
{"trace_id":"","data":[{"code":400,"start_time":0,"end_time":0,"step":"","point_count":0,"snapshots":[],"message":"build shared topology: required relation has no physical query route: node_with_pod_relation"}]}
```

```json
{"error":"topology resource limit exceeded: max_topology_admission_waiters (2 > 1)"}
```

后一个示例来自本地将执行槽/队列设为 1 的验收，不是部署默认配置。

`path_resources` / `path_resources_range` 的必需关系缺物理路由时，同样返回 HTTP 200、对应单项 `code=400`，不会将未执行的查询解释为完整空结果。旧 `multi_resource` / `multi_resource_range` 的 v1beta3 适配按兼容合同保留已有结果，质量信息只写指标和 trace，不增加响应字段。

新 path 接口自动发现路径时，如果普通模式的搜索深度预算截断了候选，成功项会带 `partial=true`、`partial_reason=path_search_depth_limit`；已有结果和空结果都适用。多目标查询累计全部目标的截断状态和已有质量原因，后续健康项不继承前一项的状态。显式且已完整覆盖的路径、未触及预算的查询、YOLO 完整搜索不因此标记 partial；所有目标均无可查询路径时仍返回单项失败。

<a id="schema-and-routing"></a>
## Schema、指标与空间路由

[生成目录](timegraph-schema.md)包含默认 26 种资源主键/可选属性以及 34 类关系方向；[JSON 原始目录](timegraph-schema.json)可供示例主键解析。Redis provider 可以按空间覆盖定义，所以生产接入应固定所使用的实际 Schema；默认目录不是环境探测结果。

新增资源需定义完整主键、可选字段及类型；新增关系需定义两端类型、关系名、类别、方向，以及必要的 metric_name 显式覆盖。静态关系默认按两端名字排序生成 `<a>_with_<b>_relation`，定向关系默认 `<from>_to_<to>_flow`；info 指标为 `<resource>_info_relation`。当前 business/set 唯一关系名是 `business_with_set`，对应物理指标 `business_with_set_relation`。关系类型名称与指标名称是不同字段，不是两个别名。

还需生产者写入满足端点主键的时序数据，并配置 UQ 的字段→结果表、空间→结果表、结果表→物理存储/VM 路由及权限。仅增加 RelationDefinition 不会创建物理指标或路由。关系 metric_name 响应允许为空，表示默认命名，无须伪造显式覆盖。分别验证：Schema 能识别、目标空间能取到当前窗口的数据、目标负载能达成容量；任何一项不能代替其他项。

## 兼容与迁移

旧 multi_resource 的 source_info/source_expand_info、target_type、target_info_show、path_resource、target_list/path/paths 合同继续保留。`return_all_paths` 是显式扩展，不开启时保留旧的有效路径选择。新 path_resources 使用 matcher、target_types、path_resources 和 results.path；新 topology 返回每帧节点与诱导关系，不能用旧的目标列表伪装完整图。旧范围目标的分桶/路径选择合同仍用原回归验证，不套用 topology 的精确网格解释。

旧 instant/range 接口及其代理保持原响应结构，不返回 `partial`、`partial_reason`、`truncated`、`truncated_reason`。后端部分返回或候选缺物理路由时保留已有结果；容量超限等执行失败仍使用单项 `code=400` 和 `message`，`target_list` 保持数组。内部质量状态继续用于路径选择和排障。

`unify_query_cmdb_relation_quality_total{query_mode,quality,reason}` 记录每个兼容接口子查询的质量事件，`query_mode` 为 `instant/range`，`quality` 为 `partial/truncated`。同一子查询的同类原因只计一次；多个不同原因各计一次，不能将原因计数相加当作请求数。`reason` 使用固定类别（例如 `backend_partial`、`relation_no_physical_route`、`vm_storage_coverage_mismatch`、`max_targets`），未识别原因归入 `other`。指标不带业务 ID、资源 ID 或物理指标名；完整原因保留在子查询 trace 的 `relation-partial-reason` / `relation-truncated-reason` 中，布尔属性分别为 `relation-partial` / `relation-truncated`。正常项和普通执行失败不增加质量计数。

旧 range 兼容 VM 缓存网格：秒级 wire 步长不超过 60 秒且至少 50 个采样点时，起点向下对齐到步长边界，保留采样点数；短查询与不对齐的长步长查询保留原起点。比如 `start=1793259612,end=1793263212,step=60s` 使用 `1793259600..1793263200`。毫秒/微秒/纳秒时间戳仍先按旧 wire 规则转成整数秒，`1500ms` 仍保留 1500ms 聚合窗口和 1 秒采样步长。新 path/topology 接口继续使用各自的精确网格和质量协议。


服务端可先升级，旧消费者继续使用原路径与 DTO；新消费者显式选择新接口与 compact-v1。缺少新接口/格式时保留原功能或清晰报不支持，不能把失败 compact=null 当空成功。保留可选字段及未知字段的向前兼容处理；遇到未知 compact.version 必须明确拒绝，不能猜测位图布局。后续变更同时更新文档、例子和测试。

曾使用 `business_set` 的请求白名单或自定义定义应改为 `business_with_set`；本版本不接受旧别名，未知白名单可能返回空匹配而不报错。`biz_with_set` 是另一种资源对，不应一起删除或重命名。

## 维护与本地验收

在 `pkg/unify-query` 下执行：

```sh
make swag # pinned swag v1.16.4; first run may download the generator
# Generated catalogue; no backend access.
go run ./docs/examples/schema-catalog
# Requires Node.js 22+; runs local HTTP/model/consumer and schema contracts.
make test-timegraph-examples
```

本地 HTTP 验收注册实际路由，执行真实参数解析、空间路由、PromQL 构造、查询模型、编码和 Node HTTP wrapper。仅后端 gateway 返回合成、已聚合样本；这不是独立 VM 引擎执行或部署鉴权验收。覆盖两种时间模式、双格式逐帧对账、九类场景、多根属性筛选、失败批次、partial、缺路由、普通模式资源/排队错误及跨请求身份。公开夹具通过 `UPDATE_TIMEGRAPH_EXAMPLES=1 go test -tags=jsonsonic -run '^TestTopology(ConsumerExampleHTTP|DocumentedFailures)$' ./service/http/api` 更新；修改后复核差异，保留完全合成的数据。

真实环境数据覆盖、性能容量、正式消费页面发布各自验收；不要将本地夹具通过写成这些目标已完成。
