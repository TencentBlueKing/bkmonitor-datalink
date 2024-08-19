# 故障定位 API 帮助文档
在了解故障定位 API 之前，我们可以先来查看一下所有的路由信息。
```shell
$ kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080

# Admin Routes
--------------
* POST /-/logger
* POST /-/reload
* POST /-/dispatch

# Metadata Routes
-----------------
* GET /metrics
* GET /version
* GET /cluster_info
* GET /workload
* GET /workload/node/{node}
* GET /relation/metrics
* GET /rule/metrics

# Check Routes
--------------
* GET /check?monitor=${monitor}&scrape=true|false
* GET /check/dataid
* GET /check/scrape
* GET /check/scrape/{namespace}
* GET /check/scrape/{namespace}/{monitor}
* GET /check/namespace
* GET /check/monitor_blacklist
* GET /check/active_discover
* GET /check/active_child_config
* GET /check/active_shared_discovery
* GET /check/monitor_resource

# Profile Routes
----------------
* GET /debug/pprof/snapshot
* GET /debug/pprof/cmdline
* GET /debug/pprof/profile
* GET /debug/pprof/symbol
* GET /debug/pprof/trace
* GET /debug/pprof/{other}
```
可以看见，有 Admin、Metadata、Check、Profile 相关的路由。 Admin 提供了重新加载配置等接口；MetaData 提供了查看自定义指标、版本、工作负载等接口；Profile 提供了性能分析等接口。

本篇文档，我们重点分析 Check Routes。

## 故障排查 API 以及用法

### GET /check
故障排查接口，支持 `monitor` 关键字查询参数。

```shell
$ kubectl exec -it -n bkmonitor-operator  bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check?monitor=blueking
```
在进行故障排查之前，我们可以先使用 /check 用于来查询相关信息看一下是否能匹配上。例如：检查版本、dataIds 数量以及信息、集群信息、operator 监听 Endpoints 的数量等等。看看能否和预期对上。

