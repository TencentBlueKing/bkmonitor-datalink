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
./bin/linkd config validate --config ./configs/linkd.pm2.yaml
pm2 start ./ecosystem.config.cjs
```

配置默认连接以下本地服务：

- Kafka：`127.0.0.1:9092`；
- Elasticsearch：`http://127.0.0.1:9200`；
- Redis：`127.0.0.1:16379`，密码 `test123456`；
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

修改 `configs/linkd.pm2.yaml` 后需要同时重启 all-in-one 和两个模拟器。只调整某个模拟器的速率、周期、
生命周期或场景时，修改 `ecosystem.config.cjs` 中对应 app，再单独重启该 PM2 进程即可。
