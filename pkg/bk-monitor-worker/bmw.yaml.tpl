service:
  http:
    mode: release
    listen: 127.0.0.1
    port: 10213
    enabledPprof: true
broker:
  redis:
    mode: standalone
    db: 0
    dialTimeout: 10
    readTimeout: 10
    standalone:
      host: 127.0.0.1
      port: 6379
      password: "123456"
    sentinel:
      masterName: ""
      address:
        - 127.0.0.1
      password: ""
store:
  redis:
    mode: standalone
    db: 0
    dialTimeout: 10
    readTimeout: 10
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
    dialTimeout: 10
    readTimeout: 10
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
log:
  enableStdout: true
  level: "info"
  stdoutPath: "./bmw.log"
  stdoutFileMaxSize: 200
  stdoutFileMaxAge: 1
  stdoutFileMaxBackups: 5
aes:
  key: ""
test:
  store:
    mysql:
      host: 127.0.0.1
      port: 3306
      user: root
      password: 123456
      dbName: your-testDbName
worker:
  concurrency: 0
  queues:
    - default
taskConfig:
  metadata:
    metricDimension:
      metricKeyPrefix: bkmonitor:metrics_
      metricDimensionKeyPrefix: bkmonitor:metric_dimensions_
      maxMetricsFetchStep: 500
      timeSeriesMetricExpiredDays: 30
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
      traceMetaBloomCutLength: 16
      storage:
        saveRequestBufferSize: 100000
        workerCount: 10
        saveHoldMaxCount: 1000
        saveHoldMaxDuration: 500ms
        bloom:
          fpRate: 0.01
          normal:
            autoClean: 1440
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
task:
  watcher:
    chanSize: 10
  healthCheck:
    interval: 3
    duration: 5
  daemonTask:
    maintainer:
      interval: 1
      tolerateCount: 60
      tolerateInterval: 10
      intolerantFactor: 2
scheduler:
  daemonTask:
    numerator:
      interval: 60
    watcher:
      workerWatchInterval: 1
      taskWatchInterval: 1
