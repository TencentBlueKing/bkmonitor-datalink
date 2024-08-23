# 自监控指标文档

所有指标的 namespace 均为 bkmonitor_operator

### DataIdWatcher

| Metric                       | Type    | Description          |
|------------------------------|---------|----------------------|
| dataid_info                  | Gauge   | dataid 元数据信息         |
| dataid_watcher_handled_total | Counter | dataidwatcher 处理变更次数 |

### Discover

| Metric                                | Type    | Description         |
|---------------------------------------|---------|---------------------|
| discover_started_total                | Counter | discover 启动次数       |
| discover_stopped_total                | Counter | discover 停止次数       |
| discover_created_config_success_total | Counter | 创建子任务成功计数器          |
| discover_created_config_failed_total  | Counter | 创建子任务失败计数器          |
| discover_created_config_cached_total  | Counter | 创建子任务缓存计数器          |
| discover_handled_tg_total             | Counter | 已处理 targetgroup 计数器 |
| discover_deleted_tg_source_total      | Counter | 已删除 targetgroup 计数器 |
| monitor_scrape_interval_seconds       | Gauge   | 监控采集时间间隔            |

### Operator

| Metric                           | Type      | Description                                 |
|----------------------------------|-----------|---------------------------------------------|
| uptime                           | Counter   | 进程运行时间                                      |
| cluster_version                  | Gauge     | kubernetes 服务端版本                            |
| build_info                       | Gauge     | 构建版本信息                                      |
| handled_event_total              | Counter   | 处理 k8s 接收事件总数                               |
| handled_secret_success_total     | Counter   | 处理 secrets 成功次数                             |
| handled_secret_failed_total      | Counter   | 处理 secrets 失败次数                             |
| dispatched_task_total            | Counter   | 分派任务计数器                                     |
| dispatched_task_duration_seconds | Histogram | 分派任务耗时                                      |
| active_secret_file_count         | Gauge     | 活跃的 sercets 数量                              |
| secrets_exceeded                 | Counter   | sercets 超限计数器                               |
| scaled_statefulset_failed_total  | Counter   | 弹缩 statefulset worker 失败次数                  |
| scaled_statefulset_success_total | Counter   | 弹缩 statefulset worker 成功次数                  |
| node_config_count                | Gauge     | node 上采集任务数量                                |
| monitor_endpoint_count           | Gauge     | serviceMonitor/podMonitor 匹配到的 endpoints 数量 |
| workload_count                   | Gauge     | 工作负载数量                                      |
| node_count                       | Gauge     | 节点数量                                        |
| shared_discovery_count           | Gauge     | shared_discovery 数量                         |
| discover_count                   | Gauge     | discover 数量                                 |
| statefulset_workers              | Gauge     | statefulset_workers 数量                      |
