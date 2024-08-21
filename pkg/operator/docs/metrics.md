# 自监控指标文档

所有指标的 namespace 均为 bkmonitor_operator

### DataIdWatcher

| Metric | Type                          | Description                |
| ------ |-------------------------------|----------------------------|
| dataid_info | Gauge                         | dataid 元数据信息               |
| dataid_watcher_handled_total | Counter                       | dataidwatcher 处理变更次数       |
| dataid_watcher_handled_duration_seconds | Histogram | dataidwatcher 处理变更耗时       |
| dataid_watcher_received_event_total | Counter | dataidwatcher 接收 k8s 事件计数器 |
| dataid_watcher_handled_event_total | Counter | dataidwatcher 处理 k8s 事件计数器 |

### Discover

| Metric                                 | Type      | Description        |
|----------------------------------------|-----------|--------------------|
| discover_started_total                 | Counter   | discover 启动次数      |
| discover_stopped_total                 | Counter   | discover 停止次数      |
| discover_waited_total                  | Counter   | discover 等待停止次数    |
| discover_created_config_success_total  | Counter   | 创建子任务成功计数器         |
| discover_created_config_failed_total   | Counter   | 创建子任务失败计数器         |
| discover_removed_config_total          | Counter   | 移除子任务计数器           |
| discover_received_tg_total             | Counter   | 接收 targetgroup 计数器 |
| discover_handled_tg_duration_seconds   | Histogram | 处理 targetgroup 耗时  |

### Workload

| Metric | Type      | Description |
| ------ |-----------|-------------|
| workload_lookup_request_total | Counter   | 工作负载搜索次数    |
| workload_lookup_duration_seconds | Histogram | 工作负载搜索耗时    |

### Operator

| Metric                | Type                  | Description                 |
|-----------------------|-----------------------|-----------------------------|
| uptime                | Counter               | 进程运行时间                      |
| cluster_version | Gauge | kubernetes 服务端版本 |
| build_info            | Gauge                 | 构建版本信息                      |
| active_config_count   | Gauge                 | 活跃的采集任务数量                   |
| active_shared_discovery_count | Gauge                 | 活跃的 shared_discovery 数量     |
| active_monitor_resource_count | Gauge                 | 活跃的监控资源数量                   |
| received_event_total  | Counter               | 接收 k8s 接收事件总数               |
| handled_event_total   | Counter               | 处理 k8s 接收事件总数               |
| handled_event_duration_seconds | Histogram | 处理 k8s 事件耗时                 |
| handled_secret_success_total | Counter | 处理 secrets 成功次数             |
| handled_secret_failed_total | Counter | 处理 secrets 失败次数             |
| dispatched_task_total | Counter | 分派任务计数器                     |
| dispatched_task_duration_seconds | Histogram | 分派任务耗时                      |
| skipped_secret_total  | Counter | secrets 内容无变更 直接跳过计数        |
| compressed_config_failed_total | Counter | 压缩配置文件错误计数器                 |
| handled_discover_notify_total | Counter | 处理 discover notify 计数器      |
| handled_dataid_watcher_notify_total | Counter | 处理 dataidwatcher notify 计数器 |
| reloaded_discover_duration_seconds | Histogram | 重载 discover 耗时              |
| active_secret_file_count | Gauge | 活跃的 sercets 数量              |
| active_secret_bytes      | Gauge | 活跃的 sercets 字节大小            |
| secrets_exceeded      | Counter | sercets 超限计数器               |
| scaled_statefulset_failed_total | Counter | 弹缩 statefulset worker 失败次数  |
| scaled_statefulset_success_total | Counter | 弹缩 statefulset worker 成功次数  |
