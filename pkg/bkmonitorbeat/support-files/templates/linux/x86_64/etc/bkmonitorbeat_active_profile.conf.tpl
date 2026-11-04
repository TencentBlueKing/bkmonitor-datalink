# 子配置信息：主动 Profiling 计划任务（once_at / cron），由节点管理按主机渲染下发。
# 立即执行走 JOB + 轻量 CLI，不经过本模板。
#
# params.context 约定：host_tasks 是按 bk_host_id（字符串）分组的单机执行配置，
# 模板按 cmdb_instance.host.bk_host_id 取出当前主机那一组；取不到就下发空列表，
# 该主机上的主动 Profiling 计划任务随之全部移出调度。
# target_hosts 是计划全部目标主机的快照 [{bk_host_id, ip}]，全部主机共用一份，
# 原样渲染进每个 task，由 Beat 写入 Event content。
# 字段结构由本模板定义、随插件包版本管理；SaaS 只负责按主机拼装字段的值。
type: active_profile
name: active_profile
version: "1"

dataid: 0
max_timeout: 15m
min_period: 1m

{% set tasks = host_tasks.get(cmdb_instance.host.bk_host_id | string, []) %}
tasks:{% if not tasks %} []{% endif %}
{% for task in tasks %}
  - task_id: {{ task.task_id | tojson }}
    bk_tenant_id: {{ task.bk_tenant_id | tojson }}
    bk_biz_id: {{ task.bk_biz_id | tojson }}
    bk_host_id: {{ task.bk_host_id | tojson }}
    name: {{ task.name | tojson }}
    creator: {{ task.creator | tojson }}
    collection_method: {{ task.collection_method | tojson }}
    collection_type: {{ task.collection_type | tojson }}

    schedule:
      type: {{ task.schedule.type | tojson }}
      timezone: {{ task.schedule.timezone | tojson }}
      {% if task.schedule.type == "once_at" %}
      execute_at: {{ task.schedule.execute_at | tojson }}
      {% else %}
      cron_expression: {{ task.schedule.cron_expression | tojson }}
      {% if task.schedule.effective_start_at %}effective_start_at: {{ task.schedule.effective_start_at | tojson }}{% endif %}
      {% if task.schedule.effective_end_at %}effective_end_at: {{ task.schedule.effective_end_at | tojson }}{% endif %}
      {% endif %}

    {% if task.collection_method != "golang" %}
    process_selector:
      type: {{ task.process_selector.type | tojson }}
      {% if task.process_selector.type == "command" %}
      match_pattern: {{ task.process_selector.match_pattern | tojson }}
      {% if task.process_selector.exclude_pattern %}exclude_pattern: {{ task.process_selector.exclude_pattern | tojson }}{% endif %}
      enable_dynamic_collection: {{ task.process_selector.enable_dynamic_collection | tojson }}
      {% if task.process_selector.host_pids %}host_pids: {{ task.process_selector.host_pids | tojson }}{% endif %}
      {% elif task.process_selector.type == "pid_file" %}
      pid_path: {{ task.process_selector.pid_path | tojson }}
      {% elif task.process_selector.type == "pid" %}
      pids: {{ task.process_selector.pids | tojson }}
      {% endif %}
    {% endif %}

    collector_config:
      {% if task.collection_method == "golang" %}
      ip_address: {{ task.collector_config.ip_address | tojson }}
      port: {{ task.collector_config.port | tojson }}
      {% if task.collection_type == "go_cpu" %}duration_sec: {{ task.collector_config.duration_sec | tojson }}{% endif %}
      {% else %}
      duration_sec: {{ task.collector_config.duration_sec | tojson }}
      frequency_hz: {{ task.collector_config.frequency_hz | tojson }}
      stack_unwind: {{ task.collector_config.stack_unwind | tojson }}
      {% endif %}

    {% if task.resource_guard_config %}
    resource_guard_config:
      auto_stop_on_resource_pressure: {{ task.resource_guard_config.auto_stop_on_resource_pressure | tojson }}
      max_disk_usage: {{ task.resource_guard_config.max_disk_usage | tojson }}
      min_mem_level: {{ task.resource_guard_config.min_mem_level | tojson }}
    {% endif %}

    target_hosts: {{ target_hosts | tojson }}

    event_data_id: {{ task.event_data_id | tojson }}
    profile_data_id: {{ task.profile_data_id | tojson }}
    profile_app_name: {{ task.profile_app_name | tojson }}
    profile_service_name: {{ task.profile_service_name | tojson }}
{% endfor %}
