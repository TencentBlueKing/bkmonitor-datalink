# Linkd 自定义丰富开发规格

日期：2026-09-22。状态：已确认开发目标；实现进度见文末。

本文是 CMDB/常规丰富、补丁、配置转换和执行预览的权威规格。代码职责与依赖边界见[包结构](package-structure.md)。已有业务场景见
[Enrich 设计](enrich.md)，KAC 源码事实见[源码调研](../research/2026-09-22-kingeye-alarm-source-enrichment.md)。
实现进度与设计目标分别记录，未验证事项不表述为已经支持。

## 1. 已确认目标

1. 平替 KAC CMDB 和常规字段丰富的核心能力，保留可核验的差异，不复制旧缺陷。
2. 自定义丰富只需一个 cmdb Processor、一个 fields Processor，各自包含多条 rules。
3. 原始 Alert/Event 不修改；enrich 记录各 Processor 的有序 JSONPath 补丁，读取时动态合成。
4. 不保存最终 values，不把 enrich 当成另一份业务数据空间。
5. 正式执行仅在新建 Alert 时发生；普通更新、update_current 升级、恢复和关闭沿用已保存结果。
6. 配置随 EventSource Release 动态发布，API/YAML/Provider 共用校验。
7. 新建独立内部 OneModel SDK，第一版读 ES，接口支持后续替换后端。
8. 提供不保存的 API 预览，支持 Alert ID 和 Alert JSON；Console 提供专用调试页。
9. 暂不读取 Kingeye 丰富配置表；离线转换器供后续同步复用，不自动发布。

## 2. 输入与字段边界

### 2.1 JSONPath 上下文

| 根节点 | 语义 |
|---|---|
| $.original | 原始 Alert 的隔离副本，排除历史 enrich，始终只读 |
| $.alert | 原始 Alert 应用本次前序成功补丁后的当前视图 |
| $.lookup | 当前 CMDB 规则的查询结果，规则之间不残留上次结果 |
| $.extraction | 当前提取操作的结果，操作间不共享，不默认持久化 |

读取采用 RFC 9535 JSONPath 语义，支持属性、数组、通配符、切片、递归与过滤，不支持用户脚本和函数注册。
为保证有界执行，单个路径段最多 8 个选择器；过滤表达式内部只接受确定路径，不允许再次做集合通配、切片、递归、多选或嵌套过滤。
默认单值选择，零命中与多命中分别处理；select: all 显式返回列表。
字段缺失、null、空字符串、0、false 必须区分，默认值只处理缺失，不吞掉依赖故障。

采用 github.com/theory/jsonpath v0.12.1（MIT，无运行时第三方依赖）。标准库和已有依赖不提供
RFC 9535；用内部适配层隔离库 API，版本升级需要路径一致性和数值回归测试。

### 2.2 可写字段

| 目标 | 约束 |
|---|---|
| title、content、subject_name | 现有字符串类型和长度约束 |
| labels 的单个字段 | 允许新增/覆盖，包括 strategy_id；值必须是 Scalar |
| extra_data 的子路径 | 结构化业务信息，受大小、深度和保留字段约束 |

禁止替换根对象、整个 labels/extra_data；禁止改写租户、来源、身份、fingerprint、dimensions、severity、
生命周期状态/时间、enrich、enrich_status。CMDB 识别出的身份写入业务标签，不修改原始主体关联身份。
`labels`/`extra_data` 下的 `cw_labels`、`dynamic_group_id` 由受控处理器生成，自定义规则不能覆盖，也不能借父对象覆盖绕过限制。

写路径只接受属性名和非负数组下标，不允许通配符、过滤器、递归。缺失的对象父节点可创建；数组下标
必须存在，不填充稀疏数组。null 是赋值而不是删除，并受目标字段类型约束。

## 3. 补丁和执行协议

```json
{
  "processors": [
    {
      "fields": {
        "status": "succeeded",
        "patches": [
          {"op": "set", "path": "$.labels.strategy_id", "value": 9001,
           "rule_id": "business_labels", "operation_id": "assign_strategy"}
        ]
      }
    }
  ]
}
```

首版只有 set，value 是已求值 JSON，不保存待执行模板。Processor/rule/operation 都按数组顺序执行。
一个操作对快照完成取值和校验后，原子提交全部补丁；操作内部重复/父子重叠目标拒绝。
跨操作允许重复目标并保存实际顺序，后续操作读取已经提交的结果。
失败操作无补丁，停止当前规则剩余操作，继续后续规则；父 Context 取消立即结束整个调用。
规则的 when 只在规则开始时判断一次，不因前一步修改匹配字段而重新判断。

