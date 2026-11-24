# KAC 告警源插件调试接入 Linkd 方案

> 状态：首版代码及本地真实 HTTP/ES/原调试组件联调已完成；未部署至测试或生产环境，完整主站验收待验证。验证限制见第 10 节。
> 已确认：2026-10-12 复用原有 verification 索引，保留扩展清洗/校验/过滤，插件调试不执行丰富，
> KAC 同步调用 Linkd 并直接从 HTTP 响应取得调试字段；其余待评审项保持草案状态。
> 分析日期：2026-10-12。
> 代码基线：Linkd 主仓 `feat/linkd-dev` / `a4fd9068`；Kingeye 主仓 `develop/5.3.0` / `da37f8551b`。
> 原分析依据上述代码基线；首版实现与测试记录见第 10 节，不代表生产运行验证。

## 1. 目标与可行性结论

告警源插件采用 `BaseClientV2` 后，插件输入契约为 Linkd standard RawEvent。
KAC 插件管理页面仍需保留连通性、数据校验、过滤、扩展清洗等调试步骤，并以原 KAC 字段
展示结果和配置规则，例如 `name`、`content`、`level`、`action`、`object`、`item`、`alarm_time`。

由 KAC 执行来源插件、管理调试样本和来源规则，调用 Linkd 复用标准化与 KAC 字段转换的
纯计算能力；Linkd 通过本次 HTTP 响应直接返回供人工检查的 KAC 调试字段。技术上可行，现有代码提供了部分计算能力，
改造前缺少从 RawEvent 到 KAC 字段的完整无副作用预览入口，以及 V2 插件调试样本与步骤衔接；
首版实现与当前验证状态见第 10 节，第 2 节保留初始分析的代码取证。

本版范围是字段计算与来源规则校验。结果属于候选字段视图，不代表正式 Alert 已创建、
KAC 正式文档已可搜索，或告警已经获得处置资格。本版保持插件调试范围，不执行 Enrich、
丰富处理器或内容生成；也不执行抑制、屏蔽、合并、通知、工单及自愈。
来源已经给出的字段可以转换展示，不能为补齐正式告警字段而调用 CMDB、策略、指标等丰富数据源。
恢复、关闭输入仍需支持人工检查，但不根据单条输入猜测既有告警的生命周期和处置状态。

## 2. 当前实现与断点

### 2.1 KAC 插件调试

以下为 Kingeye 上述基线中的当前代码事实，路径相对 Kingeye 仓库：

| 位置 | 当前行为 | 本次需要解决的问题 |
| --- | --- | --- |
| `src/kingeye/kac/alarm_collect/clients_v2/baseclient.py` | `verification()` 返回校验后的 RawEvent；`verify_alarms()` 再次调用它；不写正式 topic | 页面收到标准事件字段，尚未得到 KAC 字段；拉取步骤可能重新拉取而不是复用同一轮样本 |
| `src/kingeye/kac/alarm_collect/clients/api_views.py` | `_push_event_verification()` 调用插件校验后丢弃返回值；V2 跳过 V1 字段字典校验 | 推送样本没有接入旧调试索引，也没有传递给后续步骤 |
| `src/kingeye/kac/alarm_collect/views.py` | 后续推送步骤以 `{}` 初始化插件；结果查询读来源调试索引；推送指引从展示结果删除固定字段 | REST V2 在重新解析空输入时缺字段；RawEvent 不具备旧指引代码假定的字段 |
| `src/kingeye/kac/alarm_collect/clients/custom/baseclient.py` | V1 连通性校验保存原始告警至调试索引，后续读取该样本 | V2 缺少等价的样本衔接，不能只调整返回字段名 |
| `src/web/src/projects/kac/src/views/alarm-source/plugin-manage/components/verify-alarms.vue` | 从 `data.data.items` 动态生成列，并关联 KAC 字段目录 | 返回结构可保留，展示内容需改为对应步骤的 KAC 字段视图 |
| 同目录 `verify-filter.vue`、`verify-extra-clean.vue` | 把前一步 `tableData` 作为下一步 `alarm_list` 传回 | 展示结果改成 KAC 字段后，不能再直接交给要求 RawEvent 的 V2 方法 |