发现与预期不一致，说明没有监控到相关信息。大概率是配置问题，可能是配置格式错误等等。例如下面是关于信息的检查。
```text
[√] check kubernetes version
- Description: kubernetes 集群版本为 v1.20.6-tke.34

[√] check operator version
- Description: bkmonitor-operator 版本信息
{
  "version": "3.6.2187",
  "git_hash": "0b10d1a9be30978b30c4ccd1e089dea2922e5276",
  "build_time": "2024-08-05_06:22:37上午"
}

[√] check dataids
- Description: 期待 dataids 数量应大于等于 3 个，目前发现 3 个
[
  {
    "dataid": 1572864,
    "name": "k8smetricdataid",
    "labels": {
      "isCommon": "true",
      "isSystem": "true",
      "usage": "metric"
    }
  },
  {
    "dataid": 1572865,
    "name": "custommetricdataid",
    "labels": {
      "isCommon": "true",
      "isSystem": "false",
      "usage": "metric"
    }
  },
  {
    "dataid": 1572866,
    "name": "k8seventdataid",
    "labels": {
      "isCommon": "true",
      "isSystem": "true",
      "usage": "event"
    }
  }
]

[√] check cluster information
- Description: 集群信息
{
  "bcs_cluster_id": "BCS-K8S-00000",
  "bizid": "2",
  "bk_env": ""
}

[√] check endpoint
- Description: operator 监听 monitor endpoints 数量，共 138 个
{
  "ServiceMonitor/aiops-default/kg-dgraph-kg-dgraph-alpha/0": 1,
  "ServiceMonitor/aiops-default/kg-dgraph-kg-dgraph-zero/0": 1,
  "ServiceMonitor/bkbase/bkbase-dgraph-bkbase-dgr-alpha/0": 3,
  "ServiceMonitor/bkbase/bkbase-dgraph-bkbase-dgr-zero/0": 3,
  "ServiceMonitor/bkbase/bkbase-jobnavischeduler/0": 1,
  "ServiceMonitor/bkbase/bkbase-pulsar-broker-http/0": 1,
  "ServiceMonitor/bkbase/bkbase-pulsar-broker-pulsar/0": 1,
  "ServiceMonitor/bkbase/bkbase-queryengine-api-servicemonitor/0": 1,
  "ServiceMonitor/bkbase/bkbase-querymanager-api-servicemonitor/0": 1,
  "ServiceMonitor/bkmonitor-operator/bkmonitor-operator-bkmonit-coredns/0": 2,
  "ServiceMonitor/bkmonitor-operator/bkmonitor-operator-bkmonit-kube-proxy/0": 24,
  "ServiceMonitor/bkmonitor-operator/bkmonitor-operator-bkmonit-kube-state-metrics/0": 1,
  "ServiceMonitor/bkmonitor-operator/bkmonitor-operator-bkmonit-kubelet/0": 24,
  "ServiceMonitor/bkmonitor-operator/bkmonitor-operator-bkmonit-kubelet/1": 24,
  "ServiceMonitor/bkmonitor-operator/bkmonitor-operator-bkmonit-kubelet/2": 24,
  "ServiceMonitor/bkmonitor-operator/bkmonitor-operator-bkmonit-node-exporter/0": 24,
  "ServiceMonitor/bkmonitor-operator/bkmonitor-operator-operator/0": 1,
  "ServiceMonitor/blueking/bk-apigateway-apigateway/0": 1
},
[√] check nodes
- Description: 获取集群节点列表成功，节点数量为 24，最近一次更新时间 2024-08-08T07:17:21Z

[√] check kubernetes secrets handled
- Description: 操作 secrets 资源未出现错误，最近一次操作时间 2024-08-19T02:17:27Z

[√] check monitor resources
- Description: 通过 'blueking' 关键字匹配到以下监控资源。
* 监测到 ServiceMonitor/PodMonitor/Probe 资源以及对应的采集目标，请检查资源数量是否一致
[
  {
    "kind": "ServiceMonitor",
    "namespace": "blueking",
    "name": "bk-apigateway-apigateway",
    "index": 0,
    "count": 1,
    "location": [
      {
        "address": "172.17.7.74:6008",
        "node": "10.0.7.53",
        "target": "http://172.17.7.74:6008/metrics",
        "dataid": 1572865
      }
    ]
  }
]
* 生成的 bkmonitorbeat 采集配置文件
[
  {
    "service": "ServiceMonitor/blueking/bk-apigateway-apigateway/0",
    "dataid": 1572865,
    "filename": "10-0-7-53-172-17-7-74-6008-metrics-15502861147355311619-0",
    "node": "10.0.7.53"
  }
]
...
```
在检查信息时，我们可以重点观察上面的 `check monitor resources`。上面的例子中使用了关键字 `blueking` 进行了过滤。

> 关键字匹配均为 Contains 模糊匹配，非精确匹配

其中关键字匹配规则，对于`监测到 ServiceMonitor/PodMonitor/Probe 资源以及对应的采集目标`是以 serviceMonitor/podMonitor 名称、serviceMonitor/podMonitor 的 namespace 进行匹配的；对于 `生成的 bkmonitorbeat 的采集配置文件` 是以 MonitorMeta 的 Kind/Namespace/Name/Index 的格式来进行匹配的。MonitorMeta 描述了监控类型的元数据信息，目前类型有 serviceMonitor, podMonitor, probe。

当我们想根据 serviceMonitor/podMonitor 名称或者 namespace 等等相关信息做过滤，我们就可以使用关键字。

下面是关于 `check monitor resources` 返回数据的说明，下面的两部分按照返回的次序来说明：

* **监测到 ServiceMonitor/PodMonitor/Probe 资源以及对应的采集目标**
  
  当想知道每个 serviceMonitor/podMonitor 监控的具体信息可以使用这个接口，serviceMonitor/podMonitor 会配置抓取若干个端口的信息，例如：path、port 等信息。而一个 serviceMonitor 会监控到若干 service，每个服务的 Endpoints 是一系列的 ip + 端口。

* **生成的 bkmonitorbeat 采集配置文件**

  也就是检查活跃的采集任务情况。

  `child_config` 是由蓝鲸监控这边自定义的采集器识别的采集配置文件。也就是 bkmonitorbeat 采集任务的配置文件。当我们想要知道采集任务位于哪些 serviceMonitor/podMonitor 上、采集任务被分配到了哪个 node 上、对应哪个 dataId（自定义 or 内置 or 事件）我们就可以查看这个信息。

> 这里我们简单举个例子，介绍一下 serviceMonitor/PodMonitor 的匹配规则

