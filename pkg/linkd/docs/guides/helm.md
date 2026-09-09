# Helm 部署

Chart 位于 [deploy/helm/linkd](../../deploy/helm/linkd/README.md)。使用 Helm 3，
默认部署一个 Control Plane 和多个 worker 组；每组各有一个 Cleaner、Lifecycle Deployment。
Console 和多个独立 Event Generator 实例按需启用。
没有 all-in-one 参数。Redis、Elasticsearch/MySQL、Kafka、Prometheus 和 Ingress Controller 由部署方管理。

## 构建镜像与准备配置

Chart 默认使用 GHCR 的两个 `0.1.1` 镜像：

```text
ghcr.io/tencentblueking/bkmonitor-datalink/linkd:0.1.1
ghcr.io/tencentblueking/bkmonitor-datalink/linkd-console:0.1.1
```

Chart 生成的配置在缺省时自动包含 `lifecycle: {}`，使用程序默认值；可用
`configuration.lifecycle.concurrency` 显式调整并发数。使用 `existingSecret` 时，
Chart 不修改 Secret 内容，需自行在配置文件中提供 `lifecycle` 段。

需要自建镜像时，在 Linkd 模块根目录构建，再推送到集群可拉取的仓库：

```bash
make image IMAGE=registry.example.com/linkd IMAGE_TAG=<tag>
make console-image CONSOLE_IMAGE=registry.example.com/linkd-console IMAGE_TAG=<tag>
```