每个 Processor 只有一份 patches。规则/操作状态、ID、诊断、耗时和命中数作为执行记录，不复制输出。
succeeded/partial 中已提交补丁参与合成，failed/skipped 不应用补丁；顶层 enrich_status 按适用步骤聚合。
历史内置 value 仅在读取边界提供确定投影，保护已有真实告警；所有新执行仅写 patches，不双写或回填历史数据。

## 4. EventSource 配置

每种 Processor 类型最多一次；rule ID 在 Processor 内唯一，operation ID 在规则内唯一。

```yaml
enrich:
  datasources:
    elasticsearch:
      addresses: [http://onemodel-es:9200]
  processors:
    - type: cmdb
      config:
        rules:
          - id: host
            lookup:
              model_id: cw-Host
              expect: one
              where:
                all:
                  - field: attributes.bk_host_innerip
                    type: keyword
                    operator: eq
                    value: {jsonpath: $.alert.labels.ip}
                  - field: attributes.bk_cloud_id
                    type: long
                    operator: eq
                    value: {jsonpath: $.alert.labels.bk_cloud_id}
            assignments:
              - target: $.labels.business
                value: {jsonpath: $.lookup.attributes.bk_biz_name}
              - target: $.labels.owner
                value: {jsonpath: $.lookup.attributes.operator}
    - type: fields
      config:
        rules:
          - id: normalize
            when:
              all:
                - left: {jsonpath: $.alert.content}
                  operator: contains
                  right: {literal: 'IP='}
            operations:
              - id: extract_ip
                type: extract
                source: {jsonpath: $.alert.content}
                pattern: 'IP=([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)'
                assignments:
                  - target: $.labels.extracted_ip
                    value: {jsonpath: '$.extraction.matches[0].groups[0]'}
              - id: translate
                type: replace
                target: $.content
                replacements:
                  - {from: CRITICAL, to: 严重}
                  - {from: CPU usage, to: CPU 使用率}
              - id: compose
                type: assign
                assignments:
                  - target: $.title
                    value:
                      template: '[${ip}] ${content}'
                      variables:
                        ip: {jsonpath: $.alert.labels.extracted_ip}
                        content: {jsonpath: $.alert.content}
          - id: business_labels
            operations:
              - id: assign_strategy
                type: assign
                assignments:
                  - target: $.labels.strategy_id
                    value: {literal: 9001}
                  - target: $.labels.environment
                    value: {literal: production}
```

### 4.1 条件和取值

when 支持嵌套 all/any/not，叶条件为 left/operator/right；eq/ne/contains/not_contains/regex/not_regex/in/not_in/exists。
省略 when 匹配所有输入。Value 的 literal/jsonpath/template 三选一，可另设 select、default 和顺序 transforms。
transforms 包括类型转换、join、字典映射、正则提取和展示值转换。
缺失路径默认跳过当前规则；缺少模板变量不隐式变为空串。所有静态表达式在发布前编译。

### 4.2 常规丰富

- assign：常量、复制、模板和转换后赋值。跨字段依赖拆为同一规则内多个操作。
- replace：目标已有字符串，按 replacements 做文字替换。旧 is_regex 不影响 KAC 当前实际替换语义。
- extract：source 只读一次，find-all 返回 matches[].text/groups/named_groups；多个赋值使用同一提取结果。
- 采用 Go regexp，无法表达的 Python 环视、回溯引用等明确拒绝，不静默改变语义。
- KAC 零/一捕获组按匹配次数编号、多捕获组按首匹配分组编号；转换器必须区分。

### 4.3 CMDB 丰富

lookup 描述 model_id、expect（one/many）、where；where 的 field/type/operator/value 不包含物理 ES DSL。
查询值复用 Value，可先正则提取再查询。租户来自运行上下文，配置不能覆盖。
one 的零命中为 skipped，多命中为冲突；many 返回有界列表，超限失败，不静默取首条或截断。
已有身份和附加条件都明确表达，不隐式忽略条件。

