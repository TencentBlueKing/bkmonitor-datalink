# all-in-one 双 Repository E2E

测试分别启动 `storage.repository: elasticsearch` 和 `storage.repository: mysql` 的
`linkd run all-in-one`，验证同一条链路：

```text
RawEventMessage(standard)
  -> SourceCleaner / EventFactory
  -> Event + processing metadata
  -> Redis Mailbox Event ID + lifecycle signal
  -> 上游 Kafka offset 确认
  -> Alert / AlertLog
  -> Kafka Alert V1 snapshot
```

[`rawgen`](../../tools/rawgen) 使用固定 seed 生成 triggered、重复投递、resolved、closed、等级升级、低等级抑制、坏消息和跨租户场景。断言覆盖稳定 Event ID、租户覆盖、时间回退、Severity mapping、`related_alert_ids`、处理 outcome、Alert 不可变继承字段、默认 close_and_create 的双快照升级输出和 Kafka cause。

当前 rawgen 的输入为单项 evaluations；多级别组合、update_current、升级优先级及计划恢复由
[Lifecycle 回归测试](../../../internal/lifecycle/multilevel_test.go)覆盖，不把该单元测试结果描述为真实中间件 E2E。
下述启用策略 E2E 另覆盖 `update_current` 的真实进程链路，rawgen 基础用例仍保持原有范围。

每次运行使用唯一的 MySQL database 或 Elasticsearch index prefix，以及独立 Kafka topic/group 和
Redis stream/group/mailbox/lock 前缀；结束时只清理本次资源。Raw topic 固定创建 3 个 partition，并
验证最终 Event、Alert、AlertLog 和输出数量，并验证每条 Event 在完成处理前保存完整 Enrich 结果。
固定 fixture 使用历史时间；测试在控制面创建模板后调用 storage prepare 显式准备对应时间桶，不随系统日期修改输入。Mailbox Signal 按空到非空产生并允许冗余，因此只断言
consumer group 无 pending/lag，并完整扫描确认不存在非空 Mailbox List，不断言 Signal 与 Event 一一对应。普通 `go test ./...` 不访问
外部服务，未设置 `LINKD_E2E=1` 时跳过。

运行两套后端：

```bash
./tests/e2e/allinone/run.sh
```

只运行单一后端：

```bash
LINKD_E2E=1 go test -count=1 -run '^TestAllInOneElasticsearchE2E$' -v ./tests/e2e/allinone
LINKD_E2E=1 go test -count=1 -run '^TestAllInOneMySQLE2E$' -v ./tests/e2e/allinone
```

默认 MySQL 版本断言为 8.4.10；验证其他明确版本时用 `LINKD_E2E_MYSQL_VERSION` 设置期望版本，不能省略版本核对。

连接可由 `LINKD_E2E_ELASTICSEARCH_URL`、`LINKD_E2E_REDIS_ADDRESS`、`LINKD_E2E_REDIS_PASSWORD`、`LINKD_E2E_REDIS_DATABASE`、`LINKD_E2E_KAFKA_BROKER`、`LINKD_E2E_MYSQL_ADDRESS`、`LINKD_E2E_MYSQL_USERNAME` 和 `LINKD_E2E_MYSQL_PASSWORD` 覆盖。

## Console 真实后端浏览器验收

[`TestAllInOneConsolePoliciesE2E`](console_test.go) 另需显式 `LINKD_E2E_CONSOLE=1`。
先安装 Console 的现有依赖并确保本机 Chrome 可由 Playwright 启动；该用例自动构建 Console 和 Linkd，
分别为 ES/MySQL 创建隔离策略数据，再用同一份临时 YAML 启动真实 Console 服务。
测试使用 `/real-console` 挂载前缀，浏览器直接访问正式页面、连接层及控制面，不使用 route/fulfill。

```bash
LINKD_E2E=1 LINKD_E2E_CONSOLE=1 go test -race -count=1 -timeout 25m \
  -run '^TestAllInOneConsolePoliciesE2E$' -v ./tests/e2e/allinone
```

连接和版本仍使用本页上述环境变量。普通 `make check` 不启用外部服务或 Chrome。
该用例覆盖发布版本与真实 Event 匹配预览、Event 丰富与历史抑制诊断、防抖计数、跨来源聚合成员、
屏蔽原版本复查、待裁决窗口、固定合并成员快照，以及从浏览器关闭父 Alert 后的关系解除。
防抖详情还提交受控对账：未绑定计数得到 retained/unbound_counter，实际请求的操作者和原因持久保存，
浏览器结束后由正式管理 API 核对结果与计数不变。
合并关系详情提交原版本检查得到 unchanged/no_progress，再由 Go 核对持久操作者、原因和正式步骤调用标记；
随后人工关闭父，仍只解除关系，下一条子 Event 才获准处置。
投影页面额外覆盖真实管理查询、按需读取冻结快照、填写原因恢复失败任务及刷新后读取最新恢复记录。
动作页面同时验证原请求摘要、队首观察、原 CAS 失败恢复和服务端操作者；任务在测试专属 action
集合中构造，不修改真实 Alert 绑定、不发送 KAC HTTP 请求，不代表正式动作生产/发送已经启用。
投影失败任务由测试直接写入本次部署的隔离任务集合，不修改真实 Alert 绑定；这是管理链路测试，
不代表正式生产器或自动投递器已接通。测试断言任务仅恢复为 pending、原快照摘要不变、操作者为 console-local。
浏览器结束后再次读取真实业务数据，并从 Kafka 核对处置：手动复查无额外 action，父关闭仅解除，
下一条子触发才放行；读取页面不增加计数或成员，恢复投影任务也不会增加旧 KAC Kafka action。
父关闭后还通过真实 Console 查询独立抑制清理历史，读取原终态原因与确认结果；该读取不会重新清理 Redis。

