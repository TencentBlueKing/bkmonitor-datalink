# 自监控指标文档

所有指标的 namespace 均为 bkmonitor_operator

### DataIdWatcher

| Metric                       | Type    | Description          |
|------------------------------|---------|----------------------|
| dataid_info                  | Gauge   | dataid 元数据信息         |
| dataid_watcher_handled_total | Counter | dataidwatcher 处理变更次数 |

### Discover

| Metric                                 | Type      | Description        |
|----------------------------------------|-----------|--------------------|
| discover_started_total                 | Counter   | discover 启动次数      |
| discover_stopped_total                 | Counter   | discover 停止次数      |
| discover_created_config_success_total  | Counter   | 创建子任务成功计数器         |
| discover_created_config_failed_total   | Counter   | 创建子任务失败计数器         |

### Operator

| Metric                              | Type      | Description                 |
|-------------------------------------|-----------|-----------------------------|
| uptime                              | Counter   | 进程运行时间                      |
| cluster_version                     | Gauge     | kubernetes 服务端版本            |
| build_info                          | Gauge     | 构建版本信息                      |
| handled_event_total                 | Counter   | 处理 k8s 接收事件总数               |
| handled_secret_success_total        | Counter   | 处理 secrets 成功次数             |
| handled_secret_failed_total         | Counter   | 处理 secrets 失败次数             |
| dispatched_task_total               | Counter   | 分派任务计数器                     |
| dispatched_task_duration_seconds    | Histogram | 分派任务耗时                      |
| active_secret_file_count            | Gauge     | 活跃的 sercets 数量              |
| secrets_exceeded                    | Counter   | sercets 超限计数器               |
| scaled_statefulset_failed_total     | Counter   | 弹缩 statefulset worker 失败次数  |
| scaled_statefulset_success_total    | Counter   | 弹缩 statefulset worker 成功次数  |
