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
	"time"

	"github.com/sirupsen/logrus"
)

// AIAgentConfig AI代理配置
type AIAgentConfig struct {
	// LLM配置
	LLMConfig *LLMConfig `json:"llm_config"`
	// 元数据服务
	MetadataService any `json:"metadata_service"`
	// 日志记录器
	Logger *logrus.Logger `json:"logger"`
	// 查询超时时间
	QueryTimeout time.Duration `json:"query_timeout"`
	// 最大并发查询数
	MaxConcurrentQueries int `json:"max_concurrent_queries"`
	// 缓存配置
	CacheConfig *CacheConfig `json:"cache_config"`
	// 学习配置
	LearningConfig *LearningConfig `json:"learning_config"`
}

// CacheConfig 缓存配置
type CacheConfig struct {
	// 是否启用缓存
	Enabled bool `json:"enabled"`
	// 缓存TTL
	TTL time.Duration `json:"ttl"`
	// 最大缓存大小
	MaxSize int `json:"max_size"`
	// 缓存类型
	Type string `json:"type"` // memory, redis
	// Redis配置
	RedisConfig *RedisConfig `json:"redis_config,omitempty"`
}

// RedisConfig Redis配置
type RedisConfig struct {
	// Redis地址
	Addr string `json:"addr"`
	// 密码
	Password string `json:"password"`
	// 数据库
	DB int `json:"db"`
	// 连接池大小
	PoolSize int `json:"pool_size"`
	// 最小空闲连接数
	MinIdleConns int `json:"min_idle_conns"`
}

// LearningConfig 学习配置
type LearningConfig struct {
	// 是否启用学习
	Enabled bool `json:"enabled"`
	// 学习率
	LearningRate float64 `json:"learning_rate"`
	// 最小样本数
	MinSamples int `json:"min_samples"`
	// 学习间隔
	LearningInterval time.Duration `json:"learning_interval"`
	// 模型保存路径
	ModelPath string `json:"model_path"`
}

// DefaultAIAgentConfig 默认AI代理配置
func DefaultAIAgentConfig() *AIAgentConfig {
	return &AIAgentConfig{
		LLMConfig: &LLMConfig{
			Provider:    "openai",
			Model:       "gpt-3.5-turbo",
			MaxTokens:   1000,
			Temperature: 0.7,
			Timeout:     30 * time.Second,
		},
		QueryTimeout:         30 * time.Second,
		MaxConcurrentQueries: 10,
		CacheConfig: &CacheConfig{
			Enabled: true,
			TTL:     5 * time.Minute,
			MaxSize: 1000,
			Type:    "memory",
		},
		LearningConfig: &LearningConfig{
			Enabled:          true,
			LearningRate:     0.01,
			MinSamples:       10,
			LearningInterval: 1 * time.Hour,
			ModelPath:        "./models",
		},
		Logger: logrus.New(),
	}
}