relations 描述有序关联读取，每项含 id/from/relation/direction/model_id/expect/where；from 只能引用主实例或
之前结果，不允许前向引用。关联结果放入 $.lookup.relations.<id>，中间节点要求唯一，末节点可以多实例。
主实例为 cw-Host 且 expect: one 时可配置 `topology: true`，结果位于 `$.lookup.topology`。
业务、集群、模块赋值仍须在 assignments 中显式声明；用 `$.alert.labels.bk_biz_id` 加
`default: {jsonpath: $.lookup.topology.bk_biz_id}` 只补缺失值，不隐式改写已有值。
展示转换覆盖枚举、单位、时间、用户、组织、云区域。
先逐个转换再合并，保留原始类型；组织 ID 使用完整值，不复制旧代码取首字符行为。

## 5. OneModel SDK 与装配

internal/onemodel 独立定义只读实例、查询、关系、拓扑接口和 DTO，不依赖 Enrich/KAC。
现有资源丰富和 cmdb 是两个实际消费者；先复用并扩展现有 ES 查询，不创建仓外公共模块。
后端要求：显式租户；canonical model_id/model_inst_id/entity_uid；类型化 nested 属性；单次读取 limit+1 检测超限、结果按实例 ID 稳定排序；
HTTP 200 仍检查 timed_out 和 failed shards；响应身份复核；Context 传播；资源正常/失败路径都释放。

展示元数据/Redis 缓存是独立 Reader，不把 KAC 缓存结构塞进 SDK。需要展示转换才装配相应连接；
普通字段规则不要求外部数据源。同一次调用使用局部缓存，无隐藏全局租户状态。
正式执行和预览共享编译、连接装配和执行代码，但预览只注入读能力。

默认硬上限：每类 128 条规则，每规则 64 个操作/赋值，单 Processor 配置 64 KiB；JSON 1 MiB、深度 64；
JSONPath 2048 字符/1024 最终命中、中间节点 16384、累计选择预算 65536；关联深度 8、实例 1024、外部查询 64；补丁全链 4096 条，处理器结果编码总预算 900000 字节（为最终信封预留空间）。
编译阶段另限制路径递归/过滤组合复杂度，不用后台 goroutine 伪超时；查询受父 Context 及 I/O 超时限制。
生产并发沿用 Lifecycle 预算，预览额外最多 4 个并发请求。

## 6. 动态发布和消费

API/YAML/Provider 共用无 I/O 的静态编译，返回规则/操作/配置路径错误。动态更新仍走不可变 Release 与任务交接；
单次执行固定一份配置。已保存计划重试复用已求值补丁，不自动重新丰富历史告警。

内置丰富投影成同一补丁：display 对应 title/content/subject_name，业务/策略/模型标量进入 labels，
复杂查询参数/维度展示/拓扑列表进入 extra_data。输入 dimensions 和 subject 身份保持原值。
KAC Hook 读取合成视图，保持 monitor_template_id→KAC strategy_id 等已确认契约；自定义字段显式映射，
禁止覆盖协议身份/租户/动作/级别。策略索引使用合成后的 labels.strategy_id，不用原始字段提前过滤候选告警。
原始数据查询仍返回原值及补丁，不额外持久化有效值。

内置 strategy/resource/display 等仍从原始 Alert 识别其场景和查询身份，并共享现有类型化上下文，
避免资源输出的业务标签改变旧场景查找条件。自定义 cmdb/fields 的 `$.alert` 读取前序全部成功补丁。
推荐顺序为已有内置处理器 → cmdb → fields；需要前序结果的自定义逻辑放在后面。

策略索引和 Console 对账读取身份、原始策略标签及 enrich，按租户/来源限制候选集后再计算策略归属。
这会增加宽租户下的读取量；行数、字节数、超时达到上限时返回失败，不把部分结果当完整集合。

## 7. 不保存的执行预览

POST /api/v1/enrich/preview，使用现有管理 API 鉴权。

```json
{"bk_tenant_id":"tenant-a","event_source_id":"host-alerts",
 "input":{"alert_id":"existing-alert-id"},"enrich":{"processors":[]}}
```

input.alert_id 与 input.alert 二选一；直接 JSON 支持完整 Alert 或只包含丰富输入的对象：

```json
{"bk_tenant_id":"tenant-a","event_source_id":"host-alerts",
 "input":{"alert":{"title":"CPU","content":"IP=10.0.0.8","labels":{"bk_cloud_id":0}}},
 "enrich":{"processors":[]}}
```

