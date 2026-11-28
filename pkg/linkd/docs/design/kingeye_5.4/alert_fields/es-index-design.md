# 5.4 告警 ES 索引设计

**实施状态：未实施。** 本文补充[5.4 告警字段设计](alert_field_design.md)，说明字段值写入哪类 ES 文档、由谁更新、页面查哪个索引。字段目录、内置字段清单和分阶段取值规则沿用原设计。`RawAlarmEvent` 为暂定名。

## 为什么新增 RawAlarmEvent

KAC 需要按**每次 Event 丰富后的字段值**检索原始告警，并在生成汇总告警前就展示该 Event 的处理状态。例如，插件上报 `region=华南`，丰富将它改为“华东”，用户此时按“华东”应当能查到这条 `unprocessed` 的记录。Linkd 已能根据冻结的丰富结果还原“华东”，但 Event 索引中的 `extra_data` 和 `enrich` 都是 `enabled: false`，`title` 和 `content` 也只存储。直接查 Event 要额外索引有效值，并把 KAC 字段和查询方式引入 Linkd。[Event 的 ES mapping](../../../../internal/store/elasticsearch/template.go) · [按等级读取丰富结果](../../../../internal/enrich/view/view.go)

现有 AlarmEvent 按汇总告警更新。同一告警连续触发两次，原始告警页需要保留两行和各自当时的值，汇总告警页只有一行；被抑制的 Event 还可能没有新生成的 Alert。因此，选择在 KAC 增加 RawAlarmEvent，每次 Event 形成一条可检索的文档。Linkd 继续负责丰富和策略裁决，KAC 负责面向用户的检索和关联归档。

| 取舍 | 收益 | 代价 |
| --- | --- | --- |
| 检索丰富后的值 | 原始告警和汇总告警共用 KAC 字段名、类型和查询方式。页面直接检索已投影的值，Linkd 策略仍读取本次 Event 的有效视图 | 每条 Event 多存一份页面字段，标题、正文和扩展字段还需建立索引。容量随 Event 数量及保留期增长；公共字段变更也要同步两个 KAC 索引的 mapping |
| 丰富后即可见 | 用户能看到 `unprocessed`，裁决完成后在同一行看到处理结果；即使没有生成 Alert，也能查到该 Event | 每条完成裁决的 Event 需要首次写入和一次状态更新。投影延迟期间页面可能暂时缺行或仍显示旧状态；补投时需处理重复和乱序，避免已接受的记录退回 `unprocessed` |
| 保留每次来源记录并随告警归档 | 后续触发更新汇总告警时，仍可检索前一次原始告警当时的值，也可按 `related_alert_ids` 找齐分布在不同时间桶的记录 | 归档要处理跨桶查询、部分成功后的续作，以及一条 Event 同时关联在线和已归档告警的情况。若上线时需要检索存量 Event，还要安排历史回填 |

## 四类逻辑索引

| 索引 | 一条记录表示什么 | 5.4 调整 |
| --- | --- | --- |
| Linkd Event | 一次来源事件，保存来源字段、各等级的丰富结果和整条 Event 的生命周期处理结果 | 沿用现有 ES mapping。Linkd 从丰富后的 Event 生成 KAC 原始告警，策略继续按当前 evaluation 的有效值匹配 |
| Linkd Alert | 一条告警的生命周期，保存首次触发时选定等级的丰富快照 | 沿用现有 ES mapping。向 KAC 投影随 Event 更新的字段时，引入本次引起更新的 Event 的有效值 |
| KAC RawAlarmEvent | 一条 Event 对应一条文档，保存本次丰富后的原始告警。多等级 Event 在页面上也只展示一行 | 新增。用于原始告警列表、详情、检索及关联归档 |
| KAC AlarmEvent | 用户查看和处置的汇总告警 | 沿用现有索引，补齐公共字段的 mapping、创建和更新投影，继续由 KAC 维护处置字段 |

