# Redis 特性开关实现文档

## 1. 概述

本文档描述 `unify-query` 模块使用 Redis 作为特性开关（Feature Flag）配置中心的完整实现流程。系统通过 Redis KV 存储特性开关配置，并利用 Redis Pub/Sub 实现实时配置变更通知，实现无需重启服务的动态配置更新。

### 1.1 核心组件

- **配置存储**: Redis KV Store
- **配置 Key**: `bkmonitorv3:unify-query:data:feature_flag`
- **变更通知 Channel**: `bkmonitorv3:unify-query:data:feature_flag:feature_flag_channel`
- **特性开关库**: `go-feature-flag` (github.com/thomaspoignant/go-feature-flag)
- **监听机制**: Redis Pub/Sub
- **默认数据源**: Redis（可通过配置切换到 Consul）

### 1.2 架构图

```
┌─────────────────┐
│   Redis KV      │ 存储配置
│   (Key-Value)   │
└────────┬────────┘
         │ SET
         ▼
┌─────────────────┐
│ Redis Pub/Sub   │ 发布变更通知
│   (Channel)     │
└────────┬────────┘
         │ PUBLISH
         ▼
┌─────────────────┐
│  监听循环       │ 接收变更通知
│  (Watch Loop)   │
└────────┬────────┘
         │ 重新加载
         ▼
┌─────────────────┐
│  内存缓存       │ 快速读取
│  (In-Memory)    │
└────────┬────────┘
         │ Retrieve
         ▼
┌─────────────────┐
│ go-feature-flag │ 特性开关评估
│   (Client)      │
└─────────────────┘
```

## 2. 配置初始化

### 2.1 默认配置设置

**文件**: `pkg/unify-query/service/featureFlag/hook.go`

```go
func setDefaultConfig() {
    viper.SetDefault(DataSourceConfigPath, "redis")
}
```

在配置解析前，通过事件总线订阅设置默认数据源为 `redis`。

### 2.2 配置加载

**文件**: `pkg/unify-query/service/featureFlag/hook.go`

```go
func LoadConfig() {
    DataSource = viper.GetString(DataSourceConfigPath)
    if DataSource != "consul" && DataSource != "redis" {
        DataSource = "redis" // 默认使用 redis，处理配置异常状况
    }
}
```

从配置文件读取数据源配置，如果配置异常（既不是 `consul` 也不是 `redis`），则默认使用 `redis`。

### 2.3 配置文件

**文件**: `pkg/unify-query/unify-query.yaml`

```yaml
feature_flag:
  data_source: redis  # 可选值: consul 或 redis,默认为 redis
```

## 3. 服务启动流程

### 3.1 服务初始化

**文件**: `pkg/unify-query/service/featureFlag/featureFlag.go`

```go
func (s *Service) Reload(ctx context.Context) {
    // 1. 初始化 WaitGroup
    s.wg = new(sync.WaitGroup)
    
    // 2. 创建上下文
    s.ctx, s.cancelFunc = context.WithCancel(ctx)
    
    // 3. 启动监听循环
    err = s.loopReloadFeatureFlags(s.ctx)
    
    // 4. 初始化 go-feature-flag 客户端
    err = ffclient.Init(ffclient.Config{
        PollingInterval: 1 * time.Minute,
        Context:         s.ctx,
        Retriever:       &featureFlag.CustomRetriever{},
        FileFormat:      "json",
        DataExporter: ffclient.DataExporter{
            FlushInterval:    5 * time.Second,
            MaxEventInMemory: 100,
            Exporter:         &featureFlag.CustomExport{},
        },
    })
}
```

### 3.2 配置加载流程

**方法**: `reloadFeatureFlags`

```go
func (s *Service) reloadFeatureFlags(ctx context.Context) error {
    var data []byte
    var err error
    
    // 根据配置选择数据源
    if DataSource == "consul" {
        data, err = consul.GetFeatureFlags()
    } else {
        // 默认使用 Redis
        data, err = redis.GetFeatureFlags(ctx)
    }
    
    // 加载到内存
    err = featureFlag.ReloadFeatureFlags(data)
    return err
}
```

