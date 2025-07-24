# API 文档

### 自监控指标

* GET /metrics: prometheus 自监控指标上报

### 管理端

* POST /-/logger: 运行时动态调整 logger level

    ```shell
    $ curl -XPOST -d 'level=debug' http://locahost:8080/-/logger 
    ```

* POST /-/dispatch: 运行时重新触发任务分发

### 版本信息

* GET /version: 查看 operator 版本信息

### 集群信息

* GET /cluster_info: 查看集群信息

### 性能分析

* GET /debug/pprof/snapshot: 下载 profiles 快照
    ```shell
    $ url http://localhost:8080/debug/pprof/snapshot?debug=1&seconds=15&profiles=heap,cpu,goroutine -o profiles.tar.gz
    ```
* GET /debug/pprof/cmdline: 返回 cmdline 执行命令
* GET /debug/pprof/profile: profile 采集
* GET /debug/pprof/symbol: symbol 采集
* GET /debug/pprof/trace: trace 采集
* GET /debug/pprof/{other}: 其他 profile 项采集

### 故障分析

请参考 [check-help](./check-help.md) 文档。
