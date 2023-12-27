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


  # ============================= LabelStorage ===============================
  label_storage:
    type: "builtin"
    dir: "."


  # ============================= TraceStorage ===============================
  trace_storage:
    type: "builtin"
    dir: "."


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
        - "maxconns"


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
        - "maxconns"
        - "maxbytes"

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
        - "maxbytes"

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
        enabled: false
      skywalking:
        enabled: false

  processor:
    # ApdexCalculator: 健康度状态计算器
    - name: "apdex_calculator/standard"
      config:
        calculator:
          type: "standard"
        rules:
          - kind: ""
            metric_name: "bk_apm_duration"
            destination: "apdex_type"
            apdex_t: 20 # ms

    # AttributeFilter: 属性过滤处理器
    - name: "attribute_filter/as_string"
      config:
        as_string:
          keys:
            - "attributes.db.name"

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
    - name: "resource_filter/drop_token"
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

    # Sampler: 采样处理器
    - name: "sampler/random"
      config:
        type: "random"
        sampling_percentage: 100

    # TokenChecker: 权限校验处理器
    - name: "token_checker/aes256"

    # ServiceDiscover: 服务发现处理器
    - name: "service_discover/common"

    # TokenChecker: 权限校验处理器
    # Proxy
    - name: "token_checker/proxy"
      config:
        type: "proxy"

    # LicenseChecker: 验证接入的节点数
    - name: "license_checker/common"

    # ProxyValidator: proxy 数据校验器
    - name: "proxy_validator/common"

    # RateLimiter: 流控处理器
    - name: "rate_limiter/token_bucket"
      config:
        type: token_bucket
        qps: 2000
        burst: 4000

    - name: "traces_deriver/delta"
      config:
        operations:
          - type: "delta"
            metric_name: "bk_apm_count"
            publish_interval: "60s"
            gc_interval: "1h"
            rules:
              - kind: "SPAN_KIND_SERVER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.server_name"
                  - "attributes.http.method"
                  - "attributes.http.scheme"
                  - "attributes.http.flavor"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.method"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.db.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.db.name"
                  - "attributes.db.operation"
                  - "attributes.db.system"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.messaging.destination"
                  - "attributes.messaging.destination_kind"
                  - "attributes.celery.action"
                  - "attributes.celery.task_name"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.celery.state"
                  - "attributes.celery.action"

    - name: "traces_deriver/delta_duration"
      config:
        operations:
          - type: "delta_duration"
            metric_name: "bk_apm_duration_delta"
            publish_interval: "60s"
            gc_interval: "1h"
            rules:
              - kind: "SPAN_KIND_SERVER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.server_name"
                  - "attributes.http.method"
                  - "attributes.http.scheme"
                  - "attributes.http.flavor"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.method"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.db.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.db.name"
                  - "attributes.db.operation"
                  - "attributes.db.system"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.messaging.destination"
                  - "attributes.messaging.destination_kind"
                  - "attributes.celery.action"
                  - "attributes.celery.task_name"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.celery.state"
                  - "attributes.celery.action"

    - name: "traces_deriver/bucket"
      config:
        operations:
          - type: "bucket"
            metric_name: "bk_apm_duration_bucket"
            publish_interval: "60s"
            gc_interval: "1h"
            buckets: [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10]
            rules:
              - kind: "SPAN_KIND_SERVER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.server_name"
                  - "attributes.http.method"
                  - "attributes.http.scheme"
                  - "attributes.http.flavor"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.method"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.db.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.db.name"
                  - "attributes.db.operation"
                  - "attributes.db.system"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.messaging.destination"
                  - "attributes.messaging.destination_kind"
                  - "attributes.celery.action"
                  - "attributes.celery.task_name"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.celery.state"
                  - "attributes.celery.action"

    - name: "traces_deriver/sum"
      config:
        operations:
          - type: "sum"
            metric_name: "bk_apm_duration_sum"
            publish_interval: "60s"
            gc_interval: "1h"
            rules:
              - kind: "SPAN_KIND_SERVER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.server_name"
                  - "attributes.http.method"
                  - "attributes.http.scheme"
                  - "attributes.http.flavor"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.method"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.db.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.db.name"
                  - "attributes.db.operation"
                  - "attributes.db.system"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.messaging.destination"
                  - "attributes.messaging.destination_kind"
                  - "attributes.celery.action"
                  - "attributes.celery.task_name"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.celery.state"
                  - "attributes.celery.action"

    - name: "traces_deriver/min"
      config:
        operations:
          - type: "min"
            metric_name: "bk_apm_duration_min"
            publish_interval: "60s"
            gc_interval: "1h"
            rules:
              - kind: "SPAN_KIND_SERVER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.server_name"
                  - "attributes.http.method"
                  - "attributes.http.scheme"
                  - "attributes.http.flavor"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.method"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.db.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.db.name"
                  - "attributes.db.operation"
                  - "attributes.db.system"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.messaging.destination"
                  - "attributes.messaging.destination_kind"
                  - "attributes.celery.action"
                  - "attributes.celery.task_name"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.celery.state"
                  - "attributes.celery.action"

    - name: "traces_deriver/max"
      config:
        operations:
          - type: "max"
            metric_name: "bk_apm_duration_max"
            publish_interval: "60s"
            gc_interval: "1h"
            rules:
              - kind: "SPAN_KIND_SERVER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.server_name"
                  - "attributes.http.method"
                  - "attributes.http.scheme"
                  - "attributes.http.flavor"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.method"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.db.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.db.name"
                  - "attributes.db.operation"
                  - "attributes.db.system"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.messaging.destination"
                  - "attributes.messaging.destination_kind"
                  - "attributes.celery.action"
                  - "attributes.celery.task_name"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.celery.state"
                  - "attributes.celery.action"

    - name: "traces_deriver/count"
      config:
        operations:
          - type: "count"
            metric_name: "bk_apm_total"
            publish_interval: "60s"
            gc_interval: "1h"
            rules:
              - kind: "SPAN_KIND_SERVER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.server_name"
                  - "attributes.http.method"
                  - "attributes.http.scheme"
                  - "attributes.http.flavor"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.method"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.db.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.db.name"
                  - "attributes.db.operation"
                  - "attributes.db.system"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.messaging.destination"
                  - "attributes.messaging.destination_kind"
                  - "attributes.celery.action"
                  - "attributes.celery.task_name"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.celery.state"
                  - "attributes.celery.action"

    # TracesDeriver: Traces 派生处理器
    - name: "traces_deriver/duration"
      config:
        operations:
          - type: "duration"
            metric_name: "bk_apm_duration"
            rules:
              - kind: "SPAN_KIND_SERVER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.server_name"
                  - "attributes.http.method"
                  - "attributes.http.scheme"
                  - "attributes.http.flavor"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_SERVER"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.http.method"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.http.method"
                  - "attributes.http.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.rpc.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.rpc.method"
                  - "attributes.rpc.service"
                  - "attributes.rpc.system"
                  - "attributes.rpc.grpc.status_code"
              - kind: "SPAN_KIND_CLIENT"
                predicate_key: "attributes.db.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.db.name"
                  - "attributes.db.operation"
                  - "attributes.db.system"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_PRODUCER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.messaging.destination"
                  - "attributes.messaging.destination_kind"
                  - "attributes.celery.action"
                  - "attributes.celery.task_name"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: ""
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
              - kind: "SPAN_KIND_CONSUMER"
                predicate_key: "attributes.messaging.system"
                dimensions:
                  - "resource.bk.instance.id"
                  - "span_name"
                  - "kind"
                  - "status.code"
                  - "resource.service.name"
                  - "resource.service.version"
                  - "resource.telemetry.sdk.name"
                  - "resource.telemetry.sdk.version"
                  - "resource.telemetry.sdk.language"
                  - "attributes.peer.service"
                  - "attributes.messaging.system"
                  - "attributes.celery.state"
                  - "attributes.celery.action"

  pipeline:
    - name: "traces_pipeline/common"
      type: "traces"
      processors:
        - "token_checker/aes256"
        - "rate_limiter/token_bucket"
        - "resource_filter/instance_id"
        - "attribute_filter/as_string"
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
        - "resource_filter/drop_token"

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

  # =============================== Exporter =================================
  exporter:
    queue:
      metrics_batch_size: 5000
      traces_batch_size: 600
      logs_batch_size: 100
      flush_interval: 3s