## 4. Redis 配置获取

### 4.1 获取配置

**文件**: `pkg/unify-query/redis/featureFlag.go`

```go
// GetFeatureFlagsPath 获取特性开关的 Redis 存储 key
func GetFeatureFlagsPath() string {
    return fmt.Sprintf("%s:%s:%s", basePath, dataPath, featureFlagPath)
    // 返回: bkmonitorv3:unify-query:data:feature_flag
}

// GetFeatureFlags 从 Redis 获取特性开关配置
func GetFeatureFlags(ctx context.Context) ([]byte, error) {
    return GetKVData(ctx, GetFeatureFlagsPath())
}
```

### 4.2 Redis KV 获取实现

**文件**: `pkg/unify-query/redis/redis.go`

```go
var GetKVData = func(ctx context.Context, key string) ([]byte, error) {
    data, err := globalInstance.client.Get(ctx, key).Result()
    if err != nil {
        if errors.Is(err, goRedis.Nil) {
            // 若 Key 不存在，返回空数据
            return []byte("{}"), nil
        }
        return nil, fmt.Errorf("failed to get data from redis: %w", err)
    }
    return []byte(data), nil
}
```

**特性**:
- Key 不存在时返回空配置 `{}`，避免服务崩溃
- 错误处理完善，返回明确的错误信息

### 4.3 内存存储

**文件**: `pkg/unify-query/featureFlag/featureFlag.go`

```go
type FeatureFlag struct {
    lock  *sync.RWMutex  // 读写锁保护并发访问
    flags []byte         // JSON 格式的配置数据
}

func ReloadFeatureFlags(data []byte) error {
    if data == nil {
        return nil
    }
    featureFlag.lock.Lock()
    defer featureFlag.lock.Unlock()
    featureFlag.flags = data
    return nil
}
```

**特性**:
- 使用 `sync.RWMutex` 保护并发访问
- 配置以 JSON 格式存储在内存中，读取快速

## 5. 配置监听流程

### 5.1 启动监听

**方法**: `loopReloadFeatureFlags`

```go
func (s *Service) loopReloadFeatureFlags(ctx context.Context) error {
    // 1. 首次加载配置
    err := s.reloadFeatureFlags(ctx)
    
    // 2. 根据配置选择监听方式
    var ch <-chan any
    if DataSource == "consul" {
        ch, err = consul.WatchFeatureFlags(ctx)
    } else {
        // 默认使用 Redis Pub/Sub
        ch, err = redis.WatchFeatureFlags(ctx)
    }
    
    // 3. 启动监听循环
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        for {
            select {
            case <-ctx.Done():
                return
            case <-ch:
                // 收到变更通知，重新加载配置
                err = s.reloadFeatureFlags(ctx)
            }
        }
    }()
    return nil
}
```

### 5.2 Redis Pub/Sub 监听

**文件**: `pkg/unify-query/redis/featureFlag.go`

```go
// GetFeatureFlagsChannel 获取特性开关变更通知的 Redis channel
func GetFeatureFlagsChannel() string {
    return fmt.Sprintf("%s:%s", GetFeatureFlagsPath(), featureFlagChannel)
    // 返回: bkmonitorv3:unify-query:data:feature_flag:feature_flag_channel
}

// WatchFeatureFlags 监听特性开关变更，通过 Redis Pub/Sub 实现
func WatchFeatureFlags(ctx context.Context) (<-chan any, error) {
    return WatchChange(ctx, GetFeatureFlagsChannel())
}
```

### 5.3 WatchChange 实现

**文件**: `pkg/unify-query/redis/redis.go`

