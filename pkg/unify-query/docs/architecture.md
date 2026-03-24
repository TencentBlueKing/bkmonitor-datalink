# Unify-Query 架构设计文档

## 1. 项目概述

Unify-Query 是蓝鲸监控平台（BlueKing Monitor）的统一查询模块，提供可观测数据的统一查询入口，支持多种存储引擎的 PromQL 语法查询。

### 1.1 核心功能

- **统一查询接口**：提供统一的 HTTP API 接口，支持结构体查询和 PromQL 查询
- **多存储引擎支持**：支持 InfluxDB、VictoriaMetrics、Prometheus、Elasticsearch、Redis、Doris 等多种存储后端
- **PromQL 兼容**：完整支持 PromQL 语法，包括函数、操作符、聚合等
- **元数据管理**：提供表结构、字段、标签等元数据查询能力
- **查询优化**：支持查询路由、降采样、结果合并等优化功能

### 1.2 技术栈

- **语言**：Go 1.24+
- **Web 框架**：Gin
- **配置管理**：Viper + Consul
- **缓存**：Redis + Ristretto (内存缓存)
- **追踪**：OpenTelemetry
- **解析器**：ANTLR4 (PromQL、InfluxQL、Lucene 等)

## 2. 系统架构

### 2.1 整体架构

```
┌─────────────────────────────────────────────────────────────┐
│                      HTTP API Layer                          │
│  /query/ts  /query/promql  /query/reference  /check/query    │
└──────────────────────┬──────────────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────────────┐
│                   Query Processing Layer                     │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐     │
│  │ Query Parser │  │ Query Router │  │ Query Engine │     │
│  └──────────────┘  └──────────────┘  └──────────────┘     │
└──────────────────────┬──────────────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────────────┐
│                    Metadata Layer                           │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐     │
│  │ Space Cache  │  │ Table Cache  │  │ Route Cache  │     │
│  └──────────────┘  └──────────────┘  └──────────────┘     │
└──────────────────────┬──────────────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────────────┐
│                  Storage Abstraction Layer                   │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  │
│  │ InfluxDB │  │    VM    │  │Prometheus │  │    ES    │  │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 核心组件

#### 2.2.1 HTTP 服务层 (`service/http`)

负责接收和处理 HTTP 请求，主要包含：

- **Handler**：请求处理器，包括 `HandlerQueryTs`、`HandlerQueryPromQL` 等
- **Middleware**：中间件，包括 JWT 认证、元数据注入等
- **Response**：响应格式化

#### 2.2.2 查询处理层 (`query`)

负责查询的解析、转换和执行：

- **structured**：结构化查询处理
- **promql**：PromQL 查询处理
- **interfaces**：查询接口定义

#### 2.2.3 元数据层 (`metadata`)

管理查询所需的元数据信息：

- **Space**：空间（租户）信息
- **Table**：表结构信息
- **Route**：路由信息（表到存储的映射）
- **Cache**：元数据缓存

#### 2.2.4 存储抽象层 (`tsdb`)

提供统一的存储接口，支持多种存储后端：

- **InfluxDB**：时序数据库
- **VictoriaMetrics**：高性能时序数据库
- **Prometheus**：监控系统
- **Elasticsearch**：搜索引擎
- **Redis**：内存数据库
- **Doris**：分析型数据库

## 3. 查询流程

### 3.1 查询请求流程

```
1. HTTP 请求
   ↓
2. 中间件处理（认证、元数据注入）
   ↓
3. 查询解析（PromQL → 结构体 或 结构体 → QueryReference）
   ↓
4. 元数据查询（获取表信息、路由信息）
   ↓
5. 存储实例选择（根据路由选择对应的存储后端）
   ↓
6. 查询执行（调用存储后端的查询接口）
   ↓
7. 结果合并与格式化
   ↓
8. 降采样处理（如需要）
   ↓
