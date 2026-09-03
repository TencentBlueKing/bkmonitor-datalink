// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	redactedSecret = "******"

	// RepositoryTypeMySQL 使用 MySQL 保存 Event、Alert 和 AlertLog。
	RepositoryTypeMySQL = "mysql"
	// RepositoryTypeElasticsearch 使用 Elasticsearch 保存 Event、Alert 和 AlertLog。
	RepositoryTypeElasticsearch = "elasticsearch"

	defaultElasticsearchIndexPrefix = "linkd"
	defaultBucketDays               = 7
	defaultPrecreatePastBuckets     = 1
	defaultPrecreateFutureBuckets   = 1
	defaultMaxBucketsPerEntity      = 512
	defaultMaxFutureSkewSeconds     = 300
)

var elasticsearchIndexPrefixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// StorageConfig 描述 Linkd 可使用的基础存储连接。
// 连接是否实际启用由对应职责命令的进程组装决定；配置存在不表示 cleaner 已建立连接。
type StorageConfig struct {
	Repository    string               `yaml:"repository,omitempty"`
	MySQL         *MySQLConfig         `yaml:"mysql,omitempty"`
	Elasticsearch *ElasticsearchConfig `yaml:"elasticsearch,omitempty"`
	Redis         *RedisConfig         `yaml:"redis,omitempty"`
}

// MySQLConfig 描述 MySQL Repository 的结构化连接参数。
type MySQLConfig struct {
	Address  string `yaml:"address"`
	Database string `yaml:"database"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// ElasticsearchConfig 描述 Elasticsearch HTTP 连接与可选认证。
type ElasticsearchConfig struct {
	Addresses   []string `yaml:"addresses"`
	IndexPrefix string   `yaml:"index_prefix"`
	// NumberOfReplicas 非 nil 时写入 index template；零值适用于单节点测试。
	NumberOfReplicas *int                             `yaml:"number_of_replicas,omitempty"`
	TimePartition    ElasticsearchTimePartitionConfig `yaml:"time_partition,omitempty"`
	APIKey           string                           `yaml:"api_key,omitempty"`
	BasicAuth        *BasicAuthConfig                 `yaml:"basic_auth,omitempty"`
}

// ElasticsearchTimePartitionConfig 定义时间桶划分、预创建范围和资源上限。
type ElasticsearchTimePartitionConfig struct {
	EventBucketDays        int `yaml:"event_bucket_days"`
	AlertHistoryBucketDays int `yaml:"alert_history_bucket_days"`
	AlertLogBucketDays     int `yaml:"alert_log_bucket_days"`
	PrecreatePastBuckets   int `yaml:"precreate_past_buckets"`
	PrecreateFutureBuckets int `yaml:"precreate_future_buckets"`
	MaxBucketsPerEntity    int `yaml:"max_buckets_per_entity"`
	MaxFutureSkewSeconds   int `yaml:"max_future_skew_seconds"`
}

// BasicAuthConfig 描述 HTTP Basic authentication。
type BasicAuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// RedisConfig 描述 Redis 连接、可选 ACL 认证和逻辑数据库。
type RedisConfig struct {
	Address  string `yaml:"address"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
	Database int    `yaml:"database"`
}

// WithDefaults 返回补齐非敏感默认值且不共享嵌套配置的副本。
func (c StorageConfig) WithDefaults() StorageConfig {
	normalized := c.clone()
	if normalized.Elasticsearch != nil {
		elasticsearch := normalized.Elasticsearch
		if elasticsearch.IndexPrefix == "" {
			elasticsearch.IndexPrefix = defaultElasticsearchIndexPrefix
		}
		elasticsearch.TimePartition = elasticsearch.TimePartition.WithDefaults()
	}
	return normalized
}

