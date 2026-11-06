# TimeGraph 默认资源与关系目录

此文件由 `go run ./docs/examples/schema-catalog` 从同一代码版本的默认 Schema 生成。
Redis 的空间定义或部署覆盖可能不同；先取得实际 Schema，再使用完整主键对齐实体。
定义存在不代表指标已写入、当前空间有路由或容量已验收。扩展及接入前提见 [使用指南](timegraph.md#schema-and-routing)。

## 资源（26）

所有请求和响应 dimensions 值均为字符串。字段类型是 Schema 语义；不能用可选展示字段构造实体身份。

| 类型 | 完整主键 | 可选属性 | 非字符串字段类型 |
| --- | --- | --- | --- |
| system | bk_target_ip |  |  |
| datasource | bk_data_id |  | bk_data_id:integer |
| node | bcs_cluster_id, node |  |  |
| container | bcs_cluster_id, namespace, pod, container | version |  |
| pod | bcs_cluster_id, namespace, pod |  |  |
| job | bcs_cluster_id, namespace, job |  |  |
| replicaset | bcs_cluster_id, namespace, replicaset |  |  |
| deployment | bcs_cluster_id, namespace, deployment |  |  |
| daemonset | bcs_cluster_id, namespace, daemonset |  |  |
| statefulset | bcs_cluster_id, namespace, statefulset |  |  |
| service | bcs_cluster_id, namespace, service |  |  |
| ingress | bcs_cluster_id, namespace, ingress |  |  |
| k8s_address | bcs_cluster_id, address |  |  |
| domain | bcs_cluster_id, domain |  |  |
| apm_service | apm_application_name, apm_service_name |  |  |
| apm_service_instance | apm_application_name, apm_service_name, apm_service_instance_name |  |  |
| bklogconfig | bklogconfig_namespace, bklogconfig_name |  |  |
| business | bk_biz_id |  | bk_biz_id:integer |
| biz | bk_biz_id |  | bk_biz_id:integer |
| set | bk_set_id |  | bk_set_id:integer |
| module | bk_module_id |  | bk_module_id:integer |
| app_version | app_name, version |  |  |
| git_commit | git_repo, commit_id |  |  |
| p4_changelist | p4_port, changelist_id |  |  |
| svn_revision | svn_repo, revision |  |  |
| host | bk_host_id | version, env_name, env_type, service_version, service_type | bk_host_id:integer |

## 关系（34）

静态双向关系可沿两向遍历；定向关系按请求的 both/outbound/inbound 选方向。物理指标列按显式 metric_name 或默认命名规则生成；返回的 metric_name 允许为空，表示未显式覆盖。

| 关系类型 | 起点 | 终点 | 类别 | 默认方向 | 物理指标 |
| --- | --- | --- | --- | --- | --- |
| node_with_system | node | system | static | bidirectional | node_with_system_relation |
| node_with_pod | node | pod | static | bidirectional | node_with_pod_relation |
| job_with_pod | job | pod | static | bidirectional | job_with_pod_relation |
| container_with_pod | container | pod | static | bidirectional | container_with_pod_relation |
| pod_with_replicaset | pod | replicaset | static | bidirectional | pod_with_replicaset_relation |
| pod_with_statefulset | pod | statefulset | static | bidirectional | pod_with_statefulset_relation |
| daemonset_with_pod | daemonset | pod | static | bidirectional | daemonset_with_pod_relation |
| deployment_with_replicaset | deployment | replicaset | static | bidirectional | deployment_with_replicaset_relation |
| pod_with_service | pod | service | static | bidirectional | pod_with_service_relation |
| datasource_with_pod | datasource | pod | static | bidirectional | datasource_with_pod_relation |
| datasource_with_node | datasource | node | static | bidirectional | datasource_with_node_relation |
| ingress_with_service | ingress | service | static | bidirectional | ingress_with_service_relation |
| k8s_address_with_service | k8s_address | service | static | bidirectional | k8s_address_with_service_relation |
| domain_with_service | domain | service | static | bidirectional | domain_with_service_relation |
| apm_service_instance_with_system | apm_service_instance | system | static | bidirectional | apm_service_instance_with_system_relation |
| apm_service_instance_with_pod | apm_service_instance | pod | static | bidirectional | apm_service_instance_with_pod_relation |
| apm_service_with_apm_service_instance | apm_service | apm_service_instance | static | bidirectional | apm_service_with_apm_service_instance_relation |
| bklogconfig_with_datasource | bklogconfig | datasource | static | bidirectional | bklogconfig_with_datasource_relation |
| business_with_set | business | set | static | bidirectional | business_with_set_relation |
| biz_with_set | biz | set | static | bidirectional | biz_with_set_relation |
| module_with_set | module | set | static | bidirectional | module_with_set_relation |
| host_with_module | host | module | static | bidirectional | host_with_module_relation |
| host_with_system | host | system | static | bidirectional | host_with_system_relation |
| app_version_with_host | app_version | host | static | bidirectional | app_version_with_host_relation |
| app_version_with_container | app_version | container | static | bidirectional | app_version_with_container_relation |
| app_version_with_git_commit | app_version | git_commit | static | bidirectional | app_version_with_git_commit_relation |
| app_version_with_p4_changelist | app_version | p4_changelist | static | bidirectional | app_version_with_p4_changelist_relation |
| app_version_with_svn_revision | app_version | svn_revision | static | bidirectional | app_version_with_svn_revision_relation |
| pod_to_pod | pod | pod | dynamic | outbound | pod_to_pod_flow |
| pod_to_system | pod | system | dynamic | outbound | pod_to_system_flow |
| system_to_pod | system | pod | dynamic | outbound | system_to_pod_flow |
| system_to_system | system | system | dynamic | outbound | system_to_system_flow |
| service_to_service | service | service | dynamic | outbound | service_to_service_flow |
| apm_service_to_apm_service | apm_service | apm_service | dynamic | outbound | apm_service_to_apm_service_flow |