截图和不含凭据的合成 fixture 位于日志报告的 `console/test-results/real-<backend>-<token>/`。
配置、后台进程、数据库、索引、topic 和 Redis 状态在测试结束时清理；不会留下可继续操作的 Console 服务。
Go race 仅覆盖测试进程；真实 Linkd/Console 是普通构建，不能把测试命令的 race 标记当作子进程竞态检测。
2026-10-05 首轮完整通过：ES 约 102 秒，MySQL 约 70 秒；Chrome 本身约 16/29 秒，各核对 4 条真实 KAC action。
真实浏览器曾复现屏蔽详情多查询导致的 429，已通过共用有界代理队列修正，最终用例要求所有业务 HTTP 响应无错误。
同日扩展投影任务管理后再次通过：ES 约 107 秒、MySQL 约 71 秒；Chrome 本身约 22/30 秒，仍各核对 4 条 action。

2026-10-06 扩展动作投递管理后的真实 Chrome 验收通过：ES 约 142 秒、MySQL 约 68 秒，浏览器本身
约 48/27 秒，各核对 4 条原 KAC Kafka action。恢复后验证 pending/generation=2/attempts=0，原完整
request_hash 未变，最近恢复保留 console-local、原 CAS 和“真实 Console 恢复原动作”原因。
入队、受理、处置完成在页面分开展示；该管理场景没有配置可靠出口凭据，自动动作发送保持停用，不能把它当作新协议端到端发送证明。
同日扩展抑制受控对账后通过：ES 约 137 秒、MySQL 约 71 秒；Chrome 本身约 22/30 秒，仍各核对 4 条 action。
此轮真实浏览器先于候选占位保护和最终按钮展示调整，新增候选分支由下述真实 API 用例覆盖，最终界面另有模拟浏览器复验。
后续 Playwright 运行会清理其输出目录，需保留当次截图时应在下一次浏览器测试前归档。
同日增加合并关系显式检查后，ES 约 107 秒、MySQL 约 68 秒通过；Chrome 本身约 22/28 秒，各仍核对四条
action。命令响应不确定后跨刷新重投由独立模拟 API 浏览器用例验证，不能与真实服务场景混为同一证据。

## 可靠 KAC 后台投递

[`TestAllInOneKACDeliveryE2E`](kac_delivery_test.go) 启动真实 all-in-one 进程，以显式投递凭据启用
projection-producer/projection-delivery/action-enqueue/action-delivery，使用真实 ES/MySQL、Redis、
来源发布 API 和实际 HTTP 发送器。接收端是本测试的协议模拟，不是 KAC。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout 15m \
  -run '^TestAllInOneKACDeliveryE2E$' -v ./tests/e2e/allinone
```

连接环境变量同上。初始 Event/Alert 由显式测试 Processor 创建并注入目标绑定，故意在即时动作入队前
中断；后台仅凭持久意图完成入队。测试验证投影 503 时无动作发送、来源改地址/移除目标后原任务仍走
原 Release、动作 401 保留失败及 API 恢复后自动受理、真实人工关闭接口即时入队并自动同步终态，
同一文档身份不变。
控制面任务 API 必须报告四个实际活跃循环及已完成扫描，且不含接收地址或 Token。

2026-10-06 两后端分别约 54.13/42.89 秒通过，各得到同一 Alert 的一份终态投影及两条独立动作受理。
测试结束停止进程并清理隔离数据。测试编排经过 race；启动的 Linkd 子进程为普通构建。
初始绑定为明确夹具，因此本用例不证明 Worker 自动目标绑定、所有策略组合的动作入队、
实际 KAC 接入或 Console 生产时序图已完成验收。

`TestAllInOneKACWorkerRecorderE2E` 则不配置投递凭据，并核对四个后台循环均停用。初始 fixture
保存已绑定 Alert 和未完成动作意图；后续升级来自真实 Kafka/Cleaner/Worker，必须补齐原动作并创建
升级动作；人工关闭返回时第三条动作已可排序读取。所有任务保持 pending/attempts=0，证明即时
入队不依赖后台补扫或接收端凭据。fixture 没有共享近期缓存，发送下一 Event 前先等待 ES 活动查询
可见，不以此等待替代正式 Worker 的缓存行为。
初始 Alert 建立后再发布含旧 KAC Hook 的来源版本；升级与关闭仍只留下原绑定的可靠动作任务，
旧 KAC action topic 必须没有消息，普通 Kafka V1 状态 topic 则保留升级和关闭的两个快照。
这同时验证后续来源配置不能绕过已有 Alert 的动作归属，未选择目标迁移规则。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout 15m \
  -run '^TestAllInOneKACWorkerRecorderE2E$' -v ./tests/e2e/allinone
```