四类指逻辑用途，时间桶仍可对应多个物理索引。“关联原始告警”接口改查 RawAlarmEvent，名称和正文模糊搜索也在该索引执行。Linkd 的 Lifecycle、候选策略筛选及最终匹配都无需回查 KAC。当前 Linkd 已在读取策略前冻结 Event 的丰富结果，当前 KAC 写入则主要接收 Alert。[生命周期处理顺序](../../../../internal/lifecycle/plan.go) · [策略 EventView](../../../../internal/policy/view.go) · [现有 KAC 投影](../../../../internal/kaccompat/writer.go)

## RawAlarmEvent 的身份、字段和查询

在租户内以 `event_id` 标识原始告警。ES 文档使用 `bk_tenant_id` 和 `event_id` 生成稳定的 `_id`，重投、裁决更新和归档均能定位同一条记录。汇总告警的 `alarm_id` 对应 Linkd AlertID，`AlarmEvent.event_id` 对应首次触发 Event 的 ID。[现有投影身份映射](../../../../internal/lifecycle/kachook/compatibility.go)

下表列出决定 ES 读写行为的字段。其他公共字段按字段目录逐项复用 KAC 定义。

| KAC 字段 | RawAlarmEvent 取值 | 建议 mapping 与查询 |
| --- | --- | --- |
| `bk_tenant_id`、`event_id`、`fingerprint` | 本次 Event 的租户、身份和指纹 | `keyword`，支持精确查询 |
| `related_alert_ids` | Lifecycle 实际关联的全部 AlertID，裁决前可为空 | 多值 `keyword`，供关联列表及归档查询 |
| `processing_status` | 整条 Event 的 `processing.state`，取值为 `unprocessed`、`accepted`、`suppressed`、`orphaned`、`rejected` | `keyword`。详情中的“处理状态”和列表筛选使用同一值 |
| `name`、`content` | 本次 Event 丰富后的标题和正文 | 复用 AlarmEvent 的 `text`、analyzer 和 `.keyword` 定义。模糊搜索使用文本字段，详情返回完整值 |
| `level`、`action` | 单等级来源取本次判定。原始告警接口的动作取值为 `triggered`、`resolved`、`closed`；alarmd 多等级的单行取值见下文 | `keyword`。等级名、动作名由接口展示 |
| `latest_alarm_time`、`received_time` | 分别取本次 Event 的 `occurred_at`、`received_at` | `date`。列表按 `latest_alarm_time` 倒序，同一时间按 `event_id` 稳定排序 |
| `source_id`、`source_name`、`object`、`model_id`、`model_inst_id`、`bk_biz_id` 等 | 按登记规则取本次来源和对象信息 | 复用 KAC 对应字段定义，例如 `source_id` 用 `keyword`，`bk_biz_id` 用 `integer` |
| `dimension`、`dimension_info` | 前者保留结构化维度及原始标量类型，后者为展示文本 | 建议 `dimension` 用 `flattened` 做键值精确查询，`_source` 保留原对象；按数值范围查询需另定类型。`dimension_info` 沿用文本定义 |
| `item_id`、`item`、`strategy_id`、`strategy_name`、`metric_id` | 分别按已有字段设计取值。`metric_id` 保留完整指标名或组合表达式 | 两个 KAC 索引共用查询语义。组合表达式要能按任一参与指标全名做包含检索 |
| 已登记扩展字段，如 `region` | 从本次有效 Event 按发布的映射取值 | 页面展示和检索使用同一个 KAC key 及同一个值。物理路径见下文 |

公共字段的名称、分析器和查询规则由两个 KAC 模板复用。共享字段定义允许各自按对象取值，例如原始告警保存本次 Event 的动作，汇总告警保存当前告警的动作。`conductor`、`notify_status` 属于汇总告警，由 KAC 处置和通知流程维护。[AlarmEvent 现有 mapping](../../../../internal/kaccompat/alarm_event_mapping.json)

## RawAlarmEvent 的 ES mapping 草案

下例采用现有 KAC 写入器使用的 legacy index template 格式。示例查询别名为 `raw_alarm_event`，时间桶匹配 `raw_alarm_event-*`。示例列出原始告警列表、检索和关联归档涉及的核心字段；分片、副本及桶宽沿用部署配置。名称与正文沿用 KAC 现有分析器。[KAC 模板与分析器定义](../../../../internal/kaccompat/index.go)

