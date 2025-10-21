// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ai_agent

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// ExampleUsage 示例用法
func ExampleUsage() {
	// 1. 创建配置
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
		MetadataService:      nil, // 这里需要传入实际的元数据服务
		Logger:               logrus.New(),
		QueryTimeout:         30 * time.Second,
		MaxConcurrentQueries: 10,
	}

	// 2. 创建AI代理
	agent, err := NewUnifyQueryAIAgent(config)
	if err != nil {
		log.Fatalf("Failed to create AI agent: %v", err)
	}

	// 3. 创建查询上下文
	ctx := &QueryContext{
		UserID: "user123",
		PreviousQueries: []QueryRecord{
			{
				Query:     "显示CPU使用率",
				Timestamp: time.Now().Add(-1 * time.Hour),
				Success:   true,
				Duration:  1000,
			},
		},
		UserPreferences: make(map[string]any),
		AvailableMetrics: []MetricInfo{
			{
				Name:        "cpu_usage",
				Description: "CPU使用率",
				TableID:     "system.cpu_summary",
				Type:        "gauge",
				Tags:        []string{"host", "cpu"},
			},
			{
				Name:        "memory_usage",
				Description: "内存使用率",
				TableID:     "system.memory_summary",
				Type:        "gauge",
				Tags:        []string{"host"},
			},
		},
		AvailableTables: []TableInfo{
			{
				ID:          "system.cpu_summary",
				Name:        "CPU汇总表",
				Description: "系统CPU使用情况汇总",
				Metrics:     []string{"cpu_usage", "cpu_load"},
				Tags:        []string{"host", "cpu"},
			},
		},
	}

	// 4. 处理自然语言查询
	queryRequest := &NaturalLanguageQueryRequest{
		Query:    "显示CPU使用率最高的10台服务器",
		SpaceUID: "space123",
		Context:  ctx,
		TimeRange: &TimeRange{
			Start: "1h",
			End:   "now",
			Step:  "1m",
		},
	}

	response, err := agent.ProcessNaturalLanguageQuery(context.Background(), queryRequest)
	if err != nil {
		log.Fatalf("Failed to process query: %v", err)
	}

	fmt.Printf("Query processed successfully!\n")
	fmt.Printf("Explanation: %s\n", response.Explanation)
	fmt.Printf("Suggestions: %v\n", response.Suggestions)

	// 5. 获取查询建议
	suggestions, err := agent.GenerateQuerySuggestions(context.Background(), ctx)
	if err != nil {
		log.Printf("Failed to generate suggestions: %v", err)
	} else {
		fmt.Printf("Generated suggestions: %v\n", suggestions)
	}

	// 6. 解释查询结果
	queryResult := &QueryResult{
		Data:        map[string]any{"series": []any{}},
		Query:       "cpu_usage",
		Duration:    500,
		SeriesCount: 10,
		PointsCount: 100,
	}

	explanation, err := agent.ExplainQueryResult(context.Background(), queryResult)
	if err != nil {
		log.Printf("Failed to explain result: %v", err)
	} else {
		fmt.Printf("Result explanation: %s\n", explanation)
	}

	// 7. 提交用户反馈
	feedback := &UserFeedback{
		QueryID:     "query123",
		UserID:      "user123",
		Rating:      4,
		Comments:    "查询结果很有用",
		WasHelpful:  true,
		Suggestions: "希望能提供更多的时间范围选项",
	}

	err = agent.LearnFromFeedback(context.Background(), feedback)
	if err != nil {
		log.Printf("Failed to submit feedback: %v", err)
	} else {
		fmt.Println("Feedback submitted successfully!")
	}
}

// ExampleHTTPUsage HTTP使用示例
func ExampleHTTPUsage() {
	// 1. 创建配置
	config := DefaultAIAgentConfig()
	config.LLMConfig.APIKey = "your-api-key"

	// 2. 创建AI代理
	agent, err := NewUnifyQueryAIAgent(config)
	if err != nil {
		log.Fatalf("Failed to create AI agent: %v", err)
	}

	// 3. 创建HTTP处理器
	_ = NewAIAgentHandler(agent, config.Logger)

	// 4. 注册路由
	// router := gin.Default()
	// handler.RegisterRoutes(router)
	// router.Run(":8080")

	fmt.Println("HTTP server would be started on :8080")
	fmt.Println("Available endpoints:")
	fmt.Println("  POST /ai/query - 使用AI进行查询")
	fmt.Println("  POST /ai/suggestions - 获取查询建议")
	fmt.Println("  POST /ai/explain - 解释查询结果")
	fmt.Println("  POST /ai/feedback - 提交用户反馈")
	fmt.Println("  GET /ai/health - 健康检查")
}