```go
var WatchChange = func(ctx context.Context, channel string) (<-chan any, error) {
    if globalInstance == nil {
        return nil, fmt.Errorf("redis client is not initialized")
    }
    
    msgChan := Subscribe(ctx, channel)
    
    // 转换为通用的 channel
    resultChan := make(chan any)
    go func() {
        defer close(resultChan)
        for {
            select {
            case <-ctx.Done():
                return
            case msg, ok := <-msgChan:
                if !ok {
                    return
                }
                // 非阻塞发送，避免阻塞
                select {
                case resultChan <- msg:
                case <-ctx.Done():
                    return
                default:
                    // 如果 resultChan 已满，记录日志但不阻塞
                }
            }
        }
    }()
    
    return resultChan, nil
}
```

**特性**:
- 使用非阻塞发送，避免 goroutine 阻塞
- 支持上下文取消，优雅退出
- 自动处理 channel 关闭

## 6. 配置更新流程

### 6.1 设置配置到 Redis

**文件**: `pkg/unify-query/redis/featureFlag.go`

```go
func SetFeatureFlags(ctx context.Context, data []byte) error {
    client := Client()
    if client == nil {
        return fmt.Errorf("redis client is not initialized")
    }
    
    key := GetFeatureFlagsPath()
    // 1. 设置 Key-Value
    err := client.Set(ctx, key, data, 0).Err()
    if err != nil {
        return fmt.Errorf("failed to set feature flags to redis: %w", err)
    }
    
    // 2. 发布变更通知（消息内容为完整配置）
    channel := GetFeatureFlagsChannel()
    err = client.Publish(ctx, channel, string(data)).Err()
    if err != nil {
        log.Errorf(ctx, "[redis] failed to publish feature flags change notification: %s", err)
        // 不返回错误，因为数据已经设置成功
    }
    
    return nil
}
```

**关键点**:
- 先设置 KV，再发布通知
- 发布的消息包含完整的配置内容，监听方可以直接使用
- Publish 失败不影响整体成功（数据已设置）

### 6.2 配置格式

特性开关配置采用 JSON 格式，示例：

```json
{
    "flag-1": {
        "variations": {
            "true": true,
            "false": false
        },
        "defaultRule": {
            "variation": "false"
        }
    },
    "flag-2": {
        "variations": {
            "A": "value-a",
            "B": "value-b",
            "C": "value-c"
        },
        "defaultRule": {
            "variation": "A"
        },
        "rules": [
            {
                "name": "rule for space",
                "variation": "B",
                "query": "spaceUid == \"bkcc__2\""
            }
        ]
    },
    "flag-3": {
        "variations": {
            "0": 0,
            "10": 10,
            "100": 100
        },
        "defaultRule": {
            "variation": "0"
        }
    }
}
```

**配置说明**:
- `variations`: 定义特性开关的所有可能值
- `defaultRule`: 默认规则，当没有匹配的规则时使用
- `rules`: 自定义规则列表，支持条件查询（如 `spaceUid == "bkcc__2"`）

## 7. 配置使用流程

### 7.1 go-feature-flag 获取配置

**文件**: `pkg/unify-query/featureFlag/custom.go`

```go
type CustomRetriever struct{}

func (s *CustomRetriever) Retrieve(_ context.Context) ([]byte, error) {
    return getFeatureFlags(), nil  // 从内存读取
}
```

`go-feature-flag` 通过 `CustomRetriever` 从内存获取配置，而不是直接从 Redis 读取，提高性能。

### 7.2 特性开关评估

**文件**: `pkg/unify-query/featureFlag/featureFlag.go`

