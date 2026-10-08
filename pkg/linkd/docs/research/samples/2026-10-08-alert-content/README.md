# test-bkee5 告警内容审阅样本（2026-10-08）

采集开始：2026-10-08T11:46:55.873359+08:00（Asia/Shanghai）。Linkd HEAD `ac1331dec50c`，bk-monitor 源码 HEAD `51c834dc6623`。本工作树包含尚未提交的内容生成改动。

## 验证范围

真实 alarmd_event + 线上只读 MySQL，standard Cleaner/生产 Reader/Resolver/Router/Lifecycle/Preview；Alert 仅入本地 MemoryRepository，来源配置为本地 fixture。未部署或切换线上 EventSource，未执行普通资源 processors/Hook，未写线上 ES，未提交 Kafka offset。

- 下方 `alert.content` 是此工作树代码处理真实输入后、写入本地 MemoryRepository 的实际结果；不是人工撰写的期望文案，也不是已切换模式后的线上 ES 告警。
- `bkmonitor_expected_content` 由同一触发值、同一冻结策略算法/单位输入实际 bk-monitor Threshold/Ping 源码 oracle 得到；这不是读取线上 bk-monitor 已存 Event.description。
- 来源 EventSource 使用本地 fixture，发布版本 7；这个数字不是策略版本。Alert JSON 是实际对象的审阅投影，省略维度、labels、ExtraData、实例身份等非文案字段；完整原始 payload 和凭据没有落入仓库。
- 文本中的 IP/URL 在对照完成后才脱敏，本次 0 个文本字段需要替换。保留 Kafka partition/offset、策略身份、观测值与单位供追踪。

## 结果与覆盖

- 扫描 `alarmd_event` partition 0 的快照保留区间 `[1484421, 1653820)`，共 169,399 条，约 176.04 MiB。上限 200,000 条 / 512 MiB / 120 秒，本次完整读取该快照范围；快照之后的新消息不在本次统计中。
- 候选 18 条，输出 13 条 Alert，拒绝 5 条。13 条的源码字符串对照、只读预览、终态重投、来源内容不变检查全部通过；5 条均在计划保存前被 Block。
- 成功输出均为 Threshold：主机 6 条、APM 6 条、K8s 节点 1 条，实际创建级别包括 critical 与 info。所有样本来自真实 triggered 输入；不把未触发或没有匹配样本的配置写成验收通过。

| 场景 | 当前 active/published 配置 | 真实输出 | 状态 |
| --- | ---: | ---: | --- |
| 主机 target 指标 | 30 | 6 | 已输出并对照通过 |
| APM data 指标（kapm） | 2 | 6 | 已输出并对照通过 |
| K8s 节点指标 | 1 | 1 | 已输出并对照通过 |
| MySQL target 指标 | 12 | 0 | 清单中有配置；此保留区间未读到事件 |
| 日志关键字（含 K8s Workload） | 6 | 0 | 清单中有配置；此保留区间未读到事件 |
| 日志指标（data / metric / klc） | 1 | 0 | 清单中有配置；此保留区间未读到事件 |
| 其他 DATA 指标 | 4 | 0 | 清单中有配置；此保留区间未读到事件 |
| 主机专用 event | 17 | 0 | 17 条配置，含 PingUnreachable/OsRestart；未读到事件 |
| Cloud、UptimeCheck | 本次发布清单未识别到 | 0 | 未验收；不推断其他环境或历史中不存在 |
| 历史比较、预测、无数据等 | 本次无可核对的稳定事实 | 0 | 按已确认范围记录并跳过 |

本次发布清单共 73 条；它只是采集开始时的 current 只读快照。配置可能继续变化，生产 Reader 在处理每条事件时重新核对版本与 Set/Config 的完整渲染依赖。

## 成功输出速览