## 策略控制与可靠投递组合

[`TestAllInOneKACPolicyDeliveryE2E`](kac_policy_delivery_test.go) 使用真实 ES/MySQL、Redis、来源与
策略发布、Enrich/策略引擎及正式 all-in-one 控制任务。全部触发、恢复 Event 走 Kafka/Cleaner/Worker，
Worker 按 opening Release 自动绑定目标，关闭走管理 API；HTTP 接收端只模拟协议受理。

用例逐次核对完整投影业务快照与可靠动作数量：

- 被屏蔽且从未放行的 Alert 关闭只更新投影，不生成关闭动作。
- 时间屏蔽自动到期只更新屏蔽状态；下一条 Event 才产生 firing，随后人工关闭产生 close。
- 单成员合并到期失败，由独立控制任务释放并产生 system_operation firing；后续恢复产生 resolved。
- 成功合并的成员只投影关系，不产生动作；未放行成员恢复也只同步状态。
- 人工关闭父告警后，活动子告警仅解除关系；下一条 Event 才放行，后续真实恢复正常投递。

最终五条已绑定 Alert 对应五条终态投影和六条可靠动作；合并父暂用普通 KAC Hook，单独检查其
firing/close 两条消息。合并父可靠目标和依赖屏蔽由以下独立用例验证；HTTP 接收端为协议模拟。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout 15m \
  -run '^TestAllInOneKACPolicyDeliveryE2E$' -v ./tests/e2e/allinone
```

## 依赖屏蔽与可靠投递组合

[`TestAllInOneKACDependencyDeliveryE2E`](kac_dependency_delivery_test.go) 分别验证自定义依赖与 CMDB
依赖。每类使用自己的租户、两个跨来源主告警和两个由 Worker 自动绑定可靠目标的子告警。子实例均不属于主
目标集合；CMDB 场景使用反向存储的真实 OneModel 关系边。所有初始 Event 经实际 Kafka/Cleaner/Worker。

检查最新已准入主告警选择、已有绑定不随新主迁移、CMDB 关系查询故障保留绑定及 partial 诊断。
随后把子告警定时元数据推迟一小时，在主恢复/关闭后核对正式 hint 诊断，证明即时解除来自事件提示。
解除只同步投影；下一 Event 可重新绑定另一活动主，最后一位主结束后也仍要等待下一 Event 才准入。
每一步比较完整业务投影和持久动作数量，已准入子正常恢复，从未准入子人工关闭不产生动作。

每个后端最终四条子告警对应四条终态投影和四条可靠动作；普通 KAC topic 只保留四条主告警各自
触发与终结的八条消息。该用例的主来源保留旧 Hook，HTTP 接收端仍为协议模拟。
共享的凭据/子告警测试装配显式传入租户，前述时间屏蔽与合并组合仍独立验证。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout 25m \
  -run '^TestAllInOneKACDependencyDeliveryE2E$' -v ./tests/e2e/allinone
```

## 合并父告警的可靠投递

[`TestAllInOneKACMergeParentDeliveryE2E`](kac_merge_parent_delivery_test.go) 让真实跨来源子 Event 进入
周期窗口，由正式控制任务裁决、渲染并发布内部父 Event。正式 Worker 按内置来源的 opening Release
自动绑定目标；合并任务补齐父子关系并放行父告警，可靠动作通过
正式投影门槛后受理。用例分别核对一名成员恢复时父仍活动、全部成员恢复后父产生一次系统恢复动作，
以及人工关闭父之后活动子只解除关系、后来子恢复也不覆盖父的人工关闭原因。

父、子来源均启用可靠目标。每个后端最终两个父告警、四个子告警对应六条投影及四条父告警可靠动作；
未放行子告警只同步状态，原 KAC action topic 没有消息。
HTTP 接收端仍为协议模拟，实际 KAC 接入另行验证。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout 22m \
  -run '^TestAllInOneKACMergeParentDeliveryE2E$' -v ./tests/e2e/allinone
```

## Console 可靠投递的真实时序

在可靠投递用例上设置 `LINKD_E2E_CONSOLE_DELIVERY=1`，额外构建并启动真实 Console 和 Chrome，
以及一个本次专属的 Prometheus 容器。需要本机 Apple Container、已准备的 Prometheus 镜像、Chrome
及 Console 依赖；`LINKD_E2E_PROMETHEUS_SCRAPE_HOST` 必须显式设置为容器可访问的宿主 IP。
测试 exporter 使用临时端口监听宿主网络，Prometheus 管理端口仅发布到宿主回环地址，退出清理独立容器
和测试数据；既有 Prometheus 的配置与时序不变。可用 LINKD_E2E_PROMETHEUS_IMAGE 指定镜像，默认
docker.io/prom/prometheus:latest，实际版本从运行中的 buildinfo 接口记录。

```bash
LINKD_E2E=1 LINKD_E2E_CONSOLE_DELIVERY=1 \
LINKD_E2E_PROMETHEUS_SCRAPE_HOST=192.168.65.1 \
go test -race -count=1 -timeout 15m \
  -run '^TestAllInOneKACDeliveryE2E$' -v ./tests/e2e/allinone