```go
// 字符串类型特性开关
func StringVariation(ctx context.Context, user ffuser.User, flagKey string, defaultValue string) string {
    res, err := ffclient.StringVariation(flagKey, user, defaultValue)
    if err != nil {
        return defaultValue
    }
    return res
}

// 布尔类型特性开关
func BoolVariation(ctx context.Context, user ffuser.User, flagKey string, defaultValue bool) bool {
    res, err := ffclient.BoolVariation(flagKey, user, defaultValue)
    if err != nil {
        return defaultValue
    }
    return res
}

// 整数类型特性开关
func IntVariation(ctx context.Context, user ffuser.User, flagKey string, defaultValue int) int {
    res, err := ffclient.IntVariation(flagKey, user, defaultValue)
    if err != nil {
        return defaultValue
    }
    return res
}
```

### 7.3 使用示例

```go
// 创建用户上下文
ffUser := featureFlag.FFUser("user-123", map[string]any{
    "spaceUid": "bkcc__2",
    "user_id":  "123",
})

// 获取字符串类型特性开关
value := featureFlag.StringVariation(ctx, ffUser, "flag-2", "default")

// 获取布尔类型特性开关
enabled := featureFlag.BoolVariation(ctx, ffUser, "flag-1", false)

// 获取整数类型特性开关
count := featureFlag.IntVariation(ctx, ffUser, "flag-3", 0)
```

## 8. 关键组件说明

### 8.1 Redis Key 和 Channel

| 组件 | 格式 | 示例 |
|------|------|------|
| 配置 Key | `{basePath}:{dataPath}:{featureFlagPath}` | `bkmonitorv3:unify-query:data:feature_flag` |
| 通知 Channel | `{配置Key}:{channelSuffix}` | `bkmonitorv3:unify-query:data:feature_flag:feature_flag_channel` |

**说明**:
- 使用冒号（`:`）作为分隔符，符合 Redis 命名规范
- Key 和 Channel 路径清晰，便于管理和调试

### 8.2 数据流

```
1. Redis SET 配置
   ↓
2. Redis PUBLISH 变更通知（包含完整配置）
   ↓
3. 监听循环接收通知
   ↓
4. 重新加载配置（从 Redis 获取或使用消息中的配置）
   ↓
5. 更新内存缓存
   ↓
6. go-feature-flag 从内存读取
   ↓
7. 业务代码使用特性开关
```

### 8.3 核心特性

1. **实时更新**: 通过 Redis Pub/Sub 实现配置变更实时通知，无需轮询
2. **内存缓存**: 配置加载到内存，读取性能高
3. **线程安全**: 使用 `sync.RWMutex` 保护并发访问
4. **容错处理**: Key 不存在时返回空配置 `{}`，避免服务崩溃
5. **配置验证**: 异常配置自动回退到默认值（redis）
6. **双数据源支持**: 支持 Redis 和 Consul 两种数据源，可灵活切换

## 9. 操作指南

### 9.1 设置配置（通过 Redis CLI）

```bash
# 设置特性开关配置
redis-cli SET "bkmonitorv3:unify-query:data:feature_flag" '{
    "flag-1": {
        "variations": {
            "true": true,
            "false": false
        },
        "defaultRule": {
            "variation": "false"
        }
    }
}'

# 发布变更通知（消息内容为完整配置）
redis-cli PUBLISH "bkmonitorv3:unify-query:data:feature_flag:feature_flag_channel" '{
    "flag-1": {
        "variations": {
            "true": true,
            "false": false
        },
        "defaultRule": {
            "variation": "false"
        }
    }
}'
```

**注意**: 实际使用中，`SetFeatureFlags` 函数会自动完成 SET 和 PUBLISH 两个操作。

### 9.2 HTTP 接口

**文件**: `pkg/unify-query/service/http/info.go`

```bash
# 查看当前配置（默认从 redis）
curl http://localhost:10205/ff

# 强制刷新（从 redis）
curl http://localhost:10205/ff?r=1

# 指定从 redis 刷新
curl http://localhost:10205/ff?r=1&source=redis

# 指定从 consul 刷新
curl http://localhost:10205/ff?r=1&source=consul

# 测试特性开关（检查 flag-2，用户属性 spaceUid=bkcc__2）
curl "http://localhost:10205/ff?c=flag-2&k=spaceUid&v=bkcc__2"
```

