# 配置驱动事件生成器

`linkd-eventgen` 是独立测试进程，通过 Kafka 进入真实 Cleaner、Lifecycle、Enrich 和输出链路，不直接写 Event、Alert 或 AlertLog。配置包括数据表、模板、场景及资源预算；Admin 在 Linkd 模块的「事件生成器」管理任务。架构与动态加载边界见[设计](../design/eventgen.md)。

eventgen 通常只用于测试环境，不能作为正常 Linkd 部署的依赖。Helm 的 `eventgen.enabled` 默认 `false`；开启时同步启用控制面的 `dispatch.eventgen_enabled`。单独部署生成器时，托管管理需在控制面显式设置该配置为 `true`，未启用时不打开生成器存储、不创建集合或运行相关后台任务。生成进程离线不影响 Linkd 健康和事件处理。PM2 也默认不启动生成器，测试时需显式设置 `LINKD_EVENTGEN_ENABLED=true`。

## 启动与管理

```sh
# 本地配置校验与预览不连接 Kafka。
go run ./cmd/linkd-eventgen validate --config configs/eventgen/hosts.json
go run ./cmd/linkd-eventgen preview --config configs/eventgen/hosts.json --count 10

# 本地生成，来源连接取自 Linkd 配置；每 3 秒检查生成配置文件的修改时间。
go run ./cmd/linkd-eventgen run --config configs/eventgen/hosts.json --sources configs/linkd.yaml

# 托管模式：Worker Token 通过环境注入；启动后等待 Admin 显式启动任务。
# LINKD_EVENTGEN_WORKER_TOKEN 应从已有 Secret 或本机私有环境配置提供。
go run ./cmd/linkd-eventgen serve --control-plane http://127.0.0.1:8090 --poll-interval 3s
```

[主机与多级别生命周期示例](../../configs/eventgen/hosts.json)展示整行遍历、关联模型、固定维度、随机测量值、升级、下降及概率恢复/关闭。[常用场景集合](../../configs/eventgen/scenarios.json)替代原来的场景枚举，字段和值均可编辑。示例中的主机是虚构数据，使用前替换为测试租户的真实数据，并将 `event_source_id` 改成实际来源。Standard 模板应绑定启用的 Standard 来源；自定义 JSON 使用匹配的其他 Cleaner。

Helm 使用[托管进程示例](../../deploy/helm/linkd/examples/eventgen.yaml)，默认关闭、单副本 Recreate，镜像独立版本为 0.2.0。旧的场景、速率命令行参数及 `eventgen.instances` 已移除，不保留第二套生成引擎。

## 数据表与模板

任务内 `datasets.<name>.rows` 是 JSON 对象数组。CSV 可按列指定 `string/number/boolean/json`，未指定为字符串。超过 JavaScript 安全整数范围的 ID 应使用 string，避免浏览器编辑时损失精度。加权表使用 `weight_column`，所有参与行的权重必须为正数。

`bindings` 按声明顺序选择数据行。`mode` 为 `random`、`weighted` 或 `iterate`；遍历的 `loop: false` 表示完成一遍后停止新增。`where` 的键是表列，值为生成 JSON 值的模板，可引用前序绑定，例如 `"model_id": "{{json .Bindings.host.model_id}}"`。关联索引在加载时建立，发送时不扫描整张表。遍历游标按场景、绑定和筛选组合隔离。

`instance` 是在告警创建时执行一次的 JSON 模板，存入 `.Instance`；选中的 `.Bindings` 同样保持整个生命周期稳定。事件 `body` 则每次执行。

| 模板变量/函数 | 行为 |
| --- | --- |
| `.RunID / .TenantID / .ScenarioID / .Sequence` | 运行、租户、场景和告警序号 |
| `.Fingerprint / .EventID / .EventSequence` | 稳定关联身份及本次事件身份 |
| `.Bindings.host / .Instance / .Parameters` | 数据行、创建时变量、当前阶段参数 |
| `.Stage / .ScheduledAt / .OccurredAt / .ProducedAt` | 阶段与逻辑事件时间 |
| `json value` | 保留 JSON 类型和转义，插入模板时不要再套引号 |
| `randInt min max` | `[min,max)` 整数 |
| `randFloat min max` | `[min,max)` 小数 |
| `chance percent` | 按百分比返回布尔值 |
| `pick value...` | 均匀选择一个值 |
| `weighted value weight...` | 按成对的值与权重选择 |
| `addTime time duration / formatTime time layout` | 时间偏移及 Go layout 格式化 |