当前 V2 正式执行顺序为：生成输入 → 扩展清洗 → RawEvent 校验 → 过滤 → 发布。
扩展清洗、校验和过滤均是已有能力，用户已确认在调试方案中保留；扩展清洗及过滤无配置时跳过。
这里的扩展清洗是来源字段调整，不是再次把原始协议转换成 RawEvent，也不是丰富。
页面顺序为：连通性 → 数据校验 → 过滤 → 扩展清洗。
这两种顺序存在差异，本方案不能将页面步骤顺序直接变成新的正式执行顺序。

### 2.2 已有丰富方案预览

Kingeye `src/kingeye/kac/linkd/preview.py` 已调用 `POST /api/v1/enrich/preview`：

- 手工将 RawEvent 转为 Event 形状，展开 subject，并映射部分等级。
- 使用当前来源丰富规则，逐 evaluation 生成调试数据，写入丰富预览索引。
- `preview_alarm()` 在 Python 中手工展开 KAC 字段；它不是正式兼容投影的公共转换器。

这条链路服务于来源接入页的丰富调试窗口，与插件管理页的调试步骤和样本索引不同。
本版只复用认证与 HTTP 客户端，不调用这个丰富预览接口，也不复用其丰富配置转换、处理器或内容计算。
两个页面的调试语义和存储分别维护，插件调试不因已有丰富窗口而扩大职责。
不能通过 `publish_linkd_raw_events()` 间接触发插件预览，因为该函数随后会正式发布 Kafka 消息。

### 2.3 Linkd 可复用能力

| 位置 | 当前能力 | 使用限制 |
| --- | --- | --- |
| [Mapper](../../internal/cleaner/mapper.go)、[EventFactory](../../internal/cleaner/event_factory.go) | RawEventMessage → EventDraft → Event，统一等级映射、fingerprint、时间和身份 | 工厂要求 RecordID、ReceivedAt，构造器验证完整 EventSource；预览需提供隔离身份并收窄配置校验 |
| [丰富预览](../../internal/enrich/preview/service.go) | 当前独立提供 Event 丰富预览 | 属于另一用例，本版不调用其丰富引擎或创建内容预览 |
| [KAC 兼容转换](../../internal/lifecycle/kachook/compatibility.go) | `CompatibilityPayload()` 含已有字段映射和自定义字段展开规则 | 当前输入是有效 Alert，部分字段来自丰富结果；需提取共同纯字段规则，不能直接照搬完整正式投影 |
| [KAC 写入器](../../internal/kaccompat/writer.go) | 补齐整数类型、来源状态、策略关系、首次默认值与更新合并等存储语义 | 当前包含真实读写和状态依赖，不能整体调用来做调试 |

现有正式能力与契约继续以[全局 KAC 插件](kac-compatibility-plugin.md)和
[兼容存储契约](../reference/contracts/kac-alert-projection-v1.md)为准；本文不改变其已实现行为。

## 3. 建议处理流程与职责

```text
第三方推送 / 插件拉取
  → KAC 保存原始输入及插件产生的 RawEvent 调试样本
  → KAC 按本次候选规则重新执行来源扩展清洗、校验与过滤
  → KAC 同步 HTTP 请求 Linkd 调试预览
      → StandardCleaner / EventFactory
      → 将来源已有数据转换为 KAC 调试字段（不执行丰富）
  ← Linkd 在本次 HTTP 响应中返回字段、样本引用和诊断
  → KAC 将调试结果与原生样本分开保存至 verification 索引
  → KAC 沿用页面响应结构返回结果
  → 用户核对原 KAC 字段，调整规则并重新计算
```

