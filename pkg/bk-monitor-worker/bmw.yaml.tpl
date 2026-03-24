# ================================ Http配置  ===================================
service:
  mode: release
  task:
    listen: 127.0.0.1
    port: 10211
  controller:
    listen: 127.0.0.1
    port: 10212
  worker:
    listen: 127.0.0.1
    port: 10213

# ================================ Broker配置  ===================================
broker:
  redis:
    mode: standalone
    db: 0
    dialTimeout: 10s
    readTimeout: 10s
    standalone:
      host: 127.0.0.1
      port: 6379
      password: "123456"
    sentinel:
      masterName: ""
      address:
        - 127.0.0.1
      password: ""

# ================================ 存储配置  ===================================
# 旁路配置，用于数据对账
store:
  redis:
    mode: standalone
    db: 0
    dialTimeout: 10s
    readTimeout: 10s
    keyPrefix: bmw
    standalone:
      host: 127.0.0.1
      port: 6379
      password: "123456"
    sentinel:
      masterName: ""
      address:
        - 127.0.0.1
      password: ""
  dependentRedis:
    mode: standalone
    db: 0
    dialTimeout: 10s
    readTimeout: 10s
    standalone:
      host: 127.0.0.1
      port: 6379
      password: "123456"
    sentinel:
      masterName: ""
      address:
        - 127.0.0.1
      password: ""
  mysql:
    debug: false
    host: 127.0.0.1
    port: 3306
    user: root
    password: "123456"
    dbName: your-dbName
    charset: utf8
    maxIdleConnections: 10
    maxOpenConnections: 100
  consul:
    pathPrefix: your_consulPathPrefix
    srvName: bmw
    address: "127.0.0.1:8500"
    port: 8500
    addr: "http://127.0.0.1:8500"
    tag:
      - bmw
    ttl: ""
  es:
    esRetainInvalidAlias: false

# ================================ 日志配置  ===================================
log:
  enableStdout: true
  level: "info"
  path: "./bmw.log"
  maxSize: 200
  maxAge: 1
  maxBackups: 5

# ================================ 密钥配置  ===================================
aes:
  # 全局AES加密KEY
  key: ""
  # 用于bk.data.token AES加密的salt值
  bkdataToken: "bk"
  # 用于bk.data.token AES加密的IV值
  bkdataAESIv: "bkbkbkbkbkbkbkbk"
  # 用于bk.data.token AES加密的key值
  bkdataAESKey: ""

# ================================ worker配置  ===================================
worker:
  concurrency: 0
  queues:
    - default
  healthCheck:
    interval: 3s
    duration: 5s
  daemonTask:
    maintainer:
      interval: 5s
      tolerateCount: 60
      tolerateInterval: 10s
      intolerantFactor: 2

# ================================ 任务配置  ===================================
taskConfig:
  # common: 任务通用配置
  common:
    queues:
      bigResource: big-resource
    goroutineLimit:
      your_taskName: 10
    bkapi:
      enabled: false
      host: 127.0.0.1
      stage: stag
      appCode: appCode
      appSecret: appSecret
      bcsApiMicroGwUrl: ""
      bcsApiGatewayToken: ""
      bcsApiGatewayBaseUrl: ""
      nodemanApiBaseUrl: ""
      bkdataApiBaseUrl: ""
      bkssmUrl: ""
      bcsCcApiUrl: ""
  # alarm: 告警任务配置
  alarm:
    cmdb_api_rate_limit:
      qps: 100
      burst: 100
      timeout: 10
  # metadata: metadata任务配置
  metadata:
    global:
      customReportDefaultProxyIp: []
      ipv6SupportBizList: []
      hostDisableMonitorStates: ["备用机", "测试中", "故障中"]
    pingserver:
      enablePingAlarm: true
      enableDirectAreaPingCollect: true
      dataid: 1100005
    metricDimension:
      metricKeyPrefix: bkmonitor:metrics_
      metricDimensionKeyPrefix: bkmonitor:metric_dimensions_
      maxMetricsFetchStep: 500
      timeSeriesMetricExpiredSeconds: 2592000
    bcs:
      enableBcsGray: false
      clusterBkEnvLabel: ""
      kafkaStorageClusterId: 0
      influxdb_default_proxy_cluster_name_for_k8s: "default"
      custom_event_storage_cluster_id: 0
    bkdata:
      defaultBizId: 0
      projectId: 1
      realtimeNodeWaitTime: 10
      dataExpiresDays: 30
      kafkaBrokerUrl: ""
      bkBizId: 2
      rawTableSuffix: "raw"
      CMDBFullTableSuffix: "full"
      CMDBSplitTableSuffix: "cmdb"
      druidStorageClusterName: ""
      mysqlStorageClusterName: "jungle_alert"
      mysqlStorageClusterType: "mysql_storage"
      flowClusterGroup: "default_inland"
      projectMaintainer: "admin"
      isAllowAllCmdbLevel: false
  # apmPreCalculate: apm预计算配置
  apmPreCalculate:
    notifier:
      chanBufferSize: 100000
    window:
      maxSize: 10000
      expireInterval: 1m
      maxDuration: 5m
      expireIntervalIncrement: 60
      noDataMaxDuration: 2m
      distributive:
        subSize: 10
        watchExpireInterval: 100ms
        concurrentExpirationMaximum: 100000
    processor:
      enabledTraceInfoCache: 0
    storage:
      saveRequestBufferSize: 100000
      workerCount: 10
      saveHoldMaxCount: 1000
      saveHoldMaxDuration: 500ms
      bloom:
        fpRate: 0.01
        normal:
          autoClean: 1d
        normalOverlap:
          resetDuration: 2h
        layersBloom:
          layers: 5
        decreaseBloom:
          cap: 100000000
          layers: 10
          divisor: 2
    metrics:
      report:
        semaphoreReportInterval: 5s
        prometheus:
          url: ""
          headers: {}
      profile:
        enabled: false
        host: http://127.0.0.1:14040
        appIdx: appIdx-1

# ================================ 任务调度器配置  ===================================
scheduler:
  watcher:
    chanSize: 10
  daemonTask:
    numerator:
      interval: 60s
    watcher:
      workerWatchInterval: 1s
      taskWatchInterval: 1s
