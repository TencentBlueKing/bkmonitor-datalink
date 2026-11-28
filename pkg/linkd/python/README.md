# 独立告警源 Python 执行器

本次运行时与 Linkd 能力已实现并完成下述本地验证；厂商实连和生产部署尚未验收。2026-11-15 确认本次只交付独立运行时与 Linkd，KAC 管理端另做。新能力默认关闭，现有 KAC 不切换；后续由部署一次性切换执行权，不增加逐来源切换开关。Python 只执行插件；Linkd 负责来源鉴权、定时调度、同步调用、事件发布和进度保存。Redis 保存接收事实与结果，不是 Go/Python 调用队列。

- [插件开发与调试](../docs/guides/python-plugin-development.md)
- [容器构建与部署](CONTAINER.md)
- [来源配置](../docs/modules/event-source.md)
- [standard 事件契约](../docs/reference/contracts/standard-event.md)

## 运行与并发

`serve` 提供内部 `POST /execute`，使用 Bearer token 和请求/响应字节预算。Go 同步等待执行结果，不轮询结果。

执行地址必须直接指向 `/execute`；Go 不跟随 HTTP 重定向，避免执行 POST 和内部凭据被隐式重发。Python 容量拒绝和超时分别通过 Linkd 返回 503、504，其他执行端 HTTP 错误返回 502，不透传内部响应正文，也不在当前请求内自动重试。

每个服务启动固定数量的常驻 spawn 子进程，每个进程使用固定线程池；`--processes` 与 `--threads` 决定总并发。调用使用独立结果管道，每次创建插件实例，线程共享导入模块的全局状态。无空闲槽立即返回 503，不增加业务排队。

`--execution-timeout` 覆盖单次进程执行，超时终止进程组并返回 504；同进程的其他调用也会失败，后续请求可重建进程。`--read-timeout` 约束 HTTP 输入读取。输出进入 IPC 前限制 JSON 字节数，不限制插件计算内存，也不构成不可信脚本沙箱。

SIGTERM/SIGINT 停止接入，等待请求线程退出后回收子进程。部署应为执行超时和退出清理提供足够宽限期。

## 插件能力

| 插件名称 | 模式 | 额外依赖或宿主能力 |
| --- | --- | --- |
| rest_api_push、aliyuncms_customized、tcop、prometheus、zabbix | push | Zabbix 类型规则由 configs 注入 |
| skywalking_graphql、esight、tingyun | pull | requests |
| imc、rizhiyi | pull，内部包含恢复计算 | requests、来源告警查询 |
| uc、qcloud_monitor、bk_monitor | pull、recovery | requests、来源告警查询 |
| solarwinds | pull | pymssql |
| vcenter | pull | pyvmomi |
| bs_monitor | metric、fields | 可选 KAC 只读指标宿主；fields 可直接计算 |

表中列出主要采集模式；所有内置来源另支持 fields，以及沿用 KAC 默认空能力返回值的 metric。除 BS_MONITOR 外，当前指标入口没有真实取数实现，返回 {}；它不代表成功查询到空数据。

这些名称是独立运行时已迁入的注册表，不代表全部都是当前 KAC 管理页面注册的内置来源。任意旧 KAC 上传脚本不保证原样运行；插件应迁到独立基类和显式宿主能力，不模拟 Django/ORM 环境。

配置、可信租户、来源身份、时区、固定 received_at 和 state 由 Go 注入。迁入来源可设置 collection.plugin_source_id 保留 KAC 原 ID 对指纹算法的影响，查询/存储仍使用 Linkd source_id。插件返回 standard events 和可选 proposed_state；metric/fields 返回 data。恢复事件保留原 fingerprint。push 输入会先剔除顶层心跳，纯心跳不执行插件；混合列表保留业务顺序，与 KAC V2 接入行为一致。普通插件错误返回 plugin_error；明确允许重试的 TemporaryError 返回 temporary_error。Python 不自行重试；Go 采集服务在同一截止时间内最多尝试三次明确临时失败，每次执行前持久预留次数，跨进程恢复不会重置三次上限，网络断连、超时和容量拒绝不在当前同步请求内立即重跑。明确插件失败及三次临时失败耗尽会持久记录终结错误并移出待处理集合；重放返回原失败，不再次执行，也不提交部分事件或游标。基础设施结果未知的失败保留待恢复状态，后续恢复仍受同一持久尝试上限约束。