| 关注点 | KAC | Linkd |
| --- | --- | --- |
| 来源访问与插件 | 执行插件、接收外部输入、原有鉴权/许可/启停及调试门禁 | 不执行用户 Python 插件 |
| 来源规则 | 保留过滤和扩展清洗归属，复用正式处理入口 | 不新增另一套来源过滤和 Python 清洗规则 |
| 样本与步骤 | 保存样本、规则和步骤结果，处理推送/拉取、回显和失效 | 每次调用只计算，不持有调试会话 |
| 标准化 | 提交 RawEvent 和受控的计算配置 | 复用 Cleaner/工厂，校验租户和来源 |
| KAC 调试字段转换 | 提交来源元数据及当前租户字段目录 | 只转换输入已有的数据，复用适用的字段映射和类型规则 |
| 结果传递与展示 | 接收 HTTP 结果，保存至 verification 索引，返回旧页面 envelope | 在本次 HTTP 响应中返回逐样本/逐等级字段及诊断 |

## 4. 样本与页面步骤

### 4.1 分开保存输入和展示结果

**已确认决策（2026-10-12）：复用原有来源级 verification 索引保存插件原生样本，
沿用 `{AlarmEvent.VERIFICATION_INDEX}_{alarm_source_id}` 的索引入口，不另建原生样本索引。**
首版 V2 `verification()` 已接入该索引的样本保存及同步预览；真实索引写入尚待环境验证。

每轮调试在 KAC 保存以下关联：当前租户、来源、调试会话、sample_id、原始输入、插件 RawEvent、
接收时间、输入摘要、候选规则及步骤结果。原始输入及 RawEvent 与各步骤的 KAC 候选字段、过滤结果、
差异和诊断分别保存；调试结果不能覆盖原生样本。`validate_step` 继续承接步骤状态及样本引用，
不以页面返回结果替代原生输入，也不为本功能预先创建通用会话框架。

来源级索引名不能替代租户和会话隔离：样本身份、读取、更新、失效及清理都需限定
`bk_tenant_id`、来源、调试会话和 sample_id，不能误读、覆盖或清理其他归属的样本。
物理字段、原生样本与结果的记录组织及必要 mapping 调整仍需对照现有索引定义设计，
本次确认复用入口，不代表任意嵌套 RawEvent 已能直接写入旧 mapping。
来源接入页已有的丰富预览索引仍按原职责维护，本次不将两类调试索引合并。

前端仍收到 `data.items`，并增加样本引用；后续请求传引用和规则。KAC 按租户、来源和会话读取
原始样本，不能信任客户端回传的候选告警作为事实。多等级行还需携带 evaluation 引用，
但修改共同 RawEvent 后必须重新计算相关结果，不能把各行拆成正式消息重复发布。

推送首步应保存插件生成的 RawEvent 与 `verification()` 的校验结果，后续不再以空输入重新调用插件。
当前 `verification()` 返回值已经应用扩展清洗，不能直接将它当成未清洗样本再执行一次规则；
需在规则执行前捕获插件输出，并与计算结果分别保存，来源正式执行与调试共同复用这一步骤拆分。
拉取首步保存本轮样本，后续复用；重新拉取是显式操作，会使依赖旧样本的步骤失效。
V1 插件继续保留其输入契约，可经当前来源转换得到 RawEvent 后使用同一预览出口。

### 4.2 保留操作步骤，明确中间结果

| 页面步骤 | 建议行为 |
| --- | --- |
| 连通性 | 获取并保存样本，验证插件及输入；不发布正式消息 |
| 数据校验 | 展示该样本的基础 KAC 字段视图；基础视图和应用规则后的视图分开记录 |
| 过滤 | 用当前候选扩展清洗配置重新处理原样本，再应用候选过滤；展示过滤结果与计数 |
| 扩展清洗 | 修改规则后从原样本重新执行“扩展清洗 → 校验 → 过滤 → Linkd 预览”，展示最终结果 |

最后一步修改 `name/level/action` 等过滤引用字段后，之前的过滤结果可能改变。
建议页面明确显示重新计算后的计数，并使不再有效的步骤结果失效；不能把旧过滤结果固定为事实。
这一交互调整需要评审确认，不能宣称“页面完全零改动”。