```

示例 IP 需按本机网络替换。浏览器使用独立的 playwright.delivery.config.ts，不使用 route/fulfill。
验收包括正式任务目录、动作受理与投影确认、七个投影图表和七个有数据动作图表；未制造未知接收结果，
对应第八个动作面板必须明确无数据，不能补零。进程筛选、时间范围、无数据再恢复、显式刷新、折叠、
深浅主题及 850px 布局均校验。浏览器不能发送业务写入，观察期间也不能增加实际动作 HTTP 次数。
结果截图位于本次 `console/test-results/delivery-<backend>-<token>`，不包含配置凭据。

2026-10-06 最终 ES/MySQL 场景约 60.97/49.01 秒通过，Chrome 各约四秒；版本查询确认临时
Prometheus 为 3.14.0。真实截图同时验证孤立零值点、查询起止时间和刻度避让，未修改既有 Prometheus。

## 启用告警策略的自动流程

[`TestAllInOneEnabledPoliciesE2E`](policies_test.go) 通过管理 API 发布实际启用的策略，并启动两个普通来源及
内置合并来源。两个 Repository 后端均连接真实 Kafka、Redis、MySQL 元数据和 Elasticsearch OneModel：

- 防抖第 N 条创建 Alert；活动 Alert 升级直接绕过抑制，保留 opening Event 的内容与丰富；恢复后重新计数，其他租户独立。
- 独立终态清理记录：Alert 恢复、未生成 Alert 的恢复 Event、人工关闭聚合主均从正式管理 API 核对持久确认；结果包含原子删除的窗口/代次，区分两类登记，原 action 数量不增加。
- 相同分组的两个来源复用聚合主；人工关闭主后，后续 Event 可以建立新 Alert。
- 抑制运行态 API 展示防抖计数/阈值与 owner、聚合登记与成员；活动升级不增计数，恢复/关闭后索引消失，下一次计数使用新代次。查询不改变最终处置序列。
- 受控对账保留未绑定防抖、有效活动 owner 和期限内聚合候选；正式后台清理终态 owner 残留，相同命令重投不再删除。
- 时间屏蔽通过真实业务空间和静态实例选择器匹配；没有新 Event 时自动到期解除，下一条触发才放行。
- 两个条件组自动生成内置父 Event/Alert 并建立关系；人工关闭父后只解除子关系，子仍 active，下一条触发才重新判断处置。
- 子全部恢复后自动恢复父；只有一个成员的窗口到期后释放原 Alert。
- 独立 Kafka action topic 核对每个 Alert 的完整输出序列，包括未放行子告警的零输出，避免只靠最终状态推断处置行为。
- 通过正式管理 API 读取活动窗口、持久化完成裁决、ended 历史关系和首次成员摘要，确认 Redis 窗口清理不丢失业务查询结果。

OneModel 固定实例索引名由测试代理映射到本轮独立索引，查询及 PIT 仍由真实 Elasticsearch 执行；
元数据为独立数据库内的最小表和合成实例。不会修改已有 Kingeye 数据库或共享 OneModel 别名。
此用例验证 Linkd 自动任务和现行 KAC Kafka Hook，不代表 KAC 接收端集成或可靠投影任务已装配。

沿用上面的连接环境变量，独立运行（`run.sh` 仍只运行基础两套 rawgen 用例）：

```bash
LINKD_E2E=1 go test -race -count=1 -timeout=20m -v \
  ./tests/e2e/allinone -run '^TestAllInOneEnabledPoliciesE2E$'
```

可在表达式末尾加 `/elasticsearch` 或 `/mysql` 选择后端。每个后端最多运行八分钟；等待真实定时任务、
窗口和 ES 刷新，不在测试中直接调用裁决/解屏/解除关系方法，也不强制刷新业务索引。
结束时停止进程并清理该部署的所有来源 Stream、策略 Redis 状态、来源/策略索引、topic 和临时数据库。
2026-10-05 补充抑制运行态读取后，ES 7.17.7 / MySQL 9.5.0 后端分别约 267/168 秒通过，每后端核对
14 条实际 KAC Kafka action 消息；不把查询成功等同于 KAC 接收端已应用。

## 抑制受控对账

[`TestAllInOneSuppressionReconcileE2E`](policies_test.go) 复用真实两后端、Kafka、Redis 和控制面任务，
独立执行防抖/聚合部分，包含阈值、活动绕过、恢复/关闭清理明细，以及带固定 owner/代次的显式对账。
普通计数及有效活动主保留；另在本次专属 Redis 命名空间登记终态 owner 残留和仍在期限内的候选，
分别验证真实 API/后台任务清理和 retained/candidate_pending。两个异常/中间状态来自合成夹具，
不是测试直接修改业务 Alert；它们不构成 KAC 接收端集成或故障强一致恢复证明。

沿用前述连接变量独立运行：

```bash
LINKD_E2E=1 go test -race -count=1 -timeout=20m -v \
  ./tests/e2e/allinone -run '^TestAllInOneSuppressionReconcileE2E$'