`kind: standard` 自动填入租户、event_id、fingerprint；缺少 occurred_at/produced_at 时补入调度时间。可在场景 `fingerprint` 中显式定义关联身份。自定义 `kind: json` 保持模板正文，只要求结果为 JSON object。Kafka key 默认 `tenant:fingerprint`，可由模板修改；租户、message_id 和 order_key headers 由生成器管理，其他 headers 可配置。

## 调度与生命周期

`arrival.per_second` 表示每秒新增告警数；`arrival.every` 加 `batch` 表示按周期批量新增，两种方式互斥。`delay` 为启动延迟，`count: 0` 不限制新增数；`rates` 按 `at` 递增配置分段速率。任务 `start_at` 可延迟开始，`duration` 到达后停止新增并收尾。

阶段按 `stages` 顺序执行；进入阶段即发送一次，随后按 `every` 重复，达到 `duration` 时进入下一阶段，边界不重复发送旧阶段。时长既可用 `{fixed:"10s"}`，也可用 `{min:"5s",max:"15s"}`。显式 `next: [{stage:"recover",weight:8},{stage:"close",weight:2}]` 覆盖默认顺序，可以回到之前的阶段形成循环，空 stage 表示结束编排。`terminal: true` 表示该阶段消息已经结束来源告警，不再自动追加收尾。

升级发送高等级 triggered。下降需要高级别 resolved 和低级别 triggered，Linkd 会结束旧 Alert 并创建低级别 Alert；单独发送低等级 triggered 不代表原地降级。

`occurred_offset` 允许正负时长；`send_delay` 延迟实际投递，配合随机范围可产生显式乱序。`duplicate_percent` 追加完全相同的 delivery，保留正文、身份和时间，不重新随机渲染。

## 热加载、停止与失败

- 修改新增速率或发送限速直接作用于后续调度；修改数据、模板、生命周期或来源时，先编译新配置，再停止新增、排空旧消息并关闭旧轮次，最后整体切换。任务改名不打断运行。
- Standard 收尾使用原 fingerprint 发送 `__ALL__ closed`；自定义 JSON 场景必须配置 `cleanup_template`。失败保留原始收尾消息，显式重试仍使用同一身份和正文。
- 暂停冻结逻辑时钟；继续从剩余时间执行。正常停止、有限运行结束、SIGINT/SIGTERM 默认收尾。强制停止保留未结束的来源告警。
- 操作受理、配置请求版本、实际生效版本、Kafka ACK 和下游告警结果分别展示。收尾失败不报告切换成功，可以重试收尾或强制停止。
- 进程重启后原运行中断，必须显式启动新运行。控制面连接连续失联 60 秒后停止新增并收尾，不自动重开。

`limits.active` 限制活动告警数；`queue_records/queue_bytes` 同时覆盖延迟消息和在途发送；`max_message_bytes` 限制渲染输出；`batch` 控制发送批次，`send_per_second` 为可选总发送上限。达到预算会背压，统计显示实际速率、队列字节和调度延迟。消息错误只影响当前投递；明确临时发送错误最多尝试三次，不回滚同批已成功消息。`send_timeout/cleanup_timeout` 限定发送和退出收尾等待。

## API 与验证

管理 API 使用既有管理 JWT：`/api/v1/eventgen/tasks`、`/tasks/{id}`、`/tasks/{id}/start`、`/runs`、`/runs/{id}`、`/runs/{id}/control`、`/preview`、`/sample/preview` 和 `/import`，均显式携带 `bk_tenant_id`。任务读取结果可导出，再通过保存接口导入；样本预览调用正式 Cleaner/Enrich，不保存业务实体。Admin CLI 的 `api list` 可发现 `linkd.eventgen.*` 操作。内部心跳 `/internal/eventgen/heartbeat` 只接受 Worker Token，敏感来源连接不会返回浏览器。

```sh
go test -race ./internal/eventgen/... ./cmd/linkd-eventgen
LINKD_TEST_KAFKA_BROKER=127.0.0.1:9092 go test ./internal/eventgen -run TestKafkaPublisherIntegration -v
LINKD_TEST_ELASTICSEARCH_URL=http://127.0.0.1:9200 go test ./internal/eventgen/storage -run TestElasticsearchEventgenContract -v
# 显式测试 broker；创建并清理独立临时 topics，依次运行三档各十分钟。
LINKD_TEST_KAFKA_BROKER=127.0.0.1:9092 LINKD_EVENTGEN_LOAD_SECONDS=600 go test ./internal/eventgen -run '^TestKafkaLoad$' -timeout 35m -v
```

Kafka 压测包含生成、发送和独立 consumer 排空，不能替代真实环境的 Cleaner/Lifecycle/Enrich 全链路压测。环境联调应使用实际主机与模型数据，通过事件、告警、流水和丰富结果分别留证。
