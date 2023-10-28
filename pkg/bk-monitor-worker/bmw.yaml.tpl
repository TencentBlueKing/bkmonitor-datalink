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
      expireInterval: 60
      maxDuration: 300
      expireIntervalIncrement: 60
      noDataMaxDuration: 120
      distributive:
        subSize: 10
        watchExpireInterval: 100
        concurrentCount: 1000
        concurrentExpirationMaximum: 100000
    processor:
      enabledTraceInfoCache: 0
      traceMetaBloomCutLength: 16
      storage:
        saveRequestBufferSize: 100000
        workerCount: 10
        saveHoldMaxCount: 1000
        saveHoldMaxDuration: 500
        bloom:
          fpRate: 0.01
          autoClean: 30
    metrics:
      enabled: false
      profile:
        enabled: false
        host: http://127.0.0.1:14040
      reportHost: http://127.0.0.1:10205/v2/push/
      saveRequestChanCount:
        dataId: 1572880
        accessToken: 9270f7c6bd5042a48a4db5e0839bbfa8
      messageChanCount:
        dataId: 1572881
        accessToken: 3463ac2e115048fe89e7b33e49af25a7
      windowTraceCount:
        dataId: 1572882
        accessToken: 461c858cb8254a5ca7668f727f148b7a
      windowSpanCount:
        dataId: 1572883
        accessToken: e40dd3927f134e9a93efe7c425a837ab
      esOriginTraceCount:
        dataId: 1572886
        accessToken: 806a595c44134dd8a46149d7579d6b37
      esPreCalTraceCount:
        dataId: 1572887
        accessToken: 863b3882a9984433a48e64db22b2dfe1
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
