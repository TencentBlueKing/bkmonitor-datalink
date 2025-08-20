#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.


write_root_conf_tpl() {
  path="$1"
  system="$2"
  arch="$3"
  cat <<EOF > "$path"
EOF
  cat <<EOF >> "$path"
# ================================ Outputs =====================================
output.bkpipe:
  synccfg: true
  endpoint: '{{ plugin_path.endpoint }}'
  # 地址分配方式，static：静态 dynamic：动态
  bk_addressing: {{ cmdb_instance.host.bk_addressing|default('static', true) }}
#{%- if nodeman is defined %}
#  hostip: {{ nodeman.host.inner_ip }}
#{%- else %}
#  hostip: {{ cmdb_instance.host.bk_host_innerip_v6 if cmdb_instance.host.bk_host_innerip_v6 and not cmdb_instance.host.bk_host_innerip else cmdb_instance.host.bk_host_innerip }}
#{%- endif %}
  cloudid: {{ cmdb_instance.host.bk_cloud_id[0].id if cmdb_instance.host.bk_cloud_id is iterable and cmdb_instance.host.bk_cloud_id is not string else cmdb_instance.host.bk_cloud_id }}
  hostid: {{ cmdb_instance.host.bk_host_id }}

path.logs: '{{ plugin_path.log_path }}'
path.data: '{{ plugin_path.data_path }}'
path.pid: '{{ plugin_path.pid_path }}'
seccomp.enabled: false


# ================================ Logging ======================================
# Available log levels are: critical, error, warning, info, debug
logging.level: error
logging.path: '{{ plugin_path.log_path }}'
logging.maxsize: 10
logging.maxage: 3
logging.backups: 5


EOF
  if [ "$system" != "aix" ]; then
    cat <<EOF >> "$path"
# ============================= Resource ==================================
EOF
    cat <<EOF >> "$path"
{% if cmdb_instance.host.bk_cpu and cmdb_instance.host.bk_mem %}
{%- set resource_limit = resource_limit | default({}) -%}
resource_limit:
  enabled: true
  cpu: {{
    [
      [
        cmdb_instance.host.bk_cpu * resource_limit.get('cpu', {}).get('percentage', 0.1),
        resource_limit.get('cpu', {}).get('min', 0.1)
      ] | max,
      resource_limit.get('cpu', {}).get('max', 1)
    ] | min
  }}
  mem: {{
    [
      [
        cmdb_instance.host.bk_mem * resource_limit.get('mem', {}).get('percentage', 0.1),
        resource_limit.get('mem', {}).get('min', 100)
      ] | max,
        resource_limit.get('mem', {}).get('max', 1000)
    ] | min | int
  }}
{% endif %}

EOF
  fi
  cat <<EOF >> "$path"
# ================================= Tasks =======================================
bkmonitorbeat:
  node_id: 0
  ip: 127.0.0.1
  bk_cloud_id: 0
  # 主机CMDB信息hostid文件路径
  host_id_path: {{ plugin_path.host_id }}
  # 子任务目录
  include: '{{ plugin_path.subconfig_path }}'
  # 当前节点所属业务
  bk_biz_id: 1
  # stop/reload旧任务清理超时
  clean_up_timeout: 1s
  # 事件管道缓冲大小
  event_buffer_size: 10
  # 启动模式：daemon（正常模式）,check（执行一次，测试用）
  mode: daemon
  disable_netlink: false
  {%- if extra_vars is defined and extra_vars.metricbeat_align_ts is defined and extra_vars.metricbeat_align_ts == "true" %}
  metricbeat_align_ts: true
  {%- endif %}
  metrics_batch_size: 1024

  # 是否为多租户模式（默认不开启）
  # TODO(mando): 合并分之前需要调整为 false
  enable_multi_tenant: true
  # 多租户场景下需要映射的 tasks 列表
  multi_tenant_tasks: ["basereport","exceptionbeat","processbeat_perf","processbeat_port","global_heartbeat","gather_up_beat","timesync","dmesg"]
  # 多租户场景下 gse 新的通信管道 ipc 地址
  {%- if control_info is defined and control_info.pluginipc is defined %}
  gse_message_endpoint: '{{ control_info.pluginipc }}'
  {%- endif %}
  # 管理服务，包含指标和调试接口, 可动态reload开关或变更监听地址（unix使用SIGUSR2,windows发送bkreload2）
  # admin_addr: localhost:56060
  # 并发限制，按照任务类型区分(http, tcp, udp, ping)，分为per_instance单实例限制和per_task单任务限制
  concurrency_limit:
    task:
      http:
        per_instance: 100000
        per_task: 1000
      tcp:
        per_instance: 100000
        per_task: 1000
      udp:
        per_instance: 100000
        per_task: 1000
      ping:
        per_instance: 100000
        per_task: 1000

  # 心跳采集配置
  heart_beat:
    global_dataid: 1100001
    child_dataid: 1100002
    period: 60s
    publish_immediately: true

  # 任务执行状态配置
  gather_up_beat:
    dataid: 1100017

  # 自监控指标采集
  selfstats_task:
    dataid: 1100030
    task_id: 88
    period: 1m

EOF
  cat <<EOF >> "$path"
  # 静态资源采集配置
  static_task:
    dataid: 1100010
    tasks:
    - task_id: 100
      period: 1m
      check_period: 1m
      report_period: 6h
      virtual_iface_whitelist: ["bond1"]

  # 主机性能数据采集
EOF
  if [ "$system" = "aix" ]; then
    cat <<EOF >> "$path"
#  basereport_task:
#    task_id: 101
#    dataid: 1001
#    period: 1m
#    cpu:
#      stat_times: 4
#      info_period: 1m
#      info_timeout: 30s
#    disk:
#      stat_times: 1
#      mountpoint_black_list: ["docker","container","k8s","kubelet"]
#      fs_type_white_list: ["overlay","btrfs","ext2","ext3","ext4","reiser","xfs","ffs","ufs","jfs","jfs2","vxfs","hfs","apfs","refs","ntfs","fat32","zfs"]
#      collect_all_device: true
#    mem:
#      info_times: 1
#    net:
#      stat_times: 4
#      revert_protect_number: 100
#      skip_virtual_interface: false
#      interface_black_list: ["veth", "cni", "docker", "flannel", "tunnat", "cbr", "kube-ipvs", "dummy"]
#      force_report_list: ["bond"]

EOF
  else
    cat <<EOF >> "$path"
  basereport_task:
    task_id: 101
    dataid: 1001
    period: 1m
    cpu:
      stat_times: 4
      info_period: 1m
      info_timeout: 30s
    disk:
      stat_times: 1
      mountpoint_black_list: ["docker","container","k8s","kubelet","blueking"]
{%- if extra_vars is defined and extra_vars.fs_type_white_list is defined %}
      fs_type_white_list: {{ extra_vars.fs_type_white_list | default(["overlay","btrfs","ext2","ext3","ext4","reiser","xfs","ffs","ufs","jfs","jfs2","vxfs","hfs","apfs","refs","ntfs","fat32","zfs"], true) }}
{%- else %}
      fs_type_white_list: ["overlay","btrfs","ext2","ext3","ext4","reiser","xfs","ffs","ufs","jfs","jfs2","vxfs","hfs","apfs","refs","ntfs","fat32","zfs"]
{%- endif %}
      collect_all_device: true
    mem:
      info_times: 1
    net:
      stat_times: 4
      revert_protect_number: 100
      skip_virtual_interface: false
      interface_black_list: ["veth", "cni", "docker", "flannel", "tunnat", "cbr", "kube-ipvs", "dummy"]
      force_report_list: ["bond"]

EOF
  fi
  cat <<EOF >> "$path"
  # 主机异常事件采集（磁盘满、磁盘只读、Corefile 事件以及 OOM 事件）
EOF
  if [ "$system" = "aix" ]; then
    cat <<EOF >> "$path"
#  exceptionbeat_task:
#    task_id: 102
#    dataid: 1000
#    period: 1m
#    check_bit: "C_DISK_SPACE|C_DISKRO|C_CORE|C_OOM"
#    check_disk_ro_interval: 60
#    check_disk_space_interval: 60
#    check_oom_interval: 10
#    used_max_disk_space_percent: 95

EOF
  else
    cat <<EOF >> "$path"
  exceptionbeat_task:
    task_id: 102
    dataid: 1000
    period: 1m
    check_bit: "C_DISK_SPACE|C_DISKRO|C_CORE|C_OOM"
    check_disk_ro_interval: 60
    check_disk_space_interval: 60
    check_oom_interval: 10
    used_max_disk_space_percent: 95
    free_min_disk_space: 10
{%- if extra_vars is defined and extra_vars.corefile_pattern is defined %}
    corefile_pattern: {{ extra_vars.corefile_pattern or '' }}
{%- endif %}
{%- if extra_vars is defined and extra_vars.corefile_match_regex is defined %}
    corefile_match_regex: {{ extra_vars.corefile_match_regex or '' }}
{%- endif %}
    disk_ro_black_list: ["docker","container","k8s","kubelet","blueking"]
EOF
  cat <<EOF >> "$path"
  # 进程采集：同步 CMDB 进程配置文件到 bkmonitorbeat 子任务文件夹下
  procconf_task:
    task_id: 103
    period: 1m
    perfdataid: 1007
    portdataid: 1013
    converge_pid: true
    disable_netlink: false
    hostfilepath: {{ plugin_path.host_id }}
    dst_dir: '{{ plugin_path.subconfig_path }}'

  # 进程状态
  procstatus_task:
{%- if nodeman is defined and nodeman.constants is defined %}
    dataid: {{ nodeman.constants.proc_status_dataid|default(0, true) }}
{%- else %}
    dataid: 0
{%- endif %}
    task_id: 105
    period: 1m
    # 上报周期
    report_period: 24h

EOF
    cat <<EOF >> "$path"
  # 进程采集：同步自定义进程配置文件到 bkmonitorbeat 子任务文件夹下
#  procsync_task:
#    task_id: 104
#    period: 1m
#    dst_dir: '{{ plugin_path.subconfig_path }}'

EOF
fi
  if [ "$system" = "linux" ]; then
        cat <<EOF >> "$path"
  # 时间同步服务采集
  timesync_task:
    dataid: 1100030
    task_id: 98
    period: 1m
    env: host
    query_timeout: 10s
    ntpd_path: /etc/ntp.conf
    chrony_address: "[::1]:323"

  # dmesg 事件采集
  dmesg_task:
    dataid: 1100031
    task_id: 99
    period: 1m

{%- if extra_vars is defined and extra_vars.enable_audit_tasks is defined and extra_vars.enable_audit_tasks == "true" %}
  # 登录日志采集
  loginlog_task:
    dataid: 1100021
    task_id: 110
    period: 10m

  # shellhistory 采集
  shellhistory_task:
    task_id: 111
    dataid: 1100020
    period: 10m
    last_bytes: 1048576 # 1MB
    history_files:
    - ".bash_history"

  # 进程快照采集
  procsnapshot_task:
    task_id: 112
    dataid: 1100018
    period: 1m

  # 网络快照采集
  socketsnapshot_task:
    task_id: 113
    dataid: 1100019
    period: 1m
    detector: netlink

  # rpmpackge 数据采集
  rpmpackage_task:
    task_id: 114
    dataid: 1100022
    period: 24h
    block_write_bytes: 5242880
    block_read_bytes: 5242880
    block_write_iops: 10
    block_read_iops: 10

  # 二进制属性快照采集
  procbin_task:
    task_id: 115
    dataid: 1100024
    period: 1h
    max_bytes: 10485760
# ---------
{%- endif %}

EOF
fi
  if [ "$system" = "windows" ]; then
        cat <<EOF >> "$path"
  # 时间同步服务采集
  timesync_task:
    dataid: 1100030
    task_id: 98
    period: 1m
    env: host
    query_timeout: 10s
    chrony_address: "[::1]:323"

EOF
fi
  cat <<EOF >> "$path"
  #### tcp_task child config #####
  # tcp任务全局设置
  #  tcp_task:
  #    dataid: 101176
  #    # 缓冲区最大空间
  #    max_buffer_size: 10240
  #    # 最大超时时间
  #    max_timeout: 30s
  #    # 最小检测间隔
  #    min_period: 3s
  #    # 任务列表
  #    tasks:
  #      - task_id: 1
  #        bk_biz_id: 1
  #        period: 60s
  #        # 检测超时（connect+read总共时间）
  #        timeout: 3s
  #        target_host: 127.0.0.1
  #        target_port: 9202
  #        available_duration: 3s
  #        # 请求内容
  #        request: hi
  #        # 请求格式（raw/hex）
  #        request_format: raw
  #        # 返回内容
  #        response: hi
  #        # 内容匹配方式
  #        response_format: eq

  #### udp_task child config #####
  #  udp_task:
  #    dataid: 0
  #    # 缓冲区最大空间
  #    max_buffer_size: 10240
  #    # 最大超时时间
  #    max_timeout: 30s
  #    # 最小检测间隔
  #    min_period: 3s
  #    # 最大重试次数
  #    max_times: 3
  #    # 任务列表
  #    tasks:
  #      - task_id: 5
  #        bk_biz_id: 1
  #        times: 3
  #        period: 60s
  #        # 检测超时（connect+read总共时间）
  #        timeout: 3s
  #        target_host: 127.0.0.1
  #        target_port: 9201
  #        available_duration: 3s
  #        # 请求内容
  #        request: hello
  #        # 请求格式（raw/hex）
  #        request_format: raw
  #        # 返回内容
  #        response: hello
  #        # 内容匹配方式
  #        response_format: eq
  #        # response为空时是否等待返回
  #        wait_empty_response: false

  #### http_task child config #####
  #  http_task:
  #    dataid: 0
  #    # 缓冲区最大空间
  #    max_buffer_size: 10240
  #    # 最大超时时间
  #    max_timeout: 30s
  #    # 最小检测间隔
  #    min_period: 3s
  #    # 任务列表
  #    tasks:
  #      - task_id: 5
  #        bk_biz_id: 1
  #        period: 60s
  #        # proxy: http://proxy.qq.com:8000
  #        # 是否校验证书
  #        insecure_skip_verify: false
  #        disable_keep_alives: false
  #        # 检测超时（connect+read总共时间）
  #        timeout: 3s
  #        # 采集步骤
  #        steps:
  #          - url: http://127.0.0.1:9203/path/to/test
  #            method: GET
  #            headers:
  #              referer: http://bk.tencent.com
  #            available_duration: 3s
  #            request: ""
  #            # 请求格式（raw/hex）
  #            request_format: raw
EOF
  if [ "$system" = "windows" ]; then
       cat <<EOF >> "$path"
  #            response: "response"
EOF
  else
    cat <<EOF >> "$path"
  #            response: "/path/to/test"
EOF
  fi
  cat <<EOF >> "$path"
  #            # 内容匹配方式
  #            response_format: eq
  #            response_code: 200,201

  #### metricbeat_task child config #####
  #  metricbeat_task:
  #    dataid: 0
  #    # 缓冲区最大空间
  #    max_buffer_size: 10240
  #    # 最大超时时间
  #    max_timeout: 100s
  #    # 最小检测间隔
  #    min_period: 3s
  #    tasks:
  #      - task_id: 5
  #        bk_biz_id: 1
  #        # 周期
  #        period: 60s
  #        # 超时
  #        timeout: 60s
  #        module:
  #          module: mysql
  #          metricsets: ["allstatus"]
  #          enabled: true
  #          hosts: ["root:mysql123@tcp(127.0.0.1:3306)/"]

  #### script_task child config #####
  #  script_task:
  #    dataid: 0
  #    tasks:
  #      - bk_biz_id: 2
  #        command: echo 'value' 45
  #        dataid: 0
  #        period: 1m
  #        task_id: 7
  #        timeout: 60s
  #        user_env: {}

  #### keyword_task child config #####
  #  keyword_task:
  #    dataid: 0
  #    tasks:
  #      - task_id: 5
  #        bk_biz_id: 2
  #        dataid: 12345
  #        # 采集文件路径
  #        paths:
EOF
  if [ "$system" = "windows" ]; then
       cat <<EOF >> "$path"
  #          - 'logs'
EOF
  else
    cat <<EOF >> "$path"
  #          - '/var/log/messages'
EOF
  fi
  cat <<EOF >> "$path"
  #
  #        # 需要排除的文件列表，正则表示
  #        # exclude_files:
  #        #  - '.*\.py'
  #
  #        # 文件编码类型
  #        encoding: 'utf-8'
  #        # 文件未更新需要删除的超时等待
  #        close_inactive: '86400s'
  #        # 上报周期
  #        report_period: '1m'
  #        # 日志关键字匹配规则
  #        keywords:
  #          - name: HttpError
  #            pattern: '.*ERROR.*'
  #
  #        # 结果输出格式
  #        # output_format: 'event'
  #
  #        # 上报时间单位，默认ms
  #        # time_unit: 'ms'
  #
  #        # 采集目标
  #        target: '0:127.0.0.1'
  #        # 注入的labels
  #        labels:
  #          - bk_target_service_category_id: ""
  #            bk_collect_config_id: "59"
  #            bk_target_cloud_id: "0"
  #            bk_target_topo_id: "1"
  #            bk_target_ip: "127.0.0.1"
  #            bk_target_service_instance_id: ""
  #            bk_target_topo_level: "set"
EOF
}
