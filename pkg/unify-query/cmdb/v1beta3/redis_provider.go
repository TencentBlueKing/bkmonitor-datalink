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
	DefaultRedisKeyPrefixResourceDef     = "bkmonitor:cmdb:resource_definition:"
	DefaultRedisKeyPrefixRelationDef     = "bkmonitor:cmdb:relation_definition:"
	DefaultRedisPubSubChannelResourceDef = "bkmonitor:cmdb:resource_definition:notify"
	DefaultRedisPubSubChannelRelationDef = "bkmonitor:cmdb:relation_definition:notify"

	// 内部默认值
	defaultRedisReconnectInterval  = 5 * time.Second
	defaultRedisReconnectMaxRetry  = 10
	defaultRedisScanBatchSize      = 100
)

// RedisSchemaProviderConfig Redis Schema 提供器配置
type RedisSchemaProviderConfig struct {
	// Redis Key 前缀
	KeyPrefixResourceDef string
	KeyPrefixRelationDef string

	// Pub/Sub 通道
	PubSubChannelResourceDef string
	PubSubChannelRelationDef string

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
		KeyPrefixResourceDef:     DefaultRedisKeyPrefixResourceDef,
		KeyPrefixRelationDef:     DefaultRedisKeyPrefixRelationDef,
		PubSubChannelResourceDef: DefaultRedisPubSubChannelResourceDef,
		PubSubChannelRelationDef: DefaultRedisPubSubChannelRelationDef,
		ReconnectInterval:        defaultRedisReconnectInterval,
		ReconnectMaxRetry:        defaultRedisReconnectMaxRetry,
		ScanBatchSize:            defaultRedisScanBatchSize,
		ReloadOnStart:            true,
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
func WithKeyPrefix(resourcePrefix, relationPrefix string) RedisSchemaProviderOption {
	return func(config *RedisSchemaProviderConfig) {
		config.KeyPrefixResourceDef = resourcePrefix
		config.KeyPrefixRelationDef = relationPrefix
	}
}

// WithPubSubChannels 设置 Pub/Sub 通道
func WithPubSubChannels(resourceChannel, relationChannel string) RedisSchemaProviderOption {
	return func(config *RedisSchemaProviderConfig) {
		config.PubSubChannelResourceDef = resourceChannel
		config.PubSubChannelRelationDef = relationChannel
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
		if err := provider.loadAllResourceDefinitions(); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to load resource definitions: %w", err)
		}
		if err := provider.loadAllRelationDefinitions(); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to load relation definitions: %w", err)
		}
	}

	// 启动 Pub/Sub 订阅
	provider.wg.Add(2)
	go provider.subscribeResourceDefinitions()
	go provider.subscribeRelationDefinitions()

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