// WithDefaults 返回补齐时间桶默认值的配置。
func (c ElasticsearchTimePartitionConfig) WithDefaults() ElasticsearchTimePartitionConfig {
	if c.EventBucketDays == 0 {
		c.EventBucketDays = defaultBucketDays
	}
	if c.AlertHistoryBucketDays == 0 {
		c.AlertHistoryBucketDays = defaultBucketDays
	}
	if c.AlertLogBucketDays == 0 {
		c.AlertLogBucketDays = defaultBucketDays
	}
	if c.PrecreatePastBuckets == 0 {
		c.PrecreatePastBuckets = defaultPrecreatePastBuckets
	}
	if c.PrecreateFutureBuckets == 0 {
		c.PrecreateFutureBuckets = defaultPrecreateFutureBuckets
	}
	if c.MaxBucketsPerEntity == 0 {
		c.MaxBucketsPerEntity = defaultMaxBucketsPerEntity
	}
	if c.MaxFutureSkewSeconds == 0 {
		c.MaxFutureSkewSeconds = defaultMaxFutureSkewSeconds
	}
	return c
}

// MaxFutureSkew 返回 Event 路由允许的最大未来偏移。
func (c ElasticsearchTimePartitionConfig) MaxFutureSkew() time.Duration {
	return time.Duration(c.MaxFutureSkewSeconds) * time.Second
}

// Validate 校验已经声明的存储连接和可选权威 Repository 选择。
func (c StorageConfig) Validate() error {
	c = c.WithDefaults()
	if c.MySQL == nil && c.Elasticsearch == nil && c.Redis == nil {
		return fmt.Errorf("storage must configure at least one backend")
	}
	switch c.Repository {
	case "":
	case RepositoryTypeMySQL:
		if c.MySQL == nil {
			return fmt.Errorf("storage.mysql is required when storage.repository is %q", c.Repository)
		}
	case RepositoryTypeElasticsearch:
		if c.Elasticsearch == nil {
			return fmt.Errorf("storage.elasticsearch is required when storage.repository is %q", c.Repository)
		}
	default:
		return fmt.Errorf(
			"storage.repository must be one of %q, %q: %q",
			RepositoryTypeMySQL,
			RepositoryTypeElasticsearch,
			c.Repository,
		)
	}
	if c.MySQL != nil {
		if err := c.MySQL.Validate(); err != nil {
			return fmt.Errorf("storage.mysql.%w", err)
		}
	}
	if c.Elasticsearch != nil {
		if err := c.Elasticsearch.Validate(); err != nil {
			return fmt.Errorf("storage.elasticsearch.%w", err)
		}
	}
	if c.Redis != nil {
		if err := c.Redis.Validate(); err != nil {
			return fmt.Errorf("storage.redis.%w", err)
		}
	}
	return nil
}

// Redacted 返回隐藏认证信息且不共享 slice 和嵌套认证对象的副本。
func (c StorageConfig) Redacted() StorageConfig {
	redacted := c.clone()
	if redacted.MySQL != nil {
		if redacted.MySQL.Password != "" {
			redacted.MySQL.Password = redactedSecret
		}
	}
	if redacted.Elasticsearch != nil {
		if redacted.Elasticsearch.APIKey != "" {
			redacted.Elasticsearch.APIKey = redactedSecret
		}
		if redacted.Elasticsearch.BasicAuth != nil {
			if redacted.Elasticsearch.BasicAuth.Password != "" {
				redacted.Elasticsearch.BasicAuth.Password = redactedSecret
			}
		}
	}
	if redacted.Redis != nil {
		if redacted.Redis.Password != "" {
			redacted.Redis.Password = redactedSecret
		}
	}
	return redacted
}

func (c StorageConfig) clone() StorageConfig {
	cloned := c
	if c.MySQL != nil {
		mysql := *c.MySQL
		cloned.MySQL = &mysql
	}
	if c.Elasticsearch != nil {
		elasticsearch := *c.Elasticsearch
		elasticsearch.Addresses = append([]string(nil), c.Elasticsearch.Addresses...)
		if c.Elasticsearch.NumberOfReplicas != nil {
			numberOfReplicas := *c.Elasticsearch.NumberOfReplicas
			elasticsearch.NumberOfReplicas = &numberOfReplicas
		}
		if c.Elasticsearch.BasicAuth != nil {
			basicAuth := *c.Elasticsearch.BasicAuth
			elasticsearch.BasicAuth = &basicAuth
		}
		cloned.Elasticsearch = &elasticsearch
	}
	if c.Redis != nil {
		redis := *c.Redis
		cloned.Redis = &redis
	}
	return cloned
}

