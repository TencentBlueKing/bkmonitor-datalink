// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	unifyRedis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/go-redis/redis/v8"
)

const (
	// 默认配置常量（导出供测试使用）
	// 新的统一 Entity 格式
	DefaultRedisKeyPrefix        = "bkmonitorv3:entity:"
	DefaultRedisPubSubChannelSuffix = ":channel"

	// 内部默认值
	defaultRedisReconnectInterval = 5 * time.Second
	defaultRedisReconnectMaxRetry = 10
	defaultRedisScanBatchSize     = 100

	// 关联类型 kinds（用于区分 resource 和 relation）
	KindCustomRelationStatus = "CustomRelationStatus"
)

// RedisSchemaProviderConfig Redis Schema 提供器配置
type RedisSchemaProviderConfig struct {
	// Redis Key 前缀: bkmonitorv3:entity:
	KeyPrefix string

	// Pub/Sub 通道后缀: :channel
	PubSubChannelSuffix string

	// 关联类型 kinds 集合（用于区分 resource 和 relation）
	RelationKinds map[string]bool

	// 重连配置
	ReconnectInterval time.Duration
	ReconnectMaxRetry int

	// SCAN 批次大小
	ScanBatchSize uint64

	// 是否在启动时重新加载所有数据
	ReloadOnStart bool
}

// DefaultRedisSchemaProviderConfig 返回默认配置
func DefaultRedisSchemaProviderConfig() *RedisSchemaProviderConfig {
	return &RedisSchemaProviderConfig{
		KeyPrefix:           DefaultRedisKeyPrefix,
		PubSubChannelSuffix: DefaultRedisPubSubChannelSuffix,
		RelationKinds: map[string]bool{
			KindCustomRelationStatus: true, // 关联类型
		},
		ReconnectInterval: defaultRedisReconnectInterval,
		ReconnectMaxRetry: defaultRedisReconnectMaxRetry,
		ScanBatchSize:     defaultRedisScanBatchSize,
		ReloadOnStart:     true,
	}
}

// RedisSchemaProvider Redis Schema 提供器
// 从 Redis 动态加载资源和关联定义，支持 Pub/Sub 热更新
type RedisSchemaProvider struct {
	client              redis.UniversalClient // 支持 UniversalClient 接口
	config              *RedisSchemaProviderConfig
	resourceDefinitions map[string]*ResourceDefinition // key: namespace:name
	relationDefinitions map[string]*RelationDefinition // key: namespace:name
	mu                  sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// RedisSchemaProviderOption Redis Schema 提供器配置选项
type RedisSchemaProviderOption func(*RedisSchemaProviderConfig)

// WithReloadOnStart 设置启动时是否重新加载所有数据
func WithReloadOnStart(reload bool) RedisSchemaProviderOption {
	return func(config *RedisSchemaProviderConfig) {
		config.ReloadOnStart = reload
	}
}

// WithKeyPrefix 设置 Redis Key 前缀
func WithKeyPrefix(prefix string) RedisSchemaProviderOption {
	return func(config *RedisSchemaProviderConfig) {
		config.KeyPrefix = prefix
	}
}

// WithPubSubChannelSuffix 设置 Pub/Sub 通道后缀
func WithPubSubChannelSuffix(suffix string) RedisSchemaProviderOption {
	return func(config *RedisSchemaProviderConfig) {
		config.PubSubChannelSuffix = suffix
	}
}

// WithRelationKinds 设置关联类型 kinds
func WithRelationKinds(kinds []string) RedisSchemaProviderOption {
	return func(config *RedisSchemaProviderConfig) {
		config.RelationKinds = make(map[string]bool)
		for _, kind := range kinds {
			config.RelationKinds[kind] = true
		}
	}
}

// WithReconnectConfig 设置重连配置
func WithReconnectConfig(interval time.Duration, maxRetry int) RedisSchemaProviderOption {
	return func(config *RedisSchemaProviderConfig) {
		config.ReconnectInterval = interval
		config.ReconnectMaxRetry = maxRetry
	}
}

// WithScanBatchSize 设置 SCAN 批次大小
func WithScanBatchSize(size uint64) RedisSchemaProviderOption {
	return func(config *RedisSchemaProviderConfig) {
		config.ScanBatchSize = size
	}
}

// NewRedisSchemaProvider 创建 Redis Schema 提供器（使用自定义客户端）
// 适用于测试或需要自定义 Redis 客户端的场景
func NewRedisSchemaProvider(client redis.UniversalClient, opts ...RedisSchemaProviderOption) (*RedisSchemaProvider, error) {
	// 使用默认配置
	config := DefaultRedisSchemaProviderConfig()

	// 应用配置选项
	for _, opt := range opts {
		opt(config)
	}

	ctx, cancel := context.WithCancel(context.Background())
	provider := &RedisSchemaProvider{
		client:              client,
		config:              config,
		resourceDefinitions: make(map[string]*ResourceDefinition),
		relationDefinitions: make(map[string]*RelationDefinition),
		ctx:                 ctx,
		cancel:              cancel,
	}

	// 初始化加载所有数据
	if config.ReloadOnStart {
		if err := provider.loadAllEntities(); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to load entities: %w", err)
		}
	}

	// 启动 Pub/Sub 订阅
	provider.wg.Add(1)
	go provider.subscribeEntities()

	return provider, nil
}