推送指引必须来自原始外部输入或插件输入示例，不从 KAC 投影结果反推输入。
规则保存仍沿用当前产品操作；只有最终计算成功且步骤仍有效，才能标为调试通过。
保存候选配置、调用预览失败与调试通过状态之间需有明确事务/失败处理，避免留下旧的通过结果。

### 4.3 旧字段规则与正式 V2 处理一致

仅展示 `name/level/action` 不足以保留原操作。当前 V2 过滤直接读取 RawEvent，扩展清洗只对部分
旧目标字段做映射；旧字段选项和这些读写入口之间需要逐项对账。

建议在 KAC 的来源规则边界形成一份共同字段读写规则，调试和正式执行都使用它：

- `name/content` 对应 `title/content`；`level/action` 对应 evaluation，并使用同一等级和动作映射。
- `alarm_time` 使用同一时间解析规则；来源事件/告警编号与系统投影编号分别处理，禁止通用改名。
- `object/item/meta_info`、资源字段及已登记自定义字段，明确来自 subject、dimensions、labels 还是 extra_data。
- 来源过滤读取来源已有数据，不能允许使用只有丰富后才产生的字段却宣称正式可过滤。
- 字段缺失、null、空字符串、0、false 及集合类型保留既有可观察语义。

多等级 `action/level` 扩展清洗当前明确受限于单等级输入。本版建议保持这一限制并给出可定位错误；
多等级过滤是作用于整条消息还是某个 evaluation，需要先核对旧规则和实际需求再裁定。
这部分涉及正式处理行为，必须在实现前确认；不得只修页面来掩盖语义差异。

## 5. Linkd 预览接口草案

### 5.1 入口和输入

首版控制面入口为 `POST /api/v1/ingest/preview`，承担 RawEvent 标准化与 KAC 调试字段转换。
KAC 后端同步调用该接口，直接读取 HTTP 响应，不使用 Kafka、回调或异步结果通知。
具体首版字段和限制见[接口契约](../reference/contracts/kac-ingest-preview.md)；
现有 `/api/v1/enrich/preview` 继续服务丰富调试，与本版流程独立。

首版 draft 请求示意如下：

```json
{
  "bk_tenant_id": "system",
  "event_source_id": "kac_example",
  "session_id": "debug-session-001",
  "source": {
    "kind": "draft",
    "calculation": {
      "cleaner": {"type": "standard"},
      "fingerprint_mode": "field",
      "fingerprint_field": "source_alert_id",
      "severity_mapping": {"fatal": "critical", "remind": "info", "remain": "info"}
    }
  },
  "kac_source": {"source_id": "kac-plugin-001", "source_name": "用户告警源"},
  "field_catalog": ["owner"],
  "samples": [
    {
      "sample_id": "sample-001",
      "received_at": "2026-10-12T02:00:00Z",
      "payload": {
        "bk_tenant_id": "system",
        "event_id": "external-event-001",
        "alert_id": "external-alert-001",
        "title": "主机告警",
        "content": "CPU 使用率过高",
        "evaluations": [{"action": "triggered", "severity": "fatal"}]
      }
    }
  ],
  "output": "kac_alarm"
}
```

支持两种互斥配置输入：

- `published`：读取一个明确的来源 Release，返回实际来源版本；租户必须匹配，允许停用来源调试。
- `draft`：接收 KAC 根据当前配置生成的白名单计算字段；不发布、不建立来源任务，不要求已有 Release。

KAC 当前同步对新建且未上线、远端不存在的来源跳过发布。
draft 允许这些来源直接进行标准化和字段预览，不要求先上线；调试不能调用正式同步函数来绕过这一限制。
draft 只接受标准化所需的白名单计算字段，不接受 enrich、processors、content_mode、storage、brokers、
topic、凭据、hooks、调度或输出地址。published 模式也只提取标准化字段，不执行 Release 中的丰富配置。
`kac_source` 由 KAC 根据已鉴权来源记录生成，`field_catalog` 来自当前租户字段目录；
Linkd 校验来源关联和受保护字段，不通过来源丰富处理器读取名称，也不连接外部丰富数据源。
这些元数据不能由页面任意指定；draft 请求中的来源归属由受信 KAC 后端在当前租户上下文中核对。
若支持读取已发布配置后的局部候选覆盖，应另行定义允许覆盖字段，不能混用两种模式。

