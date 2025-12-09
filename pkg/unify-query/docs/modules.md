# 核心模块说明文档

本文档详细说明 Unify-Query 项目的各个核心模块的功能、职责和关键实现。

## 目录

1. [HTTP 服务模块](#1-http-服务模块)
2. [查询处理模块](#2-查询处理模块)
3. [元数据模块](#3-元数据模块)
4. [存储抽象模块](#4-存储抽象模块)
5. [配置管理模块](#5-配置管理模块)
6. [缓存模块](#6-缓存模块)
7. [追踪模块](#7-追踪模块)

---

## 1. HTTP 服务模块

**路径**：`service/http`

### 1.1 功能概述

HTTP 服务模块是整个系统的入口，负责接收和处理所有 HTTP 请求。

### 1.2 核心组件

#### 1.2.1 Handler (`handler.go`)

主要处理器：

- **HandlerQueryTs**：处理结构体查询请求 (`/query/ts`)
- **HandlerQueryPromQL**：处理 PromQL 查询请求 (`/query/promql`)
- **HandlerQueryReference**：处理引用查询请求 (`/query/reference`)
- **HandlerCheckQueryTs**：处理查询校验请求 (`/check/query/ts`)

#### 1.2.2 Middleware (`middleware/`)

中间件：

- **JWT Middleware**：JWT 认证中间件
- **Metadata Middleware**：元数据注入中间件，从 Header 中提取用户信息、空间信息等

#### 1.2.3 Query Processing (`query.go`)

查询处理逻辑：

- **queryTsToInstanceAndStmt**：将查询结构体转换为存储实例和查询语句
- **queryTsWithPromEngine**：使用 PromQL 引擎执行查询
- **queryReferenceWithPromEngine**：执行引用查询

#### 1.2.4 Info Handlers (`info.go`, `infos.go`)

元数据查询接口：

- **HandlerInfoFieldKeys**：查询字段列表 (`/query/ts/info/field_keys`)
- **HandlerInfoTagKeys**：查询标签列表 (`/query/ts/info/tag_keys`)
- **HandlerInfoTagValues**：查询标签值 (`/query/ts/info/tag_values`)
- **HandlerInfoSeries**：查询 Series (`/query/ts/info/series`)

### 1.3 关键实现

#### 查询流程

```go
// 1. 解析请求
query := &structured.QueryTs{}
json.NewDecoder(c.Request.Body).Decode(query)

// 2. 转换为 QueryReference
queryRef, err := queryTs.ToQueryReference(ctx)

// 3. 获取存储实例
instance := prometheus.GetTsDbInstance(ctx, query)

// 4. 执行查询
result, err := instance.QueryRange(ctx, promql, start, end, step)

// 5. 格式化响应
resp.success(ctx, result)
```

---

## 2. 查询处理模块

**路径**：`query/`

### 2.1 功能概述

查询处理模块负责查询的解析、转换和执行。

### 2.2 核心组件

#### 2.2.1 Structured Query (`query/structured/`)

结构化查询处理：

- **QueryTs**：时间序列查询结构体
- **QueryPromQL**：PromQL 查询结构体
- **ToQueryReference**：转换为查询引用
- **ToPromQL**：转换为 PromQL 语句
- **ToPromExpr**：转换为 PromQL 表达式

#### 2.2.2 PromQL Query (`query/promql/`)

PromQL 查询处理：

- **Parser**：PromQL 解析器
- **Engine**：PromQL 执行引擎
- **Query**：查询执行器

#### 2.2.3 Interfaces (`query/interfaces.go`)

查询接口定义：

```go
type Querier interface {
    Query(ctx context.Context, query string, end time.Time) (*PromData, error)
    QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*PromData, error)
}
```

### 2.3 关键实现

#### 查询转换流程

```
PromQL 查询
    ↓
解析为 AST
    ↓
转换为结构化查询
    ↓
转换为 QueryReference
    ↓
执行查询
```

#### 结构化查询转换

```go
// QueryTs -> QueryReference
func (q *QueryTs) ToQueryReference(ctx context.Context) (QueryReference, error) {
    // 1. 根据 Space UID 获取表列表
    // 2. 根据 Table ID 获取表详情
    // 3. 构建 QueryReference
}
```

---

## 3. 元数据模块

**路径**：`metadata/`

### 3.1 功能概述

元数据模块管理查询所需的所有元数据信息，包括空间、表、路由等。

### 3.2 核心组件

#### 3.2.1 Metadata Cache (`metadata.go`)

元数据缓存：

- 使用 `go-cache` 实现内存缓存
- 基于 TraceID 进行缓存隔离
- 支持过期时间设置

#### 3.2.2 Space Management (`expand.go`)

空间管理：

- **GetSpace**：获取空间信息
- **GetSpaceToResultTable**：获取空间到表的映射
- **GetResultTableDetail**：获取表详情

#### 3.2.3 Query Reference (`query_reference.go`)

查询引用：

- **QueryReference**：查询引用接口
- **Query**：单个查询结构
- **SetQueryReference**：设置查询引用到上下文
- **GetQueryReference**：从上下文获取查询引用

#### 3.2.4 User Management (`user.go`)

用户管理：

- **User**：用户信息结构
- **SetUser**：设置用户信息到上下文
- **GetUser**：从上下文获取用户信息

#### 3.2.5 Status Management (`status.go`)

状态管理：

- **Status**：查询状态信息
- **SetStatus**：设置状态到上下文
- **GetStatus**：从上下文获取状态

### 3.3 关键实现

#### 元数据获取流程

```go
// 1. 从 Redis 获取空间表映射
spaceToTable := redis.GetSpaceToResultTable(spaceUID)

// 2. 从 Redis 获取表详情
tableDetail := redis.GetResultTableDetail(tableID)

// 3. 从 Consul 获取存储配置
storage := consul.GetStorage(storageID)

// 4. 构建查询引用
queryRef := BuildQueryReference(spaceToTable, tableDetail, storage)
```

#### 缓存策略

- **一级缓存**：内存缓存（Ristretto），存储热点数据
- **二级缓存**：Redis，存储持久化数据
- **缓存更新**：通过 Consul Watch 机制监听配置变化

---

## 4. 存储抽象模块

**路径**：`tsdb/`

### 4.1 功能概述

存储抽象模块提供统一的存储接口，屏蔽不同存储后端的差异。

### 4.2 核心组件

#### 4.2.1 Instance Interface (`interfaces.go`)

存储实例接口：

```go
type Instance interface {
    QueryRaw(ctx context.Context, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet
    QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (*PromData, error)
    Query(ctx context.Context, promql string, end time.Time) (*PromData, error)
    LabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error)
    LabelValues(ctx context.Context, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error)
    Series(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet
    InstanceType() string
}
```

#### 4.2.2 Storage Implementations

存储实现：

- **InfluxDB** (`tsdb/influxdb/`)：InfluxDB 存储实现
- **VictoriaMetrics** (`tsdb/victoriaMetrics/`)：VictoriaMetrics 存储实现
- **Prometheus** (`tsdb/prometheus/`)：Prometheus 存储实现
- **Elasticsearch** (`tsdb/elasticsearch/`)：Elasticsearch 存储实现
- **Redis** (`tsdb/redis/`)：Redis 存储实现
- **BKSQL** (`tsdb/bksql/`)：BKSQL 存储实现

#### 4.2.3 Storage Management (`storage.go`)

存储管理：

- **ReloadTsDBStorage**：重新加载存储配置
- **GetStorage**：获取存储实例
- **SetStorage**：设置存储实例

### 4.3 关键实现

#### 存储实例创建

```go
// 根据存储类型创建对应的实例
switch storage.Type {
case metadata.InfluxDBStorageType:
    instance = influxdb.NewInstance(ctx, storage)
case metadata.VictoriaMetricsStorageType:
    instance = victoriametrics.NewInstance(ctx, storage)
case metadata.PrometheusStorageType:
    instance = prometheus.NewInstance(ctx, storage)
// ...
}
```

#### 查询路由

```go
// 1. 根据表信息获取存储 ID
storageID := tableDetail.StorageID

// 2. 获取存储配置
storage, err := tsdb.GetStorage(storageID)

// 3. 创建存储实例
instance := NewInstance(storage)

// 4. 执行查询
result, err := instance.QueryRange(ctx, promql, start, end, step)
```

---

## 5. 配置管理模块

**路径**：`config/`, `consul/`

### 5.1 功能概述

配置管理模块负责管理系统的所有配置信息。

### 5.2 核心组件

#### 5.2.1 Config (`config/`)

配置管理：

- **settings.go**：配置项定义
- **hook.go**：配置钩子函数

#### 5.2.2 Consul (`consul/`)

Consul 配置中心：

- **consul.go**：Consul 客户端
- **storage.go**：存储配置管理
- **router.go**：路由配置管理
- **tsdb.go**：TSDB 配置管理

### 5.3 关键实现

#### 配置加载流程

```go
// 1. 加载本地配置文件
viper.ReadInConfig()

// 2. 从 Consul 加载动态配置
consul.WatchStorageInfo(ctx)
consul.WatchRouterInfo(ctx)

// 3. 配置变更时重新加载
consul.OnChange(func() {
    ReloadTsDBStorage(ctx, storages)
})
```

#### 配置热重载

```go
// 监听 SIGUSR1 信号
signal.Notify(sc, syscall.SIGUSR1)

// 收到信号后重新加载配置
case syscall.SIGUSR1:
    config.InitConfig()
    service.Reload(ctx)
```

---

## 6. 缓存模块

**路径**：`memcache/`, `metadata/`

### 6.1 功能概述

缓存模块提供多级缓存能力，提升查询性能。

### 6.2 核心组件

#### 6.2.1 Memory Cache (`memcache/ristretto.go`)

内存缓存（Ristretto）：

- 高性能内存缓存
- 支持 TTL
- 支持 LRU 淘汰策略

#### 6.2.2 Metadata Cache (`metadata/metadata.go`)

元数据缓存：

- 基于 TraceID 的缓存隔离
- 支持过期时间设置
- 自动清理过期数据

#### 6.2.3 Redis Cache (`redis/`)

Redis 缓存：

- 持久化缓存
- 支持分布式缓存
- 支持发布订阅

### 6.3 关键实现

#### 缓存策略

```go
// 1. 先查内存缓存
if value, ok := memCache.Get(key); ok {
    return value
}

// 2. 再查 Redis 缓存
if value, err := redis.Get(key); err == nil {
    memCache.Set(key, value, ttl)
    return value
}

// 3. 最后查数据源
value := fetchFromDataSource(key)
memCache.Set(key, value, ttl)
redis.Set(key, value, ttl)
return value
```

---

## 7. 追踪模块

**路径**：`trace/`, `service/trace/`

### 7.1 功能概述

追踪模块提供分布式追踪能力，用于问题排查和性能分析。

### 7.2 核心组件

#### 7.2.1 Trace (`trace/trace.go`)

追踪实现：

- 使用 OpenTelemetry
- 支持 TraceID 传递
- 支持 Span 创建和管理

#### 7.2.2 Trace Service (`service/trace/`)

追踪服务：

- 初始化追踪配置
- 配置追踪导出器
- 管理追踪生命周期

### 7.3 关键实现

#### 追踪使用

```go
// 创建 Span
ctx, span := trace.NewSpan(ctx, "operation-name")
defer span.End(&err)

// 设置属性
span.Set("key", "value")

// 记录事件
span.AddEvent("event-name")
```

---

## 8. 其他重要模块

### 8.1 降采样模块 (`downsample/`)

- **LTTB 算法**：Largest-Triangle-Three-Buckets 降采样算法
- **Realignment**：数据对齐

### 8.2 解析器模块 (`internal/`)

- **PromQL Parser** (`internal/promql_parser/`)：PromQL 语法解析
- **InfluxQL Parser** (`internal/doris_parser/`)：InfluxQL 语法解析
- **Lucene Parser** (`internal/lucene_parser/`)：Lucene 查询语法解析

### 8.3 函数模块 (`internal/function/`)

- 自定义 PromQL 函数实现

### 8.4 特性开关模块 (`featureFlag/`)

- 功能特性开关管理
- 支持基于用户、空间的条件判断

---

## 9. 模块间交互

### 9.1 查询请求流程

```
HTTP Handler
    ↓
Query Processor (解析查询)
    ↓
Metadata (获取元数据)
    ↓
TSDB (选择存储)
    ↓
Storage Instance (执行查询)
    ↓
Response Formatter (格式化响应)
```

### 9.2 配置更新流程

```
Consul Watch
    ↓
Config Service (接收配置变更)
    ↓
Storage Service (更新存储配置)
    ↓
Metadata Service (更新元数据缓存)
```

---

## 10. 扩展指南

### 10.1 添加新的存储引擎

1. 在 `tsdb/` 下创建新的包
2. 实现 `tsdb.Instance` 接口
3. 在 `tsdb/storage.go` 中注册存储类型
4. 实现查询逻辑

### 10.2 添加新的查询函数

1. 在 `internal/function/` 中实现函数
2. 在 PromQL 解析器中注册函数
3. 实现对应的查询逻辑

### 10.3 添加新的 API 接口

1. 在 `service/http/handler.go` 中添加 Handler
2. 在 `service/http/register_urls.go` 中注册路由
3. 实现业务逻辑

---

## 附录

### A. 模块依赖关系

```
service/http
    ↓
query/
    ↓
metadata/
    ↓
tsdb/
    ↓
consul/ redis/ influxdb/
```

### B. 关键数据结构

参考各模块的 `struct.go` 文件查看详细的数据结构定义。

