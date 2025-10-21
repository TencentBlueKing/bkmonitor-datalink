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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// AIAgent 智能查询代理接口
type AIAgent interface {
	// ProcessNaturalLanguageQuery 处理自然语言查询
	ProcessNaturalLanguageQuery(ctx context.Context, req *NaturalLanguageQueryRequest) (*QueryResponse, error)
	// GenerateQuerySuggestions 生成查询建议
	GenerateQuerySuggestions(ctx context.Context, context *QueryContext) ([]string, error)
	// ExplainQueryResult 解释查询结果
	ExplainQueryResult(ctx context.Context, result *QueryResult) (string, error)
	// LearnFromFeedback 从用户反馈中学习
	LearnFromFeedback(ctx context.Context, feedback *UserFeedback) error
}

// NaturalLanguageQueryRequest 自然语言查询请求
type NaturalLanguageQueryRequest struct {
	Query     string        `json:"query"`      // 自然语言查询
	SpaceUID  string        `json:"space_uid"`  // 空间ID
	Context   *QueryContext `json:"context"`    // 查询上下文
	TimeRange *TimeRange    `json:"time_range"` // 时间范围
}

// QueryContext 查询上下文
type QueryContext struct {
	UserID           string         `json:"user_id"`
	PreviousQueries  []QueryRecord  `json:"previous_queries"`
	UserPreferences  map[string]any `json:"user_preferences"`
	AvailableMetrics []MetricInfo   `json:"available_metrics"`
	AvailableTables  []TableInfo    `json:"available_tables"`
}

// QueryRecord 查询记录
type QueryRecord struct {
	Query     string    `json:"query"`
	Timestamp time.Time `json:"timestamp"`
	Success   bool      `json:"success"`
	Duration  int64     `json:"duration_ms"`
}

// MetricInfo 指标信息
type MetricInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	TableID     string   `json:"table_id"`
	Type        string   `json:"type"`
	Tags        []string `json:"tags"`
}

// TableInfo 表信息
type TableInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Metrics     []string `json:"metrics"`
	Tags        []string `json:"tags"`
}

// TimeRange 时间范围
type TimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Step  string `json:"step,omitempty"`
}

// QueryResponse 查询响应
type QueryResponse struct {
	Success         bool                `json:"success"`
	Data            any                 `json:"data"`
	StructuredQuery *structured.QueryTs `json:"structured_query"`
	Explanation     string              `json:"explanation"`
	Suggestions     []string            `json:"suggestions"`
	Metadata        map[string]any      `json:"metadata"`
	TraceID         string              `json:"trace_id"`
}

// QueryResult 查询结果
type QueryResult struct {
	Data        any    `json:"data"`
	Query       string `json:"query"`
	Duration    int64  `json:"duration_ms"`
	SeriesCount int    `json:"series_count"`
	PointsCount int    `json:"points_count"`
}

// UserFeedback 用户反馈
type UserFeedback struct {
	QueryID     string `json:"query_id"`
	UserID      string `json:"user_id"`
	Rating      int    `json:"rating"` // 1-5
	Comments    string `json:"comments"`
	WasHelpful  bool   `json:"was_helpful"`
	Suggestions string `json:"suggestions"`
}

// UnifyQueryAIAgent 统一查询AI代理实现
type UnifyQueryAIAgent struct {
	// LLM客户端
	llmClient LLMClient
	// 自然语言处理器
	nlpProcessor *NLPProcessor
	// 知识库
	knowledgeBase *KnowledgeBase
	// 查询构建器
	queryBuilder *QueryBuilder
	// 查询优化器
	queryOptimizer *QueryOptimizer
	// 元数据服务
	metadataService any
	// 日志记录器
	logger *logrus.Logger
}

// NewUnifyQueryAIAgent 创建新的AI代理实例
func NewUnifyQueryAIAgent(config *AIAgentConfig) (*UnifyQueryAIAgent, error) {
	// 初始化LLM客户端
	llmClient, err := NewLLMClient(config.LLMConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create LLM client")
	}

	// 初始化自然语言处理器
	nlpProcessor := NewNLPProcessor(llmClient)

	// 初始化知识库
	knowledgeBase := NewKnowledgeBase()

	// 初始化查询构建器
	queryBuilder := NewQueryBuilder(knowledgeBase)

	// 初始化查询优化器
	queryOptimizer := NewQueryOptimizer(knowledgeBase)

	return &UnifyQueryAIAgent{
		llmClient:       llmClient,
		nlpProcessor:    nlpProcessor,
		knowledgeBase:   knowledgeBase,
		queryBuilder:    queryBuilder,
		queryOptimizer:  queryOptimizer,
		metadataService: config.MetadataService,
		logger:          config.Logger,
	}, nil
}

