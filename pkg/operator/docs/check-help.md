# 故障定位 API 帮助文档

## 故障排查API以及用法
* GET /check: 故障排查接口，支持 `monitor` 关键字查询参数

    ```shell
    $ kubectl exec -it -n bkmonitor-operator  bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check?monitor=blueking
    ```
  在进行故障排查之前，我们可以先使用 /check 用于来查询相关信息看一下是否能匹配上。例如：检查版本、dataIds 数量以及信息、集群信息、operator 监听 Endpoints 的数量等等。看看能否和预期对上。
  发现与预期不一致，说明没有监控到相关信息。大概率是配置问题，可能是配置格式错误等等。例如下面是关于信息的检查
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


  在检查信息时，我们可以重点观察上面的 `check monitor resources`。上面的例子中使用了关键字 `blueking`  进行了过滤。


  * **检查监控资源**
        当想知道每个 serviceMonitor/podMonitor 监控的具体信息可以使用这个接口，serviceMonitor/podMonitor 会配置抓取若干个端口的信息，例如：path、port等信息。而一个 serviceMonitor 会监控到若干 service，每个服务的 Endpoints 是一系列的 ip + 端口。
    
      所以想知道自己配置的 serviceMonitor/podMonitor 是否是按照自己预期，去抓取对应服务暴露的 `target URL`。可以使用该接口进行判断
  
  * **生成的 bkmonitorbeat 采集配置文件**
      也就是检查活跃的采集任务情况。
      `child_config` 是由蓝鲸监控这边自定义的采集器识别的采集配置文件。当我们想要知道采集任务位于哪些 serviceMonitor/podMonitor 上、采集任务被分配到了哪个 node 上、对应哪个 dataId（自定义 or 内置 or 事件）我们就可以查看这个信息。

* GET /check/dataid: 检查为业务集群 dataid 注入信息。

  用户在`BCS管理平台`上配置集群相关信息，蓝鲸监控平台会为`BCS管理平台`上业务方的集群注入三种 dataId，用于标识（用户不感知这三种dataId）。

      $ kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check/dataid | jq .


* GET /check/scrape: 检查采集任务指标数量

  这个接口是用于查询某个 serviceMonitor/podMonitor 采集指标的数量。
  比如：业务方想要让蓝鲸监控这边能监控到他们自定义的一些指标，他需要配置自己的 serviceMonitor/podMonitor 来告诉蓝鲸监控，监控应该从它们的 port、path、schema 等相关的信息中进行监控。
  一般这时候，业务方想看看自己的 serviceMonitor/podMonitor 是否配置成功了，是否监控到指标了，就可以使用这个接口进行故障排查


      kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check/scrape | jq .
    
    ```json
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


* GET /check/scrape/{namespace}: 检查某个指定 namespace 指标文本并返回

  该接口会向指定的 namespace 下面的各个 serviceMonitor/podMonitor 的 Endpoints 获取指标文本信息。

  **例如**：我需要查询某一个指定 serviceMonitor/podMonitor 的的指标信息。我们可以使用如下命令，获取指标文本信息。为了精准定位到指标，我们也可以根据指标名或者 serviceMonitorNam/podMonitorName 等相关信息进行过滤

      kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check/scrape/bkmonitor-operator | grep bkmonitor-operator-operator


  ```text
  pod_with_service_relation{namespace="bkmonitor-operator",service="bkmonitor-operator-operator",pod="bkm-operator-79486746f5-n6ztd"} 1
  kube_endpoint_info{namespace="bkmonitor-operator",endpoint="bkmonitor-operator-operator"} 1
  kube_endpoint_created{namespace="bkmonitor-operator",endpoint="bkmonitor-operator-operator"} 1.689763066e+09
  kube_endpoint_labels{namespace="bkmonitor-operator",endpoint="bkmonitor-operator-operator",label_app_kubernetes_io_bk_component="bkmonitor-operator",label_app_kubernetes_io_instance="bkmonitor-operator",label_app_kubernetes_io_managed_by="Helm",label_app_kubernetes_io_name="bkmonitor-operator",label_helm_sh_chart="bkmonitor-operator-3.6.0"} 1
  kube_endpoint_address_available{namespace="bkmonitor-operator",endpoint="bkmonitor-operator-operator"} 1
  kube_endpoint_address_not_ready{namespace="bkmonitor-operator",endpoint="bkmonitor-operator-operator"} 0
  kube_service_info{namespace="bkmonitor-operator",service="bkmonitor-operator-operator",cluster_ip="172.17.253.224",external_name="",load_balancer_ip=""} 1
  kube_service_created{namespace="bkmonitor-operator",service="bkmonitor-operator-operator"} 1.689763066e+09
  kube_service_spec_type{namespace="bkmonitor-operator",service="bkmonitor-operator-operator",type="ClusterIP"} 1
  kube_service_labels{namespace="bkmonitor-operator",service="bkmonitor-operator-operator",label_app_kubernetes_io_bk_component="bkmonitor-operator",label_app_kubernetes_io_instance="bkmonitor-operator",label_app_kubernetes_io_managed_by="Helm",label_app_kubernetes_io_name="bkmonitor-operator",label_helm_sh_chart="bkmonitor-operator-3.6.0"} 1
  ```


* GET /check/scrape/{namespace}/{monitor}: 检查某个 namespace 下的 monitor 指标文本并返回。 同上面，只需要加入 monitor(即 serviceMonitorName/podMonitorName)信息即可

* GET /check/namespace: 检查黑白名单配置

  
      kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check/namespace | jq .
  
  
  ```json
  {
    "deny_namespaces": null,
    "allow_namespaces": null
  }
  ```

* GET /check/active_discover: 检查活跃的 discover 情况。

  discover 是各个 serviceMonitor/podMonitor 的 Spec 信息的监听器。一个 serviceMonitor/podMonitor 的 Endpoints 的 Spec 可能配置了若干个采集端口。这些端口由Index来标识。

  当业务方需要检查自己配置的 serviceMonitor/podMonitor 抓取端口是否配置成功，可以使用这个接口来进行判断。若原本只配置了2个抓取端口，现在需要新增(或减少)一个，但是 Index 没有变化，说明未配置成功。


      kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check/active_discover | jq .
    
  ```json
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

* GET /check/active_shared_discovery: 检查活跃的 shared_discovery 情况

  在 prometheus 的设计里，每个 discovery 有着自己独立的 apiserver 长链接，消费来自 k8s 的事件。

  为了避免每一个 serviceMonitor/podMonitor 都建立一次长链接，提出了一个 shared_discovery 的概念。也就是在同一个 namespace 下面的 serviceMonitor/podMonitor 均使用同一个 discovery。

  该接口用于展示存在哪些活跃的 shared_discovery。


      kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/check/active_shared_discovery | jq .
    
    
  ```json
  [
    {
      "role": "endpoints",
      "namespaces": [
        "bkbase"
      ]
    },
    {
      "role": "endpoints",
      "namespaces": [
        "kube-system"
      ]
    },
    {
      "role": "endpoints",
      "namespaces": [
        ""
      ]
    },
    {
      "role": "endpoints",
      "namespaces": [
        "bkmonitor-operator"
      ]
    }
  ]
  ```


    

    