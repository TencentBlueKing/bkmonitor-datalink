#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.


current_dir="$(realpath "$(dirname "$0")")"
for file in  "$current_dir"/generate_support_files/*.sh; do
  source "$file"
done

generate_template() {
  dir=$1
  system="$2"
  arch="$3"
  with_out_proc="$4"
  template_dir='templates'
  work_path="$dir/$template_dir/$system/$arch"
  mkdir -p "$work_path"
  write_project_yaml "$work_path/project.yaml" "$system" "$arch"
  etc_path="$work_path/etc"
  mkdir -p "$etc_path"
  write_root_conf "$etc_path/bkmonitorbeat.conf" "$system" "$arch"
  write_root_conf_tpl "$etc_path/bkmonitorbeat.conf.tpl" "$system" "$arch"
  write_http_conf_tpl "$etc_path/bkmonitorbeat_http.conf.tpl" "$system" "$arch"
  write_icmp_conf_tpl "$etc_path/bkmonitorbeat_icmp.conf.tpl" "$system" "$arch"
  write_keyword_conf_tpl "$etc_path/bkmonitorbeat_keyword.conf.tpl" "$system" "$arch"
  write_prometheus_conf_tpl "$etc_path/bkmonitorbeat_prometheus.conf.tpl" "$system" "$arch"
  write_prometheus_remote_conf_tpl "$etc_path/bkmonitorbeat_prometheus_remote.conf.tpl" "$system" "$arch"
  write_script_conf_tpl "$etc_path/bkmonitorbeat_script.conf.tpl" "$system" "$arch"
  write_snmptrap_conf_tpl "$etc_path/bkmonitorbeat_snmptrap.conf.tpl" "$system" "$arch"
  write_tcp_conf_tpl "$etc_path/bkmonitorbeat_tcp.conf.tpl" "$system" "$arch"
  write_udp_conf_tpl "$etc_path/bkmonitorbeat_udp.conf.tpl" "$system" "$arch"
  write_proccustom_conf_tpl "$etc_path/monitor_process.conf.tpl" "$system" "$arch"
}

generate_templates() {
  dir=$1
  for system in freebsd linux windows
  do
    if [ "$system" = 'freebsd' ]; then
        archs='x86_64'
    elif [ "$system" = 'linux' ]; then
        archs='aarch64 x86 x86_64'
    elif [ "$system" = 'windows' ]; then
        archs='x86 x86_64'
    fi
    for arch in $archs
    do
      generate_template "$dir" "$system" "$arch"
    done
  done
}

target_dir="$(dirname "$current_dir")"/support-files
echo "writing to $target_dir"
generate_templates "$target_dir"