// ProcessNaturalLanguageQuery 处理自然语言查询
func (agent *UnifyQueryAIAgent) ProcessNaturalLanguageQuery(ctx context.Context, req *NaturalLanguageQueryRequest) (*QueryResponse, error) {
	ctx, span := trace.NewSpan(ctx, "ai-agent-process-natural-query")
	defer span.End(nil)

	agent.logger.WithFields(logrus.Fields{
		"query":     req.Query,
		"space_uid": req.SpaceUID,
		"user_id":   req.Context.UserID,
	}).Info("Processing natural language query")

	// 1. 解析自然语言查询
	parsedQuery, err := agent.nlpProcessor.ParseQuery(ctx, req.Query, req.Context)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse natural language query")
	}

	span.Set("parsed_intent", parsedQuery.Intent)
	span.Set("parsed_entities", parsedQuery.Entities)

	// 2. 构建结构化查询
	structuredQuery, err := agent.queryBuilder.BuildQuery(ctx, parsedQuery, req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build structured query")
	}

	// 3. 优化查询
	optimizedQuery, err := agent.queryOptimizer.Optimize(ctx, structuredQuery, req.Context)
	if err != nil {
		agent.logger.WithError(err).Warn("Failed to optimize query, using original")
		optimizedQuery = structuredQuery
	}

	// 4. 生成解释
	explanation, err := agent.generateExplanation(ctx, parsedQuery, optimizedQuery)
	if err != nil {
		agent.logger.WithError(err).Warn("Failed to generate explanation")
		explanation = "查询已生成，但无法提供详细解释"
	}

	// 5. 生成建议
	suggestions, err := agent.GenerateQuerySuggestions(ctx, req.Context)
	if err != nil {
		agent.logger.WithError(err).Warn("Failed to generate suggestions")
		suggestions = []string{}
	}

	response := &QueryResponse{
		Success:         true,
		StructuredQuery: optimizedQuery,
		Explanation:     explanation,
		Suggestions:     suggestions,
		Metadata: map[string]any{
			"intent":          parsedQuery.Intent,
			"entities":        parsedQuery.Entities,
			"confidence":      parsedQuery.Confidence,
			"processing_time": time.Since(time.Now()).Milliseconds(),
		},
		TraceID: span.TraceID(),
	}

	// 6. 记录查询到知识库
	agent.knowledgeBase.RecordQuery(ctx, req.Context.UserID, req.Query, optimizedQuery, true)

	return response, nil
}

// GenerateQuerySuggestions 生成查询建议
func (agent *UnifyQueryAIAgent) GenerateQuerySuggestions(ctx context.Context, context *QueryContext) ([]string, error) {
	ctx, span := trace.NewSpan(ctx, "ai-agent-generate-suggestions")
	defer span.End(nil)

	// 基于用户历史查询生成建议
	suggestions := []string{}

	// 1. 基于最近查询的建议
	if len(context.PreviousQueries) > 0 {
		recentQueries := context.PreviousQueries
		if len(recentQueries) > 5 {
			recentQueries = recentQueries[len(recentQueries)-5:]
		}

		for _, query := range recentQueries {
			if query.Success {
				suggestions = append(suggestions, fmt.Sprintf("重复查询: %s", query.Query))
			}
		}
	}

	// 2. 基于可用指标的建议
	for _, metric := range context.AvailableMetrics {
		suggestions = append(suggestions, fmt.Sprintf("查询 %s 指标", metric.Name))
	}

	// 3. 基于常见查询模式的建议
	commonPatterns := []string{
		"显示CPU使用率最高的10台服务器",
		"查询最近1小时的网络流量",
		"统计各业务线的错误率",
		"分析数据库连接数趋势",
	}

	suggestions = append(suggestions, commonPatterns...)

	// 4. 使用LLM生成个性化建议
	if agent.llmClient != nil {
		llmSuggestions, err := agent.generateLLMSuggestions(ctx, context)
		if err == nil {
			suggestions = append(suggestions, llmSuggestions...)
		}
	}

	// 去重并限制数量
	uniqueSuggestions := make([]string, 0, len(suggestions))
	seen := make(map[string]bool)
	for _, suggestion := range suggestions {
		if !seen[suggestion] && len(uniqueSuggestions) < 10 {
			uniqueSuggestions = append(uniqueSuggestions, suggestion)
			seen[suggestion] = true
		}
	}

	return uniqueSuggestions, nil
}