```

每后端核对 7 条实际 KAC Kafka action，显式对账不增加处置。2026-10-05 两后端约 94/34 秒通过；
包含新候选保护分支，早先 14 条 action 的完整启用策略运行没有声称再次执行该新增分支。
结束时仅清理本次隔离资源和部署专属请求锁；不扫描或清空共享 KAC Redis/索引。

## 合并显式请求

[`TestAllInOneMergeRequestsE2E`](policies_test.go) 单独验证已有合并裁决/关系的正式控制点、持久命令、
后台执行和重复提交。真实 Linkd all-in-one 连接 Kafka、Redis、ES/MySQL，使用默认自动任务周期：

- 已就绪且成员活动的关系返回 unchanged/no_progress，实际调用正式检查但不更改进度或处置。
- 人工关闭父后按关闭前的关系版本提交新请求；人工与自动任务共用窗口租约。人工先推进则 advanced，
  自动任务先推进则 superseded，两者都必须最终只解除关系，子保持 active 且不获准处置。
- 已完成裁决返回 unchanged/already_complete，不重新准备父 Event 或重启已结束业务结果。
- 同命令重投返回完全相同的持久结果；下一条子触发才获准处置，各后端只产生预期的三条 KAC action。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout=20m -v \
  ./tests/e2e/allinone -run '^TestAllInOneMergeRequestsE2E$'
```

连接变量、单后端选择和隔离资源规则与上文一致。2026-10-05 在 ES 7.17.7 / MySQL 9.5.0 上分别约
67/42 秒通过；本次两后端的父关闭检查均实际得到 advanced/progressed。关闭进程后核对 Kafka 消息，
退出时仅清理本次部署资源，包含专属 merge-requests 租约。这里不代表 KAC 接收端已消费这些消息。

## 周期与多策略合并

[`TestAllInOneMergeCombinationsE2E`](merge_combinations_test.go) 另验证周期、多策略和抑制组合，独立运行：

```bash
LINKD_E2E=1 go test -race -count=1 -timeout=20m -v \
  ./tests/e2e/allinone -run '^TestAllInOneMergeCombinationsE2E$'
```

- 连续两个周期窗口：成员重复不改变首次 Event/截止时间；条件已满足时仍等待到期，父创建时间不得早于截止；下一周期使用新窗口身份。
- 一个成员同时进入三个策略窗口：两个策略各建父，另一个到期失败；失败释放保留两个成功关系，分别关闭父只解除对应关系，最后解除后仍等下一触发才放行。
- 合并关系结束后，正式运行态 API 仍返回两组历史关系；失败的第三个窗口不伪造父子关系。
- 防抖达到阈值后进入合并等待；等待中的活动 Alert 升级绕过防抖/聚合，保留原窗口；未放行成员不能成为跨来源聚合主。
- 两个成员均恢复后父自动恢复，子不产生从未获准的处置；下一轮新生命周期重新防抖计数。

此用例使用真实定时任务，单个后端八分钟预算；每后端核对 11 条 KAC Kafka action 消息。
模板显式提供非空 `name/content`，满足现行 KAC Hook 的必填约束；不把标准 Event 校验通过当作输出协议已满足。
Event 与 Alert 搜索可见性分别等待，不用 Event 已处理来推断 ES Alert 已刷新，也不强制刷新业务索引。
2026-10-05 在 ES 7.17.7 / MySQL 9.5.0 上通过，两个 Repository 后端分别约 312 秒和 271 秒。

## 三类策略组合准入

[`TestAllInOnePolicyAdmissionE2E`](policy_admission_test.go) 同时发布防抖/关联聚合、时间屏蔽、合并三类策略，
使用真实静态主机目标、两个来源及默认后台任务，分别运行 ES/MySQL Repository。

- 每个来源第一次触发只计防抖，第二次形成被屏蔽 Alert；被屏蔽 Alert 不成为聚合主，也不提前创建合并窗口。
- 无 Event 的定时解除保持 active、尚未准入且没有合并等待；下一次触发复用该 Alert，绕过新告警抑制后入窗。
- 两个条件组满足后真实建父；人工关闭父后子只解除关系，不恢复、不自动补发处置。
- 下一次不匹配合并的触发才分别放行子，仍保留各自 opening Event 的内容；子结束后新生命周期重新计数。
- 最终核对父与两个子的六条 KAC action 消息，确认计数、屏蔽、定时解除、合并等待和解除关系没有额外处置。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout=20m -v \
  ./tests/e2e/allinone -run '^TestAllInOnePolicyAdmissionE2E$'
