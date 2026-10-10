# Linkd Console 运维调试工具

Console 是仓库内独立构建的运维控制台。默认本地模式仅监听回环地址；服务模式要求 Basic Auth，
部署方式见 [Helm 指南](helm.md)。基础设施与实体查询保持只读，来源配置及主动关闭告警通过正式控制面 API 执行。
它读取 Linkd YAML 与控制面的动态来源配置，使用 Node 连接层查询
Prometheus、Kafka、Redis 和权威 Repository；Linkd 进程不增加中间件诊断查询 API。

ES 支持范围和部署注意事项见 [Elasticsearch 版本兼容](elasticsearch-compatibility.md)。

完整启动参数、环境变量、接口与安全边界见 [Console README](../../console/README.md)。

深浅主题选择保存在当前浏览器的 localStorage，刷新页面或重新打开同站点后恢复上次选择；首次访问默认深色。
浏览器禁用本地存储时仍可切换主题，但选择仅在当前页面内有效。

## 感知模型

```text
Linkd YAML ───────────────→ 配置期望
Prometheus ───────────────→ 进程与历史处理趋势
Kafka Admin / Redis XINFO → 当前 partition、lag、PEL、Mailbox
MySQL / Elasticsearch ───→ Event、Alert、AlertLog 明细与统计
```

总览只展示当前已经实现的处理链：

```text
EventSource / Kafka partition
  → Cleaner transform
  → Event store
  → Redis Mailbox + Signal
  → Event Enrich / Lifecycle / 抑制、屏蔽、合并与处置准入
  → Alert / AlertLog / 普通来源 Hooks / 持久输出意图
  → 控制面策略任务 / KAC 兼容 ES 写入 / 获准动作可靠通知
```

Kafka 和 Redis 页面展示请求时的当前快照；历史趋势只来自 Prometheus，不通过 Console 自建时序存储。
Prometheus 图表页面统一提供 15 分钟到 7 天的查询时间范围，默认 1 小时；采样步长随所选范围调整。
页面另行提供独立的“计算窗口”，默认 1 分钟，并直接用于速率、增量与直方图分位计算。

Control Plane 使用“运行概况 → 全量任务列表 → 任务详情”。目录和生效参数来自控制面
`GET /api/v1/control-plane/tasks`，受管理 JWT 保护；Console 通过 `/local-api/runtime/control-plane`
代理读取，浏览器不接触 token。需要同时更新控制面和 Console；接口缺失时明确报错，不从 Console YAML
推断任务启停。目录覆盖来源调度、Provider、ES/Redis 维护、策略索引、动态配置、屏蔽/合并、可靠投影/
动作及 KAC 兼容索引维护等任务，管理 API 作为常驻服务单独展示。未启用项目保留在列表并解释原因；
支持搜索、分组和需要关注筛选。

运行详情以当前响应进程为准，显示 owner、执行状态和有限子流程结果；执行情况区分本次启动累计与
Prometheus 时间窗数据，生效配置只包含显式挑选的无凭据预算。统一刷新每 15 秒读取任务与动态配置状态，
可暂停；刷新失败保留有时间戳的旧快照。刷新不触发对账、归档、裁剪或重试。

任务生命周期、执行结果和指标采样分别判断。缺少 Prometheus owner 映射显示“观测不完整”，不改写控制面
已经确认的运行状态；只有能按 `serviceInstanceId` 关联到当前 owner 的历史指标才进入详情，无数据保持未知。
其他 owner 的活跃指标仍参与重复部署提示。周期任务只在存在明确整轮观测预算时推导逾期；连续归档的空闲
间隔不作为执行超时，常驻 API 不要求周期成功记录。控制面共享任务监督，调度中心通过 Redis 租约保持独占。

调度子流程包含发布恢复、Kafka 探测和分配；Provider 统计完整 Pull/Apply，动态配置统计读取、校验、持久化
和发布完整轮次，并展示现有 Worker 配置应用状态。策略索引展示目标发现、初始化和各目标刷新结果，逐策略
状态从已有策略索引页下钻。目标失败不会被其他目标成功覆盖；没有 Hook 目标是正常空闲。
每项任务最多保留 64 个子流程的最近结果；Redis 每轮最多展示 16 个来源的一页扫描结果，不表示全部来源健康。
当前状态是内存快照，重启会清空累计计数；没有新增持久化执行日志。Archiver backlog 仍来自 Console 所配置
同一部署的固定 Active alias 只读 `_count`，连接未配置时保持未知。

## Event 抑制记录

Event 结构化详情中的“事件抑制记录”展示 `_processing.policy_decision.suppression`，区分活动 Alert
绕过、没有触发判定、未命中、跳过、候选占位、被抑制和通过门槛。每个 severity 独立显示结果；防抖
显示当时的次数/阈值、窗口秒数和判定时间，聚合显示当时的候选或关联主、固定起点与截止时间。
策略链接保留租户和准确发布版本，关联主链接进入相同租户的 Alert 查询；裁决证据保留完整原始步骤。
步骤在浏览器按 16 条分页，翻页不重新执行策略。缺少计数或未知结果不会显示成零或正常通过。

这里是 Event 保存的历史事实，刷新只重新读取 Event，不读取或修改当前 Redis 计数、不延长聚合窗口。
被抑制 Event 不代表主 Alert 被抑制；候选占位也不证明主 Alert 已放行。当前缓存查询见下述抑制运行态。
组件和模拟 API 浏览器测试覆盖边界显示、租户/版本跳转、刷新和无写请求；独立真实 Console 用例
另验证 ES/MySQL Event 的丰富结果、已保存防抖裁决及原始内容，见下文真实联调说明。

## 告警策略与只读匹配

“核心数据 → 告警策略”（/policies）按明确租户查询事件抑制、告警屏蔽和告警合并。
配置由 KAC 编辑、启停和删除；Console 只读取配置、精确发布版本，以及调用只读预览。
列表每页最多 8 条，可按配置启用状态筛选；筛选后的空页仍可能有下一页，不表示已经扫描完成。
租户、类型、筛选、策略 ID 和查看版本保存在地址栏，切换查询范围会重置分页与选择。

