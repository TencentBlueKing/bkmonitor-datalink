# 自定义资源 dataid 介绍

## 自定义资源

> 在介绍 `dataid` 之前，先来了解一下自定义资源

在 k8s 中除了内置资源 Pod、ReplicaSet、Deployment 等等之外，还支持自定义资源（CR），通常每一个自定义资源都需要有一个自定义资源定义（CRD）。有了 CRD 就可以自由地增加各种内置资源平级的资源。

先来看一下 k8s 中的资源
```shell
$ kubectl api-resources -o wide
NAME                              SHORTNAMES   APIVERSION                                  NAMESPACED   KIND                             VERBS                                                        CATEGORIES
bindings                                       v1                                          true         Binding                          create
componentstatuses                 cs           v1                                          false        ComponentStatus                  get,list
configmaps                         cm           v1                                          true         ConfigMap                         create,delete,deletecollection,get,list,patch,update,watch
endpoints                         ep           v1                                          true         Endpoints                        create,delete,deletecollection,get,list,patch,update,watch
events                            ev           v1                                          true         Event                            create,delete,deletecollection,get,list,patch,update,watch
limitranges                       limits       v1                                          true         LimitRange                       create,delete,deletecollection,get,list,patch,update,watch
namespaces                        ns           v1                                          false        Namespace                        create,delete,get,list,patch,update,watch
nodes                             no           v1                                          false        Node                             create,delete,deletecollection,get,list,patch,update,watch
persistentvolumeclaims            pvc          v1                                          true         PersistentVolumeClaim            create,delete,deletecollection,get,list,patch,update,watch
persistentvolumes                 pv           v1                                          false        PersistentVolume                 create,delete,deletecollection,get,list,patch,update,watch
pods                              po           v1                                          true         Pod                              create,delete,deletecollection,get,list,patch,update,watch   all
podtemplates                                   v1                                          true         PodTemplate                      create,delete,deletecollection,get,list,patch,update,watch
replicationcontrollers            rc           v1                                          true         ReplicationController            create,delete,deletecollection,get,list,patch,update,watch   all
resourcequotas                    quota        v1                                          true         ResourceQuota                    create,delete,deletecollection,get,list,patch,update,watch
secrets                                        v1                                          true         Secret                           create,delete,deletecollection,get,list,patch,update,watch
serviceaccounts                   sa           v1                                          true         ServiceAccount                   create,delete,deletecollection,get,list,patch,update,watch
services                          svc          v1                                          true         Service                          create,delete,get,list,patch,update,watch                    all
dataids                           bkd          monitoring.logging/v1beta1           false        DataID                           delete,deletecollection,get,list,patch,create,update,watch
...
```

可以看见 APIVERSION 为 `v1` 的均为 k8s 中的内置资源；而 dataids 是自定义资源，版本号为 `monitoring.bk.tencent.com/v1beta1` 。

## dataid 介绍

在蓝鲸监控中 dataid 用于对数据来源进行管理，这是管理端的特性，用户不感知。在容器环境中，dataid 是一种 CR。在用户集群接入蓝鲸监控之后，首先监控管理后端会对集群注入 3 个 dataid。分别是：
* custommetricdataid：自定义指标 dataid。
* k8seventdataid：内置事件 dataid。
* k8smetricdataid：内置指标 dataid。

```shell
$ kubectl get dataids -owide
NAME                 AGE
custommetricdataid   399d
k8seventdataid       399d
k8smetricdataid      399d
```

```shell
# 这里使用 `custommetricdataid` 这个 dataid 来举例子，具体看看 dataid 详细信息。
$ kubectl get dataids custommetricdataid -oyaml
apiVersion: monitoring.logging/v1beta1
kind: DataID
metadata:
  creationTimestamp: "2023-07-19T10:40:03Z"
  generation: 1
  labels:
    isCommon: "true"
    isSystem: "false"
    usage: metric
  name: custommetricdataid
  resourceVersion: "5719372880"
  selfLink: /apis/monitoring.logging/v1beta1/dataids/custommetricdataid
  uid: 2f2f4b12-e63f-49d8-83e2-dd0d79f9fa16
spec:
  dataID: 1572865
  dimensionReplace: {}
  labels:
    bcs_cluster_id: BCS-K8S-00000
    bk_biz_id: "2"
  metricReplace: {}
```

从上面的配置中，可以看见，`lebels` 中有：
- isCommon：是否为集群通用 dataid。
- isSystem：是否为平台内置 dataid。
- usage：数据类型标识，目前支持 metrics/event。

`Spec` 是该资源的详细规范
- dataID：具体的 dataid 数值。
- labels：
  - bcs_cluster_id：bcs 集群 id。
  - bk_biz_id：bcs 集群关联的业务 id。