ID 模式按租户读取并核对来源；JSON 模式不要求入库，不伪造业务 ID、时间或状态。输入内租户/来源若存在必须一致。
未提交 enrich 使用当前已发布来源配置；临时配置省略连接时继承同来源连接，凭据只在服务器内使用。修改连接目标时不继承旧目标密码。
请求固定 Release，响应给实际版本和排除凭据后的配置摘要。清除历史 enrich 后重跑；旧结果仅作差异对比。
查询当前 CMDB 数据，不承诺历史 as-of 重放。

响应包括 original、enrich_status、enrich、effective_alert、changes、previous_changes 和 trace。
不保存 Alert、AlertLog、配置或预览记录，不更新策略索引、不调用 Hook、不初始化 schema。
错误：输入 400，配置 422，未找到 404，繁忙 429，超时 504；规则运行失败作为 200 的执行结果返回。

Console “丰富调试”页面：ID/JSON 两模式，租户/来源选择，JSON/YAML 配置编辑，加载来源配置，执行，
原始与合成结果、字段差异、规则/操作详情；支持从 Alert 详情带 ID 跳转。编辑只保存在页面内存，不隐式发布。
所有管理调用经 Console 服务端代理，token 和数据源凭据不下发浏览器。

## 8. Kingeye 离线转换

输入为 cmdb_rules/normal_rules、同租户模型目录和来源字段映射；纯转换函数不查规则表、不发布。
CLI 从文件读取，向 stdout 输出配置和报告，供未来配置同步复用。
cmdb_rules 整组→cmdb.config.rules，normal_rules 整组→fields.config.rules，enrich_settings→operations。
保留一次匹配和操作顺序；条件表达式解析为树，不使用 eval；模型通过目录映射为 canonical 身份。多模型关联还需要关系标识和方向，当前转换器将其明确标为 unsupported，保留原配置供人工配置 `relations`，不会猜测。
name→title、content→content、object→subject_name；普通自定义字段显式映射到 labels/extra_data；strategy_id 等必须明确映射，不能按名称猜测。
每条规则报告 converted/needs_review/unsupported，带源 JSON 路径及原因。非法正则/目标、缺失映射不得静默丢规则。
多命中、空值和失败回退差异明确记录，不承诺任意旧配置无损迁移。

### 8.1 离线转换命令

```bash
go run ./cmd/linkd enrich convert-kingeye --file kingeye-enrich.json > converted-enrich.json
```

输入示例（模型 ID/属性类型来自同一租户的导出目录）：

```json
{
  "models": {"12": {"model_id": "cw-Host", "bk_obj_id": "host", "attributes": {"bk_host_innerip": "keyword"}}},
  "field_mappings": {"ip": "$.labels.ip", "owner": "$.labels.owner", "strategy_id": "$.labels.strategy_id"},
  "cmdb_rules": [],
  "normal_rules": [{"name": "environment", "enrich_settings": [{"type": "field_adjust", "fields": [{"key": "owner", "value": "ops"}], "rules": []}]}]
}
```

输出 enrich 和 report；无有效规则时保留空 processors。报告中每条输入都有对应项，unsupported 项不产生
半条规则。CMDB 候选规则始终标 needs_review，复核已有实例身份分支、模型匹配先后、多命中及展示连接。
常规转换保留字段调整的逐字段依赖；提取操作内依赖其他告警字段时要求手工拆分，避免误改旧顺序。

### 8.2 展示转换与 KAC 输出连接示例

```yaml
enrich:
  datasources:
    mysql: {address: kingeye-mysql:3306, database: kingeye, username: reader, password: '<secret>'}
    kingeye_display:
      redis: {address: kingeye-redis:6379, database: 0, password: '<secret>'}
      key_prefix: ''
```

在相应 Value 中配置 `transforms: [{type: display, model_id: cw-Host, field: operator}]`。
数组展示值逐个转换后，另接 `{type: join, separator: ','}`；单次展示转换最多 64 个值/用户名。
缓存缺失保留原值，缓存损坏或连接故障导致当前操作失败，不伪装成成功。字段值为空、0 或 false 不被跳过。

KAC Hook 的 `config.field_mappings` 示例：`owner: $.labels.owner`、`environment: $.labels.environment`。
只能新增非固定协议字段；固定 title/content 等映射自动读取有效视图。`monitor_template_id` 对应 KAC strategy_id，
`labels.strategy_id` 不自动覆盖该协议字段。查询型 regex 使用 ES/Lucene 语义，常规匹配/提取使用 Go regexp。