| 样本 | 场景 | 策略 / 级别 | Kafka offset | 实际 Alert.content |
| --- | --- | --- | ---: | --- |
| [S01](#s01) | APM 指标 | 48 / critical | 1484499 | AVG_WITHOUT_TIME(分钟请求数) >= 5.0, 当前值10 |
| [S02](#s02) | 主机指标 | 18 / critical | 1484468 | AVG(CPU使用率) >= 1.0, 当前值27.71261% |
| [S04](#s04) | 主机指标 | 35 / critical | 1484421 | AVG(磁盘空间使用率) >= 10.0%, 当前值83.579242% |
| [S05](#s05) | K8s 节点指标 | 75 / critical | 1485266 | AVG(节点15分钟CPU负载) != 12321321.0, 当前值9.19 |
| [S06](#s06) | APM 指标 | 48 / critical | 1484672 | AVG_WITHOUT_TIME(分钟请求数) >= 5.0, 当前值9.833333 |
| [S07](#s07) | 主机指标 | 73 / critical | 1484502 | AVG(CPU使用率) >= 1.0, 当前值27.785919% |
| [S09](#s09) | 主机指标 | 16 / info | 1484481 | AVG(CPU使用率) >= 1.0%, 当前值50.012525% |
| [S10](#s10) | APM 指标 | 47 / critical | 1485545 | AVG(分钟请求数) >= 3.0, 当前值36 |
| [S12](#s12) | 主机指标 | 27 / critical | 1484494 | AVG(CPU使用率) >= 40.0%, 当前值40.274794% |
| [S13](#s13) | APM 指标 | 48 / critical | 1485604 | AVG_WITHOUT_TIME(分钟请求数) >= 5.0, 当前值36 |
| [S15](#s15) | 主机指标 | 22 / critical | 1484501 | AVG(CPU使用率) >= 2.0%, 当前值37.377124% |
| [S16](#s16) | APM 指标 | 47 / critical | 1487170 | AVG(分钟请求数) >= 3.0, 当前值7 |
| [S18](#s18) | APM 指标 | 47 / critical | 1491933 | AVG(分钟请求数) >= 3.0, 当前值4 |

## 逐条样本

### S01

- 场景：APM 指标；配置 `data/metric/kapm/空`。
- 定位：`alarmd_event` partition 0 / offset 1484499；策略 ID `48`，事件版本 `1790758901024762`，当前发布版本 `1790758901024762`。
- 输入值：`10`，单位 `空`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=10 at 2026-10-06T16:38:00Z; 5 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.04fff2efcac2e233",
  "begin_at": "2026-10-06T16:38:00Z",
  "bk_tenant_id": "system",
  "content": "AVG_WITHOUT_TIME(分钟请求数) >= 5.0, 当前值10",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 48 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.c19d8436f3a5dae1"
}
```

bk-monitor 同事实源码输出：

```text
AVG_WITHOUT_TIME(分钟请求数) >= 5.0, 当前值10
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S02

- 场景：主机指标；配置 `target/metric/空/cw-Host`。
- 定位：`alarmd_event` partition 0 / offset 1484468；策略 ID `18`，事件版本 `1790740300905777`，当前发布版本 `1790740300905777`。
- 输入值：`27.7126099706`，单位 `percent`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=27.7126099706 at 2026-10-06T16:37:00Z; 4 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.0355a4685d2a5e6b",
  "begin_at": "2026-10-06T16:37:00Z",
  "bk_tenant_id": "system",
  "content": "AVG(CPU使用率) >= 1.0, 当前值27.71261%",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 18 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.832b78e3d25a377c"
}
```

bk-monitor 同事实源码输出：

```text
AVG(CPU使用率) >= 1.0, 当前值27.71261%
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S03

- 场景：主机指标；配置 `target/metric/kmc/cw-Host`。
- 定位：`alarmd_event` partition 0 / offset 1484446；策略 ID `3`，事件版本 `1791223569846858`，当前发布版本 `1791429887550023`。
- 输入值：`33458397184`，单位 `bytes`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=33458397184 at 2026-10-06T16:37:00Z; 3 of 3 points in the window were anomalous
```

实际结果：`blocked`，错误码 `publication_version_mismatch`。没有生成 Alert.content，没有保存 EventPlan；来源 Event 保持=True。

原因：事件整数 strategy_version 与 current 发布记录不符。按实现拒绝借用当前配置生成旧事件内容；这不是把大整数当成异常，也没有回退复制来源 content。恢复需要同一触发版本的可验证发布材料，不读取废弃历史表。

### S04

- 场景：主机指标；配置 `target/metric/kmc/cw-Host`。
- 定位：`alarmd_event` partition 0 / offset 1484421；策略 ID `35`，事件版本 `1790758900458208`，当前发布版本 `1790758900458208`。
- 输入值：`83.5792420435`，单位 `percent`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=83.5792420435 at 2026-10-06T16:37:00Z; 4 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.12835117aff4775a",
  "begin_at": "2026-10-06T16:37:00Z",
  "bk_tenant_id": "system",
  "content": "AVG(磁盘空间使用率) >= 10.0%, 当前值83.579242%",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 35 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.bbb95d244caadf8d"
}
```

bk-monitor 同事实源码输出：

```text
AVG(磁盘空间使用率) >= 10.0%, 当前值83.579242%
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S05

- 场景：K8s 节点指标；配置 `target/metric/kmc/cw-K8s_Node`。
- 定位：`alarmd_event` partition 0 / offset 1485266；策略 ID `75`，事件版本 `1790758901821402`，当前发布版本 `1790758901821402`。
- 输入值：`9.19`，单位 `空`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=9.19 at 2026-10-06T16:47:00Z; 1 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.e6bceafecdf416cb",
  "begin_at": "2026-10-06T16:47:00Z",
  "bk_tenant_id": "system",
  "content": "AVG(节点15分钟CPU负载) != 12321321.0, 当前值9.19",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 75 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.ed9d3b10a856e884"
}
```

bk-monitor 同事实源码输出：

```text
AVG(节点15分钟CPU负载) != 12321321.0, 当前值9.19
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S06

- 场景：APM 指标；配置 `data/metric/kapm/空`。
- 定位：`alarmd_event` partition 0 / offset 1484672；策略 ID `48`，事件版本 `1790758901024762`，当前发布版本 `1790758901024762`。
- 输入值：`9.833333333333334`，单位 `空`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=9.833333333333334 at 2026-10-06T16:40:00Z; 5 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.f60882b28c1a6a1e",
  "begin_at": "2026-10-06T16:40:00Z",
  "bk_tenant_id": "system",
  "content": "AVG_WITHOUT_TIME(分钟请求数) >= 5.0, 当前值9.833333",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 48 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.67bef416c9d21823"
}
```

bk-monitor 同事实源码输出：

```text
AVG_WITHOUT_TIME(分钟请求数) >= 5.0, 当前值9.833333
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S07

- 场景：主机指标；配置 `target/metric/空/cw-Host`。
- 定位：`alarmd_event` partition 0 / offset 1484502；策略 ID `73`，事件版本 `1790740323957111`，当前发布版本 `1790740323957111`。
- 输入值：`27.785919082042`，单位 `percent`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=27.785919082042 at 2026-10-06T16:38:00Z; 5 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.1431b4ee63b6a97b",
  "begin_at": "2026-10-06T16:38:00Z",
  "bk_tenant_id": "system",
  "content": "AVG(CPU使用率) >= 1.0, 当前值27.785919%",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 73 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.310d99fd63d07446"
}
```

bk-monitor 同事实源码输出：

```text
AVG(CPU使用率) >= 1.0, 当前值27.785919%
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S08

- 场景：主机指标；配置 `target/metric/kmc/cw-Host`。
- 定位：`alarmd_event` partition 0 / offset 1484447；策略 ID `3`，事件版本 `1791223569846858`，当前发布版本 `1791429887550023`。
- 输入值：`154704982016`，单位 `bytes`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=154704982016 at 2026-10-06T16:37:00Z; 2 of 3 points in the window were anomalous
```

实际结果：`blocked`，错误码 `publication_version_mismatch`。没有生成 Alert.content，没有保存 EventPlan；来源 Event 保持=True。

原因：事件整数 strategy_version 与 current 发布记录不符。按实现拒绝借用当前配置生成旧事件内容；这不是把大整数当成异常，也没有回退复制来源 content。恢复需要同一触发版本的可验证发布材料，不读取废弃历史表。

### S09

- 场景：主机指标；配置 `target/metric/kmc/cw-Host`。
- 定位：`alarmd_event` partition 0 / offset 1484481；策略 ID `16`，事件版本 `1790758899400975`，当前发布版本 `1790758899400975`。
- 输入值：`50.0125250459`，单位 `percent`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=50.0125250459 at 2026-10-06T16:37:00Z; 4 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.3bb2cd616cb421a7",
  "begin_at": "2026-10-06T16:37:00Z",
  "bk_tenant_id": "system",
  "content": "AVG(CPU使用率) >= 1.0%, 当前值50.012525%",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "info",
  "status": "active",
  "title": "Strategy 16 level 3 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.192942898e0f7b9e"
}
```

bk-monitor 同事实源码输出：

```text
AVG(CPU使用率) >= 1.0%, 当前值50.012525%
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S10

- 场景：APM 指标；配置 `data/metric/kapm/空`。
- 定位：`alarmd_event` partition 0 / offset 1485545；策略 ID `47`，事件版本 `1790758901024762`，当前发布版本 `1790758901024762`。
- 输入值：`36`，单位 `空`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=36 at 2026-10-06T16:51:00Z; 1 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.9ee8da961c2b3a3f",
  "begin_at": "2026-10-06T16:51:00Z",
  "bk_tenant_id": "system",
  "content": "AVG(分钟请求数) >= 3.0, 当前值36",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 47 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.9c455aa5d761ff0e"
}
```

bk-monitor 同事实源码输出：

```text
AVG(分钟请求数) >= 3.0, 当前值36
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S11

- 场景：主机指标；配置 `target/metric/kmc/cw-Host`。
- 定位：`alarmd_event` partition 0 / offset 1484469；策略 ID `11`，事件版本 `1791223571555808`，当前发布版本 `1791429888447047`。
- 输入值：`40.2747942174`，单位 `percent`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=40.2747942174 at 2026-10-06T16:37:00Z; 2 of 5 points in the window were anomalous
```

实际结果：`blocked`，错误码 `publication_version_mismatch`。没有生成 Alert.content，没有保存 EventPlan；来源 Event 保持=True。

原因：事件整数 strategy_version 与 current 发布记录不符。按实现拒绝借用当前配置生成旧事件内容；这不是把大整数当成异常，也没有回退复制来源 content。恢复需要同一触发版本的可验证发布材料，不读取废弃历史表。

### S12

- 场景：主机指标；配置 `target/metric/kmc/cw-Host`。
- 定位：`alarmd_event` partition 0 / offset 1484494；策略 ID `27`，事件版本 `1790758899573486`，当前发布版本 `1790758899573486`。
- 输入值：`40.2747942174`，单位 `percent`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=40.2747942174 at 2026-10-06T16:37:00Z; 1 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.6078eb76ea1e2f2b",
  "begin_at": "2026-10-06T16:37:00Z",
  "bk_tenant_id": "system",
  "content": "AVG(CPU使用率) >= 40.0%, 当前值40.274794%",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 27 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.23859a27a1cc429f"
}
```

bk-monitor 同事实源码输出：

```text
AVG(CPU使用率) >= 40.0%, 当前值40.274794%
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S13

- 场景：APM 指标；配置 `data/metric/kapm/空`。
- 定位：`alarmd_event` partition 0 / offset 1485604；策略 ID `48`，事件版本 `1790758901024762`，当前发布版本 `1790758901024762`。
- 输入值：`36`，单位 `空`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=36 at 2026-10-06T16:52:00Z; 1 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.b4f04b03db6c108e",
  "begin_at": "2026-10-06T16:52:00Z",
  "bk_tenant_id": "system",
  "content": "AVG_WITHOUT_TIME(分钟请求数) >= 5.0, 当前值36",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 48 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.2c6f114a23132cf0"
}
```

bk-monitor 同事实源码输出：

```text
AVG_WITHOUT_TIME(分钟请求数) >= 5.0, 当前值36
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S14

- 场景：主机指标；配置 `target/metric/kmc/cw-Host`。
- 定位：`alarmd_event` partition 0 / offset 1484482；策略 ID `2`，事件版本 `1791223571659731`，当前发布版本 `1791429888571820`。
- 输入值：`40.2747942174`，单位 `percent`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=40.2747942174 at 2026-10-06T16:37:00Z; 2 of 5 points in the window were anomalous
```

实际结果：`blocked`，错误码 `publication_version_mismatch`。没有生成 Alert.content，没有保存 EventPlan；来源 Event 保持=True。

原因：事件整数 strategy_version 与 current 发布记录不符。按实现拒绝借用当前配置生成旧事件内容；这不是把大整数当成异常，也没有回退复制来源 content。恢复需要同一触发版本的可验证发布材料，不读取废弃历史表。

### S15

- 场景：主机指标；配置 `target/metric/kmc/cw-Host`。
- 定位：`alarmd_event` partition 0 / offset 1484501；策略 ID `22`，事件版本 `1790758899708949`，当前发布版本 `1790758899708949`。
- 输入值：`37.3771243036`，单位 `percent`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=37.3771243036 at 2026-10-06T16:38:00Z; 5 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.87142033d7c2e0f9",
  "begin_at": "2026-10-06T16:38:00Z",
  "bk_tenant_id": "system",
  "content": "AVG(CPU使用率) >= 2.0%, 当前值37.377124%",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 22 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.ad1f3850c568d900"
}
```

bk-monitor 同事实源码输出：

```text
AVG(CPU使用率) >= 2.0%, 当前值37.377124%
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S16

- 场景：APM 指标；配置 `data/metric/kapm/空`。
- 定位：`alarmd_event` partition 0 / offset 1487170；策略 ID `47`，事件版本 `1790758901024762`，当前发布版本 `1790758901024762`。
- 输入值：`7`，单位 `空`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=7 at 2026-10-06T17:11:00Z; 3 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.956f7cbe7e68b0a3",
  "begin_at": "2026-10-06T17:11:00Z",
  "bk_tenant_id": "system",
  "content": "AVG(分钟请求数) >= 3.0, 当前值7",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 47 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.1a9fc3a2ddcaa6c7"
}
```

bk-monitor 同事实源码输出：

```text
AVG(分钟请求数) >= 3.0, 当前值7
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

### S17

- 场景：主机指标；配置 `target/metric/kmc/cw-Host`。
- 定位：`alarmd_event` partition 0 / offset 1484503；策略 ID `7`，事件版本 `1791223571748751`，当前发布版本 `1791429888677439`。
- 输入值：`0.853175695597`，单位 `percentunit`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=0.853175695597 at 2026-10-06T16:38:00Z; 5 of 5 points in the window were anomalous
```

实际结果：`blocked`，错误码 `publication_version_mismatch`。没有生成 Alert.content，没有保存 EventPlan；来源 Event 保持=True。

原因：事件整数 strategy_version 与 current 发布记录不符。按实现拒绝借用当前配置生成旧事件内容；这不是把大整数当成异常，也没有回退复制来源 content。恢复需要同一触发版本的可验证发布材料，不读取废弃历史表。

### S18

- 场景：APM 指标；配置 `data/metric/kapm/空`。
- 定位：`alarmd_event` partition 0 / offset 1491933；策略 ID `47`，事件版本 `1790758901024762`，当前发布版本 `1790758901024762`。
- 输入值：`4`，单位 `空`；算法 `Threshold`。

来源 Event.content（保持原文）：

```text
value=4 at 2026-10-06T18:09:00Z; 5 of 5 points in the window were anomalous
```

实际新建 Alert 的审阅投影：

```json
{
  "alert_id": "20261008000000.system.built_in_bk.2e7a58663ccd5c3f",
  "begin_at": "2026-10-06T18:09:00Z",
  "bk_tenant_id": "system",
  "content": "AVG(分钟请求数) >= 3.0, 当前值4",
  "enrich_status": "succeeded",
  "event_source_id": "built_in_bk",
  "event_source_version": 7,
  "severity": "critical",
  "status": "active",
  "title": "Strategy 47 level 1 triggered",
  "trigger_event_id": "20261008000000.system.built_in_bk.f37f393ca43e5c78"
}
```

bk-monitor 同事实源码输出：

```text
AVG(分钟请求数) >= 3.0, 当前值4
```

验证：源码对照=True；预览一致=True；重投保持=True；来源 Event 保持=True。

## 文件与复现入口

- [samples.json](samples.json)：机器可读的输入事实摘要、实际 Alert 审阅投影、成功检查项、拒绝错误码、读取范围和 current 发布清单。
- [审阅导出集成测试](../../../../internal/enrich/assembly/content_review_live_test.go)：输入最多 64 条 / 8 MiB，输出为新建的 0600 文件；集成测试需显式开启，普通单测默认跳过。
- 当前全保留区间未取得的场景只能等待真实触发或另一个已存在的、具有版本绑定的真实事实入口；本轮没有改策略、制造告警、发布来源或向 Hook 发消息。

## 本轮检查

- 显式真实输入导出集成测试 `TestContentReviewLiveSamples`：`go test -race -count=1` 通过，导出 18 条记录。
- assembly 包普通 race 测试：14 个测试通过；普通运行中两个真实输入测试仍按显式环境条件跳过。
- 本轮 `go vet ./internal/enrich/assembly` 通过，`golangci-lint run ./internal/enrich/assembly --timeout=3m` 为 0 issues。
- 本轮未重新运行完整 `make check`；9 月 30 日的完整门禁结果不作为 10 月 8 日的新结果。