`--linkd-url` 与 `--linkd-token-file` 为 serve 和 debug 配置同一来源告警查询适配器。它按租户、来源读取 Linkd 告警，支持活动告警和最新来源告警，失败不返回假成功空列表。查询使用已签发的 Internal-Token JWT，每次从凭据文件重新读取，禁止跟随重定向；不使用执行器 token 或 Worker Token。旧 KAC 来源 ID 与 Linkd ID 不一致时，需要宿主完成映射。

自定义脚本可通过可信部署清单 `--plugin-manifest` 加载，debug 和 serve 使用同一工厂与加载器；debug 可用 `--plugin-file` 临时覆盖请求目标。清单在进程启动时读取，脚本文件应使用不可变版本目录。本地部署目录支持相对导入的辅助模块和数据附件，各插件命名空间隔离；KAC 在线上传、附件发布和滚动版本管理尚未接通。

## Linkd 接收与调度现状

管理 JWT 保护的 `POST /api/v1/event-sources/{source_id}/plugin-preview` 已支持按来源草稿预览五种模式，允许未发布或停用来源。只执行一次，不创建接收记录、不发布事件或提交游标。插件事件可再交给现有 `ingest/preview` 检查清洗结果；真实 Python 进程池与两个管理路由的连接已通过集成测试。请求示例与边界见[管理 API 预览](../docs/guides/python-plugin-development.md#管理-api-预览)，KAC 页面尚未改用此入口。

控制面配置 collector 后注册 `POST /api/v1/ingest/sources/{source_id}`，通过 X-Secret 匹配已发布、启用的 push 来源。鉴权发生在读取正文前，准备执行时再次读取来源配置；正文不能覆盖租户、插件或凭据。接收并发由 collector.max_concurrent_requests 限制（默认 4），满载在读取正文及持久接收前立即返回 503，不增加等待队列。

接收请求先保存 Redis Stream 记录。推送与定时调用在执行前均持久保存首次输入快照，包括发布版本、配置、输入游标和固定时间；相同请求重放读取首次输入，不重新拼接当前状态。输入保存失败不调用 Python。发布前保存首个完整输出，Kafka 使用 AllISRAcks、固定接收时间和 `collector/<request_id>/<batch_index>` 消息身份。计算结果保存失败不会提前发布；发布失败不会推进游标。批次发布次数单独持久计数，总计最多三次；Kafka 明确不可重试的错误直接终结，未知或临时错误保留供恢复，耗尽后终结当前请求，不永久阻塞该来源。成功发布后原子写入候选进度与完成状态，已完成请求重放直接返回原结果。HTTP 成功表示本次处理和发布完成，不表示 Cleaner/Lifecycle 已完成。

`linkd run collector` 通过现有 taskdispatch 注册 collector 角色。已发布的 pull 来源或配置独立恢复周期的来源可分配任务，每来源目标执行者数量为一。定时器沿用分钟 `*/N` 语义，每小时重新匹配；同来源 pull/recovery 串行，不补跑停机期间全部周期，单轮失败不停止后续周期。取消后不再启动后续保存、发布或完成提交阶段；已发出的 I/O 仍可能生效。

控制面 HTTP 写期限覆盖接收处理预算并预留 5 秒响应时间；调度中心失去授权或进程退出时，服务任务取消会传入正在执行的 HTTP 请求，停止尚未发起的后续阶段。已发送的外部 I/O 仍可能生效，这不等于存储侧隔离了过期执行者。

推送恢复由当前单活动控制面执行：接收时原子登记部署范围的待处理身份，启动立即处理一批，此后每五秒继续扫描，单轮最多处理 collector.recovery_batch_size（默认 32）条，每条有独立超时。扫描不读取正文，按持久 source_version 装配历史发布；已接受请求不会因为最新来源停用或删除而失去恢复入口。同步接收与恢复共用进程内请求登记，同一身份只能有一个执行者；冲突不排队，后台跳过，HTTP 返回 503。独立 HTTP 重复提交仍是不同接收身份，不按正文 hash 去重。

控制面复用 taskdispatch 已有领导权：接收、保存输入/输出、预留尝试次数、提交结果/游标及清理索引都在同一 Redis Lua 中检查当前会话。旧领导权过期或被继任者替换后，旧进程不能再改写这些状态，不新增分布式锁。已发出的 Kafka 发布仍可能到达，继任者沿用冻结输出、固定消息身份和接收时间处理重投，不宣称恰好一次。

pull worker 在启动及每个新周期前通过 Recovery.Drain 串行恢复，使用受任务授权约束的历史发布接口；未终结错误阻止新周期，下一计划时刻继续。旧 push 记录由控制面恢复，worker 不代替它执行。独立 pull worker 的每次 Redis 写入也在同一 Lua 中核对现有任务的 worker、epoch、来源、角色、活动阶段和过期时间；时间取 Redis 服务端时钟，续租取当前协调快照。失授权即结束本任务，不再等待下一拉取周期。真实 Redis 已验证同 worker 新代次、跨 worker、过期、停止与移除任务，以及旧执行者晚返回不能覆盖新结果/游标；本地真实双 collector 进程接管 E2E 已通过，部署环境仍需验收。

终结记录的输入快照、结果和尝试计数按 `collector.completed_retention_seconds` 保留，零值默认七天。成功或明确失败终结时，在同一 Redis Lua 中移除 Stream 的重复正文、清除待处理索引并设置 TTL；保留期内仍可通过快照读取原请求。未完成请求、其计数和来源游标不设置 TTL。过期后不再承诺依靠接收记录识别旧请求，显式重新执行原身份可能再次计算/投递；不把这一窗口当作永久去重。

## 安装与调试

在 Linkd 根目录执行：

```bash
uv sync --project python --python 3.11 --frozen --all-extras
python/.venv/bin/linkd-plugin-runtime debug \
  --request python/examples/push_request.json \
  --plugin-file python/examples/push_plugin.py
```

基础依赖为 requests，可选 sqlserver/vmware 分别安装 pymssql/pyvmomi；版本和间接依赖固定在 uv.lock。pyproject.toml 支持构建 wheel/sdist，并安装 linkd-plugin-runtime 命令。凭据通过配置或文件注入，不写入镜像。

调试在单个执行进程中计算，--execution-timeout、--max-body-bytes 和 --max-result-bytes 同样生效；超时或 SIGTERM/SIGINT 退出会回收子进程。stdout 输出计算结果，不发布事件或提交进度；依赖上游接口的插件仍会执行其网络调用。恢复调试方式、配置字段和输出契约见开发指南。

## 验证与边界

```bash
# Python 单元、真实进程及本地 HTTP 测试；厂商接口使用替身
PYTHONPATH=python python/.venv/bin/python -m unittest discover -s python/tests -q

# Go 与 Python 同步 HTTP 联调
LINKD_RUNTIME_PYTHON="$PWD/python/.venv/bin/python" go test -race ./internal/pluginruntime -count=1

# 启动独立临时 Redis，不使用已有 Redis 数据库
LINKD_RECEIPTS_INTEGRATION=1 go test -race ./internal/collector -count=1

# 显式指定测试 Kafka；测试创建并删除专用 topic
LINKD_RECEIPTS_INTEGRATION=1 \
LINKD_TEST_KAFKA_BROKERS=127.0.0.1:9092 \
LINKD_RUNTIME_PYTHON="$PWD/python/.venv/bin/python" \
go test -race ./internal/collector -run TestPythonKafkaIntegration -count=1
```

已验证常驻进程复用、多线程、超时/崩溃后重建、同步 HTTP、来源鉴权、结果冻结、Redis 完成状态及租户隔离。真实 Python 对正式 Go 查询路由的联调覆盖 JWT 鉴权、跨页活动告警、最新终态查询和恢复事件生成，领域仓储使用内存实现。真实 Redis/Python/Kafka 的 ACK 丢失注入验证：重建执行服务后从持久索引发现请求，沿用首次输入和已计算结果恢复原发布；Python 只计算一次，重发记录经正式 Cleaner Mapper 得到相同 EventID，内存 Repository 命中幂等。该 ACK 丢失分支不覆盖真实外部领域存储或进程崩溃接管。

另已通过正式 HTTP 接收 → Python 2 进程×2 线程 → Kafka → Cleaner → Redis Mailbox → Lifecycle → Elasticsearch 的真实进程 E2E：单租户产生 2 条 accepted Event 并完成同一 Alert 的触发/恢复；共享双租户产生 3 条 accepted Event 和 2 条独立 Alert，相同指纹不会跨租户恢复。验证包含持久 AlertLog 和接收索引清空。运行方法见 [接收链路 E2E](../tests/e2e/allinone/README.md#独立-python-接收链路-e2e)。这不代表已验证所有厂商 API 或拉取进程故障接管。

真实双 collector 进程接管 E2E 也已通过：分钟调度开始后暂停旧 owner，既有租约到期后由另一进程接管，恢复首次保存的请求与调度时刻；旧进程恢复并完成后续心跳后，结果和游标没有被覆盖。恢复事件经真实 Kafka、Cleaner、Lifecycle 到达 Elasticsearch。测试使用受控 HTTP 上游和部署清单插件，运行方法见 [拉取接管 E2E](../tests/e2e/allinone/README.md#拉取调度与-collector-进程接管-e2e)。

独立 Python 3.11 环境、wheel 安装后调试和 Linux ARM64 非 root 容器挂载测试已通过。最终 Linux ARM64 镜像已通过离线模式构建，镜像内调试、多进程/线程 HTTP 调用和 SIGTERM 退出已验证，详情见容器部署说明。厂商 API、SQL Server、vCenter 和生产 Linkd 查询凭据尚未实连验收。

## 本次交付验收

| 要求 | 实现与验证 |
| --- | --- |
| 独立执行存量内置插件 | 已迁入上述 16 个插件实现；独立基类与显式查询能力替代 Django/ORM 依赖，插件样本测试覆盖正常与失败路径。厂商接口使用替身。 |
| Go/Python 同步调用，Python 只执行 | HTTP `/execute` 直接返回计算结果；Go 负责持久接收、发布与游标提交。真实 Python HTTP 联调通过。 |
| 多进程、多线程及可配置并发 | 常驻 spawn 进程与线程池；满载拒绝、超时回收、崩溃重建和退出测试通过。 |
| 接收、鉴权和定时调度 | 来源 Secret、可信租户注入、分钟调度、独立恢复周期与 taskdispatch 分配；单租户及共享双租户接收 E2E 通过。 |
| 结果进入 Cleaner，支持故障恢复 | 真实 Kafka/Redis/Elasticsearch 链路和双 collector 进程接管通过；旧执行者不能覆盖结果或游标。 |
| 开发文档与调试 | CLI debug、自定义部署清单、附件、管理预览 API 及 Cleaner 预览；真实进程调试、超时和输入/输出预算测试通过。 |
| 默认不启用 | 旧配置无需增加 collector/collection；运行时与采集角色需显式配置和启动，2.x 不内置 Helm Chart，未部署或切换业务来源。 |

Go 完整 `make check` 已通过。Python 3.11、3.14 及安装实际 wheel 的 Linux ARM64 非 root/只读容器均通过 109 项测试。上述结果证明本地实现与测试覆盖的行为，不代表生产或全部厂商环境兼容性已经验收。

## 后续管理端与部署工作

以下工作不属于本次运行时与 Linkd 交付范围；KAC 管理端另做，执行权切换仍由后续部署完成。

- KAC 来源配置/脚本发布、附件、旧接收 URL/HTTP 响应兼容及管理调试流程接入。
- KAC 管理入口与独立运行时的调用切换；已提供单租户/共享来源的离线配置片段导出。
- 目标部署环境的角色发布与接管验收；本地双 collector 进程接管及旧进程恢复后的结果/游标保护已验证。
- 目标部署环境启动与厂商插件实连验收；本地 ARM64 镜像及 REST 接收至 Elasticsearch 的完整链路已验证。

迁入实现有明确行为修正：IMC、日志易和听云的恢复/子请求失败不再静默报告完整成功；日志易首次无历史记录不再索引空列表。TCOP 保留原指纹算法，数字时间戳改按注入的来源时区格式化，缺少发生时间时拒绝以当前时间补身份；跨宿主时区的回归已通过。vCenter 连接改为协商可用 TLS 版本并设置 SDK 网络超时，避免强制 TLS 1.0 与当前 OpenSSL 默认密码套件不兼容；原证书校验策略不变。各插件仍需用真实样本核对身份稳定性，不能把算法保留视为已完成验收。