* 先查询 namespace下的所有 serviceMonitors
  ```shell
  $ kubectl get servicemonitors.monitoring.coreos.com -n bkmonitor-operator
  NAME                                                 AGE
  bkmonitor-operator-bkmonit-apiserver                 397d
  bkmonitor-operator-bkmonit-coredns                   397d
  bkmonitor-operator-bkmonit-kube-controller-manager   397d
  bkmonitor-operator-bkmonit-kube-proxy                397d
  bkmonitor-operator-bkmonit-kube-state-metrics        397d
  bkmonitor-operator-bkmonit-kubelet                   397d
  bkmonitor-operator-bkmonit-node-exporter             397d
  bkmonitor-operator-operator                          208d
  ```
* 查看其中某一个 serviceMonitor 配置信息，这里以 bkmonitor-operator-bkmonit-kubelet 举例子
  ```shell
  $ kubectl get servicemonitors.monitoring.coreos.com -n bkmonitor-operator  bkmonitor-operator-bkmonit-kubelet -oyaml
  apiVersion: monitoring.coreos.com/v1
  kind: ServiceMonitor
  name: bkmonitor-operator-bkmonit-kubelet
  namespace: bkmonitor-operator
  spec:
    endpoints:
    ...
    ...
    jobLabel: k8s-app
    namespaceSelector:
      matchNames:
      - bkmonitor-operator
    selector:
      matchLabels:
        app.kubernetes.io/managed-by: bkmonitor-operator
        k8s-app: kubelet
  ```
  ServiceMonitor 的 selector 和 namespaceSelector 字段用于选择需要监控的 Kubernetes 服务。这些字段决定了，应该要抓取哪些服务。配置可见 如上配置文件的最后几行。
* 根据查询 serviceMonitor 监控的 service
  ```shell
  $ kubectl get service -n bkmonitor-operator -l app.kubernetes.io/managed-by=bkmonitor-operator,k8s-app=kubelet
  NAME                         TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)                        AGE
  bkmonitor-operator-kubelet   ClusterIP   None         <none>        10250/TCP,10255/TCP,4194/TCP   397d
  ```
  可以看见 bkmonitor-operator-bkmonit-kubelet 监控到的 service 是 bkmonitor-operator-kubelet

* 查询该 service 对应的的 Endpoints
  ```shell
  $ kubectl describe service -n bkmonitor-operator bkmonitor-operator-kubelet
  Name:              bkmonitor-operator-kubelet
  Namespace:         bkmonitor-operator
  Labels:            app.kubernetes.io/managed-by=bkmonitor-operator
                    app.kubernetes.io/name=kubelet
                    k8s-app=kubelet
  Annotations:       <none>
  Selector:          <none>
  Type:              ClusterIP
  IP Families:       <none>
  IP:                None
  IPs:               None
  Port:              https-metrics  10250/TCP
  TargetPort:        10250/TCP
  Endpoints:         10.0.3.8:10250,10.0.4.6:10250,10.0.4.8:10250 + 21 more...
  Port:              http-metrics  10255/TCP
  TargetPort:        10255/TCP
  Endpoints:         10.0.3.8:10255,10.0.4.6:10255,10.0.4.8:10255 + 21 more...
  Port:              cadvisor  4194/TCP
  TargetPort:        4194/TCP
  Endpoints:         10.0.3.8:4194,10.0.4.6:4194,10.0.4.8:4194 + 21 more...
  Session Affinity:  None
  ```
  这里的 Endpoints 也就是例子中 bkmonitor-operator-bkmonit-kubelet 这个 serviceMonitor 监控到的 Ip + Port 列表。

### GET /check/scrape
检查采集任务指标数量。

这个接口是用于查询某个 serviceMonitor/podMonitor 采集指标的数量。

**例如**：
用户想要让蓝鲸监控这边能监控到他们自定义的一些指标，则需要配置自己的 serviceMonitor/podMonitor 来告诉蓝鲸监控，监控会从配置的 serviceMonitor/podMonitor 中找到需要抓取的 port、path、schema 等相关的信息中进行监控。
一般这时候，用户想看看自己配置的 serviceMonitor/podMonitor 的指标量级，就可以使用这个接口。

