// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
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

	"linkd/internal/redisclient"
)

const (
	redactedSecret = "******"

	// RepositoryTypeMySQL 使用 MySQL 保存 Event、Alert 和 AlertLog。
	RepositoryTypeMySQL = "mysql"
	// RepositoryTypeElasticsearch 使用 Elasticsearch 保存 Event、Alert 和 AlertLog。
	RepositoryTypeElasticsearch = "elasticsearch"
	// ElasticsearchTranslogDurabilityRequest 在每次写请求返回前同步 translog。
	ElasticsearchTranslogDurabilityRequest = "request"
	// ElasticsearchTranslogDurabilityAsync 按 Elasticsearch sync_interval 异步同步 translog。
	ElasticsearchTranslogDurabilityAsync = "async"
	// RedisModeStandalone 直连单个 Redis 数据节点。
	RedisModeStandalone = "standalone"
	// RedisModeSentinel 通过 Sentinel 发现并跟随当前 master。
	RedisModeSentinel = "sentinel"

	defaultElasticsearchIndexPrefix                       = "linkd"
	defaultElasticsearchRefreshIntervalSeconds            = 5
	defaultElasticsearchActiveAlertRefreshIntervalSeconds = 5
	defaultElasticsearchAlertLogTranslogDurability        = ElasticsearchTranslogDurabilityAsync
	maxElasticsearchRefreshIntervalSeconds                = 3600
	defaultBucketDays                                     = 7
	defaultPrecreatePastBuckets                           = 1
	defaultPrecreateFutureBuckets                         = 1
	defaultMaxBucketsPerEntity                            = 512
	defaultMaxFutureSkewSeconds                           = 300
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
	// RefreshIntervalSeconds 驱动 Event、Alert History 和 AlertLog 时间桶模板的 refresh_interval。
	RefreshIntervalSeconds int `yaml:"refresh_interval_seconds"`
	// ActiveAlertRefreshIntervalSeconds 同时驱动 Active Alert 模板、现有索引对账和 Recent Alert 缓存 TTL。
	ActiveAlertRefreshIntervalSeconds int `yaml:"active_alert_refresh_interval_seconds"`
	// AlertLogTranslogDurability 配置 AlertLog 索引的 translog durability。
	AlertLogTranslogDurability string `yaml:"alert_log_translog_durability"`
	// NumberOfReplicas 非 nil 时写入 index template；零值适用于单节点测试。
	NumberOfReplicas *int `yaml:"number_of_replicas,omitempty"`
	// NumberOfShards 仅影响之后新建的索引，不能修改已有索引的主分片数。
	NumberOfShards *int                             `yaml:"number_of_shards,omitempty"`
	TimePartition  ElasticsearchTimePartitionConfig `yaml:"time_partition,omitempty"`
	APIKey         string                           `yaml:"api_key,omitempty"`
	BasicAuth      *BasicAuthConfig                 `yaml:"basic_auth,omitempty"`
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

// RedisConfig 描述 Redis 数据节点认证、逻辑数据库和连接发现模式。
type RedisConfig struct {
	Mode     string               `yaml:"mode,omitempty"`
	Address  string               `yaml:"address,omitempty"`
	Username string               `yaml:"username,omitempty"`
	Password string               `yaml:"password,omitempty"`
	Database int                  `yaml:"database"`
	Sentinel *RedisSentinelConfig `yaml:"sentinel,omitempty"`
}

// RedisSentinelConfig 描述 Sentinel seed、master 名称和 Sentinel 自身认证。
// Redis 数据节点认证仍由 RedisConfig.Username 和 RedisConfig.Password 提供。
type RedisSentinelConfig struct {
	MasterName string   `yaml:"master_name"`
	Addresses  []string `yaml:"addresses"`
	Username   string   `yaml:"username,omitempty"`
	Password   string   `yaml:"password,omitempty"`
}

// WithDefaults 返回补齐非敏感默认值且不共享嵌套配置的副本。
func (c StorageConfig) WithDefaults() StorageConfig {
	normalized := c.clone()
	if normalized.Elasticsearch != nil {
		elasticsearch := normalized.Elasticsearch
		if elasticsearch.IndexPrefix == "" {
			elasticsearch.IndexPrefix = defaultElasticsearchIndexPrefix
		}
		if elasticsearch.RefreshIntervalSeconds == 0 {
			elasticsearch.RefreshIntervalSeconds = defaultElasticsearchRefreshIntervalSeconds
		}
		if elasticsearch.ActiveAlertRefreshIntervalSeconds == 0 {
			elasticsearch.ActiveAlertRefreshIntervalSeconds = defaultElasticsearchActiveAlertRefreshIntervalSeconds
		}
		if elasticsearch.AlertLogTranslogDurability == "" {
			elasticsearch.AlertLogTranslogDurability = defaultElasticsearchAlertLogTranslogDurability
		}
		elasticsearch.TimePartition = elasticsearch.TimePartition.WithDefaults()
	}
	if normalized.Redis != nil {
		redis := normalized.Redis.WithDefaults()
		normalized.Redis = &redis
	}
	return normalized
}

// WithDefaults 返回补齐连接模式且不共享 Sentinel 地址列表的副本。
func (c RedisConfig) WithDefaults() RedisConfig {
	normalized := c.clone()
	if normalized.Mode == "" {
		normalized.Mode = RedisModeStandalone
	}
	return normalized
}

// ClientOptions 构造所有 Redis 使用方共享的建连参数。
func (c RedisConfig) ClientOptions() redisclient.Options {
	c = c.WithDefaults()
	options := redisclient.Options{
		Address: c.Address, Username: c.Username, Password: c.Password, Database: c.Database,
	}
	if c.Mode == RedisModeSentinel && c.Sentinel != nil {
		options.Sentinel = &redisclient.SentinelOptions{
			MasterName: c.Sentinel.MasterName,
			Addresses:  append([]string(nil), c.Sentinel.Addresses...),
			Username:   c.Sentinel.Username,
			Password:   c.Sentinel.Password,
		}
	}
	return options
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

// RefreshInterval 返回非 Active 时间桶索引配置的 refresh_interval。
// 零值按默认五秒处理，便于直接构造配置的调用方与 YAML 加载行为一致。
func (c ElasticsearchConfig) RefreshInterval() time.Duration {
	seconds := c.RefreshIntervalSeconds
	if seconds == 0 {
		seconds = defaultElasticsearchRefreshIntervalSeconds
	}
	return time.Duration(seconds) * time.Second
}

// ActiveAlertRefreshInterval 返回 Active Alert 索引配置的 refresh_interval。
// 零值按默认五秒处理，便于直接构造配置的调用方与 YAML 加载行为一致。
func (c ElasticsearchConfig) ActiveAlertRefreshInterval() time.Duration {
	seconds := c.ActiveAlertRefreshIntervalSeconds
	if seconds == 0 {
		seconds = defaultElasticsearchActiveAlertRefreshIntervalSeconds
	}
	return time.Duration(seconds) * time.Second
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
		if redacted.Redis.Sentinel != nil && redacted.Redis.Sentinel.Password != "" {
			redacted.Redis.Sentinel.Password = redactedSecret
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
		redis := c.Redis.clone()
		cloned.Redis = &redis
	}
	return cloned
}

func (c RedisConfig) clone() RedisConfig {
	cloned := c
	if c.Sentinel != nil {
		sentinel := *c.Sentinel
		sentinel.Addresses = append([]string(nil), c.Sentinel.Addresses...)
		cloned.Sentinel = &sentinel
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
	if c.NumberOfShards != nil && (*c.NumberOfShards < 1 || *c.NumberOfShards > 1024) {
		return fmt.Errorf("number_of_shards must be between 1 and 1024")
	}
	durability := c.AlertLogTranslogDurability
	if durability == "" {
		durability = defaultElasticsearchAlertLogTranslogDurability
	}
	if durability != ElasticsearchTranslogDurabilityRequest && durability != ElasticsearchTranslogDurabilityAsync {
		return fmt.Errorf(
			"alert_log_translog_durability must be one of %q, %q: %q",
			ElasticsearchTranslogDurabilityRequest,
			ElasticsearchTranslogDurabilityAsync,
			durability,
		)
	}
	for name, setting := range map[string]struct {
		seconds      int
		defaultValue int
	}{
		"refresh_interval_seconds": {
			seconds: c.RefreshIntervalSeconds, defaultValue: defaultElasticsearchRefreshIntervalSeconds,
		},
		"active_alert_refresh_interval_seconds": {
			seconds: c.ActiveAlertRefreshIntervalSeconds, defaultValue: defaultElasticsearchActiveAlertRefreshIntervalSeconds,
		},
	} {
		seconds := setting.seconds
		if seconds == 0 {
			seconds = setting.defaultValue
		}
		if seconds < 1 || seconds > maxElasticsearchRefreshIntervalSeconds {
			return fmt.Errorf("%s must be between 1 and %d", name, maxElasticsearchRefreshIntervalSeconds)
		}
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

// Validate 校验 Redis 连接模式、地址和逻辑数据库编号。
func (c RedisConfig) Validate() error {
	c = c.WithDefaults()
	switch c.Mode {
	case RedisModeStandalone:
		if c.Sentinel != nil {
			return fmt.Errorf("sentinel must be omitted when mode is %q", RedisModeStandalone)
		}
	case RedisModeSentinel:
		if c.Sentinel == nil {
			return fmt.Errorf("sentinel is required when mode is %q", RedisModeSentinel)
		}
	default:
		return fmt.Errorf("mode must be one of %q, %q: %q", RedisModeStandalone, RedisModeSentinel, c.Mode)
	}
	return c.ClientOptions().Validate()
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