```

连接变量与其他用例一致，每后端八分钟预算；只使用本轮独立资源。此用例覆盖时间屏蔽与前后策略的
组合边界，依赖屏蔽自身的选择/固定关系/故障行为由下述独立用例验证。
2026-10-05 在 ES 7.17.7 / MySQL 9.5.0 上通过，两个后端分别约 152 秒和 122 秒。

## 满窗口合并容量

[`TestAllInOneMergeCapacityE2E`](merge_capacity_test.go) 在默认控制面扫描周期、执行预算和 ES 刷新周期下，
分别运行 ES/MySQL Repository。先通过两个来源发送 255 个不同指纹的成员，只满足第一个条件组；再发送
第 256 个成员满足第二组，验证完整快照、模板成员数、真实父建立、256 个成员关系及父获准处置。随后输入
256 条恢复 Event，等待父自动恢复、关系全部解除，并核对仅有父的两条 KAC action 消息。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout=45m -v \
  ./tests/e2e/allinone -run '^TestAllInOneMergeCapacityE2E$'
```

连接变量与其他用例一致，每后端二十分钟预算；父建联最多观察五分钟，关系清理最多观察十分钟。
保持每轮最多 16 个解除步骤、扫描到末页后等待 30 秒，因此满窗口清理可能需要约八分钟；这段时间不代表
父仍 active，父终态与剩余关系清理分别检查。测试不缩短控制面周期、不直接调用裁决、不强制刷新业务索引。
ES 测试读取使用显式 10000 条硬上限并核对完整总数、超时和分片失败，避免原有首 100 条截断误报。
日志记录首次等待、建父、恢复及清理各段墙钟耗时；这是单窗口功能容量验证，不代表生产吞吐或延迟承诺。

2026-10-05 在 ES 7.17.7 / MySQL 9.5.0 上均通过，各核对两条父 KAC Kafka action 消息：

| Repository | 255 成员首次等待 | 第 256 条触发至父获准处置 | 256 条恢复输入至父恢复 | 父恢复后关系完整清理 |
| --- | --- | --- | --- | --- |
| Elasticsearch | 4.755 秒 | 165.344 秒 | 9.943 秒 | 478.946 秒 |
| MySQL | 0.889 秒 | 183.269 秒 | 29.882 秒 | 479.219 秒 |

总用例约 1423 秒，包含构建、环境准备和两后端串行执行；计时包含默认刷新及调度等待。
两个后端使用同一代码和既定任务预算，不据此做数据库性能排名。测试执行 `go test -race`，启动的 Linkd
子进程沿用普通 `go build`；生产包并发检查由 `make check` 的竞态测试另行覆盖。

## 依赖屏蔽自动流程

[`TestAllInOneDependencyShieldE2E`](dependency_test.go) 复用启用策略的隔离环境，分别验证自定义依赖和
CMDB 依赖；业务空间、模型映射、静态目标、主机/交换机实例及双向关系使用本轮独立 MySQL/ES 数据。
两个来源经真实 Kafka、Cleaner、Lifecycle 和控制面定时任务处理，分别运行 ES 与 MySQL Repository。

- 首次子 Alert 固定绑定已放行主 A；主 B 出现后，新子选择较新的 B，已有绑定经过真实定时复查仍保持 A。
- CMDB 子目标通过 `switch_connect_host` 关系匹配，分别覆盖主到子、子到主两个存储方向。
- 关系查询故障时，已有绑定保留并安排下轮检查；新的匹配明确记录跳过原因，继续处理。
- 正式管理 API 提交带当前版本的手动复查，独立任务执行并记录 partial；精确结果、最新诊断、请求历史及重复提交一致，未新增处置。时间屏蔽用例同样验证 retained 请求。
- 被屏蔽子自身人工关闭或来源恢复，终结后清理绑定并可通过管理 API 查到完整解除前后记录，不产生从未放行告警的处置消息。
- A 恢复只解除绑定 A 的子；下一条子触发才重新选择 B。B 人工关闭后，后台自动解除其绑定，子保持 active 且尚未放行。
- 只有下一条子触发才获准处置；正式管理 API 同时验证按主/类型筛选、精确绑定详情和包含空页的历史分页。
- 每个 Repository 后端核对 11 条实际 KAC Kafka action 消息，未放行子、定时解除及关系清理没有额外输出。

2026-10-05 的复查版本验证分别约 177 秒（ES）/96 秒（MySQL）。同轮启用三类策略用例约 267/168 秒，
分别仍为 14 条 action 消息；新增手动复查没有放行处置。这里是实际管理 API 与后台任务的测试，
该依赖场景的诊断/重试浏览器测试使用模拟接口，不与 API 用例合称真实浏览器联调；
另有上文独立的真实 Console 用例，覆盖时间屏蔽 retained 复查及三类运行态。

测试代理仅将 `kingeye_all_instance`、`kingeye_topo` 映射到本轮独立索引；故障场景暂时对关系查询返回 503，
其余实例、关系及 PIT 查询都由真实 Elasticsearch 执行。不会修改共享 OneModel 别名、Kingeye 表或生产配置。
这是静态实例和小规模关系场景，尚不覆盖拓扑/动态分组完整业务链、关系容量或 KAC 接收端。

沿用前述连接环境变量独立运行：

```bash
LINKD_E2E=1 go test -race -count=1 -timeout=20m -v \
  ./tests/e2e/allinone -run '^TestAllInOneDependencyShieldE2E$'
```

