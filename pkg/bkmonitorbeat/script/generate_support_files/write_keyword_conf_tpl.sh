#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.


write_keyword_conf_tpl() {
  path="$1"
  system="$2"
  arch="$3"
  cat <<EOF > "$path"
EOF
  cat <<EOF >> "$path"
# 日志关键字配置模板
type: keyword
name: {{ config_name | default("keyword_task", true) }}
version: {{ config_version| default("1.1.1", true) }}

# 配置框架需要，这里补充0，实际dataid在tasks下
dataid: 0

tasks: {% for task in tasks %}
   - task_id: {{ task.task_id }}
     bk_biz_id: {{ task.bk_biz_id }}
     dataid: {{ task.dataid | int }}
     # 采集文件路径
     paths:{% for path in task.path_list %}
       - '{{ path }}'{% endfor %}
     # 文件编码类型
     encoding: '{{ task.encoding | lower}}'
     # 文件未更新需要删除的超时等待
     close_inactive: '86400s'
     # 上报周期
     report_period: '1m'
     # 日志文本过滤规则
     filter_patterns:{% for pattern in task.filter_patterns %}
       - '{{ pattern | replace("'", "''") }}'{% endfor %}
     # 日志关键字匹配规则
     keywords:{% for task in task.task_list %}
       - name: '{{ task['name'] }}'
         pattern: '{{ task['pattern'] | replace("'", "''") }}'{% endfor %}
     # 采集目标
     target: '{{ task.target }}'
     # 注入的labels
     labels:{% for label in task.labels %}
          {% for key, value in label.items() %}{{ "-" if loop.first else " "  }} {{ key }}: "{{ value }}"
          {% endfor %}{% endfor %}
     # 运行时加入新文件往前读取字节（默认 1M）
     retain_file_bytes: 1048576
{% endfor %}
EOF
}
