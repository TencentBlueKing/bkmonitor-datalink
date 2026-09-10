# Linkd

Linkd 是独立的 Go 告警接入和生命周期处理项目。当前领域事实以
[`docs/design/define.md`](docs/design/define.md) 为唯一权威：异构来源消息经过来源 Cleaner 标准化为
`domain.Event`，Event 持久化后由生命周期处理器按 fingerprint 创建或推进 `domain.Alert`，真实操作和
输出记录为不可变 `AlertLog`。

项目处于早期开发阶段，没有稳定 Go API 或历史版本兼容承诺。当前配置、领域身份和物理资源直接描述
当前实现；早期草案中的 `AlertEvent`、固定 RawEvent 映射、
`severity_priority`、`revision` 和 `terminal_event_id` 不属于当前模型。

## 当前链路

```text
MQ delivery
  → RawEventMessage
  → EventSource.cleaner.type 选择 SourceCleaner
  → 多 worker 并发生成 EventDraft
  → 通用事件工厂：租户、severity、fingerprint、稳定 event_id、时间 fallback
  → 各 lane 恢复连续顺序并独立批量持久化 Event
  → 各 lane 将仍为 unprocessed 的 Event ID 写入 Redis Mailbox
  → 确认原 MQ 消息
  → Mailbox Signal + fingerprint lease
  → Elasticsearch Recent Alert 缓存优先裁决
  → Alert 创建/更新/升级/抑制/终态
  → AlertLog + Kafka V1 Alert change
```

- Event action 仅 `triggered | resolved | closed`。
- Alert status 仅 `active | recovered | closed`，后两者不可重新打开。
- 同等级 `triggered` 只推进生命周期字段；更高等级关闭旧 Alert 并创建新 Alert；更低等级 Event 被抑制且不修改 Alert。
- accepted 与 suppressed Event 写入 `related_alert_id`；suppressed 不推进 Alert，orphaned/rejected 保持为空。
- Enricher 在 Alert 创建前同步执行一次；未配置规则视为 succeeded 空结果，错误降级为 failed。
- MySQL 和 Elasticsearch 都只承诺单对象 CAS；跨对象步骤依赖稳定身份和幂等流水恢复。Cleaner 确认
  原消息后不再扫描 Event 补发 Signal，因此 Redis Mailbox 必须依靠自身持久化和复制保证已确认数据。
- Elasticsearch Event create 使用 `refresh=false`；Cleaner 在主分片确认后入 Mailbox，Lifecycle 和重复
  Event 核对通过 realtime GET 读取，不依赖 `_search` 可见性。
- Elasticsearch Lifecycle 使用共享 Redis Recent Alert 缓存跨越可配置的 search refresh 窗口；Alert create/CAS
  使用 `refresh=false`，缓存写入失败时 Event 保留在 Mailbox 重试。MySQL 不启用该缓存。
- Elasticsearch 使用控制面进程内三个独立任务分别执行 Schema 与 Active 资源对账、时间桶维护和 Alert 归档：
  Event 按 received_at，AlertHistory 与 AlertLog 按 Alert 创建锚点分桶；终态 Alert 由有界 Worker 连续批量从
  Active 热索引幂等归档到 History，积压期间批次之间不等待固定周期。
- 控制面可监控 Redis Signal Stream 的长度、内存、Consumer Group、PEL 和 lag，并在超过软上限时有界
  裁剪所有 Group 都已确认的连续前缀；未读和 Pending Signal 不会为满足长度目标而删除。

## 目录

```text
cmd/linkd/            Linkd 服务进程入口
cmd/linkd-eventgen/   Standard Event 模拟器入口
configs/     示例配置
console/     运行观测与来源管理控制台
internal/    领域、Cleaner、生命周期、存储和运行时实现
docs/        设计、配置、协议、调研和审查记录
tests/       数据生成和 all-in-one E2E
```

文档导航见 [`docs/README.md`](docs/README.md)，配置见
[`docs/guides/configuration.md`](docs/guides/configuration.md)，存储边界见
[`docs/design/core-storage-contract.md`](docs/design/core-storage-contract.md)，生命周期见
[`docs/modules/lifecycle.md`](docs/modules/lifecycle.md)。后续部署形态统一为测试和小规模场景使用的
`all-in-one`，以及 Cleaner、Lifecycle、控制面（API / Leader / Manager）三进程模式；当前实现与目标
边界见 [`docs/design/deployment.md`](docs/design/deployment.md)。

## 动态来源与任务调度

EventSource 通过控制面 API/自定义 provider 管理，显式 `linkd event-source import --file <yaml>` 导入；常驻进程不自动加载文件中的来源。
Cleaner/Lifecycle 按来源和标签分配多副本，数量默认 all，支持 0 和 enabled 总开关；Cleaner 自动受 Kafka partition 数限制。
Event/Alert 保存实际使用的 event_source_version，来源级 Stream 支持多个 Lifecycle consumer。
启动前设置不同的 LINKD_API_TOKEN / LINKD_WORKER_TOKEN；Console 的 Event Sources 页面通过正式 API 修改配置。