```shell
$ kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check/scrape | jq .
{
  "monitor_count": 16,
  "lines_total": 557443,
  "errors_total": 1,
  "stats": [
    {
      "monitor_name": "bkmonitor-operator-bkmonit-kubelet",
      "namespace": "bkmonitor-operator",
      "lines": 245004,
      "errors": 0
    },
    {
      "monitor_name": "bkmonitor-operator-bkmonit-kube-state-metrics",
      "namespace": "bkmonitor-operator",
      "lines": 140573,
      "errors": 0
    },
    {
      "monitor_name": "bkbase-pulsar-broker-http",
      "namespace": "bkbase",
      "lines": 85742,
      "errors": 1
    }
  ]
}
```

### GET /check/scrape/{namespace}
检查某个指定 namespace 指标文本并返回。

该接口会向指定的 namespace 下面的各个 serviceMonitor/podMonitor 的 Endpoints 获取指标文本信息。

**例如**：
我需要查询某一个具体的指标名对应的指标文本信息，这里我们通过 kube_endpoint_info 这个指标名进行过滤来展示。

```shell
$ kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check/scrape/bkmonitor-operator | grep kube_endpoint_info
kube_endpoint_info{namespace="bkapp-cc-portal-prod",endpoint="cc-portal--quality"} 1
kube_endpoint_info{namespace="bkapp-cc-portal-prod",endpoint="cc-portal--auditlog"} 1
kube_endpoint_info{namespace="bk-jaeger",endpoint="jaeger-collector"} 1
kube_endpoint_info{namespace="bkapp-bkaidev-prod",endpoint="bkaidev-m-llm-gateway--web"} 1
kube_endpoint_info{namespace="blueking",endpoint="job-config-watcher"} 1
kube_endpoint_info{namespace="aiops-default",endpoint="service-9dc4ec690399daef49e0399d697dab67"} 1
kube_endpoint_info{namespace="kube-system",endpoint="bkmonitor-operator-bkmonit-kube-proxy"} 1
kube_endpoint_info{namespace="blueking",endpoint="bk-repo-bkrepo-repository"} 1
kube_endpoint_info{namespace="bkbase",endpoint="bkbase-hive-primary-service-clusterip"} 1
kube_endpoint_info{namespace="bkapp-bk0us0gsekit-prod",endpoint="bk0us0gsekit--pwatch"} 1
kube_endpoint_info{namespace="aiops-default",endpoint="service-c22c7db09943e5c505af2b21129ce030"} 1
kube_endpoint_info{namespace="bkmonitor-operator",endpoint="bkmonitor-operator-kubelet"} 1
...
```
kube_endpoint_info 指标里面的 namespace 指采集目标的 namespace。

### GET /check/scrape/{namespace}/{monitor}
检查某个 namespace 下的 monitor 指标文本并返回。
同上面，只需要加入 monitor （即 serviceMonitor/podMonitor 名称）信息即可。

### GET /check/namespace
检查黑白名单配置。
```shell
$ kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check/namespace | jq .
{
  "deny_namespaces": null,
  "allow_namespaces": null
}
```

### GET /check/active_discover
检查活跃的 discover 情况。

discover 是各个 serviceMonitor/podMonitor 的 Spec 信息的监听器。一个 serviceMonitor/podMonitor 的 Endpoints 的 Spec 可能配置了若干个采集端口。这些端口由 Index 来标识。

当用户需要检查自己配置的 serviceMonitor/podMonitor 抓取端口是否配置成功，可以使用这个接口来进行判断。若原本只配置了 2 个抓取端口，现在需要新增（或减少）一个，但是 Index 没有变化，说明未配置成功。

```shell
$ kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check/active_discover | jq .
[
  {
    "name": "bkmonitor-operator-bkmonit-kubelet",
    "kind": "ServiceMonitor",
    "namespace": "bkmonitor-operator",
    "index": 1
  },
  {
    "name": "bkbase-dgraph-bkbase-dgr-zero",
    "kind": "ServiceMonitor",
    "namespace": "bkbase",
    "index": 0
  },
  {
    "name": "kg-dgraph-kg-dgraph-alpha",
    "kind": "ServiceMonitor",
    "namespace": "aiops-default",
    "index": 0
  },
  {
    "name": "bkmonitor-operator-bkmonit-apiserver",
    "kind": "ServiceMonitor",
    "namespace": "bkmonitor-operator",
    "index": 0
  },
  {
    "name": "kg-dgraph-kg-dgraph-zero",
    "kind": "ServiceMonitor",
    "namespace": "aiops-default",
    "index": 0
  }
]
```