9. HTTP 响应
```

### 3.2 两种查询模式

#### 3.2.1 结构体查询 (`/query/ts`)

- **输入**：结构化的查询 JSON
- **处理**：直接转换为 QueryReference，然后执行查询
- **适用场景**：前端 UI、API 调用

#### 3.2.2 PromQL 查询 (`/query/promql`)

- **输入**：PromQL 查询语句
- **处理**：先解析 PromQL，转换为结构体，再转换为 QueryReference
- **适用场景**：兼容 Prometheus 生态

### 3.3 查询路由

查询路由决定查询应该发送到哪个存储后端：

1. **根据 Space UID 获取表列表**
2. **根据 Table ID 获取表详情（包括 Storage ID）**
3. **根据 Storage ID 获取存储实例配置**
4. **根据存储类型选择对应的查询实现**

## 4. 元数据管理

### 4.1 元数据来源

元数据主要存储在：

- **Redis**：空间表映射、表详情、标签映射等
- **Consul**：存储实例配置
- **内存缓存**：查询过程中的临时缓存

### 4.2 元数据结构

#### Space（空间）

```go
SpaceUID -> {
    TableID -> {
        Filters: [...],
        ...
    }
}
```

#### Table（表）

```go
TableID -> {
    StorageID: "...",
    DB: "...",
    Measurement: "...",
    Fields: [...],
    TagsKey: [...],
    ...
}
```

#### Storage（存储）

```go
StorageID -> {
    Type: "influxdb|victoria_metrics|...",
    Address: "...",
    Username: "...",
    Password: "..."
}
```

## 5. 存储引擎集成

### 5.1 存储接口

所有存储引擎都实现 `tsdb.Instance` 接口：

```go
type Instance interface {
    QueryRaw(ctx context.Context, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet
    QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promData *PromData, err error)
    Query(ctx context.Context, promql string, end time.Time) (promData *PromData, err error)
    LabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error)
    LabelValues(ctx context.Context, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error)
    Series(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet
    InstanceType() string
}
```

### 5.2 支持的存储类型

| 存储类型        | 说明             | 适用场景           |
| --------------- | ---------------- | ------------------ |
| InfluxDB        | 时序数据库       | 传统时序数据存储   |
| VictoriaMetrics | 高性能时序数据库 | 大规模时序数据查询 |
| Prometheus      | 监控系统         | Prometheus 数据源  |
| Elasticsearch   | 搜索引擎         | 日志、事件数据     |
| Redis           | 内存数据库       | 热数据查询         |
| Doris           | 分析型数据库     | 大数据分析         |

## 6. 性能优化

### 6.1 缓存策略

- **元数据缓存**：使用内存缓存（Ristretto）缓存表信息、路由信息
- **查询结果缓存**：支持查询结果缓存（可选）

### 6.2 查询优化

- **查询路由**：根据表信息自动路由到对应的存储后端
- **结果合并**：多表查询时自动合并结果
- **降采样**：支持 LTTB 算法进行数据降采样
- **并发查询**：支持并发查询多个存储后端

### 6.3 限流与熔断

- **查询限流**：限制单次查询的查询列表数量
- **超时控制**：设置查询超时时间
- **单飞模式**：相同查询使用 singleflight 避免重复查询

## 7. 可观测性

### 7.1 日志

- 使用 `logrus` 进行日志记录
- 支持结构化日志
- 包含 TraceID 用于链路追踪

### 7.2 追踪

- 使用 OpenTelemetry 进行分布式追踪
- 支持 TraceID 传递
- 记录查询各个阶段的耗时

### 7.3 指标

- 使用 Prometheus 客户端库记录指标
- 记录查询次数、耗时、错误率等

## 8. 配置管理

### 8.1 配置来源

- **配置文件**：YAML 格式的配置文件
- **Consul**：动态配置（存储实例、路由等）
- **环境变量**：部分配置可通过环境变量覆盖

### 8.2 配置热重载

- 支持通过 `SIGUSR1` 信号触发配置重载
- 支持 Consul Watch 机制监听配置变化

## 9. 扩展性

### 9.1 添加新的存储引擎

1. 实现 `tsdb.Instance` 接口
2. 在 `tsdb/storage.go` 中注册存储类型
3. 实现对应的查询逻辑

### 9.2 添加新的查询函数

1. 在 `internal/function` 中实现函数
2. 在 PromQL 解析器中注册函数
3. 实现对应的查询逻辑

## 10. 安全

### 10.1 认证

- 支持 JWT 认证
- 支持通过 Header 传递用户信息

### 10.2 授权

- 基于 Space UID 进行空间隔离
- 支持表级别的过滤条件

### 10.3 数据安全

- 支持密码加密存储
- 支持 HTTPS 传输

## 11. 部署架构

### 11.1 服务启动流程

```
1. 加载配置
2. 初始化日志
3. 初始化元数据缓存
4. 启动服务：
   - Consul 服务（配置管理）
   - Redis 服务（元数据）
   - Trace 服务（追踪）
   - InfluxDB 服务（存储）
   - TSDB 服务（存储抽象）
   - PromQL 服务（查询引擎）
   - HTTP 服务（API）
   - FeatureFlag 服务（特性开关）
5. 监听信号（重载、关闭）
```

### 11.2 依赖服务

- **Consul**：配置中心
- **Redis**：元数据存储
- **InfluxDB/VictoriaMetrics 等**：数据存储

## 12. 故障处理

### 12.1 容错机制

- **存储故障**：查询失败时返回错误，不影响其他查询
- **元数据缺失**：返回明确的错误信息
- **超时处理**：设置查询超时，避免长时间等待

### 12.2 降级策略

- **缓存降级**：缓存失效时直接从数据源查询
- **功能降级**：部分功能不可用时返回基础功能

---

## 附录

### A. 相关文档

- [API 文档](./api/api.md)
- [PromQL 文档](./promql/promql.md)
- [LTTB 降采样](./lttb/lttb.md)

### B. 架构图

参考 `docs/common/unify-query-arch.png` 查看详细的架构流程图。