// loadAllResourceDefinitions 加载所有资源定义
func (rsp *RedisSchemaProvider) loadAllResourceDefinitions() error {
	var err error
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.load_all_resource_definitions")
	defer span.End(&err)

	pattern := rsp.config.KeyPrefixResourceDef + "*"
	span.Set("redis.pattern", pattern)

	// 使用 SCAN 代替 KEYS，避免阻塞 Redis
	var cursor uint64
	var loadedCount, failedCount int

	for {
		keys, nextCursor, scanErr := rsp.client.Scan(ctx, cursor, pattern, int64(rsp.config.ScanBatchSize)).Result()
		if scanErr != nil {
			err = fmt.Errorf("failed to scan resource definition keys: %w", scanErr)
			return err
		}

		for _, key := range keys {
			if loadErr := rsp.loadResourceDefinitionByKey(key); loadErr != nil {
				log.Errorf(ctx, "failed to load resource definition %s: %v", key, loadErr)
				failedCount++
			} else {
				loadedCount++
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	span.Set("loaded_count", loadedCount)
	span.Set("failed_count", failedCount)
	log.Infof(ctx, "loaded %d resource definitions, %d failed", loadedCount, failedCount)
	return nil
}

// loadAllRelationDefinitions 加载所有关联定义
func (rsp *RedisSchemaProvider) loadAllRelationDefinitions() error {
	var err error
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.load_all_relation_definitions")
	defer span.End(&err)

	pattern := rsp.config.KeyPrefixRelationDef + "*"
	span.Set("redis.pattern", pattern)

	// 使用 SCAN 代替 KEYS，避免阻塞 Redis
	var cursor uint64
	var loadedCount, failedCount int

	for {
		keys, nextCursor, scanErr := rsp.client.Scan(ctx, cursor, pattern, int64(rsp.config.ScanBatchSize)).Result()
		if scanErr != nil {
			err = fmt.Errorf("failed to scan relation definition keys: %w", scanErr)
			return err
		}

		for _, key := range keys {
			if loadErr := rsp.loadRelationDefinitionByKey(key); loadErr != nil {
				log.Errorf(ctx, "failed to load relation definition %s: %v", key, loadErr)
				failedCount++
			} else {
				loadedCount++
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	span.Set("loaded_count", loadedCount)
	span.Set("failed_count", failedCount)
	log.Infof(ctx, "loaded %d relation definitions, %d failed", loadedCount, failedCount)
	return nil
}

// loadResourceDefinitionByKey 从 Redis key 加载资源定义
func (rsp *RedisSchemaProvider) loadResourceDefinitionByKey(key string) error {
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.load_resource_definition_by_key")
	var err error
	defer span.End(&err)

	span.Set("redis.key", key)

	data, getErr := rsp.client.Get(ctx, key).Result()
	if getErr != nil {
		if getErr == redis.Nil {
			// Key 不存在，从缓存中删除
			rsp.deleteResourceDefinitionFromCache(key)
			return nil
		}
		err = fmt.Errorf("failed to get resource definition: %w", getErr)
		return err
	}

	var rd ResourceDefinition
	if unmarshalErr := json.Unmarshal([]byte(data), &rd); unmarshalErr != nil {
		err = fmt.Errorf("failed to unmarshal resource definition: %w", unmarshalErr)
		return err
	}

	rsp.mu.Lock()
	cacheKey := makeResourceCacheKey(rd.Namespace, rd.Name)
	rsp.resourceDefinitions[cacheKey] = &rd
	rsp.mu.Unlock()

	span.Set("resource.namespace", rd.Namespace)
	span.Set("resource.name", rd.Name)
	log.Infof(ctx, "loaded resource definition: %s:%s", rd.Namespace, rd.Name)
	return nil
}

// loadRelationDefinitionByKey 从 Redis key 加载关联定义
func (rsp *RedisSchemaProvider) loadRelationDefinitionByKey(key string) error {
	ctx, span := trace.NewSpan(rsp.ctx, "redis_provider.load_relation_definition_by_key")
	var err error
	defer span.End(&err)

	span.Set("redis.key", key)

	data, getErr := rsp.client.Get(ctx, key).Result()
	if getErr != nil {
		if getErr == redis.Nil {
			// Key 不存在，从缓存中删除
			rsp.deleteRelationDefinitionFromCache(key)
			return nil
		}
		err = fmt.Errorf("failed to get relation definition: %w", getErr)
		return err
	}

	var rd RelationDefinition
	if unmarshalErr := json.Unmarshal([]byte(data), &rd); unmarshalErr != nil {
		err = fmt.Errorf("failed to unmarshal relation definition: %w", unmarshalErr)
		return err
	}

	rsp.mu.Lock()
	cacheKey := makeRelationCacheKey(rd.Namespace, rd.Name)
	rsp.relationDefinitions[cacheKey] = &rd
	rsp.mu.Unlock()

	span.Set("relation.namespace", rd.Namespace)
	span.Set("relation.name", rd.Name)
	log.Infof(ctx, "loaded relation definition: %s:%s", rd.Namespace, rd.Name)
	return nil
}

// deleteResourceDefinitionFromCache 从缓存中删除资源定义
func (rsp *RedisSchemaProvider) deleteResourceDefinitionFromCache(redisKey string) {
	// 从 Redis key 提取 namespace 和 name
	// Key 格式: {prefix}namespace:name
	prefix := rsp.config.KeyPrefixResourceDef
	if !strings.HasPrefix(redisKey, prefix) {
		return
	}

	namespaceName := strings.TrimPrefix(redisKey, prefix)
	parts := strings.SplitN(namespaceName, ":", 2)
	if len(parts) != 2 {
		return
	}

	cacheKey := makeResourceCacheKey(parts[0], parts[1])
	rsp.mu.Lock()
	delete(rsp.resourceDefinitions, cacheKey)
	rsp.mu.Unlock()

	log.Infof(rsp.ctx, "deleted resource definition from cache: %s", cacheKey)
}

// deleteRelationDefinitionFromCache 从缓存中删除关联定义
func (rsp *RedisSchemaProvider) deleteRelationDefinitionFromCache(redisKey string) {
	// 从 Redis key 提取 namespace 和 name
	// Key 格式: {prefix}namespace:name
	prefix := rsp.config.KeyPrefixRelationDef
	if !strings.HasPrefix(redisKey, prefix) {
		return
	}

	namespaceName := strings.TrimPrefix(redisKey, prefix)
	parts := strings.SplitN(namespaceName, ":", 2)
	if len(parts) != 2 {
		return
	}

	cacheKey := makeRelationCacheKey(parts[0], parts[1])
	rsp.mu.Lock()
	delete(rsp.relationDefinitions, cacheKey)
	rsp.mu.Unlock()

	log.Infof(rsp.ctx, "deleted relation definition from cache: %s", cacheKey)
}

// subscribeResourceDefinitions 订阅资源定义更新通知
func (rsp *RedisSchemaProvider) subscribeResourceDefinitions() {
	defer rsp.wg.Done()

	retryCount := 0
	for {
		select {
		case <-rsp.ctx.Done():
			return
		default:
		}

		pubsub := rsp.client.Subscribe(rsp.ctx, rsp.config.PubSubChannelResourceDef)
		defer pubsub.Close() // 使用 defer 确保资源释放

		log.Infof(rsp.ctx, "subscribed to resource definition channel: %s", rsp.config.PubSubChannelResourceDef)

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

					// 消息格式: {namespace}:{name}
					log.Debugf(rsp.ctx, "received resource definition update: %s", msg.Payload)

					parts := strings.SplitN(msg.Payload, ":", 2)
					if len(parts) != 2 {
						log.Errorf(rsp.ctx, "invalid message format: %s", msg.Payload)
						continue
					}

					key := rsp.config.KeyPrefixResourceDef + msg.Payload
					if err := rsp.loadResourceDefinitionByKey(key); err != nil {
						log.Errorf(rsp.ctx, "failed to reload resource definition %s: %v", msg.Payload, err)
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
			log.Errorf(rsp.ctx, "max retry count reached for resource definition subscription")
			return
		}

		log.Warnf(rsp.ctx, "resource definition subscription disconnected, retrying in %v (attempt %d/%d)",
			rsp.config.ReconnectInterval, retryCount, rsp.config.ReconnectMaxRetry)

		select {
		case <-rsp.ctx.Done():
			return
		case <-time.After(rsp.config.ReconnectInterval):
			// 继续重试
		}
	}
}

// subscribeRelationDefinitions 订阅关联定义更新通知
func (rsp *RedisSchemaProvider) subscribeRelationDefinitions() {
	defer rsp.wg.Done()

	retryCount := 0
	for {
		select {
		case <-rsp.ctx.Done():
			return
		default:
		}

		pubsub := rsp.client.Subscribe(rsp.ctx, rsp.config.PubSubChannelRelationDef)
		defer pubsub.Close() // 使用 defer 确保资源释放

		log.Infof(rsp.ctx, "subscribed to relation definition channel: %s", rsp.config.PubSubChannelRelationDef)

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

					// 消息格式: {namespace}:{name}
					log.Debugf(rsp.ctx, "received relation definition update: %s", msg.Payload)

					parts := strings.SplitN(msg.Payload, ":", 2)
					if len(parts) != 2 {
						log.Errorf(rsp.ctx, "invalid message format: %s", msg.Payload)
						continue
					}

					key := rsp.config.KeyPrefixRelationDef + msg.Payload
					if err := rsp.loadRelationDefinitionByKey(key); err != nil {
						log.Errorf(rsp.ctx, "failed to reload relation definition %s: %v", msg.Payload, err)
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
			log.Errorf(rsp.ctx, "max retry count reached for relation definition subscription")
			return
		}

		log.Warnf(rsp.ctx, "relation definition subscription disconnected, retrying in %v (attempt %d/%d)",
			rsp.config.ReconnectInterval, retryCount, rsp.config.ReconnectMaxRetry)

		select {
		case <-rsp.ctx.Done():
			return
		case <-time.After(rsp.config.ReconnectInterval):
			// 继续重试
		}
	}
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