```json
{
  "index_patterns": ["raw_alarm_event-*"],
  "aliases": {"raw_alarm_event": {}},
  "settings": {
    "analysis": {
      "char_filter": {
        "split_by_whitespace_analyzer": {"type": "pattern_replace", "pattern": "(.+?)", "replacement": "$1 "}
      },
      "analyzer": {
        "split_by_whitespace_analyzer": {
          "type": "custom", "char_filter": ["split_by_whitespace_analyzer"],
          "tokenizer": "whitespace", "filter": ["lowercase"]
        }
      }
    }
  },
  "mappings": {
    "properties": {
      "bk_tenant_id": {"type": "keyword"},
      "event_id": {"type": "keyword"},
      "fingerprint": {"type": "keyword"},
      "related_alert_ids": {"type": "keyword"},
      "processing_status": {"type": "keyword"},
      "name": {"type": "text", "analyzer": "split_by_whitespace_analyzer", "norms": false, "fields": {"keyword": {"type": "keyword", "ignore_above": 8191}}},
      "content": {"type": "text", "analyzer": "split_by_whitespace_analyzer", "norms": false, "fields": {"keyword": {"type": "keyword", "ignore_above": 8191}}},
      "level": {"type": "keyword"},
      "action": {"type": "keyword"},
      "latest_alarm_time": {"type": "date", "format": "strict_date_optional_time||epoch_millis"},
      "received_time": {"type": "date", "format": "strict_date_optional_time||epoch_millis"},
      "source_id": {"type": "keyword"},
      "source_name": {"type": "keyword"},
      "object": {"type": "text", "analyzer": "split_by_whitespace_analyzer", "norms": false, "fields": {"keyword": {"type": "keyword", "ignore_above": 8191}}},
      "model_id": {"type": "text", "analyzer": "split_by_whitespace_analyzer", "norms": false, "fields": {"keyword": {"type": "keyword", "ignore_above": 8191}}},
      "model_inst_id": {"type": "text", "analyzer": "split_by_whitespace_analyzer", "norms": false, "fields": {"keyword": {"type": "keyword", "ignore_above": 8191}}},
      "model_name": {"type": "text", "analyzer": "split_by_whitespace_analyzer", "norms": false, "fields": {"keyword": {"type": "keyword", "ignore_above": 8191}}},
      "bk_biz_id": {"type": "integer"},
      "dimension": {"type": "flattened"},
      "dimension_info": {"type": "text", "analyzer": "split_by_whitespace_analyzer", "norms": false, "fields": {"keyword": {"type": "keyword", "ignore_above": 8191}}},
      "item_id": {"type": "keyword"},
      "item": {"type": "text", "analyzer": "split_by_whitespace_analyzer", "norms": false, "fields": {"keyword": {"type": "keyword", "ignore_above": 8191}}},
      "strategy_id": {"type": "text", "analyzer": "split_by_whitespace_analyzer", "norms": false, "fields": {"keyword": {"type": "keyword", "ignore_above": 8191}}},
      "strategy_name": {"type": "text", "analyzer": "split_by_whitespace_analyzer", "norms": false, "fields": {"keyword": {"type": "keyword", "ignore_above": 8191}}},
      "metric_id": {"type": "text", "analyzer": "split_by_whitespace_analyzer", "norms": false, "fields": {"keyword": {"type": "keyword", "ignore_above": 8191}}}
    }
  }
}
```

- `related_alert_ids` 声明为 `keyword`，写入时传 ID 数组。按 `bk_tenant_id` 和 `related_alert_ids` 同时做 `term` 查询，可以从一个汇总告警找到跨时间桶的全部关联原始告警。
- `name_search`、`content_search` 使用文本主字段；`.keyword` 用于整值精确匹配。草案建议 `metric_id` 复用同一文本分析器，以 `match_phrase` 检索指标全名，实施时用组合表达式的第二个指标及含点号名称验证包含匹配。
- 模板保留 `_source` 默认设置，详情可以返回完整正文与结构化 `dimension`。建议两个时间字段写入带时区的 ISO 8601 时间，或 epoch 毫秒。
- 已登记的租户扩展字段由登记结果决定物理路径。若采用顶层同名文本方案，`region` 可复用 `name` 的字段定义，精确过滤使用 `region.keyword`。若采用登记 ID 路径，则在相应物理路径声明相同类型。示例只列固定字段；扩展字段的模板和已有时间桶的 mapping 更新方式待物理路径确定后补齐。