### 5.2 计算与结果

一次调用固定来源计算配置、等级配置及字段目录；返回 `config_origin`、`config_digest` 和实际使用的
等级配置摘要。仅 published 返回真实 `event_source_version`，draft 不伪装为正式发布版本。

结果通过本次 HTTP 响应按 sample 和 evaluation 返回 sample_id、KAC 调试字段、severity/action、
passed 和 diagnostics。首版不返回字段差异或完整阶段轨迹，也不引入 EnrichStatus。
KAC 核对逐样本等级数量后按旧页面的 count/items 封装。HTTP 成功不表示正式告警已创建；
硬校验或转换错误拒绝整批，不能标为调试通过。

响应示意如下；字段封装、诊断代码和缺值表示仍为接口草案：

```json
{
  "config_origin": "draft",
  "config_digest": "<本次计算配置摘要>",
  "items": [
    {
      "sample_id": "sample-001",
      "severity": "critical",
      "action": "triggered",
      "passed": true,
      "kac_alarm": {
        "bk_tenant_id": "system",
        "name": "主机告警",
        "content": "CPU 使用率过高",
        "level": "fatal",
        "action": "firing",
        "source_id": "kac-plugin-001",
        "source_name": "用户告警源"
      },
      "diagnostics": []
    }
  ]
}
```

### 5.3 同步返回与 KAC 保存

1. 页面调用 KAC 原有调试接口，KAC 按样本引用读取原生数据并运行本次来源规则。
2. KAC 调用 Linkd，等待本次 HTTP 响应；Linkd 不保存样本或结果，也不向 KAC 发起回调。
3. KAC 核对响应的样本归属、逐等级结果及诊断，将结果与原生样本分开写入原 verification 索引。
4. KAC 将 `kac_alarm` 字段转换为页面的 `data.items`，保留样本/evaluation 引用，沿用
   `is_validated/data.items/count` 等返回结构；Linkd 直接响应的 items 与页面 items 分别封装。
5. 保存完成后返回页面并更新相应步骤。Linkd 超时、响应错配或 KAC 保存失败都返回调试失败，
   不沿用旧的通过状态，也不把失败伪装为零条结果。

来源过滤全空是合法的来源规则结果：KAC 返回真实 count=0，不必请求 Linkd；
连通性步骤未取得原生样本、后端转换失败和过滤全空应分别提示，不能相互替代。
原页面有读取结果索引的接口时，读取已保存的当前会话结果，不另设异步等待 Linkd 的流程。

预览 RecordID 必须使用隔离命名空间和固定样本身份/摘要，ReceivedAt 使用首次保存的值。
相同输入重试不能通过当前时间改变缺省发生时间；该身份不承诺等于真实 Kafka 记录生成的 Event ID。
需要的临时 KAC 编号只用于关联展示，不作为来源规则可修改字段，也不能作为正式告警查询或操作目标。

### 5.4 复用边界与 Linkd 改造

- 从 Mapper/工厂中复用计算能力，将纯计算字段验证与完整消费配置验证适当分开；不伪造 Kafka 配置。
- 提取可适用于来源已有数据的 KAC 字段映射、类型和缺值规则，与正式出口的对应规则共同维护；
  输入是标准化 Event、明确 evaluation 与 KAC 来源元数据，不把 Event 伪装成完整生命周期 Alert。
- 当前 `CompatibilityPayload()` 接受 Alert 且部分字段依赖丰富结果，不能整体照搬；
  需补充没有丰富结果时的调试转换入口，不能通过制造空丰富快照来宣称完整正式投影等价。
- 不调用 Enrich、BuildContent、外部丰富数据源、`kaccompat.Client.Send`、Lifecycle Processor
  或投影/动作任务执行器；来源 content 原样承接本次来源规则结果。
- 新 HTTP 服务返回字段与错误，只有 KAC 保存调试数据；不在 KAC 再实现完整 Linkd 转换。
- 保留现有丰富窗口与正式处理行为，它们的合并和迁移均不属于本版范围。

