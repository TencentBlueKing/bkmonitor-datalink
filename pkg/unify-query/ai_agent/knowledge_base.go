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
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// KnowledgeBase 知识库
type KnowledgeBase struct {
	// 元数据知识
	metadataKnowledge map[string]*MetadataInfo
	// 查询模式库
	queryPatterns map[string]*QueryPattern
	// 用户历史查询
	userHistory map[string][]QueryRecord
	// 用户偏好
	userPreferences map[string]*UserPreference
	// 问题查询记录
	problematicQueries map[string]bool
	// 互斥锁
	mutex sync.RWMutex
	// 日志记录器
	logger *logrus.Logger
}

// MetadataInfo 元数据信息
type MetadataInfo struct {
	TableID     string    `json:"table_id"`
	TableName   string    `json:"table_name"`
	Description string    `json:"description"`
	Metrics     []string  `json:"metrics"`
	Tags        []string  `json:"tags"`
	Fields      []string  `json:"fields"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// QueryPattern 查询模式
type QueryPattern struct {
	Pattern     string              `json:"pattern"`
	Intent      string              `json:"intent"`
	Entities    map[string]any      `json:"entities"`
	Query       *structured.QueryTs `json:"query"`
	SuccessRate float64             `json:"success_rate"`
	UsageCount  int                 `json:"usage_count"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

// UserPreference 用户偏好
type UserPreference struct {
	UserID           string         `json:"user_id"`
	PreferredMetrics []string       `json:"preferred_metrics"`
	PreferredTables  []string       `json:"preferred_tables"`
	QueryHistory     []QueryRecord  `json:"query_history"`
	FeedbackHistory  []UserFeedback `json:"feedback_history"`
	CustomSettings   map[string]any `json:"custom_settings"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// NewKnowledgeBase 创建知识库
func NewKnowledgeBase() *KnowledgeBase {
	return &KnowledgeBase{
		metadataKnowledge:  make(map[string]*MetadataInfo),
		queryPatterns:      make(map[string]*QueryPattern),
		userHistory:        make(map[string][]QueryRecord),
		userPreferences:    make(map[string]*UserPreference),
		problematicQueries: make(map[string]bool),
		logger:             logrus.New(),
	}
}

// RecordQuery 记录查询
func (kb *KnowledgeBase) RecordQuery(ctx context.Context, userID, query string, structuredQuery *structured.QueryTs, success bool) error {
	kb.mutex.Lock()
	defer kb.mutex.Unlock()

	// 记录用户查询历史
	record := QueryRecord{
		Query:     query,
		Timestamp: time.Now(),
		Success:   success,
		Duration:  0, // 这里可以添加实际执行时间
	}

	if kb.userHistory[userID] == nil {
		kb.userHistory[userID] = make([]QueryRecord, 0)
	}
	kb.userHistory[userID] = append(kb.userHistory[userID], record)

	// 限制历史记录数量
	if len(kb.userHistory[userID]) > 100 {
		kb.userHistory[userID] = kb.userHistory[userID][len(kb.userHistory[userID])-100:]
	}

	// 更新查询模式
	patternKey := kb.generatePatternKey(query)
	if pattern, exists := kb.queryPatterns[patternKey]; exists {
		pattern.UsageCount++
		if success {
			pattern.SuccessRate = (pattern.SuccessRate*float64(pattern.UsageCount-1) + 1.0) / float64(pattern.UsageCount)
		} else {
			pattern.SuccessRate = (pattern.SuccessRate * float64(pattern.UsageCount-1)) / float64(pattern.UsageCount)
		}
		pattern.UpdatedAt = time.Now()
	} else {
		kb.queryPatterns[patternKey] = &QueryPattern{
			Pattern:     query,
			Intent:      "", // 这里可以添加意图识别
			Entities:    make(map[string]any),
			Query:       structuredQuery,
			SuccessRate: 1.0,
			UsageCount:  1,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
	}

	return nil
}

// UpdateUserPreference 更新用户偏好
func (kb *KnowledgeBase) UpdateUserPreference(ctx context.Context, userID string, feedback *UserFeedback) error {
	kb.mutex.Lock()
	defer kb.mutex.Unlock()

	if kb.userPreferences[userID] == nil {
		kb.userPreferences[userID] = &UserPreference{
			UserID:           userID,
			PreferredMetrics: make([]string, 0),
			PreferredTables:  make([]string, 0),
			QueryHistory:     make([]QueryRecord, 0),
			FeedbackHistory:  make([]UserFeedback, 0),
			CustomSettings:   make(map[string]any),
			UpdatedAt:        time.Now(),
		}
	}

	// 添加反馈历史
	kb.userPreferences[userID].FeedbackHistory = append(kb.userPreferences[userID].FeedbackHistory, *feedback)

	// 限制反馈历史数量
	if len(kb.userPreferences[userID].FeedbackHistory) > 50 {
		kb.userPreferences[userID].FeedbackHistory = kb.userPreferences[userID].FeedbackHistory[len(kb.userPreferences[userID].FeedbackHistory)-50:]
	}

	// 根据反馈更新偏好
	if feedback.Rating >= 4 {
		// 高评分，可以用于学习用户偏好
		// 这里可以添加更复杂的偏好学习逻辑
	}

	kb.userPreferences[userID].UpdatedAt = time.Now()

	return nil
}

// MarkQueryAsProblematic 标记查询为问题查询
func (kb *KnowledgeBase) MarkQueryAsProblematic(ctx context.Context, queryID string) error {
	kb.mutex.Lock()
	defer kb.mutex.Unlock()

	kb.problematicQueries[queryID] = true
	return nil
}

// UpdateQueryPattern 更新查询模式
func (kb *KnowledgeBase) UpdateQueryPattern(ctx context.Context, queryID, suggestions string) error {
	kb.mutex.Lock()
	defer kb.mutex.Unlock()

	// 这里可以添加更新查询模式的逻辑
	// 例如：根据建议优化查询模式
	return nil
}

// GetUserPreference 获取用户偏好
func (kb *KnowledgeBase) GetUserPreference(ctx context.Context, userID string) (*UserPreference, error) {
	kb.mutex.RLock()
	defer kb.mutex.RUnlock()

	preference, exists := kb.userPreferences[userID]
	if !exists {
		return &UserPreference{
			UserID:           userID,
			PreferredMetrics: make([]string, 0),
			PreferredTables:  make([]string, 0),
			QueryHistory:     make([]QueryRecord, 0),
			FeedbackHistory:  make([]UserFeedback, 0),
			CustomSettings:   make(map[string]any),
			UpdatedAt:        time.Now(),
		}, nil
	}

	return preference, nil
}

// GetQueryPatterns 获取查询模式
func (kb *KnowledgeBase) GetQueryPatterns(ctx context.Context, intent string) ([]*QueryPattern, error) {
	kb.mutex.RLock()
	defer kb.mutex.RUnlock()

	var patterns []*QueryPattern
	for _, pattern := range kb.queryPatterns {
		if intent == "" || pattern.Intent == intent {
			patterns = append(patterns, pattern)
		}
	}

	return patterns, nil
}

// IsSlowQuery 判断是否为慢查询
func (kb *KnowledgeBase) IsSlowQuery(query *structured.QueryTs) bool {
	// 这里可以添加慢查询判断逻辑
	// 例如：检查查询复杂度、时间范围等
	return false
}

// HasHighCardinality 判断是否有高基数标签
func (kb *KnowledgeBase) HasHighCardinality(query *structured.QueryTs) bool {
	// 这里可以添加高基数标签判断逻辑
	// 例如：检查查询中的标签数量
	return false
}

// GetMetadataInfo 获取元数据信息
func (kb *KnowledgeBase) GetMetadataInfo(ctx context.Context, tableID string) (*MetadataInfo, error) {
	kb.mutex.RLock()
	defer kb.mutex.RUnlock()

	info, exists := kb.metadataKnowledge[tableID]
	if !exists {
		return nil, errors.Errorf("metadata info not found for table: %s", tableID)
	}

	return info, nil
}

// UpdateMetadataInfo 更新元数据信息
func (kb *KnowledgeBase) UpdateMetadataInfo(ctx context.Context, info *MetadataInfo) error {
	kb.mutex.Lock()
	defer kb.mutex.Unlock()

	info.UpdatedAt = time.Now()
	kb.metadataKnowledge[info.TableID] = info
	return nil
}

// generatePatternKey 生成模式键
func (kb *KnowledgeBase) generatePatternKey(query string) string {
	// 这里可以添加更复杂的模式键生成逻辑
	// 例如：基于查询的语义相似性生成键
	return query
}

// Cleanup 清理过期数据
func (kb *KnowledgeBase) Cleanup(ctx context.Context) error {
	kb.mutex.Lock()
	defer kb.mutex.Unlock()

	now := time.Now()
	cutoff := now.Add(-30 * 24 * time.Hour) // 30天前

	// 清理过期的查询模式
	for key, pattern := range kb.queryPatterns {
		if pattern.UpdatedAt.Before(cutoff) {
			delete(kb.queryPatterns, key)
		}
	}

	// 清理过期的用户历史
	for userID, history := range kb.userHistory {
		var filteredHistory []QueryRecord
		for _, record := range history {
			if record.Timestamp.After(cutoff) {
				filteredHistory = append(filteredHistory, record)
			}
		}
		kb.userHistory[userID] = filteredHistory
	}

	return nil
}