// NewRedisSchemaProviderWithGlobalClient 创建 Redis Schema 提供器（使用全局客户端）
// 推荐用于生产环境，使用 unify-query 统一的 Redis 客户端管理
func NewRedisSchemaProviderWithGlobalClient(opts ...RedisSchemaProviderOption) (*RedisSchemaProvider, error) {
	client := unifyRedis.Client()
	if client == nil {
		return nil, fmt.Errorf("global redis client is not initialized, please call redis.SetInstance first")
	}
	return NewRedisSchemaProvider(client, opts...)
}

// Close 关闭提供器
func (rsp *RedisSchemaProvider) Close() error {
	rsp.cancel()
	rsp.wg.Wait()
	return nil
}

// loadAllEntities 加载所有实体数据（统一方法）
func (rsp *RedisSchemaProvider) loadAllEntities() error {
	var err error
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.load_all_entities")
	defer span.End(&err)

	pattern := rsp.config.KeyPrefix + "*"
	span.Set("redis.pattern", pattern)

	// 使用 SCAN 扫描所有 entity keys
	var cursor uint64
	var totalLoadedCount, totalFailedCount int

	for {
		keys, nextCursor, scanErr := rsp.client.Scan(ctx, cursor, pattern, int64(rsp.config.ScanBatchSize)).Result()
		if scanErr != nil {
			err = fmt.Errorf("failed to scan entity keys: %w", scanErr)
			return err
		}

		for _, key := range keys {
			loadedCount, failedCount := rsp.loadEntitiesByKey(key)
			totalLoadedCount += loadedCount
			totalFailedCount += failedCount
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	span.Set("total_loaded_count", totalLoadedCount)
	span.Set("total_failed_count", totalFailedCount)
	log.Infof(ctx, "loaded %d entities, %d failed", totalLoadedCount, totalFailedCount)
	return nil
}

// loadEntitiesByKey 从 Redis Hash key 加载所有实体
func (rsp *RedisSchemaProvider) loadEntitiesByKey(key string) (loadedCount int, failedCount int) {
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.load_entities_by_key")
	defer span.End(nil)

	span.Set("redis.key", key)

	// 从 Redis Hash 中获取所有 field-value 对
	data, getErr := rsp.client.HGetAll(ctx, key).Result()
	if getErr != nil {
		log.Errorf(ctx, "failed to get all entities from key %s: %v", key, getErr)
		return 0, 1
	}

	// 提取 kind（从 key 中）
	// key 格式: bkmonitorv3:entity:{kind}
	kind := strings.TrimPrefix(key, rsp.config.KeyPrefix)
	span.Set("entity.kind", kind)

	// 遍历每个 field-value 对
	for field, value := range data {
		// field 格式: {namespace}:{name}
		parts := strings.SplitN(field, ":", 2)
		if len(parts) != 2 {
			log.Errorf(ctx, "invalid field format: %s", field)
			failedCount++
			continue
		}

		namespace, name := parts[0], parts[1]

		// 解析 JSON 数据并根据 kind 分类存储
		if loadErr := rsp.loadEntityByKind(kind, namespace, name, value); loadErr != nil {
			log.Errorf(ctx, "failed to load entity %s:%s:%s: %v", kind, namespace, name, loadErr)
			failedCount++
		} else {
			loadedCount++
		}
	}

	log.Infof(ctx, "loaded %d entities from key %s, %d failed", loadedCount, key, failedCount)
	return loadedCount, failedCount
}

// loadEntityByKind 根据 kind 加载实体到对应的缓存中
func (rsp *RedisSchemaProvider) loadEntityByKind(kind, namespace, name, jsonData string) error {
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.load_entity_by_kind")
	var err error
	defer span.End(&err)

	span.Set("entity.kind", kind)
	span.Set("entity.namespace", namespace)
	span.Set("entity.name", name)

	// 根据 kind 判断是资源还是关联
	isRelation := rsp.config.RelationKinds[kind]
	span.Set("entity.is_relation", isRelation)

	cacheKey := makeResourceCacheKey(namespace, name)

	if isRelation {
		// 解析为关联定义
		var rd RelationDefinition
		if unmarshalErr := json.Unmarshal([]byte(jsonData), &rd); unmarshalErr != nil {
			err = fmt.Errorf("failed to unmarshal relation definition: %w", unmarshalErr)
			return err
		}

		rsp.mu.Lock()
		rsp.relationDefinitions[cacheKey] = &rd
		rsp.mu.Unlock()

		log.Infof(ctx, "loaded relation definition: %s:%s", namespace, name)
	} else {
		// 解析为资源定义
		var rd ResourceDefinition
		if unmarshalErr := json.Unmarshal([]byte(jsonData), &rd); unmarshalErr != nil {
			err = fmt.Errorf("failed to unmarshal resource definition: %w", unmarshalErr)
			return err
		}

		rsp.mu.Lock()
		rsp.resourceDefinitions[cacheKey] = &rd
		rsp.mu.Unlock()

		log.Infof(ctx, "loaded resource definition: %s:%s", namespace, name)
	}

	return nil
}

// deleteEntityFromCache 从缓存中删除实体（统一删除方法）
func (rsp *RedisSchemaProvider) deleteEntityFromCache(kind, namespace, name string) {
	cacheKey := makeResourceCacheKey(namespace, name)
	isRelation := rsp.config.RelationKinds[kind]

	rsp.mu.Lock()
	if isRelation {
		delete(rsp.relationDefinitions, cacheKey)
	} else {
		delete(rsp.resourceDefinitions, cacheKey)
	}
	rsp.mu.Unlock()

	log.Infof(rsp.ctx, "deleted entity from cache: %s:%s:%s", kind, namespace, name)
}

// subscribeEntities 订阅所有实体类型的更新通知（统一订阅方法）
func (rsp *RedisSchemaProvider) subscribeEntities() {
	defer rsp.wg.Done()

	// 订阅所有实体类型的通道
	// 通道格式: bkmonitorv3:entity:{kind}:channel
	// 使用 Redis Pattern Subscribe 订阅所有匹配的通道
	channelPattern := rsp.config.KeyPrefix + "*" + rsp.config.PubSubChannelSuffix

	retryCount := 0
	for {
		select {
		case <-rsp.ctx.Done():
			return
		default:
		}

		pubsub := rsp.client.PSubscribe(rsp.ctx, channelPattern)
		defer pubsub.Close()

		log.Infof(rsp.ctx, "subscribed to entity channels: %s", channelPattern)

		// 使用独立的 goroutine 处理消息，避免阻塞
		done := make(chan struct{})
		go func() {
			defer close(done)
			ch := pubsub.Channel()
			for {
				select {
				case <-rsp.ctx.Done():
					return
				case msg := <-ch:
					if msg == nil {
						return
					}

					// msg.Channel 格式: bkmonitorv3:entity:{kind}:channel
					// msg.Payload 格式: {namespace}:{name}
					log.Debugf(rsp.ctx, "received entity update from channel %s: %s", msg.Channel, msg.Payload)

					// 提取 kind
					kind := strings.TrimPrefix(msg.Channel, rsp.config.KeyPrefix)
					kind = strings.TrimSuffix(kind, rsp.config.PubSubChannelSuffix)

					// 解析 namespace:name
					parts := strings.SplitN(msg.Payload, ":", 2)
					if len(parts) != 2 {
						log.Errorf(rsp.ctx, "invalid message format: %s", msg.Payload)
						continue
					}

					namespace, name := parts[0], parts[1]

					// 从 Redis Hash 重新加载实体数据
					if err := rsp.reloadEntity(kind, namespace, name); err != nil {
						log.Errorf(rsp.ctx, "failed to reload entity %s:%s:%s: %v", kind, namespace, name, err)
					}
				}
			}
		}()

		// 等待消息处理完成或 context 取消
		select {
		case <-rsp.ctx.Done():
			<-done
			return
		case <-done:
			// 连接断开
		}

		retryCount++
		if retryCount > rsp.config.ReconnectMaxRetry {
			log.Errorf(rsp.ctx, "max retry count reached for entity subscription")
			return
		}

		log.Warnf(rsp.ctx, "entity subscription disconnected, retrying in %v (attempt %d/%d)",
			rsp.config.ReconnectInterval, retryCount, rsp.config.ReconnectMaxRetry)

		select {
		case <-rsp.ctx.Done():
			return
		case <-time.After(rsp.config.ReconnectInterval):
			// 继续重试
		}
	}
}

// reloadEntity 重新加载单个实体
func (rsp *RedisSchemaProvider) reloadEntity(kind, namespace, name string) error {
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.reload_entity")
	var err error
	defer span.End(&err)

	span.Set("entity.kind", kind)
	span.Set("entity.namespace", namespace)
	span.Set("entity.name", name)

	// 构建 Redis key 和 field
	redisKey := rsp.config.KeyPrefix + kind
	field := fmt.Sprintf("%s:%s", namespace, name)

	// 从 Redis Hash 获取数据
	jsonData, getErr := rsp.client.HGet(ctx, redisKey, field).Result()
	if getErr != nil {
		if getErr == redis.Nil {
			// 数据已删除，从缓存中移除
			rsp.deleteEntityFromCache(kind, namespace, name)
			return nil
		}
		err = fmt.Errorf("failed to get entity from redis: %w", getErr)
		return err
	}

	// 加载实体到缓存
	return rsp.loadEntityByKind(kind, namespace, name, jsonData)
}

// GetResourceDefinition 获取资源定义
func (rsp *RedisSchemaProvider) GetResourceDefinition(namespace, name string) (*ResourceDefinition, error) {
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.get_resource_definition")
	var err error
	defer span.End(&err)

	span.Set("resource.namespace", namespace)
	span.Set("resource.name", name)

	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	cacheKey := makeResourceCacheKey(namespace, name)
	rd, ok := rsp.resourceDefinitions[cacheKey]
	if !ok {
		err = ErrResourceDefinitionNotFound
		span.Set("cache.hit", false)
		return nil, err
	}

	span.Set("cache.hit", true)
	log.Debugf(ctx, "get resource definition from cache: %s:%s", namespace, name)
	return rd, nil
}

// ListResourceDefinitions 列出资源定义
func (rsp *RedisSchemaProvider) ListResourceDefinitions(namespace string) ([]*ResourceDefinition, error) {
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.list_resource_definitions")
	var err error
	defer span.End(&err)

	span.Set("resource.namespace", namespace)

	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	result := make([]*ResourceDefinition, 0)
	for _, rd := range rsp.resourceDefinitions {
		if rd.Namespace == namespace {
			result = append(result, rd)
		}
	}

	span.Set("result.count", len(result))
	log.Debugf(ctx, "list resource definitions for namespace %s: found %d", namespace, len(result))
	return result, nil
}

// GetRelationDefinition 获取关联定义
func (rsp *RedisSchemaProvider) GetRelationDefinition(namespace, name string) (*RelationDefinition, error) {
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.get_relation_definition")
	var err error
	defer span.End(&err)

	span.Set("relation.namespace", namespace)
	span.Set("relation.name", name)

	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	cacheKey := makeRelationCacheKey(namespace, name)
	rd, ok := rsp.relationDefinitions[cacheKey]
	if !ok {
		err = ErrRelationDefinitionNotFound
		span.Set("cache.hit", false)
		return nil, err
	}

	span.Set("cache.hit", true)
	log.Debugf(ctx, "get relation definition from cache: %s:%s", namespace, name)
	return rd, nil
}

// ListRelationDefinitions 列出关联定义
func (rsp *RedisSchemaProvider) ListRelationDefinitions(namespace string) ([]*RelationDefinition, error) {
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.list_relation_definitions")
	var err error
	defer span.End(&err)

	span.Set("relation.namespace", namespace)

	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	result := make([]*RelationDefinition, 0)
	for _, rd := range rsp.relationDefinitions {
		if rd.Namespace == namespace {
			result = append(result, rd)
		}
	}

	span.Set("result.count", len(result))
	log.Debugf(ctx, "list relation definitions for namespace %s: found %d", namespace, len(result))
	return result, nil
}

// GetResourcePrimaryKeys 获取资源主键字段列表
func (rsp *RedisSchemaProvider) GetResourcePrimaryKeys(resourceType ResourceType) []string {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	// 遍历所有资源定义，查找匹配的资源类型
	for _, rd := range rsp.resourceDefinitions {
		if rd.ToResourceType() == resourceType {
			return rd.GetPrimaryKeys()
		}
	}

	return []string{}
}

// GetRelationSchema 获取关联 Schema
func (rsp *RedisSchemaProvider) GetRelationSchema(relationType RelationType) (*RelationSchema, error) {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	// 遍历所有关联定义，查找匹配的关联类型
	for _, rd := range rsp.relationDefinitions {
		if rd.ToRelationType() == relationType {
			schema := rd.ToRelationSchema()
			return &schema, nil
		}
	}

	return nil, ErrRelationDefinitionNotFound
}

// ListRelationSchemas 列出所有关联 Schema
func (rsp *RedisSchemaProvider) ListRelationSchemas() []RelationSchema {
	rsp.mu.RLock()
	defer rsp.mu.RUnlock()

	result := make([]RelationSchema, 0, len(rsp.relationDefinitions))
	for _, rd := range rsp.relationDefinitions {
		result = append(result, rd.ToRelationSchema())
	}

	return result
}

// Helper functions

// makeResourceCacheKey 生成资源定义缓存 key
func makeResourceCacheKey(namespace, name string) string {
	return fmt.Sprintf("%s:%s", namespace, name)
}

// makeRelationCacheKey 生成关联定义缓存 key
func makeRelationCacheKey(namespace, name string) string {
	return fmt.Sprintf("%s:%s", namespace, name)
}

// Ensure RedisSchemaProvider implements SchemaProvider
var _ SchemaProvider = (*RedisSchemaProvider)(nil)