不预先固定新 Go 包结构。实现时按真实依赖选取消费方窄接口，避免 control-plane、cleaner、
lifecycle/kachook 与 kaccompat 之间形成循环依赖；单次 HTTP 编排不作为提取通用框架的理由。

## 6. KAC 调试字段的保证范围

这里的输出是供插件页面检查的 KAC 字段形状，不是包含丰富及运行状态的完整正式告警文档。

| 字段或场景 | 本版建议保证 |
| --- | --- |
| name/content/object/item/meta_info、已提供的资源与自定义字段 | 只转换来源已有数据，共用适用的字段规则；缺值回退与显式空值保持一致，不查询外部系统补齐 |
| level/action、时间、数值及集合 | 使用正式映射和类型处理；时间遵循 KAC Asia/Shanghai 文本格式，数值 0 和 false 不作为缺失 |
| source_id/source_name | 使用 KAC 提交且受控的来源元数据，与产品来源身份对应；缺失明确诊断，不调用来源丰富处理器 |
| triggered | 转换为 KAC firing 字段；不生成 opening Alert、不重算内容，不证明正式策略会允许创建 Alert |
| resolved/closed、`__ALL__` 关闭 | 保留输入动作并做标准化校验；未知历史开始时间、既有丰富快照和状态单列为不可确定，禁止强行当作 triggered |
| status、关联、屏蔽/合并、处置字段 | 不计算真实运行状态；确需默认值时标明为候选初始化值，不表达真实处置或关系 |
| 只有丰富后才能得到的字段 | 本版不补齐；未提供、不适用与转换错误分别表达，不能把正常缺省作为丰富失败 |
| alarm_id/event_id/storage_time | 区分临时展示身份、来源身份、正式投影身份及写入时间，排除与正式记录的逐值相等承诺 |

正式 Alert 的展示和丰富快照是另一条正式处理链的结果，不属于本版插件调试计算。
若需查看输入对应的真实 KAC 文档，应设计独立只读查询，
不在本版预览里自动查找生产告警并修改状态。

## 7. 隔离、预算与失败处理

- KAC 继续执行来源管理权限、租户上下文、许可和现有推送调试门禁；Linkd 使用现有 Internal Token 客户端。
- Linkd 同时核对请求、payload、来源配置中的租户与来源；调试索引、样本读取及缓存含稳定租户作用域。
- Linkd 预览服务不注入业务写端口。允许 KAC 写调试索引、步骤和用户提交的规则，不写正式 Event/Alert、
  Mailbox、投影任务、动作任务或业务 Kafka topic，也不创建正式资源和发布配置。
- Linkd 预览不装配外部丰富数据源；只允许必要的已发布配置/等级读取及技术观测，不运行资源初始化。
- 建议首版每请求最多 10 个 RawEvent、总 JSON 1 MiB，每条最多 32 个 evaluations、合计最多 64 个结果行。
  并发 4、请求总超时 10 秒作为候选基线，计算池与正式处理池隔离，外部读取传播取消。
- 建议样本有效期最多 1 小时，同时限制每来源活跃会话数及调试索引存储；具体总量在实现前裁定。
- 400 表示输入/模式错误，422 表示计算配置或字段无法转换，429 表示预览预算耗尽，502/504 表示依赖失败/超时。
  返回样本及阶段定位，不记录凭据或完整外部响应；页面不能把超时展示为零条成功。
- 上游依赖失败不回退旧执行链；重试保持样本身份，配置改变后重新计算并使旧通过结果失效。

## 8. 决策状态

### 8.1 已确认决策