同样支持在表达式末尾添加 `/elasticsearch` 或 `/mysql`，每个后端八分钟预算；`run.sh` 不自动运行此用例。
2026-10-05 在 ES 7.17.7 / MySQL 9.5.0 上通过，两个后端分别约 187 秒和 96 秒；资源退出时按测试作用域清理。

## 依赖关系与解除分页容量

[`TestAllInOneDependencyCapacityE2E`](dependency_capacity_test.go) 在隔离 OneModel ES 索引中准备
1024 个双向关系成员及其他租户干扰边，使用正式 SDK 和策略链路验证边界；另验证单方向 1025 条边、
双向并集 1025 个目标均报超限。实际 Event 明确记录策略跳过，既有绑定复查得到 partial 并保留。
测试删除自己创建的溢出边后，从两个来源输入 65 个子告警，核对完整固定绑定。

主关闭前，仅在测试子告警的正式指纹租约内把下一定时检查延后一小时，并等待旧定时轮次结束；
关闭后由正式提示任务按 16 条/页解除全部子告警。逐个核对 hint/changed 诊断、active 状态和未准入，
当前主筛选不再包含解除历史。下一条子 Event 才放行；Kafka 核对主 firing/close、超限候选 firing、
下一条子 Event firing 共四条 action，其余子告警均零处置。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout=35m -v \
  ./tests/e2e/allinone -run '^TestAllInOneDependencyCapacityE2E$'
```

两种业务 Repository 分别运行，每种最多 15 分钟；不强制刷新业务索引，不手工调用提示执行器。
实例/关系来自测试合成数据和真实 ES 查询，不是实时 CMDB API。65 个子告警用于覆盖多个满页和尾页，
不代表单主的最大容量或生产吞吐承诺；4096 主候选、4097 超限、32 MiB 和末页失败在 runtime 单测验证。

2026-10-06 真实 ES 7.17.7 / MySQL 9.5.0 两后端约 388/37 秒通过，各核对四条实际 Kafka action。
其中 65 子告警从主关闭到全部解除约 3.9/0.3 秒；ES 总耗时包含逐条保存未来定时检查时间时的正常
刷新等待。该测试使用 Go race 检查测试进程，Linkd 子进程为普通构建；结束时已清理测试资源。

## 拓扑与动态分组屏蔽

[`TestAllInOneTargetShieldE2E`](targets_test.go) 使用真实 MySQL 目标定义和 ES OneModel 数据，分别在 ES/MySQL
Repository 上运行。每种目标准备 205 个实例以跨越 200 条分页边界；通过真实 Kafka 输入建立屏蔽，
随后修改本轮专属元数据/拓扑成员，让独立定时任务重新检查，不直接调用解除函数。
额外使用只读 SDK 核对完整拓扑目标恰为 205 个，以便区分依赖解析与生命周期故障。

- 动态分组从 `dynamic_group_v2` 当前定义求值，测试不创建成员缓存表；字段类型来自模型目录，ES 查询使用真实 nested `attribute_values`。
- 验证根条件按 KAC 的 AND 行为执行，`contains` 中的星号按字面量处理，其他业务成员被排除；相同分组 ID 的其他租户定义不能混入。
- 主机拓扑从当前租户/业务的节点与 membership 索引完整 Scroll，重复路径去重后经实例 PIT 复核；相同 locator 的其他租户/业务数据不能混入。
- 动态定义出现未知字段、拓扑成员身份损坏时，既有绑定保留，新候选记录 `target_resolution_failed` 并跳过策略。
- 修正动态条件使原实例不再匹配，或保留拓扑节点并清空本租户业务成员后，后台自动解除；无新触发时不放行，下一触发才产生处置。
- 每个 Repository 后端核对 8 条 KAC Kafka action 消息；实例/拓扑数据变更不会额外触发处置。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout=20m -v \
  ./tests/e2e/allinone -run '^TestAllInOneTargetShieldE2E$'
```

连接环境变量、单后端选择和每后端八分钟预算与前面的用例相同。测试代理只重写统一实例别名，
拓扑路径直接使用本轮独立前缀；全部 PIT/Scroll/SQL 由真实服务执行。仅修改/删除测试专属数据并在退出时清理。
这不覆盖服务实例 CMDB 实时来源、主机拓扑 CMDB 回源、大规模目标容量或生产 Kingeye 进程集成。
2026-10-05 在 ES 7.17.7 / MySQL 9.5.0 上通过，两个 Repository 后端分别约 103 秒和 66 秒。

## 混合目标与全局业务

[`TestAllInOneTargetCombinationE2E`](target_combination_test.go) 将静态实例、拓扑和动态分组放入同一策略，
使用真实 MySQL 定义、ES 业务/实例/拓扑数据以及正式双仓储流程。只读 SDK 先验证六种 selector 顺序
得到相同排序并集，逐项计数保留重叠，静态重复实例去重；全局业务从当前租户展开为业务 2/3，固定
业务组只在业务 2 执行，全局组受策略范围约束，拓扑分别覆盖绑定和不绑定业务的情况。
没有业务的租户得到完整空集合，不借用其他租户；具体业务策略中的越界显式实例使解析失败。