两个 Makefile 入口优先使用 Docker，也可通过 `CONTAINER=container` 选择 Apple Container。
Console 也可直接执行 `docker build -t linkd-console:<tag> console`；构建上下文必须是 `console`。
构建器建议至少 4 GiB 内存，TypeScript 构建默认最多使用 3 GiB V8 heap，可用构建参数
`NODE_BUILD_OPTIONS` 调整；该参数不进入运行镜像。
可通过 `image` 和 `console.image` 覆盖默认 registry、repository、tag 或 digest。
如果显式清空 tag，必须提供 digest；使用私有镜像时配置 imagePullSecrets。
显式 digest 优先于 tag，`global.imageRegistry` 优先于各镜像 registry。
GitHub Actions 支持单独打包 Chart 并下载 `.tgz`，见[手动构建与发布](image-release.md#helm-chart-打包)。

复制 [外部服务示例](../../deploy/helm/linkd/examples/external-services.yaml) 为本地私有 values，
填写镜像和实际外部服务地址；来源与 Kafka 输出规则通过正式来源 API 配置。
不把含凭据的 values 提交到仓库。Helm 管理的配置会同时存入 Kubernetes Secret 和 Helm release 记录。

预先创建 namespace 和认证 Secret。下面的文件路径指向部署者准备的无末尾换行凭据文件，命令不会把明文凭据放入参数：

```bash
kubectl create namespace linkd
kubectl -n linkd create secret generic linkd-auth \
  --from-file=api-token=/secure/linkd-api-token \
  --from-file=worker-token=/secure/linkd-worker-token
```

API token 与 worker token 必须非空且不同。Chart 引用已有 Secret，不自动生成或轮换 token。

## Worker 分组

```yaml
clusters:
  default:
    labels:
      pool: default
    cleaner:
      replicas: 2
      resources:
        requests: {cpu: 200m, memory: 256Mi}
        limits: {cpu: "1", memory: 512Mi}
    lifecycle:
      replicas: 2
      resources:
        requests: {cpu: 500m, memory: 512Mi}
        limits: {cpu: "2", memory: 1Gi}
  alarmd:
    labels:
      pool: alarmd
    configuration:
      worker:
        require_explicit_selector: true
    cleaner:
      replicas: 1
      resources:
        requests: {cpu: 500m, memory: 512Mi}
        limits: {cpu: "2", memory: 1Gi}
    lifecycle:
      replicas: 1
      resources:
        requests: {cpu: "1", memory: 1Gi}
        limits: {cpu: "4", memory: 2Gi}
```

组名必须为最长 20 字符的小写 DNS label。上例创建四个 worker Deployment。
`replicas: 0` 可暂停对应角色；省略的角色参数继承 `workerDefaults.<role>`。
Helm 合并 map 时会保留默认 `default` 组；只部署其他组时，将 default 两个角色的 replicas 设为 0。

`clusters.<name>.labels` **就是 Linkd 的 worker.labels**，同组两个角色共用它。
Chart 通过 `LINKD_WORKER_LABELS` 注入 JSON 对象，并完整替换配置文件的 worker.labels；
未设置环境变量时保持文件配置，`{}` 明确清空标签。变量最多 16384 字节，标签沿用 Linkd 的校验规则。
保留环境变量不可通过 extraEnvVars 重复声明。

来源调度使用 `scheduling.cleaner.selector`、`scheduling.lifecycle.selector` 匹配这些标签。
组名本身不参与调度；默认空 selector 可匹配多个组。专属 worker 组可设置
`worker.require_explicit_selector: true`，并在来源 selector 中明确填写对应标签。

Pod/Deployment 另有 `linkd/worker-group: <name>` 用于运维筛选，不会复制任意 worker labels：

```bash
kubectl -n linkd get deployment,pod -l linkd/worker-group=alarmd
```

所有组共用一个控制面与同一个 `dispatch.deployment`，它是调度和来源存储作用域，
不是 Kubernetes Deployment 名称。默认取 namespace-release；安装后应保持稳定。
本 Chart 不通过 worker 组创建独立控制面或租户边界。

## 配置 Secret

默认每组创建独立 Secret `<fullname>-<group>-config`，包含
`cleaner.yaml` 和 `lifecycle.yaml`；各 Deployment 只投影自身使用的 key。
Control Plane 和 Console 各有独立配置 Secret。

内联配置按以下顺序合并：公共 `configuration` → 组 `configuration` →
角色配置（先 workerDefaults，再组内角色覆盖）。
角色副本和资源使用 Kubernetes values，不会写进 Linkd YAML。
Chart 最后设置 dispatch 网络地址与作用域、telemetry 和 worker labels，并将 event_sources 置空；
常驻进程不自动导入来源。

也可以让所有组引用同一个已有 Secret，并使用不同的 key：

```yaml
configuration: {}
controlPlane:
  existingSecret: linkd-config
  secretKey: control-plane.yaml
clusters:
  default:
    labels: {pool: default}
    existingSecret: linkd-config
    secretKeys:
      cleaner: default-cleaner.yaml
      lifecycle: default-lifecycle.yaml
  alarmd:
    labels: {pool: alarmd}
    existingSecret: linkd-config
    secretKeys:
      cleaner: alarmd-cleaner.yaml
      lifecycle: alarmd-lifecycle.yaml
```

可把各组 existingSecret 改成不同名称，以使用多个已有 Secret。
已有 Secret 模式要求不配置适用于该角色的内联 configuration；如部分组使用已有 Secret，
将其他角色所需配置放在其自身 configuration 中，不设置公共 configuration。

已有 Secret 中是完整且严格有效的 Linkd YAML。各角色必须使用一致的 deployment、存储、
Severity 配置；控制面设置 `dispatch.listen: 0.0.0.0:8090`。
开启指标时设置 Prometheus exporter 和 `0.0.0.0:9464`。
worker.labels 仍由 Chart 的环境变量覆盖，控制面 URL 也通过环境变量指向内部 Service。

## Console、Basic Auth 与 Ingress

```bash
kubectl -n linkd create secret generic linkd-console-auth \
  --from-file=username=/secure/console-username \
  --from-file=password=/secure/console-password
```

```yaml
console:
  enabled: true
  basicAuth:
    existingSecret: linkd-console-auth
    usernameKey: username
    passwordKey: password
  prometheusUrl: http://prometheus:9090
  ingress:
    enabled: true
    ingressClassName: "<集群中的 IngressClass>"
    hostname: linkd.example.com
    tls:
      - hosts: [linkd.example.com]
        secretName: linkd-console-tls
```

Ingress 使用 `console.basePath`（默认 `/`）和 Prefix 匹配，只暴露 Console。
子路径同时配置到 Console 服务端，页面资源、浏览器路由和 API 自动使用同一前缀；Ingress 必须保留完整路径，不配置 rewrite-target。
路径段仅支持字母、数字、下划线、连字符，总长度不超过 256；除根路径外不能以 `/` 结尾。

同域名下使用 HTTP 子路径的示例（与其他 values 合并）：

```yaml
console:
  enabled: true
  basePath: /apps/linkd
  ingress:
    enabled: true
    ingressClassName: nginx
    hostname: apps.example.com
    tls: []
```

访问 `http://apps.example.com/apps/linkd/`。
同一镜像可通过运行时配置切换根路径和子路径，无需为每个路径重新构建。
可叠加使用 [HTTP 子路径示例](../../deploy/helm/linkd/examples/console-subpath.yaml)。
TLS Secret、DNS 和 Controller 由部署方准备，HTTPS 和跳转策略按所用 Controller 配置。
Basic Auth 不提供传输加密，远程访问应使用 HTTPS。

Console Service 是 ClusterIP，Basic Auth 在 Node 服务执行，直连 Service 也需要认证。
所有页面、静态资源和 local-api 都受保护。凭据来自 Secret，只配置单个运维账户，
该账户具有 Console 现有管理权限，不代表提供按用户划分的租户权限。

Console 以 server 模式监听 `0.0.0.0:4399`，该模式强制 Basic Auth。
本地开发仍默认 local 模式，仅允许回环监听。浏览器认证信息不会转发给控制面；
控制面请求使用服务端 API token，基础设施凭据不会发送给浏览器。

Console 可配置自己的 configuration / existingSecret、资源、调度参数和 extraEnvVars，
通过既有只读凭据环境变量覆盖数据库或 Prometheus 连接凭据。其配置 Secret 需包含有效的存储配置，
不需要 worker token。

## 可选 Event Generator

Chart `0.1.2` 增加 `eventgen`，默认 `enabled: false`，镜像独立使用 `linkd-eventgen:0.1.0`。
启用后，每个 `instances` 项独立运行，最多 32 项；名称最长 20 字符，只支持小写字母、数字、连字符。

模拟器从独立 Secret 中读取包含 `event_sources` 的 Linkd YAML；不能复用 Chart 为 worker 生成的配置，因为其中的静态来源会被清空。
根据 [来源配置示例](../../deploy/helm/linkd/examples/eventgen-config.yaml) 调整 Kafka 地址、topic 和认证，再创建 Secret：

```bash
kubectl -n linkd create secret generic linkd-eventgen-config \
  --from-file=linkd.yaml=/absolute/path/eventgen-config.yaml
```

将下面配置与原有 values 合并：

```yaml
eventgen:
  enabled: true
  existingSecret: linkd-eventgen-config
  defaults:
    cycleDuration: 30s
    maxActiveAlerts: 10000
  instances:
    infra:
      eventSourceId: demo-infra
      tenantId: tenant-a
      newAlertsPerMinute: 20
      duplicatePercent: 20
    service:
      eventSourceId: demo-service
      tenantId: tenant-b
      newAlertsPerMinute: 60
      cycleDuration: 15s
      cycles: 10
```

`defaults` 中的速率、周期、平均寿命、重复比例、场景、随机种子、活动池上限、周期数、资源和调度参数可逐实例覆盖。
`cycleDuration` 使用整数加 `ms`、`s` 或 `m`，范围为 `10ms..10m`；其他参数约束见 [模拟器参数](event-generator.md#2-参数)。
每个实例还可覆盖 `existingSecret` / `secretKey`，或设置 `enabled: false` 单独关闭。
`tenantId` 仅在来源配置已固定 `related_tenant_id` 时可省略。

`cycles: 0` 使用单副本、Recreate 策略的 Deployment，按给定速率持续推送；正数使用带 revision 后缀的 Job，
正常完成后停止，失败不自动重试，默认完成 24 小时后清理。每次 Helm 升级会创建新一轮有限任务。
配置 Secret 变更不会自动重启持续实例，需对相应 Deployment 执行 `rollout restart`。

实例只挂载模拟器配置，不接收控制面 API token，也不创建 Service 或 ServiceMonitor。
`eventgen.image` 可独立覆盖 `registry`、`repository`、`tag`、`digest` 和 `pullSecrets`；`global.imageRegistry` 仍具有最高优先级。
来源配置须与控制面实际登记的来源保持一致，Chart 不自动注册或启用 EventSource。
完整叠加配置见 [多实例示例](../../deploy/helm/linkd/examples/eventgen.yaml)。

## ServiceMonitor 指标采集

默认 `metrics.enabled: true` 会为 Control Plane 和每组 Cleaner/Lifecycle 创建独立指标 Service，
端口名为 `metrics`，Service 与容器端口均为 `9464`。内联配置自动监听 `0.0.0.0:9464`。

安装 Prometheus Operator CRD 后启用：

```yaml
metrics:
  enabled: true
  serviceMonitor:
    enabled: true
    labels:
      release: kube-prometheus-stack
    interval: 30s
    scrapeTimeout: 10s
```

`labels` 必须匹配实际 Prometheus 的 `spec.serviceMonitorSelector`，示例中的 release 值需要按环境替换。
Prometheus 的 `spec.serviceMonitorNamespaceSelector` 也必须包含 ServiceMonitor 所在 namespace。
Chart 不安装 Prometheus Operator 或修改 Prometheus 实例；这些是 Operator 的
[发现规则](https://prometheus-operator.dev/docs/getting-started/design/)。

单个 ServiceMonitor 通过 release 标签与 `linkd/metrics: "true"` 选择当前 release 的所有指标 Service，
固定采集 HTTP `/metrics`；Operator 发现各 Service 后采集其对应的 Pod endpoint，而不是通过
Service 负载均衡只采一份副本。Control Plane API Service、Console 和 migrate Job 不纳入采集。
关闭 `metrics.enabled` 时必须同时关闭 ServiceMonitor。

ServiceMonitor 默认创建在 Linkd namespace。若设置 `metrics.serviceMonitor.namespace: monitoring`，
则只改变 ServiceMonitor 对象的位置，目标 namespace 仍固定为 Linkd release namespace，避免跨部署误采集。
指定的 ServiceMonitor namespace 必须预先存在；Prometheus 也需具备跨 namespace 发现所需的权限。

支持的采集参数：

| 参数 | 默认值与含义 |
| --- | --- |
| `labels` / `annotations` | ServiceMonitor 对象元数据，可匹配 Prometheus 的发现条件 |
| `interval` / `scrapeTimeout` | `30s` / `10s`；超时不得大于采集间隔 |
| `honorLabels` | `false`，指标标签冲突时采用 Prometheus 目标标签 |
| `targetLabels` | 默认传递 `app.kubernetes.io/component` 和 `linkd/worker-group`，用于区分角色与组 |
| `podTargetLabels` | 默认空，可从 Pod 传递额外标签 |
| `relabelings` | 默认空，在采集前调整目标标签 |
| `metricRelabelings` | 默认空，在入库前处理或过滤样本 |
| `sampleLimit` / `targetLimit` | 默认 `0`，不在此层额外限制样本/目标数量，仍受 Prometheus 全局约束 |

这些字段按 [Prometheus Operator API](https://prometheus-operator.dev/docs/api-reference/api/) 传递；
重标记规则的语义由 Operator/Prometheus 校验。带有 `/`、`.`、`-` 的 Kubernetes 标签名会转换为合法的
Prometheus 标签名。完整示例见 [servicemonitor.yaml](../../deploy/helm/linkd/examples/servicemonitor.yaml)。

```bash
helm upgrade --install linkd deploy/helm/linkd -n linkd \
  -f /secure/linkd-values.yaml -f deploy/helm/linkd/examples/servicemonitor.yaml
kubectl -n linkd get servicemonitor
kubectl -n linkd get service -l 'linkd/metrics=true'
```

使用已有配置 Secret 时，部署者必须设置 `telemetry.metrics.exporter: prometheus` 和
`telemetry.metrics.prometheus.listen_address: 0.0.0.0:9464`；Chart 不修改已有 Secret。
实际抓取是否成功需在 Prometheus Targets 中确认；仅创建 ServiceMonitor 不代表已被 Prometheus 选中。

## 初始化 Job 与等待方式

Chart 默认在创建、更新时运行 `linkd storage migrate`，使用 Control Plane 的配置、镜像和认证 Secret。
这个一次性命令完成配置与认证前提校验、Redis PING、Repository schema 初始化及 EventSource
Record/Release 集合初始化。Elasticsearch 复用控制面的 Schema/Active 资源与时间桶对账；MySQL
复用 Repository 的 EnsureSchema。成功后退出，失败返回非零退出码。

```yaml
migrate:
  enabled: true
  watch: true
  timeoutSeconds: 240
  activeDeadlineSeconds: 300
  backoffLimit: 1
  ttlSecondsAfterFinished: 86400
  resources:
    requests: {cpu: 100m, memory: 128Mi}
    limits: {cpu: "1", memory: 512Mi}
```

`watch: true` 使用 `pre-install,pre-upgrade` Hook Job；Helm 等待完成后才创建/更新普通资源。
Job 失败则本次 Helm 操作失败。初次安装所需的迁移配置 Secret 和专用 ServiceAccount 也作为更早的
Hook 创建，不依赖尚未创建的普通资源；成功后清理这些 Hook。失败 Job 保留到 TTL 或下次执行前，
失败的 Hook Secret/ServiceAccount 保留用于排障，可在确认 Job 已结束后手工删除。

认证 Secret、引用的已有配置 Secret、已有 ServiceAccount、imagePullSecrets，以及 extraVolumes 中
引用的证书等资源，必须在 Hook 执行前存在。迁移可覆盖 resources、调度、安全上下文和扩展环境/卷参数，
未覆盖时继承 Control Plane 和通用 Pod 参数；不能通过 migrate 覆盖控制面配置或保留的认证变量。

```bash
helm upgrade --install linkd deploy/helm/linkd -n linkd \
  -f /secure/linkd-values.yaml --set migrate.watch=true --timeout 6m
```

Helm 总等待上限由 CLI `--timeout` 决定，Chart 不能修改它。建议大于 Job 的 activeDeadlineSeconds，
后者必须大于单次命令的 timeoutSeconds；重试也计入 Job 的总期限。

`watch: false` 创建普通 Job `<fullname>-migrate-r<revision>`，每次 Helm revision 使用新名称和新的
内联配置快照，避免更新不可变 Job spec。示例见 [异步初始化](../../deploy/helm/linkd/examples/migrate-async.yaml)。
Job 与服务启动并行，初始化失败不会自动令已经返回的 Helm 操作失败，也不会阻止服务启动。
新一轮 Helm 更新可能删除仍在运行的上一轮普通 Job；初始化可能部分生效，应按幂等初始化重试处理。

```bash
helm upgrade --install linkd deploy/helm/linkd -n linkd \
  -f /secure/linkd-values.yaml --set migrate.watch=false --wait=false
kubectl -n linkd get jobs -l app.kubernetes.io/component=migrate
kubectl -n linkd wait --for=condition=complete job/<fullname>-migrate-r<revision> --timeout=5m
kubectl -n linkd logs job/<fullname>-migrate-r<revision>
```

Helm Hook Job 始终阻塞，所以不能只对 Hook 使用 `--wait=false` 来实现异步。
普通 Job 模式下，如果显式传入 `--wait --wait-for-jobs`，Helm 仍然会等待 Job；`--atomic`
也会启用 Helm 的资源等待。上述行为依据 [Helm 3 Hook 语义](https://helm.sh/zh/docs/v3/topics/charts_hooks/)。

`migrate.enabled: false` 完全不生成迁移 Job。Hook 不注册 rollback 事件；普通 Job 随 release 资源
管理，回滚到包含普通 Job 的历史版本时可能重新创建该版本 Job，因此只用于幂等初始化。

迁移不会启动 API、归档或调度任务，不申请活动控制面资格、不导入来源、不清空业务数据，也不执行
`scheduling init`。它检查的是控制面配置及基础存储，不验证所有 worker Secret、来源 Kafka 权限或完整
业务链路，不提供未发布历史 schema 的自动转换。它可以与已有控制面同时存在；破坏性变更仍需先
停机和排空，不能把该 Job 当作维护窗口或多控制面并行运行的替代。

## 安装、升级与检查

```bash
helm lint --strict deploy/helm/linkd -f /secure/linkd-values.yaml
helm upgrade --install linkd deploy/helm/linkd \
  --namespace linkd -f /secure/linkd-values.yaml
```

通过控制面 API 或现有 `linkd event-source import` 命令显式导入来源。
先确认控制面和 worker 注册，再发布来源；Pod Ready 不代表已接管来源或完成业务处理。

Control Plane 固定一副本并使用 Recreate。它重启时可能等待旧 Redis 授权过期；
Chart 不承诺无中断升级，也不会自动运行 `scheduling init`。
从已有 all-in-one 部署迁入前必须停止旧控制面与 worker，避免同时存在活动中心。
需要破坏性契约变更时，按相应版本说明暂停和排空 worker 后升级。

Chart 管理的内联配置变化通过 checksum 更新对应 Deployment。
已有配置/认证 Secret 更新后，需显式重启受影响 Deployment；共享配置 Secret 的 key 变化不会自动重启：

```bash
kubectl -n linkd rollout restart deployment -l linkd/worker-group=alarmd
```

不要在普通 Helm 升级时重建丢失的协调历史；先停止旧执行环境，再按调度协议显式恢复。
卸载只删除 Chart 管理的 Kubernetes 资源，不删除已有 Secret、中间件或其中的业务数据。

`metrics.enabled` 默认开启；`metrics.serviceMonitor.enabled` 默认关闭，启用前需要安装对应 CRD。
Linkd 当前没有专用健康接口，默认不配置探针，可在角色中自定义。
Console 默认 TCP 探针只判断端口存活，不代表外部依赖健康。

在模块根目录执行：

```bash
make check
make helm-check
cd console
pnpm build
pnpm exec playwright test --config playwright.auth.config.ts
```

Helm 测试检查渲染、配置加载、组隔离和错误参数，不连接真实集群。
生产镜像与浏览器验证不等于 Kafka/Redis/Repository 端到端验证，真实部署仍需单独联调。