// Validate 校验 MySQL 地址和认证目标。
func (c MySQLConfig) Validate() error {
	if err := validateHostPort("address", c.Address); err != nil {
		return err
	}
	if c.Database == "" {
		return fmt.Errorf("database is required")
	}
	if c.Username == "" {
		return fmt.Errorf("username is required")
	}
	return nil
}

// Validate 校验 Elasticsearch URL 与认证方式。
func (c ElasticsearchConfig) Validate() error {
	c.TimePartition = c.TimePartition.WithDefaults()
	if len(c.Addresses) == 0 {
		return fmt.Errorf("addresses must not be empty")
	}
	seen := make(map[string]struct{}, len(c.Addresses))
	for index, address := range c.Addresses {
		if _, exists := seen[address]; exists {
			return fmt.Errorf("addresses[%d] duplicates an earlier address", index)
		}
		seen[address] = struct{}{}
		parsed, err := url.Parse(address)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
			parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" ||
			parsed.Fragment != "" {
			return fmt.Errorf("addresses[%d] must be an http(s) origin URL", index)
		}
	}
	if c.APIKey != "" && c.BasicAuth != nil {
		return fmt.Errorf("api_key and basic_auth are mutually exclusive")
	}
	if c.BasicAuth != nil {
		if c.BasicAuth.Username == "" {
			return fmt.Errorf("basic_auth.username is required")
		}
		if c.BasicAuth.Password == "" {
			return fmt.Errorf("basic_auth.password is required")
		}
	}
	if !elasticsearchIndexPrefixPattern.MatchString(c.IndexPrefix) {
		return fmt.Errorf("index_prefix must contain 1 to 128 lowercase letters, digits, underscores, or hyphens")
	}
	if c.NumberOfReplicas != nil && *c.NumberOfReplicas < 0 {
		return fmt.Errorf("number_of_replicas must not be negative")
	}
	if err := c.TimePartition.Validate(); err != nil {
		return fmt.Errorf("time_partition.%w", err)
	}
	return nil
}

// Validate 校验时间桶周期、预创建范围和管理器资源上限。
func (c ElasticsearchTimePartitionConfig) Validate() error {
	c = c.WithDefaults()
	for name, value := range map[string]int{
		"event_bucket_days":         c.EventBucketDays,
		"alert_history_bucket_days": c.AlertHistoryBucketDays,
		"alert_log_bucket_days":     c.AlertLogBucketDays,
	} {
		if value < 1 || value > 365 {
			return fmt.Errorf("%s must be between 1 and 365", name)
		}
	}
	if c.PrecreatePastBuckets < 0 || c.PrecreatePastBuckets > 16 {
		return fmt.Errorf("precreate_past_buckets must be between 0 and 16")
	}
	if c.PrecreateFutureBuckets < 1 || c.PrecreateFutureBuckets > 16 {
		return fmt.Errorf("precreate_future_buckets must be between 1 and 16")
	}
	if c.MaxBucketsPerEntity < 2 || c.MaxBucketsPerEntity > 4096 {
		return fmt.Errorf("max_buckets_per_entity must be between 2 and 4096")
	}
	if c.MaxFutureSkewSeconds < 0 || c.MaxFutureSkewSeconds > 86400 {
		return fmt.Errorf("max_future_skew_seconds must be between 0 and 86400")
	}
	return nil
}

// Validate 校验 Redis 地址和逻辑数据库编号。
func (c RedisConfig) Validate() error {
	if err := validateHostPort("address", c.Address); err != nil {
		return err
	}
	if c.Database < 0 {
		return fmt.Errorf("database must not be negative")
	}
	return nil
}

func validateHostPort(name, address string) error {
	if strings.TrimSpace(address) != address || address == "" {
		return fmt.Errorf("%s must be host:port", name)
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return fmt.Errorf("%s must be host:port", name)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s port must be between 1 and 65535", name)
	}
	return nil
}
