## annotations features 配置规范

### keyForwardLocalhost

用于检查采集端点是否需要重定向到 localhost。

例如：
将采集 IP 为 127.123.12.1 -> localhost

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    forwardLocalhost: "true" # 声明配置
...
```

### normalizeMetricName

用于检查是否需要标准化指标名。标准化指标名：将非数字字母转化为下划线，也就是将非 `[a-zA-Z0-9_]` 的字符全部替换成 `_` 。

此配置可以使得我们观察指标更加规范清晰。

例如：
```text
kube.deployment.replicas -> kube_deployment_replicas
kube:statefulset:replicas -> kube_statefulet_replicas
...
```

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    normalizeMetricName: "true" # 声明配置
...
```

### antiAffinity

检查调度时是否需要反节点亲和。

反亲和性：对于有相同标签的 Pod，不希望把他们调度到相同的 node 下。
```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    antiAffinity: "true" # 声明配置
...
```

### relabelRule

通知采集器是否启用 workload、labeljoin 等特性。

* v1/workload：补充 pod workload 信息。
  ```shell
  # 如 /workload/node/127.0.6.23
  $ kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/workload/node/127.0.6.1 | jq .

  [
    {
      "sourceLabels": [
        "namespace",
        "pod_name"
      ],
      "separator": ";",
      "regex": "kube-system;audit-prepare-127.0.6.1",
      "targetLabel": "workload_kind",
      "replacement": "Pod",
      "action": "replace",
      "nodeName": "127.0.6.1"
    },
    {
      "sourceLabels": [
        "namespace",
        "pod_name"
      ],
      "separator": ";",
      "regex": "kube-system;audit-prepare-127.0.6.1",
      "targetLabel": "workload_name",
      "replacement": "audit-prepare-127.0.6.1",
      "action": "replace",
      "nodeName": "127.0.6.1"
    }
  ]
  ```
  采集器根据 URL 返回的 relabels 配置进行 actions 处理，即补充上对应的 workload_name/workload_kind 信息。

* v2/workload：在 v1 版本的基础上，若存在 pod_name，查询参数添加上 pod_name。
  ```shell
  # 如 /workload/node/worker1?podName=pod1
  $ kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/workload/node/127.2.3.1?podName=pod1
  ```

* v1/labeljoin：**与 LabelJoinMatcher 搭配使用**，用于下发 kind、annotations、labels 等配置。
  ```shell
  $ kubectl exec -it -n bkmonitor-operator bkm-operator-79486746f5-n6ztd -- curl http://localhost:8080/workload/node/127.2.3.1?annotations=annotations1&kind=Pod&labels=label1&podName=pod1&rules=labeljoin
  ```

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    relabelRule: "v1/workload,v2/workload,v1/node" # 声明配置
...
```

### labelJoinMatcher

在这里 `relabelRule: v1/labeljoin` 必须和 labelJoinMatcher 成对出现。

格式如下：
labelJoinMatcher: `{Kind}://annotation:{value1},annotation:{value2},label:{value3},label:{value4}`
```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    relabelRule: "v1/labeljoin" # 声明配置
    labelJoinMatcher: "Pod://annotation:biz_service,label:deploy_zone" # 声明配置
...
```
使用上面的例子，生成的指标会带上 `annotation_` 或者 `label_` 前缀，并以匹配到的 label value 作为其值。如 `container_cpu_usage_seconds_total{annotation_biz_service="blueking",label_deploy_zone="gz"}`。

### relabelIndex

指定的 serviceMonitor/porMonitor 中 Spec 中的 Endpoints 中的索引。Endpoints 是一个列表，relabelIndex 用于指定其中的一个端点。

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    isSystem: "true"
    meta.helm.sh/release-name: bkmonitor-operator
    meta.helm.sh/release-namespace: bkmonitor-operator
    relabelIndex: "1"
    relabelRule: v1/workload
  labels:
    release: bkmonitor-operator
  name: bkmonitor-operator-bkmonit-kubelet
  namespace: bkmonitor-operator
spec:
  endpoints:
  - bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
    port: https-metrics
    relabelings:
    - action: replace
      sourceLabels:
      - __metrics_path__
      targetLabel: metrics_path
    scheme: https
  - bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
    path: /metrics/cadvisor
    port: https-metrics
    relabelings:
    - action: replace
      sourceLabels:
      - __metrics_path__
      targetLabel: metrics_path
    scheme: https
    tlsConfig:
      caFile: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
      insecureSkipVerify: true
  - bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
    path: /metrics/probes
    port: https-metrics
    relabelings:
    - action: replace
      sourceLabels:
      - __metrics_path__
      targetLabel: metrics_path
    scheme: https
  jobLabel: k8s-app
  namespaceSelector:
    matchNames:
    - bkmonitor-operator
  selector:
    matchLabels:
      app.kubernetes.io/managed-by: bkmonitor-operator
      k8s-app: kubelet
```

如下配置中 relabelIndex 为 0，就是具体指定上述 serviceMonitor Endpoints 列表中的第一个 Endpoint，即这部分。

```yaml
  - bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
    port: https-metrics
    relabelings:
    - action: replace
      sourceLabels:
      - __metrics_path__
      targetLabel: metrics_path
    scheme: https
```

配置如下：

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    relabelIndex: "0" # 声明配置
...
```

### monitorMatchSelector

monitorMatchSelector 监控匹配的选择器，用于白名单过滤。

用来查看 targetGroup 中的 target 和 label 有没有能够匹配上白名单中的配置。

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    monitorMatchSelector: "{key1}={value1},{key2}={value2}" # 声明配置
...
```

### monitorDropSelector

monitorDropSelector 监控跳过的选择器，用于黑名单过滤。

用于过滤掉 targetGroup 中的 target 和 label 被 BAN 的配置。

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    monitorDropSelector: "{key1}={value1},{key2}={value2}" # 声明配置
...
```
