## annotations features配置规范

### keyForwardLocalhost
#### 介绍 

用于检查采集端点是否需要重定向到 localhost
例如：
> 将采集 IP 为 192.183.1.3 -> localhost


```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    forwardLocalhost: "true" # 更改配置
...
```

### normalizeMetricName
#### 介绍
用于检查是否需要标准化指标名。标准化指标名：指标名称转为小写，将非数字字母转化为下划线，同时去除前后下划线。

此配置可以使得我们观察指标更加规范清晰

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
    normalizeMetricName: "true" #更改配置
...
```

### antiAffinity
#### 介绍
检查调度时是否需要反节点亲和

反亲和性：对于有相同标签的 Pod，不希望把他们调度到相同的 node 下。

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    antiAffinity: "true" #更改配置
...
```

### relabelRule
#### 介绍
为采集器添加 workload、labeljoin 信息
* v1/workload：v1版本默认
    
* v2/workload：需要保证有 pod_name 才下发 

* v1/labeljoin：**与 LabelJoinMatcher 搭配使用**，用于下发 kind、annotations、labels等配置

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    relabelRule: "v1/workload,v2/workload,v1/node" #更改配置
...
```

### labelJoinMatcher
#### 介绍
> 在这里 relabelRule: v1/labeljoin 必须和 labelJoinMatcher 成对出现。

格式如下：
labelJoinMatcher: "{Kind}://annotation:{value1},annotation:{value2},label:{value3},label:{value4}"

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    relabelRule: "v1/labeljoin" #更改配置
    labelJoinMatcher: "Pod://annotation:biz_service,label:deploy_zone" # 更改配置
...
```

使用上面的例子，我们的指标格式类似于 container_cpu_usage_seconds_total{annotation_biz_service="blueking",label_deploy_zone="gz"}

### relabelIndex
#### 介绍
也就是具体指定 serviceMonitor 中 Spec 中的 Endpoints中的索引。
Endponits是一个列表，relabelIndex用于指定其中的一个端点，用于将原标签的值，复制到新的标签中。


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

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    relabelIndex: "0" #更改配置
...
```
例如上面的配置，就是具体指定第一个Endpoint。将原标签为 `__metrics_path__` 的值复制到目标标签 `metrics_path`

### monitorMatchSelector
#### 介绍
monitorMatchSelector 监控匹配的选择器，用于白名单过滤。

用来查看 targetGroup 中的 target 和 label 有没有能够匹配上白名单中的配置。

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    monitorMatchSelector: "{key1}={value1},{key2}={value2}" #更改配置格式
...
```

### monitorDropSelector
#### 介绍
monitorDropSelector 监控跳过的选择器，用于黑名单过滤

用于过滤掉 targetGroup 中的 target 和 label 被 BAN 的配置

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  annotations:
    monitorDropSelector: "{key1}={value1},{key2}={value2}" #更改配置格式
...
```