详情区分编辑 revision、已发布 published 和 pending；可展开待发布快照、规范配置及编译摘要。
查看历史版本不回退到最新版本；配置对比按顶层字段列出变化，并展示两份完整 Spec。
“已发布”只表示配置可读取，不代表全部 Worker 已应用，也不代表当前时间/对象一定命中策略。

匹配预览可选择 Event ID、Alert ID 或完整领域 JSON，使用已经保存的丰富结果，不自动重跑 Enrich。
等级留空按输入返回，判定时间留空使用当前时间；指定时间遵循页头 UTC/本地时间选择，切换显示时保留同一绝对时刻。
临时策略 JSON 仅用于本次预览，不保存配置。依赖屏蔽可以单独匹配 rely_policy，并显式指定同租户
主 Alert 作为关系查询起点。跨租户样例、重复 JSON 键和超限载荷在发送前拒绝。

结果按等级、条件组和条件 ID 展示，并保留原因码。无法求值与普通未匹配分开显示。
mode=matching_only 不读取运行计数或占窗口，不证明防抖阈值已达到、屏蔽关系已建立或合并已成功。
修改输入、取消、切换策略/版本时取消旧请求，迟到结果不会绑定到新输入。

浏览器只请求同源 /local-api/policies 路由，管理 JWT 在 Node 层按请求签发；不代理策略写入方法。
代理逐项核对租户/类型/ID/版本及 pending 归属，保留空筛选页的原始游标，不返回后端原始错误正文。
每进程最多两个并发请求，预览 body 最多 3 MiB；响应分别限制为列表 64 MiB、单记录 9 MiB、
单 Release 4 MiB、预览 2 MiB。超时使用配置的查询期限且不超过 15 秒，断开时向上游传播取消。

