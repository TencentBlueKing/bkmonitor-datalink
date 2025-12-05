# CMDB 关联关系设计文档

## 📋 目录

1. [概述](#1-概述)
2. [核心概念](#2-核心概念)
3. [静态关联设计](#3-静态关联设计)
4. [动态关联设计](#4-动态关联设计)
5. [Info 扩展设计](#5-info-扩展设计)
6. [查询流程](#6-查询流程)
7. [技术实现](#7-技术实现)
8. [应用场景](#8-应用场景)

---

## 1. 概述

### 1.1 背景

在可观测性场景中，需要查询不同资源之间的关联关系。例如：
- 查询某个 Pod 运行在哪个 Node 上
- 查询某个 Node 上运行了哪些 Pod
- 查询某个 Container 属于哪个 Pod
- 查询某个 Pod 属于哪个 Deployment

这些关联关系需要支持：
- **静态关联**：资源类型之间的关联关系（如 Pod 可以关联到 Node），通过 `{resource1}_with_{resource2}_relation` 指标存储
- **动态关联**：资源实例之间的流量访问关系（如 pod-1 访问 pod-2），通过 `pod_to_pod_flow_total`、`pod_to_pod_flow_seconds`、`pod_to_pod_flow_error` 等指标存储，代表访问量、访问耗时、错误数

### 1.2 设计目标

- **统一查询接口**：提供统一的 API 查询不同资源之间的关联关系
- **支持多路径查询**：支持从源资源到目标资源的多条路径查询
- **实时关联数据**：通过时序数据存储和查询实时关联关系
- **扩展性强**：支持新增资源类型和关联关系

---

## 2. 核心概念

### 2.1 Resource（资源）

**定义**：资源是可观测系统中的实体，如 Pod、Node、Container、Deployment 等。

**示例**：
- `pod`：Kubernetes Pod
- `node`：Kubernetes Node
- `container`：容器
- `system`：系统（通过 IP 标识）
- `deployment`：Kubernetes Deployment

### 2.2 Index（关键维度）

**定义**：用于唯一标识资源实例的维度集合。

**作用**：
- 唯一标识资源实例
- 作为关联查询的过滤条件
- 作为 PromQL 查询的 `by` 子句

**示例**：
- `pod` 的 Index：`["bcs_cluster_id", "namespace", "pod"]`
- `node` 的 Index：`["bcs_cluster_id", "node"]`
- `system` 的 Index：`["bk_target_ip"]`

### 2.3 Matcher（维度映射）

**定义**：Key-Value 对，用于过滤和查询资源实例。

**示例**：
```json
{
  "bcs_cluster_id": "BCS-K8S-00000",
  "namespace": "bkmonitor-operator",
  "pod": "bkm-pod-1"
}
```

### 2.4 Relation（关联关系）

**定义**：两个资源类型之间的关联关系。

**示例**：
- `pod -> node`：Pod 可以关联到 Node
- `container -> pod`：Container 可以关联到 Pod
- `node -> system`：Node 可以关联到 System

### 2.5 Path（关联路径）

**定义**：从源资源到目标资源的关联路径，由多个 Relation 组成。

**示例**：
- `pod -> node -> system`：从 Pod 到 System 的路径
- `container -> pod -> node -> system`：从 Container 到 System 的路径

---

## 3. 静态关联设计

### 3.1 资源配置

**定义**：在 `config.go` 中静态定义资源的 Index 和 Info。

**配置结构**：
```go
type ResourceConf struct {
    Name  Resource  // 资源名称
    Index Index     // 关键维度
    Info  Index     // 扩展信息维度（可选）
}
```

**示例**：
```go
{
    Name: "pod",
    Index: cmdb.Index{
        "bcs_cluster_id",
        "namespace",
        "pod",
    },
},
{
    Name: "container",
    Index: cmdb.Index{
        "bcs_cluster_id",
        "namespace",
        "pod",
        "container",
    },
    Info: cmdb.Index{
        "version",  // 扩展信息：版本号
    },
},
{
    Name: "host",
    Index: cmdb.Index{
        "host_id",
    },
    Info: cmdb.Index{
        "version",
        "env_name",
        "env_type",
        "service_version",
        "service_type",
    },
},
```

### 3.2 关联关系配置

**定义**：在 `config.go` 中静态定义资源之间的关联关系。

**配置结构**：
```go
type RelationConf struct {
    Resources []Resource  // 两个资源类型
}
```

**示例**：
```go
{
    Resources: []cmdb.Resource{
        "node", "system",
    },
},
{
    Resources: []cmdb.Resource{
        "node", "pod",
    },
},
{
    Resources: []cmdb.Resource{
        "container", "pod",
    },
},
{
    Resources: []cmdb.Resource{
        "pod", "replicaset",
    },
},
{
    Resources: []cmdb.Resource{
        "deployment", "replicaset",
    },
},
```

### 3.3 图算法存储

**实现**：使用图算法（`graph`）存储和管理静态关联关系。

**初始化流程**：
1. 将所有资源类型作为图的顶点（Vertex）
2. 将所有关联关系作为图的边（Edge）
3. 使用 `graph.AllPathsBetween` 查找从源资源到目标资源的所有路径

**优势**：
- 支持多路径查询
- 自动找到最短路径
- 支持路径过滤（`pathResource`）

---

## 4. 动态关联设计

### 4.1 静态关联指标（拓扑关联）

**命名规则**：`{resource1}_with_{resource2}_relation`

**规则说明**：
- 资源名称按字母序排序
- 例如：`node` 和 `pod` 的关联指标为 `node_with_pod_relation`
- 例如：`container` 和 `pod` 的关联指标为 `container_with_pod_relation`

**指标结构**：
- **指标名称**：`bkmonitor:node_with_pod_relation`
- **Label**：包含两个资源的所有 Index 维度
  - `bcs_cluster_id`
  - `namespace`
  - `node`
  - `pod`
- **Value**：通常为 1（表示关联关系存在）

**存储方式**：
- 数据由外部采集服务写入
- 现阶段存储在 VictoriaMetrics（VM）中
- 后续会调研时序图数据库的方案，以解决 VM 查询的性能瓶颈和功能瓶颈

**示例**：
```
bkmonitor:node_with_pod_relation{
  bcs_cluster_id="BCS-K8S-00000",
  namespace="bkmonitor-operator",
  node="node-127-0-0-1",
  pod="bkm-pod-1"
} = 1
```

### 4.2 动态关联指标（流量关联）

**设计说明**：动态关联指标用于存储资源之间的流量访问关系，目前已有设计但代码尚未实现。

**指标类型**：
- `pod_to_pod_flow_total`：Pod 到 Pod 的访问量
- `pod_to_pod_flow_seconds`：Pod 到 Pod 的访问耗时
- `pod_to_pod_flow_error`：Pod 到 Pod 的访问错误数
- `pod_to_system_flow_total`：Pod 到 System 的访问量
- `system_to_pod_flow_total`：System 到 Pod 的访问量
- `system_to_system_flow_total`：System 到 System 的访问量
- （其他资源类型的流量指标类似）

**指标结构**：
- **指标名称**：`bkmonitor:pod_to_pod_flow_total`
- **Label**：包含源资源和目标资源的所有 Index 维度，每个维度都需要加上 `source_` 和 `target_` 前缀
  - `source_bcs_cluster_id`（源 Pod 的集群 ID）
  - `source_namespace`（源 Pod 的命名空间）
  - `source_pod`（源 Pod 名称）
  - `target_bcs_cluster_id`（目标 Pod 的集群 ID）
  - `target_namespace`（目标 Pod 的命名空间）
  - `target_pod`（目标 Pod 名称）
- **Value**：访问量（数值，非固定值）

**示例**：
```
bkmonitor:pod_to_pod_flow_total{
  source_bcs_cluster_id="BCS-K8S-00000",
  source_namespace="bkmonitor-operator",
  source_pod="bkm-pod-1",
  target_bcs_cluster_id="BCS-K8S-00000",
  target_namespace="bkmonitor-operator",
  target_pod="bkm-pod-2"
} = 1000
```

**System 主机访问指标示例**：
```
bkmonitor:pod_to_system_flow_total{
  source_bcs_cluster_id="BCS-K8S-00000",
  source_namespace="bkmonitor-operator",
  source_pod="bkm-pod-1",
  target_bk_target_ip="10.0.0.1"
} = 500

bkmonitor:system_to_pod_flow_total{
  source_bk_target_ip="10.0.0.1",
  target_bcs_cluster_id="BCS-K8S-00000",
  target_namespace="bkmonitor-operator",
  target_pod="bkm-pod-1"
} = 300
```

**存储方式**：
- 数据由外部采集服务写入
- 现阶段存储在 VictoriaMetrics（VM）中
- 后续会调研时序图数据库的方案

**应用场景**：
- 服务依赖关系分析
- 流量拓扑可视化
- 性能瓶颈定位
- 跨资源类型的流量分析（如 Pod 到 System 的访问）

### 4.3 静态关联查询

**查询方式**：通过 PromQL 查询关联指标，获取实时关联关系。

**单层关联查询**：
```promql
count by (bcs_cluster_id, node) (
  node_with_pod_relation{
    bcs_cluster_id="cluster1",
    namespace="ns1",
    pod="pod1"
  }
)
```

**多层关联查询**：
使用 PromQL 的 `* on(...) group_left()` 操作符进行链式关联。

**示例**：查询 Pod 到 System 的关联（通过 Node）
```promql
count by (bk_target_ip) (
  node_with_system_relation{
    bcs_cluster_id="cluster1",
    bk_target_ip!="",
    node!=""
  }
  * on (bcs_cluster_id, node) group_left () (
    count by (bcs_cluster_id, node) (
      node_with_pod_relation{
        bcs_cluster_id="cluster1",
        namespace="ns1",
        node!="",
        pod="pod1"
      }
    )
  )
)
```

### 4.4 时间范围查询

**支持范围查询**：使用 `count_over_time` 支持时间范围查询。

**示例**：
```promql
count by (bcs_cluster_id, node) (
  count_over_time(
    node_with_pod_relation{
      bcs_cluster_id="cluster1",
      namespace="ns1",
      pod="pod1"
    }[1m]
  )
)
```

---

## 5. Info 扩展设计

### 5.1 Info 字段定义

**定义**：某些资源有扩展信息字段（Info），用于存储资源的额外属性。

**重要说明**：
- Info 字段**不属于资源的唯一维度**（Index），不能用于唯一标识资源实例
- Info 字段**不参与关联匹配**，仅用于资源属性的扩展展示
- Info 字段**可以参与资源过滤**，作为查询条件使用
- Info 数据由外部采集服务上报

**配置示例**：
```go
{
    Name: "container",
    Index: cmdb.Index{
        "bcs_cluster_id",
        "namespace",
        "pod",
        "container",
    },
    Info: cmdb.Index{
        "version",  // 容器版本
    },
},
{
    Name: "host",
    Index: cmdb.Index{
        "host_id",
    },
    Info: cmdb.Index{
        "version",         // 主机版本
        "env_name",        // 环境名称
        "env_type",        // 环境类型
        "service_version", // 服务版本
        "service_type",    // 服务类型
    },
},
```

### 5.2 Info 指标设计

**命名规则**：`{resource}_info_relation`

**指标结构**：
- **指标名称**：`bkmonitor:container_info_relation`
- **Label**：包含资源的 Index 和 Info 维度
  - `bcs_cluster_id`
  - `namespace`
  - `pod`
  - `container`
  - `version`（Info 字段）

**示例**：
```
bkmonitor:container_info_relation{
  bcs_cluster_id="BCS-K8S-00000",
  namespace="bkmonitor-operator",
  pod="bkm-pod-1",
  container="unify-query",
  version="3.9.3269"
} = 1
```

### 5.3 Info 扩展查询

**查询方式**：使用 PromQL 的 `* on(...) group_left(...)` 操作符扩展信息。

**示例**：查询 Container 到 Node 的关联，并扩展 Container 的版本信息
```promql
count by (bcs_cluster_id, node) (
  node_with_pod_relation{
    bcs_cluster_id!="",
    namespace!="",
    node!="",
    pod!=""
  }
  * on (bcs_cluster_id, namespace, pod) group_left () (
    count by (bcs_cluster_id, namespace, pod) (
      container_with_pod_relation{
        bcs_cluster_id!="",
        container="unify-query",
        namespace!="",
        pod!=""
      }
      * on (bcs_cluster_id, namespace, pod, container) group_left () (
        container_info_relation{
          bcs_cluster_id!="",
          container="unify-query",
          namespace!="",
          pod!="",
          version="3.9.3269"
        }
      )
    )
  )
)
```

---

## 6. 查询流程

### 6.1 查询请求

**API 接口**：
- `/api/v1/relation/multi_resource`：瞬时查询（instant）
- `/api/v1/relation/multi_resource_range`：范围查询（range）

**请求参数**：
```json
{
  "query_list": [
    {
      "timestamp": 1693973987,  // 瞬时查询时间戳
      "source_type": "pod",      // 源资源类型（可选）
      "source_info": {           // 源资源信息
        "bcs_cluster_id": "BCS-K8S-00000",
        "namespace": "bkmonitor-operator",
        "pod": "bkm-pod-1"
      },
      "source_expand_info": {    // 源资源扩展信息（可选）
        "version": "3.9.3269"
      },
      "target_type": "system",   // 目标资源类型
      "target_info_show": false, // 是否显示目标资源扩展信息
      "path_resource": [],        // 指定路径资源（可选）
      "look_back_delta": "5m"    // 回溯时间（可选）
    }
  ]
}
```

### 6.2 查询处理流程

```
1. 解析请求参数
   ↓
2. 识别源资源类型（如果未指定，通过 IndexMatcher 自动识别）
   ↓
3. 使用图算法查找从源资源到目标资源的所有路径
   ↓
4. 按路径长度排序，优先使用最短路径
   ↓
5. 对于每条路径（按长度从短到长）：
   a. 构建 PromQL 查询语句
   b. 查询关联指标
   c. 如果查询到数据，返回结果并停止（选择有数据的最短路径）
   ↓
6. 如果没有查询到数据，返回空列表
```

### 6.3 路径查找算法

**算法**：使用 `graph.AllPathsBetween` 查找所有路径。

**路径选择策略**：
1. 按路径长度排序，优先使用最短路径
2. 对于每条路径，依次查询关联指标
3. **选择有数据的最短路径**：如果某条路径查询到数据，立即返回结果并停止查询
4. 如果所有路径都没有数据，返回空列表

**示例**：从 `pod` 到 `system` 的路径
- 路径1：`pod -> node -> system`（长度：2，优先查询）
- 路径2：`pod -> apm_service_instance -> system`（长度：2，如果路径1无数据则查询）
- 路径3：`pod -> datasource -> node -> system`（长度：3，如果前两条路径都无数据则查询）
- 路径4：`pod -> container -> app_version -> host -> system`（长度：4，最后查询）

**路径过滤**：
- 如果指定了 `path_resource`，只返回匹配的路径
- 例如：`path_resource: ["node"]` 只返回包含 `node` 的路径

### 6.4 PromQL 查询构建

**构建规则**：
1. 对于路径上的每一段关联，查询对应的关联指标
2. 使用 `* on(...) group_left()` 进行链式关联
3. 使用 `count by (...)` 聚合目标资源的 Index 维度
4. 如果启用 `target_info_show`，使用 `* on(...) group_left(...)` 扩展信息

**示例**：从 Pod 到 System 的查询（路径：`pod -> node -> system`）
```promql
count by (bk_target_ip) (
  node_with_system_relation{
    bcs_cluster_id="cluster1",
    bk_target_ip!="",
    node!=""
  }
  * on (bcs_cluster_id, node) group_left () (
    count by (bcs_cluster_id, node) (
      node_with_pod_relation{
        bcs_cluster_id="cluster1",
        namespace="ns1",
        node!="",
        pod="pod1"
      }
    )
  )
)
```

---

## 7. 技术实现

### 7.1 图算法实现

**库**：使用 `github.com/dominikbraun/graph` 实现图算法。

**初始化**：
```go
g := graph.New(graph.StringHash)

// 添加顶点
for _, r := range cfg.Resource {
    g.AddVertex(string(r.Name))
}

// 添加边
for _, r := range cfg.Relation {
    g.AddEdge(string(r.Resources[0]), string(r.Resources[1]))
}
```

**路径查找**：
```go
paths, err := graph.AllPathsBetween(g, string(source), string(target))
```

### 7.2 查询构建实现

**QueryFactory**：负责构建 PromQL 查询语句。

**核心方法**：
- `buildRelationQueries`：构建关联查询
- `buildInfoQuery`：构建 Info 扩展查询
- `MakeQueryTs`：生成完整的查询结构

**查询构建流程**：
1. 解析路径，生成多个 `Relation`
2. 对于每个 `Relation`，构建对应的查询
3. 使用 `metricMerge` 表达式链式关联多个查询
4. 如果启用 `ExpandShow`，添加 Info 扩展查询

### 7.3 查询执行实现

**执行流程**：
1. 将 `QueryTs` 转换为 `QueryReference`
2. 根据路由信息选择存储引擎
3. 执行 PromQL 查询
4. 解析查询结果，提取目标资源的 Index 维度
5. 按时间戳聚合结果（范围查询）

**结果处理**：
```go
merged := make(map[int64]cmdb.Matchers)
for _, series := range matrix {
    for _, p := range series.Points {
        lbs := make(cmdb.Matcher, len(series.Metric))
        for _, m := range series.Metric {
            lbs[m.Name] = m.Value
        }
        merged[p.T] = append(merged[p.T], lbs)
    }
}
```

---

## 8. 性能优化设计

### 8.1 复杂多层关联查询优化

**问题**：多层关联查询（如 4 层以上）会导致 PromQL 查询语句复杂，查询性能下降。

**优化策略**：

#### 策略1：路径预筛选
- **原理**：在查询前先检查路径上每个关联关系是否存在数据
- **实现**：对每条路径的每个关联关系，先执行简单的存在性查询
- **优势**：避免执行复杂的多层 PromQL 查询
- **适用场景**：路径较长（4 层以上）的查询

#### 策略2：TimeGraph 缓存
- **原理**：使用 TimeGraph 数据结构缓存时间范围内的关联关系
- **实现**：
  - 一次性查询所有关联关系，构建 TimeGraph
  - 在 TimeGraph 中查找最短路径
  - 查询完成后自动清理 TimeGraph
- **优势**：
  - 减少重复查询
  - 支持路径上的所有资源查询（`QueryPathResources`）
  - 内存使用优化（节点跨时间点共享）
- **适用场景**：需要查询路径上所有资源的场景

#### 策略3：查询结果缓存
- **原理**：缓存常用的关联查询结果
- **实现**：
  - 使用 Redis 缓存查询结果
  - 缓存 Key：`relation:{source_type}:{target_type}:{source_info_hash}`
  - 缓存 TTL：根据数据更新频率设置（如 5 分钟）
- **优势**：减少对时序数据库的查询压力
- **适用场景**：高频查询场景

#### 策略4：并行查询
- **原理**：对于多条路径，并行查询而不是串行查询
- **实现**：
  - 使用 goroutine pool 并行查询多条路径
  - 设置超时时间，避免长时间等待
  - 一旦某条路径查询到数据，立即返回结果
- **优势**：提高查询响应速度
- **适用场景**：路径数量较多（3 条以上）的场景

#### 策略5：查询语句优化
- **原理**：优化 PromQL 查询语句，减少不必要的计算
- **实现**：
  - 使用 `count` 而不是 `sum` 进行聚合
  - 避免不必要的 `group_left` 操作
  - 使用 `by` 子句精确指定聚合维度
- **优势**：减少查询计算量
- **适用场景**：所有查询场景

### 8.2 TimeGraph 数据结构

**设计说明**：TimeGraph 是一个时间维度的图数据结构，用于高效处理时序关联关系。

**核心特性**：
- **时间分片图**：每个时间戳维护一个独立的图结构
- **局部字符串字典**：每个 TimeGraph 实例拥有独立的字符串字典，避免全局字典的内存泄漏
- **节点跨时间点共享**：相同节点在不同时间点共享 ID，减少内存使用

**API 接口**：
- `QueryPathResources`：查询指定时间点的路径上的所有资源（instant 查询）
- `QueryPathResourcesRange`：查询指定时间段的路径上的所有资源（range 查询）

**性能优势**：
- 内存使用降低 30.9%（基于 1000 节点、100 时间戳的测试）
- 性能提升 44.5%
- 支持自动清理，避免内存泄漏

---

## 9. 应用场景

### 8.1 资源拓扑查询

**场景**：查询资源的完整拓扑关系。

**示例**：
- 查询某个 Pod 运行在哪个 Node 上
- 查询某个 Node 上运行了哪些 Pod
- 查询某个 Container 属于哪个 Pod

### 8.2 资源关联分析

**场景**：分析资源之间的关联关系。

**示例**：
- 分析某个 Deployment 下的所有 Pod
- 分析某个 Service 关联的所有 Pod
- 分析某个 Ingress 关联的所有 Service

### 8.3 资源扩展信息查询

**场景**：查询资源的扩展信息。

**示例**：
- 查询 Container 的版本信息
- 查询 Host 的环境信息、服务版本等

### 9.4 时间范围关联查询

**场景**：查询资源在时间范围内的关联关系变化。

**示例**：
- 查询某个 Pod 在 1 小时内运行过的所有 Node
- 查询某个 Container 在 1 天内的版本变化

### 9.5 路径资源查询

**场景**：查询从源资源到目标资源的完整路径上的所有资源。

**示例**：
- 查询从 Pod 到 System 的路径上的所有资源（Pod -> Node -> System）
- 查询从 Container 到 System 的路径上的所有资源（Container -> Pod -> Node -> System）

**API 接口**：
- `/api/v1/relation/path_resources`：瞬时查询
- `/api/v1/relation/path_resources_range`：范围查询

---

## 10. 技术亮点

### 10.1 静态关联 + 动态关联（流量关联）

**创新点**：
- **静态关联**：通过配置定义资源类型之间的关联关系，支持图算法查找路径，通过 `{resource1}_with_{resource2}_relation` 指标存储拓扑关联
- **动态关联**：通过 `pod_to_pod_flow_total`、`pod_to_pod_flow_seconds`、`pod_to_pod_flow_error` 等指标存储流量访问关系，代表访问量、访问耗时、错误数

**优势**：
- 灵活性高：支持新增资源类型和关联关系
- 实时性强：通过时序数据获取实时关联关系
- 扩展性强：支持 Info 扩展信息
- 功能完整：同时支持拓扑关联和流量关联

### 10.2 多路径查询与智能路径选择

**创新点**：
- 使用图算法查找从源资源到目标资源的所有路径
- 按路径长度排序，优先使用最短路径
- **选择有数据的最短路径**：依次查询每条路径，一旦查询到数据立即返回
- 支持路径过滤（`pathResource`）

**优势**：
- 自动找到最优路径（有数据的最短路径）
- 支持多条路径的容错查询
- 灵活控制查询路径
- 避免无效查询，提高性能

### 10.3 PromQL 链式关联

**创新点**：
- 使用 PromQL 的 `* on(...) group_left()` 操作符进行链式关联
- 支持多层关联查询
- 支持 Info 扩展信息查询

**优势**：
- 利用 PromQL Engine 的强大计算能力
- 支持复杂的关联查询
- 性能优秀

### 10.4 TimeGraph 时序图数据结构

**创新点**：
- **时间分片图**：每个时间戳维护一个独立的图结构
- **局部字符串字典**：每个 TimeGraph 实例拥有独立的字符串字典，避免全局字典的内存泄漏
- **节点跨时间点共享**：相同节点在不同时间点共享 ID，减少内存使用
- **自动清理机制**：查询完成后自动清理 TimeGraph，避免内存泄漏

**优势**：
- 内存使用降低 30.9%（基于 1000 节点、100 时间戳的测试）
- 性能提升 44.5%
- 支持路径上的所有资源查询
- 支持时间范围内的关联关系变化追踪

### 10.5 时间范围查询

**创新点**：
- 支持瞬时查询（instant）和范围查询（range）
- 使用 `count_over_time` 支持时间范围查询
- 返回带时间戳的关联关系
- 支持 TimeGraph 的时间维度图查询

**优势**：
- 支持关联关系的时间变化查询
- 支持历史关联关系查询
- 支持关联关系的趋势分析
- 支持路径上的所有资源的时间变化追踪

---

**文档版本**：v1.0  
**最后更新**：2024年12月  
**维护者**：项目团队