实现和边界见[来源管理](docs/design/event-source-dynamic-configuration.md)与[调度协议](docs/design/task-scheduling-protocol.md)。

## 本地开发

要求 Go 1.26.7、golangci-lint v2.13.2，以及已安装依赖的 Node.js/pnpm 环境。

```bash
go run ./cmd/linkd config validate --config ./configs/linkd.yaml
go run ./cmd/linkd run control-plane --config ./configs/linkd.yaml
go run ./cmd/linkd run cleaner --config ./configs/linkd.yaml
go run ./cmd/linkd run lifecycle --config ./configs/linkd.yaml
go run ./cmd/linkd run all-in-one --config ./configs/linkd.yaml
go run ./cmd/linkd-eventgen --config ./configs/linkd.yaml --event-source-id demo-source --tenant-id tenant-a
make check
```

仓库内配置不保存凭据。本机服务需要认证时，复制对应示例为被 Git 忽略的
`configs/linkd.local.yaml` 或 `configs/linkd.pm2.local.yaml`，只在本地副本中填写凭据，并通过
`--config` 或 `LINKD_CONFIG` 选择该文件。具体步骤见[配置指南](docs/guides/configuration.md)。

独立模拟器的速率、生命周期、场景和重启边界见
[`docs/guides/event-generator.md`](docs/guides/event-generator.md)。

需要同时托管一个 all-in-one 和两组差异化 Standard Event 模拟器时，使用
[`ecosystem.config.cjs`](ecosystem.config.cjs)，构建、启动和运维命令见
[`docs/guides/pm2.md`](docs/guides/pm2.md)。

`cleaner`、`lifecycle` 和 `all-in-one` 都传播 Context 取消并执行有界排空。真实 MySQL、Elasticsearch、
Redis 和 Kafka 集成验证由 `tests/e2e/allinone` 提供，需显式配置对应环境变量。

## 容器镜像

`pkg/linkd` 可独立构建镜像，不依赖仓库根目录预编译。运行时基础镜像为
`tencentos/tencentos4-minimal`。

```bash
make image IMAGE_TAG=dev VERSION=$(git rev-parse --short HEAD)
# 或
docker build -t linkd:dev --build-arg VERSION=$(git rev-parse --short HEAD) .
```

以上示例统一构建 `linkd:dev`，二进制版本使用当前 Git 短哈希。不指定 `IMAGE_TAG` 时，
`make image` 默认以当前 Git 短哈希作为镜像标签，运行时应使用对应标签。
`make image` 和 `make console-image` 都自动传入完整 `GIT_COMMIT`，并将 VERSION 与 commit 写入产物。
直接使用 Docker 构建时，需同时传入 `--build-arg VERSION=<version>` 和 `--build-arg GIT_COMMIT=<full-sha>`。
两个镜像都支持追加 `version` 查询版本号及 commit；未注入的构建信息显示 dev / unknown。

构建缓存：两个 Go 镜像的依赖下载层不依赖版本、commit 或目标架构；依赖文件和基础镜像等
输入不变且缓存可用时，`go mod download` 应显示 `CACHED`。Go 编译使用 BuildKit cache mount，
同一 builder 可复用编译缓存；GitHub Actions 的 `type=gha` 默认只恢复镜像层，不恢复 cache mount，
因此临时 runner 上的 Go 增量编译缓存仍需额外配置。Console 将生产依赖裁剪与源码编译分开缓存，
仅修改版本信息不会重新安装依赖或编译前端。构建日志会列出缓存命中的步骤，不能仅凭步骤名称判断重新执行。

默认启动 `run all-in-one`。镜像不包含可用配置，必须把配置文件挂载到
`/data/linkd/configs/linkd.yaml`，或用 `--config` 指定其他路径：

```bash
docker run --rm -v /path/to/linkd.yaml:/data/linkd/configs/linkd.yaml linkd:dev
docker run --rm -v /path/to/linkd.yaml:/data/linkd/configs/linkd.yaml linkd:dev \
  run cleaner --config /data/linkd/configs/linkd.yaml
docker run --rm linkd:dev version
```

容器终止宽限期建议至少 60 秒。当前镜像只提供与本地进程相同的启动入口，不表示生产部署方案已完成。

Kubernetes 部署见 [Helm 指南](docs/guides/helm.md)：Chart 仅支持三角色独立部署，
可按 worker 组配置标签、副本、资源与 Secret，另外提供可选 Console、Ingress 和 Basic Auth。

Linkd Console 使用独立镜像，在当前目录执行 `make console-image IMAGE_TAG=dev` 构建
`linkd-console:dev`；也可执行 `docker build -t linkd-console:dev console`。
配置挂载和启动方式见 [Console README](console/README.md#容器镜像)。

GitHub Actions 支持手动选择构建 Linkd、Console 或两个镜像并推送到 GHCR；没有自动触发事件。
参数、权限和版本标签约定见 [手动构建与发布镜像](docs/guides/image-release.md)。