## 写入、更新与归档

1. **丰富完成后首次写入。** Linkd 将已冻结的有效字段投影到 RawAlarmEvent，写入 `processing_status=unprocessed`。此时原始告警已可检索，页面无需等待生成 Alert。
2. **Lifecycle 裁决完成后更新原文档。** 用同一身份更新 `processing_status` 和 `related_alert_ids`。被抑制或未生成 Alert 的 Event 也保留自己的记录。投影需要能独立重试；即使两次投递乱序，最终文档也应包含完整的丰富字段和已提交的处理状态。迟到的首次投递不能将终态改回 `unprocessed`。现有 hook 随 Alert 变更执行，新增投影还要覆盖没有 Alert 变更的 Event。
3. **汇总告警按字段所有权更新。** 首次触发投影到 AlarmEvent；持续触发时，按产品约定随 Event 更新的字段取本次有效值。处置、关闭等没有新来源 Event 的操作只更新自己负责的状态。同一告警两次地域先后为“华南”“华东”时，两条 RawAlarmEvent 各留本次地域，按最新 Event 同步地域的 AlarmEvent 更新为“华东”。
4. **按关联归档。** 在同租户内用 `AlarmEvent.alarm_id` 匹配 `RawAlarmEvent.related_alert_ids`，跨时间桶找齐原始告警；部分完成后可继续。若同一原始告警还关联在线告警，归档应兼顾在线关联详情的可见性。建议为 RawAlarmEvent 使用独立查询别名，按不可变的 Event 接收时间分桶，首次写入和后续更新定位同一桶。桶宽及物理搬迁方式留到实施时确定。

## 新增字段的租户隔离

插件或丰富方案声明字段时，沿用三张字段表。`kac_alert_field` 以 `(bk_tenant_id, key)` 登记产品字段，`alert_field` 登记同租户标准路径，`kac_alert_field_mapping` 记录各阶段的读写规则。同租户给已有字段赋值时复用原登记。RawAlarmEvent 和 AlarmEvent 共享该字段身份，投影、检索和归档均带租户作用域。

以某租户的 `region` 为例，下面三种情况适用于本次 Event 的展示、检索、后续 Event 策略匹配和相应的 KAC 告警投影。

| 插件上报 | 丰富赋值 | RawAlarmEvent 展示及检索 | Event 策略读到 | 投影到 AlarmEvent |
| --- | --- | --- | --- | --- |
| 华南 | 无 | 华南 | 华南 | 华南 |
| 无 | 华东 | 华东 | 华东 | 华东 |
| 华南 | 华东 | 华东 | 华东 | 华东 |

第三种情况下，按“华东”能查到这条原始告警，按“华南”查不到。另一个租户登记同名 key 时独立发布和查询。

## 待定的两个取值与存储问题

- **alarmd 多等级 Event 的单行展示。** 单等级来源直接展示唯一结果。alarmd 的同一 Event 可以同时包含一个等级的触发和另一个等级的恢复。建议沿用 alarmd 已选定的主等级，取对应的 `level`、`action` 和丰富值。主等级如何明确传给 Linkd 投影仍待确认；现有 Event 的 `evaluations` 数组顺序没有这个含义。该决策只影响展示取值，文档身份、Event 级处理状态和分页条数均已确定。[alarmd 输出](../../../../../alarmd/linkdoutput/converter.go) · [Linkd Event 定义](../../../../internal/domain/event.go)
- **跨租户同名字段的 ES 类型。** 租户隔离和字段登记已经确定。共享同一物理索引时，若同名 key 的 ES 类型一致，可直接写顶层 `region`。若要容纳不同类型，则需区分物理路径，可利用已有字段登记 ID，由 KAC 按租户把页面 key 翻译为 ES 路径。实施前需核对同名字段的类型约定和实际字段量，再选择物理表示。