| 编号 | 决策 | 确认日期 | 实施约束 |
| --- | --- | --- | --- |
| C1 | 复用原有来源级 verification 索引保存原生样本和调试结果 | 2026-10-12 | 两类数据分开保存；按租户、来源、会话及样本隔离，后续步骤从原生样本重算；存储细节见第 4.1 节 |
| C2 | 保留来源扩展清洗、RawEvent 校验和过滤 | 2026-10-12 | 属于已有来源能力；无扩展/过滤配置时跳过，与正式来源规则共用语义；页面顺序的衔接仍待裁定 |
| C3 | 插件调试不执行丰富和内容生成 | 2026-10-12 | Linkd 仅标准化并转换来源已有数据，来源元数据由 KAC 提供；不调用 Enrich/BuildContent 或外部丰富数据源 |
| C4 | KAC 同步调用 Linkd，调试字段直接由 HTTP 响应返回 | 2026-10-12 | KAC 核对响应后保存 verification 结果并返回页面；不使用 Kafka、回调或异步投递，失败不能标为通过 |

用户随后授权按此方案开发首版；下列细节按第 10 节记录的首版取舍实现，未完成项仍明确标注。

### 8.2 待评审决策

| 编号 | 问题 | 本版建议 | 尚需确认的影响 |
| --- | --- | --- | --- |
| D1 | 调试字段与完整正式文档的区别如何展示 | 展示来源可计算字段与缺省诊断 | 页面不承诺正式创建，不将未丰富字段视为错误 |
| D2 | 新建未发布来源如何调试 | 支持 draft 标准化配置及 KAC 来源元数据 | 新入口需收窄配置验证，不装配丰富资源 |
| D3 | 页面步骤与正式顺序不一致 | 保留步骤导航，每次从原样本按正式顺序重算 | 最后一步可能使过滤计数变化，需重新确认 |
| D4 | 旧字段规则如何作用于 V2 | 来源侧共用字段读写语义，调试/正式同时调整 | 字段列表、ID 语义和多等级过滤须先对账 |
| D5 | 多等级 action/level 扩展清洗 | 首版保持当前单等级限制 | 不默默挑某一级，不默认修改所有级别 |
| D6 | 字段缺省与硬转换错误如何影响步骤通过 | 来源未提供与字段不适用正常展示，硬校验/转换错误阻断 | 固定字段的必填规则与诊断代码仍需逐项核对 |
| D7 | 调试接口的字段封装与身份如何确定 | 暂用 ingest/preview、sample_id 及逐 evaluation 结果 | 响应 envelope、编号及缺值表示在实现前定稿 |
| D8 | 终态缺少历史字段如何展示 | 明确不可确定字段，保留动作和可计算字段 | 不冒充完整正式文档；真实读取另行设计 |

本表用于人工检查和裁定，不表示已获实现批准。

## 9. 实施拆分与验收

建议依次完成：确认上述决策与字段矩阵 → Linkd 纯计算复用及入口 → KAC 样本/步骤衔接和共同规则 →
前端样本引用与失败/计数展示 → 跨仓真实验收。无需为了此功能改变来源正式投递协议或 KAC 索引所有权。

| 验收项 | 必须验证的可观察结果 |
| --- | --- |
| 拉取与推送四步骤 | 样本一致、推送后能继续、无空输入重解析、前后退回显有效、重新取样使旧步骤失效 |
| verification 索引复用 | 原生输入与未应用扩展清洗的 RawEvent 写入原来源索引；结果不能覆盖原生样本；读取、更新和清理按租户/来源/会话/样本限定，重算不重复清洗 |
| V1/V2 与旧字段 | 保持各自输入契约；name/level/action/时间及自定义字段配置能执行，调试和正式语义一致 |
| 规则边界 | 无规则、过滤全空、缺字段、非法表达式、扩展清洗错误、单/多等级限制及字段覆盖保护 |
| 字段等价 | 固定相同来源输入/配置，对比调试转换和正式出口共同规则中的字段、类型、存在性和时间；丰富派生和运行态字段不作为逐值等价目标 |
| 无丰富 | Enrich、BuildContent、CMDB/策略/指标等丰富读取调用均为零；来源 content 与已提供字段正确承接 |
| 同步返回与保存 | 页面→KAC→Linkd→KAC→页面在本次请求内返回；sample_id 对应正确，结果保存至 verification；超时、响应错配或保存失败不得标为通过 |
| 终态 | resolved/closed/`__ALL__` 不被改成触发；缺少历史字段明确表达，不能伪造完整生命周期 |
| 配置状态 | draft、新建未上线、停用但已发布、发布版本变化、目录变化；不为调试正式发布或启动任务 |
| 租户与权限 | 同名样本跨租户不可读、来源归属错配拒绝、目录不能覆盖租户/身份，KAC 两种租户开关路径 |
| 无业务副作用 | 正式 Kafka、Event/Alert 存储、Mailbox、投影/动作任务、Celery 处置调用均为零；仅调试数据允许写入 |
| 故障与预算 | 配置读取失败、硬转换错误、超时、429、取消、超大请求、样本过期和重复点击，不错误标为通过；正常字段缺省和过滤全空单独表达 |
| 质量与联调 | Linkd 修改后 `make check`；KAC 按 `task dev-test` 运行最小相关测试；真实页面→KAC→Linkd→页面逐步骤验收 |

