# ================================ Http配置  ===================================
service:
  http:
    mode: release
    listen: 127.0.0.1
    port: 10213
    enabledPprof: true

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
    es_retain_invalid_alias: false
  bbolt:
    defaultPath: "bolt.db"
    defaultBuckName: "spaceBucket"
    defaultSync: false

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
  key: ""

# ================================ 测试配置  ===================================
test:
  store:
    mysql:
      host: 127.0.0.1
      port: 3306
      user: root
      password: 123456
      dbName: your-testDbName

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
      interval: 1s
      tolerateCount: 60
      tolerateInterval: 10s
      intolerantFactor: 2

# ================================ 任务配置  ===================================
taskConfig:
  # common: 任务通用配置
  common:
    goroutineLimit:
      your_taskName: 10
    bkapi:
      enabled: false
      host: 127.0.0.1
      stage: stag
      appCode: appCode
      appSecret: appSecret
  # metadata: metadata任务配置
  metadata:
    metricDimension:
      metricKeyPrefix: bkmonitor:metrics_
      metricDimensionKeyPrefix: bkmonitor:metric_dimensions_
      maxMetricsFetchStep: 500
      timeSeriesMetricExpiredDays: 30
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
        concurrentCount: 1000
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
      timeSeries:
        enabled: false
        host: http://127.0.0.1:10205/v2/push/
        interval: 1m
        dataId: 0
        accessToken: ""
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
