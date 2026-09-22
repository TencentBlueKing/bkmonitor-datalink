# Linkd Go 包结构与依赖边界

日期：2026-09-22。状态：与当前工作区实现一致；本次仅调整内部组织，不改变配置、HTTP、存储和 Kafka 协议。

## 1. 组织原则

顶层按已有业务能力和有真实复用的技术能力组织。一个能力可以包含多个 Go 包；文件夹数量不作为拆分目标。
同一能力的编译、执行、调试和集成适配归入同一目录。本次丰富执行与预览的外部连接由装配层创建和管理，核心用例消费窄接口。

```text
internal/
  domain/                    # Event/Alert/补丁约束与基础值类型
  cleaner/                   # 接入清洗
  lifecycle/                 # 生命周期裁决及其消费端口
    process/                 # 生命周期进程装配
    kachook/                 # KAC Kafka 输出运行时
    kafkahook/               # 通用 Alert Kafka 输出运行时
  enrich/                    # 独立丰富能力
    types.go                 # 共用 Input/Result/ChainKind
    custom/                  # CMDB/fields 规则编译与执行
    preview/                 # 不保存的预览用例
    view/                    # 根据步骤输出合成临时视图
    kingeye/                 # 纯 KAC 协议和内置结果投影
      convert/               # 离线丰富配置转换
    processors/              # 内置场景及自定义规则处理器
    datasources/             # Kingeye 只读 Reader
    assembly/                # 路由注册与外部连接装配
    models/, rules/          # 内置场景模型、常量和判断
    basetarget/, collect/, uptime/
  onemodel/                  # 独立 OneModel SDK
  eventsource/               # 配置编辑、发布与不可变 Release
  taskdispatch/              # 任务规划、Worker 协议客户端和授权
  controlplane/
    api/                     # 管理及 Worker HTTP 服务端
    process/                 # 控制面进程及预览连接装配
  config/                    # 配置数据、加载和校验
  dynamicconfig/             # 上游配置同步和持久化
  runtimeconfig/             # 进程内有效配置快照
  store/                     # 持久化端口、后端和连接装配
  activeindex/               # 策略活动告警索引
  consume/                   # 消息消费及传输适配
  kafkaclient/, redisclient/  # 实际共享的连接和协议参数
  jsonpath/                  # 通用路径读取、确定路径与预算约束
  telemetry/, logging/       # 观测和日志运行时
  taskgroup/                 # 有界任务退出协调
  cli/, eventgen/, testkit/  # 命令行、压测生成和测试支持
```

## 2. 丰富的调用方向

- Lifecycle 定义自己的 `AlertEnricher` 消费端口，使用 `enrich.Input/Result`。新 Alert 的执行时机和失败处理仍由 Lifecycle 决定。
- `enrich` 核心只依赖领域对象、纯结果映射、规则和 Reader 端口，不依赖 Lifecycle、配置加载、存储装配或 Telemetry 运行时。
- `enrich/assembly` 构建固定 Release 的路由和连接，向正式执行与预览提供同一实现。
- `enrich/preview` 只接收来源读取、告警读取及执行器工厂；并发上限、输入隔离和差异计算留在用例中。
- `controlplane/process/enrich_preview.go` 创建预览所需的只读 Repository 和丰富运行时，负责关闭连接。
- KAC Hook、策略索引、预览通过 `enrich/view` 读取历史值与补丁。索引只合成标签，不要求加载正文数组。

Enrich 已有独立预览入口，因此提升为顶层能力。原 `lifecycle/enrich`、`enrichrule`、`enrichpreview`、
`enrichconvert` 不保留转发包。`lifecycle/process/enrich.go` 只保留当前进程需要的配置校验，不再包装共享运行时。

## 3. Domain 与 Kingeye 边界

Domain 保留 `EnrichPatch`、可写目标白名单、类型校验和补丁的原子应用，不认识具体 Processor 名称。
旧 `value` 如何变成补丁、`display.object` 如何对应 `subject_name`、内置权限列表空缺省值的含义，
统一放在 `enrich/kingeye`。`enrich/view` 负责有序提取及合成，兼容真实历史数据，不回填、不双写。

KAC 的固定 Alarm JSON 字段及自定义字段映射校验也归入 `enrich/kingeye` 的纯协议定义。
`lifecycle/kachook` 负责转换告警与 Kafka 发送。配置校验可引用纯协议，但不导入发送实现。
OneModel 保持独立，不依赖 Kingeye 缓存、Enrich 或 Lifecycle。

## 4. 控制面与调度边界

`controlplane/api` 统一提供来源管理、运行态、动态配置、预览和 Worker HTTP 服务端，保留原 URL、鉴权、状态码及响应结构。
它通过 `Controller` 窄接口读取调度快照和处理心跳。`taskdispatch` 负责调度、Worker 生命周期、协议 DTO 和客户端，
不引用 API 服务端、预览用例或丰富装配。

原 HTTP/凭据保护测试迁入 API 包；控制器内部 CAS 测试仍留在 taskdispatch。
Worker/API 的 Redis 集成测试迁入 API 包，使用独立命名空间及 WATCH 更新测试规划快照，继续验证真实 HTTP 握手和断联停止。

## 5. 配置与共享参数

- `config.TelemetryConfig` 及其子类型只定义 YAML 配置和校验；`telemetry` 消费这些参数创建 SDK/监听器。
- `kafkaclient.ProducerConfig` 为通用 Kafka 输出提供参数校验；配置包不再通过 Kafka Hook 运行时获取校验能力。
- `config` 仍可以调用无 I/O 的规则编译和纯协议校验，保持发布前发现错误的行为。
- `dynamicconfig` 管理同步/持久化，`runtimeconfig` 提供原子有效快照；两者继续分开。

## 6. 验证与维护

[包边界测试](../../internal/enrich/boundary_test.go)检查核心、预览、Domain、配置和调度的生产导入，防止未来新增功能重新引入运行时装配依赖。
该检查仅保护代码组织；业务行为仍由既有单元、HTTP、竞态和集成测试验证。

本次验证记录：

- `make check` 通过：格式、普通测试、vet、race、golangci-lint、Console 检查和构建、Helm、发布脚本；静态分析 0 issues。
- 独立临时 Redis 上执行 `go test -race -count=1 ./internal/controlplane/api ./internal/taskdispatch -run 'Integration$' -v`：4 项通过，包括 Worker 停止握手、断联自停、协调状态和超时重新分配。测试后关闭临时服务。
- 文档本地链接检查通过，ES 兼容测试脚本的包路径已同步并通过 shell 语法检查。
- 未启用依赖真实 Kingeye、MySQL 和 OneModel ES 环境的集成测试；此次调整不替代业务环境联调。

基于当前 `go list` 的依赖快照：顶层目录从 25 个收拢为 23 个；预览包传递内部依赖从 36 个减至 22 个，
调度包从 38 个减至 23 个，配置包从 23 个减至 19 个。包数仅作验证记录，后续维护以职责和依赖边界为准。