// ExampleQueryRequests 示例查询请求
func ExampleQueryRequests() {
	// 示例1: CPU使用率查询
	cpuQuery := &NaturalLanguageQueryRequest{
		Query:    "显示CPU使用率最高的10台服务器",
		SpaceUID: "space123",
		TimeRange: &TimeRange{
			Start: "1h",
			End:   "now",
			Step:  "1m",
		},
	}

	// 示例2: 内存使用率查询
	memoryQuery := &NaturalLanguageQueryRequest{
		Query:    "查询最近1天的内存使用情况",
		SpaceUID: "space123",
		TimeRange: &TimeRange{
			Start: "1d",
			End:   "now",
			Step:  "5m",
		},
	}

	// 示例3: 网络流量查询
	networkQuery := &NaturalLanguageQueryRequest{
		Query:    "分析网络流量趋势",
		SpaceUID: "space123",
		TimeRange: &TimeRange{
			Start: "7d",
			End:   "now",
			Step:  "1h",
		},
	}

	// 示例4: 错误率查询
	errorQuery := &NaturalLanguageQueryRequest{
		Query:    "显示错误率最高的应用",
		SpaceUID: "space123",
		TimeRange: &TimeRange{
			Start: "1h",
			End:   "now",
			Step:  "1m",
		},
	}

	// 示例5: 响应时间查询
	responseTimeQuery := &NaturalLanguageQueryRequest{
		Query:    "查询API响应时间最慢的接口",
		SpaceUID: "space123",
		TimeRange: &TimeRange{
			Start: "1h",
			End:   "now",
			Step:  "1m",
		},
	}

	fmt.Printf("CPU Query: %+v\n", cpuQuery)
	fmt.Printf("Memory Query: %+v\n", memoryQuery)
	fmt.Printf("Network Query: %+v\n", networkQuery)
	fmt.Printf("Error Query: %+v\n", errorQuery)
	fmt.Printf("Response Time Query: %+v\n", responseTimeQuery)
}

// ExampleResponses 示例响应
func ExampleResponses() {
	// 示例查询响应
	queryResponse := &QueryResponse{
		Success: true,
		StructuredQuery: &structured.QueryTs{
			SpaceUid: "space123",
			QueryList: []*structured.Query{
				{
					TableID:       "system.cpu_summary",
					FieldName:     "usage",
					ReferenceName: "a",
				},
			},
			MetricMerge: "a",
			Start:       "1h",
			End:         "now",
			Step:        "1m",
		},
		Explanation: "此查询将显示CPU使用率最高的10台服务器，使用5分钟时间窗口进行平均聚合，查询最近1小时的数据。",
		Suggestions: []string{
			"可以调整时间范围查看更长时间的趋势",
			"可以添加业务维度进行分组分析",
			"可以设置告警阈值进行监控",
		},
		Metadata: map[string]any{
			"intent":          "cpu_usage",
			"confidence":      0.95,
			"processing_time": 150,
		},
		TraceID: "trace123",
	}

	fmt.Printf("Query Response: %+v\n", queryResponse)
}

// ExampleIntegration 集成示例
func ExampleIntegration() {
	// 这个函数展示了如何将AI代理集成到现有的unify-query系统中

	// 1. 在现有的HTTP处理器中添加AI路由
	// 2. 在查询处理流程中集成AI代理
	// 3. 在元数据服务中提供指标和表信息
	// 4. 在日志系统中记录AI查询

	fmt.Println("Integration example:")
	fmt.Println("1. Add AI routes to existing HTTP handler")
	fmt.Println("2. Integrate AI agent into query processing flow")
	fmt.Println("3. Provide metric and table info from metadata service")
	fmt.Println("4. Log AI queries in logging system")
}
