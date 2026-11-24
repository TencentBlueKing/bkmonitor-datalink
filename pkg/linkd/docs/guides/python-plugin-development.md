# Python 告警源插件开发与本地调试

交付范围（2026-11-15 确认）：本次完成独立运行时与 Linkd，包括 CLI 调试和管理预览 API；KAC 四步骤调试页面与在线脚本发布另做。功能默认关闭，后续部署统一切换。本指南中尚未接通的 KAC 管理能力属于后续工作，不表示本地运行时调试依赖它们。

状态：开发中。独立执行、接收、调度与恢复已实现并完成本文列出的本地验证，默认不启用，不表示已经部署或切换 KAC。Python 只执行采集和转换；Linkd 负责鉴权、调度、可靠发布和进度提交。

## 环境与第一个插件

独立镜像的构建、进程/线程参数和挂载方式见 [容器部署说明](../../python/CONTAINER.md)。

从 Linkd 仓库根目录执行。Python 使用 3.11；推送示例仅依赖标准库，不需要 Django、Redis、数据库或 Kafka。

```sh
PYTHONPATH=python python3 -S -m linkd_runtime debug \
  --request python/examples/push_request.json \
  --plugin-file python/examples/push_plugin.py
```

[示例插件](../../python/examples/push_plugin.py)导出 `AlarmClient` 和 `MODES = ("push",)`。
`AlarmClient` 继承 `linkd_runtime.base.BaseClientV2`。成功输出 JSON，退出码 0；失败退出码 1，stderr 输出错误分类。插件在独立执行进程运行，其 print 重定向到 stderr，stdout 保留 JSON。
不要在日志中打印账号、请求正文或凭据；工具不会自动脱敏插件自己打印的内容。

debug 固定使用一个执行进程和一个调用槽，不发布事件或提交候选游标。--execution-timeout 默认 30 秒，覆盖初始化与本次计算；--max-body-bytes 默认 1 MiB，读取请求文件时执行；--max-result-bytes 默认 8 MiB，在结果进入进程管道前执行。超时终止执行进程组，超限失败且不截断结果。SIGTERM/SIGINT 会取消初始化或计算并回收子进程，退出码分别为 143/130。serve 的进程数和线程数选项不改变单次 debug 的并发。

```sh
PYTHONPATH=python python3 -S -m linkd_runtime debug \
  --request python/examples/push_request.json \
  --plugin-file python/examples/push_plugin.py \
  --execution-timeout 10 --max-body-bytes 1048576 --max-result-bytes 8388608
```