## 9. 验收与实施状态

单元测试：原始值不变，补丁顺序，字段保护，0/false/null，数组与数值边界，复杂路径，有界与并发隔离。
KAC 对照：匹配一次，连续替换，零/一/多捕获组，无匹配，多次匹配，前后规则依赖，多模型和展示转换。
SDK：租户、唯一/多命中、关系方向、超限、部分 ES 失败、取消和资源释放。
预览：两种输入相同事实产生相同补丁，写端口和 Hook 调用数为零；Console 两模式和差异展示通过浏览器验证。
动态发布：编译失败不发布，配置切换不混用，恢复重试复用补丁，历史结果可读。
门禁：make check；显式集成与浏览器 E2E 单独运行，外部依赖不可用时写明阻塞。
保留当前工作区已有修改，不创建 commit/push，不清理真实历史数据。

- [x] 分组配置与开发规格。
- [x] 补丁、字段保护、动态合成、内置输出。
- [x] JSONPath 和常规规则，含过滤内部查询成本限制。
- [x] OneModel SDK、CMDB/关系/拓扑读取和展示转换。
- [x] 配置发布校验、KAC 和 Go/Console 策略索引消费。
- [x] 预览 API 和 Console，两种输入、无写入和浏览器交互回归。
- [x] Kingeye 离线转换与常规规则源码样例回归；CMDB 候选附复核报告。
- [x] `make check`、离线 CLI、预览页面 Playwright E2E。
- [ ] 真实 Kingeye/OneModel 环境联调及多模型旧配置的自动转换；当前没有可用环境和真实来源配置。

### 9.1 本次验证记录

- `make check`：格式、全部 Go 普通测试、vet、race、golangci-lint、Console 类型检查/测试/构建、Helm 和发布脚本检查通过；静态分析 0 issues。
- Console 单元测试：231 passed，5 个显式外部集成测试未启用而 skipped。
- `LINKD_CONSOLE_E2E_PORT=5379 console/node_modules/.bin/playwright test --config console/playwright.config.ts console/tests/e2e/enrich-preview.spec.ts`：1 passed；覆盖 JSON/ID、临时 YAML、补丁展示、桌面/窄屏。
- 浏览器测试使用受控 API 响应；真实 Go 规则执行由 HTTP/服务/处理器测试覆盖，两者不等于真实 Kingeye 联调。
- `go run ./cmd/linkd enrich convert-kingeye --file <file>`：离线字段调整样例输出一个 fields 处理器及 converted 报告。
- 调用顺序回归：CMDB 输出 → fields 模板读取，保留 `$.original`；旧值读取、新结果只含 patches。
- 策略索引只投影标签，正文数组补丁不要求读取整个原始 extra_data。
- 未执行需要外部环境变量的 MySQL、OneModel ES 和 Redis 集成测试，不据此认定已在生产可用。

### 9.2 代码导航

| 内容 | 实现 |
|---|---|
| 补丁类型、目标白名单和原子应用 | [domain/enrich_patch.go](../../internal/domain/enrich_patch.go) |
| 历史结果和临时有效视图 | [enrich/view/view.go](../../internal/enrich/view/view.go)、[Kingeye 投影](../../internal/enrich/kingeye/projection.go) |
| JSONPath 适配和资源限制 | [jsonpath/path.go](../../internal/jsonpath/path.go) |
| 规则编译/执行 | [enrich/custom/config.go](../../internal/enrich/custom/config.go)、[run.go](../../internal/enrich/custom/run.go) |
| 可复用 OneModel SDK | [onemodel/query.go](../../internal/onemodel/query.go) |
| 外部读取装配 | [enrich/assembly/runtime.go](../../internal/enrich/assembly/runtime.go) |
| 展示值转换 | [display_client.go](../../internal/enrich/datasources/display_client.go) |
| 不保存预览用例 | [enrich/preview/service.go](../../internal/enrich/preview/service.go) |
| API | [controlplane/api/http.go](../../internal/controlplane/api/http.go) |
| Console 页面 | [EnrichPreviewPage.tsx](../../console/src/web/pages/EnrichPreviewPage.tsx) |
| 离线转换 | [enrich/kingeye/convert/convert.go](../../internal/enrich/kingeye/convert/convert.go) |

