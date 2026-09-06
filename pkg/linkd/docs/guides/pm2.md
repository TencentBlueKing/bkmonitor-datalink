# 使用 PM2 托管本地 Linkd 拓扑

仓库根目录的 [`ecosystem.config.cjs`](../../ecosystem.config.cjs) 定义了三个 PM2 进程：

| PM2 名称                 | 进程                             | EventSource        | 主要配置                           |
| ------------------------ | -------------------------------- | ------------------ | ---------------------------------- |
| `linkd-all-in-one`       | Cleaner + Lifecycle + 控制面任务 | 全部启用来源       | `configs/linkd.pm2.yaml`           |
| `linkd-eventgen-infra`   | Standard Event 模拟器            | `standard-infra`   | 20 条/分钟、30 秒周期、20% 重复    |
| `linkd-eventgen-service` | Standard Event 模拟器            | `standard-service` | 60 条/分钟、15 秒周期、20% 重复    |

两个 EventSource 使用不同 tenant、Kafka topic、consumer group、fingerprint 和 Cleaner 预算。两个模拟器
也选择不同场景、seed、速率和生命周期，因此可以独立调整，不会共享活动告警池。
重复项复用完全相同的 Event body、headers 和身份，用于持续验证 Repository 去重与 Mailbox 重复引用收敛。

## 构建和启动

PM2 托管预编译二进制；`bin/` 已被 Git 忽略：

```bash
mkdir -p ./bin
go build -o ./bin/linkd ./cmd/linkd
go build -o ./bin/linkd-eventgen ./cmd/linkd-eventgen
cp ./configs/linkd.pm2.yaml ./configs/linkd.pm2.local.yaml
# 仅在被 Git 忽略的 local 文件中填写本机凭据。
./bin/linkd config validate --config ./configs/linkd.pm2.local.yaml
LINKD_CONFIG=./configs/linkd.pm2.local.yaml pm2 start ./ecosystem.config.cjs
```

`LINKD_CONFIG` 未设置时仍使用 `configs/linkd.pm2.yaml`；相对路径按 `pkg/linkd` 根目录解析，绝对路径
保持不变。三个 PM2 进程始终共享同一个配置路径。

配置默认连接以下本地服务：

- Kafka：`127.0.0.1:9092`；
- Elasticsearch：`http://127.0.0.1:9200`；
- Redis：`127.0.0.1:16379`，跟踪示例使用空密码；启用认证时在 local 配置中填写；
- Prometheus metrics：`127.0.0.1:9464`。

PM2 会在依赖暂时不可用或进程异常退出时按 3 秒间隔重启，最多连续重启 20 次。Kafka topic 需要由
broker 自动创建，或提前创建 `linkd-standard-infra`、`linkd-standard-service` 和
`linkd-pm2-alerts`。

## 查看和维护

```bash
pm2 status
pm2 logs linkd-all-in-one --lines 100
pm2 logs linkd-eventgen-infra --lines 100
pm2 logs linkd-eventgen-service --lines 100
pm2 restart ./ecosystem.config.cjs --update-env
pm2 stop linkd-all-in-one linkd-eventgen-infra linkd-eventgen-service
pm2 delete linkd-all-in-one linkd-eventgen-infra linkd-eventgen-service
```

`all-in-one` 使用 Elasticsearch 时会先按顺序确保模板、Active 资源、当前时间桶和 alias 已就绪，再启动
cleaner/lifecycle；Schema 与 Active 资源对账、时间桶维护和终态 Alert 归档由同进程内三个独立控制面任务执行。
归档任务以有界 Worker 连续处理积压，只在空闲、无进展或失败时等待配置间隔，并且不阻塞数据面启动。
拆分部署时必须先启动 `linkd run control-plane`；三项任务没有独立入口。

修改当前 `LINKD_CONFIG` 指向的配置后，需要同时重启 all-in-one 和两个模拟器。只调整某个模拟器的
速率、周期、生命周期或场景时，修改 `ecosystem.config.cjs` 中对应 app，再单独重启该 PM2 进程即可。

## 独立角色运行

高吞吐对照可以使用现有的三个角色入口，不需要改业务协议。先停止生成器并排空，再停止
`linkd-all-in-one`，避免它与独立角色同时消费。先启动控制面，确认索引、alias 和时间桶
就绪后，再启动 Cleaner 与 Lifecycle。

三份配置的业务字段必须一致，只区分 Prometheus 监听端口；生成器和 DevTools 使用
Lifecycle 对应的共同业务配置。下面是本地测试使用的映射，不是新的默认端口分配规则：

| 角色 | 配置文件 | metrics 地址 |
| --- | --- | --- |
| control-plane | `configs/linkd.control-plane.local.yaml` | `127.0.0.1:9466` |
| cleaner | `configs/linkd.cleaner.local.yaml` | `127.0.0.1:9465` |
| lifecycle | `configs/linkd.pm2.local.yaml` | `127.0.0.1:9464` |

```bash
./bin/linkd run control-plane --config ./configs/linkd.control-plane.local.yaml
./bin/linkd run cleaner --config ./configs/linkd.cleaner.local.yaml
./bin/linkd run lifecycle --config ./configs/linkd.pm2.local.yaml
```

上述命令是三个独立的常驻进程，应分别交给 PM2 等进程管理器托管，而不是在同一终端中
顺序等待退出。Prometheus 必须分别抓取三个端点，保留不同 `instance`/job；尤其不要
把三个进程的 GC 暂停占比相加当作 Lifecycle 的暂停占比。

本地两轮十分钟吞吐验证后采用的配置为 Lifecycle 并发 256、写批量和最长等待自动派生为 128 项/
128ms、读等待 10ms、共享批次执行上限 32。只需设置 `lifecycle.concurrency`，
不要把派生出的数值再次写成一组互相独立的 YAML 参数。该配置仍需结合实际 CPU、
事件大小和负载分布评估；完整数据和验证状态见[吞吐压测报告](../research/benchmarks/2026-09-04-lifecycle-elasticsearch-throughput.md)。

Go 执行并行度与业务并发不是同一个值。测试主机的 Go 执行并行度为 14，这不是通用部署默认值；
不要照搬到不同核数的机器，也不要把业务并发 256 当作 Go 可同时运行的线程数。
测试中的 ES 零副本、AlertLog async 和 Redis 持久化设置也不代表生产可靠性配置。
