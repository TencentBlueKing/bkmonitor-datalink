# 确定性任务失败的显式恢复

状态：本次实施新增的管理契约，尚未线上发布。中心与 Worker 使用同一版本。

> 当前错误处理更新：普通消息错误按[运行容量与失败隔离](../../design/runtime-capacity-and-recovery.md)
> 记录失败后移出 Mailbox，不再默认暂停整个来源。本文暂停/恢复流程用于历史 blocked 任务。
> 创建和只读内容预览共享统一降级策略：任何文案生成失败都保留来源原文，原文为空则使用固定文案。
> 下方暂停流程描述历史 blocked 任务，不再由单纯文案失败触发。

## 暂停与诊断

历史实现中，`bkmonitor_description` 内容构建出现确定性错误时，Lifecycle 保留 mailbox 队首，Signal 不 XACK，且不保存该 Event 的处理计划或创建错误 Alert。消费 Runtime 退出后，Worker 的确切任务代次报告 `blocked=true`。中心将标记存入协调快照，停止同来源、同角色的其他副本，并禁止自动重新分配该来源角色。中心或 Worker 重启、时间推进、扩容和新 Release 都不会自动解除标记。暂时性连接失败继续走原有有界重试和调度退避。

管理 JWT 鉴权的 `GET /api/v1/runtime` 返回 `tasks[*].blocked`、`source/role/epoch/version/phase`，对应 status 的 reason 为 `task requires repair and explicit resume`。Worker 日志 reason_code 为 `task_requires_repair`；Scheduler 日志另含租户、来源、Event ID 和内容错误码，不记录完整输入或凭据。授权到期时仍保留 blocked 标记；停止未完成时不允许提前恢复。

## 恢复接口

```http
POST /api/v1/event-sources/{source_id}/tasks/{task_id}/resume
Internal-Token: <管理 JWT>
Content-Type: application/json

{"expected_source_version":7,"expected_epoch":42}
```

- 沿用来源管理的 JWT 权限边界；Worker Token 无权调用。
- 路径必须与协调快照中的来源和 task ID 精确相符，版本与 epoch 都为正数并匹配。
- 该来源角色的全部任务均须为 `stopped`，确保旧消费所有权已释放。
- 中心使用 Leader 身份与完整旧状态的 Redis CAS 解除确切任务的 blocked 和 RetryAfter。不会确认 Signal、删除 mailbox、修改 Event、生成 Alert 或重写 Release。
- 返回 `204` 表示解除已提交。规划器随后创建新 epoch，并携带 retired consumer 记录接管 PEL；不复活旧 epoch。
- 同一 stopped 代次的重复恢复幂等。已分配后续代次时，旧请求返回 `409`，不能解除后续失败。
- 未找到同来源任务返回 `404`；代次、版本或停止握手不符返回 `409`；请求不合法返回 `400`；管理认证失败返回 `401`。

多个失败任务各自持有 blocked 标记，需要逐一恢复；只要仍有一个标记，该来源角色就保持暂停。

## 操作顺序与内容边界

1. 从 runtime 和 Scheduler 日志定位失败 Event、错误码与任务代次。
2. 修复读取/模板实现或补齐稳定检测事实，并用 opening Event 的只读预览验证。修复依赖不意味着可以用最新策略替代触发时策略。
3. 确认当前发布的来源配置符合预期，完成 Worker 停止握手后调用恢复接口。
4. 核对新 epoch、PEL/mailbox 顺序以及最终 Alert.content。失败 Event 创建成功前一直保留；已经保存的 EventPlan 和 Alert.content 不重新生成。

Lifecycle 的普通丰富与内容构建均使用当前任务加载的来源配置，积压 Event 不再读取接入时的历史 Release。内容配置 Reader 只解析历史快照 `strategy.items` 查询中实际使用的 `unit`、`metric_id`；未使用的 `index_set_id` 等扩展字段可为空字符串，不因声明模型的类型约束阻断内容构建。实际使用字段、算法、租户和版本绑定仍严格校验。

所有待处理 Event 共用当前任务运行时及 `lifecycle.concurrency` 的消费预算，不再为旧来源版本独立创建连接。原接入版本保留，实际丰富版本记入 `enrich_source_version`；租户隔离、同告警 mailbox 顺序和已提交结果复用不变。

普通丰富和文案均从 `alarm_strategy_revision` 读取同一不可变快照：查询条件为
`bk_tenant_id + strategy_id + strategy_revision`，其中历史表的 `strategy_revision`
对应 Event `labels.strategy_version`。不按事件 `bk_biz_id` 过滤策略管理业务；全局策略的
管理业务取自唯一历史记录，事件业务关系仍由 Processor 校验。缺失或歧义均返回明确错误，由普通丰富记录失败诊断、文案入口执行统一降级；不查询 SplitRecord、revision head、current/latest，也不读取其发布状态。
单次读取共享 5s Context 预算，SQL 端限制快照不超过 4 MiB，最多取两行以检测歧义。

