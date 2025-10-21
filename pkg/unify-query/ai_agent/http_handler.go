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
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// AIAgentHandler AI代理HTTP处理器
type AIAgentHandler struct {
	agent  AIAgent
	logger *logrus.Logger
}

// NewAIAgentHandler 创建AI代理HTTP处理器
func NewAIAgentHandler(agent AIAgent, logger *logrus.Logger) *AIAgentHandler {
	return &AIAgentHandler{
		agent:  agent,
		logger: logger,
	}
}

// RegisterRoutes 注册路由
func (h *AIAgentHandler) RegisterRoutes(router *gin.Engine) {
	// AI查询相关路由
	aiGroup := router.Group("/ai")
	{
		aiGroup.POST("/query", h.QueryWithAI)
		aiGroup.POST("/suggestions", h.GetSuggestions)
		aiGroup.POST("/explain", h.ExplainResult)
		aiGroup.POST("/feedback", h.SubmitFeedback)
		aiGroup.GET("/health", h.HealthCheck)
	}
}

// QueryWithAI 使用AI进行查询
func (h *AIAgentHandler) QueryWithAI(c *gin.Context) {
	var req NaturalLanguageQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.WithError(err).Error("Failed to bind request")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}

	// 设置默认上下文
	if req.Context == nil {
		req.Context = &QueryContext{
			UserID:          c.GetHeader("X-User-ID"),
			PreviousQueries: []QueryRecord{},
			UserPreferences: make(map[string]any),
		}
	}

	// 设置默认时间范围
	if req.TimeRange == nil {
		req.TimeRange = &TimeRange{
			Start: "1h",
			End:   "now",
			Step:  "1m",
		}
	}

	// 设置空间ID
	if req.SpaceUID == "" {
		req.SpaceUID = c.GetHeader("X-Space-UID")
	}

	// 调用AI代理处理查询
	response, err := h.agent.ProcessNaturalLanguageQuery(c.Request.Context(), &req)
	if err != nil {
		h.logger.WithError(err).Error("Failed to process natural language query")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to process query",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetSuggestions 获取查询建议
func (h *AIAgentHandler) GetSuggestions(c *gin.Context) {
	var req struct {
		Context *QueryContext `json:"context"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.WithError(err).Error("Failed to bind request")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}

	// 设置默认上下文
	if req.Context == nil {
		req.Context = &QueryContext{
			UserID:          c.GetHeader("X-User-ID"),
			PreviousQueries: []QueryRecord{},
			UserPreferences: make(map[string]any),
		}
	}

	// 获取建议
	suggestions, err := h.agent.GenerateQuerySuggestions(c.Request.Context(), req.Context)
	if err != nil {
		h.logger.WithError(err).Error("Failed to generate suggestions")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to generate suggestions",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"suggestions": suggestions,
	})
}

// ExplainResult 解释查询结果
func (h *AIAgentHandler) ExplainResult(c *gin.Context) {
	var req struct {
		Result *QueryResult `json:"result"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.WithError(err).Error("Failed to bind request")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}

	if req.Result == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Result is required",
		})
		return
	}

	// 解释结果
	explanation, err := h.agent.ExplainQueryResult(c.Request.Context(), req.Result)
	if err != nil {
		h.logger.WithError(err).Error("Failed to explain result")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to explain result",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"explanation": explanation,
	})
}

// SubmitFeedback 提交用户反馈
func (h *AIAgentHandler) SubmitFeedback(c *gin.Context) {
	var feedback UserFeedback
	if err := c.ShouldBindJSON(&feedback); err != nil {
		h.logger.WithError(err).Error("Failed to bind request")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}

	// 设置用户ID
	if feedback.UserID == "" {
		feedback.UserID = c.GetHeader("X-User-ID")
	}

	// 验证评分
	if feedback.Rating < 1 || feedback.Rating > 5 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Rating must be between 1 and 5",
		})
		return
	}

	// 提交反馈
	err := h.agent.LearnFromFeedback(c.Request.Context(), &feedback)
	if err != nil {
		h.logger.WithError(err).Error("Failed to submit feedback")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to submit feedback",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Feedback submitted successfully",
	})
}

// HealthCheck 健康检查
func (h *AIAgentHandler) HealthCheck(c *gin.Context) {
	// 检查AI代理状态
	// 这里可以添加更复杂的健康检查逻辑

	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
		"version":   "1.0.0",
	})
}

// QueryWithAIRequest 查询请求结构
type QueryWithAIRequest struct {
	Query     string        `json:"query" binding:"required"`
	SpaceUID  string        `json:"space_uid"`
	Context   *QueryContext `json:"context,omitempty"`
	TimeRange *TimeRange    `json:"time_range,omitempty"`
}

// QueryWithAIResponse 查询响应结构
type QueryWithAIResponse struct {
	Success         bool           `json:"success"`
	Data            any            `json:"data,omitempty"`
	StructuredQuery any            `json:"structured_query,omitempty"`
	Explanation     string         `json:"explanation,omitempty"`
	Suggestions     []string       `json:"suggestions,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	TraceID         string         `json:"trace_id,omitempty"`
	Error           string         `json:"error,omitempty"`
}

// GetSuggestionsRequest 获取建议请求结构
type GetSuggestionsRequest struct {
	Context *QueryContext `json:"context,omitempty"`
}

// GetSuggestionsResponse 获取建议响应结构
type GetSuggestionsResponse struct {
	Success     bool     `json:"success"`
	Suggestions []string `json:"suggestions,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// ExplainResultRequest 解释结果请求结构
type ExplainResultRequest struct {
	Result *QueryResult `json:"result" binding:"required"`
}

// ExplainResultResponse 解释结果响应结构
type ExplainResultResponse struct {
	Success     bool   `json:"success"`
	Explanation string `json:"explanation,omitempty"`
	Error       string `json:"error,omitempty"`
}

// SubmitFeedbackRequest 提交反馈请求结构
type SubmitFeedbackRequest struct {
	QueryID     string `json:"query_id" binding:"required"`
	UserID      string `json:"user_id"`
	Rating      int    `json:"rating" binding:"required,min=1,max=5"`
	Comments    string `json:"comments,omitempty"`
	WasHelpful  bool   `json:"was_helpful"`
	Suggestions string `json:"suggestions,omitempty"`
}

// SubmitFeedbackResponse 提交反馈响应结构
type SubmitFeedbackResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// HealthCheckResponse 健康检查响应结构
type HealthCheckResponse struct {
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
	Version   string `json:"version"`
}