真实 Event 验证四个并集成员屏蔽、非成员/其他租户/未授权业务不匹配；最后一个动态 selector
失败时不能使用前面的静态/拓扑命中。既有绑定复查 partial 并保留，合法空分组或空拓扑只解除
不再被其他 selector 选中的成员，下一条触发 Event 才准入。随后删除一个显式实例，验证整条策略
跳过及既有屏蔽保留；恢复实例后重新读取完整目标。每后端核对七条 Kafka action，定时解除零处置。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout=20m -v \
  ./tests/e2e/allinone -run '^TestAllInOneTargetCombinationE2E$'
```

Go race 覆盖测试进程，Linkd 子进程为普通构建；所有配置/数据均为测试隔离资源。该用例不连接
真实 CMDB API，也不等同于已经执行完整 KAC 对照套件。共享候选预算另有单测验证：重复动态 selector
总计 20000 个候选成功，第三次读取超过预算时拒绝整份结果并释放最后一个 PIT 游标。

2026-10-06 真实 ES 7.17.7 / MySQL 9.5.0 两后端约 117/66 秒通过，各核对七条实际 Kafka action；
本次隔离进程、索引、数据库、topic 和 Redis 数据已清理。

## 数据生成与调度演练

生成独立 JSONL 数据集：

```bash
go run ./tests/tools/rawgen \
  -seed 42 \
  -mix active=1000,recovered=1000,closed=1000,severity_rotation=500,cross_tenant=100 \
  -tenant-count 20 \
  -duplicates 100 \
  -invalid 20 \
  -out /tmp/linkd-raw-events.jsonl \
  -expected-out /tmp/linkd-expected.json
```

生成器的随机性只影响稳定输入字段和生命周期块顺序；相同 seed/profile 产生字节级一致记录，同一生命周期内部顺序不变。

动态调度 E2E 使用测试专属部署命名空间和临时 API 端口，启动后显式调用 API 导入来源。
随后增加 3 个 Cleaner worker 和 1 个 Lifecycle worker，验证 4 个 Cleaner 候选受 3 partition 限制、2 个 Lifecycle 副本共享来源 Stream，再校验业务处理和输出。
普通测试不自动访问外部服务；所有清理仅针对本次测试生成的专属资源。

独立的多进程故障演练入口是 `TestSchedulingDrillE2E`，步骤和断言见
[核心任务调度验证流程](../../../docs/guides/task-scheduling-validation.md)。该演练保留本轮外部资源与证据，
只停止本轮子进程，不使用上述业务 E2E 的资源自动清理逻辑。

## 自动出口绑定与实时 CMDB 目标

`TestAllInOneKACTargetBindingE2E` 通过实际 Kafka/Cleaner/Worker 验证 opening Release 绑定，包含
空绑定不补绑、每租户相同 target ID 隔离、纯状态目标、来源移除后的原发布重试和新生命周期重新选择。

`TestAllInOneCMDBTargetsE2E` 的 APIGW/用户管理接收端为协议模拟，元数据 MySQL、业务 ES/MySQL、Redis、Kafka
及 Linkd 进程为真实本地服务。配置全局 `blueking` 与 `resources.cmdb: {}`，不向 OneModel 写入服务或主机成员，
验证显式服务、服务拓扑、主机回源、故障保留、完整零成员定时解除和下一 Event 再准入。

```bash
LINKD_E2E=1 go test -race -count=1 -timeout 25m \
  -run '^TestAllInOne(KACTargetBinding|CMDBTargets)E2E$' -v ./tests/e2e/allinone
```

CMDB 用例分别覆盖单租户和多租户，在两套业务存储上执行。单租户断言用户查询为零；多租户先注入
bk_admin 空结果验证策略跳过，再恢复查询验证失败不缓存与两个运行时的成功缓存复用。所有调用核对
同一应用凭据、原租户头和正确用户名，CMDB 仅接受 APIGW 路径。这不是实际蓝鲸部署权限联调。

### 全局 KAC 兼容插件验证（2026-10-08）

KAC 可靠投递相关用例已改为全局 `plugins.kac`，不再发布 kac_targets 或按租户配置凭据。
`TestAllInOneKACTargetBindingE2E` 验证两个来源、三个租户无需出口声明即自动生效；
`TestAllInOneKACDeliveryE2E` 验证直接写入故障阻挡处置、恢复重试和终态；策略、依赖屏蔽及合并父用例
继续验证处置资格与状态同步。每项覆盖 ES/MySQL 两种 Linkd Repository。

兼容 alarm_event 实际写入本地 Elasticsearch。测试代理仅注入故障并观察成功持久化的同步元数据，
不模拟存储成功；KAC 动作接收端仍使用进程内协议模拟，真实 KAC 应用联调单独报告。
每次使用独立 alias、模板、ILM 与同步元数据索引，并在进程退出后只清理本次资源。

### 策略模拟与统计隔离

`TestAllInOnePolicyDiagnosticsE2E` 覆盖 ES/MySQL 两套仓储：Kafka输入产生已丰富Event，查询真实
逐策略观察，通过模拟API读取已保存Event并在私有内存达到阈值；核对生产计数/TTL、业务记录及观察
未被模拟修改，再发送真实Event验证正常放行。该用例不连接KAC页面或动作接收API。