Reader 核验 schema v1、原始 canonical bytes 的字节数和 SHA-256，并核对表列与
`strategy.id/bk_biz_id`、`strategy.runtime_config` 中的租户和执行策略 ID 绑定。
`strategy.runtime_config.metadata/spec` 提供声明式上下文，`strategy.items` 提供该版本
执行查询与算法。缺少 runtime_config 的旧快照不回查当前表补齐。标准 Event 尚无可信
item 身份，多 item 文案快照明确拒绝，不能任选首项。普通补全的模板名称仍从租户作用域
的展示元数据表读取，不借它反查或替换历史策略定义。

显式提交的自定义 content 优先于自动文案，允许空串；失败或 skipped Processor 的补丁不参与覆盖。

普通文案统一降级，不再按错误码白名单决定是否创建告警：

- 对合法 opening Event，构建器的任何返回错误（包括未知算法、事实/单位错误、快照绑定/损坏、
  依赖失败）、panic、空白输出、超过 1 MiB 或非法 UTF-8 的输出都只影响展示。
- 来源 `Event.content` 非空白时逐字保留为新 Alert.content，诊断模式为 `source_fallback`；
  来源为空、纯空白或编码不可用时使用固定文案“告警已触发，详细内容暂不可用。”，模式为 `default_fallback`。
  `source` 模式的空内容也使用此兜底。Event 原文和检测判定均不改写，不读取最新策略补造事实。
- Lifecycle 以 WARN 记录租户、来源及版本、Event ID、有限长度原因码和降级模式；
  不记录原文、panic 值或任意依赖错误文本。只读预览返回同样的 `content_diagnostic`，页面显示降级原因。
- 降级内容经 Alert 校验并冻结进 EventPlan；重投不重算。后续同级触发和 `update_current` 升级按本次 Event.content 更新，恢复、关闭及低等级抑制保留已有内容。

事件身份、选中 triggered 判定与 opening Alert 的绑定在构建器外独立检查，非法输入不降级为合法事件。
原始来源内容本身超限属于非法 Event；来源编码不可用时不改写 Event，Alert 使用固定文案。
整个 Worker Context 取消或到期时不冻结兜底、不推进 mailbox，保留事件按控制协议重试；
文案子操作自行返回超时、但 Worker Context 仍有效时正常降级。存储、策略准入、队列所有权等
内容生成以外的失败继续遵循各自处理规则。显式 `ErrControlPlaneUnavailable` 表示运行时控制面传输故障，保留事件重试，不将依赖不可用冻结为正常内容。

普通指标名称优先使用同版本 `spec.name`，为空/空白时使用 `alias_name`；两者均缺失
则按上述内容策略降级。当前发布 Spec 未声明 `metric_name`，不从当前指标目录补造历史名称。
普通指标聚合方式优先使用 `strategy_item.agg_method`，缺失时使用首条声明查询的
`agg_method`。`field_tag=derived_metric` 的聚合语义属于指标定义，名称不再额外套聚合前缀。
历史算法（包括 `YearRoundAmplitude`、`YearRoundRange`）有完整冻结条件时仅描述条件和当前值，
缺少条件时沿用来源/固定文案，不伪造 History 或命中方向。自定义 Severity 已可用于生命周期，但事件没有可靠的数字算法级别映射，
文案按 `severity_mapping_missing` 降级，不将 Severity 优先级或代码后缀当作数字算法级别。

`ProcPort` 从 `values.value`（0/1）、`dimensions.display_name/nonlisten/not_accurate_listen/bind_ip`
生成进程不存在、端口未监听或绑定 IP 不符的描述。端口维度按有界 JSON 整数数组字符串读取，
仅接受 1–65535；缺失、非法或当前点无法解释窗口触发时按上述进程事实错误降级。
`OsRestart` 使用已验证的 triggered 判定和当前 uptime 秒数陈述重启时间，不重算未传递的历史点；
当前 uptime 不在 (0,600] 时按 `detection_evidence_missing` 降级。
专用逻辑指标仍需单查询、单选中且类型匹配的算法，拒绝误用于 Threshold 的改写查询。

无数据文案内部的标记/等级冲突遵守同一降级策略，不放宽事件的来源与租户身份校验。
历史快照缺失时普通丰富仍带失败诊断，文案可保留非空来源 content；不将缺失解释为
SplitRecord 未发布并重试当前表。已冻结 Event/Alert 不会后台重补，同指纹后续事件
仍按既有生命周期规则处理。此变更不回填既有失败事件或告警。
恢复接口不改写这些数据。相同来源配置的 PUT 会被发布服务去重，不能用它替代本接口。

## 已落库计划的幂等比较

EventPlan 可保留原始业务 JSON，而 Alert 读取可能已将 JSON 规范化。恢复时，`extra_data` 与 `enrich` 按规范 JSON 比较，键顺序与无意义空白不构成身份冲突；数值、数组顺序及实际内容仍须一致。租户、来源、告警身份、revision、目标绑定和其他业务字段保持严格比较，既有投影 ACK 等完成元数据继续不参与业务快照比较。比较不修改冻结计划或已落库告警，也不把规范化重新放回每次 Event 的处理路径。
