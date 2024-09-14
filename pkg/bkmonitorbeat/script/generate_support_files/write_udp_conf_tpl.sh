#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.


write_udp_conf_tpl() {
  path="$1"
  system="$2"
  arch="$3"
  cat <<EOF > "$path"
EOF
  cat <<EOF >> "$path"
# 子配置信息
type: udp
name: {{ config_name | default("udp_task", true) }}
version: {{ config_version| default("1.1.1", true) }}

dataid: {{ data_id | default(1010, true) }}
# 缓冲区最大空间
max_buffer_size: {{ max_buffer_size | default(10240, true) }}
# 最大超时时间
max_timeout: {{ max_timeout | default("30s", true) }}
# 最小检测间隔
min_period: {{ min_period | default("3s", true) }}
# 任务列表
tasks: {% for task in tasks or get_hosts_by_node(config_hosts) %}
  - task_id: {{ task.task_id or task_id }}
    bk_biz_id: {{ task.bk_biz_id or bk_biz_id }}
    times: {{ task.times | default(3, true) }}
    target_ip_type: {{ task.target_ip_type | default(0, true) }}
    dns_check_mode: {{ task.dns_check_mode | default("single", true) }}
    period: {{ task.period or period }}
    # 检测超时（connect+read总共时间）
    timeout: {{ (task.timeout or timeout) | default("3s", true) }}
    target_host: {{ task.target_host or task.ip }}
    # 当配置的target_host_list不为空时，使用target_host_list，忽略target_host
    {% if task.node_list %}{% set instances = get_hosts_by_node(task.node_list) %}{% endif %}
    target_host_list: {% if task.target_host_list %}{% for target_host in task.target_host_list %}
    - {{ target_host }}{% endfor %}{% endif %}
    {% if instances %}{% for instance in instances -%}
    {% for output_field in task.output_fields -%}
    {% if instance[output_field] -%}
    - {{ instance[output_field] }}
    {% endif %}{% endfor %}{% endfor %}{% endif -%}
    target_port: {{ task.target_port or target_port }}
    available_duration: {{ task.available_duration or available_duration }}
    # 请求内容
    request: {{ task.request or request or '' }}
    # 请求格式（raw/hex）
    request_format: {{ (task.request_format or request_format) | default("raw", true) }}
    # 返回内容
    response: {{ task.response or response or ''  }}
    # 内容匹配方式
    response_format: {{ (task.response_format or response_format) | default("eq", true) }}
    # response为空时是否等待返回
    wait_empty_response: {{ (task.wait_empty_response or wait_empty_response or false) }}{% endfor %}
    {% if labels %}labels:
    {% for label in labels %}{% for key, value in label.items() %}{{"-" if loop.first else " "}} {{key}}: "{{ value }}"
    {% endfor %}{% endfor %}
    {% endif %}
EOF
}
