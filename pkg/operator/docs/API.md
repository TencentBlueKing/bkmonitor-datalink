# API 文档

### 自监控指标

* GET /metrics: prometheus 自监控指标上报

### 管理端

* POST /-/logger: 运行时动态调整 logger level

    ```shell
    $ curl -XPOST -d 'level=debug' http://locahost:8080/-/logger 
    ```

* POST /-/reload: 运行时重载 operator
* POST /-/dispatch: 运行时重新触发任务分发

### 版本信息

* GET /version: 查看 operator 版本信息

### 集群信息

* GET /cluster_info: 查看集群信息

### 工作负载

* GET /workload: 查看所有 Pod 工作负载
* GET /workload/node/{node}: 查看指定主机的 Pod 工作负载

### 故障定位

* GET /check: 故障排查接口，支持 `monitor` 关键字查询参数

    ```shell
    $ curl http://localhost:8080/check?monitor=blueking
    ```
* GET /check/dataid: 检查 dataid
* GET /check/scrape: 检查采集任务指标数量
* GET /check/scrape/{namespace}: 检查某个 namespace 指标文本并返回
* GET /check/scrape/{namespace}/{monitor}: 检查某个 namespace 下的 monitor 指标文本并返回
* GET /check/namespace: 检查黑白名单配置
* GET /check/active_discover: 检查活跃的 discover 情况
* GET /check/active_child_config: 检查活跃的采集任务情况
* GET /check/active_shared_discovery: 检查活跃的 shared_discovery 情况
* GET /check/monitor_resource: 检查监控资源

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
