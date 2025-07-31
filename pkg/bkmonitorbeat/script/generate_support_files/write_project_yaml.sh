#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.


write_project_yaml() {
  path="$1"
  system="$2"
  arch="$3"
  cat <<EOF > "$path"
EOF
  cat <<EOF >> "$path"
name: bkmonitorbeat
EOF
  if [ "$system" = "windows" ] ;then
    cat <<EOF >> "$path"
version: 1.0.5
EOF
  else
    cat <<EOF >> "$path"
version: 1.9.1
EOF
  fi
  cat <<EOF >> "$path"
description: 蓝鲸监控指标采集器
scenario: 蓝鲸监控拨测采集器 支持多协议多任务的采集，监控和可用率计算，提供多种运行模式和热加载机制
category: official
config_file: bkmonitorbeat.conf
config_format: yaml
launch_node: all
auto_launch: 1
is_binary: 1
use_db: 0
config_templates:
  - plugin_version: "*"
    name: bkmonitorbeat.conf
    version: 1
    file_path: etc
    format: yaml
    is_main_config: 1
    source_path: etc/bkmonitorbeat.conf.tpl
    variables:
      type: object
      title: variables
      properties:
        extra_vars:
          title: extra_vars
          type: object
          properties:
            fs_type_white_list:
              title: fs_type_white_list
              type: array
              items:
                title: interface
                type: string
            corefile_pattern:
              title: corefile_pattern
              type: string
            corefile_match_regex:
              title: corefile_match_regex
              type: string
            disable_resource_limit:
              title: disable_resource_limit
              type: string
            enable_audit_tasks:
              title: enable_audit_tasks
              type: string
            metricbeat_align_ts:
              title: metricbeat_align_ts
              type: string
  - plugin_version: "*"
    name: bkmonitorbeat_prometheus.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/bkmonitorbeat_prometheus.conf.tpl
  - plugin_version: "*"
    name: bkmonitorbeat_script.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/bkmonitorbeat_script.conf.tpl
  - plugin_version: "*"
    name: bkmonitorbeat_http.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/bkmonitorbeat_http.conf.tpl
  - plugin_version: "*"
    name: bkmonitorbeat_tcp.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/bkmonitorbeat_tcp.conf.tpl
  - plugin_version: "*"
    name: bkmonitorbeat_udp.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/bkmonitorbeat_udp.conf.tpl
  - plugin_version: "*"
    name: bkmonitorbeat_keyword.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/bkmonitorbeat_keyword.conf.tpl
  - plugin_version: "*"
    name: bkmonitorbeat_icmp.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/bkmonitorbeat_icmp.conf.tpl
  - plugin_version: "*"
    name: monitor_process.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/monitor_process.conf.tpl
EOF
  if [ "$system" = "aix" ] ;then
    cat <<EOF >> "$path"
control:
  start: "./start.ksh bkmonitorbeat"
  stop: "./stop.ksh bkmonitorbeat"
  restart: "./restart.ksh bkmonitorbeat"
  reload: "./reload.ksh bkmonitorbeat"
  version: "./bkmonitorbeat -v"
EOF
  elif [ "$system" = "windows" ] ;then
    cat <<EOF >> "$path"
  - plugin_version: "*"
    name: bkmonitorbeat_snmptrap.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/bkmonitorbeat_snmptrap.conf.tpl
  - plugin_version: "*"
    name: bkmonitorbeat_prometheus_remote.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/bkmonitorbeat_prometheus_remote.conf.tpl
control:
  start: "start.bat bkmonitorbeat"
  stop: "stop.bat bkmonitorbeat"
  restart: "restart.bat bkmonitorbeat"
  reload: "restart.bat bkmonitorbeat"
  version: "bkmonitorbeat -v"
EOF
  else
    cat <<EOF >> "$path"
  - plugin_version: "*"
    name: bkmonitorbeat_snmptrap.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/bkmonitorbeat_snmptrap.conf.tpl
  - plugin_version: "*"
    name: bkmonitorbeat_prometheus_remote.conf
    version: 1
    file_path: etc/bkmonitorbeat
    format: yaml
    source_path: etc/bkmonitorbeat_prometheus_remote.conf.tpl
control:
  start: "./start.sh bkmonitorbeat"
  stop: "./stop.sh bkmonitorbeat"
  restart: "./restart.sh bkmonitorbeat"
  reload: "./reload.sh bkmonitorbeat"
  version: "./bkmonitorbeat -v"
EOF
  fi
}
