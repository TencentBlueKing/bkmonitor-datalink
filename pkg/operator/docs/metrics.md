# 自监控指标文档

所有指标的 namespace 均为 bkmonitor_operator

### DataIdWatcher

| Metric | Type                          | Description                |
| ------ |-------------------------------|----------------------------|
| dataid_info | Gauge                         | dataid 元数据信息               |
| dataid_watcher_handled_total | Counter                       | dataidwatcher 处理变更次数       |
| dataid_watcher_handled_duration_seconds | Histogram | dataidwatcher 处理变更耗时       |
| dataid_watcher_received_k8s_event_total | Counter | dataidwatcher 接收 k8s 事件计数器 |
| dataid_watcher_handled_k8s_event_total | Counter | dataidwatcher 处理 k8s 事件计数器 |

### Discover

| Metric                                         | Type      | Description        |
|------------------------------------------------|-----------|--------------------|
| discover_started_total                         | Counter   | discover 启动次数      |
| discover_stopped_total                         | Counter   | discover 停止次数      |
| discover_waited_total                          | Counter   | discover 等待停止次数    |
| discover_created_child_config_success_total    | Counter   | 创建子任务成功计数器         |
| discover_created_child_config_failed_total     | Counter   | 创建子任务失败计数器         |
| discover_removed_child_config_total            | Counter   | 移除子任务计数器           |
| discover_received_target_group_total           | Counter   | 接收 targetgroup 计数器 |
| discover_handled_target_group_duration_seconds | Histogram | 处理 targetgroup 耗时  |
| discover_got_secret_success_total              | Counter       | 获取 sercets 成功次数    |
| discover_got_secret_failed_total                 | Counter       | 获取 sercets 失败次数    |

### Workload

| Metric | Type      | Description |
| ------ |-----------|-------------|
| workload_count | Gauge     | 工作负载统计      |
| workload_lookup_request_total | Counter   | 工作负载搜索次数    |
| workload_lookup_duration_seconds | Histogram | 工作负载搜索耗时    |
| cluster_node_count     | Gauge     | 集群节点数量      |

### Operator

| Metric                                  | Type                  | Description                 |
|-----------------------------------------|-----------------------|-----------------------------|
| uptime                                  | Counter               | 进程运行时间                      |
| k8s_cluster_version | Gauge | kubernetes 服务端版本 |
| build_info                              | Gauge                 | 构建版本信息                      |
| active_child_config_count               | Gauge                 | 活跃的采集任务数量                   |
| active_shared_discovery_count           | Gauge                 | 活跃的 shared_discovery 数量     |
| active_monitor_resource_count           | Gauge                 | 活跃的监控资源数量                   |
| received_k8s_event_total                | Counter               | 接收 k8s 接收事件总数               |
| handled_k8s_event_total                 | Counter               | 处理 k8s 接收事件总数               |
| handled_k8s_event_duration_seconds      | Histogram | 处理 k8s 事件耗时                 |
| handled_secret_success_total            | Counter | 处理 secrets 成功次数             |
| handled_secret_failed_total             | Counter | 处理 secrets 失败次数             |
| dispatched_task_total                   | Counter | 分派任务计数器                     |
| dispatched_task_duration_seconds        | Histogram | 分派任务耗时                      |
| skipped_secret_total                    | Counter | secrets 内容无变更 直接跳过计数        |
| compressed_config_failed_total          | Counter | 压缩配置文件错误计数器                 |
| handled_discover_notify_total           | Counter | 处理 discover notify 计数器      |
| handled_dataid_watcher_notify_total     | Counter | 处理 dataidwatcher notify 计数器 |
| reloaded_discover_duration_seconds      | Histogram | 重载 discover 耗时              |
| active_secret_file_count                | Gauge | 活跃的 sercets 数量              |
| active_secret_bytes                         | Gauge | 活跃的 sercets 字节大小            |
| secrets_exceeded                        | Counter | sercets 超限计数器               |
| reconciled_node_endpoints_success_total | Counter | 同步 endpoints 资源成功计数器        |
| reconciled_node_endpoints_failed_total  | Counter | 同步 endpoints 资源失败计数器        |
| scraped_lines_count                     | Cauge | 抓取采集端点数据行数                  |
| scraped_errors_count                    | Gauge | 抓取采集端点错误次数                  |
| scraped_duration_seconds                | Histogram | 抓取采集端点耗时                    |
| scaled_statefulset_failed_total         | Counter | 弹缩 statefulset worker 失败次数  |
| scaled_statefulset_success_total        | Counter | 弹缩 statefulset worker 成功次数  |