省略 `--plugin-file` 和 `--plugin-manifest` 使用内置插件，名称与模式见 [运行时插件表](../../python/README.md#插件能力)。自定义文件只对本次本地调试注册，不上传、不影响运行中服务。

## 入口与输出

| 模式 | 构造方式 | 必须实现 |
| --- | --- | --- |
| push | `AlarmClient(source, data)` | `pull_raw_events()`，转换传入数据 |
| pull | `AlarmClient(source)` | `pull_raw_events()`，读取第三方接口 |
| recovery | `AlarmClient(source)` | `pull_recovery_events()`，返回恢复/关闭事件 |
| fields | 类方法调用，不创建实例 | `get_custom_msg_fields()`；基类默认返回空列表 |
| metric | `AlarmClient(source)`（真实覆盖实现） | `get_metric(alarm, start_time, end_time)`；默认空能力直接返回 `{}` |

每次请求创建新实例，不依赖实例属性跨请求保存进度。模块级可变状态也不得作为持久化进度。
返回事件迭代对象，空列表表示正常无事件。事件字段遵循[standard 契约](../reference/contracts/standard-event.md)，建议明确填写 `fingerprint`、`title`、`evaluations` 和来源时间。

- `fingerprint` 关联同一告警的触发和恢复，使用来源稳定身份，不能以随机值或当前时间兜底。
- 来源等级和动作由插件映射；支持 triggered、resolved、closed。
- 租户由执行器按可信来源上下文注入，插件不要从业务数据选取租户。
- 时间使用 `self.source_time(value)`，按请求 timezone 解释无时区值。
- 心跳由公共计算层过滤，结果为空也可能是成功。
- 普通拉取附带的恢复放入公共恢复输出；独立 recovery 入口不能生成 triggered。

## 参数和运行状态

请求必填 version=1、request_id、bk_tenant_id、source_id、plugin、mode。push 额外提供 data。
configs 是宿主解密后的来源参数，由 `self.alarm_source_config_dict` 访问；不能在脚本中读取 KAC ORM。
timezone 默认 Asia/Shanghai。received_at 是宿主固定的本次时间，恢复生成依赖它；eSight 也用它固定查询窗口。
state.meta_info 是旧游标，映射为 `self.alarm_source_obj.meta_info`。interval_minutes 对应原采集周期。

有游标插件覆写 `proposed_state()`，返回例如 `{"meta_info": 123}`。只有执行完全成功，结果才带 proposed_state。
Python 不保存该值；Linkd 已实现事件可靠发布后的原子游标与完成记录提交。debug 不经过这个提交步骤，看到候选游标不代表已经推进。

## 管理 API 预览

启用 Collector 后，可使用管理 JWT 调用 `POST /api/v1/event-sources/{source_id}/plugin-preview`。鉴权头为 `Internal-Token`；来源 `X-Secret` 和 Worker Token 不能调用此接口。默认关闭 Collector 时返回 503。

请求示例：

```json
{
  "request_id": "manual-preview-1",
  "mode": "pull",
  "received_at": "2026-11-15T00:00:00Z",
  "state": {"meta_info": {}}
}
```

接口读取当前草稿，允许预览尚未发布或已停用的来源。插件名、配置、时区和原 KAC 来源 ID 均取自草稿，不能由请求覆盖。共享来源必须传入匹配绑定的 `bk_tenant_id`；单租户来源可省略。管理预览允许选择停用的租户绑定。

`mode` 支持 `push`、`pull`、`recovery`、`fields`、`metric`。前三种必须指定 RFC3339 `received_at`；`push` 和 `metric` 必须提供 `data`。显式 `state` 优先；省略时，拉取和恢复模式读取已提交游标，没有游标则使用草稿初始状态。字段和指标查询不读取已提交游标。

接口只执行一次，不自动重试，直接返回运行时结果（包括候选事件、候选游标或辅助查询 `data`），不产生接收记录、不发布 Kafka、不提交游标。`request_id` 用于关联本次调用，不提供调试请求去重。插件仍可能访问真实厂商服务，因此应使用适当的调试凭据和数据。

管理预览使用独立的并发槽，槽数和请求字节预算分别沿用 Collector 的 `max_concurrent_requests`、`max_request_bytes` 配置，超时沿用接收超时。容量不足立即返回 503，输入超限返回 413，插件执行失败返回 422，超时返回 504，依赖不可用返回 502。该接口尚未接入 KAC 调试页面或其四步骤工作流。

Python HTTP 服务返回的 503/504 在 Linkd 预览和来源接收接口中保留；内部认证失败等其他 HTTP 错误返回 502，内部响应正文不透传。HTTP 错误不转换成插件 `temporary_error`，不会触发当前请求内的自动重试；正式接收的持久恢复行为见[Collector 契约](../reference/contracts/standard-event.md#collector-重投身份开发中)。

`collector.endpoint` 必须是可直接调用的 `/execute` 地址，不使用重定向入口。Go 不跟随 301/302/303/307/308，不会向重定向目标重发请求或凭据；这些执行端响应作为依赖错误返回 502。

### 从插件结果预览清洗

插件预览返回的 `events` 是 standard Cleaner 的原始输入，不是页面展示用的标准事件。要检查清洗结果，将每条事件作为 `POST /api/v1/ingest/preview` 的 `samples[].payload`，沿用首次取样的 `received_at`。该接口同样使用管理 JWT。

```json
{
  "bk_tenant_id": "tenant",
  "event_source_id": "source",
  "session_id": "manual-preview-1",
  "kac_source": {"source_id": "original-kac-id", "source_name": "测试来源"},
  "source": {
    "kind": "draft",
    "calculation": {
      "cleaner": {"type": "standard"},
      "fingerprint_mode": "field",
      "fingerprint_field": "source_alert_id",
      "severity_mapping": {"fatal": "critical"}
    }
  },
  "output": "standard",
  "samples": [{
    "sample_id": "one",
    "received_at": "2026-11-15T00:00:00Z",
    "payload": {
      "bk_tenant_id": "tenant",
      "fingerprint": "original-alert",
      "title": "CPU",
      "evaluations": [{"action": "triggered", "severity": "fatal"}]
    }
  }]
}
```

示例中的 `payload` 应替换为插件实际返回的事件；租户、来源和 `kac_source` 取自管理端可信配置。已提供的 `fingerprint` 会保留，`source_alert_id` 是未提供指纹时的回退字段配置，不能把 `fingerprint_field` 配成 `fingerprint`。正式调试应使用来源真实清洗配置，示例仅演示默认 standard 清洗。

清洗预览按事件中的等级展开 `items`；一个插件事件可以产生多个展示结果。后续修改过滤或字段规则时复用首次保存的插件事件重新预览，不将 `items[].standard_event` 再作为插件输入，也不重新拉取厂商数据。两次 API 调用均不负责保存调试会话；会话和原始样本仍由管理端持有。

真实常驻 Python（2 进程 × 2 线程）与这两个管理路由的集成验证命令：

```bash
LINKD_RUNTIME_PYTHON="$PWD/python/.venv/bin/python" \
  go test -race ./internal/controlplane/api \
  -run '^TestPluginPreviewPythonCleanerIntegration$' -count=1 -v
```

该测试使用临时本地运行时和只读来源草稿，不需要 Django、Redis、Kafka 或数据库；不代表 KAC 页面工作流已切换。

## 错误、外部查询与调试边界

普通错误直接抛出，执行器返回 plugin_error。仅已确认可安全重试的临时故障抛 `TemporaryError`；Python 不自行重试。Linkd 采集服务收到明确的 temporary_error 时，在同一次同步调用预算内立即重试，总计最多三次，保留原请求身份、时间、配置和输入。网络错误、超时、容量拒绝及普通插件错误不会在本次同步请求内立即重跑；发布和进度提交也不在计算重试范围内。耗尽后持久记录本次终结失败并移出待处理集合，定时来源继续后续周期。相同请求重放返回原失败，不再执行；失败返回中的部分事件和候选游标不会保存为成功结果。终结记录写入失败保留错误并等待恢复，网络断连等结果未知的错误不据此终结。每次真正调用 Python 前，Go 会在 Redis 持久预留一次尝试；同一请求跨进程恢复仍累计最多三次，已缓存输出的发布重放不占用计算次数。预留后进程崩溃也消耗一次机会，因为不能确认插件是否已经执行。预留写入失败不调用 Python；预算耗尽后保存 attempts_exhausted 终结错误。
不要捕获异常后返回空列表冒充成功。分页失败应抛错，不能把前几页作为完整批次提交。

本地调试复用真实插件代码，拉取插件仍会访问第三方服务。调试使用与服务相同的执行进程、超时和结果预算；没有恶意代码沙箱或通用离线 HTTP 回放，仍仅调试可信代码。
开发单测应替换 HTTP 调用，至少覆盖正常、空结果、非法输入、外部失败、分页失败和游标不提前提交。

IMC 恢复需要宿主活动告警查询。当前可在 Python 测试中通过 `Executor(active_alerts=reader)` 注入；CLI 已支持通过 --linkd-url/--linkd-token-file 查询，未配置能力时仍明确失败；厂商 API 联调不能由缺依赖失败替代。

同步 HTTP 服务只返回计算结果，不代表告警已创建。容量不足立即 503；serve 命令通过常驻进程池执行，execution-timeout 超时后终止执行进程；读取超时仅控制 HTTP 输入。调试页面、KAC 四步骤衔接、脚本版本发布及完整部署仍未完成。

## 验证命令

```sh
PYTHONPATH=python python3 -S -m unittest discover -s python/tests -v
LINKD_RUNTIME_PYTHON=/path/to/python3 go test -race ./internal/pluginruntime -count=1
```

厂商 HTTP 测试使用替身，不能替代真实厂商联调；独立运行时依赖已由 pyproject.toml/uv.lock 固定，安装和镜像验证范围见运行时 README。

## 本地同步服务

先准备只含内部认证 token 的本地文件，再执行（不要将凭据文件提交到仓库）：

```sh
PYTHONPATH=python python3 -m linkd_runtime serve \
  --token-file /path/to/runtime-token \
  --host 127.0.0.1 --port 8091 --processes 4 --threads 2 \
  --execution-timeout 30 --read-timeout 10 --max-body-bytes 1048576 \
  --max-result-bytes 8388608
```

启动后 stdout 输出监听地址。Go 同步客户端请求 POST /execute，使用同一内部 token。
每个槽对应常驻执行进程内的一个并发调用名额；容量不足立即 503，脚本超时 504，不排队、不自动重试。
SIGTERM/SIGINT 停止接入并等待当前调用结束（受读取和执行超时约束），随后回收执行进程。
serve 默认注册内置插件，也可通过 --plugin-manifest 加载可信部署目录。--plugin-file 仅用于本次 debug 覆盖；KAC 在线上传与版本发布尚未接通。
Python 3.11 的 spawn 方式隔离执行进程，但这不是恶意代码沙箱。结果字节预算由 --max-result-bytes 控制，默认 8 MiB，为包含多个最高 1 MiB 内容事件的批次预留空间；超限返回错误，不截断事件。此限制控制序列化/通信结果，不能限制插件在构造事件时的内存。大内存插件需配置容器资源预算；同进程线程共享模块，非线程安全插件使用 --threads 1。

## 多进程与多线程

serve 的 --processes 设置进程数，--threads 设置每个进程线程数（默认 1），
总执行容量为两者乘积，满载立即拒绝。每个线程独立插件实例，但同进程共享导入模块，
插件不得以模块全局变量保存租户、请求数据或可变连接上下文；非线程安全插件先使用 --threads 1。
任一调用超时会终止整个执行进程，因此同进程其他调用也会失败；不能安全终止单个 Python 线程。
调用方必须处理这种不确定结果，不自动重跑可能有副作用的插件。
已测试同一进程两个线程同时执行，多线程超时和崩溃传播已有回归覆盖；实际容量仍需结合厂商接口和部署资源压测。

## 恢复插件的宿主查询

serve 可指定 `--linkd-url` 和 `--linkd-token-file`，为 IMC/UC 注入活动告警读取能力。
查询使用 `Internal-Token: Bearer <已签发 JWT>` 访问 Linkd `/api/v1/alerts`，按请求的可信租户和来源筛选 active，分页读取；
默认总量预算沿用 KAC 查询路径的 10000 项、32 MiB、1000 页，超限或 HTTP 错误终止本次调用。
当前 source_id 同时作为 Linkd event_source_id；旧 KAC ID 与 Linkd ID 不同的迁移需在宿主转换，不能直接混用。
返回给插件的 event_id 仍是 fingerprint，extra_data 中的来源字段、meta_info 及原级别供恢复判断使用。
此配置已接到 spawn 工厂，并通过真实 Python 请求与 Go 正式 Token 验证器联调；生产查询服务及 IMC/UC 外部链路仍未联调。
本地 debug CLI 同样支持上述查询参数，具体用法见本页“调试需要历史告警的插件”。

## Linkd 控制面接收装配（开发中）

现有 Linkd 配置可增加以下可选块；未配置时仍不启用接收服务。沿用已有 storage.redis 和来源发布存储，Python 调用仍为同步 HTTP。

```yaml
collector:
  endpoint: http://127.0.0.1:8091/execute
  token: <与 Python token 文件一致的内部凭据>
  timeout_seconds: 35
  recovery_batch_size: 32
  max_concurrent_requests: 4
  max_request_bytes: 1048576
  max_response_bytes: 8388608
```

启动现有控制面进程后，接收路径为 `POST /api/v1/ingest/sources/{event_source_id}`，使用已发布来源 collection.secret 对应的 `X-Secret`。来源必须已启用且 collection.mode 为 push；单租户配置 related_tenant_id，共享来源配置 tenant_bindings，由各租户 Secret 决定可信租户。请求正文是原始来源 JSON。
Kafka 来源连接按发布版本复用，版本切换不会中断正在进行的发布；计算与发布成功后，Redis 原子保存候选进度和完成记录，全部成功才返回成功响应。
本地已验证真实接收至 Elasticsearch 的完整链路、共享租户隔离及双 collector 进程接管；成功请求保存完成状态，重放不重复提交旧游标。厂商实连、KAC 管理调用切换与目标部署环境仍待验收，当前默认关闭。

## 服务加载自定义插件

部署清单 JSON 示例：`{"demo": "plugins/demo.py"}`，文件路径相对清单目录解析。服务启动增加 `--plugin-manifest /path/to/manifest.json`，清单中的名称可用于请求 plugin 字段。模块仍需导出 AlarmClient 和 MODES。
`debug` 同样接受 `--plugin-manifest`，与 `serve` 共用 ExecutorFactory 和加载器。清单相对路径以清单目录为基准，与启动命令所在目录无关；若同时指定 `--plugin-file`，只覆盖本次 request.plugin 对应的入口，便于调试修改中的文件，清单文件不会被改写。缺失文件或加载失败会明确失败。

```bash
PYTHONPATH=python python3 -m linkd_runtime debug \
  --request /path/to/request.json \
  --plugin-manifest /path/to/manifest.json
```

清单由部署方控制，HTTP 请求不能指定路径。插件在执行进程启动时加载，正常调用复用模块、每次创建独立实例；进程替换时重新加载文件。
部署时应使用不可变版本目录，不能原地覆盖运行中插件文件，否则新旧进程可能使用不同代码。在线上传、版本发布和滚动切换尚未实现。
服务启动等待每个执行进程完成插件初始化握手；加载失败或初始化超时会阻止 HTTP 服务就绪并清理进程。异常只返回分类，不打印插件 traceback 中可能包含的凭据。

## 真实接收与 Kafka 联调

可显式设置 `LINKD_TEST_KAFKA_BROKERS` 和 `LINKD_RUNTIME_PYTHON`，执行：

```sh
LINKD_RECEIPTS_INTEGRATION=1 go test -race ./internal/collector -run TestPythonKafkaIntegration -count=1 -v
```

测试创建专用单分区 topic，并在结束后删除；需要 Kafka 创建/删除测试 topic 的权限。它实际启动 Python serve，经过 Go HTTP 来源接收和同步调用后发布 Kafka，再读取并验证结果。初次 HTTP 接收的来源配置及接收记录使用测试夹具。设置 LINKD_RECEIPTS_INTEGRATION=1 后，ACK 丢失恢复分支会启动独立临时 Redis，保存首次输入/计算结果，重建 Service/Factory，从待处理索引发现请求并使用历史发布恢复到原 Kafka topic。测试确认 Python 仅执行一次、完成后移出待处理集合，以及正式 Cleaner Mapper 产生同一 EventID，内存 Repository 幂等命中。需要本机 redis-server；测试自动清理临时 Redis 和专用 Kafka topic。该验证不包含控制面授权 RPC、真实多 worker 租约接管或外部 Cleaner/Lifecycle 存储。

## 拉取 worker（开发中）

配置好 collector、storage.redis 和 dispatch 后，可运行 `linkd run collector --config <配置文件>`，该进程以 collector 角色注册到现有调度中心。已发布的 pull 来源以及配置独立恢复周期的来源会被分配，每个来源目标执行者数量为一。
当前定时器沿用 KAC 的分钟 `*/N` 语义（每小时重新匹配分钟），不是从启动时起每 N 分钟执行；启动等待下一匹配时刻，不补跑停机周期。单来源串行拉取，不积压重叠任务。
每轮读取已提交游标，在调用 Python 前保存首次执行输入，再同步计算和发布 Kafka，成功后原子提交候选进度及完成状态；单轮失败记录错误并等待下一周期，授权 Context 取消即退出。调用身份和接收时间由固定调度时刻构成。采集服务在保存计算结果、发布事件和提交完成状态前检查取消；即使上游忽略取消并返回成功，也不再启动后续阶段。该取消检查不能撤回已经发出的 I/O；另由任务授权写入器在 Redis 原子拒绝旧任务代次提交。
拉取类来源在任务启动和每个新周期前，通过 `Recovery.Drain` 分批恢复待处理请求，每批数量由 `collector.recovery_batch_size` 控制（默认 32）。每个请求使用独立超时；已终结的插件失败继续，未终结错误停止本批，下一计划时刻再尝试，期间不越过旧请求生成新周期。

worker 使用 `GET /internal/collector/replay-release?task=<任务ID>&request_id=<持久请求ID>` 获取原发布。接口要求 Worker Token、X-Worker-ID，以及未过期的 collector starting/running 授权；租户和来源由分配确定，版本由持久请求确定，不能自选历史版本。普通发布读取接口保持原权限。

推送自动恢复在当前单活动控制面执行，启动立即处理一批，此后每五秒推进一批。接收时同时登记部署级推送索引；每轮扫描仅获取租户/来源/请求身份，不读取业务正文，每轮最多执行 recovery_batch_size 条。Redis SCAN 可能返回超过提示数量的身份，多余身份留到后续批次，不丢弃也不一次性执行。暂停、删除或变更当前来源不改变已经接受请求的历史发布。

接收和恢复复用同一请求执行登记；同租户/来源/请求身份冲突时不排队，后台跳过，HTTP 返回 503。恢复使用持久输入和原发布，已有计算结果直接复用。控制面 Redis 写操作复用 taskdispatch 的领导权 key/token，在同一 Lua 内检查授权并写入；旧控制面到期后不能接收新记录、消耗尝试次数、保存结果或覆盖游标。鉴权值不放入请求、配置发布或日志，也不创建新分布式锁。

推送的领导权交接、过期写入拒绝、接收后尚无执行快照的恢复、共享来源租户隔离和真实 Python/Redis/Kafka ACK 丢失恢复已验证。已经发出的 Kafka 请求可能继续完成，消息仍按稳定身份重投，不提供恰好一次保证。独立 pull worker 已接入任务代次存储保护；本地双 collector 进程接管与 Elasticsearch 链路已通过 E2E；具体厂商实连及目标部署环境仍待验收。来源从 push 改为 pull 时，旧 push 请求由控制面恢复；worker 等其终结后继续新周期，不接管正在接收的请求。

## 独立恢复周期

来源 collection 可配置 recovery_interval_minutes，0 关闭，正数沿用分钟 cron 语义。
同一个 collector 任务串行处理 pull/recovery，各有独立下一触发时刻；同时到期先拉取后恢复。
push 来源也可仅配置恢复周期。插件需支持 recovery 模式，否则本轮明确失败；不会按缺失接口推测恢复。
两类任务身份包含不同模式前缀；恢复不应返回拉取游标。已经过虚拟时钟与调度单执行者测试，真实集群接管尚未验证。

## 其他内置来源

已迁入 `prometheus`（push）、`solarwinds`（pull）和 `vcenter`（pull）。Prometheus 保留来源 fingerprint 优先及旧身份兜底算法，恢复使用 endsAt；SolarWinds 保留 SQL 查询/字段映射，需要 pymssql；vCenter 需要 pyVmomi，保留已有告警关联算法，不新增 hash 校验。
vCenter 采集窗口使用 received_at，取消原脚本额外六线程的转换步骤，由执行器统一控制并发；正常和异常路径销毁远端 collector 并断开连接。驱动版本已锁定并验证独立环境与 Linux ARM64 导入；本地最终镜像已验证，真实 SQL Server/vCenter 连接与目标部署环境仍待验收。
vCenter 连接使用可协商协议的 TLS 客户端上下文，沿用原插件不校验证书的策略，不再强制 TLS 1.0；SDK 的 httpConnectionTimeout 设置为 30 秒，connectionPoolTimeout 为 60 秒。已对照锁定的 pyVmomi 9.1.1.0 连接签名验证参数，单元测试覆盖协议选择、超时和错误因果保留，未宣称厂商握手已实连通过。

## Zabbix 来源类型映射

`zabbix` 支持 push。保留原六个必填字段、Base64/JSON 解码、`source_id` 派生事件身份及 `event_status=RESOLVED` 恢复语义。类型规则由 configs.alarm_types 注入，例如：

```json
{"alarm_types": [{"name": "CPU", "match_mode": "wildcard", "match_patten": "cpu.*"}]}
```

规则字段保留 KAC 的 match_patten 拼写，支持 string、regular（正则 match）、wildcard（fnmatch）。多条匹配按配置顺序逗号连接，无匹配保留原 alarm_type。来源配置发布方尚需把旧 AlarmType 表转换成此列表，执行器不直连旧数据库。
本地 debug 和服务 manifest 使用同一加载器，避免两种入口对模块导入或基类检查行为不同。

## 腾讯云监控与听云

`qcloud_monitor` 支持 pull/recovery，保留现有腾讯云 API 请求签名和字段映射。请求 received_at 固定本轮窗口，state.activate_time 传入上次成功时间；拉取成功返回 proposed_state.activate_time，恢复不推进该游标。恢复窗口按活动告警时间范围计算，不依赖列表顺序或执行机本地时区。

`bk_monitor` 沿用旧 KAC 在 `USE_TZ=True` 下的 UTC 查询窗口。上游 `source_time__gt/source_time__lte` 不携带时区，因此先将 `received_at` 和 `state.activate_time` 转为 UTC，再格式化；同一时刻的 `Z` 与 `+08:00` 表示不会改变拉取范围。首次拉取、历史激活时间和超过 12 小时的窗口裁剪均有回归覆盖；已有 `meta_info` 的增量 ID 查询保持原行为。
`tingyun` 支持 pull，移除 Django DEBUG 注入样本的分支，调试应使用测试替身/样本而不是切换服务全局 DEBUG。保留既有中文字段名称及来源映射。厂商 HTTP 请求均设置超时；尚未连接真实 API。
听云的应用列表和各应用/告警类型必须全部读取成功；任一子请求非 200 或解析失败均终止本批，不发布此前已读取的部分事件。此处有意修正旧插件静默跳过失败子请求的行为，避免把不完整快照作为成功。错误不携带包含认证参数的 URL 或响应正文。测试覆盖完整读取、空结果和中途失败，真实厂商接口尚未验收。

## 日志易历史与恢复

`rizhiyi` 支持 pull，普通拉取包含恢复检查。最新告警查询改为 Linkd 同租户、同来源的告警列表（含终态），沿用来源查询适配的 begin_at→alarm_time 映射，再将最新时间回退一分钟过滤本次数据。无历史时不过滤，查询失败则明确失败；不再按可变来源名称查询旧 ES。
历史时间字段与旧 KAC 的完全等价性仍需真实数据对账。恢复 HTTP 失败不再被吞成空恢复列表；如需部分成功模式，仍需明确响应契约。HTTP 查询与执行器均不直接操作旧 KAC 存储。

## 旧蓝鲸监控来源

`bk_monitor` 支持 pull/recovery。configs 显式提供 alarm_url、app_code、app_secret、username，替代 KAC 全局 settings。state.meta_info 和 state.activate_time 为增量输入，成功拉取返回候选状态，不再写 Django 模型。
恢复查询保留每批 50 个第三方编号的请求边界；批次顺序执行，整体失败不返回部分恢复成功。时间窗口使用 received_at，原身份及业务字段映射保留。已验证增量参数、空拉取、失败及多批恢复，真实蓝鲸监控 API 和 KAC 配置迁移仍待验证。

## 插件辅助查询（开发中）

执行协议新增 metric、fields 模式，结果放在 data，不进入事件发布流程。自定义插件的 MODES 可声明这些模式；fields 调用静态 get_custom_msg_fields()，metric 调用 get_metric(alarm, start, end)。metric 请求 data 包含 alarm、start_time、end_time，窗口必须为正。
`bs_monitor` 仅注册 metric/fields，原生告警接入不通过 Python 重新采集。fields 返回 close_reason。metric 使用宿主注入的能力：recovery_fields 读取指定恢复记录，query_builtin 按历史执行事实取数，query_points 处理无执行引用的旧指标参数；query_builtin 只有返回 None 才允许旧参数分支，异常不能回退。
已提供标准库 HTTP 适配器和 KAC 可选只读宿主接口。serve/debug 显式配置 --metric-url 与 --metric-token-file 后启用；未配置时 bs_monitor.metric 仍明确失败，不自动回调旧插件。现有 KAC 管理页面保持旧调用路径，后续随部署统一切换。指标多分组查询当前顺序执行且失败传播，不再静默吞掉某组异常。

## 独立安装与构建

仓库根目录执行：

```sh
uv sync --project python --python 3.11 --frozen
# 使用 SQL Server 或 vCenter 时增加相应驱动：
uv sync --project python --python 3.11 --frozen --extra sqlserver --extra vmware
python/.venv/bin/linkd-plugin-runtime debug --request python/examples/push_request.json --plugin-file python/examples/push_plugin.py
uv build --project python --out-dir python/dist
```

pyproject.toml 声明包和 CLI，uv.lock 固定依赖解析；无需 KAC 虚拟环境。已在独立 Python 3.11 环境执行全部 Python 测试，并将构建的 wheel 安装到另一个临时环境运行示例（移除 PYTHONPATH）。SQL Server/vCenter 驱动完成导入与所用连接参数检查，但真实服务兼容性尚未验证。

本次依赖依据各项目维护者的发布资料核对：

| 依赖 | 用途、许可证与取舍 |
| --- | --- |
| [Requests 2.34.2](https://pypi.org/project/requests/) | Apache-2.0；保留现有 HTTP 插件的认证、参数编码和异常接口，避免同时重写 HTTP 栈。当前项目仍有维护发布，升级需运行 HTTP 插件测试。 |
| [pyVmomi 9.1.1.0](https://pypi.org/project/pyvmomi/) | Apache-2.0；可选 vSphere SDK，标准库不能替代其对象协议。当前有维护发布，升级必须核对目标 vCenter API 兼容性。 |
| [pymssql 2.4.6](https://pypi.org/project/pymssql/) | LGPL-2.1；可选 SQL Server 驱动，依赖 FreeTDS，标准库没有相应 DB-API 驱动。当前有维护发布，升级需验证目标平台 wheel 和实际查询。 |
| Hatchling 1.33.0 | MIT；仅构建环境使用，负责 wheel/sdist 与脚本入口，运行进程不依赖构建工具。升级需重新验证独立安装产物。 |

可选驱动不会成为普通推送的启动依赖。锁定与安装通过不表示生产漏洞审计、厂商联调或完整 KAC 平替验收完成。

## 接收失败定位

接收接口只返回固定错误消息，具体原因查看 Linkd 的 `module=collector` 日志：
`source_lookup` 表示来源配置读取失败，`source_intake` 表示接收准备/持久化失败，
`source_process` 表示插件执行、结果保存、事件发布或进度提交失败，具体步骤保留在 `error` 错误链中。
已取得可信身份时携带 `bk_tenant_id`、`event_source_id`、`event_source_version` 和 `request_id`。
来源尚未匹配时只记录外部接入 `source_id`，不能从请求正文推断租户。
鉴权失败和非法请求不作为服务内部故障记录；日志不包含请求正文、X-Secret 或插件配置。

## 调试需要历史告警的插件

IMC、UC 等插件的去重或恢复计算需要活动告警；日志易还需要最近来源告警。`debug` 与 `serve` 均使用相同的 Linkd 查询适配器，可以显式提供查询服务和凭据：

```bash
PYTHONPATH=python python3 -m linkd_runtime debug \
  --request /path/to/recovery-request.json \
  --plugin-file /path/to/plugin.py \
  --linkd-url http://linkd-query:8080 \
  --linkd-token-file /path/to/query-token
```

内置插件省略 `--plugin-file`。请求中设置插件支持的 `mode`、准确的 `bk_tenant_id`、`source_id`，恢复计算还必须提供固定的 `received_at`。查询地址和认证文件来自 CLI 部署参数，不能由业务 payload 覆盖。该入口会访问 Linkd 查询服务及插件自身的上游接口，但不会发布事件或提交候选进度。

没有配置查询能力、鉴权失败或查询服务不可用时，调试退出码为 1，不输出假成功的空恢复批次。成功时检查 stdout 中的原始 `fingerprint`、`resolved/closed` 动作和固定发生时间，再接入采集链路。查询 token 不进入请求 JSON 或调试结果。

`--linkd-token-file` 文件应包含已签发的 Internal-Token JWT，可带或不带 `Bearer ` 前缀；不要填 Worker Token、执行器接入 token 或 JWT 签名密钥。每次查询（包括分页）重新读取文件，签发方应在有效期内更新文件或 Kubernetes Secret，常驻进程无需重启。该接口沿用 Linkd 管理 JWT 认证，不应把此凭据描述为受限只读 token；Python 不持有签名密钥。查询禁止跟随 HTTP 重定向。

### 正式查询路由联调

```bash
LINKD_RUNTIME_PYTHON="$PWD/python/.venv/bin/python" \
  go test -race ./internal/controlplane/api -run TestPythonQueriesSourceAlerts -count=1
```

此测试启动正式 `API.Handler`、Internal-Token 验证器和来源告警查询处理器，真实 Python 请求跨 32 条分页读取 35 条活动告警，并验证租户/来源过滤、fingerprint 与原始 meta_info 映射、UTC 时间和包含终态的最新告警查询。随后独立 Executor 使用这些查询结果生成恢复事件，验证原指纹、级别与固定 received_at。仓储为内存实现；该测试不代替 Elasticsearch/MySQL 或厂商 API 验收。

## 自定义插件的附属文件

清单入口按独立 Python 包加载，支持同目录辅助模块和数据文件：

```text
manifest.json
plugins/vendor/plugin.py
plugins/vendor/helpers.py
plugins/vendor/mapping.json
```

`manifest.json` 指向 `{"vendor": "plugins/vendor/plugin.py"}`。入口中使用 `from .helpers import ...`，辅助模块读取数据文件时使用 `Path(__file__).with_name("mapping.json")`，不要依赖进程工作目录。不同插件即使都有 helpers.py，也处于各自包命名空间；加载器不会把插件目录加入全局 sys.path。

部署时挂载整个不可变版本目录。加载失败会清理入口和已导入的附件模块；重新启动进程再加载新版本，不支持执行过程中并发重载。第三方 Python 依赖仍需预先装入运行时环境，附件支持不等于自动安装 requirements，也不等于已接通 KAC 在线附件上传。此行为已通过真实执行进程测试，覆盖同名模块隔离、JSON 文件读取和失败清理。

## 迁移时保留插件原来源身份

KAC 的 `AlarmSource.id` 与 `linkd_source_id` 是不同身份。TCOP、听云、日志易、SolarWinds 的既有指纹算法使用 `self.alarm_source_id`；迁入时在已发布来源中设置 `collection.plugin_source_id` 为原 KAC 来源 ID。Go 会把它作为独立执行字段注入，基类的 `self.alarm_source_id` 优先使用该值；未设置时使用 Linkd 来源 ID，适用于新建来源。

TCOP 缺少 sessionId/alertId 时仍沿用原拼接与 MD5 指纹算法；数字时间戳在拼接前按 collection.timezone 格式化，不依赖 Python 容器的 TZ。迁移时应设置为原 KAC 使用的时区（运行时默认 Asia/Shanghai）。缺少来源发生时间时明确报错，不使用当前时间补身份。已验证 UTC 与 Pacific/Honolulu 两种宿主时区下，数字秒、数字毫秒和对应来源时间文本生成相同指纹；这不代替真实厂商样本验收。

`Source.id`、查询条件、Redis/Kafka 作用域和协议 `source_id` 仍使用 Linkd 来源 ID，`plugin_source_id` 不改变租户权限或投递目标，也不能由外部推送正文覆盖。这只是保留既有算法的输入，不增加身份 hash 校验。真实 KAC 发布桥接尚未写入该字段，迁移配置必须显式携带原 ID，不能假设两个 ID 相同。

## 推送心跳

推送入口在调用插件构造函数前处理顶层 `{"action":"heartbeat"}`，行为与 KAC V2 接收入口一致：纯心跳返回空 events，不执行插件或推进候选游标；混合列表仅移除心跳项，保留业务项的顺序和列表形态。嵌套字段中的 heartbeat 不会被当成接入心跳，空列表也仍交给插件。插件名称和执行模式仍先检查，心跳不能绕过未知插件或不支持模式的错误。

上述行为仅覆盖执行与返回计数；KAC 来源页面的 heartbeat_time、运行状态和错误信息回写仍待管理链路接入。

迁移来源还可以在 collection 中设置 `source_name`、`timezone` 和 `initial_state`。`source_name` 提供原 KAC 展示名称；时区应使用 IANA 名称（如 Asia/Shanghai、UTC）；`initial_state` 可携带原 `meta_info` 和 ISO 格式 `activate_time`。首次拉取没有已提交进度时才使用该初始状态，Redis 中已有进度始终优先；后续修改配置不会覆盖运行进度。

SkyWalking 的窗口终点及候选游标固定使用请求 received_at，同一请求重试不重新取当前时间；缺少该时间时在调用上游前失败。初始窗口与历史游标格式化均使用来源时区。

## 辅助查询的默认返回值

所有已迁入内置插件均支持 `fields` 和 `metric` 辅助模式，不产生告警事件或采集进度。`fields` 调用类的 `get_custom_msg_fields()`，默认返回 `[]`，BS_MONITOR 返回 `["close_reason"]`。查询字段不实例化插件，不要求伪造推送正文。

`metric` 沿用 KAC 基类的默认 `{}`，表示插件没有真实指标实现，不表示上游查询成功或某个时间段没有数据。继承默认方法的推送插件不为此解析空业务正文；覆盖了 `get_metric` 的插件仍走真实实现。BS_MONITOR 缺少指标宿主时明确报错，不能回退成默认空对象。自定义插件要在 MODES 中显式声明辅助模式；指标实现的构造方式为 `AlarmClient(source)`。

### BS_MONITOR 指标宿主配置

```bash
PYTHONPATH=python python3 -m linkd_runtime debug \
  --request /path/to/metric-request.json \
  --metric-url http://kingeye:8000/kac/internal/linkd/plugin-queries/ \
  --metric-token-file /path/to/kac-internal-token
```

宿主地址是完整 API 地址，包含部署的外部路径前缀。凭据文件包含已签发的 KAC Internal-Token JWT，每次请求重新读取；不把签名密钥传给执行器。相同参数可用于 serve。2.x 不内置 Helm Chart；部署侧通过 Secret 文件挂载和上述 CLI 参数注入，默认不配置。

协议为 POST `{operation, bk_tenant_id, params}`，返回 `{data}`。operation 仅允许 recovery_fields（alarm_id、record_id）、query_builtin（alarm、ISO start_time/end_time）、query_points（query）。KAC 使用明确租户上下文复用现有恢复、历史策略与 KMC operation，不调用旧告警源插件，不发布告警、不写来源状态或游标。只有 data=null 表示历史服务确认无执行引用，HTTP 错误、缺少 data 或响应超预算都失败，不转为旧参数取数。运行时响应读取预算沿用 max-result-bytes。

验证覆盖真实 HTTP 协议、spawn 进程能力注入、CLI 指标调试、认证文件轮换、失败不回退与字节预算；KAC 侧协议/租户委派和历史指标回归通过。KMC、历史数据库仍使用测试替身，未启用现有来源或执行部署切换。

## 发布失败与恢复预算

Python 计算与 Kafka 发布分别保存尝试次数，各最多三次。发布恢复复用首次完整输出，不重新执行插件；每次 Service 调用只尝试一次批次发布，不增加隐藏队列或无界等待。Kafka 明确不可重试的协议/消息错误直接终结；临时或结果未知的错误在剩余预算内等待恢复，耗尽后记录脱敏终结错误并移出待处理集合，后续周期可继续。

终结发布失败不提交候选游标，也不能回滚可能已被 Kafka 接收的部分事件。错误日志保留底层原因，返回结果不报告成功。Redis 预留发布次数失败时不发送；完成记录写入失败时仍保留错误，不宣称终结已持久化。

## 推送接收并发预算

`collector.max_concurrent_requests` 限制每个控制面进程同时处理的来源推送请求，省略或 0 使用 4。预算覆盖来源查询、正文读取、持久接收、同步计算和发布；满载立即返回 HTTP 503，不读取正文、不创建接收记录，也不排队等待。请求结束、失败或取消均释放槽位。

该参数与 max_request_bytes/max_response_bytes 共同限制活跃请求资源，不是 Redis 持久积压配额，也不保证精确的进程 RSS。它独立于 Python 每副本 processes×threads 和 Go 拉取 worker 的任务数量；扩容时应分别配置三者。

### 共享 REST 接入

同一个接入 URL 可配置多个租户绑定，Secret 只用于宿主鉴权，不传入 Python。下面是 EventSource 的 collection 片段；来源级 related_tenant_id 保持为空，Kafka/清洗配置仍按正常来源配置。

```yaml
collection:
  plugin: rest_api_push
  mode: push
  tenant_bindings:
    - bk_tenant_id: tenant_one
      enabled: true
      secret: tenant-one-ingress-secret
      plugin_source_id: original-kac-source-one
      source_name: 租户一接入
      configs: {}
    - bk_tenant_id: tenant_two
      enabled: true
      secret: tenant-two-ingress-secret
      plugin_source_id: original-kac-source-two
      configs: {}
```

调用方用相应 X-Secret 请求 `/api/v1/ingest/sources/{source_id}`。每个绑定提供自己的完整插件 configs，不继承根级凭据；修改来源时可以重排绑定，脱敏占位符按租户 ID 恢复。停用绑定或发布新 Secret 后，旧 Secret 不再接受新请求。该模式不支持定时拉取/恢复，且不会自动开启部署或替换现有 KAC 接入。

### 同步接收的服务退出行为

`collector.timeout_seconds` 约束接收处理（插件执行、结果保存和发布）的总时间。控制面 HTTP 写期限取 `max(25 秒, 接收预算 + 5 秒)`，避免业务仍在预算内时先关闭响应连接；读取请求头和正文仍分别限为 3 秒与 10 秒，不随插件执行预算放大。

控制面服务任务取消会传播到正在处理的 HTTP 请求，包括调度中心失去授权触发的任务组取消。服务停止接收新连接，最多等待 30 秒排空，排空失败后关闭连接。取消能阻止尚未开始的执行阶段，不能撤销已经发出的 Kafka/Redis 请求；推送 Redis 写入另外由既有领导权原子保护，已发出的 Kafka 请求仍依靠稳定身份处理重投。

### 从 KAC 导出迁移配置

在有权读取目标租户来源的 KAC 管理环境中运行以下命令（使用该环境既有的 Python/配置启动方式）：

```bash
python src/manage.py export_alarm_source_runtime \
  --tenant TENANT --source KAC_SOURCE_ID --plugin uc \
  --timezone Asia/Shanghai --output /secure/source-runtime.json
```

输出仅含 event_source_id、related_tenant_id 和 collection，需合并到对应 Linkd 来源的完整配置，不是完整 EventSource，也不自动发布。单租户来源保留原 ID、名称、接收密钥、插件参数、拉取/恢复周期和初始游标。共享 REST 来源每次只导出当前租户的一行 tenant_bindings，必须汇集所有租户后才可替换完整列表。现有启停和 Celery 调度均不改变，当前 KAC 来源同步也不会自动添加 collection。

文件包含解密后的实际参数，按 0600 权限独占创建，拒绝覆盖已有文件，不输出正文到终端。--plugin 必须是已经迁入的内置实现或部署清单中的插件名；命令不转换旧源码和附件，也不运行插件。Zabbix 自动带出来源类型规则。使用 --extra-configs /secure/extra.json 可补齐原来的全局宿主参数，显式值覆盖同名来源参数，例如 bk_monitor 所需 alarm_url、app_code、app_secret、username。不要把凭据直接放在命令参数里。

应在实际部署切换时重新导出游标；开发期间导出的游标不会随着仍在运行的 KAC 自动更新。默认不启用新 Collector/运行时，执行导出不会改变这一约定。

### 完成记录保留期

`collector.completed_retention_seconds` 默认为 604800（七天），配置 0 也使用默认值。终结事务为首次输入快照、结果、执行尝试次数和发布尝试次数设置 TTL，并删除该推送请求在 Stream 中的重复正文；保留期内 Load/LoadRequest 仍可从快照读取首次输入。失败终结同样清理，且不推进来源游标。

未完成请求、待处理索引和来源游标不设置 TTL，不按该保留期丢弃故障积压。保留期从终结时开始，普通完成结果查询不续期。过期后首次输入和终结结果不可再查询，也不提供永久请求去重保证；部署时按排障和重放需要选择保留时间。终结、索引移除及设置 TTL 已在真实 Redis 中验证，包含成功/失败、待处理记录不受影响和游标保留。

### 拉取任务交接的写入保护

每个 collector 任务创建绑定自身 worker/epoch 的写入器和 Kafka 工厂，退出时关闭工厂。Redis 写入在同一次 Lua 调用中读取现有 taskdispatch 协调快照，确认任务仍属于原 worker/epoch、来源和角色，阶段为 starting/running 且未过期，然后才写入输入、结果、次数或游标。没有新增锁、授权副本或心跳流程；存储不自行解析调度私有结构，解释由 taskdispatch 提供。

授权过期时间每次取当前协调快照，使用 Redis 服务端时间比较，不冻结任务启动时的 Expires，也不依赖 worker 的本机时钟。RFC3339Nano 的时区和毫秒转换已对照 Go time.Parse 验证，包括闰日与正负时区。失授权会直接结束任务及其恢复流程，不能把它当成普通插件错误再等待下一周期。

真实 Redis 测试覆盖续租、starting/running、停止、过期、删除、worker/epoch/来源/角色改变；还模拟了旧 Context 未取消、旧执行结果在新代次提交后才返回，确认旧结果和游标不能覆盖新记录。已经发出的 Kafka 请求仍可能完成，按稳定消息身份处理重投；本地双 collector 进程及 Elasticsearch 链路已完成联调，见下方接管 E2E。

### 真实接收与生命周期链路验证

[独立 Python 接收链路 E2E](../../tests/e2e/allinone/README.md#独立-python-接收链路-e2e) 使用真实 Python/Linkd 进程、Kafka、Redis 与 Elasticsearch，已经覆盖单租户和共享双租户的触发/恢复、租户注入、accepted Event、Alert/AlertLog 持久化及接收索引清空。普通单元测试不会隐式连接这些服务，需要显式设置 LINKD_E2E=1。

该验证发现并修复了推送恢复任务未登记到控制面任务目录的装配缺口。collector-push-recovery 仅在配置 collector 时启用；任务目录只展示批量和超时预算，不展示运行时地址或凭据。授权丢失直接终止所属 dispatch 任务组，不在旧会话内重新启动恢复任务。厂商实连和 MySQL 插件完整链路仍未由此用例证明；拉取进程接管由独立用例验证。

### 真实拉取与接管验证

[拉取调度与 collector 进程接管 E2E](../../tests/e2e/allinone/README.md#拉取调度与-collector-进程接管-e2e) 已通过。两个独立 Go collector 进程共用正式 taskdispatch 与 Python 进程池；暂停当前 owner 后，按既有租约到期交接，由继任者恢复原请求。旧进程恢复至少两轮心跳后，已完成结果与游标保持不变，旧计算结果未写入事件库。恢复事件进入真实 Kafka、Cleaner、Redis Mailbox、Lifecycle 和 Elasticsearch。

该测试没有缩短生产调度或租约：分钟周期、60 秒授权和接管安全间隔均使用现有实现。它使用受控 HTTP 上游与测试插件，不是厂商实连或生产部署验收。
