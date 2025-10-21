# AI Agent for Unify-Query

这是一个为 Unify-Query 项目设计的 AI Agent 实现，能够将自然语言查询转换为结构化的监控查询。

## 功能特性

### 1. 自然语言处理
- **意图识别**：识别用户查询的意图（CPU使用率、内存使用率、网络流量等）
- **实体提取**：提取查询中的关键实体（指标、服务器、时间范围等）
- **查询构建**：将自然语言转换为结构化查询

### 2. 智能优化
- **查询优化**：自动优化查询性能
- **时间范围优化**：自动调整合理的时间范围
- **聚合函数优化**：选择最优的聚合方式

### 3. 学习能力
- **用户偏好学习**：记录用户查询历史和偏好
- **反馈学习**：从用户反馈中改进查询质量
- **查询模式学习**：识别常见查询模式

### 4. 多种LLM支持
- OpenAI (GPT-3.5/GPT-4)
- Claude (Anthropic)
- 本地模型 (Ollama等)

## 文件结构

```
ai_agent/
├── agent.go              # AI代理核心接口和实现
├── llm_client.go         # LLM客户端实现
├── nlp_processor.go      # 自然语言处理器
├── intent_classifier.go  # 意图分类器
├── entity_extractor.go   # 实体提取器
├── query_builder.go      # 查询构建器
├── query_optimizer.go    # 查询优化器
├── knowledge_base.go     # 知识库
├── config.go            # 配置管理
├── http_handler.go      # HTTP处理器
├── example_usage.go     # 使用示例
└── README.md            # 本文档
```

## 快速开始

### 1. 创建配置

```go
config := &AIAgentConfig{
    LLMConfig: &LLMConfig{
        Provider:    "openai",
        APIKey:      "your-api-key",
        BaseURL:     "https://api.openai.com",
        Model:       "gpt-3.5-turbo",
        MaxTokens:   1000,
        Temperature: 0.7,
        Timeout:     30 * time.Second,
    },
    QueryTimeout:         30 * time.Second,
    MaxConcurrentQueries: 10,
}
```

### 2. 创建AI代理

```go
agent, err := NewUnifyQueryAIAgent(config)
if err != nil {
    log.Fatal(err)
}
```

### 3. 处理自然语言查询

```go
request := &NaturalLanguageQueryRequest{
    Query:    "显示CPU使用率最高的10台服务器",
    SpaceUID: "space123",
    TimeRange: &TimeRange{
        Start: "1h",
        End:   "now",
        Step:  "1m",
    },
}

response, err := agent.ProcessNaturalLanguageQuery(ctx, request)
```

## API 接口

### HTTP 端点

- `POST /ai/query` - 自然语言查询
- `POST /ai/suggestions` - 获取查询建议
- `POST /ai/explain` - 解释查询结果
- `POST /ai/feedback` - 提交用户反馈
- `GET /ai/health` - 健康检查

### 查询示例

```bash
# 自然语言查询
curl -X POST http://localhost:8080/ai/query \
  -H "Content-Type: application/json" \
  -d '{
    "query": "显示CPU使用率最高的10台服务器",
    "space_uid": "space123",
    "time_range": {
      "start": "1h",
      "end": "now",
      "step": "1m"
    }
  }'
```

## 支持的查询类型

### 1. 资源使用查询
- "显示CPU使用率"
- "查询内存占用情况"
- "磁盘使用率统计"
- "网络流量分析"

### 2. Top N 查询
- "CPU使用率最高的10台服务器"
- "内存占用前5的服务器"
- "流量最大的服务器"

### 3. 趋势分析
- "CPU使用率趋势"
- "内存使用变化"
- "网络流量增长趋势"

### 4. 对比分析
- "不同业务线的CPU使用率对比"
- "各服务器性能比较"

### 5. 告警分析
- "最近的告警信息"
- "告警频率统计"
- "异常服务器列表"

## 配置说明

### LLM 配置

```yaml
llm:
  provider: "openai"  # openai, claude, local
  api_key: "your-api-key"
  base_url: "https://api.openai.com"
  model: "gpt-3.5-turbo"
  max_tokens: 1000
  temperature: 0.7
  timeout: 30s
```

### 缓存配置

```yaml
cache:
  enabled: true
  ttl: 5m
  max_size: 1000
  type: memory  # memory, redis
```

### 学习配置

```yaml
learning:
  enabled: true
  learning_rate: 0.01
  min_samples: 10
  learning_interval: 1h
  model_path: "./models"
```

## 注意事项

1. **API密钥安全**：请妥善保管LLM API密钥，不要提交到代码仓库
2. **成本控制**：LLM调用会产生费用，建议设置合理的缓存和限流策略
3. **性能优化**：对于高频查询，建议启用缓存
4. **错误处理**：LLM可能无法理解所有查询，需要有降级方案

## 扩展开发

### 添加新的意图

在 `intent_classifier.go` 中添加新的意图定义：

```go
"custom_intent": {
    Name:        "自定义意图",
    Description: "描述",
    Keywords:    []string{"关键词1", "关键词2"},
    Examples:    []string{"示例1", "示例2"},
}
```

### 添加新的实体类型

在 `entity_extractor.go` 中添加提取逻辑：

```go
func (ee *EntityExtractor) extractCustomEntity(query string) []string {
    // 实现提取逻辑
}
```

## 故障排查

### 常见问题

1. **LLM连接失败**
   - 检查API密钥是否正确
   - 检查网络连接
   - 检查BaseURL配置

2. **查询转换失败**
   - 检查查询是否包含足够的信息
   - 查看日志了解解析过程
   - 尝试更明确的查询表达

3. **性能问题**
   - 启用缓存
   - 调整LLM超时时间
   - 减少并发查询数

## 贡献

欢迎提交Issue和Pull Request！

## 许可证

MIT License