### 9.3 检查配置状态

```bash
# 检查 Redis Key 是否存在
redis-cli EXISTS "bkmonitorv3:unify-query:data:feature_flag"

# 获取配置内容
redis-cli GET "bkmonitorv3:unify-query:data:feature_flag"

# 检查 Channel 订阅数
redis-cli PUBSUB NUMSUB "bkmonitorv3:unify-query:data:feature_flag:feature_flag_channel"
```

## 10. 与 Consul 实现的对比

### 10.1 相同点

1. 都支持动态配置更新，无需重启服务
2. 都使用 `go-feature-flag` 库进行特性开关评估
3. 都支持内存缓存，提高读取性能
4. 都支持 HTTP 接口查看和刷新配置

### 10.2 不同点

| 特性 | Consul | Redis |
|------|--------|-------|
| 存储方式 | KV Store | KV Store |
| 变更通知 | Watch API | Pub/Sub |
| Key 格式 | 路径格式（`/` 分隔） | Key 格式（`:` 分隔） |
| 消息内容 | 仅通知，需重新获取 | 包含完整配置 |
| 默认数据源 | 否 | 是 |

### 10.3 选择建议

- **使用 Redis**: 
  - 已有 Redis 基础设施
  - 需要更高的性能
  - 消息中包含完整配置，减少一次 GET 操作
  
- **使用 Consul**:
  - 已有 Consul 基础设施
  - 需要服务发现和配置管理一体化
  - 更偏向微服务架构

## 11. 故障排查

### 11.1 配置未生效

**检查项**:
1. 确认 Redis 连接正常
2. 检查 Key 是否存在: `redis-cli EXISTS "bkmonitorv3:unify-query:data:feature_flag"`
3. 检查配置格式是否正确（JSON 格式）
4. 查看服务日志，确认是否有错误信息
5. 通过 HTTP 接口强制刷新: `curl http://localhost:10205/ff?r=1`

### 11.2 变更通知未收到

**检查项**:
1. 确认 PUBLISH 操作成功: `redis-cli PUBSUB NUMSUB "bkmonitorv3:unify-query:data:feature_flag:feature_flag_channel"`
2. 检查服务是否正常订阅 Channel
3. 查看服务日志，确认监听循环是否正常运行
4. 确认 Redis 连接未断开

### 11.3 配置读取失败

**检查项**:
1. 确认 Redis 客户端已初始化
2. 检查 Redis 连接配置是否正确
3. 查看错误日志，确认具体错误信息
4. 验证 Key 路径是否正确

## 12. 测试

### 12.1 单元测试

**文件**: `pkg/unify-query/redis/featureFlag_test.go`

测试覆盖:
- `GetFeatureFlagsPath`: 路径生成测试
- `GetFeatureFlagsChannel`: Channel 路径生成测试
- `GetFeatureFlags`: 配置获取测试（正常、不存在、失败、空配置）
- `WatchFeatureFlags`: 监听功能测试（正常、失败、上下文取消）
- `SetFeatureFlags`: 设置配置测试（使用 miniredis 真实 Redis 实例）

### 12.2 集成测试

测试完整的特性开关流程:
1. 设置配置到 Redis
2. 验证配置已存储
3. 启动监听
4. 发布变更通知
5. 验证配置已更新
6. 验证特性开关评估正确

## 13. 总结

利用 Redis 做特性开关的核心优势:

1. **高性能**: Redis 内存存储，读写速度快
2. **实时性**: Pub/Sub 机制实现毫秒级配置更新
3. **可靠性**: 完善的错误处理和容错机制
4. **灵活性**: 支持双数据源，可灵活切换
5. **易用性**: 简单的 API 接口，便于操作和调试

该实现与 Consul 版本保持一致的设计模式，支持两种数据源无缝切换，默认使用 Redis，为系统提供了高性能、高可用的特性开关管理能力。

