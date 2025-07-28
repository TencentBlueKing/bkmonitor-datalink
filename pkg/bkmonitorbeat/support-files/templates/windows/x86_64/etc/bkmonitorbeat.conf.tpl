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


# ============================= Resource ==================================
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

  # 主机异常事件采集（磁盘满、磁盘只读、Corefile 事件以及 OOM 事件）
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

  # 进程采集：同步自定义进程配置文件到 bkmonitorbeat 子任务文件夹下
#  procsync_task:
#    task_id: 104
#    period: 1m
#    dst_dir: '{{ plugin_path.subconfig_path }}'

  # 时间同步服务采集
  timesync_task:
    dataid: 1100030
    task_id: 98
    period: 1m
    env: host
    query_timeout: 10s
    chrony_address: "[::1]:323"

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
  #            response: "response"
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
  #          - 'logs'
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
