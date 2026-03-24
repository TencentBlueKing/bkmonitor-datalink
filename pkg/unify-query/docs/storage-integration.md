# 存储引擎集成文档

本文档说明如何在 Unify-Query 中集成新的存储引擎，以及现有存储引擎的实现细节。

## 目录

1. [存储接口定义](#1-存储接口定义)
2. [集成新存储引擎](#2-集成新存储引擎)
3. [现有存储引擎](#3-现有存储引擎)
4. [配置管理](#4-配置管理)
5. [最佳实践](#5-最佳实践)

---

## 1. 存储接口定义

### 1.1 Instance 接口

所有存储引擎都需要实现 `tsdb.Instance` 接口：

```go
type Instance interface {
    // QueryRaw 原始查询接口，返回 SeriesSet
    QueryRaw(ctx context.Context, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet
    
    // QueryRange 范围查询接口，执行 PromQL 查询并返回时间序列数据
    QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (*PromData, error)
    
    // Query 即时查询接口，执行 PromQL 查询并返回瞬时数据
    Query(ctx context.Context, promql string, end time.Time) (*PromData, error)
    
    // LabelNames 获取标签名称列表
    LabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error)
    
    // LabelValues 获取指定标签的值列表
    LabelValues(ctx context.Context, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error)
    
    // Series 获取 Series 列表
    Series(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet
    
    // InstanceType 返回存储类型标识
    InstanceType() string
}
```

### 1.2 PromData 结构

查询结果统一使用 `PromData` 结构：

```go
type PromData struct {
    ResultType string      `json:"resultType"`
    Result     interface{} `json:"result"`
    Tables     []*Table    `json:"tables,omitempty"`
}
```

### 1.3 Table 结构

时间序列数据使用 `Table` 结构：

```go
type Table struct {
    Name        string      `json:"name"`
    MetricName  string      `json:"metric_name"`
    Columns     []string    `json:"columns"`
    Types       []string    `json:"types"`
    GroupKeys   []string    `json:"group_keys"`
    GroupValues []string    `json:"group_values"`
    Values      [][]interface{} `json:"values"`
}
```

---

## 2. 集成新存储引擎

### 2.1 步骤概览

1. 创建存储包
2. 实现 Instance 接口
3. 注册存储类型
4. 实现查询逻辑
5. 添加配置支持

### 2.2 详细步骤

#### 步骤 1：创建存储包

在 `tsdb/` 目录下创建新的包，例如 `tsdb/mystorage/`：

```bash
mkdir -p tsdb/mystorage
```

#### 步骤 2：定义存储类型常量

在 `metadata/tsdb.go` 中添加新的存储类型：

```go
const (
    // ... 其他存储类型
    MyStorageType = "my_storage"
)
```

#### 步骤 3：实现 Instance 接口

创建 `instance.go` 文件：

```go
package mystorage

import (
    "context"
    "time"
    
    "github.com/prometheus/prometheus/model/labels"
    "github.com/prometheus/prometheus/storage"
    
    "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
    "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

type Instance struct {
    address  string
    username string
    password string
    timeout  time.Duration
}

var _ tsdb.Instance = (*Instance)(nil)

func NewInstance(ctx context.Context, opt *Options) (*Instance, error) {
    return &Instance{
        address:  opt.Address,
        username: opt.Username,
        password: opt.Password,
        timeout:  opt.Timeout,
    }, nil
}

func (i *Instance) InstanceType() string {
    return metadata.MyStorageType
}

func (i *Instance) QueryRaw(ctx context.Context, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
    // 实现原始查询逻辑
    // ...
}

func (i *Instance) QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (*tsdb.PromData, error) {
    // 实现范围查询逻辑
    // ...
}

func (i *Instance) Query(ctx context.Context, promql string, end time.Time) (*tsdb.PromData, error) {
    // 实现即时查询逻辑
    // ...
}

func (i *Instance) LabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
    // 实现标签名称查询逻辑
    // ...
}

func (i *Instance) LabelValues(ctx context.Context, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
    // 实现标签值查询逻辑
    // ...
}

func (i *Instance) Series(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet {
    // 实现 Series 查询逻辑
    // ...
}
```

#### 步骤 4：注册存储类型

在 `tsdb/prometheus/tsdb_instance.go` 的 `GetTsDbInstance` 函数中添加新存储类型的处理：

```go
func GetTsDbInstance(ctx context.Context, qry *metadata.Query) tsdb.Instance {
    switch qry.StorageType {
    case metadata.InfluxDBStorageType:
        // ... InfluxDB 处理
    case metadata.MyStorageType:
        opt := &mystorage.Options{
            Address:  storage.Address,
            Username: storage.Username,
            Password: storage.Password,
            Timeout:  tsDBService.MyStorageTimeout,
        }
        instance, err = mystorage.NewInstance(ctx, opt)
    // ...
    }
}
```

#### 步骤 5：添加配置支持

在 `tsdb/storage.go` 的 `ReloadTsDBStorage` 函数中添加配置处理：

```go
func ReloadTsDBStorage(_ context.Context, tsDBs map[string]*consul.Storage, opt *Options) error {
    for storageID, tsDB := range tsDBs {
        switch tsDB.Type {
        case metadata.MyStorageType:
            storage.Timeout = opt.MyStorage.Timeout
            storage.MaxLimit = opt.MyStorage.MaxLimit
        // ...
        }
    }
}
```

在 `tsdb/struct.go` 中添加配置结构：

```go
type MyStorageOption struct {
    Timeout  time.Duration
    MaxLimit int
}

type Options struct {
    // ... 其他配置
    MyStorage *MyStorageOption
}
```

---

## 3. 现有存储引擎

### 3.1 InfluxDB

**路径**：`tsdb/influxdb/`

**特点**：
- 支持 HTTP 和 gRPC 协议
- 支持多种内容类型（Protobuf、JSON、Msgpack）
- 支持数据压缩（Snappy）
- 支持查询限流

**关键实现**：
- 使用 InfluxQL 查询语言
- 支持 PromQL 到 InfluxQL 的转换
- 支持分片查询和结果合并

**配置示例**：
```yaml
influxdb:
  timeout: 30s
  content_type: "application/x-protobuf"
  chunk_size: 10000
  max_limit: 100000
  read_rate_limit: 1000.0
```

### 3.2 VictoriaMetrics

**路径**：`tsdb/victoriaMetrics/`

**特点**：
- 高性能时序数据库
- 完全兼容 PromQL
- 支持直查模式

**关键实现**：
- 直接使用 PromQL 查询
- 支持 VM 扩展语法
- 支持查询优化

**配置示例**：
```yaml
victoria_metrics:
  uri_path: "/api/v1/query_range"
  timeout: 30s
```

### 3.3 Prometheus

**路径**：`tsdb/prometheus/`

**特点**：
- 使用 Prometheus 查询引擎
- 支持多存储后端路由
- 支持查询结果合并

**关键实现**：
- 集成 Prometheus PromQL 引擎
- 支持查询路由到不同的存储后端
- 支持 Singleflight 避免重复查询

**配置示例**：
```yaml
prometheus:
  query_max_routing: 10
  singleflight_timeout: 5s
```

### 3.4 Elasticsearch

**路径**：`tsdb/elasticsearch/`

**特点**：
- 支持 Lucene 查询语法
- 支持日志和事件数据查询
- 支持 BkData API 集成

**关键实现**：
- 使用 Elasticsearch Query DSL
- 支持 Lucene 到 ES Query 的转换
- 支持滚动查询（Scroll API）

**配置示例**：
```yaml
elasticsearch:
  timeout: 30s
  max_routing: 10
  max_size: 10000
```

### 3.5 Redis

**路径**：`tsdb/redis/`

**特点**：
- 支持热数据查询
- 支持 Hash 数据结构
- 支持数据标签过滤

**关键实现**：
- 使用 Redis Hash 存储时序数据
- 支持标签过滤和聚合
- 支持数据序列化/反序列化

**配置示例**：
```yaml
redis:
  timeout: 5s
  max_limit: 10000
```

### 3.6 Doris

**路径**：`tsdb/bksql/`

**特点**：
- 支持 SQL 查询
- 支持大数据分析
- 支持复杂聚合查询

**关键实现**：
- 使用 SQL 查询语言
- 支持 PromQL 到 SQL 的转换
- 支持查询结果缓存

**配置示例**：
```yaml
doris:
  timeout: 60s
  max_limit: 1000000
```

---

## 4. 配置管理

### 4.1 Consul 配置

存储实例配置存储在 Consul 中：

**路径格式**：`bkmonitorv3/unify-query/data/storage/{storage_id}`

**配置格式**：
```json
{
  "address": "http://127.0.0.1:8086",
  "username": "",
  "password": "",
  "type": "influxdb"
}
```

### 4.2 配置加载

配置通过 Consul Watch 机制动态加载：

```go
// 监听配置变化
ch, err := consul.WatchStorageInfo(ctx)

// 处理配置变更
for change := range ch {
    ReloadTsDBStorage(ctx, storages, opt)
}
```

### 4.3 配置热重载

支持通过信号触发配置重载：

```go
// 监听 SIGUSR1 信号
signal.Notify(sc, syscall.SIGUSR1)

// 收到信号后重新加载
case syscall.SIGUSR1:
    config.InitConfig()
    service.Reload(ctx)
```

---

## 5. 最佳实践

### 5.1 查询实现建议

1. **错误处理**：所有查询方法都应该返回明确的错误信息
2. **超时控制**：设置合理的查询超时时间
3. **结果格式化**：统一使用 `PromData` 和 `Table` 结构
4. **日志记录**：记录查询的关键信息（查询语句、参数、耗时等）
5. **追踪支持**：使用 OpenTelemetry 记录查询链路

### 5.2 性能优化建议

1. **连接池**：使用连接池管理数据库连接
2. **查询缓存**：对热点查询结果进行缓存
3. **并发控制**：限制并发查询数量
4. **结果分页**：对大数据量查询进行分页处理
5. **查询优化**：优化查询语句，减少数据传输量

### 5.3 测试建议

1. **单元测试**：为每个查询方法编写单元测试
2. **集成测试**：编写集成测试验证端到端功能
3. **性能测试**：进行性能测试，确保满足性能要求
4. **错误测试**：测试各种错误场景的处理

### 5.4 代码组织建议

1. **包结构**：按照功能组织代码文件
2. **接口设计**：保持接口简洁，避免过度设计
3. **文档注释**：为公共方法添加详细的文档注释
4. **错误定义**：定义清晰的错误类型和错误消息

---

## 6. 示例：集成新的时序数据库

假设要集成一个新的时序数据库 "TimeSeriesDB"：

### 6.1 创建包结构

```bash
mkdir -p tsdb/timeseriesdb
touch tsdb/timeseriesdb/instance.go
touch tsdb/timeseriesdb/options.go
touch tsdb/timeseriesdb/query.go
```

### 6.2 实现 Instance 接口

参考 `tsdb/influxdb/instance.go` 的实现，实现所有必需的方法。

### 6.3 添加配置

在配置文件中添加新存储的配置项，并在代码中读取和使用这些配置。

### 6.4 编写测试

编写单元测试和集成测试，确保功能正常。

### 6.5 更新文档

更新相关文档，说明新存储引擎的使用方法。

---

## 7. 常见问题

### Q1: 如何支持自定义查询语言？

A: 可以在存储实现中添加自定义查询方法，或者实现 PromQL 到自定义查询语言的转换器。

### Q2: 如何处理存储后端的认证？

A: 在存储配置中包含认证信息（用户名、密码、Token 等），在查询时使用这些信息进行认证。

### Q3: 如何支持查询结果缓存？

A: 可以在存储实现中添加缓存层，或者使用统一的缓存服务。

### Q4: 如何处理存储后端的故障？

A: 实现重试机制和降级策略，在存储不可用时返回错误或使用备用存储。

### Q5: 如何支持查询限流？

A: 使用 Go 的 `golang.org/x/time/rate` 包实现限流功能。

---

## 附录

### A. 相关文件

- `tsdb/interfaces.go`：存储接口定义
- `tsdb/storage.go`：存储管理
- `tsdb/struct.go`：存储结构定义
- `metadata/tsdb.go`：存储类型常量

### B. 参考实现

- InfluxDB：`tsdb/influxdb/`
- VictoriaMetrics：`tsdb/victoriaMetrics/`
- Prometheus：`tsdb/prometheus/`

