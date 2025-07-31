# Beat self-config
# ================================ Logging ===================================
# repo: https://github.com/TencentBlueKing/beats
# path: libbeat/logp/config.go
logging:
  level: error
  rotateeverybytes: 10485760
  keepfiles: 7


# ================================ Output ====================================
# console for debugging
# output.console:

# bkpipe for production
output.bkpipe:
  endpoint: {{ plugin_path.endpoint }}
  synccfg: true
  fastmode: true
  concurrency: 6

seccomp.enabled: false


# ================================= Path =====================================
path:
  logs: {{ plugin_path.log_path }}
  data: {{ plugin_path.data_path }}
  pid: {{ plugin_path.pid_path }}


# ============================ Publisher Queue ===============================
# publisher 发送队列配置
# repo: https://github.com/TencentBlueKing/beats
# path: libbeat/publisher/queue/memqueue/config.go
queue:
  mem:
    events: 128
    flush.min_events: 0
    flush.timeout: "1s"


# ============================== Monitoring ==================================
xpack:
  monitoring:
    enabled: false


# =============================== Resource ===================================
resource_limit:
  enabled: false
#  # CPU 资源限制 单位 core(float64)
#  cpu: 1
#  # 内存资源限制 单位 MB(int)
#  mem: 512


# bk-collector self-config
bk-collector:
  host_id_path: {{ plugin_path.host_id }}
  # ================================= Hook ===================================
  hook:
    on_failure:
      timeout: "1m"
      scripts:
        # 保留网络现场
        - "ss -ap 2>&1>/tmp/bk-collector-`date +%s`.ssap"
        # 保留进程现场
        - "ps aux 2>&1>/tmp/bk-collector-`date +%s`.psaux"
        # 保留 profiles 现场
        - "curl -o /tmp/bk-collector-`date +%s`.profiles.tar.gz http://localhost:4318/debug/pprof/snapshot?debug=2"


  # =============================== SubConfig ================================
  apm:
    patterns:
      - "{{ plugin_path.subconfig_path }}/bk-collector-*.conf"


  # =============================== Logging ==================================
  logging:
    # stdout: true
    # optional: logfmt/json/console
    format: "console"
    level: info
    path: {{ plugin_path.log_path }}
    maxsize: 20
    maxage: 3
    backups: 5


  # ============================= Metrics Push ===============================
  bk_metrics_pusher:
    dataid: 1100014
    period: 30s
    batch_size: 1024
    labels: []
    metric_relabel_configs:


  # ================================ Cluster =================================
  cluster:
    disabled: true


  # ================================= Proxy ==================================
  proxy:
    disabled: false
    auto_reload: true
    http:
      host: ""
      port: 10205
      retry_listen: true
      middlewares:
        - "logging"
        - "maxconns;maxConnectionsRatio=256"


  # ============================== Pingserver ================================
  pingserver:
    disabled: false
    auto_reload: true
    patterns:
      - "{{ plugin_path.subconfig_path }}/bkmonitorproxy_*.conf"


  # =============================== Receiver =================================
  receiver:
    # Http Server Config
    http_server:
      # 是否启动 Http 服务
      # default: false
      enabled: true
      # 服务监听端点
      # default: ""
      endpoint: ":4318"
      middlewares:
        - "logging"
        - "cors"
        - "content_decompressor"
        - "maxconns;maxConnectionsRatio=256"
{%- if extra_vars is defined and extra_vars.http_max_bytes is defined and extra_vars.http_max_bytes != "" %}
        - "maxbytes;maxRequestBytes={{ extra_vars.http_max_bytes }}"
{%- else %}
        - "maxbytes;maxRequestBytes=209715200"
{%- endif %}

    # Admin Server Config
    admin_server:
      # 是否启动 Http 服务
      # default: false
      enabled: true
      # 服务监听端点
      # default: ""
      endpoint: "127.0.0.1:4310"
      middlewares:
        - "logging"

    # Grpc Server Config
    grpc_server:
      # 是否启动 Grpc 服务
      # default: false
      enabled: true
      # 传输协议，目前支持 tcp
      # default: ""
      transport: "tcp"
      # 服务监听端点
      # default: ""
      endpoint: ":4317"
      middlewares:
{%- if extra_vars is defined and extra_vars.grpc_max_bytes is defined and extra_vars.grpc_max_bytes != "" %}
        - "maxbytes;maxRequestBytes={{ extra_vars.grpc_max_bytes }}"
{%- else %}
        - "maxbytes;maxRequestBytes=8388608"
{%- endif %}

    # Tars Server Config
    tars_server:
      # 是否启动 Tars 服务
      # default: false
      enabled: false
      # 传输协议，目前支持 tcp
      # default: ""
      transport: "tcp"
      # 服务监听端点
      # default: ""
      endpoint: ":4319"

    components:
      jaeger:
        enabled: true
      otlp:
        enabled: true
      pushgateway:
        enabled: true
      remotewrite:
        enabled: true
      zipkin:
        enabled: true
      skywalking:
        enabled: false
      pyroscope:
        enabled: true
      fta:
        enabled: true
      beat:
        enabled: true
      tars:
        enabled: false

  processor:
    # ApdexCalculator: 健康度状态计算器
    - name: "apdex_calculator/standard"
      config:
        calculator:
          type: "standard"

    # AttributeFilter: 属性过滤处理器
    - name: "attribute_filter/common"
      config:
        as_string:
          keys:
            - "attributes.db.name"

    # ResourceFilter: 维度补充
    - name: "resource_filter/fill_dimensions"

    # ResourceFilter: 资源过滤处理器
    - name: "resource_filter/instance_id"
      config:
        assemble:
          - destination: "bk.instance.id"
            separator: ":"
            keys:
              - "resource.telemetry.sdk.language"
              - "resource.service.name"
              - "resource.net.host.name"
              - "resource.net.host.ip"
              - "resource.net.host.port"
        drop:
          keys:
            - "resource.bk.data.token"

    # ResourceFilter: 资源过滤处理器
    - name: "resource_filter/logs"
      config:
        assemble:
        drop:
          keys:
            - "resource.bk.data.token"

    # ResourceFilter: 资源过滤处理器
    - name: "resource_filter/metrics"
      config:
        assemble:
        drop:
          keys:
            - "resource.bk.data.token"
            - "resource.process.pid"
        from_token:
          keys:
            - "app_name"

    # Sampler: 采样处理器（概率采样）
    - name: "sampler/random"
      config:
        type: "random"
        sampling_percentage: 100

    # Sampler: profiles 采样处理器（做直接丢弃处理）
    - name: "sampler/drop_profiles"
      config:
        type: "drop"
        enabled: false

    # Sampler: traces 采样处理器（做直接丢弃处理）
    - name: "sampler/drop_traces"
      config:
        type: "drop"
        enabled: false

    # TokenChecker: 权限校验处理器
    - name: "token_checker/aes256"

    # TokenChecker: 权限校验处理器
    - name: "token_checker/beat"
      config:
        type: "beat"

    # ServiceDiscover: 服务发现处理器
    - name: "service_discover/common"

    # TokenChecker: 权限校验处理器
    # Proxy
    - name: "token_checker/proxy"
      config:
        type: "proxy"

    # LicenseChecker: 验证接入的节点数
    - name: "license_checker/common"

    # DbFilter: db 处理器
    - name: "db_filter/common"

    # Attribute_filter 应用层级的配置
    - name: "attribute_filter/app"

    # Attribute_filter 日志数据源的 tag 配置
    - name: "attribute_filter/logs"

    # PprofTranslator: pprof 协议转换器
    - name: "pprof_translator/common"
      config:
        type: "spy"

    # ProxyValidator: proxy 数据校验器
    - name: "proxy_validator/common"

    # RateLimiter: 流控处理器
    - name: "rate_limiter/token_bucket"
      config:
        type: token_bucket
        qps: 2000
        burst: 4000

    # TracesDeriver: 指标派生处理器
    - name: "traces_deriver/delta"
    - name: "traces_deriver/delta_duration"
    - name: "traces_deriver/bucket"
    - name: "traces_deriver/sum"
    - name: "traces_deriver/min"
    - name: "traces_deriver/max"
    - name: "traces_deriver/count"
    - name: "traces_deriver/duration"

  pipeline:
    - name: "traces_pipeline/common"
      type: "traces"
      processors:
        - "token_checker/aes256"
        - "rate_limiter/token_bucket"
        - "sampler/drop_traces"
        - "resource_filter/fill_dimensions"
        - "resource_filter/instance_id"
        - "db_filter/common"
        - "attribute_filter/common"
        - "attribute_filter/app"
        - "service_discover/common"
        - "apdex_calculator/standard"
        - "traces_deriver/delta"
        - "traces_deriver/delta_duration"
        - "traces_deriver/duration"
        - "traces_deriver/count"
        - "traces_deriver/max"
        - "traces_deriver/min"
        - "traces_deriver/sum"
        - "traces_deriver/bucket"
        - "sampler/random"

    - name: "metrics_pipeline/common"
      type: "metrics"
      processors:
        - "token_checker/aes256"
        - "rate_limiter/token_bucket"
        - "resource_filter/metrics"

    - name: "metrics_pipeline/derived"
      type: "metrics.derived"
      processors:

    - name: "logs_pipeline/common"
      type: "logs"
      processors:
        - "token_checker/aes256"
        - "rate_limiter/token_bucket"
        - "resource_filter/logs"
        - "attribute_filter/logs"

    - name: "pushgateway_pipeline/common"
      type: "pushgateway"
      processors:
        - "token_checker/aes256"
        - "rate_limiter/token_bucket"

    - name: "remotewrite_pipeline/common"
      type: "remotewrite"
      processors:
        - "token_checker/aes256"
        - "rate_limiter/token_bucket"

    - name: "proxy_pipeline/common"
      type: "proxy"
      processors:
        - "token_checker/proxy"
        - "rate_limiter/token_bucket"
        - "proxy_validator/common"

    - name: "pingserver_pipeline/common"
      type: "pingserver"
      processors:

    - name: "fta_pipeline/common"
      type: "fta"
      processors:
        - "token_checker/aes256"
        - "rate_limiter/token_bucket"

    - name: "profiles_pipeline/common"
      type: "profiles"
      processors:
        - "token_checker/aes256"
        - "pprof_translator/common"
        - "sampler/drop_profiles"

    - name: "beat_pipeline/common"
      type: "beat"
      processors:
        - "token_checker/beat"
        - "rate_limiter/token_bucket"

    - name: "tars_pipeline/common"
      type: "tars"
      processors:
        - "token_checker/aes256"
        - "rate_limiter/token_bucket"

  # =============================== Exporter =================================
  exporter:
    queue:
      metrics_batch_size: 5000
      traces_batch_size: 600
      logs_batch_size: 100
      proxy_batch_size: 3000
      profiles_batch_size: 50
      flush_interval: 3s