当前页面已有组件、代理和模拟 Chrome 测试；真实双后端 Console 用例额外验证已发布防抖版本与
真实 Event 的只读匹配。三类运行态查询、显式检查、投影/动作自动任务及指标页面均已接入。
状态序列模拟、逐策略执行观察和可选 KAC 告警跳转已补齐，使用方式见本页对应章节；真实 KAC
策略同步与处置接入仍未完成。
当前能力和剩余差距统一见[开发方案](../design/event-enrich-and-alarm-policies.md#112-当前能力与剩余差距2026-10-08)。

### KAC 配置入口

策略详情提供“在 KAC 管理此类策略”。入口按租户、策略类型显式配置，不从控制面地址推导 KAC 域名。
Console 环境变量 `LINKD_CONSOLE_KAC_POLICY_LINKS` 接收 JSON；例如只配置三类列表入口：

```json
{
  "tenant-a": {
    "suppression": "https://kingeye.example.com/#/kac/alarmRestrain",
    "shield": "https://kingeye.example.com/#/kac/alarmShield",
    "merge": "https://kingeye.example.com/#/kac/alarmMerge"
  }
}
```

也可使用 `{policy_id}`、`{bk_tenant_id}`、`{space_code}` 占位符，每个值独立 URL 编码。
确认 KAC 与 Linkd 使用同一个策略 ID 后，可配置对应的编辑入口：

| 类型 | KAC 当前编辑路由（域名与部署前缀需按实际填写） |
| --- | --- |
| suppression | `/#/kac/editAlarmRestrain?mode=edit&id={policy_id}` |
| shield | `/#/kac/editAlarmShield?mode=edit&id={policy_id}` |
| merge | `/#/kac/alarmMerge/edit?id={policy_id}` |

以上路由来自 Kingeye `1874066858` 的 `src/web/src/projects/kac/src/router/index.js` 及各列表的
`editStrategy`；它们不是 Linkd 推断出的策略映射或统一租户切换协议。KAC 继续使用自己的登录会话、
租户与权限，进入后核对当前上下文。查看 Linkd 历史版本时，入口仍指向 KAC 当前配置；不会带上历史
version，也不会修改历史快照。修改后回到 Console 刷新并核对发布结果。

只配置已有的租户和类型；未配置或缺少占位符所需信息时不生成链接，不回退到其他租户。链接在新窗口
打开，不携带 Console 的 Referer 或认证信息。`GET /local-api/policy-links` 只返回请求租户和类型的
结果，不执行 KAC 请求或写入；查询支持 `bk_tenant_id`、`type`、可选 `id/space_code`，禁止额外参数。
读取失败提供独立重试，切换策略/租户立即清除旧入口；“刷新策略”同时重读当前入口。

最多 64 个租户，原 JSON 最多 64 KiB，每个 URL 模板及生成 URL 最多 2048 字符。只接受完整 HTTP(S)
页面地址，占位符不能位于协议或域名；拒绝未知占位符、userinfo 和含 token/password/secret 等凭据的
查询键。配置错误在启动时失败，错误消息不回显配置值。该映射不放入 Linkd Go 配置，变更后重启 Console。
Helm 使用 `console.kacPolicyLinks` 传入同一对象，仅注入 Console 容器。

本地验证覆盖配置加载、租户隔离、占位符编码、错误/空状态及 Chrome 三类跳转、历史查看和新窗口
隔离；浏览器目标页为拦截的测试页面，未验证实际 KAC 部署的登录或编辑权限。

## 抑制运行态

“核心数据 → 抑制运行态”（/suppression-runtime）按租户和方式查看防抖计数、关联聚合窗口及当前保留成员。
支持策略、登记主、来源过滤和精确运行 ID；来源过滤匹配防抖来源或聚合主来源。每页最多四个原始登记，
筛选或已到期记录可形成空页，仍保留下一页入口。所有策略、Alert、Event 链接保留租户，策略链接固定版本。

详情分开显示观察时间、代次、物理保留期、当前滑动计数或固定聚合窗口；零计数正常展示，缺失/损坏响应
显示错误。登记主不等于当前仍活动，候选未确认时不显示为已放行主。成员集合可能随 Redis 丢失，不代表
完整事件历史；成员换代/游标失效后须刷新详情。

刷新显式使首页、成员和请求历史缓存失效，重读当前快照并回到第一页，避免显示旧计数。
读取不会清零计数、强制释放或改写 owner。请求范围、游标和运行上限见
[抑制查询契约](../reference/contracts/policy-runtime-api.md#抑制路由和查询范围)。

详情中的“受控对账”要求填写原因，提交时固定当前 owner 和代次，由后台在正式身份租约内复核真实 Alert。
未绑定防抖、有效活动 owner 和尚在期限内的聚合候选占位保留；只有失效的原 owner 登记可条件删除。
窗口换代则原命令失效，不追随新 owner，不重新计数或补发处置。请求接受与最终删除分开显示。
认证操作者由服务端提供，浏览器不能指定；同一个不确定命令跨刷新保存在 sessionStorage，重试沿用原
owner、代次、原因和 operation ID。请求 pending 时等待后台结果；失败结果保留，新的检查使用新命令。

窗口消失后仍可从同一 URL 查询独立请求历史和精确结果，不能用当前 404 否定已保存的操作。
窗口、成员、对账请求共用两路代理并发及最多 16 条等待，断连取消且等待计入超时。
终态自动清理的逐窗口明细在独立历史页，显式对账结果在原窗口的请求历史中；具体边界见
[抑制受控对账](../reference/contracts/policy-runtime-api.md#抑制受控对账)。
模拟浏览器覆盖分页、零值、故障和刷新边界；真实 ES/MySQL Console 浏览器用例另验证当前防抖计数、
跨来源聚合 owner/成员与实体链接、未绑定计数的显式对账保留，并核对计数及实际处置消息没有增加。

## 合并运行态

“核心数据 → 合并运行态”（/merge-runtime）分别查看临时窗口、持久化裁决和 Alert 父子关系。
填写租户后查询；关系页还需填写 Alert ID。支持策略 ID、裁决阶段筛选，以及精确窗口/裁决/关系 ID。
列表每页最多四条，过滤空页仍可继续翻页；切换查询范围会重置游标。

窗口展示已提交/尚未确认的成员、首次条件组命中数、截止时间与倒计时。窗口消失可能来自清理、到期
或 Redis 数据丢失，不能据此认定合并失败；已持久化裁决和 Alert 等待引用需分别检查。
首次条件组命中数不代替正式裁决中的实时 Alert 复核。

裁决页面把窗口结果、执行阶段、快照捕获进度、成员处理进度及清理确认分开展示。
父 Event、父 Alert、关系及成员都有带租户的跳转；没有父 ID 时不会生成预计链接。
固定成员表是捕获时的状态，“查看冻结快照”单独读取完整的首次 Alert 数据和丰富结果，供核对模板输入；
点击 Alert 链接查看的是当前状态，两者不能混用。刷新会重新读取列表、详情和成员页；失败结果不会继续展示成有效快照。

关系页保留 ended 历史、解除原因和处理进度。父关闭只解除关系，子真实生命周期与 admission 独立；
历史成员的 linked 标记不代表当前仍受该关系抑制。Alert 详情可从“查看合并关系历史”进入同一查询。

持久裁决详情提供“请求接续裁决”，关系详情提供“请求检查关系”，填写原因后提交原控制点版本。
后台复用正式步骤，可能继续创建主告警、按原准入规则释放成员或更新关系；不会重新裁决已冻结的业务结果，
也不会更换成员/模板创建另一个父告警。业务 outcome=failed 表示条件未满足，不是可以通过重试改成成功的错误。
父人工关闭后的关系检查仍仅解除关系，子告警保持真实状态，等下一条触发才判断处置。

控制点与历史列表分开读取。后台在窗口租约内比较原版本，自动任务已推进时返回“原版本已变化”；
不能让旧命令追随新版本。页面区分本次操作结果和整个任务完成，展示执行前后阶段、是否调用正式步骤及原始诊断。
原任务已完成时禁用新的接续按钮；查询历史不依赖 Redis 窗口仍存在，控制点读取失败时仍可查原操作。
认证操作者由 Node 服务端提供，管理 JWT 不下发。响应不确定时 sessionStorage 保留完整命令，刷新后重试同一操作；
已保存的失败或版本失效结果不被覆盖，新的检查使用当前控制点和新的 operation。

刷新重读列表、详情、成员和控制点，并把操作历史重置到首页；各读取共用两路代理并发及 16 条等待，
断开/取消传播至控制面。页面不提供强制合并、直接删关系或修改 admission 的入口。
路由、结果字段和预算见[合并受控接续与关系检查](../reference/contracts/policy-runtime-api.md#合并受控接续与关系检查)。
模拟浏览器覆盖分页和边界；真实双后端 Console 浏览器另验证待裁决窗口、完整冻结快照、父关闭后的
结束关系及子 Alert 状态，并真实提交活动关系检查得到 no_progress，核对持久操作者和原因。
浏览器结束后通过真实事件和 Kafka 核对下一条触发才放行；未知响应后原命令重投由独立模拟浏览器验证。

## 屏蔽运行态

“核心数据 → 屏蔽运行态”（/shield-runtime）按租户查看当前绑定和待完成状态输出，支持策略 ID、
固定依赖主 Alert ID、时间/自定义依赖/CMDB 依赖类型筛选。填写精确 Alert ID 可查询已解除或终态记录，
Alert 详情中的“查看屏蔽关系与历史”会保留租户并打开对应详情。

列表包含尚未到复查时间的绑定；每页最多四条原始候选，筛选后空页仍可继续翻页。详情分开展示真实
生命周期、屏蔽状态和历史放行记录，绑定卡片显示策略版本、来源 Event、固定主告警、绑定等级与时间。
所有实体链接保留租户。计划复查时间只表示后台调度依据，解除屏蔽也不意味着自动放行处置。

屏蔽历史只显示实际建立/变化/解除流水，可展开查看前后绑定和原因。每次最多扫描四条原始日志，
非屏蔽日志形成的空页仍保留“下一页历史”；历史游标过期后可重新读首页。页头刷新重新读取列表和
当前详情，并将历史查询重置到首页，避免使用旧 ES 快照漏掉新变化。已解除记录不一定留在当前列表中。
仅复查时间推进不会生成一条屏蔽变更流水；终态清理仍保留解除记录。待完成输出意图单独显示，
它不代表处置待补发，也不证明 KAC 已收到可靠投影。

详情额外展示最近复查时间、观察版本、结果和逐绑定原因；没有记录与检查成功分开。
“部分条件未能检查”表示相关绑定仍保留；失败结果可能包含已保存的部分变更，应结合当前 Alert 查看。
填写复查原因后可提交异步请求，版本取自当前详情，操作者取自 Console 认证。排队不代表已解除；
页面轮询该请求的完成、失败或失效结果，并保留请求历史。版本变化时刷新详情后重新提交。
提交结果不确定时会保留原操作 ID、版本和原因，包括重新打开页面；使用“重试同一复查操作”确认结果。
完成后才能准备新的复查。解除仍等待下一条触发 Event 判断处置，不提供强制解除按钮。
同一页面的绑定、历史和复查请求共享两并发代理及最多十六条等待，避免一次刷新触发自身限流；
队列等待计入总超时，离开页面会取消尚未发送的查询。真正满载时仍显示繁忙结果。
最新诊断完整保留原绑定复查与候选规则评估，本地每页显示 16 步。原绑定检查失败显示“保留关系”，候选评估失败显示“跳过候选”；解除旧绑定后评估其他时间屏蔽仍使用既有匹配逻辑。
最新诊断的触发来源区分“定时复查”“事件提示复查”和“手动复查”；只有手动请求有请求 ID 与操作者。
Control Plane 中的“屏蔽事件提示”展示订阅子步骤与最近分页检查；订阅失败不会停止“屏蔽定时解除”。
提示队列满、断连或重复不会生成强制解除，实际仍重读原绑定和主状态，解除不补发处置。

管理 JWT 留在 Console 服务端，具体游标和预算见[复查契约](../reference/contracts/policy-runtime-api.md#屏蔽复查与请求)。
Chrome 测试验证只读请求、租户链接、空历史翻页、刷新、深浅主题与 850px 布局，使用模拟 API。
真实双后端依赖屏蔽 E2E 独立验证故障 partial 等管理行为；真实 Console 浏览器用例另通过时间屏蔽
提交并读取持久化 retained 复查，核对没有额外处置。完整执行方式、隔离清理和截图路径见
[Console 真实浏览器验收](../../tests/e2e/allinone/README.md#console-真实后端浏览器验收)。

## 抑制清理历史

“核心数据 → 抑制清理历史”（`/suppression-cleanups`）查看独立持久记录，抑制运行态页提供同租户跳转。
支持来源、fingerprint、Alert、Event、记录阶段、窗口方式/ID/代次筛选和精确清理 ID；
指定窗口必须同时指定方式，指定代次必须同时指定窗口。每页四条，按稳定 ID 扫描，不按时间倒排。
过滤后空页仍可继续，刷新返回首页并重读详情；详情可跳转到原终态 Alert 或未生成 Alert 的恢复/关闭 Event。

“诊断已保存”不代表 Redis 清理均成功。两类结果分别显示本轮删除登记数、未发现可清理登记、
Redis 不可用或不适用；pending 显示结果未完成，不能推断清理未发生。
此前未完成的调用被重试时，页面保留“此前清理结果未确认”提示，避免把重试的零删除当作首次零删除。
详情按防抖、聚合分别展示已确认清理的窗口和代次，每次显示最多 16 项，完整 JSON 按需展开。
元信息已丢失的防抖残余引用显示代次未知，不能按猜测代次筛选；这些明细来自原子删除结果。
本页仍为只读；显式对账从抑制运行态详情提交，结果在该窗口的独立请求历史中查看。
字段、故障语义与管理预算见[终态抑制清理历史](../reference/contracts/policy-runtime-api.md#终态抑制清理历史)。

## 告警投影任务

“核心数据 → 告警投影任务”（`/projection-tasks`）使用正式管理 API 查询部署专属的持久任务。
填写租户后，可按 Alert、目标、来源或任务状态筛选，也可直接指定任务 ID；每页最多四条，筛选后的
空页仍可继续。刷新返回首页并重读当前详情，Alert 详情提供带租户与 Alert 条件的跳转。

详情区分冻结的 Alert 业务版本与任务 CAS 版本，展示来源发布、重试轮次/累计次数、安全错误码、
远端确认及最近一次人工恢复。冻结快照按需加载，跳转“当前 Alert”才查看后续业务变更。
`delivered` 表示远端确认已保存但本地水位待 ACK；`succeeded` 只表示本任务版本同步完成，
不表示所有后续版本或处置动作已完成。最近恢复记录不是完整的操作历史。

失败任务可填写原因后“恢复原任务”。服务端保留原快照、业务来源版本和累计次数，仅重置一轮自动尝试。
浏览器保存原 CAS、操作 ID 和原因；响应不确定时，刷新页面后仍可“重试同一恢复操作”。
操作者由 Console 服务端提供，浏览器不能伪造；冲突后需读取当前任务重新决定。

控制面配置 Lifecycle 并启用全局 `plugins.kac` 后自动处理已有任务；在“控制面任务”中检查
kac-index-maintenance、projection-producer/projection-delivery 是否启用和运行。
Linkd 直接维护兼容 ES 文档，不调用 KAC 状态投影接收端；索引未就绪时保留同步待办。
页面接受恢复不代表已经写入或已完成同步；旧 KAC Kafka Hook 不在此任务列表。协议、容量和错误语义见
[投影管理契约](../reference/contracts/kac-alert-projection-v1.md#管理查询与人工恢复)。

“查看投影运行观测”按需展开七个进程面板：运行/执行中实例、扫描页结束速率、P95 耗时、工作观察
结果、最近页数量、最大年龄和观察距今。按进程 instance、时间范围和计算窗口查询，收起会取消读取；
不把租户、Alert 或任务筛选传给 Prometheus，刷新指标不推进任务。

生产阶段的 advanced 表示创建或复用任务，投递阶段表示已完成本地 ACK；复用失败任务也不表示已经
恢复发送。页数量和最大年龄不是全局积压。失败扫描不会更新页观察时间，必须结合“观察距今”阅读。
无时序不能解释为零或已同步；查询失败时隐藏旧图表，部分面板不可用仍保留其他结果。指标不改变下方
任务详情中 delivered 与 succeeded 的区别。接口与共用预算见[投影运行观测](../reference/contracts/kac-alert-projection-v1.md#console-运行观测)。

## 告警动作投递

“核心数据 → 告警动作投递”（`/action-deliveries`）查询独立的 ActionDelivery。填写租户后可按
Alert、目标、来源、动作类型和进度筛选，每页最多四条，筛选空页仍可继续。冻结动作请求按需读取，
页面同时展示原 action/cause、来源发布、业务 revision、任务 CAS、请求摘要及最近一次人工恢复。

- pending 表示已入队，waiting_projection 表示等待所需投影可搜索；正常等待不是可强行重试的失败。
- succeeded 表示已投递 Celery，通知、工单和自动处置完成情况应到接收端查看。
- skipped 分为本地依据较新终态停止发送和接收端明确确认跳过；此前结果未确认标记仍保留，不能据此认定此前从未受理。
- 同目标顺序区显示可见的最早未结清动作，可跳到前序任务。failed 会阻塞后续同 Alert/目标动作；
  队首位置或暂时为空都不等于可以立即发送。

失败任务填写原因后可恢复原任务。原请求、业务来源版本和累计次数不改变；浏览器刷新后继续重试同一
未确认命令，操作者由 Console 服务端提供。成功或跳过的动作不提供重新执行入口。
Alert 详情另行展示 action_pending 和逐目标动作开关，并提供当前租户/Alert 的任务跳转；意图已清除
只表示入队待办已完成，不能解释为远端已经受理。

“查看动作运行观测”按需展开八组进程指标：运行/执行中实例、扫描页结束速率、页面 P95、工作观察结果、
最近页项目数、页面最大待办年龄、观察距今和此前结果未确认速率。使用独立的时间范围、计算窗口与
Prometheus instance 筛选；上方租户、Alert、来源和任务条件不影响这些跨业务指标。刷新只读取，收起取消请求。

最近页最多 16 项，不是全局积压，页面年龄必须结合观察距今；多个进程的页面不能相加成队列总量。
工作观察可重复统计同一任务，queued 不等于本轮新增投递或处置完成。查询失败隐藏旧图；没有时序、
非有限样本不补零。未接入或尚未启动运行器时正常显示无数据，不据此认定任务完成。

详情的“日志定位”列出发送阶段、租户、Alert 与 action_task_id，并说明补扫日志只按租户/Alert 定位。
这些是用于检索进程日志的字段，Console 尚无进程日志采集/搜索后端，也不宣称已找到对应日志。
每个失败页只有最多四条样本；队首阻塞时可沿前序任务继续定位。“Alert 操作流水”入口携带租户、Alert
和任务更新时间之前一小时的窗口，展示业务事实，不替代动作受理记录。
指标接口与预算见[动作运行观测契约](../reference/contracts/kac-action-delivery-v2.md#console-运行观测)。

控制面配置 Lifecycle 并启用全局 `plugins.kac` 后启动 action-enqueue/action-delivery；任务页
区分未启用、空闲、运行和失败。管理 API 已能查询和恢复已有任务，旧 KAC Kafka Hook
不在此列表中。完整接口与预算见[动作投递管理契约](../reference/contracts/kac-action-delivery-v2.md#管理查询与人工恢复)。

## 实体查询与关联排障

Events、Alerts、AlertLogs 采用相同的查询工作区：快捷或自定义时间、租户和实体 ID、对象专用筛选、
更多关联标识、已应用条件标签、分布统计、结果列表和详情。枚举通过下拉选择，文本字段精确匹配；
点击分布项可继续筛选。输入条件后点击“执行查询”生效；分页保持同一时间范围，变更条件返回首页。
UTC / 本地时间切换同时影响展示和自定义时间输入；查询及详情可复制链接恢复。

“本页搜索”仅匹配当前加载页的标题、正文、对象、来源或 ID，不代表全库全文搜索。
当前 ES mapping 的标题和正文未建索引，本次不增加 mapping 或全表扫描兜底。
统计与列表共用全部筛选，分别读取快照；统计失败显示不可用，列表仍可独立使用。
MySQL Alert 统计现在也应用 JSON `update_at` 时间条件，但不提供时间趋势；大范围查询仍可能超时。

- Event 列表突出来源、处理状态、处理结果与关联告警数；详情区分来源逐级判定、Lifecycle 逐级裁决和观测值。
- Alert 列表同时呈现首次发生 `begin_at`、创建 `create_at` 和最近更新 `update_at`。默认按最近更新过滤和排序，**更新不代表新建**。
- Alert 详情首屏展示生命周期时间、当前状态、对象与来源，并单独展示策略与投影状态：业务版本、屏蔽状态和复查时间、合并状态/窗口/关系引用、历史放行级别以及逐目标要求/确认水位。缺失水位不显示为零，非法或超前水位显示异常。
- 屏蔽绑定、合并等待及待完成变更可展开查看；最近放行是历史事实，解除屏蔽或父合并关系不立即补发处置。详情中的投影表展示已保存的水位，可跳转到同租户/Alert 的[告警投影任务](#告警投影任务)查询；水位表自身不修改状态。
- 随后加载关联 Event 列表和 AlertLog 时间线，支持独立筛选、分页及就地展开记录。两类查询都绑定当前租户和 Alert ID；不会只凭 ID 跨租户查找。
- 关联查询默认覆盖最近更新前的最大允许窗口（默认七天），不是完整历史保证。Event 按接收时间、AlertLog 按记录时间；生命周期更长时明确提示窗口截断，可自行查询更早窗口。首次和最近事件仍可按精确 ID 独立查看。
- AlertLog 默认使用时间线，可切换表格，展示操作、操作方、父告警、原因及 Hook 摘要；支持按时间正序或倒序查看。

### KAC alarm_event 查询

“核心数据 → KAC 告警”（`/explore/kac-alarms`）只读查询 KAC ES 中实际保存的 `alarm_event` 文档。
复用 `plugins.kac.elasticsearch` 与 `plugins.kac.alarm_event_index`，连接独立于 Linkd Repository；
浏览器不能指定其他集群或索引。只要连接和 alias 已配置即可查询，不要求启用投影/动作插件。

仅用于只读查询的最小配置如下；已有完整 KAC 插件配置时直接复用，不修改 `enabled`：

```yaml
plugins:
  kac:
    enabled: false
    alarm_event_index: cw_kac_saas_3.0_alarm_event
    elasticsearch:
      addresses: [http://127.0.0.1:9200]
```

必须填写 `bk_tenant_id` 后查询，列表、统计、详情与游标都限定同一租户。
支持精确 `alarm_id`（可不限时间）、`source_id`、`status`、`level`、`action` 与 `event_id` 筛选，
分页及时间预算沿用实体查询工作区。支持原 KAC 的告警 ID，不限于 `linkd-` 格式。
状态、来源、级别和动作分布可点击筛选；详情提供结构化字段、扩展数据与完整原始 JSON，
不提供修改或关闭 KAC 告警的操作。投影任务详情可通过“查询 KAC 实际文档”携带租户和 `alarm_id` 跳转。

时间筛选和排序使用 `alarm_time`，表示告警发生时间，不代表最近投影或处置更新时间。
原协议时间字符串按 `Asia/Shanghai` 解释；范围、分布和 UTC/本地展示使用对应的实际时刻，
完整 JSON 保留原始无时区字符串。该转换依赖现有 KAC `yyyy-MM-dd HH:mm:ss` mapping。
列表与统计分别读取快照；查询结果不等同于 Linkd 的任务确认状态，也不代表 KAC 后续处置已完成。

可使用独立只读账号覆盖 KAC ES 凭据：`LINKD_CONSOLE_KAC_ELASTICSEARCH_API_KEY`，
或 `LINKD_CONSOLE_KAC_ELASTICSEARCH_PASSWORD`（用户名沿用 KAC 配置）。
账号需具备固定 alias 的 `read` 与 `view_index_metadata` 权限；凭据仅保存在 Node 连接层。
连接缺失时页面提示配置，服务不可用时显示查询失败，不回退查询 Linkd 存储。

### 主动关闭告警

Alert 详情中的“主动关闭”要求填写原因并确认当前租户和 Alert；仅 active 告警可发起新关闭。
Console 通过管理 JWT 调用正式控制面关闭接口，不修改 Node 查询连接所指向的数据库。
Console 与控制面的 Repository 必须属于同一 Linkd 部署；功能需要同时部署包含关闭接口的新控制面与 Console。
控制面使用当前已发布来源配置执行 `CloseAlert`，完成 CAS、近期缓存、close 流水和 FinalHook；不会生成伪 Event。
来源被停用或删除后，仍可使用其已保留的最近发布配置关闭既有告警。

结果不确定时，原操作 ID、原因和时间保存在当前浏览器标签页的 sessionStorage；重新打开详情后可继续
“重试同一关闭操作”。关闭可能已经生效，不能把网络失败等同于未执行。成功后详情立即使用控制面返回的终态，
刷新列表和关联记录；ES 流水仍可能受 refresh 延迟影响。Hook 失败不会回滚告警，须查看 push 流水判断。
服务模式的操作方来自 Basic Auth 用户名，本地模式记为 `console-local`；不接受浏览器自报操作方。

协议字段、错误与重试边界见 [Alert 主动关闭 API](../reference/contracts/alert-close.md)。

### Redis Stream Manager 的 owner 指标

Redis Stream 的真实 owner 由控制面任务生命周期提供，历史
`linkd_control_plane_task_active_ratio{linkd_task="redis-stream-manager"}` 由整个来源循环在启动时记录 1、退出时记录 0。
单条 Stream 的历史执行次数和耗时继续分别累计；当前状态 API 则统计完整来源扫描轮次，包括来源列表读取失败。
因此这两类计数不能直接比较。只有成功次数而缺少 owner 指标时，页面标记观测不完整。
Redis 与 Lifecycle 均已配置时任务默认启用，`control_plane.redis_stream.enabled: false` 可显式关闭。

## 动态来源的 Kafka 查询

配置 `dispatch.url` 和 `dispatch.jwt.secret_key` 后，Console 服务端通过控制面来源列表接口读取完整配置，
再用 Kafka Admin 查询输入 topic 和各个 Kafka hook 的输出 topic。Leader、replicas/ISR 来自 topic metadata，
High/Low 来自 topic offset 查询，Committed 来自 consumer group 的已提交位点；Lag 使用整数精度计算
`max(High - Committed, 0)`。Owner 来自 Kafka consumer group 的实际成员分配，不再用调度器分区数拼接健康快照。
没有已提交位点时保持“未知”，不会补成 0；查询失败显示不可用，不回退为 `AVAILABLE`。

控制面的 `GET /api/v1/event-sources` 和 `GET /api/v1/event-sources/{id}` 默认脱敏；显式添加
`include_secrets=true` 可返回含认证材料的完整记录，响应设置 `Cache-Control: no-store`。
两种读取均需要管理 JWT，worker token 不能访问。此参数只由 Console 服务端使用，
浏览器侧来源管理代理不转发该参数，运行状态响应也只包含查询结果和脱敏配置摘要。

Console 必须能够访问 Kafka bootstrap 地址及 broker 的 advertised 地址，并拥有 topic/group 的查询权限。
使用 SASL/TLS 时沿用完整来源配置；如果 TLS 材料使用文件路径，需把对应文件挂载到 Console 可读取的同一路径，
或使用内联 PEM。仅更新 Console 而未更新支持完整配置读取的控制面，无法获取受保护 Kafka 的真实凭据。
同一 Console 的并发刷新共享本轮查询，每轮最多四个 Admin 连接；请求使用配置的查询超时并关闭自动重试。
本次查询只使用 Admin 读取接口，不消费消息或提交 offset。

## 指标边界

「系统 → 指标目录」（`/metrics/catalog`）展示当前控制面二进制包含的指标定义，支持按功能模块、
指标类型、用途过滤，以及按中文名、OTel 名、Prometheus 名、维度和描述搜索。点击指标名可展开
完整统计口径、维度说明和可复制的查询序列。筛选条件保存在 URL 中，可收藏或分享同一查询。

目录由控制面 `GET /api/v1/metrics/catalog` 提供，经 Console 的 `GET /local-api/metrics/catalog`
代理读取。配置 `dispatch.url` 与 `dispatch.jwt.secret_key` 即可使用；不要求配置 Prometheus，也不要求
开启 `telemetry.metrics`。管理 JWT 只留在 Console 服务端，worker token 无权读取此接口。

```bash
curl --fail --silent --show-error \
  -H "Internal-Token: Bearer ${LINKD_INTERNAL_JWT}" \
  "${LINKD_CONTROL_PLANE_URL}/api/v1/metrics/catalog"
```

`LINKD_INTERNAL_JWT` 是已签发的 JWT，不是共享密钥；签发示例见 [内部认证协议](../reference/contracts/internal-token.md)。

API 返回完整只读目录，不分页、不查询历史样本、不接受修改；Console 在浏览器中筛选并按 20 项分页。
响应字段如下：

| 字段 | 含义 |
| --- | --- |
| `schema_version` | 当前响应结构版本为 `1` |
| `modules` / `purposes` | `{id, name}` 形式的功能模块和用途分类 |
| `metrics[].name` / `prometheus_name` | 原始注册名与 Prometheus family 名 |
| `metrics[].display_name` / `description` | 中文短名称与完整统计口径；业务指标描述与 HELP 共用定义 |
| `metrics[].module` / `purpose` | 所属分类键 |
| `metrics[].type` / `prometheus_type` | 注册类型与导出类型；`up_down_counter` 导出为 `gauge` |
| `metrics[].unit` / `unit_label` | 注册单位与便于阅读的中文单位 |
| `metrics[].dimensions` | 可能出现的维度，每项包含原始名、Prometheus 标签名、中文说明 |
| `metrics[].series` | 可查询序列名；Histogram 包含 `_bucket`、`_sum`、`_count` |
| `metrics[].origin` | `linkd`、`runtime` 或 `exporter` |
| `common_dimensions` / `notes` | 公共 OTel scope 维度与使用边界说明 |

目录表示**二进制可提供的定义**，不代表功能已启用、产生了样本或已被采集。Go/process 指标从相同
collector 自动发现，会随平台和依赖版本变化。`Histogram` 的桶序列另带 `le` 标签，`Summary` 的
分位序列另带 `quantile` 标签；Prometheus 抓取时添加的 `job`、`instance` 不属于业务维度。
`service_version`、`linkd_role` 等 Resource 信息位于 `target_info`。

开发者增加指标的登记流程见 [指标目录与注册约束](../design/observability.md#指标目录与注册约束)。

「核心数据 → 策略活跃索引」自动列出 `active-alert-by-strategy` 的租户、策略组合，使用 SCAN/ZSCAN 游标逐批读取，点击行即可只读对账；「开始整体对账」后台检查当前缓存目标的全部租户和策略，展示进度、双向差异和无法确认的范围。
会合并已发布配置识别出的共享来源，并区分一致、Redis 缺失、Redis 独有和无法确认；查询失败、
扫描超限及并发变更不会被当成确定一致。页面展示控制面最近完整发现、待刷新数量，以及单策略校准时间、快照年龄和失败状态。用法、上限与一致性边界见
[策略索引查询与对账](active-alert-by-strategy.md#console-查询与对账)。

Cleaner 指标按 EventSource 聚合，received、settled 和 lane gauge 可以带 Kafka partition。Lifecycle
记录 Signal、Mailbox、lease、Event 裁决和 FinalHook。指标禁止包含 tenant、实体 ID、fingerprint、
topic、group、完整错误或 payload。

Lifecycle 页的「Hook 输出」及 `final_hook` 节点详情同时展示成功率、调用结果速率与 P95，
按 EventSource 和 Hook 名称区分实例。成功率使用所选计算窗口内
`succeeded / (succeeded + failed)`，跨 Worker 按调用量聚合；错误、超时与 panic 计为失败，
`skipped` 不计入。全部失败显示 0%；无调用、只有 skipped 或无时序时不补成 0% 或 100%。
成功只表示 Hook 调用返回成功，不保证下游业务处理完成；失败不会回滚已保存的 Alert 状态。

Prometheus exporter 使用统一的 `telemetry.metrics.prometheus.listen_address`。cleaner、lifecycle、
control-plane 和 all-in-one 不拥有各自的配置字段，但每个实际进程都会启动独立 exporter，并通过
`linkd.role` Resource 属性区分。多个角色共享宿主网络时必须使用不同配置文件分配不冲突的监听端口。
控制面任务额外使用固定 `linkd.task` 枚举；任务名、Stream、Group、索引名和错误文本不会作为动态属性。

## 降级语义

- Prometheus 不可用：当前 Kafka/Redis/存储快照仍可查看，历史图表显示不可用。
- 单个控制面数据源不可用：对应任务显示 `partial/unavailable`，其他任务状态与配置仍可查看。
- Kafka 或 Redis 不可用：对应页面和运行节点显示 `unavailable`，其他区域继续工作。
- Event 详情的 values/evaluations 是来源事实，`_processing.evaluations` 是逐级裁决；关联入口支持多个 Alert。
- severity 过滤针对 Alert 当前级别，不把事件多个级别合成单个 severity；values 只存储展示，不做数值聚合。
- 当前存储缺少可聚合字段时隐藏对应 facet，不扫描 `source_raw_data/extra_data/enrich/params` 等仅存储
  JSON 对象伪造结果。
- Elasticsearch 拓扑只检查 `index_prefix` 推导的稳定读 alias，不接受浏览器传入任意索引表达式；alias
  背后的时间桶元数据按有界批次读取。
- Alert 列表和详情折叠归档时的相同副本；只有投影 ACK 不同时，选择确认水位覆盖另一副本的真实快照，不能仅凭 History 名称选取旧确认。业务内容或绑定冲突、两边确认互不覆盖时明确报错，等待归档收敛。聚合统计无法原子去重，该短暂窗口可能重复计数，并返回 warning。

## OneModel 查询

「系统 → OneModel 查询」（`/onemodel`）提供独立的实例与关联查询，无需选择 EventSource。
控制面读取顶层 `resources.onemodel`；Console 只需已有的 `dispatch.url` 和管理 JWT。
此功能是复用 Go OneModel SDK 的领域查询，普通中间件诊断仍由 Console 的 Node 连接层执行。

实例查询填写租户、模型，可叠加实例 ID 和类型化属性条件；高级 JSON 支持 `all/any/not`。
结果提供表格、属性详情、JSON 复制及游标翻页。默认每页 50 条，最多 200 条，改变条件后重新开始。
快照一分钟无访问即过期，页面会提示重新查询。实例结果可带入关联查询，选择目标模型、关系和方向；
关联结果有 1024 条硬上限，超限需要收窄条件。

公共资源的地址和认证由部署配置统一管理，修改后重启控制面和 Lifecycle；来源编辑和丰富预览只编辑规则。
配置页展示本机启动配置的脱敏资源副本，并不证明控制面或各 Worker 已加载相同配置。
接口、预算和错误语义见 [OneModel 查询 API](../reference/contracts/onemodel-query.md)。

## 策略模拟与执行统计

策略详情中的“计数与窗口模拟”支持抑制和合并：添加多个已有 Event ID 和判定时间，或在步骤
JSON 中提供完整已丰富 Event；“推进虚拟时间”追加一个没有 Event 的时间步骤。每次运行从空状态
重新模拟，可取消。修改输入或切换版本会清除旧结果，候选配置不保存。结果展开可查看计数/阈值、
活动 Alert 绕过、窗口和虚拟 Alert 快照。只模拟当前策略，不代表实际父告警或处置已执行；完整
边界和预算见[模拟 API](../reference/contracts/policy-api.md#隔离状态模拟)。

策略列表下方展示本页策略当前小时、最近6或24个UTC小时桶的观察：命中、未命中、求值失败和执行
跳过。统计跨版本，包含重试、重查和候选匹配；缓存尽力采样，不是唯一事件数。读取失败隐藏旧表格，
不能把不可用当零。可独立刷新，详见[统计口径](../reference/contracts/policy-api.md#逐策略执行观察)。

## 可选 KAC 告警详情入口

Console 环境变量 `LINKD_CONSOLE_KAC_ALERT_URL_TEMPLATE` 是**可选的全局导航配置**，不按来源或租户
分别配置。省略或空字符串时投影详情不显示 KAC 入口，不影响投影、处置、模拟和统计。

```bash
export LINKD_CONSOLE_KAC_ALERT_URL_TEMPLATE='https://kac.example/kingeye/#/kac/alarmDetail?type=all&id={alarm_id}'
```

模板必须包含 `{alarm_id}`，可选 `{bk_tenant_id}`，只允许 HTTP(S)，最长2048字符。
占位符只能位于路径、查询或hash，不允许改变站点；禁止URL凭据及token/secret等鉴权查询参数。
部署地址和路由由管理员按实际 KAC 设置，修改配置后重启 Console。页面以编码后的稳定 alarm_id
生成“查看 KAC 告警”链接，与“查看当前 Alert”并列，打开新页且不携带 Linkd 凭据或 referrer。
使用 KAC 当前登录租户和权限；模板中附带租户参数不意味着自动切换登录租户。

本地 KAC 源码可见 `/kac/alarmDetail` 及 `id/type` 查询路由；示例不证明任何部署地址或权限已联调。
Helm 对应 `console.kacAlertUrlTemplate`，仅注入 Console；未配置时不注入相关环境变量。

### 创建内容预览与 Event 丰富

丰富调试保留 Event ID、Event JSON 两种多等级输入，另提供 Opening Event JSON 创建内容预览。
对应 `input.opening_event`，必须提供完整领域 Event 和匹配当前发布的来源版本；多个 triggered
等级时填写 `input.severity`。响应分别展示来源 Event 的丰富结果和候选告警内容，不修改来源事实。
普通 `input.event_id/input.event` 不调用内容构建器；三类输入不能混用。

### 内部合并来源的诊断边界

Console 接受 `storage.type=internal_merge` 的内部来源；它没有 Kafka 输入和 Cleaner 任务，
不会出现在 Cleaner 来源列表或创建 Kafka Admin 输入连接。它的输出 Hook 与策略活跃索引 Hook
仍参与对应诊断，不能为了展示 Cleaner 而丢弃整个内部来源。未知存储类型和损坏的 Kafka
输入配置仍明确失败，不用空列表伪装正常。
