#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

write_proccustom_conf_tpl() {
  path="$1"
  system="$2"
  arch="$3"
  cat <<EOF > "$path"
EOF
  cat <<EOF >> "$path"
# 子配置信息
name: proccustom_task
version: 1.0.0
type: proccustom
period: {{ config.period }}
dataid: {{ config.dataid }}
task_id: {{ config.taskid }}
port_dataid: {{ config.port_dataid }}
{% if config.match_pattern %}match_pattern: {{ config.match_pattern }}{% endif %}
{% if config.process_name %}process_name:  {{ config.process_name }}{% endif %}
{% if config.extract_pattern is not None %}extract_pattern: {{ config.extract_pattern }}{% endif %}
{% if config.exclude_pattern %}exclude_pattern: {{ config.exclude_pattern }}{% endif %}
{% if config.pid_path %}pid_path: {{ config.pid_path }}{% endif %}
proc_metric: []
{% if config.port_detect %}port_detect: true {% else %}port_detect: false{% endif %}
ports: []
listen_port_only: false
report_unexpected_port: false
disable_mapping: false
# 注入的labels
labels:{% for label in config.labels %}
    {% for key, value in label.items() %}{{ "-" if loop.first else " "  }} {{ key }}: "{{ value }}"
    {% endfor %}{% endfor %}
{% if config.tags|length >  0 %}
tags:{% for key, value in config.tags.items() %}
  {{ key }}: "{{ value }}"{% endfor %}
  {% else %}
tags: null
{% endif %}
EOF
}