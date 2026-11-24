# 策略流转业务事实

状态：已实现本地改造，尚未部署或验证在线数据；2026-10-12 用户确认只处理后续新记录。

`AlertLog` 信封继续使用租户、真实 AlertID、操作类型、操作人类别和 UTC 时间；`params` 保留 JSON 结构，流转页面不展示原始 JSON。

以下为已存在的 v1 记录格式，后续新记录按本文 v2 约定生成：

- 屏蔽/解除屏蔽：`display_version=1`；`bindings` 是真正增加或解除的绑定，`before_bindings/after_bindings` 保留完整有界前后状态。绑定中的 `display` 包括策略名称、时区、最多 64 个具体生效时段；保留准确策略版本、方式、手动操作人和原因。手动绑定的 `request_operation_id` 对应 KAC 固定命令身份。
- 汇总抑制：准确 `policy`、`policy_display`、来源 EventID、等级与窗口；绑定真实主 AlertID。`policy_display` 含策略名、秒单位窗口、最多 32 个分组字段及其发布时名称。没有真实 Alert 的防抖事件仍只写 Event 诊断。
- 合并等待：准确 `policy` 与 `policy_display`，秒单位窗口长度、原截止时间及窗口 ID。
- 合并控制：`display_version=1`、`change_kind`、`merge_role`，准确策略版本/展示事实、主告警身份/标题、成员数量以及窗口/关系/操作身份；关系结束保存 `parent_ended` 等确认原因，系统恢复保存 `merge_members_ended`；窗口释放区分 `conditions_not_met/window_missing/member_not_selected/window_released`。

2026-10-12 后续确认：不可变策略发布永久保留。后续新日志使用 `display_version=2` 与 `policy-flow-log-v2` 身份，只保存准确策略版本引用和当次执行事实；策略名、时区、分组字段及发布时字段名称、配置周期、配置中的屏蔽原因改由 KAC 接口读取准确发布版本，不读取当前配置。上述 v1 结构说明保留用于已有日志和待输出意图。

v2 屏蔽绑定不输出 `display/reason`，实际命中的 UTC 时间段保存在绑定的 `periods` 中；手动操作人、依赖主和前后绑定集合仍保留。v2 合并日志不输出 `policy_display`，保留实际等待时长/截止时间、关系、角色、成员数量、当时主告警标题和原因。v2 汇总抑制不输出 `policy_display/suppression.display`，保留窗口和裁决。屏蔽三个绑定集合、抑制两处策略引用本次不压缩。

运行状态不再复制策略静态展示信息：屏蔽绑定直接保存 `periods`，合并等待/关系和抑制裁决仅保留策略引用及执行事实。合并关系仍固定父告警标题，日志输出仍随业务 CAS 固定，不修改已冻结的流水参数。写入失败不重新匹配策略或计数，控制面输出重试不重新读取关系。旧日志、绑定和待输出意图不回填；旧待输出意图使用原身份和内容。

KAC 按这些事实生成可读主流程，Linkd 策略引用不能当作 KAC 快照身份。快捷屏蔽成功节点只来自 AlertLog，本地操作审计和人员通知由 KAC 保留。

后续新发布可在 Release 的 `origin` 固定 KAC 快照引用。流转查询由 AlertLog 的精确版本读取来源，KAC 核对策略类型、ID、快照内容中的租户及名称后生成旧页面的 `data-strategy-id` 跳转，并返回 `policy_snapshot_id/content_params`。快照不可用时仅显示名称，不猜测最新快照。关联不重复写入 AlertLog，重复绑定/策略引用仍保持；已有 Release 不回填。

## v3 参数布局

当前处于研发测试阶段，新策略日志与新屏蔽/合并变更直接生成 v3，不提供输出格式切换配置。已持久化变更仍按自身 v1/v2/v3 版本、原身份和事实输出；既有数据后续统一处理。

v3 params 的共同字段为 display_version=3、content_key、content_params，以及原来实际存在的 operation_id、request_operation_id、event_id。其余原动作事实全部移入 content_params；只保留一个位置，不复制 content/content_format 或静态策略配置。准确 policy.id/version/digest、绑定集合、实际时段、窗口/关系、角色、父告警/成员数及 reason_code 仍按原产生点冻结。

| content_key | operation_kind | 动作参数 |
| --- | --- | --- |
| linkd_shield.v1 | shield | bindings、before_bindings、after_bindings 和实际 reason_code |
| linkd_unshield.v1 | unshield | 本次实际解除集合及 before/after，不把部分解除变成全部解除 |
| linkd_policy_suppress.v1 | suppress | policy、suppression、severity、reason_code |
| linkd_merge_wait.v1 | merge_wait | policy、原 policy_id、window_id、duration_seconds、deadline、change_kind、reason_code |
| linkd_merge_member_link.v1 | merge | 原 policy、主告警/成员数、角色、窗口/关系与原因 |
| linkd_merge_parent_ready.v1 | merge | 原主告警生成事实 |
| linkd_merge_member_unlink.v1 | merge_end | 原关系结束事实 |
| linkd_merge_release.v1 | merge_release | 原等待释放事实及已有 duration_seconds |
| linkd_merge_parent_recover.v1 | recover | 原主告警恢复事实及 merge_members_ended |

v3 流水使用 policy-flow-log-v3 身份命名空间。AlertPolicyChange.LogVersion 固定原输出；AlertMergeChange.LogParams 按保存事实重放。EventPlan.Logs 只在内存中，失败重试会重新准备，不提供跨重启或跨配置切换的持久冻结保证。旧 Event 已部分追加日志、结果尚未完成时，切换格式可能产生两个版本的同义日志。2026-11-13 用户确认已写入旧数据后续统一处理，本轮不新增 Event 级输出版本或暂停接入前提；切换期间旧 Event 可能留下的跨版本同义记录也在后续处理。回退格式有相同边界。新格式不更改策略裁决、窗口、计数、绑定或投递条件。