// ExplainQueryResult 解释查询结果
func (agent *UnifyQueryAIAgent) ExplainQueryResult(ctx context.Context, result *QueryResult) (string, error) {
	ctx, span := trace.NewSpan(ctx, "ai-agent-explain-result")
	defer span.End(nil)

	if agent.llmClient == nil {
		return "查询结果已返回，包含 " + fmt.Sprintf("%d 个时间序列，%d 个数据点", result.SeriesCount, result.PointsCount), nil
	}

	// 构建解释提示
	prompt := fmt.Sprintf(`
请解释以下监控查询结果：

查询语句: %s
执行时间: %d 毫秒
时间序列数量: %d
数据点数量: %d

请用通俗易懂的语言解释这个查询结果的含义和可能的问题。
`, result.Query, result.Duration, result.SeriesCount, result.PointsCount)

	// 调用LLM生成解释
	response, err := agent.llmClient.ChatCompletion(ctx, []Message{
		{
			Role:    "system",
			Content: "你是一个监控数据专家，请用通俗易懂的语言解释查询结果",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	})
	if err != nil {
		return "查询结果已返回，但无法生成详细解释", err
	}

	return response.Content, nil
}

// LearnFromFeedback 从用户反馈中学习
func (agent *UnifyQueryAIAgent) LearnFromFeedback(ctx context.Context, feedback *UserFeedback) error {
	ctx, span := trace.NewSpan(ctx, "ai-agent-learn-feedback")
	defer span.End(nil)

	agent.logger.WithFields(logrus.Fields{
		"query_id":    feedback.QueryID,
		"user_id":     feedback.UserID,
		"rating":      feedback.Rating,
		"was_helpful": feedback.WasHelpful,
	}).Info("Learning from user feedback")

	// 1. 更新用户偏好
	err := agent.knowledgeBase.UpdateUserPreference(ctx, feedback.UserID, feedback)
	if err != nil {
		return errors.Wrap(err, "failed to update user preference")
	}

	// 2. 如果评分较低，标记查询为问题查询
	if feedback.Rating < 3 {
		err = agent.knowledgeBase.MarkQueryAsProblematic(ctx, feedback.QueryID)
		if err != nil {
			agent.logger.WithError(err).Warn("Failed to mark query as problematic")
		}
	}

	// 3. 如果有建议，更新查询模式
	if feedback.Suggestions != "" {
		err = agent.knowledgeBase.UpdateQueryPattern(ctx, feedback.QueryID, feedback.Suggestions)
		if err != nil {
			agent.logger.WithError(err).Warn("Failed to update query pattern")
		}
	}

	return nil
}

// generateExplanation 生成查询解释
func (agent *UnifyQueryAIAgent) generateExplanation(ctx context.Context, parsedQuery *ParsedQuery, structuredQuery *structured.QueryTs) (string, error) {
	if agent.llmClient == nil {
		return fmt.Sprintf("已解析查询意图: %s，生成了结构化查询", parsedQuery.Intent), nil
	}

	// 构建解释提示
	queryJSON, _ := json.MarshalIndent(structuredQuery, "", "  ")
	prompt := fmt.Sprintf(`
请解释以下监控查询：

原始查询: %s
解析意图: %s
实体信息: %v
置信度: %.2f

生成的结构化查询:
%s

请用简洁的语言解释这个查询的作用和预期结果。
`, parsedQuery.OriginalQuery, parsedQuery.Intent, parsedQuery.Entities, parsedQuery.Confidence, string(queryJSON))

	// 调用LLM生成解释
	response, err := agent.llmClient.ChatCompletion(ctx, []Message{
		{
			Role:    "system",
			Content: "你是一个监控数据专家，请简洁地解释查询的作用",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	})
	if err != nil {
		return fmt.Sprintf("已解析查询意图: %s", parsedQuery.Intent), err
	}

	return response.Content, nil
}

// generateLLMSuggestions 使用LLM生成个性化建议
func (agent *UnifyQueryAIAgent) generateLLMSuggestions(ctx context.Context, context *QueryContext) ([]string, error) {
	// 构建用户上下文
	userContext := fmt.Sprintf("用户ID: %s", context.UserID)
	if len(context.PreviousQueries) > 0 {
		userContext += fmt.Sprintf("\n最近查询: %v", context.PreviousQueries)
	}
	if len(context.AvailableMetrics) > 0 {
		userContext += fmt.Sprintf("\n可用指标: %v", context.AvailableMetrics)
	}

	prompt := fmt.Sprintf(`
基于以下用户上下文，生成5个个性化的监控查询建议：

%s

请生成具体、实用的查询建议，每个建议一行。
`, userContext)

	response, err := agent.llmClient.ChatCompletion(ctx, []Message{
		{
			Role:    "system",
			Content: "你是一个监控数据专家，请基于用户上下文生成个性化的查询建议",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	})
	if err != nil {
		return nil, err
	}

	// 解析建议
	suggestions := strings.Split(strings.TrimSpace(response.Content), "\n")
	var result []string
	for _, suggestion := range suggestions {
		suggestion = strings.TrimSpace(suggestion)
		if suggestion != "" {
			result = append(result, suggestion)
		}
	}

	return result, nil
}