普通单测使用隔离样本并 patch 非目标 I/O；真实依赖测试明确标识为集成测试，使用独立资源。
校验正式投影的测试应对比共享计算规则与独立预期，不通过往生产索引或正式 topic 写调试数据获取证据。

## 10. 本版文档交付边界

首版实现位置：

- Linkd `internal/ingestpreview`、控制面 API 与装配：draft/published 同步标准化及 KAC 字段返回。
- `cleaner.NewNormalizationFactory`：复用正式 Build，独立校验计算字段，不要求 Kafka 配置。
- `lifecycle/kachook.DebugFields`：来源字段转换，共用文本、Integer 与目录展开规则；不构造正式 Alert。
- Kingeye `alarm_collect/linkd/verification.py`：verification 索引保存、会话、同步请求与结果校验。
- V2 client、调试视图和插件页面：推送原始输入重用、样本引用、失败失效及 0/false 展示。

首版取舍：每租户来源保留一个当前会话、有效期一小时，原生与结果各一个固定 ES 文档槽位，
JSON 序列化到独立的不索引文本字段，结果不能覆盖原生样本。页面仍保留四步骤，扩展清洗后
按“清洗→校验→过滤”重新计算；后继步骤的新插件实例复用首次捕获的原生输入。四个步骤均通过后
才能标为已验证。推送首步在 event/common 的取样和结果保存成功后记录通过；最终保存步骤状态
避开 AlarmSource.save() 在 validated 状态删除 verification 的行为。多等级旧 action/level
清洗及过滤明确拒绝，共同来源字段可过滤。
V1 继续沿用既有调试入口，本版没有强制把 V1 改接新接口。来源未提供的字段不通过丰富补齐；
目前返回字段及终态诊断，字段差异和完整阶段轨迹尚未实现。

当前已验证：相关 Linkd 包普通/race 测试；KAC 相关 35 项隔离测试；页面数据传递与
0/false 展示的 3 项 Node 行为测试。KAC ES 和 HTTP 在单测中隔离，不能作为真实跨仓联调证据。
五个受影响 Vue 文件 ESLint 已通过；Web 联调复用本机已安装依赖，并通过开发编译。
完整生产构建和全主站验收未执行，依赖版本差异及非调试模块警告见联调记录。
Linkd `make check` 的 Go 格式、普通测试、vet、race、lint，以及 Console lint/typecheck/test/build 已通过；
Console 在本机 Node 26 下需设置 `NODE_OPTIONS=--no-experimental-webstorage`。完整门禁在 Helm 检查处
因缺少 helm CLI 停止，另行运行 release-check 的 Chart 测试也受同一缺失阻断。
真实本地 KAC→Linkd→KAC HTTP 四步骤、verification mapping/原生样本保留、旧引用拒绝、
Linkd 停机后的失败失效及原会话恢复已验证；详见[本地联调记录](../research/2026-10-12-kac-debug-preview-local-validation.md)。
本地组件联调使用登录、来源授权和许可夹具；真实 SSO/IAM/许可、测试或生产部署，
以及正式处理字段全面对照仍未验证。
早先讨论中包含 Enrich 和内容生成的流程图已不符合本次确认范围，以本文第 3 节修订流程为准。
